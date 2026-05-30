package notify

// notify_e2e_test.go 是 Story 5-x 通知渠道的**真外部投递** e2e(#2 通知集成的外部前哨)。
//
// 与 notify_test.go(httptest 本地接收器)互补:本测试对**真实外部 webhook**(钉钉/企业微信/
// 飞书机器人,或 https://webhook.site/<uuid>)真发一条通知,验证真实出网投递 + 渠道格式被对端
// 接受(2xx)。脱敏由既有单测 + LLM e2e 覆盖,此处聚焦「真能投到真端点」。
//
// 默认 SKIP:仅当 PIPEWRIGHT_E2E_NOTIFY_URL 非空时运行(URL 由用户提供,绝不硬编码)。
// 跑法:PIPEWRIGHT_E2E_NOTIFY_URL='https://oapi.dingtalk.com/robot/send?access_token=***' \
//        go test ./internal/notify/ -run E2ENotify -v

import (
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// TestE2ENotifyRealWebhook 对真实外部 webhook 真发一条测试通知,验真投递成功。
func TestE2ENotifyRealWebhook(t *testing.T) {
	url := strings.TrimSpace(os.Getenv("PIPEWRIGHT_E2E_NOTIFY_URL"))
	if url == "" {
		t.Skip("设 PIPEWRIGHT_E2E_NOTIFY_URL=<真实 webhook 地址> 启用真外部通知投递 e2e")
	}

	s, _, _ := newSvc(t, &http.Client{Timeout: 20 * time.Second})
	ch, err := s.Create(ctx(), CreateInput{
		Name: "e2e-real", Type: TypeWebhook, Enabled: true,
		Config: Config{URL: url},
	})
	if err != nil {
		t.Fatalf("创建 webhook 渠道失败: %v", err)
	}

	res, err := s.Test(ctx(), ch.ID)
	if err != nil {
		t.Fatalf("Test(真投递)返回 error: %v", err)
	}
	if !res.OK {
		t.Fatalf("真外部投递未成功(对端非 2xx 或不可达): %+v —— 检查 URL/网络/机器人配置", res)
	}
	t.Logf("✅ 真外部 webhook 投递成功(对端 2xx)。去你的接收端确认收到「测试通知」内容。")
}
