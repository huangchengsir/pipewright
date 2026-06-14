package notify

import (
	"net/http"
	"strings"
	"testing"
)

// approval_required 必须是合法事件,且出现在冻结枚举列表里(供前端/路由消费)。
func TestApprovalRequiredIsValidEvent(t *testing.T) {
	if !validEvent(EventApprovalRequired) {
		t.Fatal("approval_required should be a valid event")
	}
	found := false
	for _, e := range Events() {
		if e == EventApprovalRequired {
			found = true
		}
	}
	if !found {
		t.Fatalf("approval_required missing from Events(): %v", Events())
	}
}

// EventPayload 对 approval_required 产出非空标题/正文,且 event 字段正确。
func TestApprovalRequiredEventPayload(t *testing.T) {
	p := EventPayload("zh-CN", EventApprovalRequired, "proj", "", "", "waiting_approval", 0)
	if p.Title == "" || p.Body == "" {
		t.Fatalf("empty approval payload: %+v", p)
	}
	if p.Fields["event"] != EventApprovalRequired {
		t.Fatalf("missing event field: %+v", p.Fields)
	}
	if !strings.Contains(p.Body, "需要审批") {
		t.Fatalf("approval label missing from body: %q", p.Body)
	}
}

// defaultPayload 携带 ActionURL 时,链接透出到 Fields[actionUrl] 与正文。
func TestApprovalRequiredPayloadCarriesActionURL(t *testing.T) {
	s, _, _ := newSvc(t, http.DefaultClient)
	link := "https://ci.example.com/approvals?token=abc.def"
	p := s.defaultPayload(ctx(), EventApprovalRequired, TemplateVars{
		Project:   "proj",
		Event:     EventApprovalRequired,
		RunID:     "run-1",
		ActionURL: link,
	})
	if p.Fields[fieldActionURL] != link {
		t.Fatalf("action url not in fields: %+v", p.Fields)
	}
	if !strings.Contains(p.Body, link) {
		t.Fatalf("action url not in body: %q", p.Body)
	}
}

// 飞书卡片把审批 actionUrl 渲染为底部可点击按钮(action/button,主色 primary,带 url)。
// 点击按钮即打开 HMAC 审批确认页(回调链路见 approval 确认端点)。
func TestFeishuCardRendersActionURLAsButton(t *testing.T) {
	link := "https://ci.example.com/approvals?token=abc.def"
	card := feishuCardFor(Payload{
		Title:  "需要审批",
		Body:   "请确认",
		Fields: map[string]string{"project": "proj", "event": EventApprovalRequired, fieldActionURL: link},
		Lang:   "zh-CN",
	})
	var btn *feishuActionBtn
	for _, el := range card.Elements {
		if el.Tag == "action" && len(el.Actions) > 0 {
			b := el.Actions[0]
			btn = &b
		}
		// actionUrl 不应再混进字段双列
		for _, f := range el.Fields {
			if strings.Contains(f.Text.Content, link) {
				t.Fatalf("actionUrl 不应出现在字段双列: %q", f.Text.Content)
			}
		}
	}
	if btn == nil {
		t.Fatalf("飞书卡片缺少 actionUrl 的行动按钮: %+v", card.Elements)
	}
	if btn.URL != link {
		t.Fatalf("按钮 url = %q,应为 %q", btn.URL, link)
	}
	if btn.Type != "primary" {
		t.Fatalf("审批按钮应为 primary 主色,得 %q", btn.Type)
	}
	if btn.Text.Content != "前往审批" {
		t.Fatalf("审批按钮文案应为「前往审批」,得 %q", btn.Text.Content)
	}
}
