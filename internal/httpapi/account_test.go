package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/huangjiawei/devopstool/internal/audit"
	"github.com/huangjiawei/devopstool/internal/auth"
	"github.com/huangjiawei/devopstool/internal/mask"
	"github.com/huangjiawei/devopstool/internal/store"
)

// mustURL 解析 URL,失败即 fatal。
func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse url %q: %v", raw, err)
	}
	return u
}

// setupAccountServer 构造带认证 + 账户设置 + 审计的测试 server,返回 server/client/csrf/store。
func setupAccountServer(t *testing.T) (*httptest.Server, *http.Client, string, *store.Store) {
	t.Helper()
	st := testStoreAuth(t)
	svc := auth.NewService(st.DB, nil)
	if err := svc.Bootstrap("admin", "testpass"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	rec := audit.New(st.DB, mask.NewMasker(), nil)
	srv := httptest.NewServer(New(testWebFSAuth(), svc, WithAccount(svc), WithAudit(rec)))
	t.Cleanup(srv.Close)

	client := newTestClient(t)
	csrf := loginWithClient(t, client, srv.URL)
	return srv, client, csrf, st
}

// TestChangePasswordSuccess 成功改口令返回 204,旧其它会话失效,当前会话保留。
func TestChangePasswordSuccess(t *testing.T) {
	srv, client, csrf, st := setupAccountServer(t)

	// 用第二个 client 建一个「其它会话」。
	other := newTestClient(t)
	loginWithClient(t, other, srv.URL)

	var before int
	_ = st.DB.QueryRow(`SELECT COUNT(1) FROM sessions`).Scan(&before)
	if before != 2 {
		t.Fatalf("setup: want 2 sessions, got %d", before)
	}

	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/account/password", csrf,
		`{"currentPassword":"testpass","newPassword":"newpass123"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("change password status = %d, want 204; body=%s", resp.StatusCode, raw)
	}

	// 其它会话被删,只剩当前。
	var after int
	_ = st.DB.QueryRow(`SELECT COUNT(1) FROM sessions`).Scan(&after)
	if after != 1 {
		t.Fatalf("after change want 1 session (current kept), got %d", after)
	}

	// 当前会话仍可用(未掉线)。
	sessResp := doJSON(t, client, http.MethodGet, srv.URL+"/api/account/sessions", "", "")
	defer sessResp.Body.Close()
	if sessResp.StatusCode != http.StatusOK {
		t.Fatalf("current session should survive, got %d", sessResp.StatusCode)
	}

	// 新口令可登录,旧口令不可。
	bad := doLogin(t, srv, "admin", "testpass")
	defer bad.Body.Close()
	if bad.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old password should fail, got %d", bad.StatusCode)
	}
	good := doLogin(t, srv, "admin", "newpass123")
	defer good.Body.Close()
	if good.StatusCode != http.StatusOK {
		t.Fatalf("new password should login, got %d", good.StatusCode)
	}

	// 改口令入审计且无明文。
	var detail string
	if err := st.DB.QueryRow(
		`SELECT detail_json FROM audit_log WHERE action = ?`, audit.ActionPasswordChange,
	).Scan(&detail); err != nil {
		t.Fatalf("password_change audit missing: %v", err)
	}
	if strings.Contains(detail, "testpass") || strings.Contains(detail, "newpass123") {
		t.Fatalf("audit detail leaks plaintext password: %s", detail)
	}
}

// TestChangePasswordWrongCurrent 错当前口令 → 401 invalid_current_password。
func TestChangePasswordWrongCurrent(t *testing.T) {
	srv, client, csrf, _ := setupAccountServer(t)
	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/account/password", csrf,
		`{"currentPassword":"wrongpass","newPassword":"newpass123"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	if code := errorCode(t, resp); code != "invalid_current_password" {
		t.Fatalf("error code = %q, want invalid_current_password", code)
	}
}

// TestChangePasswordWeak 弱新口令(<8)→ 422 weak_password。
func TestChangePasswordWeak(t *testing.T) {
	srv, client, csrf, _ := setupAccountServer(t)
	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/account/password", csrf,
		`{"currentPassword":"testpass","newPassword":"short"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
	if code := errorCode(t, resp); code != "weak_password" {
		t.Fatalf("error code = %q, want weak_password", code)
	}
}

// TestListSessionsNoToken 会话列表绝不回传原 token,且含 current 标识。
func TestListSessionsNoToken(t *testing.T) {
	srv, client, _, st := setupAccountServer(t)

	// 取出真实 token 用于断言响应不含它。
	var token string
	if err := st.DB.QueryRow(`SELECT token FROM sessions LIMIT 1`).Scan(&token); err != nil {
		t.Fatalf("read token: %v", err)
	}

	resp := doJSON(t, client, http.MethodGet, srv.URL+"/api/account/sessions", "", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(raw), token) {
		t.Fatalf("session list leaks raw token: %s", raw)
	}
	var body struct {
		Sessions []struct {
			ID      string `json:"id"`
			Current bool   `json:"current"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Sessions) != 1 {
		t.Fatalf("want 1 session, got %d", len(body.Sessions))
	}
	if body.Sessions[0].ID != auth.SessionID(token) {
		t.Fatalf("id mismatch: got %s want %s", body.Sessions[0].ID, auth.SessionID(token))
	}
	if !body.Sessions[0].Current {
		t.Fatalf("only session should be marked current")
	}
}

// TestRevokeSession 撤销另一会话 → 204;再撤销同 id → 404 session_not_found。
func TestRevokeSession(t *testing.T) {
	srv, client, csrf, st := setupAccountServer(t)

	other := newTestClient(t)
	loginWithClient(t, other, srv.URL)

	// 找到「非当前」会话的 id。
	var curToken string
	for _, c := range client.Jar.Cookies(mustURL(t, srv.URL)) {
		if c.Name == cookieSession {
			curToken = c.Value
		}
	}
	curID := auth.SessionID(curToken)

	rows, _ := st.DB.Query(`SELECT token FROM sessions`)
	var otherID string
	for rows.Next() {
		var tk string
		_ = rows.Scan(&tk)
		if id := auth.SessionID(tk); id != curID {
			otherID = id
		}
	}
	_ = rows.Close()
	if otherID == "" {
		t.Fatal("no other session found")
	}

	resp := doJSON(t, client, http.MethodDelete, srv.URL+"/api/account/sessions/"+otherID, csrf, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke status = %d, want 204", resp.StatusCode)
	}

	// 再撤销 → 404。
	resp2 := doJSON(t, client, http.MethodDelete, srv.URL+"/api/account/sessions/"+otherID, csrf, "")
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("re-revoke status = %d, want 404", resp2.StatusCode)
	}
	if code := errorCode(t, resp2); code != "session_not_found" {
		t.Fatalf("error code = %q, want session_not_found", code)
	}
}

// errorCode 解析 { "error": { "code": ... } } 的 code。
func errorCode(t *testing.T, resp *http.Response) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	raw, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal error body: %v (%s)", err, raw)
	}
	return body.Error.Code
}
