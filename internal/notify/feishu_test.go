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
	var sent feishuCardBody
	if err := json.Unmarshal(gotBody, &sent); err != nil {
		t.Fatalf("POST 体应为合法 JSON: %s", gotBody)
	}
	if sent.MsgType != "interactive" {
		t.Fatalf("msg_type 应为 interactive(交互卡片),得 %q", sent.MsgType)
	}
	if sent.Card.Header.Title.Content != "🚀 Pipewright 测试通知" {
		t.Fatalf("卡片头部标题应为带图标的测试标题: %q", sent.Card.Header.Title.Content)
	}
	if len(sent.Card.Elements) == 0 {
		t.Fatalf("卡片 elements 不应为空")
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
	var sent feishuCardBody
	if err := json.Unmarshal(gotBody, &sent); err != nil {
		t.Fatalf("POST 体非法: %s", gotBody)
	}
	if sent.MsgType != "interactive" {
		t.Fatalf("msg_type 应为 interactive,得 %q", sent.MsgType)
	}
	if sent.Timestamp == "" || sent.Sign == "" {
		t.Fatalf("配密钥应带 timestamp+sign: %+v", sent)
	}
	want, _ := feishuSign(sent.Timestamp, secret)
	if sent.Sign != want {
		t.Fatalf("sign 与规范算法不一致: got %q want %q", sent.Sign, want)
	}
}

// 卡片拼装:彩色头部 + 标题 + lark_md 正文 + 字段双列(业务顺序稳定、中文标签)。
func TestFeishuCard(t *testing.T) {
	card := feishuCardFor(Payload{
		Title:  "部署成功",
		Body:   "aireboot @ staging",
		Fields: map[string]string{"status": "success", "branch": "main", "event": "deploy_succeeded"},
	})

	// 头部:状态图标 + 标题 + 成功绿。
	if card.Header.Title.Content != "✅ 部署成功" {
		t.Fatalf("头部标题应为 %q,得 %q", "✅ 部署成功", card.Header.Title.Content)
	}
	if card.Header.Template != "green" {
		t.Fatalf("部署成功事件头部应为 green,得 %q", card.Header.Template)
	}

	// 正文段(lark_md)。
	var bodyContent string
	var fieldElem *feishuCardElem
	for i := range card.Elements {
		e := &card.Elements[i]
		if e.Text != nil && e.Text.Content == "aireboot @ staging" {
			bodyContent = e.Text.Content
			if e.Text.Tag != "lark_md" {
				t.Fatalf("正文应为 lark_md,得 %q", e.Text.Tag)
			}
		}
		if len(e.Fields) > 0 {
			fieldElem = e
		}
	}
	if bodyContent == "" {
		t.Fatalf("应含正文段 aireboot @ staging: %+v", card.Elements)
	}
	if fieldElem == nil {
		t.Fatalf("应含字段双列 div: %+v", card.Elements)
	}

	// 字段:中文标签 + 值,is_short(双列),业务顺序 branch 在 status 在 event 前。
	joined := ""
	for _, f := range fieldElem.Fields {
		if !f.IsShort {
			t.Fatalf("字段格应 is_short=true(双列): %+v", f)
		}
		joined += f.Text.Content + "|"
	}
	for _, want := range []string{"**分支**\nmain", "**状态**\n✅ success", "**事件**\ndeploy_succeeded"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("字段应含 %q,得:\n%s", want, joined)
		}
	}
	if strings.Index(joined, "**分支**") > strings.Index(joined, "**状态**") {
		t.Fatalf("业务顺序:分支应在状态前: %s", joined)
	}
	if strings.Index(joined, "**状态**") > strings.Index(joined, "**事件**") {
		t.Fatalf("业务顺序:状态应在事件前: %s", joined)
	}

	// 空载荷:仍产合法卡片(默认标题 + 占位段),elements 非空。
	empty := feishuCardFor(Payload{})
	if empty.Header.Title.Content == "" || len(empty.Elements) == 0 {
		t.Fatalf("空载荷应产默认标题 + 占位段: %+v", empty)
	}
}

// 头部配色:失败红 / 回滚橙 / 成功绿 / 中性蓝,event 优先、回退 status。
func TestFeishuHeaderColor(t *testing.T) {
	cases := []struct {
		name   string
		fields map[string]string
		want   string
	}{
		{"构建失败事件→红", map[string]string{"event": "build_failed"}, "red"},
		{"部署失败 status→红", map[string]string{"status": "partial_failed"}, "red"},
		{"回滚事件→橙", map[string]string{"event": "rollback"}, "orange"},
		{"回滚 status→橙", map[string]string{"status": "rolled_back"}, "orange"},
		{"构建成功事件→绿", map[string]string{"event": "build_succeeded"}, "green"},
		{"成功 status→绿", map[string]string{"status": "success"}, "green"},
		{"未知→中性蓝", map[string]string{"kind": "test"}, "blue"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := feishuHeaderColor(Payload{Fields: c.fields}); got != c.want {
				t.Fatalf("want %q got %q (fields=%v)", c.want, got, c.fields)
			}
		})
	}
}
