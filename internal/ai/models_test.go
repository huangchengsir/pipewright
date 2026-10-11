package ai

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/mask"
)

func deepSeekCatalogModel(t *testing.T, transport opsTransport) *opsModel {
	t.Helper()
	svc, _, _ := newService(t, &http.Client{Transport: transport})
	return &opsModel{service: svc.(*service), masker: mask.NewMasker(), lookupIP: func(_ context.Context, host string) ([]net.IPAddr, error) {
		if host != "api.deepseek.com" {
			t.Fatalf("unexpected DNS destination %q", host)
		}
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}}
}

func TestDeepSeekClaudeCatalogAndProbeKeepMessagesProtocol(t *testing.T) {
	for _, suffix := range []string{"/anthropic", "/anthropic/", "/anthropic/v1", "/anthropic/v1/"} {
		t.Run(suffix, func(t *testing.T) {
			catalogs, messages := 0, 0
			m := deepSeekCatalogModel(t, opsTransport(func(r *http.Request) (*http.Response, error) {
				deadline, ok := r.Context().Deadline()
				if !ok || time.Until(deadline) > 60*time.Second {
					t.Error("missing bounded request deadline")
				}
				body := `{"data":[{"id":"z"},{"id":"a"},{"id":"a"}]}`
				if r.Method == http.MethodGet {
					catalogs++
					if r.URL.String() != "https://api.deepseek.com/models" || r.Header.Get("Authorization") != "Bearer test-key" || r.Header.Get("x-api-key") != "" || r.Header.Get("anthropic-version") != "" || time.Until(deadline) > 8*time.Second {
						t.Error("catalog URL/auth/deadline differs from official OpenAI catalog")
					}
				} else {
					messages++
					if r.URL.String() != "https://api.deepseek.com/anthropic/v1/messages" || r.Header.Get("x-api-key") != "test-key" || r.Header.Get("anthropic-version") != "2023-06-01" || r.Header.Get("Authorization") != "" {
						t.Error("Claude inference changed")
					}
					body = `{"content":[{"type":"text","text":"reply"}]}`
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			}))
			base := "https://api.deepseek.com" + suffix
			res, err := m.models(ctx(), TestInput{Provider: strp(ProviderClaude), BaseURL: &base, APIKey: strp("test-key")})
			if err != nil || !res.OK || len(res.Items) != 2 || res.Items[0].ID != "a" || catalogs != 1 {
				t.Fatal(res, err, catalogs)
			}
			if res := m.probe(ctx(), ProviderClaude, base, "test-key"); !res.OK || catalogs != 2 {
				t.Fatal(res, catalogs)
			}
			text, err := m.chat(ctx(), &Config{Provider: ProviderClaude, BaseURL: base, Model: "custom-model"}, "test-key", "question", 4096)
			if err != nil || text != "reply" || messages != 1 {
				t.Fatal(text, err, messages)
			}
		})
	}
}

func TestDeepSeekCatalogSpecializationIsExact(t *testing.T) {
	for _, base := range []string{
		"http://api.deepseek.com/anthropic", "https://api.deepseek.com:443/anthropic",
		"https://api.deepseek.com.evil.invalid/anthropic", "https://gateway.invalid/anthropic",
		"https://user@api.deepseek.com/anthropic", "https://api.deepseek.com/anthropic?key=x",
		"https://api.deepseek.com/anthropic?", "https://api.deepseek.com/anthropic#fragment",
		"https://api.deepseek.com/anthropic#", "https://api.deepseek.com/%61nthropic",
		"https://api.deepseek.com/gateway/anthropic", "https://api.deepseek.com/anthropic/custom",
		"https://api.deepseek.com", "https://api.deepseek.com/v1",
	} {
		t.Run(base, func(t *testing.T) {
			provider, destination, special := catalogDestination(ProviderClaude, base)
			if special || provider != ProviderClaude || destination != base {
				t.Fatal("custom/invalid destination was rewritten", provider, destination, special)
			}
			endpoint, headers := probeRequest(ProviderClaude, base, "test-key")
			if endpoint != endpointBase(ProviderClaude, base)+"/v1/models" || headers["x-api-key"] != "test-key" || headers["Authorization"] != "" {
				t.Fatal("default Claude catalog changed")
			}
		})
	}
	if _, _, special := catalogDestination(ProviderOpenAI, "https://api.deepseek.com/anthropic"); special {
		t.Fatal("non-Claude protocol specialized")
	}
}

func TestDeepSeekCatalogFailuresBoundedNoRedirectOrClaudePagination(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"claude-pagination", 200, `{"data":[{"id":"a"}],"has_more":true,"last_id":"a"}`},
		{"invalid-json", 200, `{`}, {"wrong-envelope", 200, `{"models":[{"name":"a"}]}`},
		{"empty", 200, `{"data":[]}`}, {"key-reflection", 200, `{"data":[{"id":"test-key"}]}`},
		{"too-large", 200, strings.Repeat("x", (2<<20)+1)},
		{"unauthorized", 401, `test-key`}, {"not-found", 404, `test-key`},
		{"redirect", 307, ``},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			m := deepSeekCatalogModel(t, opsTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.String() != "https://api.deepseek.com/models" {
					t.Fatal("redirect/pagination followed")
				}
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Location": {"https://other.invalid/models"}}, Body: io.NopCloser(strings.NewReader(tc.body)), Request: r}, nil
			}))
			res, err := m.models(ctx(), TestInput{Provider: strp(ProviderClaude), BaseURL: strp("https://api.deepseek.com/anthropic"), APIKey: strp("test-key")})
			if err != nil || res.OK || len(res.Items) != 0 || res.Error == "" || strings.Contains(res.Error, "test-key") || calls != 1 {
				t.Fatal(res, err, calls)
			}
			if tc.status != 200 {
				res := m.probe(ctx(), ProviderClaude, "https://api.deepseek.com/anthropic", "test-key")
				if res.OK || strings.Contains(res.Error, "test-key") || calls != 2 {
					t.Fatal(res, calls)
				}
			}
		})
	}
}

func TestDeepSeekCatalogSavedKeyRemainsInferenceDestinationScoped(t *testing.T) {
	calls := 0
	m := deepSeekCatalogModel(t, opsTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != "https://api.deepseek.com/models" || r.Header.Get("Authorization") != "Bearer saved-key" {
			t.Fatal("saved catalog key/address changed")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"a"}]}`)), Request: r}, nil
	}))
	if _, err := m.service.Save(ctx(), SaveInput{Provider: ProviderClaude, BaseURL: "https://api.deepseek.com/anthropic", APIKey: strp("saved-key")}); err != nil {
		t.Fatal(err)
	}
	res, err := m.models(ctx(), TestInput{BaseURL: strp("https://api.deepseek.com/anthropic/v1")})
	if err != nil || !res.OK || calls != 1 {
		t.Fatal(res, err, calls)
	}
	for _, in := range []TestInput{
		{BaseURL: strp("https://api.deepseek.com")},
		{BaseURL: strp("https://api.deepseek.com/gateway/anthropic")},
		{BaseURL: strp("https://gateway.invalid/anthropic")},
		{Provider: strp(ProviderOpenAI), BaseURL: strp("https://api.deepseek.com/anthropic")},
	} {
		if _, err := m.models(ctx(), in); err != ErrAPIKeyRequired {
			t.Fatal("models reused saved key", err)
		}
		if _, err := m.service.Test(ctx(), in); err != ErrAPIKeyRequired {
			t.Fatal("probe reused saved key", err)
		}
	}
	if calls != 1 {
		t.Fatal("new destination reached transport", calls)
	}
}

func TestDeepSeekCatalogDNSAndTimeoutBoundaries(t *testing.T) {
	calls := 0
	m := deepSeekCatalogModel(t, opsTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		<-r.Context().Done()
		return nil, r.Context().Err()
	}))
	m.service.client.Timeout = 25 * time.Millisecond
	in := TestInput{Provider: strp(ProviderClaude), BaseURL: strp("https://api.deepseek.com/anthropic"), APIKey: strp("test-key")}
	if res, err := m.models(ctx(), in); err != nil || res.OK || calls != 1 {
		t.Fatal(res, err, calls)
	}
	if res := m.probe(ctx(), ProviderClaude, *in.BaseURL, *in.APIKey); res.OK || calls != 2 {
		t.Fatal(res, calls)
	}
	m.lookupIP = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("169.254.169.254")}}, nil
	}
	if res, err := m.models(ctx(), in); err != nil || res.OK || calls != 2 {
		t.Fatal("unsafe DNS reached transport", res, err, calls)
	}
	if res := m.probe(ctx(), ProviderClaude, *in.BaseURL, *in.APIKey); res.OK || calls != 2 {
		t.Fatal("unsafe probe DNS reached transport", res, calls)
	}
}

func TestDraftModelsProtocolsAndInferenceNormalization(t *testing.T) {
	for _, provider := range []string{ProviderOpenAI, ProviderClaude, ProviderOllama} {
		t.Run(provider, func(t *testing.T) {
			pages, chats := 0, 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" {
					pages++
					want := "/gateway/v1/models"
					if provider == ProviderOllama {
						want = "/gateway/api/tags"
					}
					if r.URL.Path != want {
						t.Errorf("path %s != %s", r.URL.Path, want)
					}
					switch provider {
					case ProviderClaude:
						if r.Header.Get("x-api-key") != "test-key" || r.URL.Query().Get("limit") != "1000" {
							t.Error("Claude auth/paging")
						}
						if pages == 1 {
							w.Write([]byte(`{"data":[{"id":"z","display_name":"Z"}],"has_more":true,"last_id":"z"}`))
							return
						}
						if r.URL.Query().Get("after_id") != "z" {
							t.Error("cursor")
						}
						w.Write([]byte(`{"data":[{"id":"a"},{"id":"z"}],"has_more":false}`))
					case ProviderOpenAI:
						if r.Header.Get("Authorization") != "Bearer test-key" {
							t.Error("OpenAI auth")
						}
						w.Write([]byte(`{"data":[{"id":"z"},{"id":"a","name":"A"},{"id":"a"}]}`))
					case ProviderOllama:
						w.Write([]byte(`{"models":[{"name":"z"},{"name":"a","model":"a"}]}`))
					}
					return
				}
				chats++
				want := "/gateway/v1/chat/completions"
				if provider == ProviderClaude {
					want = "/gateway/v1/messages"
				}
				if provider == ProviderOllama {
					want = "/gateway/api/chat"
				}
				if r.URL.Path != want {
					t.Errorf("inference path %s", r.URL.Path)
				}
				var body map[string]any
				json.NewDecoder(r.Body).Decode(&body)
				if body["model"] != "custom/model:1" {
					t.Error("model changed")
				}
				switch provider {
				case ProviderOpenAI:
					w.Write([]byte(`{"choices":[{"message":{"content":"reply"}}]}`))
				case ProviderClaude:
					w.Write([]byte(`{"content":[{"type":"text","text":"reply"}]}`))
				case ProviderOllama:
					w.Write([]byte(`{"message":{"content":"reply"}}`))
				}
			}))
			defer srv.Close()
			svc, _, _ := newService(t, srv.Client())
			base := srv.URL + "/gateway/v1/"
			if provider == ProviderOllama {
				base = srv.URL + "/gateway/api/"
			}
			res, err := svc.(ModelLister).Models(ctx(), TestInput{Provider: strp(provider), BaseURL: strp(base), APIKey: strp("test-key")})
			if err != nil || !res.OK || len(res.Items) != 2 || res.Items[0].ID != "a" {
				t.Fatalf("%+v %v", res, err)
			}
			text, err := svc.(*service).chat(ctx(), provider, base, "custom/model:1", "test-key", "question")
			if err != nil || text != "reply" || chats != 1 {
				t.Fatal(text, err, chats)
			}
		})
	}
}

func TestDraftModelsNeverReuseKeyAtNewDestination(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++; w.Write([]byte(`{"data":[{"id":"a"}]}`)) }))
	defer srv.Close()
	svc, _, _ := newService(t, srv.Client())
	_, err := svc.Save(ctx(), SaveInput{Provider: ProviderOpenAI, BaseURL: srv.URL + "/api/v1", APIKey: strp("saved-key")})
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range []TestInput{
		{Provider: strp(ProviderClaude), BaseURL: strp(srv.URL + "/api/v1")},
		{BaseURL: strp(srv.URL + "/different")},
	} {
		if _, err = svc.(ModelLister).Models(ctx(), in); err != ErrAPIKeyRequired {
			t.Fatal(err)
		}
		if _, err = svc.Test(ctx(), in); err != ErrAPIKeyRequired {
			t.Fatal(err)
		}
	}
	if requests != 0 {
		t.Fatal("saved key left its destination")
	}
	if _, err = svc.Save(ctx(), SaveInput{Provider: ProviderOpenAI, BaseURL: srv.URL + "/different"}); err != ErrAPIKeyRequired {
		t.Fatal(err)
	}
	res, err := svc.(ModelLister).Models(ctx(), TestInput{BaseURL: strp(srv.URL + "/api")})
	if err != nil || !res.OK || requests != 1 {
		t.Fatal(res, err, requests)
	}
}

func TestModelsFailuresBoundedAndSanitized(t *testing.T) {
	for _, body := range []string{`{`, `{"data":[]}`, `{"data":[{"id":"test-secret"}]}`, `{"data":[{"id":"a"}],"has_more":true,"last_id":"a"}`, strings.Repeat("x", (2<<20)+1)} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
		svc, _, _ := newService(t, srv.Client())
		res, err := svc.(ModelLister).Models(ctx(), TestInput{Provider: strp(ProviderOpenAI), BaseURL: strp(srv.URL), APIKey: strp("test-secret")})
		if err != nil || res.OK || len(res.Items) != 0 || res.Error == "" || strings.Contains(res.Error, "test-secret") {
			t.Fatal(res, err)
		}
		srv.Close()
	}
	redirected := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected++ }))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer srv.Close()
	svc, _, _ := newService(t, srv.Client())
	res, err := svc.(ModelLister).Models(ctx(), TestInput{Provider: strp(ProviderOpenAI), BaseURL: strp(srv.URL), APIKey: strp("test-secret")})
	if err != nil || res.OK || redirected != 0 {
		t.Fatal(res, err, redirected)
	}
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer slow.Close()
	client := slow.Client()
	client.Timeout = 25 * time.Millisecond
	svc, _, _ = newService(t, client)
	res, err = svc.(ModelLister).Models(ctx(), TestInput{Provider: strp(ProviderOpenAI), BaseURL: strp(slow.URL), APIKey: strp("test-secret")})
	if err != nil || res.OK {
		t.Fatal(res, err)
	}
}
