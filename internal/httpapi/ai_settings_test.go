package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/ai"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/vault"
)

// setupAIServer 构造带 auth + vault + ai 的测试 server;ai 用注入 client(默认指向给定 stub)。
func setupAIServer(t *testing.T, client *http.Client) (*httptest.Server, *http.Client, string) {
	t.Helper()
	st := testStoreAuth(t)
	svc := auth.NewService(st.DB, nil)
	if err := svc.Bootstrap("admin", "testpass"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	v := vault.New(st.DB, testMasterKey())
	if client == nil {
		client = http.DefaultClient
	}
	aiSvc := ai.New(st.DB, v, client)
	srv := httptest.NewServer(New(testWebFSAuth(), svc, WithVault(v), WithAISettings(aiSvc)))
	t.Cleanup(srv.Close)

	c := newTestClient(t)
	csrf := loginWithClient(t, c, srv.URL)
	return srv, c, csrf
}

func TestAIGetLazyDefault(t *testing.T) {
	srv, client, csrf := setupAIServer(t, nil)
	resp := doJSON(t, client, http.MethodGet, srv.URL+"/api/settings/ai", csrf, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	var dto map[string]any
	_ = json.Unmarshal(raw, &dto)
	if dto["configured"] != false || dto["enabled"] != false {
		t.Fatalf("默认应 configured/enabled=false: %s", raw)
	}
	if dto["provider"] != "" || dto["apiKeyMasked"] != "" {
		t.Fatalf("默认应空 provider/apiKeyMasked: %s", raw)
	}
	if dto["updatedAt"] != nil {
		t.Fatalf("默认 updatedAt 应 null: %v", dto["updatedAt"])
	}
	b, _ := dto["budget"].(map[string]any)
	if _, ok := b["monthlyTokenLimit"]; !ok {
		t.Fatalf("budget.monthlyTokenLimit 字段应存在: %s", raw)
	}
}

func TestAIPutClaudeMaskedNoPlaintext(t *testing.T) {
	srv, client, csrf := setupAIServer(t, nil)
	body := `{"provider":"claude","model":"claude-sonnet-4","apiKey":"sk-ant-PLAINTEXT-abcd","budget":{"monthlyTokenLimit":1000},"enabled":true}`
	resp := doJSON(t, client, http.MethodPut, srv.URL+"/api/settings/ai", csrf, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("PUT status = %d, body=%s", resp.StatusCode, raw)
	}
	raw, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(raw), "PLAINTEXT") || strings.Contains(string(raw), "sk-ant-PLAINTEXT") {
		t.Fatalf("响应不应含明文 key: %s", raw)
	}
	var dto map[string]any
	_ = json.Unmarshal(raw, &dto)
	if dto["configured"] != true || dto["enabled"] != true {
		t.Fatalf("应 configured/enabled=true: %s", raw)
	}
	masked, _ := dto["apiKeyMasked"].(string)
	if !strings.Contains(masked, "••••") || !strings.HasSuffix(masked, "abcd") {
		t.Fatalf("apiKeyMasked 应掩码保末 4: %q", masked)
	}
	if dto["baseUrl"] != "https://api.anthropic.com" {
		t.Fatalf("baseUrl 应兜底默认: %v", dto["baseUrl"])
	}
	if dto["updatedAt"] == nil {
		t.Fatalf("保存后 updatedAt 应非 null")
	}
}

func TestAIPutKeyOmittedRetains(t *testing.T) {
	srv, client, csrf := setupAIServer(t, nil)
	base := srv.URL + "/api/settings/ai"
	// 首存带 key。
	r1 := doJSON(t, client, http.MethodPut, base, csrf,
		`{"provider":"claude","apiKey":"sk-ant-firstKEY9","budget":{"monthlyTokenLimit":null},"enabled":true}`)
	raw1, _ := io.ReadAll(r1.Body)
	r1.Body.Close()
	var d1 map[string]any
	_ = json.Unmarshal(raw1, &d1)
	mask1, _ := d1["apiKeyMasked"].(string)
	if mask1 == "" {
		t.Fatalf("首存应有掩码: %s", raw1)
	}

	// 二次省略 apiKey → 保留旧。
	r2 := doJSON(t, client, http.MethodPut, base, csrf,
		`{"provider":"claude","model":"claude-x","budget":{"monthlyTokenLimit":null},"enabled":true}`)
	raw2, _ := io.ReadAll(r2.Body)
	r2.Body.Close()
	if r2.StatusCode != http.StatusOK {
		t.Fatalf("二次保存 status=%d: %s", r2.StatusCode, raw2)
	}
	var d2 map[string]any
	_ = json.Unmarshal(raw2, &d2)
	if d2["configured"] != true {
		t.Fatalf("省略 key 应仍 configured: %s", raw2)
	}
	if d2["apiKeyMasked"] != mask1 {
		t.Fatalf("省略 key 应保留旧掩码: %v != %q", d2["apiKeyMasked"], mask1)
	}
}

func TestAIPutInvalidProvider422(t *testing.T) {
	srv, client, csrf := setupAIServer(t, nil)
	resp := doJSON(t, client, http.MethodPut, srv.URL+"/api/settings/ai", csrf,
		`{"provider":"gemini","enabled":false,"budget":{"monthlyTokenLimit":null}}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	var e map[string]map[string]string
	_ = json.Unmarshal(raw, &e)
	if e["error"]["code"] != "invalid_provider" {
		t.Fatalf("应 invalid_provider: %s", raw)
	}
}

func TestAIPutNonOllamaWithoutKey422(t *testing.T) {
	srv, client, csrf := setupAIServer(t, nil)
	resp := doJSON(t, client, http.MethodPut, srv.URL+"/api/settings/ai", csrf,
		`{"provider":"openai","enabled":false,"budget":{"monthlyTokenLimit":null}}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	var e map[string]map[string]string
	_ = json.Unmarshal(raw, &e)
	if e["error"]["code"] != "api_key_required" {
		t.Fatalf("应 api_key_required: %s", raw)
	}
}

func TestAITestStubOK(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer stub.Close()

	srv, client, csrf := setupAIServer(t, stub.Client())
	body := `{"provider":"claude","baseUrl":"` + stub.URL + `","apiKey":"sk-ant-draftKEY"}`
	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/settings/ai/test", csrf, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("test status = %d: %s", resp.StatusCode, raw)
	}
	raw, _ := io.ReadAll(resp.Body)
	var dto map[string]any
	_ = json.Unmarshal(raw, &dto)
	if dto["ok"] != true {
		t.Fatalf("stub 2xx 应 ok=true: %s", raw)
	}
	if _, ok := dto["latencyMs"]; !ok {
		t.Fatalf("应回 latencyMs: %s", raw)
	}
	if dto["error"] != nil {
		t.Fatalf("成功 error 应 null: %v", dto["error"])
	}
}

func TestAITestStub4xxNoKeyLeak(t *testing.T) {
	const secret = "sk-ant-SECRET-LEAK"
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer stub.Close()

	srv, client, csrf := setupAIServer(t, stub.Client())
	body := `{"provider":"openai","baseUrl":"` + stub.URL + `","apiKey":"` + secret + `"}`
	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/settings/ai/test", csrf, body)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(raw), secret) || strings.Contains(string(raw), "SECRET") {
		t.Fatalf("test 错误绝不含密钥: %s", raw)
	}
	var dto map[string]any
	_ = json.Unmarshal(raw, &dto)
	if dto["ok"] != false {
		t.Fatalf("401 应 ok=false: %s", raw)
	}
	if dto["error"] == nil {
		t.Fatalf("失败应有 error: %s", raw)
	}
}

func TestAIGetRequiresAuth(t *testing.T) {
	srv, _, _ := setupAIServer(t, nil)
	resp, err := http.Get(srv.URL + "/api/settings/ai")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("无会话 GET status = %d, want 401", resp.StatusCode)
	}
}

func TestAIPutRequiresCSRF(t *testing.T) {
	srv, client, _ := setupAIServer(t, nil)
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/settings/ai",
		strings.NewReader(`{"provider":"ollama","enabled":true,"budget":{"monthlyTokenLimit":null}}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("无 CSRF PUT status = %d, want 403", resp.StatusCode)
	}
}

func TestAITestRequiresCSRF(t *testing.T) {
	srv, client, _ := setupAIServer(t, nil)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/settings/ai/test",
		strings.NewReader(`{"provider":"ollama"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("无 CSRF test status = %d, want 403", resp.StatusCode)
	}
}
