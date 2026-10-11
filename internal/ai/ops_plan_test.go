package ai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/opschat"
)

func TestOpsPlanNoTargetsHidesToolCatalog(t *testing.T) {
	m := &opsModel{masker: mask.NewMasker()}
	prompt, err := m.planPrompt(opschat.PlanRequest{Targets: []string{}, Tools: opschat.Tools(), Messages: []opschat.Message{{Role: "user", Text: "Check resources"}}})
	if err != nil || !strings.Contains(prompt, "Return actions as []") || !strings.Contains(prompt, `"tools":null`) || strings.Contains(prompt, `"toolId":"host_resources"`) {
		t.Fatal("empty target prompt exposed tools", err)
	}
}

func TestOpsPlanWholeFenceCompatibilityAndStrictValidation(t *testing.T) {
	valid := `{"text":"Check postgres.","actions":[{"toolId":"docker_inspect","args":{"container":"postgres"},"targetIndexes":[0]}]}`
	cases := []struct {
		name, text string
		valid      bool
	}{
		{"plain", valid, true},
		{"json-fence", "```json\n" + valid + "\n```", true},
		{"bare-fence", "```\n" + valid + "\n```", true},
		{"crlf", " \r\n```JSON\r\n" + valid + "\r\n```\r\n ", true},
		{"prose-prefix", "Here is a plan:\n```json\n" + valid + "\n```", false},
		{"prose-suffix", "```json\n" + valid + "\n```\nDone", false},
		{"missing-close", "```json\n" + valid, false},
		{"unsupported-fence", "```javascript\n" + valid + "\n```", false},
		{"multiple-json", "```json\n" + valid + " {}\n```", false},
		{"duplicate-field", "```json\n" + strings.Replace(valid, `"text":`, `"Text":"other","text":`, 1) + "\n```", false},
		{"authority", "```json\n" + strings.Replace(valid, `"args":{"container":"postgres"}`, `"args":{"container":"postgres","shell":"sh"}`, 1) + "\n```", false},
		{"unknown-tool", "```json\n" + strings.Replace(valid, "docker_inspect", "shell_exec", 1) + "\n```", false},
		{"invalid-target", "```json\n" + strings.Replace(valid, "[0]", "[1]", 1) + "\n```", false},
	}
	for _, provider := range []string{ProviderOpenAI, ProviderClaude, ProviderOllama} {
		t.Run(provider, func(t *testing.T) {
			var response string
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				var request struct{ Stream bool }
				if json.NewDecoder(r.Body).Decode(&request) != nil {
					t.Error("invalid request")
				}
				if request.Stream {
					if provider == ProviderOllama {
						w.Header().Set("Content-Type", "application/x-ndjson")
					} else {
						w.Header().Set("Content-Type", "text/event-stream")
					}
					// Split both opening and closing fences across provider chunks.
					for i := 0; i < len(response); i += 3 {
						writeOpsFrame(w, provider, response[i:min(i+3, len(response))], false)
					}
					writeOpsFrame(w, provider, "", true)
					return
				}
				switch provider {
				case ProviderOpenAI:
					_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": response}}}})
				case ProviderClaude:
					_ = json.NewEncoder(w).Encode(map[string]any{"content": []map[string]string{{"type": "text", "text": response}}})
				case ProviderOllama:
					_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]string{"content": response}})
				}
			}))
			defer server.Close()
			svc, _, _ := newService(t, server.Client())
			key := "test-only-secret"
			if _, err := svc.Save(ctx(), SaveInput{Provider: provider, BaseURL: server.URL, Model: "m", APIKey: &key, Enabled: true}); err != nil {
				t.Fatal(err)
			}
			m := NewOpsModel(svc, mask.NewMasker())
			for _, tc := range cases {
				for _, streaming := range []bool{false, true} {
					name := tc.name + "/buffered"
					if streaming {
						name = tc.name + "/stream"
					}
					t.Run(name, func(t *testing.T) {
						response = tc.text
						before := requests.Load()
						var visible strings.Builder
						var plan opschat.Plan
						var err error
						if streaming {
							plan, err = m.(opschat.StreamModel).PlanStream(context.Background(), opsRequest(), func(chunk opschat.StreamChunk) error { visible.WriteString(chunk.Text); return nil })
						} else {
							plan, err = m.Plan(context.Background(), opsRequest())
						}
						if tc.valid {
							if err != nil || plan.Text != "Check postgres." || len(plan.Actions) != 1 || plan.Actions[0].ToolID != "docker_inspect" {
								t.Fatal(plan, err)
							}
							if streaming && visible.String() != plan.Text {
								t.Fatalf("not natural text: %q", visible.String())
							}
						} else if !errors.Is(err, opschat.ErrInvalid) {
							t.Fatalf("accepted invalid plan: %v %v", plan, err)
						}
						for _, forbidden := range []string{"```", "toolId", "targetIndexes", "shell", key} {
							if strings.Contains(visible.String(), forbidden) {
								t.Fatal("published plan data")
							}
						}
						if requests.Load() != before+1 {
							t.Fatal("provider request retried")
						}
					})
				}
			}
		})
	}
}
