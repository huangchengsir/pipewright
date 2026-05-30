package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/huangjiawei/devopstool/internal/auth"
	"github.com/huangjiawei/devopstool/internal/vault"
)

const testPEM = "-----BEGIN OPENSSH PRIVATE KEY-----\nPLAINTEXTSECRETMARKER\n-----END OPENSSH PRIVATE KEY-----"

func testMasterKey() *[32]byte {
	var k [32]byte
	for i := range k {
		k[i] = byte(i + 7)
	}
	return &k
}

// setupVaultServer 构造带认证 + 凭据保险库的测试 server。
func setupVaultServer(t *testing.T) (*httptest.Server, *http.Client, string) {
	t.Helper()
	st := testStoreAuth(t)
	svc := auth.NewService(st.DB, nil)
	if err := svc.Bootstrap("admin", "testpass"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	v := vault.New(st.DB, testMasterKey())
	srv := httptest.NewServer(New(testWebFSAuth(), svc, WithVault(v)))
	t.Cleanup(srv.Close)

	client := newTestClient(t)
	csrf := loginWithClient(t, client, srv.URL)
	return srv, client, csrf
}

func doJSON(t *testing.T, client *http.Client, method, url, csrf, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, url, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	return resp
}

// TestCreateAndListCredential 验证创建返回 201 掩码视图(无明文),列表只见掩码。
func TestCreateAndListCredential(t *testing.T) {
	srv, client, csrf := setupVaultServer(t)

	body := `{"name":"ci deploy","type":"ssh_key","scope":"prod","secret":"` +
		strings.ReplaceAll(testPEM, "\n", "\\n") + `"}`
	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/credentials", csrf, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(raw), "PLAINTEXTSECRETMARKER") || strings.Contains(string(raw), "BEGIN") {
		t.Fatalf("create response leaks plaintext: %s", raw)
	}
	var created map[string]any
	_ = json.Unmarshal(raw, &created)
	if _, ok := created["maskedValue"]; !ok {
		t.Fatalf("response missing maskedValue: %s", raw)
	}
	if _, ok := created["secret"]; ok {
		t.Fatalf("response must not echo secret: %s", raw)
	}
	if created["lastUsedAt"] != nil {
		t.Fatalf("new credential lastUsedAt should be null: %v", created["lastUsedAt"])
	}

	// 列表。
	listResp := doJSON(t, client, http.MethodGet, srv.URL+"/api/credentials", "", "")
	defer listResp.Body.Close()
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, want 200", listResp.StatusCode)
	}
	listRaw, _ := io.ReadAll(listResp.Body)
	if strings.Contains(string(listRaw), "PLAINTEXTSECRETMARKER") || strings.Contains(string(listRaw), "BEGIN") {
		t.Fatalf("list leaks plaintext: %s", listRaw)
	}
}

// TestCreateInvalidType 验证非法类型返回 400 invalid_credential。
func TestCreateInvalidType(t *testing.T) {
	srv, client, csrf := setupVaultServer(t)
	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/credentials", csrf,
		`{"name":"x","type":"bogus","secret":"s"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertErrCode(t, resp, "invalid_credential")
}

// TestCreateMissingSecret 验证缺 secret 返回 400。
func TestCreateMissingSecret(t *testing.T) {
	srv, client, csrf := setupVaultServer(t)
	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/credentials", csrf,
		`{"name":"x","type":"git_token"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertErrCode(t, resp, "invalid_credential")
}

// TestCreateRequiresCSRF 验证写操作缺 CSRF → 403。
func TestCreateRequiresCSRF(t *testing.T) {
	srv, client, _ := setupVaultServer(t)
	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/credentials", "",
		`{"name":"x","type":"git_token","secret":"ghp_a"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (missing CSRF)", resp.StatusCode)
	}
}

// TestCredentialsRequireAuth 验证未登录访问列表 → 401。
func TestCredentialsRequireAuth(t *testing.T) {
	srv, _, _ := setupVaultServer(t)
	resp, err := http.Get(srv.URL + "/api/credentials")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

// TestPatchAndDelete 验证 PATCH 改名 200、DELETE 204。
func TestPatchAndDelete(t *testing.T) {
	srv, client, csrf := setupVaultServer(t)
	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/credentials", csrf,
		`{"name":"tok","type":"git_token","scope":"s","secret":"ghp_abcd1234"}`)
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var created map[string]any
	_ = json.Unmarshal(raw, &created)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("no id in create response: %s", raw)
	}

	patchResp := doJSON(t, client, http.MethodPatch, srv.URL+"/api/credentials/"+id, csrf,
		`{"name":"renamed"}`)
	defer patchResp.Body.Close()
	if patchResp.StatusCode != http.StatusOK {
		t.Fatalf("patch status = %d, want 200", patchResp.StatusCode)
	}
	patchRaw, _ := io.ReadAll(patchResp.Body)
	var patched map[string]any
	_ = json.Unmarshal(patchRaw, &patched)
	if patched["name"] != "renamed" {
		t.Fatalf("name not updated: %s", patchRaw)
	}

	delResp := doJSON(t, client, http.MethodDelete, srv.URL+"/api/credentials/"+id, csrf, "")
	defer delResp.Body.Close()
	if delResp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", delResp.StatusCode)
	}

	// 删后再删 → 404。
	delAgain := doJSON(t, client, http.MethodDelete, srv.URL+"/api/credentials/"+id, csrf, "")
	defer delAgain.Body.Close()
	if delAgain.StatusCode != http.StatusNotFound {
		t.Fatalf("delete-again status = %d, want 404", delAgain.StatusCode)
	}
	assertErrCode(t, delAgain, "credential_not_found")
}

// TestVaultUnconfiguredEndpoint 验证未配置 master key 时端点返回 503 vault_unconfigured。
func TestVaultUnconfiguredEndpoint(t *testing.T) {
	st := testStoreAuth(t)
	svc := auth.NewService(st.DB, nil)
	if err := svc.Bootstrap("admin", "testpass"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	v := vault.New(st.DB, nil) // 未配置
	srv := httptest.NewServer(New(testWebFSAuth(), svc, WithVault(v)))
	t.Cleanup(srv.Close)
	client := newTestClient(t)
	loginWithClient(t, client, srv.URL)

	resp, err := client.Get(srv.URL + "/api/credentials")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	assertErrCode(t, resp, "vault_unconfigured")
}

func assertErrCode(t *testing.T, resp *http.Response, want string) {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Error.Code != want {
		t.Fatalf("error.code = %q, want %q", body.Error.Code, want)
	}
}
