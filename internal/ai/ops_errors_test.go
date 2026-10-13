package ai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/opschat"
)

func assertOpsFailure(t *testing.T, err error, code string, status int) {
	t.Helper()
	var failure *OpsFailure
	if !errors.Is(err, ErrGenerateFailed) || !errors.As(err, &failure) || failure.SafeCode() != code || failure.HTTPStatus != status {
		t.Fatalf("failure = %v, want code=%s status=%d", err, code, status)
	}
	for _, forbidden := range []string{"mock-key", "raw-secret", "https://", "reasoning payload", "finish-secret"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatal("raw provider material escaped", err)
		}
	}
}

func TestOpsFailureOnlyFixedDiagnosticsAndWorkerInterface(t *testing.T) {
	for _, tc := range []struct{ detail, worker string }{
		{"http_auth", "rejected"}, {"rate_limit", "rejected"}, {"context_limit", "rejected"}, {"rejected", "rejected"},
		{"timeout", "timeout"}, {"token_limit", "token_limit"}, {"cancelled", "unavailable"}, {"transport_error", "unavailable"},
		{"invalid_response", "invalid_response"}, {"empty_response", "invalid_response"}, {"incomplete_stream", "invalid_response"}, {"response_limit", "invalid_response"},
	} {
		wrapped := opsFailure(tc.detail, 429)
		var safe interface{ OpsFailureCode() string }
		if !errors.As(wrapped, &safe) || safe.OpsFailureCode() != tc.worker {
			t.Fatal("worker interface mismatch", wrapped)
		}
		if errors.Is(wrapped, context.Canceled) != (tc.detail == "cancelled") || errors.Is(wrapped, context.DeadlineExceeded) != (tc.detail == "timeout") {
			t.Fatal("context identity lost", wrapped)
		}
	}
	for _, status := range []int{-1, 0, 99, 600, 1234567} {
		failure := &OpsFailure{Code: "raw-secret https://secret.invalid", HTTPStatus: status}
		if failure.SafeCode() != "invalid_response" || failure.OpsFailureCode() != "invalid_response" || failure.Error() != "ai: ops model invalid_response" {
			t.Fatal("unsafe diagnostic", failure)
		}
	}
	var empty *OpsFailure
	if empty.OpsFailureCode() != "invalid_response" || empty.Error() != "ai: ops model invalid_response" || empty.Unwrap() != nil {
		t.Fatal("unsafe nil error")
	}
}

func TestOpsSafeHTTPAndTransportFailuresNoRetry(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, tc := range []struct {
			status int
			code   string
		}{{401, "http_auth"}, {403, "http_auth"}, {429, "rate_limit"}, {413, "context_limit"}, {400, "rejected"}, {402, "rejected"}, {503, "rejected"}, {307, "rejected"}} {
			t.Run(tc.code+"/"+http.StatusText(tc.status)+map[bool]string{false: "/chat", true: "/stream"}[streaming], func(t *testing.T) {
				calls := 0
				m := compatibleMockModel(t, "https://api.deepseek.com", opsTransport(func(r *http.Request) (*http.Response, error) {
					calls++
					return &http.Response{StatusCode: tc.status, Header: http.Header{"Location": {"https://raw-secret.invalid"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"raw-secret mock-key"}}`)), Request: r}, nil
				}))
				cfg := &Config{Provider: ProviderOpenAI, BaseURL: "https://api.deepseek.com", Model: "deepseek-v4-pro"}
				var err error
				if streaming {
					_, err = m.chatStream(ctx(), cfg, "mock-key", "question", 4096, false, func(opschat.StreamChunk) error { t.Error("failed request emitted text"); return nil })
				} else {
					_, err = m.chat(ctx(), cfg, "mock-key", "question", 4096)
				}
				assertOpsFailure(t, err, tc.code, tc.status)
				if calls != 1 {
					t.Fatal("billable request retried or redirect followed", calls)
				}
			})
		}
		for _, tc := range []struct {
			name  string
			cause error
			code  string
		}{
			{"cancel", context.Canceled, "cancelled"}, {"timeout", context.DeadlineExceeded, "timeout"}, {"transport", errors.New("raw-secret mock-key https://host.invalid"), "transport_error"},
		} {
			t.Run(tc.name+map[bool]string{false: "/chat", true: "/stream"}[streaming], func(t *testing.T) {
				calls := 0
				m := compatibleMockModel(t, "https://api.deepseek.com", opsTransport(func(*http.Request) (*http.Response, error) { calls++; return nil, tc.cause }))
				cfg := &Config{Provider: ProviderOpenAI, BaseURL: "https://api.deepseek.com", Model: "deepseek-v4-pro"}
				var err error
				if streaming {
					_, err = m.chatStream(ctx(), cfg, "mock-key", "question", 4096, false, func(opschat.StreamChunk) error { return nil })
				} else {
					_, err = m.chat(ctx(), cfg, "mock-key", "question", 4096)
				}
				assertOpsFailure(t, err, tc.code, 0)
				if calls != 1 || (tc.name != "transport" && !errors.Is(err, tc.cause)) {
					t.Fatal(err, calls)
				}
			})
		}
	}
}

func TestOpsSafeStreamFinishDiagnosticsAndReasoningOnly(t *testing.T) {
	for _, tc := range []struct{ name, provider, body, contentType, code string }{
		{"reasoning-length", ProviderOpenAI, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"raw-secret reasoning payload\"}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"length\"}]}\n\ndata: [DONE]\n\n", "text/event-stream", "token_limit"},
		{"claude-max-tokens", ProviderClaude, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"max_tokens\"}}\n\n", "text/event-stream", "token_limit"},
		{"ollama-length", ProviderOllama, "{\"message\":{},\"done\":true,\"done_reason\":\"length\"}\n", "application/x-ndjson", "token_limit"},
		{"unknown-finish", ProviderOpenAI, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"finish-secret\"}]}\n\n", "text/event-stream", "rejected"},
		{"error-event", ProviderOpenAI, "data: {\"error\":{\"message\":\"raw-secret mock-key\"}}\n\n", "text/event-stream", "rejected"},
		{"malformed", ProviderOpenAI, "data: {raw-secret\n\n", "text/event-stream", "invalid_response"},
		{"empty", ProviderOpenAI, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", "text/event-stream", "empty_response"},
		{"missing-end", ProviderOpenAI, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n", "text/event-stream", "incomplete_stream"},
		{"fallback-length", ProviderOpenAI, `{"choices":[{"message":{"content":""},"finish_reason":"length"}]}`, "application/json", "token_limit"},
		{"fallback-empty", ProviderOpenAI, `{"choices":[{"message":{"content":""},"finish_reason":"stop"}]}`, "application/json", "empty_response"},
		{"fallback-malformed", ProviderOpenAI, `{raw-secret`, "application/json", "invalid_response"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, emitted := 0, 0
			m := compatibleMockModel(t, "https://api.deepseek.com", opsTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {tc.contentType}}, Body: io.NopCloser(strings.NewReader(tc.body)), Request: r}, nil
			}))
			text, err := m.chatStream(ctx(), &Config{Provider: tc.provider, BaseURL: "https://api.deepseek.com", Model: "deepseek-v4-pro"}, "mock-key", "question", 4096, false, func(chunk opschat.StreamChunk) error { emitted++; return nil })
			assertOpsFailure(t, err, tc.code, 0)
			if calls != 1 || emitted != 0 || text != "" {
				t.Fatal("failed stream published or retried", calls, emitted, text)
			}
		})
	}
}

func TestOpsDeepSeekThinkingCompatibilityExactDestinationOnly(t *testing.T) {
	for _, tc := range []struct {
		provider, base string
		disabled       bool
	}{
		{ProviderOpenAI, "https://api.deepseek.com", true}, {ProviderOpenAI, "https://api.deepseek.com/", true}, {ProviderOpenAI, "https://api.deepseek.com/v1", true}, {ProviderOpenAI, "https://api.deepseek.com/v1/", true},
		{ProviderClaude, "https://api.deepseek.com/anthropic", true}, {ProviderClaude, "https://api.deepseek.com/anthropic/v1/", true},
		{ProviderOpenAI, "http://api.deepseek.com", false}, {ProviderOpenAI, "https://api.deepseek.com:443/v1", false}, {ProviderOpenAI, "https://api.deepseek.com/custom/v1", false},
		{ProviderOpenAI, "https://api.deepseek.com/v1?key=raw-secret", false}, {ProviderOpenAI, "https://api.deepseek.com/v1?", false}, {ProviderOpenAI, "https://api.deepseek.com/v1#", false}, {ProviderOpenAI, "https://user@api.deepseek.com/v1", false},
		{ProviderClaude, "https://gateway.invalid/anthropic", false}, {ProviderClaude, "https://api.deepseek.com/gateway/anthropic", false}, {ProviderClaude, "https://api.deepseek.com/anthropic?key=raw-secret", false},
		{ProviderOllama, "https://api.deepseek.com", false}, {ProviderOpenAI, "https://api.deepseek.com.evil.invalid", false},
	} {
		for _, model := range []string{"deepseek-v4-pro", "deepseek-flash", "deepseek-reasoner"} {
			body := map[string]any{"max_tokens": 4096}
			opsRequestCompatibility(&Config{Provider: tc.provider, Model: model}, tc.base, body)
			_, disabled := body["thinking"]
			if disabled != (tc.disabled && model != "deepseek-reasoner") || body["max_tokens"] != 4096 {
				t.Fatal(tc, model, body)
			}
			if disabled && body["thinking"].(map[string]string)["type"] != "disabled" {
				t.Fatal(body)
			}
		}
	}
}

func TestOpsDeepSeekThinkingWirePlanAnalysisAndStructuredChat(t *testing.T) {
	for _, provider := range []string{ProviderOpenAI, ProviderClaude} {
		base := "https://api.deepseek.com/v1"
		if provider == ProviderClaude {
			base = "https://api.deepseek.com/anthropic/v1"
		}
		calls := 0
		expectDisabled := true
		m := compatibleMockModel(t, base, opsTransport(func(r *http.Request) (*http.Response, error) {
			calls++
			var body map[string]json.RawMessage
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Fatal("bad request JSON")
			}
			_, disabled := body["thinking"]
			if disabled != expectDisabled {
				t.Fatal("wrong Ops/general reasoning semantics", disabled, expectDisabled)
			}
			if disabled && string(body["thinking"]) != `{"type":"disabled"}` {
				t.Fatal(string(body["thinking"]))
			}
			if string(body["stream"]) == "true" {
				if string(body["max_tokens"]) != "4096" {
					t.Fatal("Ops token budget changed")
				}
				if provider == ProviderOpenAI {
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"raw-secret\"},\"finish_reason\":\"length\"}]}\n\n")), Request: r}, nil
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"max_tokens\"}}\n\n")), Request: r}, nil
			}
			bodyText := `{"choices":[{"message":{"content":"reply"},"finish_reason":"stop"}]}`
			if provider == ProviderClaude {
				bodyText = `{"content":[{"type":"text","text":"reply"}],"stop_reason":"end_turn"}`
			}
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(bodyText)), Request: r}, nil
		}))
		if _, err := m.service.Save(ctx(), SaveInput{Provider: provider, BaseURL: base, Model: "deepseek-v4-pro", APIKey: strp("mock-key"), Enabled: true}); err != nil {
			t.Fatal(err)
		}
		_, err := m.PlanStream(ctx(), opsRequest(), func(opschat.StreamChunk) error { t.Error("reasoning published"); return nil })
		assertOpsFailure(t, err, "token_limit", 0)
		_, err = m.AnalyzeStream(ctx(), opschat.AnalysisRequest{Provider: m.Info(), Question: "original", Items: []opschat.AnalysisItem{{Status: opschat.Succeeded, Output: "local facts"}}, MaxResponseBytes: 16 << 10}, func(opschat.StreamChunk) error { t.Error("reasoning published"); return nil })
		assertOpsFailure(t, err, "token_limit", 0)
		cfg := &Config{Provider: provider, BaseURL: base, Model: "deepseek-v4-pro"}
		if text, err := m.chat(ctx(), cfg, "mock-key", "question", 4096); err != nil || text != "reply" {
			t.Fatal(text, err)
		}
		if text, err := m.chatWithTokens(ctx(), provider, base, cfg.Model, "mock-key", "question", 4096); err != nil || text != "reply" {
			t.Fatal(text, err)
		}
		if calls != 4 {
			t.Fatal("Plan/analysis/chat/general request retried", calls)
		}
	}
}

func TestOpsSafeDeadlineWhileReadingStream(t *testing.T) {
	calls := 0
	m := compatibleMockModel(t, "https://api.deepseek.com", opsTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		reader, writer := io.Pipe()
		go func() { <-r.Context().Done(); writer.CloseWithError(errors.New("raw-secret mock-key timeout")) }()
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: reader, Request: r}, nil
	}))
	c, cancel := context.WithTimeout(ctx(), 10*time.Millisecond)
	defer cancel()
	_, err := m.chatStream(c, &Config{Provider: ProviderOpenAI, BaseURL: "https://api.deepseek.com", Model: "deepseek-v4-pro"}, "mock-key", "question", 4096, false, func(opschat.StreamChunk) error { return nil })
	assertOpsFailure(t, err, "timeout", 0)
	if !errors.Is(err, context.DeadlineExceeded) || calls != 1 {
		t.Fatal(err, calls)
	}
}
