package notify

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// newSilentSink 返回一个对所有请求回 200 的 httptest server(不关心 body)。
func newSilentSink(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
}

// newCapturingSink 返回 server + 取最后一次请求 body 的闭包(线程安全)。
func newCapturingSink(t *testing.T) (*httptest.Server, func() string) {
	t.Helper()
	var (
		mu   sync.Mutex
		last []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		last = b
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	return srv, func() string {
		mu.Lock()
		defer mu.Unlock()
		return string(last)
	}
}

// ── renderTemplate:纯文本替换、未知占位空串、无 RCE ──────────────────────────

func TestRenderTemplateReplacesKnownAndBlanksUnknown(t *testing.T) {
	vars := map[string]string{"project": "demo", "branch": "main"}
	out := renderTemplate("构建失败:{{project}}@{{branch}} {{unknown}}!", vars)
	if out != "构建失败:demo@main !" {
		t.Fatalf("unexpected render: %q", out)
	}
}

func TestRenderTemplateNoExecutionNoRCE(t *testing.T) {
	// 用户模板含 text/template 风格函数调用语法:应被当作纯文本(无匹配占位 → 原样保留非 {{x}} 部分)。
	vars := map[string]string{"project": "demo"}
	tpl := `{{.Project}} {{printf "x"}} {{project}}`
	out := renderTemplate(tpl, vars)
	// {{.Project}} 与 {{printf "x"}} 含非 [A-Za-z0-9_] 字符,不匹配占位正则 → 原样保留;{{project}} → demo。
	if !strings.Contains(out, "{{.Project}}") || !strings.Contains(out, `{{printf "x"}}`) {
		t.Fatalf("template syntax must not be executed, got: %q", out)
	}
	if !strings.HasSuffix(out, "demo") {
		t.Fatalf("known placeholder not replaced: %q", out)
	}
}

// ── RenderPayload:配模板 → 渲染真实值 ───────────────────────────────────────

func TestRenderPayloadUsesConfiguredTemplate(t *testing.T) {
	s, _, _ := newSvc(t, nil)
	if _, err := s.CreateTemplate(ctx(), CreateTemplateInput{
		Event:         EventBuildFailed,
		TitleTemplate: "构建失败:{{project}}",
		BodyTemplate:  "{{branch}}@{{commit}} 挂了({{durationMs}}ms)\n{{errorSummary}}",
	}); err != nil {
		t.Fatalf("create template: %v", err)
	}

	vars := TemplateVars{
		Project:      "demo",
		Branch:       "main",
		Commit:       "abcdef12",
		Status:       "failed",
		Event:        EventBuildFailed,
		DurationMs:   "1500",
		RunID:        "run-1",
		ErrorSummary: "boom",
	}
	p := s.RenderPayload(ctx(), EventBuildFailed, "", vars)
	if p.Title != "构建失败:demo" {
		t.Fatalf("title not rendered: %q", p.Title)
	}
	if !strings.Contains(p.Body, "main@abcdef12 挂了(1500ms)") {
		t.Fatalf("body not rendered: %q", p.Body)
	}
	if !strings.Contains(p.Body, "boom") {
		t.Fatalf("errorSummary not rendered: %q", p.Body)
	}
}

// ── RenderPayload:未配模板 → 平台默认(行为不变)────────────────────────────

func TestRenderPayloadFallsBackToDefault(t *testing.T) {
	s, _, _ := newSvc(t, nil)
	vars := TemplateVars{Project: "demo", Branch: "main", Status: "failed", Event: EventBuildFailed, DurationMs: "1500"}

	got := s.RenderPayload(ctx(), EventBuildFailed, "", vars)
	want := EventPayload(EventBuildFailed, "demo", "main", "", "failed", 1500)
	if got.Title != want.Title || got.Body != want.Body {
		t.Fatalf("default fallback mismatch:\n got=%+v\nwant=%+v", got, want)
	}
}

// ── RenderPayload:未知占位 → 空串、不报错 ──────────────────────────────────

func TestRenderPayloadUnknownPlaceholderBlank(t *testing.T) {
	s, _, _ := newSvc(t, nil)
	if _, err := s.CreateTemplate(ctx(), CreateTemplateInput{
		Event:         EventBuildFailed,
		TitleTemplate: "T",
		BodyTemplate:  "[{{nope}}]{{project}}",
	}); err != nil {
		t.Fatalf("create template: %v", err)
	}
	p := s.RenderPayload(ctx(), EventBuildFailed, "", TemplateVars{Project: "demo", Event: EventBuildFailed})
	if p.Body != "[]demo" {
		t.Fatalf("unknown placeholder must render blank, got: %q", p.Body)
	}
}

// ── matchTemplate:channelId 精确 > 通用 ────────────────────────────────────

func TestRenderPayloadChannelSpecificBeatsGeneric(t *testing.T) {
	srv := newSilentSink(t)
	defer srv.Close()
	s, _, _ := newSvc(t, srv.Client())
	chID := newWebhookChannel(t, s, srv.URL)

	// 通用模板。
	if _, err := s.CreateTemplate(ctx(), CreateTemplateInput{
		Event: EventBuildFailed, TitleTemplate: "通用", BodyTemplate: "g",
	}); err != nil {
		t.Fatalf("create generic: %v", err)
	}
	// 渠道精确模板。
	if _, err := s.CreateTemplate(ctx(), CreateTemplateInput{
		Event: EventBuildFailed, ChannelID: chID, TitleTemplate: "精确", BodyTemplate: "x",
	}); err != nil {
		t.Fatalf("create exact: %v", err)
	}

	exact := s.RenderPayload(ctx(), EventBuildFailed, chID, TemplateVars{Event: EventBuildFailed})
	if exact.Title != "精确" {
		t.Fatalf("expected channel-specific template, got: %q", exact.Title)
	}
	generic := s.RenderPayload(ctx(), EventBuildFailed, "other-ch", TemplateVars{Event: EventBuildFailed})
	if generic.Title != "通用" {
		t.Fatalf("expected generic template, got: %q", generic.Title)
	}
}

// ── 模板 CRUD + FK CASCADE ────────────────────────────────────────────────

func TestTemplateCRUD(t *testing.T) {
	srv := newSilentSink(t)
	defer srv.Close()
	s, db, _ := newSvc(t, srv.Client())
	chID := newWebhookChannel(t, s, srv.URL)

	tpl, err := s.CreateTemplate(ctx(), CreateTemplateInput{
		Event: EventBuildFailed, ChannelID: chID, TitleTemplate: "T", BodyTemplate: "B",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// invalid event.
	if _, err := s.CreateTemplate(ctx(), CreateTemplateInput{Event: "nope"}); err != ErrInvalidEvent {
		t.Fatalf("expected ErrInvalidEvent, got %v", err)
	}
	// channel not found.
	if _, err := s.CreateTemplate(ctx(), CreateTemplateInput{Event: EventBuildFailed, ChannelID: "ghost"}); err != ErrTemplateChannelNotFound {
		t.Fatalf("expected ErrTemplateChannelNotFound, got %v", err)
	}

	// update.
	newTitle := "T2"
	upd, err := s.UpdateTemplate(ctx(), tpl.ID, UpdateTemplateInput{TitleTemplate: &newTitle})
	if err != nil || upd.TitleTemplate != "T2" {
		t.Fatalf("update: %v / %+v", err, upd)
	}

	// list.
	ls, err := s.ListTemplates(ctx())
	if err != nil || len(ls) != 1 {
		t.Fatalf("list: %v len=%d", err, len(ls))
	}

	// FK CASCADE: 删渠道 → 模板一并清。
	if err := s.Delete(ctx(), chID); err != nil {
		t.Fatalf("delete channel: %v", err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(1) FROM notification_templates WHERE id = ?`, tpl.ID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected template cascade-deleted, got %d rows", n)
	}

	// delete not found.
	if err := s.DeleteTemplate(ctx(), "ghost"); err != ErrTemplateNotFound {
		t.Fatalf("expected ErrTemplateNotFound, got %v", err)
	}
}

// ── errorSummary 脱敏:绝无明文 secret ──────────────────────────────────────

func TestSummarizeFailureMasksSecrets(t *testing.T) {
	raw := "fatal: auth failed\npassword=SuperSecret123\nBearer ghp_aaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	out := SummarizeFailure(raw)
	if strings.Contains(out, "SuperSecret123") {
		t.Fatalf("password leaked: %q", out)
	}
	if strings.Contains(out, "ghp_aaaaaaaaaaaaaaaaaaaaaaaaaaaa") {
		t.Fatalf("token leaked: %q", out)
	}
	if !strings.Contains(out, "fatal: auth failed") {
		t.Fatalf("non-secret content should survive: %q", out)
	}
}

// ── RouteEventVars:按渠道渲染 → 真发渲染后真实值 ──────────────────────────

func TestRouteEventVarsRendersConfiguredTemplate(t *testing.T) {
	sink, body := newCapturingSink(t)
	defer sink.Close()
	s, _, _ := newSvc(t, sink.Client())
	chID := newWebhookChannel(t, s, sink.URL)

	if _, err := s.CreateRoute(ctx(), CreateRouteInput{Event: EventBuildFailed, ChannelID: chID, Enabled: true}); err != nil {
		t.Fatalf("create route: %v", err)
	}
	if _, err := s.CreateTemplate(ctx(), CreateTemplateInput{
		Event: EventBuildFailed, TitleTemplate: "构建失败:{{project}}", BodyTemplate: "{{branch}} {{durationMs}}ms",
	}); err != nil {
		t.Fatalf("create template: %v", err)
	}

	vars := TemplateVars{Project: "demo", Branch: "release", Event: EventBuildFailed, DurationMs: "777"}
	if err := s.RouteEventVars(ctx(), EventBuildFailed, vars); err != nil {
		t.Fatalf("route event vars: %v", err)
	}

	got := body()
	if !strings.Contains(got, "构建失败:demo") {
		t.Fatalf("rendered title missing in body: %s", got)
	}
	if !strings.Contains(got, "release 777ms") {
		t.Fatalf("rendered body missing: %s", got)
	}
}

// RenderText:导出给 notify 节点内联模板复用;占位替换、未知占位 → 空、空模板 → 空。
func TestRenderText(t *testing.T) {
	vars := TemplateVars{Project: "demo", Branch: "main", Commit: "abc1234", Status: "success", RunID: "r-1"}
	got := RenderText("部署 {{project}} @ {{branch}}/{{commit}} = {{status}}", vars)
	if got != "部署 demo @ main/abc1234 = success" {
		t.Fatalf("unexpected render: %q", got)
	}
	if RenderText("{{unknown}}x", vars) != "x" {
		t.Fatalf("unknown placeholder should render empty")
	}
	if RenderText("", vars) != "" {
		t.Fatalf("empty template should render empty")
	}
}
