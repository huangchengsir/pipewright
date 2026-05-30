package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/oauth"
	"github.com/huangchengsir/pipewright/internal/vault"
)

// setupOAuthServer 构造带 auth + vault + oauth 的测试 server;oauth 用注入 client(默认 http.DefaultClient)。
// 返回 server、已登录 client、csrf token、底层 vault(供断言凭据已存)。
func setupOAuthServer(t *testing.T, client *http.Client) (*httptest.Server, *http.Client, string, vault.Vault) {
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
	oauthSvc := oauth.New(st.DB, v, client)
	srv := httptest.NewServer(New(testWebFSAuth(), svc, WithVault(v), WithOAuth(oauthSvc)))
	t.Cleanup(srv.Close)

	c := newTestClient(t)
	csrf := loginWithClient(t, c, srv.URL)
	return srv, c, csrf, v
}

// TestOAuthAppPutMaskedNoPlaintext 验证 PUT apps/{provider} secret 只写 + 响应仅掩码无明文。
func TestOAuthAppPutMaskedNoPlaintext(t *testing.T) {
	srv, client, csrf, _ := setupOAuthServer(t, nil)
	body := `{"clientId":"iv1.abc","clientSecret":"SECRET-PLAINTEXT-xyz","enabled":true}`
	resp := doJSON(t, client, http.MethodPut, srv.URL+"/api/oauth/apps/github", csrf, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("PUT status = %d, body=%s", resp.StatusCode, raw)
	}
	raw, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(raw), "PLAINTEXT") {
		t.Fatalf("响应不应含明文 client_secret: %s", raw)
	}
	var dto map[string]any
	_ = json.Unmarshal(raw, &dto)
	if dto["provider"] != "github" || dto["configured"] != true || dto["enabled"] != true {
		t.Fatalf("响应字段不符: %s", raw)
	}
	if dto["clientId"] != "iv1.abc" {
		t.Fatalf("clientId 应回显: %s", raw)
	}
	if ms, _ := dto["maskedSecret"].(string); ms == "" || strings.Contains(ms, "PLAINTEXT") {
		t.Fatalf("maskedSecret 不应为空/含明文: %q", ms)
	}
}

// TestOAuthAppPutInvalidProvider 验证非法 provider → 422。
func TestOAuthAppPutInvalidProvider(t *testing.T) {
	srv, client, csrf, _ := setupOAuthServer(t, nil)
	resp := doJSON(t, client, http.MethodPut, srv.URL+"/api/oauth/apps/bitbucket", csrf, `{"clientId":"x","clientSecret":"y","enabled":true}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
}

// TestOAuthAppPutRequiresCSRF 验证 PUT 缺 CSRF token → 403。
func TestOAuthAppPutRequiresCSRF(t *testing.T) {
	srv, client, _, _ := setupOAuthServer(t, nil)
	// 故意传错 csrf。
	resp := doJSON(t, client, http.MethodPut, srv.URL+"/api/oauth/apps/github", "wrong-csrf", `{"clientId":"x","clientSecret":"y","enabled":true}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (csrf)", resp.StatusCode)
	}
}

// TestOAuthListApps 验证 GET apps 列表返回已配项(掩码)。
func TestOAuthListApps(t *testing.T) {
	srv, client, csrf, _ := setupOAuthServer(t, nil)
	_ = doJSON(t, client, http.MethodPut, srv.URL+"/api/oauth/apps/gitee", csrf, `{"clientId":"g","clientSecret":"SECRET-PLAINTEXT","enabled":true}`).Body.Close()

	resp := doJSON(t, client, http.MethodGet, srv.URL+"/api/oauth/apps", csrf, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(raw), "PLAINTEXT") {
		t.Fatalf("列表泄漏明文: %s", raw)
	}
	var arr []map[string]any
	_ = json.Unmarshal(raw, &arr)
	if len(arr) != 1 || arr[0]["provider"] != "gitee" {
		t.Fatalf("列表内容不符: %s", raw)
	}
}

// TestOAuthAuthorizeUnconfigured 验证未配置 provider 发起 authorize → 422。
func TestOAuthAuthorizeUnconfigured(t *testing.T) {
	srv, client, _, _ := setupOAuthServer(t, nil)
	resp, err := client.Get(srv.URL + "/api/oauth/github/authorize")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
}

// TestOAuthAuthorizeRedirects 验证已配 provider 发起 authorize → 302 跳授权页带 state/redirect_uri。
func TestOAuthAuthorizeRedirects(t *testing.T) {
	srv, client, csrf, _ := setupOAuthServer(t, nil)
	_ = doJSON(t, client, http.MethodPut, srv.URL+"/api/oauth/apps/github", csrf, `{"clientId":"CID","clientSecret":"s","enabled":true}`).Body.Close()

	resp, err := client.Get(srv.URL + "/api/oauth/github/authorize")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	u, _ := url.Parse(loc)
	if u.Host != "github.com" {
		t.Fatalf("authorize redirect host = %q (%s)", u.Host, loc)
	}
	q := u.Query()
	if q.Get("client_id") != "CID" || q.Get("state") == "" {
		t.Fatalf("authorize params: %v", q)
	}
	if !strings.HasSuffix(q.Get("redirect_uri"), "/api/oauth/github/callback") {
		t.Fatalf("redirect_uri: %q", q.Get("redirect_uri"))
	}
}

// TestOAuthCallbackBadState 验证 callback state 不匹配 → 跳回 oauth_error=invalid_state(不 Exchange)。
func TestOAuthCallbackBadState(t *testing.T) {
	srv, client, csrf, v := setupOAuthServer(t, nil)
	_ = doJSON(t, client, http.MethodPut, srv.URL+"/api/oauth/apps/github", csrf, `{"clientId":"CID","clientSecret":"s","enabled":true}`).Body.Close()

	resp, err := client.Get(srv.URL + "/api/oauth/github/callback?code=somecode&state=forged-state")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.Contains(loc, "oauth_error=invalid_state") {
		t.Fatalf("应跳回 invalid_state: %q", loc)
	}
	// 未存任何凭据。
	creds, _ := v.List()
	if len(creds) != 0 {
		t.Fatalf("state 不匹配不应存凭据,got %d", len(creds))
	}
}

// TestOAuthCallbackSuccessStoresCredential 走完整 authorize→callback,经 mock provider 换 token、
// 存成 git_token 凭据、跳回 connected。custom provider 指向本地 mock。
func TestOAuthCallbackSuccessStoresCredential(t *testing.T) {
	// mock provider:/oauth/token 回 token;/api/v4/user 回 login。
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.FormValue("client_secret") != "the-secret" {
			t.Errorf("provider 未收到正确 client_secret")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"glpat-FAKE-TOKEN","token_type":"bearer"}`))
	})
	mux.HandleFunc("/api/v4/user", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"username":"alice","id":7}`))
	})
	mock := httptest.NewServer(mux)
	t.Cleanup(mock.Close)

	srv, client, csrf, v := setupOAuthServer(t, mock.Client())

	// 配 custom provider 指向 mock。
	body := `{"clientId":"cid","clientSecret":"the-secret","baseUrl":"` + mock.URL + `","enabled":true}`
	if r := doJSON(t, client, http.MethodPut, srv.URL+"/api/oauth/apps/custom", csrf, body); r.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		t.Fatalf("save custom app status=%d body=%s", r.StatusCode, raw)
	}

	// 发起 authorize 拿到合法 state(从 Location 提取)。
	authResp, err := client.Get(srv.URL + "/api/oauth/custom/authorize")
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	_ = authResp.Body.Close()
	if authResp.StatusCode != http.StatusFound {
		t.Fatalf("authorize status = %d, want 302", authResp.StatusCode)
	}
	au, _ := url.Parse(authResp.Header.Get("Location"))
	state := au.Query().Get("state")
	if state == "" {
		t.Fatal("authorize 未带 state")
	}

	// callback 带合法 code + state(同会话 client)。
	cbResp, err := client.Get(srv.URL + "/api/oauth/custom/callback?code=valid-code&state=" + url.QueryEscape(state))
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	defer cbResp.Body.Close()
	if cbResp.StatusCode != http.StatusFound {
		raw, _ := io.ReadAll(cbResp.Body)
		t.Fatalf("callback status = %d body=%s", cbResp.StatusCode, raw)
	}
	loc := cbResp.Header.Get("Location")
	if !strings.Contains(loc, "connected=custom") || !strings.Contains(loc, "account=alice") {
		t.Fatalf("应跳回 connected=custom&account=alice: %q", loc)
	}

	// 验证已存成 git_token 凭据 name=custom:alice,且可解密回 token。
	creds, err := v.List()
	if err != nil {
		t.Fatalf("vault.List: %v", err)
	}
	if len(creds) != 1 || creds[0].Name != "custom:alice" || creds[0].Type != vault.TypeGitToken {
		t.Fatalf("凭据未正确创建: %+v", creds)
	}
	tok, err := v.Reveal(creds[0].ID)
	if err != nil || tok != "glpat-FAKE-TOKEN" {
		t.Fatalf("token 解密不符: tok=%q err=%v", tok, err)
	}
	// 跳回 URL 绝不含 token。
	if strings.Contains(loc, "glpat-FAKE-TOKEN") {
		t.Fatalf("跳回 URL 泄漏 token: %q", loc)
	}
}

// TestOAuthEndpoints401WithoutLogin 验证未登录访问 oauth 端点 → 401。
func TestOAuthEndpoints401WithoutLogin(t *testing.T) {
	srv, _, _, _ := setupOAuthServer(t, nil)
	anon := newTestClient(t)
	resp, err := anon.Get(srv.URL + "/api/oauth/apps")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}
