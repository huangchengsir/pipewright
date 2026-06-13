package notify

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"net/smtp"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/vault"
)

func testMasterKey() *[32]byte {
	var k [32]byte
	for i := range k {
		k[i] = byte(i + 7)
	}
	return &k
}

// testDB 打开临时 SQLite(含全部迁移),返回 *sql.DB 与库文件路径(供整库 dump 验明文)。
func testDB(t *testing.T) (*sql.DB, string) {
	return storetest.OpenDBWithPath(t)
}

func newSvc(t *testing.T, client *http.Client) (*service, *sql.DB, string) {
	t.Helper()
	db, path := testDB(t)
	v := vault.New(db, testMasterKey())
	s := New(db, v, client).(*service)
	return s, db, path
}

func ctx() context.Context { return context.Background() }

// ── webhook：真发 POST ──────────────────────────────────────────────────────

func TestWebhookTestRealPOST(t *testing.T) {
	var (
		mu       sync.Mutex
		gotBody  []byte
		gotCT    string
		gotPath  string
		gotCount int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		gotBody = body
		gotCT = r.Header.Get("Content-Type")
		gotPath = r.URL.Path
		gotCount++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s, _, _ := newSvc(t, srv.Client())
	ch, err := s.Create(ctx(), CreateInput{
		Name: "ops-webhook", Type: TypeWebhook, Enabled: true,
		Config: Config{URL: srv.URL + "/hook"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	res, err := s.Test(ctx(), ch.ID)
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if !res.OK {
		t.Fatalf("webhook test 应成功: %+v", res)
	}
	mu.Lock()
	defer mu.Unlock()
	if gotCount != 1 {
		t.Fatalf("接收端应收到 1 次 POST,实际 %d", gotCount)
	}
	if gotPath != "/hook" {
		t.Fatalf("路径错: %q", gotPath)
	}
	if !strings.Contains(gotCT, "application/json") {
		t.Fatalf("Content-Type 应为 json: %q", gotCT)
	}
	if !bytes.Contains(gotBody, []byte("测试通知")) {
		t.Fatalf("POST 体应含测试标题: %s", gotBody)
	}
}

func TestWebhookNon2xxHumanReadable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	s, _, _ := newSvc(t, srv.Client())
	ch, _ := s.Create(ctx(), CreateInput{Name: "x", Type: TypeWebhook, Enabled: true, Config: Config{URL: srv.URL}})
	res, err := s.Test(ctx(), ch.ID)
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if res.OK {
		t.Fatalf("非 2xx 应失败")
	}
	if !strings.Contains(res.Error, "500") {
		t.Fatalf("错误应人读含状态码: %q", res.Error)
	}
}

// ── SSRF：拒云元数据/链路本地;允许私网 ─────────────────────────────────────

func TestWebhookSSRFBlocksMetadata(t *testing.T) {
	s, _, _ := newSvc(t, http.DefaultClient)
	_, err := s.Create(ctx(), CreateInput{
		Name: "evil", Type: TypeWebhook, Enabled: true,
		Config: Config{URL: "http://169.254.169.254/latest/meta-data/"},
	})
	if err != ErrSSRFBlocked {
		t.Fatalf("169.254 应被 SSRF 拒绝,得: %v", err)
	}
}

func TestValidWebhookURLMatrix(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"http://169.254.169.254/", false},    // 云元数据/链路本地:拒
		{"http://169.254.1.1/", false},        // 链路本地:拒
		{"http://0.0.0.0/", false},            // 未指定:拒
		{"ftp://example.com/", false},         // 非 http/https:拒
		{"http://10.0.0.5/hook", true},        // 私网:放行
		{"http://192.168.1.20:8080/x", true},  // 私网:放行
		{"http://127.0.0.1:9000/", true},      // 回环:放行(本机自测)
		{"https://hooks.example.com/x", true}, // 公网域名:放行
		{"not a url with spaces ::", false},   // 解析失败/无 host:拒
	}
	for _, c := range cases {
		if got := validWebhookURL(c.url); got != c.want {
			t.Errorf("validWebhookURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

// ── AC-SEC：裸 DB 无明文 SMTP 密码 ──────────────────────────────────────────

func TestEmailSecretNotPlaintextInDB(t *testing.T) {
	const secret = "sup3r-secret-smtp-pw-ZZZ"
	s, db, dbPath := newSvc(t, http.DefaultClient)
	ch, err := s.Create(ctx(), CreateInput{
		Name: "mail", Type: TypeEmail, Enabled: true,
		Config: Config{SMTPHost: "smtp.example.com", SMTPPort: 587, From: "a@b.com", To: "c@d.com", Username: "a@b.com"},
		Secret: secret,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !ch.HasSecret {
		t.Fatalf("应标记 HasSecret")
	}

	// 1) config_json 列绝无明文。
	var cfgJSON string
	if err := db.QueryRow(`SELECT config_json FROM notification_channels WHERE id = ?`, ch.ID).Scan(&cfgJSON); err != nil {
		t.Fatalf("query config_json: %v", err)
	}
	if strings.Contains(cfgJSON, secret) {
		t.Fatalf("config_json 含明文密码: %s", cfgJSON)
	}

	// 2) 整库文件 dump 绝无明文(密文 BLOB 不可逆出明文)。
	if dbPath != "" {
		raw, err := os.ReadFile(dbPath)
		if err != nil {
			t.Fatalf("read db file: %v", err)
		}
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatalf("裸 DB 文件含明文 SMTP 密码")
		}
	}

	// 3) 视图/响应路径绝无明文(Get 返回 Channel 无密码字段,仅 HasSecret)。
	got, err := s.Get(ctx(), ch.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.HasSecret {
		t.Fatalf("Get 应保留 HasSecret")
	}
}

// ── email：注入 stub sender 真投递(无需起 SMTP server)──────────────────────

func TestEmailTestViaStubSender(t *testing.T) {
	const secret = "pw-abc-123"
	s, _, _ := newSvc(t, http.DefaultClient)

	var (
		gotAddr string
		gotFrom string
		gotTo   []string
		gotMsg  []byte
		gotAuth smtp.Auth
	)
	s.sendMail = func(addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
		gotAddr, gotAuth, gotFrom, gotTo, gotMsg = addr, auth, from, to, msg
		return nil
	}

	ch, err := s.Create(ctx(), CreateInput{
		Name: "mail", Type: TypeEmail, Enabled: true,
		Config: Config{SMTPHost: "smtp.local", SMTPPort: 1025, From: "ci@x.com", To: "ops@x.com, dev@x.com", Username: "ci@x.com"},
		Secret: secret,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	res, err := s.Test(ctx(), ch.ID)
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if !res.OK {
		t.Fatalf("email test 应成功: %+v", res)
	}
	if gotAddr != "smtp.local:1025" {
		t.Fatalf("addr 错: %q", gotAddr)
	}
	if gotFrom != "ci@x.com" {
		t.Fatalf("from 错: %q", gotFrom)
	}
	if len(gotTo) != 2 {
		t.Fatalf("收件人应拆为 2 个: %v", gotTo)
	}
	if gotAuth == nil {
		t.Fatalf("有密码应构造 auth")
	}
	if !bytes.Contains(gotMsg, []byte("Subject: Pipewright 测试通知")) {
		t.Fatalf("消息应含 Subject: %s", gotMsg)
	}
	// 消息体绝不含密码明文。
	if bytes.Contains(gotMsg, []byte(secret)) {
		t.Fatalf("邮件消息体泄漏密码明文")
	}
}

func TestEmailNoPasswordNoAuth(t *testing.T) {
	s, _, _ := newSvc(t, http.DefaultClient)
	var gotAuth smtp.Auth = smtp.PlainAuth("x", "y", "z", "h")
	s.sendMail = func(_ string, auth smtp.Auth, _ string, _ []string, _ []byte) error {
		gotAuth = auth
		return nil
	}
	ch, _ := s.Create(ctx(), CreateInput{
		Name: "mail", Type: TypeEmail, Enabled: true,
		Config: Config{SMTPHost: "smtp.local", SMTPPort: 25, From: "a@x.com", To: "b@x.com"},
	})
	if _, err := s.Test(ctx(), ch.ID); err != nil {
		t.Fatalf("Test: %v", err)
	}
	if gotAuth != nil {
		t.Fatalf("无密码应无 auth")
	}
}

func TestInvalidTypeRejected(t *testing.T) {
	s, _, _ := newSvc(t, http.DefaultClient)
	if _, err := s.Create(ctx(), CreateInput{Name: "x", Type: "slack"}); err != ErrInvalidType {
		t.Fatalf("非法 type 应拒: %v", err)
	}
}

// ── SendVia：未启用渠道优雅跳过(不发不报错);不存在 → ErrNotFound ─────────

func TestSendViaSkipsDisabled(t *testing.T) {
	var hit int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s, _, _ := newSvc(t, srv.Client())
	ch, _ := s.Create(ctx(), CreateInput{Name: "x", Type: TypeWebhook, Enabled: false, Config: Config{URL: srv.URL}})
	if err := s.SendVia(ctx(), ch.ID, Payload{Title: "t", Body: "b"}); err != nil {
		t.Fatalf("未启用渠道 SendVia 应优雅跳过(nil): %v", err)
	}
	if hit != 0 {
		t.Fatalf("未启用渠道不应真发,hit=%d", hit)
	}
}

func TestSendViaEnabledDelivers(t *testing.T) {
	var hit int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	s, _, _ := newSvc(t, srv.Client())
	ch, _ := s.Create(ctx(), CreateInput{Name: "x", Type: TypeWebhook, Enabled: true, Config: Config{URL: srv.URL}})
	if err := s.SendVia(ctx(), ch.ID, Payload{Title: "t", Body: "b"}); err != nil {
		t.Fatalf("SendVia: %v", err)
	}
	if hit != 1 {
		t.Fatalf("启用渠道应真发 1 次,hit=%d", hit)
	}
}

func TestSendViaNotFound(t *testing.T) {
	s, _, _ := newSvc(t, http.DefaultClient)
	if err := s.SendVia(ctx(), "nope", Payload{}); err != ErrNotFound {
		t.Fatalf("不存在渠道应 ErrNotFound: %v", err)
	}
}

// ── CRUD + 更新 secret 保留/清除/轮换语义 ──────────────────────────────────

func TestUpdateSecretRetainClearRotate(t *testing.T) {
	s, _, _ := newSvc(t, http.DefaultClient)
	ch, _ := s.Create(ctx(), CreateInput{
		Name: "mail", Type: TypeEmail, Enabled: true,
		Config: Config{SMTPHost: "h", SMTPPort: 25, From: "a@x", To: "b@x"},
		Secret: "pw1",
	})
	if !ch.HasSecret {
		t.Fatalf("初始应有 secret")
	}

	// 省略 secret(nil):保留。
	name := "mail2"
	got, err := s.Update(ctx(), ch.ID, UpdateInput{Name: &name})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !got.HasSecret || got.Name != "mail2" {
		t.Fatalf("省略 secret 应保留 + 改名: %+v", got)
	}

	// 空串:清除。
	empty := ""
	got, err = s.Update(ctx(), ch.ID, UpdateInput{Secret: &empty})
	if err != nil {
		t.Fatalf("Update clear: %v", err)
	}
	if got.HasSecret {
		t.Fatalf("空串应清除 secret")
	}

	// 非空:轮换。
	pw := "pw2"
	got, err = s.Update(ctx(), ch.ID, UpdateInput{Secret: &pw})
	if err != nil {
		t.Fatalf("Update rotate: %v", err)
	}
	if !got.HasSecret {
		t.Fatalf("非空应轮换出 secret")
	}
}

func TestUpdateAndDeleteNotFound(t *testing.T) {
	s, _, _ := newSvc(t, http.DefaultClient)
	n := "x"
	if _, err := s.Update(ctx(), "nope", UpdateInput{Name: &n}); err != ErrNotFound {
		t.Fatalf("Update 不存在应 ErrNotFound: %v", err)
	}
	if err := s.Delete(ctx(), "nope"); err != ErrNotFound {
		t.Fatalf("Delete 不存在应 ErrNotFound: %v", err)
	}
}

func TestCreateValidationErrors(t *testing.T) {
	s, _, _ := newSvc(t, http.DefaultClient)
	if _, err := s.Create(ctx(), CreateInput{Name: "", Type: TypeWebhook, Config: Config{URL: "http://10.0.0.1"}}); err != ErrEmptyName {
		t.Fatalf("空名应拒: %v", err)
	}
	if _, err := s.Create(ctx(), CreateInput{Name: "x", Type: TypeWebhook}); err != ErrInvalidConfig {
		t.Fatalf("webhook 缺 url 应拒: %v", err)
	}
	if _, err := s.Create(ctx(), CreateInput{Name: "x", Type: TypeEmail, Config: Config{SMTPHost: "h"}}); err != ErrInvalidConfig {
		t.Fatalf("email 缺字段应拒: %v", err)
	}
}

// ── vault 未配置时敏感字段保存降级为明确错误(不 panic)──────────────────────

func TestSaveSecretVaultUnconfigured(t *testing.T) {
	db, _ := testDB(t)
	v := vault.New(db, nil) // 未配置 master key
	s := New(db, v, http.DefaultClient).(*service)
	_, err := s.Create(ctx(), CreateInput{
		Name: "mail", Type: TypeEmail, Enabled: true,
		Config: Config{SMTPHost: "h", SMTPPort: 25, From: "a@x", To: "b@x"},
		Secret: "pw",
	})
	if err != ErrVaultUnconfigured {
		t.Fatalf("vault 未配置存敏感字段应 ErrVaultUnconfigured: %v", err)
	}
	// 无敏感字段的渠道(webhook)仍可保存(不依赖 vault)。
	if _, err := s.Create(ctx(), CreateInput{Name: "wh", Type: TypeWebhook, Enabled: true, Config: Config{URL: "http://10.0.0.1/h"}}); err != nil {
		t.Fatalf("无敏感字段渠道应可保存: %v", err)
	}
}
