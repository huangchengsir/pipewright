package ai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/releaseguard"
)

func TestReleaseGuardModelExplicitSnapshotNoTools(t *testing.T) {
	var requests atomic.Int64
	key := "guard-fixture-provider-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+key {
			t.Error("provider authentication not preserved")
		}
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), key) || strings.Contains(string(body), `"tools"`) {
			t.Error("key or tool catalogue leaked into request")
		}
		var input struct {
			Messages  []struct{ Content string } `json:"messages"`
			MaxTokens int                        `json:"max_tokens"`
		}
		if json.Unmarshal(body, &input) != nil || len(input.Messages) != 1 || input.MaxTokens != 4096 || !strings.Contains(input.Messages[0].Content, "frozen") || !strings.Contains(input.Messages[0].Content, "E1") {
			t.Error("unbounded or incorrect evidence envelope")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": `{"summary":"` + key + `","candidates":[]}`}}}})
	}))
	defer server.Close()
	svc, _, _ := newService(t, server.Client())
	if _, err := svc.Save(context.Background(), SaveInput{Provider: ProviderOpenAI, BaseURL: server.URL, Model: "fixture", APIKey: &key, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	m := NewReleaseGuardModel(svc, mask.NewMasker())
	info, err := m.Info(context.Background())
	if err != nil || info.ConfigHash == "" || info.Destination != server.URL || requests.Load() != 0 {
		t.Fatal(info, err, requests.Load())
	}
	text, err := m.Explain(context.Background(), info, `{"evidenceId":"E1","untrusted":"`+key+`"}`)
	if err != nil || requests.Load() != 1 || strings.Contains(text, key) || !strings.Contains(text, "[MASKED]") {
		t.Fatal(text, err, requests.Load())
	}
	if _, err = svc.Save(context.Background(), SaveInput{Provider: ProviderOpenAI, BaseURL: server.URL, Model: "changed-fixture", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Explain(context.Background(), info, `{"evidenceId":"E1"}`); err == nil || requests.Load() != 1 {
		t.Fatal("provider change sent old consent", err, requests.Load())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fresh, _ := m.Info(context.Background())
	if _, err = m.Explain(ctx, fresh, "{}"); err == nil || requests.Load() != 1 {
		t.Fatal("cancelled request dispatched")
	}
}

func TestReleaseGuardModelDisabledIsLocal(t *testing.T) {
	svc, _, _ := newService(t, nil)
	m := NewReleaseGuardModel(svc, mask.NewMasker())
	if _, err := m.Info(context.Background()); err == nil {
		t.Fatal("unconfigured provider accepted")
	}
	if NewReleaseGuardModel(svc, nil) != nil {
		t.Fatal("missing masker accepted")
	}
}

func TestReleaseGuardModelPreflightVersusDispatchedFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		h, ok := w.(http.Hijacker)
		if !ok {
			t.Error("fixture cannot close connection")
			return
		}
		conn, _, err := h.Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}))
	defer server.Close()
	svc, _, _ := newService(t, server.Client())
	key := "fixture-key"
	if _, err := svc.Save(context.Background(), SaveInput{Provider: ProviderOpenAI, BaseURL: server.URL, Model: "fixture", APIKey: &key, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	m := NewReleaseGuardModel(svc, mask.NewMasker()).(*releaseGuardModel)
	info, _ := m.Info(context.Background())
	if _, err := m.Explain(context.Background(), info, "{}"); err == nil || errors.Is(err, releaseguard.ErrNotDispatched) {
		t.Fatal("dispatched transport failure falsely made retryable", err)
	}
	if _, err := svc.Save(context.Background(), SaveInput{Provider: ProviderOpenAI, BaseURL: "https://fixture.invalid", Model: "fixture", APIKey: &key, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	m.ops.lookupIP = func(context.Context, string) ([]net.IPAddr, error) { return nil, errors.New("fixture DNS failure") }
	info, _ = m.Info(context.Background())
	if _, err := m.Explain(context.Background(), info, "{}"); !errors.Is(err, releaseguard.ErrNotDispatched) {
		t.Fatal("local DNS failure became ambiguous dispatch", err)
	}
}

func TestReleaseGuardOllamaOutputTokenBound(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		var body struct {
			Options struct {
				NumPredict int `json:"num_predict"`
			} `json:"options"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.Options.NumPredict != 4096 {
			t.Error("output token bound missing")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]string{"content": `{"summary":"fixture","candidates":[]}`}, "done": true})
	}))
	defer server.Close()
	svc, _, _ := newService(t, server.Client())
	if _, err := svc.Save(context.Background(), SaveInput{Provider: ProviderOllama, BaseURL: server.URL, Model: "fixture", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	m := NewReleaseGuardModel(svc, mask.NewMasker())
	info, err := m.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Explain(context.Background(), info, "{}"); err != nil || requests.Load() != 1 {
		t.Fatal(err, requests.Load())
	}
}
