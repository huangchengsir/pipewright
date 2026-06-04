package notify

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// 飞书机器人成功:返回 {code:0} → Test 成功;POST 体须是 {msg_type:text,content:{text}}。
func TestFeishuSendSuccess(t *testing.T) {
	var (
		mu      sync.Mutex
		gotBody []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		gotBody = body
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"StatusCode":0,"StatusMessage":"success","code":0,"msg":"success","data":{}}`))
	}))
	defer srv.Close()

	s, _, _ := newSvc(t, srv.Client())
	ch, err := s.Create(ctx(), CreateInput{
		Name: "ops-feishu", Type: TypeFeishu, Enabled: true,
		Config: Config{URL: srv.URL + "/open-apis/bot/v2/hook/abc"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	res, err := s.Test(ctx(), ch.ID)
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if !res.OK {
		t.Fatalf("飞书 test 应成功: %+v", res)
	}

	mu.Lock()
	defer mu.Unlock()
	var sent feishuTextBody
	if err := json.Unmarshal(gotBody, &sent); err != nil {
		t.Fatalf("POST 体应为合法 JSON: %s", gotBody)
	}
	if sent.MsgType != "text" {
		t.Fatalf("msg_type 应为 text,得 %q", sent.MsgType)
	}
	if !strings.Contains(sent.Content.Text, "测试通知") {
		t.Fatalf("content.text 应含测试标题: %q", sent.Content.Text)
	}
	if sent.Sign != "" || sent.Timestamp != "" {
		t.Fatalf("未配密钥不应带签名: %+v", sent)
	}
}

// 关键回归守卫:飞书参数错误**也回 HTTP 200**,但 body 内 code!=0。
// 必须解析 code 判失败(否则误报成功、群里收不到)——这正是通用 webhook 渠道发不进飞书的坑。
func TestFeishuRejectedHTTP200ButCodeNonZero(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK) // 注意:HTTP 200
		_, _ = w.Write([]byte(`{"code":19002,"data":{},"msg":"params error, msg_type need"}`))
	}))
	defer srv.Close()

	s, _, _ := newSvc(t, srv.Client())
	ch, _ := s.Create(ctx(), CreateInput{
		Name: "bad", Type: TypeFeishu, Enabled: true,
		Config: Config{URL: srv.URL + "/hook"},
	})
	res, err := s.Test(ctx(), ch.ID)
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if res.OK {
		t.Fatalf("飞书 code!=0 应判失败(不可因 HTTP 200 误报成功)")
	}
	if !strings.Contains(res.Error, "19002") || !strings.Contains(res.Error, "params error") {
		t.Fatalf("错误应人读含 code 与 msg: %q", res.Error)
	}
}

// 飞书 webhook 地址必填。
func TestFeishuRequiresURL(t *testing.T) {
	s, _, _ := newSvc(t, http.DefaultClient)
	if _, err := s.Create(ctx(), CreateInput{Name: "x", Type: TypeFeishu, Enabled: true}); err != ErrInvalidConfig {
		t.Fatalf("飞书缺 URL 应 ErrInvalidConfig,得: %v", err)
	}
}

// 飞书地址同样过 SSRF 收口(拒云元数据)。
func TestFeishuSSRFBlocked(t *testing.T) {
	s, _, _ := newSvc(t, http.DefaultClient)
	_, err := s.Create(ctx(), CreateInput{
		Name: "evil", Type: TypeFeishu, Enabled: true,
		Config: Config{URL: "http://169.254.169.254/hook"},
	})
	if err != ErrSSRFBlocked {
		t.Fatalf("飞书 169.254 应被 SSRF 拒绝,得: %v", err)
	}
}

// 配了签名密钥 → POST 体带 timestamp + sign,且 sign 与规范算法一致。
func TestFeishuSignsWhenSecretConfigured(t *testing.T) {
	var (
		mu      sync.Mutex
		gotBody []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		gotBody = body
		mu.Unlock()
		_, _ = w.Write([]byte(`{"code":0,"msg":"success"}`))
	}))
	defer srv.Close()

	const secret = "my-sign-secret"
	s, _, _ := newSvc(t, srv.Client())
	ch, err := s.Create(ctx(), CreateInput{
		Name: "signed", Type: TypeFeishu, Enabled: true,
		Config: Config{URL: srv.URL + "/hook"}, Secret: secret,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	res, err := s.Test(ctx(), ch.ID)
	if err != nil || !res.OK {
		t.Fatalf("带签名应成功: %+v err=%v", res, err)
	}

	mu.Lock()
	defer mu.Unlock()
	var sent feishuTextBody
	if err := json.Unmarshal(gotBody, &sent); err != nil {
		t.Fatalf("POST 体非法: %s", gotBody)
	}
	if sent.Timestamp == "" || sent.Sign == "" {
		t.Fatalf("配密钥应带 timestamp+sign: %+v", sent)
	}
	want, _ := feishuSign(sent.Timestamp, secret)
	if sent.Sign != want {
		t.Fatalf("sign 与规范算法不一致: got %q want %q", sent.Sign, want)
	}
}

// 文本拼装:标题 + 正文 + 字段(字段按 key 稳定排序)。
func TestFeishuText(t *testing.T) {
	got := feishuText(Payload{
		Title:  "部署成功",
		Body:   "aireboot @ staging",
		Fields: map[string]string{"status": "success", "branch": "main"},
	})
	for _, want := range []string{"部署成功", "aireboot @ staging", "branch: main", "status: success"} {
		if !strings.Contains(got, want) {
			t.Fatalf("文本应含 %q,得:\n%s", want, got)
		}
	}
	// 稳定排序:branch 在 status 前。
	if strings.Index(got, "branch:") > strings.Index(got, "status:") {
		t.Fatalf("字段应按 key 排序: %s", got)
	}
	if feishuText(Payload{}) != "(空通知)" {
		t.Fatalf("空载荷应回占位文本")
	}
}
