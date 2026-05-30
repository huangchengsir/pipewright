package oauth

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/vault"
)

// testMasterKey 返回确定性测试用 master key。
func testMasterKey() *[32]byte {
	var k [32]byte
	for i := range k {
		k[i] = byte(i + 7)
	}
	return &k
}

// testDB 打开临时 SQLite(含全部迁移),返回 *sql.DB 与库文件路径(供整库 dump 验明文)。
func testDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st.DB, dbPath
}

func newService(t *testing.T, client *http.Client) (Service, vault.Vault, *sql.DB, string) {
	t.Helper()
	db, path := testDB(t)
	v := vault.New(db, testMasterKey())
	return New(db, v, client), v, db, path
}

func ctx() context.Context { return context.Background() }

// ---- provider URL 构造 -------------------------------------------------

func TestResolveProviderPresets(t *testing.T) {
	cases := []struct {
		name      string
		authorize string
		token     string
		user      string
		scopeHas  string
	}{
		{ProviderGitee, "https://gitee.com/oauth/authorize", "https://gitee.com/oauth/token", "https://gitee.com/api/v5/user", "projects"},
		{ProviderGitHub, "https://github.com/login/oauth/authorize", "https://github.com/login/oauth/access_token", "https://api.github.com/user", "repo"},
		{ProviderGitLab, "https://gitlab.com/oauth/authorize", "https://gitlab.com/oauth/token", "https://gitlab.com/api/v4/user", "read_repository"},
	}
	for _, c := range cases {
		p, err := resolveProvider(c.name, "")
		if err != nil {
			t.Fatalf("%s resolve: %v", c.name, err)
		}
		if p.AuthorizeURL != c.authorize || p.TokenURL != c.token || p.UserURL != c.user {
			t.Fatalf("%s endpoints mismatch: %+v", c.name, p)
		}
		if !strings.Contains(p.scope(), c.scopeHas) {
			t.Fatalf("%s scope %q missing %q", c.name, p.scope(), c.scopeHas)
		}
	}
}

func TestResolveCustomDerivesEndpoints(t *testing.T) {
	p, err := resolveProvider(ProviderCustom, "https://git.internal.example.com/")
	if err != nil {
		t.Fatalf("custom resolve: %v", err)
	}
	if p.AuthorizeURL != "https://git.internal.example.com/oauth/authorize" {
		t.Fatalf("custom authorize: %q", p.AuthorizeURL)
	}
	if p.TokenURL != "https://git.internal.example.com/oauth/token" {
		t.Fatalf("custom token: %q", p.TokenURL)
	}
	if p.UserURL != "https://git.internal.example.com/api/v4/user" {
		t.Fatalf("custom user: %q", p.UserURL)
	}
}

func TestResolveCustomRequiresBaseURL(t *testing.T) {
	if _, err := resolveProvider(ProviderCustom, ""); err != ErrBaseURLRequired {
		t.Fatalf("custom no baseURL err = %v, want ErrBaseURLRequired", err)
	}
}

func TestNormalizeProviderRejectsUnknown(t *testing.T) {
	if _, err := normalizeProvider("bitbucket"); err != ErrInvalidProvider {
		t.Fatalf("unknown provider err = %v, want ErrInvalidProvider", err)
	}
}

// ---- app 配置:secret 只写 + 掩码 + configured ------------------------

func TestSaveAppMaskedNoPlaintextAndConfigured(t *testing.T) {
	svc, _, _, dbPath := newService(t, http.DefaultClient)
	cfg, err := svc.SaveApp(ctx(), SaveAppInput{
		Provider:     ProviderGitHub,
		ClientID:     "iv1.client",
		ClientSecret: strp("super-secret-PLAINTEXT"),
		Enabled:      true,
	})
	if err != nil {
		t.Fatalf("SaveApp: %v", err)
	}
	if !cfg.Configured || !cfg.Enabled {
		t.Fatalf("应 configured/enabled=true: %+v", cfg)
	}
	if cfg.MaskedSecret == "" || strings.Contains(cfg.MaskedSecret, "PLAINTEXT") {
		t.Fatalf("maskedSecret 不应含明文: %q", cfg.MaskedSecret)
	}
	// 整库 dump 绝无明文 secret(裸断言)。
	raw, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("read db: %v", err)
	}
	if strings.Contains(string(raw), "super-secret-PLAINTEXT") {
		t.Fatalf("DB dump 含明文 client_secret!")
	}
}

func TestSaveAppSecretWriteOnlyPreservesOnOmit(t *testing.T) {
	svc, v, _, _ := newService(t, http.DefaultClient)
	if _, err := svc.SaveApp(ctx(), SaveAppInput{
		Provider: ProviderGitee, ClientID: "cid1", ClientSecret: strp("secret-1"), Enabled: true,
	}); err != nil {
		t.Fatalf("save 1: %v", err)
	}
	// 第二次保存省略 clientSecret(nil)→ 保留旧 secret;改 clientID/enabled。
	if _, err := svc.SaveApp(ctx(), SaveAppInput{
		Provider: ProviderGitee, ClientID: "cid2", ClientSecret: nil, Enabled: false,
	}); err != nil {
		t.Fatalf("save 2: %v", err)
	}
	// 省略 secret 的二次保存未报 ErrClientSecretRequired,且 ListApps 仍 configured=true、
	// clientID 已更新、enabled 已更新 —— 证明旧 secret 被保留(未清空)、其余字段已轮换。
	apps, err := svc.ListApps(ctx())
	if err != nil || len(apps) != 1 {
		t.Fatalf("ListApps: %v len=%d", err, len(apps))
	}
	a := apps[0]
	if !a.Configured {
		t.Fatalf("省略 secret 后应仍 configured(旧 secret 保留): %+v", a)
	}
	if a.ClientID != "cid2" || a.Enabled {
		t.Fatalf("clientID/enabled 应已更新: %+v", a)
	}
	_ = v
}

func TestSaveAppRejectsEmptyClientID(t *testing.T) {
	svc, _, _, _ := newService(t, http.DefaultClient)
	if _, err := svc.SaveApp(ctx(), SaveAppInput{Provider: ProviderGitee, ClientID: "  ", ClientSecret: strp("s")}); err != ErrClientIDRequired {
		t.Fatalf("empty clientID err = %v, want ErrClientIDRequired", err)
	}
}

func TestSaveAppFirstTimeRequiresSecret(t *testing.T) {
	svc, _, _, _ := newService(t, http.DefaultClient)
	if _, err := svc.SaveApp(ctx(), SaveAppInput{Provider: ProviderGitee, ClientID: "cid", ClientSecret: nil}); err != ErrClientSecretRequired {
		t.Fatalf("first save no secret err = %v, want ErrClientSecretRequired", err)
	}
}

func TestSaveAppCustomRequiresBaseURL(t *testing.T) {
	svc, _, _, _ := newService(t, http.DefaultClient)
	if _, err := svc.SaveApp(ctx(), SaveAppInput{Provider: ProviderCustom, ClientID: "cid", ClientSecret: strp("s")}); err != ErrBaseURLRequired {
		t.Fatalf("custom no baseURL err = %v, want ErrBaseURLRequired", err)
	}
}

func TestListAppsMaskedOnly(t *testing.T) {
	svc, _, _, _ := newService(t, http.DefaultClient)
	_, _ = svc.SaveApp(ctx(), SaveAppInput{Provider: ProviderGitee, ClientID: "g", ClientSecret: strp("sec-PLAINTEXT"), Enabled: true})
	apps, err := svc.ListApps(ctx())
	if err != nil {
		t.Fatalf("ListApps: %v", err)
	}
	if len(apps) != 1 {
		t.Fatalf("want 1 app, got %d", len(apps))
	}
	if strings.Contains(apps[0].MaskedSecret, "PLAINTEXT") {
		t.Fatalf("masked secret leaks plaintext: %q", apps[0].MaskedSecret)
	}
}

// ---- AuthorizeURL ------------------------------------------------------

func TestAuthorizeURLUnconfiguredProviderErrors(t *testing.T) {
	svc, _, _, _ := newService(t, http.DefaultClient)
	if _, err := svc.AuthorizeURL(ctx(), ProviderGitHub, "st", "https://x/cb"); err != ErrAppNotConfigured {
		t.Fatalf("unconfigured authorize err = %v, want ErrAppNotConfigured", err)
	}
}

func TestAuthorizeURLBuildsParams(t *testing.T) {
	svc, _, _, _ := newService(t, http.DefaultClient)
	_, _ = svc.SaveApp(ctx(), SaveAppInput{Provider: ProviderGitHub, ClientID: "CID123", ClientSecret: strp("s"), Enabled: true})
	raw, err := svc.AuthorizeURL(ctx(), ProviderGitHub, "STATE-XYZ", "https://app.example/api/oauth/github/callback")
	if err != nil {
		t.Fatalf("AuthorizeURL: %v", err)
	}
	u, _ := url.Parse(raw)
	if u.Host != "github.com" {
		t.Fatalf("authorize host = %q", u.Host)
	}
	q := u.Query()
	if q.Get("client_id") != "CID123" || q.Get("state") != "STATE-XYZ" {
		t.Fatalf("params mismatch: %v", q)
	}
	if q.Get("redirect_uri") != "https://app.example/api/oauth/github/callback" {
		t.Fatalf("redirect_uri mismatch: %q", q.Get("redirect_uri"))
	}
	if q.Get("response_type") != "code" || !strings.Contains(q.Get("scope"), "repo") {
		t.Fatalf("response_type/scope mismatch: %v", q)
	}
}

// ---- Exchange(httptest mock provider)---------------------------------

// mockProvider 返回一个假冒 provider:/token 回 access_token、/user 回 login;
// 记录是否收到 client_secret(验证 server-to-server 真带上了 secret),但绝不要求回显它。
func mockProvider(t *testing.T, wantSecret string) (*httptest.Server, *bool) {
	t.Helper()
	gotSecret := new(bool)
	mux := http.NewServeMux()
	// custom provider 据 baseURL 推导端点为 /oauth/token 与 /api/v4/user。
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.FormValue("client_secret") == wantSecret {
			*gotSecret = true
		}
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("token request missing Accept: application/json")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"gho_FAKE_TOKEN_123","token_type":"bearer"}`))
	})
	mux.HandleFunc("/api/v4/user", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer gho_FAKE_TOKEN_123" {
			t.Errorf("user request bad auth header: %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"login":"octocat","username":"octocat","id":1}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, gotSecret
}

func TestExchangeViaCustomProviderStoresToken(t *testing.T) {
	mock, gotSecret := mockProvider(t, "the-client-secret")
	svc, v, _, _ := newService(t, mock.Client())

	// custom 据 mock baseURL 推导端点;但 mock 端点路径是 /token、/user,
	// customProvider 推导为 /oauth/token、/api/v4/user。改用直接覆盖端点的方式不便,
	// 故注册 mock 在这两条路径上。
	if _, err := svc.SaveApp(ctx(), SaveAppInput{
		Provider: ProviderCustom, ClientID: "cid", ClientSecret: strp("the-client-secret"),
		BaseURL: mock.URL, Enabled: true,
	}); err != nil {
		t.Fatalf("SaveApp: %v", err)
	}

	res, err := svc.Exchange(ctx(), ProviderCustom, "auth-code-xyz", mock.URL+"/cb")
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if res.AccessToken != "gho_FAKE_TOKEN_123" || res.Login != "octocat" {
		t.Fatalf("exchange result: %+v", res)
	}
	if !*gotSecret {
		t.Fatalf("provider 未收到 client_secret(server-to-server 应带上)")
	}

	// 经 vault.Create 存成 git_token 凭据,验证可解密回 token。
	cred, err := v.Create(vault.CreateInput{
		Name: ProviderCustom + ":" + res.Login, Type: vault.TypeGitToken, Scope: "global", Secret: res.AccessToken,
	})
	if err != nil {
		t.Fatalf("vault.Create: %v", err)
	}
	got, err := v.Reveal(cred.ID)
	if err != nil {
		t.Fatalf("Reveal: %v", err)
	}
	if got != "gho_FAKE_TOKEN_123" {
		t.Fatalf("stored token mismatch: %q", got)
	}
}

func TestExchangeUnconfiguredProviderErrors(t *testing.T) {
	svc, _, _, _ := newService(t, http.DefaultClient)
	if _, err := svc.Exchange(ctx(), ProviderGitee, "code", "https://x/cb"); err != ErrAppNotConfigured {
		t.Fatalf("unconfigured exchange err = %v, want ErrAppNotConfigured", err)
	}
}

func TestExchangeProviderErrorMapsToExchangeFailed(t *testing.T) {
	// provider /token 回 4xx → ErrExchangeFailed(绝不泄漏 provider 原文)。
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad_verification_code","secret_leak":"the-client-secret"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	svc, _, _, _ := newService(t, srv.Client())
	_, _ = svc.SaveApp(ctx(), SaveAppInput{Provider: ProviderCustom, ClientID: "cid", ClientSecret: strp("the-client-secret"), BaseURL: srv.URL, Enabled: true})

	_, err := svc.Exchange(ctx(), ProviderCustom, "stale-code", srv.URL+"/cb")
	if err != ErrExchangeFailed {
		t.Fatalf("exchange err = %v, want ErrExchangeFailed", err)
	}
	if strings.Contains(err.Error(), "the-client-secret") || strings.Contains(err.Error(), "bad_verification_code") {
		t.Fatalf("错误体泄漏了 provider 原文/secret: %v", err)
	}
}

// ---- state CSRF --------------------------------------------------------

func TestStateRoundTrip(t *testing.T) {
	svc, _, _, _ := newService(t, http.DefaultClient)
	st, err := svc.IssueState("session-A")
	if err != nil {
		t.Fatalf("IssueState: %v", err)
	}
	if st == "" {
		t.Fatal("state empty")
	}
	if !svc.ConsumeState(st, "session-A") {
		t.Fatal("匹配会话应消费成功")
	}
	// 一次性:重复消费应失败(防重放)。
	if svc.ConsumeState(st, "session-A") {
		t.Fatal("state 应一次性,重复消费应拒")
	}
}

func TestStateWrongSessionRejected(t *testing.T) {
	svc, _, _, _ := newService(t, http.DefaultClient)
	st, _ := svc.IssueState("session-A")
	if svc.ConsumeState(st, "session-B") {
		t.Fatal("不同会话的 state 应拒(CSRF 防护)")
	}
	// 即便会话错被拒,state 也已被消费,正确会话再用也应失败。
	if svc.ConsumeState(st, "session-A") {
		t.Fatal("被错误会话触碰后 state 应已失效")
	}
}

func TestStateUnknownRejected(t *testing.T) {
	svc, _, _, _ := newService(t, http.DefaultClient)
	if svc.ConsumeState("never-issued", "session-A") {
		t.Fatal("未签发的 state 应拒")
	}
	if svc.ConsumeState("", "session-A") {
		t.Fatal("空 state 应拒")
	}
}

func strp(s string) *string { return &s }
