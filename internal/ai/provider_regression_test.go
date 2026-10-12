package ai

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type providerRoundTrip func(*http.Request) (*http.Response, error)

func (f providerRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type providerFailedBody struct{ err error }

func (b providerFailedBody) Read([]byte) (int, error) { return 0, b.err }
func (b providerFailedBody) Close() error             { return nil }

func TestIssue189SlowSuccessfulBody(t *testing.T) {
	for _, feature := range []string{"generate", "diagnose"} {
		t.Run(feature, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					io.WriteString(w, `{"data":[{"id":"test-model"}]}`)
					return
				}
				w.WriteHeader(http.StatusOK)
				io.WriteString(w, "\n\n")
				w.(http.Flusher).Flush()
				select {
				case <-time.After(9 * time.Second):
				case <-r.Context().Done():
					return
				}
				text := stubProposalJSON
				if feature == "diagnose" {
					text = stubDiagnosisJSON
				}
				io.WriteString(w, wrapProviderResponse(ProviderClaude, text))
			}))
			defer server.Close()
			client := server.Client()
			client.Timeout = 8 * time.Second // Matches the production composition root.
			svc, _, _ := newService(t, client)
			configureEnabled(t, svc, ProviderClaude, server.URL+"/anthropic")
			probe, err := svc.Test(context.Background(), TestInput{})
			if err != nil || !probe.OK {
				t.Fatalf("probe: %v %v", probe, err)
			}
			if feature == "generate" {
				result, err := svc.Generate(context.Background(), GenerateInput{Analysis: RepoAnalysis{Cloned: true, Language: "node"}})
				if err != nil || result == nil || len(result.Stages) != 3 {
					t.Fatalf("generate: %v", err)
				}
			} else {
				result, err := svc.Diagnose(context.Background(), DiagnoseInput{FailureLog: failureLogWithSecret, Masker: maskerWithSecret()})
				if err != nil || result.Status != "ready" {
					t.Fatalf("diagnose: %+v %v", result, err)
				}
			}
			if client.Timeout != 8*time.Second {
				t.Fatal("mutated shared client")
			}
		})
	}
}

func TestIssue189BodyFailuresAreNotParseFailures(t *testing.T) {
	for _, provider := range []string{ProviderClaude, ProviderOpenAI, ProviderOllama} {
		for _, failure := range []error{context.DeadlineExceeded, context.Canceled, io.ErrUnexpectedEOF, errors.New("https://secret.example/sk-secret-KEY9")} {
			t.Run(provider+"/"+failure.Error(), func(t *testing.T) {
				calls := 0
				client := &http.Client{Transport: providerRoundTrip(func(r *http.Request) (*http.Response, error) {
					calls++
					deadline, ok := r.Context().Deadline()
					if !ok || time.Until(deadline) > 60*time.Second {
						t.Error("missing inference deadline")
					}
					return &http.Response{StatusCode: 200, Body: providerFailedBody{failure}, Header: make(http.Header)}, nil
				})}
				svc, _, _ := newService(t, client)
				_, err := svc.(*service).chat(context.Background(), provider, "https://192.168.1.10", "test-model", "key", "prompt")
				if !errors.Is(err, ErrGenerateFailed) || strings.Contains(err.Error(), "解析") || strings.Contains(err.Error(), "sk-secret") || strings.Contains(err.Error(), "secret.example") {
					t.Fatalf("unsafe/misclassified: %v", err)
				}
				if (failure == context.Canceled || failure == context.DeadlineExceeded) && !errors.Is(err, failure) {
					t.Fatalf("lost context identity: %v", err)
				}
				if calls != 1 {
					t.Fatalf("requests=%d", calls)
				}
			})
		}
	}
}

func TestIssue189ResponseBoundAndCallerCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	svc, _, _ := newService(t, server.Client())
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	_, err := svc.(*service).chat(ctx, ProviderOpenAI, server.URL, "test-model", "key", "prompt")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline: %v", err)
	}
	ctx, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	_, err = svc.(*service).chat(ctx, ProviderOpenAI, server.URL, "test-model", "key", "prompt")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}

	client := &http.Client{Transport: providerRoundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(strings.Repeat(" ", maxLLMRespBytes+1)))}, nil
	})}
	svc, _, _ = newService(t, client)
	_, err = svc.(*service).chat(context.Background(), ProviderClaude, "http://192.168.1.10", "test-model", "key", "prompt")
	if !errors.Is(err, ErrGenerateFailed) || !strings.Contains(err.Error(), "大小限制") {
		t.Fatalf("limit: %v", err)
	}
}

func TestIssue189ProbeKeepsShortDeadline(t *testing.T) {
	client := &http.Client{Timeout: 8 * time.Second, Transport: providerRoundTrip(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > 8*time.Second {
			t.Error("probe must keep its short deadline")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("{}"))}, nil
	})}
	svc, _, _ := newService(t, client)
	configureEnabled(t, svc, ProviderClaude, "http://192.168.1.10")
	result, err := svc.Test(context.Background(), TestInput{})
	if err != nil || !result.OK {
		t.Fatalf("probe: %v %v", result, err)
	}
}
