package ai

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/opschat"
)

var compatibleVendors = []struct {
	name string
	base string
	api  string
}{
	{"gemini", "https://generativelanguage.googleapis.com/v1beta/openai", "https://generativelanguage.googleapis.com/v1beta/openai"},
	{"glm", "https://open.bigmodel.cn/api/paas/v4", "https://open.bigmodel.cn/api/paas/v4"},
	{"glm-coding", "https://open.bigmodel.cn/api/coding/paas/v4", "https://open.bigmodel.cn/api/coding/paas/v4"},
	{"kimi-cn", "https://api.moonshot.cn/v1", "https://api.moonshot.cn/v1"},
	{"kimi-ai", "https://api.moonshot.ai/v1", "https://api.moonshot.ai/v1"},
	{"mimo", "https://api.xiaomimimo.com/v1", "https://api.xiaomimimo.com/v1"},
	{"custom-root", "https://gateway.invalid/prefix", "https://gateway.invalid/prefix/v1"},
	{"custom-versioned", "https://gateway.invalid/prefix/v1", "https://gateway.invalid/prefix/v1"},
	{"custom-gemini-prefix", "https://gateway.invalid/v1beta/openai", "https://gateway.invalid/v1beta/openai/v1"},
	{"custom-glm-prefix", "https://gateway.invalid/api/paas/v4", "https://gateway.invalid/api/paas/v4/v1"},
}

func compatibleMockModel(t *testing.T, base string, transport opsTransport) *opsModel {
	t.Helper()
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	svc, _, _ := newService(t, &http.Client{Transport: transport})
	return &opsModel{service: svc.(*service), masker: mask.NewMasker(), lookupIP: func(_ context.Context, host string) ([]net.IPAddr, error) {
		if host != u.Hostname() {
			t.Fatalf("unexpected DNS destination %q", host)
		}
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}}
}

func TestCompatibleVendorsCatalogProbeGenerateChatAndStream(t *testing.T) {
	for _, vendor := range compatibleVendors {
		for _, slash := range []string{"", "/"} {
			t.Run(vendor.name+slash, func(t *testing.T) {
				catalogs, chats, streams := 0, 0, 0
				base := vendor.base + slash
				m := compatibleMockModel(t, base, opsTransport(func(r *http.Request) (*http.Response, error) {
					deadline, ok := r.Context().Deadline()
					if !ok || time.Until(deadline) > 60*time.Second || r.Header.Get("Authorization") != "Bearer mock-key" || r.Header.Get("x-api-key") != "" {
						t.Error("unbounded request or incorrect compatible authentication")
					}
					body := `{"data":[{"id":"account/model:custom","name":"Custom"},{"id":"a"},{"id":"a"}]}`
					header := http.Header{"Content-Type": {"application/json"}}
					if r.Method == http.MethodGet {
						catalogs++
						if r.URL.String() != vendor.api+"/models" || time.Until(deadline) > 8*time.Second {
							t.Errorf("catalog endpoint %q", r.URL.String())
						}
					} else {
						if r.URL.String() != vendor.api+"/chat/completions" || r.Method != http.MethodPost {
							t.Errorf("inference endpoint %q", r.URL.String())
						}
						var request struct {
							Model  string `json:"model"`
							Stream bool   `json:"stream"`
						}
						if json.NewDecoder(r.Body).Decode(&request) != nil || request.Model != "account/model:custom" {
							t.Error("manual/account model ID changed")
						}
						body = `{"choices":[{"message":{"content":"reply"}}]}`
						if request.Stream {
							streams++
							body = "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"hidden\",\"content\":\"First \"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"reply\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
							header.Set("Content-Type", "text/event-stream")
						} else {
							chats++
						}
					}
					return &http.Response{StatusCode: 200, Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
				}))
				if apiEndpoint(ProviderOpenAI, base, "chat/completions") != vendor.api+"/chat/completions" || apiEndpoint(ProviderOpenAI, base, "models") != vendor.api+"/models" {
					t.Fatal("shared endpoint mismatch")
				}
				res, err := m.models(ctx(), TestInput{Provider: strp(ProviderOpenAI), BaseURL: &base, APIKey: strp("mock-key")})
				if err != nil || !res.OK || len(res.Items) != 2 || res.Items[1].ID != "account/model:custom" {
					t.Fatal(res, err)
				}
				if res := m.probe(ctx(), ProviderOpenAI, base, "mock-key"); !res.OK {
					t.Fatal(res)
				}
				cfg := &Config{Provider: ProviderOpenAI, BaseURL: base, Model: "account/model:custom"}
				text, err := m.chat(ctx(), cfg, "mock-key", "question", 4096)
				if err != nil || text != "reply" {
					t.Fatal(text, err)
				}
				text, err = m.chatWithTokens(ctx(), ProviderOpenAI, base, cfg.Model, "mock-key", "question", 4096)
				if err != nil || text != "reply" {
					t.Fatal("generate/shared chat", text, err)
				}
				var visible strings.Builder
				chunks := 0
				text, err = m.chatStream(ctx(), cfg, "mock-key", "question", 4096, false, func(chunk opschat.StreamChunk) error {
					if chunk.Fallback {
						t.Error("SSE falsely treated as fallback")
					}
					chunks++
					visible.WriteString(chunk.Text)
					return nil
				})
				if err != nil || text != "First reply" || visible.String() != text || chunks < 2 || catalogs != 2 || chats != 2 || streams != 1 {
					t.Fatal(text, err, visible.String(), chunks, catalogs, chats, streams)
				}
			})
		}
	}
}

func TestCompatibleEndpointSpecializationIsNarrow(t *testing.T) {
	for _, base := range []string{
		"http://generativelanguage.googleapis.com/v1beta/openai",
		"https://generativelanguage.googleapis.com:443/v1beta/openai",
		"https://generativelanguage.googleapis.com.evil.invalid/v1beta/openai",
		"https://user@generativelanguage.googleapis.com/v1beta/openai",
		"https://generativelanguage.googleapis.com/v1beta/openai?key=x",
		"https://generativelanguage.googleapis.com/v1beta/openai?",
		"https://generativelanguage.googleapis.com/v1beta/openai#fragment",
		"https://generativelanguage.googleapis.com/v1beta/openai#",
		"https://generativelanguage.googleapis.com/v1beta/%6fpenai",
		"https://generativelanguage.googleapis.com/v1beta/openai/v1",
		"https://generativelanguage.googleapis.com/custom/v1beta/openai",
		"http://open.bigmodel.cn/api/paas/v4",
		"https://open.bigmodel.cn:443/api/paas/v4",
		"https://open.bigmodel.cn.evil.invalid/api/paas/v4",
		"https://user@open.bigmodel.cn/api/paas/v4",
		"https://open.bigmodel.cn/api/paas/v4?key=x",
		"https://open.bigmodel.cn/api/paas/v4#fragment",
		"https://open.bigmodel.cn/api/paas/v4/v1",
		"https://open.bigmodel.cn/api/paas/v4/custom",
		"https://open.bigmodel.cn/custom/api/coding/paas/v4",
	} {
		if directCompatibleBase(base) || apiEndpoint(ProviderOpenAI, base, "chat/completions") != endpointBase(ProviderOpenAI, base)+"/v1/chat/completions" {
			t.Fatal("nonofficial or custom destination rewritten", base)
		}
	}
	for _, base := range []string{"https://generativelanguage.googleapis.com/v1beta/openai", "https://open.bigmodel.cn/api/paas/v4"} {
		if apiEndpoint(ProviderClaude, base, "messages") != base+"/v1/messages" || apiEndpoint(ProviderOllama, base, "chat") != base+"/api/chat" {
			t.Fatal("non-OpenAI protocol changed", base)
		}
	}
	for _, provider := range []string{ProviderOpenAI, ProviderClaude, ProviderOllama} {
		resource, version := "chat/completions", "/v1"
		if provider == ProviderClaude {
			resource = "messages"
		} else if provider == ProviderOllama {
			resource, version = "chat", "/api"
		}
		for _, suffix := range []string{"", version, version + "/"} {
			if apiEndpoint(provider, "http://localhost:1234/gateway"+suffix, resource) != "http://localhost:1234/gateway"+version+"/"+resource {
				t.Fatal("legacy root/versioned prefix changed", provider, suffix)
			}
		}
	}
}

func TestCompatibleVendorsCatalog404IsFailureAndPreservesManualModel(t *testing.T) {
	for _, vendor := range compatibleVendors[:6] {
		t.Run(vendor.name, func(t *testing.T) {
			calls := 0
			m := compatibleMockModel(t, vendor.base, opsTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.String() != vendor.api+"/models" || r.Method != http.MethodGet {
					t.Fatal("catalog failure retried inference or changed destination")
				}
				return &http.Response{StatusCode: 404, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("mock-key")), Request: r}, nil
			}))
			if _, err := m.service.Save(ctx(), SaveInput{Provider: ProviderOpenAI, BaseURL: vendor.base, APIKey: strp("mock-key"), Model: "manual-model"}); err != nil {
				t.Fatal(err)
			}
			res, err := m.models(ctx(), TestInput{})
			if err != nil || res.OK || len(res.Items) != 0 || !strings.Contains(res.Error, "404") || strings.Contains(res.Error, "mock-key") {
				t.Fatal(res, err)
			}
			if res := m.probe(ctx(), ProviderOpenAI, vendor.base, "mock-key"); res.OK || !strings.Contains(res.Error, "404") || strings.Contains(res.Error, "mock-key") {
				t.Fatal(res)
			}
			cfg, err := m.service.Get(ctx())
			if err != nil || cfg.Model != "manual-model" || cfg.BaseURL != vendor.base || calls != 2 {
				t.Fatal("catalog failure mutated manual configuration", cfg, err, calls)
			}
		})
	}
}

func TestCompatibleVendorsSavedKeyDestinationBoundaries(t *testing.T) {
	for _, vendor := range compatibleVendors[:6] {
		t.Run(vendor.name, func(t *testing.T) {
			calls := 0
			m := compatibleMockModel(t, vendor.base, opsTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"a"}]}`)), Request: r}, nil
			}))
			if _, err := m.service.Save(ctx(), SaveInput{Provider: ProviderOpenAI, BaseURL: vendor.base, APIKey: strp("saved-key")}); err != nil {
				t.Fatal(err)
			}
			if res, err := m.models(ctx(), TestInput{BaseURL: strp(vendor.base + "/")}); err != nil || !res.OK || calls != 1 {
				t.Fatal("same base no longer works", res, err, calls)
			}
			changed := vendor.base + "/custom"
			if directCompatibleBase(vendor.base) {
				changed = vendor.base + "/v1"
			}
			gateway, _ := url.Parse(vendor.base)
			gateway.Host = "gateway.invalid"
			for _, in := range []TestInput{
				{BaseURL: &changed},
				{BaseURL: strp(gateway.String())},
				{Provider: strp(ProviderClaude), BaseURL: &vendor.base},
			} {
				if _, err := m.models(ctx(), in); err != ErrAPIKeyRequired {
					t.Fatal("models reused saved key", err)
				}
				if _, err := m.service.Test(ctx(), in); err != ErrAPIKeyRequired {
					t.Fatal("probe reused saved key", err)
				}
			}
			for _, other := range compatibleVendors[:6] {
				if other.base == vendor.base {
					continue
				}
				in := TestInput{BaseURL: &other.base}
				if _, err := m.models(ctx(), in); err != ErrAPIKeyRequired {
					t.Fatal("catalog reused key across vendor/region/plan", other.name, err)
				}
				if _, err := m.service.Test(ctx(), in); err != ErrAPIKeyRequired {
					t.Fatal("probe reused key across vendor/region/plan", other.name, err)
				}
			}
			if _, err := m.service.Save(ctx(), SaveInput{Provider: ProviderOpenAI, BaseURL: changed}); err != ErrAPIKeyRequired || calls != 1 {
				t.Fatal("save reused saved key", err, calls)
			}
		})
	}
}
