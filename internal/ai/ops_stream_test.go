package ai

import (
	"context"
	"encoding/json"
	"fmt"
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

func writeOpsFrame(w http.ResponseWriter, provider, text string, done bool) {
	var value any
	switch provider {
	case ProviderOpenAI:
		reason := any(nil)
		if done {
			reason = "stop"
		}
		value = map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]string{"content": text}, "finish_reason": reason}}}
	case ProviderClaude:
		if done {
			fmt.Fprint(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
			w.(http.Flusher).Flush()
			return
		}
		value = map[string]any{"type": "content_block_delta", "delta": map[string]string{"type": "text_delta", "text": text}}
	case ProviderOllama:
		value = map[string]any{"message": map[string]string{"content": text}, "done": done, "done_reason": "stop"}
	}
	raw, _ := json.Marshal(value)
	if provider == ProviderOllama {
		fmt.Fprintln(w, string(raw))
	} else {
		fmt.Fprintf(w, "data: %s\n\n", raw)
		if done && provider == ProviderOpenAI {
			fmt.Fprint(w, "data: [DONE]\n\n")
		}
	}
	w.(http.Flusher).Flush()
}

func TestOpsRealBoundedStreamsNaturalPlanAndAnalysis(t *testing.T) {
	for _, provider := range []string{ProviderOpenAI, ProviderClaude, ProviderOllama} {
		for _, planning := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/plan=%v", provider, planning), func(t *testing.T) {
				gate := make(chan struct{})
				received := make(chan string, 8)
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					var body struct{ Stream bool }
					if json.NewDecoder(r.Body).Decode(&body) != nil || !body.Stream {
						t.Error("not requesting streaming")
					}
					if provider == ProviderOllama {
						w.Header().Set("Content-Type", "application/x-ndjson")
					} else {
						w.Header().Set("Content-Type", "text/event-stream")
					}
					first := "Ready "
					if planning {
						first = `{"text":"Ready `
					}
					writeOpsFrame(w, provider, first, false)
					select {
					case <-gate:
					case <-r.Context().Done():
						return
					}
					for _, part := range []string{"s", "e", "cret-key", " completed"} {
						writeOpsFrame(w, provider, part, false)
					}
					if planning {
						writeOpsFrame(w, provider, `","actions":[{"toolId":"host_ports","args":{},"targetIndexes":[0]}]}`, false)
					}
					writeOpsFrame(w, provider, "", true)
				}))
				defer server.Close()
				svc, _, _ := newService(t, server.Client())
				key := "secret-key"
				if _, err := svc.Save(ctx(), SaveInput{Provider: provider, BaseURL: server.URL, Model: "m", APIKey: &key, Enabled: true}); err != nil {
					t.Fatal(err)
				}
				m := NewOpsModel(svc, mask.NewMasker()).(opschat.StreamModel)
				info := NewOpsModel(svc, mask.NewMasker()).Info()
				completed := make(chan error, 1)
				go func() {
					emit := func(c opschat.StreamChunk) error { received <- c.Text; return nil }
					if planning {
						plan, err := m.PlanStream(ctx(), opsRequest(), emit)
						if err == nil && (len(plan.Actions) != 1 || plan.Text != "Ready [MASKED] completed") {
							err = fmt.Errorf("bad final plan %v", plan)
						}
						completed <- err
					} else {
						text, err := m.AnalyzeStream(ctx(), opschat.AnalysisRequest{Provider: info, Question: "check", Items: []opschat.AnalysisItem{{Status: opschat.Succeeded, Output: "data"}}, MaxResponseBytes: 16 << 10}, emit)
						if err == nil && text != "Ready [MASKED] completed" {
							err = fmt.Errorf("bad final text %s", text)
						}
						completed <- err
					}
				}()
				select {
				case first := <-received:
					if first != "Ready " {
						t.Error(first)
					}
				case <-time.After(2 * time.Second):
					close(gate)
					t.Fatal("no text before final response")
				}
				select {
				case <-completed:
					close(gate)
					t.Fatal("completed before provider finished")
				default:
				}
				close(gate)
				if err := <-completed; err != nil {
					t.Fatal(err)
				}
				close(received)
				out := "Ready "
				for part := range received {
					out += part
				}
				if out != "Ready [MASKED] completed" || requests.Load() != 1 {
					t.Fatal(out, requests.Load())
				}
			})
		}
	}
}

func TestOpsStreamEOFErrorCancellationAndFallbackNoRetry(t *testing.T) {
	for _, mode := range []string{"eof", "error", "limit", "cancel", "fallback"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			cancelled := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				io.Copy(io.Discard, r.Body)
				if mode == "fallback" {
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"choices":[{"message":{"content":"complete"}}]}`)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				writeOpsFrame(w, ProviderOpenAI, "visible ", false)
				switch mode {
				case "error":
					fmt.Fprint(w, "data: {\"error\":{\"message\":\"secret-key\"}}\n\n")
				case "limit":
					writeOpsFrame(w, ProviderOpenAI, strings.Repeat("x", 17000), false)
				case "cancel":
					<-r.Context().Done()
					close(cancelled)
				}
			}))
			defer server.Close()
			svc, _, _ := newService(t, server.Client())
			key := "secret-key"
			svc.Save(ctx(), SaveInput{Provider: ProviderOpenAI, BaseURL: server.URL, Model: "m", APIKey: &key, Enabled: true})
			m := NewOpsModel(svc, mask.NewMasker())
			c, cancel := context.WithCancel(ctx())
			defer cancel()
			out := ""
			fallback := false
			text, err := m.(opschat.StreamModel).AnalyzeStream(c, opschat.AnalysisRequest{Provider: m.Info(), Items: []opschat.AnalysisItem{{Output: "data"}}, MaxResponseBytes: 16 << 10}, func(chunk opschat.StreamChunk) error {
				out += chunk.Text
				fallback = fallback || chunk.Fallback
				if mode == "cancel" {
					cancel()
				}
				return nil
			})
			if mode == "fallback" {
				if err != nil || text != "complete" || !fallback {
					t.Fatal(text, err, fallback)
				}
			} else if err == nil {
				t.Fatal("invalid stream succeeded")
			}
			if calls.Load() != 1 || strings.Contains(out, key) {
				t.Fatal(calls.Load(), out)
			}
			if mode == "cancel" {
				select {
				case <-cancelled:
				case <-time.After(time.Second):
					t.Fatal("stream read not cancelled")
				}
			}
		})
	}
}

func TestPlanTextPrefixUnicodeEscapesAndNoRawAuthority(t *testing.T) {
	raw := `{"actions":[],"text":"你\n\ud83d\ude00!","manualAdvice":"do not stream this"}`
	previous := ""
	for i := 1; i <= len(raw); i++ {
		text := planTextPrefix(raw[:i])
		if !strings.HasPrefix(text, previous) {
			t.Fatal(i, text, previous)
		}
		previous = text
		if strings.Contains(text, "actions") || strings.Contains(text, "manualAdvice") {
			t.Fatal(text)
		}
	}
	if previous != "你\n😀!" {
		t.Fatal(previous)
	}
}
