package ai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/opschat"
)

func opsRequest() opschat.PlanRequest {
	return opschat.PlanRequest{Messages: []opschat.Message{{Role: "user", Text: "list containers"}}, Targets: []string{"target_0"}, MaxResponseBytes: 32 << 10}
}

func TestOpsProvidersPrivacyAndStrictPlan(t *testing.T) {
	for _, provider := range []string{ProviderClaude, ProviderOpenAI, ProviderOllama} {
		t.Run(provider, func(t *testing.T) {
			key := "provider-secret-key"
			var count atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count.Add(1)
				if provider == ProviderOpenAI && r.Header.Get("Authorization") != "Bearer "+key {
					t.Error("missing auth")
				}
				if provider == ProviderClaude && (r.Header.Get("x-api-key") != key || r.URL.Path != "/v1/messages") {
					t.Error("claude protocol")
				}
				raw, _ := io.ReadAll(r.Body)
				if strings.Contains(string(raw), key) || strings.Contains(string(raw), "malicious-host") {
					t.Error("prompt leaked key or caller catalogue")
				}
				var body struct{ Messages []struct{ Content string } }
				if json.Unmarshal(raw, &body) != nil || len(body.Messages) != 1 || !strings.Contains(body.Messages[0].Content, "target_0") {
					t.Error("bad prompt")
				}
				text := `{"text":"` + key + `","actions":[{"toolId":"docker_containers","args":{},"targetIndexes":[0]}]}`
				switch provider {
				case ProviderClaude:
					_ = json.NewEncoder(w).Encode(map[string]any{"content": []map[string]string{{"type": "text", "text": text}}})
				case ProviderOpenAI:
					_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": text}}}})
				case ProviderOllama:
					_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]string{"content": text}})
				}
			}))
			defer server.Close()
			svc, _, _ := newService(t, server.Client())
			if _, err := svc.Save(ctx(), SaveInput{Provider: provider, BaseURL: server.URL, Model: "test-model", APIKey: &key, Enabled: true}); err != nil {
				t.Fatal(err)
			}
			masker := mask.NewMasker()
			m := NewOpsModel(svc, masker)
			info := m.Info()
			if count.Load() != 0 || info.Provider != provider || len(info.ConfigHash) != 64 {
				t.Fatal(info, count.Load())
			}
			in := opsRequest()
			in.Messages[0].Text += key
			in.Tools = []opschat.Tool{{ID: "malicious-host"}}
			plan, err := m.Plan(ctx(), in)
			if err != nil || plan.Text != "[MASKED]" || len(plan.Actions) != 1 || count.Load() != 1 {
				t.Fatal(plan, err, count.Load())
			}
		})
	}
}

func TestOpsStrictJSONRejectsAuthorityAndDuplicates(t *testing.T) {
	for _, raw := range []string{
		`{"text":"a","text":"b","actions":[]}`,
		`{"text":"a","Text":"b","actions":[]}`,
		`{"text":"a","actions":[{"toolId":"host_ports","ToolID":"host_ports","args":{},"targetIndexes":[0]}]}`,
		`{"text":"a","host":"1.2.3.4","actions":[]}`,
		`{"text":"a","actions":[{"toolId":"host_ports","args":{},"targetIndexes":[0],"risk":"safe"}]}`,
		`{"text":"a","actions":[]} {"text":"b"}`,
		`null`, `[]`, `{"text":"a","actions":[{"toolId":"host_ports","args":{"argv":["rm"]},"targetIndexes":[0]}]}`,
		`{"text":"a","actions":[{"toolId":"docker_logs","args":{"container":"a","container":"b"},"targetIndexes":[0]}]}`,
	} {
		var plan opschat.Plan
		err := opsStrictJSON([]byte(raw), &plan)
		if err == nil {
			for _, action := range plan.Actions {
				err = opsArgs(action.ToolID, action.Args)
				if err != nil {
					break
				}
			}
		}
		if err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	for _, args := range []string{`{"container":"app","shell":"sh"}`, `{"container":null}`, `{"container":"app","lines":1.5}`, `{"container":"app","lines":null}`} {
		if opsArgs("docker_logs", []byte(args)) == nil {
			t.Fatal(args)
		}
	}
}

func TestOpsPromptFinalByteBoundsEscapesAndOverhead(t *testing.T) {
	m := &opsModel{masker: mask.NewMasker()}
	in := opsRequest()
	in.Tools = opschat.Tools()
	in.MaxResponseBytes = 0
	in.Messages = []opschat.Message{{Role: "assistant", Text: strings.Repeat("\x00", 6000)}, {Role: "user", Text: "current request"}}
	prompt, err := m.planPrompt(in)
	if err != nil || len(prompt) > 32<<10 || !strings.Contains(prompt, "current request") || strings.Contains(prompt, `\u0000`) {
		t.Fatal(len(prompt), err)
	}
	in.Messages = []opschat.Message{{Role: "user", Text: strings.Repeat("\x00", 6000)}}
	if _, err := m.planPrompt(in); err != opschat.ErrQuota {
		t.Fatal(err)
	}
	in.Messages[0].Text = ""
	base, err := m.planPrompt(in)
	if err != nil {
		t.Fatal(err)
	}
	in.Messages[0].Text = strings.Repeat("a", (32<<10)-len(base))
	boundary, err := m.planPrompt(in)
	if err != nil || len(boundary) != 32<<10 {
		t.Fatal(len(boundary), err)
	}
	in.Messages[0].Text += "a"
	if _, err := m.planPrompt(in); err != opschat.ErrQuota {
		t.Fatal("instruction overhead", err)
	}
}

type opsTransport func(*http.Request) (*http.Response, error)

func (f opsTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func opsResponse(text string) *http.Response {
	raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": text}}}})
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(raw))), Header: make(http.Header)}
}

func TestOpsAnalysisFrozenConfigRotationAndDisabledRegistration(t *testing.T) {
	key := "old-secret-key"
	var calls atomic.Int32
	svc, db, _ := newService(t, &http.Client{})
	base := "http://127.0.0.1:12345"
	in := SaveInput{Provider: ProviderOpenAI, BaseURL: base, Model: "m", APIKey: &key, Enabled: true}
	if _, err := svc.Save(ctx(), in); err != nil {
		t.Fatal(err)
	}
	masker := mask.NewMasker()
	m := NewOpsModel(svc, masker)
	if _, err := db.Exec("UPDATE ai_config SET updated_at=? WHERE id=1", "2026-10-07T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	old := m.Info()
	newKey := "new-secret-key"
	in.APIKey = &newKey
	if _, err := svc.Save(ctx(), in); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE ai_config SET updated_at=? WHERE id=1", "2026-10-07T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if m.Info().ConfigHash == old.ConfigHash {
		t.Fatal("rotation not detected")
	}
	svc.(*service).client.Transport = opsTransport(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+newKey || r.URL.Host != "127.0.0.1:12345" {
			t.Error("not using approved snapshot")
		}
		// Change the database after snapshot capture: this request must not reload it.
		if _, err := db.Exec("UPDATE ai_config SET base_url=?,model=? WHERE id=1", "http://127.0.0.1:22222", "changed"); err != nil {
			t.Error(err)
		}
		return opsResponse(newKey), nil
	})
	request := opschat.AnalysisRequest{Provider: old, Items: []opschat.AnalysisItem{{CallID: "c", Output: "approved output"}}, MaxResponseBytes: 16 << 10}
	if _, err := m.Analyze(ctx(), request); err != opschat.ErrConsent || calls.Load() != 0 {
		t.Fatal(err, calls.Load())
	}
	request.Provider = m.Info()
	text, err := m.Analyze(ctx(), request)
	if err != nil || text != "[MASKED]" || calls.Load() != 1 {
		t.Fatal(text, err)
	}
	in.Enabled = false
	if _, err := svc.Save(ctx(), in); err != nil {
		t.Fatal(err)
	}
	if err := m.(opschat.SecretRegistrar).RegisterSecrets(ctx()); err != nil || masker.Scrub(newKey) != "[MASKED]" {
		t.Fatal(err)
	}
	if m.Info() != (opschat.ProviderInfo{}) {
		t.Fatal("disabled provider reported available")
	}
	freshMasker := mask.NewMasker()
	if err := NewOpsModel(svc, freshMasker).(opschat.SecretRegistrar).RegisterSecrets(ctx()); err != nil || freshMasker.Scrub(newKey) != "[MASKED]" {
		t.Fatal("disabled key not registered", err)
	}
}

func TestOpsRedirectDoesNotForwardDataOrCredentials(t *testing.T) {
	var destinationCalls atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { destinationCalls.Add(1); w.WriteHeader(200) }))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, 307) }))
	defer source.Close()
	client := source.Client()
	svc, _, _ := newService(t, client)
	key := "secret-redirect-key"
	if _, err := svc.Save(ctx(), SaveInput{Provider: ProviderOpenAI, BaseURL: source.URL, Model: "m", APIKey: &key, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	m := NewOpsModel(svc, mask.NewMasker())
	if _, err := m.Plan(ctx(), opsRequest()); err != ErrGenerateFailed || destinationCalls.Load() != 0 || client.CheckRedirect != nil {
		t.Fatal(err, destinationCalls.Load())
	}
}

func TestOpsOnlyModelDeadlinePreservesLegacyClient(t *testing.T) {
	short := false
	client := &http.Client{Timeout: 8 * time.Second, Transport: opsTransport(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok {
			t.Fatal("missing model deadline")
		}
		remaining := time.Until(deadline)
		if (!short && (remaining < 50*time.Second || remaining > 60*time.Second)) || (short && remaining > time.Second) {
			t.Fatal("model deadline", remaining)
		}
		return opsResponse(`{"text":"ok","actions":[]}`), nil
	})}
	svc, _, _ := newService(t, client)
	key := "model-key"
	if _, err := svc.Save(ctx(), SaveInput{Provider: ProviderOpenAI, BaseURL: "http://127.0.0.1", Model: "m", APIKey: &key, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	m := NewOpsModel(svc, mask.NewMasker())
	if _, err := m.Plan(ctx(), opsRequest()); err != nil {
		t.Fatal(err)
	}
	short = true
	shortCtx, cancel := context.WithTimeout(ctx(), time.Second)
	defer cancel()
	if _, err := m.Plan(shortCtx, opsRequest()); err != nil {
		t.Fatal(err)
	}
	if client.Timeout != 8*time.Second {
		t.Fatal("legacy timeout changed")
	}
}

func TestOpsAddressAndPostMaskBounds(t *testing.T) {
	for _, address := range []string{"file:///tmp/secret", "http://169.254.169.254", "http://0.0.0.0", "http://[fe80::1]", "http://user:pass@127.0.0.1", "http://127.0.0.1?key=secret", "http://127.0.0.1#fragment"} {
		if opsURLAllowed(ctx(), address) {
			t.Fatal(address)
		}
	}
	svc, _, _ := newService(t, &http.Client{Transport: opsTransport(func(*http.Request) (*http.Response, error) { return opsResponse(strings.Repeat("abcd", 100)), nil })})
	key := "abcd"
	if _, err := svc.Save(ctx(), SaveInput{Provider: ProviderOpenAI, BaseURL: "http://127.0.0.1", Model: "m", APIKey: &key, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	m := NewOpsModel(svc, mask.NewMasker()).(*opsModel)
	cfg, secret, _, err := m.snapshot(ctx())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.chat(ctx(), cfg, secret, "prompt", 500); err != ErrGenerateFailed {
		t.Fatal("post-mask expanded output accepted", err)
	}
}

func TestOpsAnalysisEscapedInputAndInstructionQuota(t *testing.T) {
	svc, _, _ := newService(t, &http.Client{Transport: opsTransport(func(*http.Request) (*http.Response, error) { t.Fatal("oversize data sent"); return nil, nil })})
	if _, err := svc.Save(ctx(), SaveInput{Provider: ProviderOllama, Model: "m", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	m := NewOpsModel(svc, mask.NewMasker())
	in := opschat.AnalysisRequest{Provider: m.Info(), Items: []opschat.AnalysisItem{{Output: strings.Repeat("\x00", 6000)}}, MaxResponseBytes: 16 << 10}
	if _, err := m.Analyze(ctx(), in); err != opschat.ErrQuota {
		t.Fatal(err)
	}
	in.Items[0].Output = ""
	raw, _ := json.Marshal(in.Items)
	in.Items[0].Output = strings.Repeat("a", (32<<10)-len(raw))
	if _, err := m.Analyze(ctx(), in); err != opschat.ErrQuota {
		t.Fatal("instruction overhead", err)
	}
}

type opsBrokenReader struct{}

func (opsBrokenReader) Read([]byte) (int, error) { return 0, errors.New("raw key and URL") }
func (opsBrokenReader) Close() error             { return nil }

func TestOpsEnvelopeOutputBoundsAndSanitizedFailures(t *testing.T) {
	for _, mode := range []string{"envelope", "read", "status", "output", "redirect", "transport", "limit"} {
		t.Run(mode, func(t *testing.T) {
			svc, _, _ := newService(t, &http.Client{Transport: opsTransport(func(*http.Request) (*http.Response, error) {
				switch mode {
				case "envelope":
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(strings.Repeat("a", opsEnvelopeBytes+1)))}, nil
				case "read":
					return &http.Response{StatusCode: 200, Body: opsBrokenReader{}}, nil
				case "status", "redirect":
					return &http.Response{StatusCode: 302, Body: io.NopCloser(strings.NewReader("secret"))}, nil
				case "transport":
					return nil, errors.New("raw secret http://host")
				case "limit":
					return opsResponse(`{"text":"longer than limit","actions":[]}`), nil
				default:
					return opsResponse(strings.Repeat("a", (32<<10)+1)), nil
				}
			})})
			key := "secret-key"
			if _, err := svc.Save(ctx(), SaveInput{Provider: ProviderOpenAI, BaseURL: "http://127.0.0.1:12345", Model: "m", APIKey: &key, Enabled: true}); err != nil {
				t.Fatal(err)
			}
			m := NewOpsModel(svc, mask.NewMasker())
			in := opsRequest()
			if mode == "limit" {
				in.MaxResponseBytes = 8
			}
			_, err := m.Plan(context.Background(), in)
			if err != ErrGenerateFailed {
				t.Fatal(err)
			}
		})
	}
}
