package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/huangjiawei/devopstool/internal/auth"
	"github.com/huangjiawei/devopstool/internal/target"
	"github.com/huangjiawei/devopstool/internal/vault"
)

// stubDialer 让 HTTP 层测试可控 SSH 结果,不触网。捕获 cfg 以断言不泄漏。
type stubDialer struct {
	res *target.ExecResult
	err error
}

func (d stubDialer) Run(_ context.Context, _ string, _ target.SSHConfig, _ []string) (*target.ExecResult, error) {
	return d.res, d.err
}

// setupServerAPI 构造带 auth + vault + target 的测试 server。
func setupServerAPI(t *testing.T, dialer target.SSHDialer) (*httptest.Server, *http.Client, string) {
	t.Helper()
	st := testStoreAuth(t)
	svc := auth.NewService(st.DB, nil)
	if err := svc.Bootstrap("admin", "testpass"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	v := vault.New(st.DB, testMasterKey())
	tsvc := target.New(st.DB, v, dialer)
	srv := httptest.NewServer(New(testWebFSAuth(), svc, WithVault(v), WithServers(tsvc)))
	t.Cleanup(srv.Close)

	client := newTestClient(t)
	csrf := loginWithClient(t, client, srv.URL)
	return srv, client, csrf
}

// newSSHCredAPI 经 API 建一条 ssh_key 凭据,返回 id。
func newSSHCredAPI(t *testing.T, client *http.Client, srvURL, csrf, secret string) string {
	t.Helper()
	resp := doJSON(t, client, http.MethodPost, srvURL+"/api/credentials", csrf,
		`{"name":"ssh","type":"ssh_key","secret":"`+secret+`"}`)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var c map[string]any
	_ = json.Unmarshal(raw, &c)
	id, _ := c["id"].(string)
	if id == "" {
		t.Fatalf("create credential failed: %s", raw)
	}
	return id
}

func TestServerCRUDAndNoLeak(t *testing.T) {
	srv, client, csrf := setupServerAPI(t, stubDialer{res: &target.ExecResult{Stdout: "Darwin x"}})
	credID := newSSHCredAPI(t, client, srv.URL, csrf, "LEAKMARKER_priv_key")

	// Create
	body := `{"name":"web-prod-1","host":"10.0.0.5","port":22,"user":"deploy","credentialId":"` + credID + `"}`
	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/servers", csrf, body)
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want 201: %s", resp.StatusCode, raw)
	}
	if strings.Contains(string(raw), "LEAKMARKER") {
		t.Fatalf("create 响应泄漏凭据明文: %s", raw)
	}
	var s map[string]any
	_ = json.Unmarshal(raw, &s)
	id, _ := s["id"].(string)
	if id == "" {
		t.Fatalf("no id in response: %s", raw)
	}
	if s["credentialName"] != "ssh" {
		t.Fatalf("credentialName = %v, want join 出 ssh", s["credentialName"])
	}
	// 响应绝无私钥字段。
	for _, banned := range []string{"privateKey", "password", "secret", "ciphertext"} {
		if _, present := s[banned]; present {
			t.Fatalf("响应含禁止字段 %q: %s", banned, raw)
		}
	}

	// List → { items: [...] }
	lresp := doJSON(t, client, http.MethodGet, srv.URL+"/api/servers", csrf, "")
	lraw, _ := io.ReadAll(lresp.Body)
	lresp.Body.Close()
	var list struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.Unmarshal(lraw, &list)
	if len(list.Items) != 1 {
		t.Fatalf("list len = %d, want 1: %s", len(list.Items), lraw)
	}

	// Update (PUT)
	uresp := doJSON(t, client, http.MethodPut, srv.URL+"/api/servers/"+id, csrf,
		`{"name":"renamed","port":2222}`)
	uraw, _ := io.ReadAll(uresp.Body)
	uresp.Body.Close()
	if uresp.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d: %s", uresp.StatusCode, uraw)
	}
	var u map[string]any
	_ = json.Unmarshal(uraw, &u)
	if u["name"] != "renamed" || u["port"].(float64) != 2222 {
		t.Fatalf("update mismatch: %s", uraw)
	}

	// Test connection → ok=true + output
	tresp := doJSON(t, client, http.MethodPost, srv.URL+"/api/servers/"+id+"/test", csrf, "")
	traw, _ := io.ReadAll(tresp.Body)
	tresp.Body.Close()
	if tresp.StatusCode != http.StatusOK {
		t.Fatalf("test status = %d: %s", tresp.StatusCode, traw)
	}
	if strings.Contains(string(traw), "LEAKMARKER") {
		t.Fatalf("test 响应泄漏凭据明文: %s", traw)
	}
	var tr map[string]any
	_ = json.Unmarshal(traw, &tr)
	if tr["ok"] != true {
		t.Fatalf("test ok = %v, want true: %s", tr["ok"], traw)
	}
	if !strings.Contains(tr["output"].(string), "Darwin") {
		t.Fatalf("test output = %v, want uname 输出", tr["output"])
	}
	if tr["error"] != nil {
		t.Fatalf("test error 应为 null: %v", tr["error"])
	}

	// Delete
	dresp := doJSON(t, client, http.MethodDelete, srv.URL+"/api/servers/"+id, csrf, "")
	dresp.Body.Close()
	if dresp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", dresp.StatusCode)
	}
	// Get after delete → 404
	gresp := doJSON(t, client, http.MethodGet, srv.URL+"/api/servers/"+id, csrf, "")
	gresp.Body.Close()
	if gresp.StatusCode != http.StatusNotFound {
		t.Fatalf("get after delete = %d, want 404", gresp.StatusCode)
	}
}

// TestServerTestFailureHumanReadable 验证连接失败 → 200 + ok=false + 人读 error,绝不 500、绝不泄漏。
func TestServerTestFailureHumanReadable(t *testing.T) {
	srv, client, csrf := setupServerAPI(t, stubDialer{err: target.ErrUnreachable})
	credID := newSSHCredAPI(t, client, srv.URL, csrf, "LEAKMARKER_priv_key")

	body := `{"name":"dead","host":"10.0.0.9","port":2200,"user":"deploy","credentialId":"` + credID + `"}`
	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/servers", csrf, body)
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var s map[string]any
	_ = json.Unmarshal(raw, &s)
	id := s["id"].(string)

	tresp := doJSON(t, client, http.MethodPost, srv.URL+"/api/servers/"+id+"/test", csrf, "")
	traw, _ := io.ReadAll(tresp.Body)
	tresp.Body.Close()
	if tresp.StatusCode != http.StatusOK {
		t.Fatalf("test status = %d, want 200(连接失败仍 200 + ok=false)", tresp.StatusCode)
	}
	var tr map[string]any
	_ = json.Unmarshal(traw, &tr)
	if tr["ok"] != false {
		t.Fatalf("test ok = %v, want false", tr["ok"])
	}
	errStr, _ := tr["error"].(string)
	if errStr == "" {
		t.Fatalf("test error 应为人读串: %s", traw)
	}
	if strings.Contains(string(traw), "LEAKMARKER") {
		t.Fatalf("test 错误泄漏凭据明文: %s", traw)
	}
}

// TestServerInvalidInput 验证缺字段 → 400。
func TestServerInvalidInput(t *testing.T) {
	srv, client, csrf := setupServerAPI(t, stubDialer{})
	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/servers", csrf,
		`{"host":"h","user":"u","credentialId":"c"}`) // 缺 name
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

// TestServerListServiceUnavailable 验证未注入 target 服务时返回 503(不 panic)。
func TestServerListServiceUnavailable(t *testing.T) {
	st := testStoreAuth(t)
	svc := auth.NewService(st.DB, nil)
	if err := svc.Bootstrap("admin", "testpass"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	// 不传 WithServers。
	srv := httptest.NewServer(New(testWebFSAuth(), svc))
	t.Cleanup(srv.Close)
	client := newTestClient(t)
	csrf := loginWithClient(t, client, srv.URL)

	resp := doJSON(t, client, http.MethodGet, srv.URL+"/api/servers", csrf, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}
