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

// 飞书卡片把 actionUrl 渲染为可点击 lark_md 链接 [text](url)。
func TestFeishuCardRendersActionURLAsLink(t *testing.T) {
	link := "https://ci.example.com/approvals?token=abc.def"
	card := feishuCardFor(Payload{
		Title:  "需要审批",
		Body:   "请确认",
		Fields: map[string]string{"project": "proj", fieldActionURL: link},
		Lang:   "zh-CN",
	})
	var joined strings.Builder
	for _, el := range card.Elements {
		for _, f := range el.Fields {
			joined.WriteString(f.Text.Content)
			joined.WriteString("\n")
		}
		if el.Text != nil {
			joined.WriteString(el.Text.Content)
		}
	}
	if !strings.Contains(joined.String(), "]("+link+")") {
		t.Fatalf("feishu card missing clickable link for actionUrl: %q", joined.String())
	}
}
