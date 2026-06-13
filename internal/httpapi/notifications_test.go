package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/notify"
	"github.com/huangchengsir/pipewright/internal/vault"
)

// setupNotifyServer 构造带 auth + vault + notify 的测试 server;notify 用注入 client(默认指向给定 stub)。
func setupNotifyServer(t *testing.T, client *http.Client) (*httptest.Server, *http.Client, string) {
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
	nf := notify.New(st.DB, v, client)
	srv := httptest.NewServer(New(testWebFSAuth(), svc, WithVault(v), WithNotifications(nf)))
	t.Cleanup(srv.Close)

	c := newTestClient(t)
	csrf := loginWithClient(t, c, srv.URL)
	return srv, c, csrf
}

func decodeMap(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	raw, _ := io.ReadAll(resp.Body)
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode json: %v (%s)", err, raw)
	}
	return m
}

func TestChannelsCRUDLifecycle(t *testing.T) {
	srv, client, csrf := setupNotifyServer(t, nil)
	base := srv.URL + "/api/notifications/channels"

	// 空列表 → { items: [] }
	resp := doJSON(t, client, http.MethodGet, base, csrf, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d", resp.StatusCode)
	}
	m := decodeMap(t, resp)
	resp.Body.Close()
	items, _ := m["items"].([]any)
	if items == nil || len(items) != 0 {
		t.Fatalf("初始应空 items: %v", m["items"])
	}

	// 创建 webhook
	resp = doJSON(t, client, http.MethodPost, base, csrf,
		`{"name":"ops","type":"webhook","enabled":true,"config":{"url":"http://10.0.0.2/hook"}}`)
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("create status = %d (%s)", resp.StatusCode, raw)
	}
	created := decodeMap(t, resp)
	resp.Body.Close()
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("创建应返回 id")
	}
	if created["type"] != "webhook" {
		t.Fatalf("type 错: %v", created["type"])
	}

	// 详情
	resp = doJSON(t, client, http.MethodGet, base+"/"+id, csrf, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 更新(改名 + 停用)
	resp = doJSON(t, client, http.MethodPut, base+"/"+id, csrf,
		`{"name":"ops-2","enabled":false}`)
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("update status = %d (%s)", resp.StatusCode, raw)
	}
	upd := decodeMap(t, resp)
	resp.Body.Close()
	if upd["name"] != "ops-2" || upd["enabled"] != false {
		t.Fatalf("更新未生效: %v", upd)
	}

	// 删除
	resp = doJSON(t, client, http.MethodDelete, base+"/"+id, csrf, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 删后 404
	resp = doJSON(t, client, http.MethodGet, base+"/"+id, csrf, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("删后 get 应 404,得 %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestEmailPasswordWriteOnlyMasked(t *testing.T) {
	srv, client, csrf := setupNotifyServer(t, nil)
	base := srv.URL + "/api/notifications/channels"

	resp := doJSON(t, client, http.MethodPost, base, csrf,
		`{"name":"mail","type":"email","enabled":true,"config":{"smtpHost":"smtp.x.com","smtpPort":587,"from":"a@x.com","to":"b@x.com","username":"a@x.com","password":"TOPSECRET-PW"}}`)
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("create status = %d (%s)", resp.StatusCode, raw)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	// 响应绝无明文密码;仅 hasPassword:true。
	if strings.Contains(string(raw), "TOPSECRET-PW") {
		t.Fatalf("响应泄漏密码明文: %s", raw)
	}
	if !strings.Contains(string(raw), `"hasPassword":true`) {
		t.Fatalf("响应应含 hasPassword:true: %s", raw)
	}
}

func TestCreateWebhookSSRFRejected(t *testing.T) {
	srv, client, csrf := setupNotifyServer(t, nil)
	base := srv.URL + "/api/notifications/channels"
	resp := doJSON(t, client, http.MethodPost, base, csrf,
		`{"name":"evil","type":"webhook","config":{"url":"http://169.254.169.254/latest/meta-data/"}}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("SSRF 应 422,得 %d", resp.StatusCode)
	}
	m := decodeMap(t, resp)
	resp.Body.Close()
	errObj, _ := m["error"].(map[string]any)
	if errObj["code"] != "url_not_allowed" {
		t.Fatalf("应 url_not_allowed: %v", m)
	}
}

func TestCreateInvalidTypeRejected(t *testing.T) {
	srv, client, csrf := setupNotifyServer(t, nil)
	base := srv.URL + "/api/notifications/channels"
	resp := doJSON(t, client, http.MethodPost, base, csrf,
		`{"name":"x","type":"slack","config":{}}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("非法 type 应 422,得 %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestTestChannelRealWebhookPOST(t *testing.T) {
	var (
		mu  sync.Mutex
		hit int
	)
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		hit++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer stub.Close()

	srv, client, csrf := setupNotifyServer(t, stub.Client())
	base := srv.URL + "/api/notifications/channels"

	resp := doJSON(t, client, http.MethodPost, base, csrf,
		`{"name":"hook","type":"webhook","enabled":true,"config":{"url":"`+stub.URL+`/x"}}`)
	created := decodeMap(t, resp)
	resp.Body.Close()
	id, _ := created["id"].(string)

	resp = doJSON(t, client, http.MethodPost, base+"/"+id+"/test", csrf, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("test status = %d", resp.StatusCode)
	}
	m := decodeMap(t, resp)
	resp.Body.Close()
	if m["ok"] != true {
		t.Fatalf("test 应 ok=true: %v", m)
	}
	mu.Lock()
	defer mu.Unlock()
	if hit != 1 {
		t.Fatalf("接收端应收到 1 次 POST,得 %d", hit)
	}
}

// 企业微信群机器人:经 HTTP API 创建 + test 应真发并成功(end-to-end,经 deliver 分派)。
func TestWecomChannelTestSendsEndToEnd(t *testing.T) {
	var hit int
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit++
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer stub.Close()

	srv, client, csrf := setupNotifyServer(t, stub.Client())
	base := srv.URL + "/api/notifications/channels"
	resp := doJSON(t, client, http.MethodPost, base, csrf,
		`{"name":"wc","type":"wecom","enabled":true,"config":{"url":"`+stub.URL+`/cgi-bin/webhook/send?key=k"}}`)
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("create status = %d (%s)", resp.StatusCode, raw)
	}
	created := decodeMap(t, resp)
	resp.Body.Close()
	id, _ := created["id"].(string)

	resp = doJSON(t, client, http.MethodPost, base+"/"+id+"/test", csrf, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("test status = %d", resp.StatusCode)
	}
	m := decodeMap(t, resp)
	resp.Body.Close()
	if m["ok"] != true {
		t.Fatalf("企微 test 应 ok=true: %v", m)
	}
	if hit != 1 {
		t.Fatalf("企微机器人应收到 1 次 POST,得 %d", hit)
	}
}

// 缺 URL 的 wecom/dingtalk 创建应被拒(配置校验:URL 必填)。
func TestWecomDingtalkRequireURLViaAPI(t *testing.T) {
	srv, client, csrf := setupNotifyServer(t, nil)
	base := srv.URL + "/api/notifications/channels"
	for _, ty := range []string{"wecom", "dingtalk"} {
		resp := doJSON(t, client, http.MethodPost, base, csrf,
			`{"name":"x","type":"`+ty+`","enabled":true,"config":{}}`)
		if resp.StatusCode == http.StatusCreated {
			t.Fatalf("%s 缺 URL 不应创建成功(应 4xx)", ty)
		}
		resp.Body.Close()
	}
}

// 未注入 notify 服务 → 503。
func TestNotifyServiceUnavailable(t *testing.T) {
	st := testStoreAuth(t)
	svc := auth.NewService(st.DB, nil)
	_ = svc.Bootstrap("admin", "testpass")
	srv := httptest.NewServer(New(testWebFSAuth(), svc)) // 无 WithNotifications
	t.Cleanup(srv.Close)
	c := newTestClient(t)
	csrf := loginWithClient(t, c, srv.URL)

	resp := doJSON(t, c, http.MethodGet, srv.URL+"/api/notifications/channels", csrf, "")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("未注入服务应 503,得 %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// 写操作缺 CSRF → 403。
func TestCreateChannelRequiresCSRF(t *testing.T) {
	srv, client, _ := setupNotifyServer(t, nil)
	base := srv.URL + "/api/notifications/channels"
	// 故意传空 csrf。
	resp := doJSON(t, client, http.MethodPost, base, "",
		`{"name":"x","type":"webhook","config":{"url":"http://10.0.0.1/h"}}`)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("缺 CSRF 应 403,得 %d", resp.StatusCode)
	}
	resp.Body.Close()
}
