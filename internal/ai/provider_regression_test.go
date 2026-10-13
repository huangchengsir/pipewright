package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

func TestIssue189StructuredDeepSeekRequestCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name, provider, base, model string
		disabled                    bool
	}{
		{"openai", ProviderOpenAI, "https://api.deepseek.com", "deepseek-v4-pro", true},
		{"openai-v1", ProviderOpenAI, "https://api.deepseek.com/v1", "deepseek-flash", true},
		{"anthropic", ProviderClaude, "https://api.deepseek.com/anthropic", "deepseek-v4-pro", true},
		{"explicit-reasoner", ProviderOpenAI, "https://api.deepseek.com", "deepseek-reasoner", false},
		{"gateway", ProviderOpenAI, "https://gateway.example/v1", "deepseek-v4-pro", false},
		{"custom-path", ProviderOpenAI, "https://api.deepseek.com/custom/v1", "deepseek-v4-pro", false},
		{"lookalike", ProviderOpenAI, "https://api.deepseek.com.example/v1", "deepseek-v4-pro", false},
	} {
		for _, limit := range []int{generateMaxTokens, diagnoseMaxTokens, completeMaxTokens} {
			t.Run(fmt.Sprintf("%s/%d", tc.name, limit), func(t *testing.T) {
				calls := 0
				m := compatibleMockModel(t, tc.base, opsTransport(func(r *http.Request) (*http.Response, error) {
					calls++
					var body map[string]json.RawMessage
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					_, disabled := body["thinking"]
					if disabled != tc.disabled || disabled && string(body["thinking"]) != `{"type":"disabled"}` {
						t.Fatal("unexpected reasoning mode", string(body["thinking"]))
					}
					_, bounded := body["max_tokens"]
					if bounded != (tc.provider == ProviderClaude || tc.disabled) || bounded && string(body["max_tokens"]) != fmt.Sprint(limit) {
						t.Fatal("unexpected token budget", string(body["max_tokens"]))
					}
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(wrapProviderResponse(tc.provider, "reply"))), Request: r}, nil
				}))
				text, err := m.chatWithTokens(ctx(), tc.provider, tc.base, tc.model, "mock-key", "public fixture", limit)
				if err != nil || text != "reply" || calls != 1 {
					t.Fatalf("text=%q err=%v calls=%d", text, err, calls)
				}
			})
		}
	}
}

func TestIssue189StructuredReplyRejectsTruncatedCommand(t *testing.T) {
	for _, tc := range []struct {
		provider, base, response string
	}{
		{ProviderOpenAI, "https://api.deepseek.com", `{"choices":[{"message":{"content":"docker run --name demo"},"finish_reason":"length"}]}`},
		{ProviderClaude, "https://api.deepseek.com/anthropic", `{"content":[{"type":"text","text":"docker run --name demo"}],"stop_reason":"max_tokens"}`},
		{ProviderOllama, "https://ollama.example", `{"message":{"content":"docker run --name demo"},"done_reason":"length"}`},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			calls := 0
			m := compatibleMockModel(t, tc.base, opsTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.response)), Request: r}, nil
			}))
			text, err := m.chatWithTokens(ctx(), tc.provider, tc.base, "deepseek-v4-pro", "mock-key", "complete docker run", completeMaxTokens)
			if !errors.Is(err, ErrGenerateFailed) || !strings.Contains(err.Error(), "长度限制") || text != "" || calls != 1 {
				t.Fatalf("partial command accepted: text=%q err=%v calls=%d", text, err, calls)
			}
		})
	}
}
