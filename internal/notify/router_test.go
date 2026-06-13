package notify

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// newWebhookChannel 建一条启用的 webhook 渠道,指向给定 URL,返回 channelID。
func newWebhookChannel(t *testing.T, s *service, url string) string {
	t.Helper()
	ch, err := s.Create(ctx(), CreateInput{
		Name:    "test-webhook",
		Type:    TypeWebhook,
		Enabled: true,
		Config:  Config{URL: url},
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	return ch.ID
}

// ── RouteEvent:配了路由 → 真发 POST ──────────────────────────────────────────

func TestRouteEventDeliversToConfiguredChannel(t *testing.T) {
	var (
		mu      sync.Mutex
		count   int
		gotBody []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		count++
		gotBody = body
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s, _, _ := newSvc(t, srv.Client())
	chID := newWebhookChannel(t, s, srv.URL)

	if _, err := s.CreateRoute(ctx(), CreateRouteInput{
		Event:     EventBuildFailed,
		ChannelID: chID,
		Enabled:   true,
	}); err != nil {
		t.Fatalf("create route: %v", err)
	}

	payload := EventPayload("zh-CN", EventBuildFailed, "demo", "main", "abcdef1234", "failed", 1500)
	if err := s.RouteEvent(ctx(), EventBuildFailed, payload); err != nil {
		t.Fatalf("route event: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if count != 1 {
		t.Fatalf("expected 1 POST, got %d", count)
	}
	if !strings.Contains(string(gotBody), "build_failed") {
		t.Fatalf("body missing event: %s", gotBody)
	}
	if !strings.Contains(string(gotBody), "demo") {
		t.Fatalf("body missing project: %s", gotBody)
	}
}

// ── RouteEvent:未配路由 → 不发(FR-20)──────────────────────────────────────

func TestRouteEventNoRouteNoSend(t *testing.T) {
	var (
		mu    sync.Mutex
		count int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		count++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s, _, _ := newSvc(t, srv.Client())
	// 建一个渠道但**只**为 build_failed 配路由;触发 build_succeeded 应不发。
	chID := newWebhookChannel(t, s, srv.URL)
	if _, err := s.CreateRoute(ctx(), CreateRouteInput{Event: EventBuildFailed, ChannelID: chID, Enabled: true}); err != nil {
		t.Fatalf("create route: %v", err)
	}

	if err := s.RouteEvent(ctx(), EventBuildSucceeded, testPayload()); err != nil {
		t.Fatalf("route event: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if count != 0 {
		t.Fatalf("expected 0 POST for unconfigured event, got %d", count)
	}
}

// ── RouteEvent:disabled route 不参与;多渠道去重 ──────────────────────────────

func TestRouteEventDisabledRouteSkipped(t *testing.T) {
	var (
		mu    sync.Mutex
		count int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		count++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s, _, _ := newSvc(t, srv.Client())
	chID := newWebhookChannel(t, s, srv.URL)
	if _, err := s.CreateRoute(ctx(), CreateRouteInput{Event: EventBuildFailed, ChannelID: chID, Enabled: false}); err != nil {
		t.Fatalf("create route: %v", err)
	}

	if err := s.RouteEvent(ctx(), EventBuildFailed, testPayload()); err != nil {
		t.Fatalf("route event: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if count != 0 {
		t.Fatalf("expected 0 POST for disabled route, got %d", count)
	}
}

// ── RouteEvent:best-effort,单渠道失败不阻断(返回 nil)────────────────────────

func TestRouteEventBestEffortOnFailure(t *testing.T) {
	// 渠道指向一个立即关闭的 server(连接被拒)→ SendVia 失败,但 RouteEvent 仍返回 nil。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	url := srv.URL
	srv.Close() // 立即关闭:连接将被拒。

	s, _, _ := newSvc(t, &http.Client{})
	chID := newWebhookChannel(t, s, url)
	if _, err := s.CreateRoute(ctx(), CreateRouteInput{Event: EventBuildFailed, ChannelID: chID, Enabled: true}); err != nil {
		t.Fatalf("create route: %v", err)
	}
	if err := s.RouteEvent(ctx(), EventBuildFailed, testPayload()); err != nil {
		t.Fatalf("route event should be best-effort (nil), got: %v", err)
	}
}

// ── Route CRUD ────────────────────────────────────────────────────────────────

func TestRouteCRUD(t *testing.T) {
	s, _, _ := newSvc(t, &http.Client{})
	chID := newWebhookChannel(t, s, "https://hooks.example.com/x")

	// 非法事件 → ErrInvalidEvent。
	if _, err := s.CreateRoute(ctx(), CreateRouteInput{Event: "nope", ChannelID: chID, Enabled: true}); err != ErrInvalidEvent {
		t.Fatalf("expected ErrInvalidEvent, got %v", err)
	}
	// 渠道不存在 → ErrRouteChannelNotFound。
	if _, err := s.CreateRoute(ctx(), CreateRouteInput{Event: EventBuildFailed, ChannelID: "missing", Enabled: true}); err != ErrRouteChannelNotFound {
		t.Fatalf("expected ErrRouteChannelNotFound, got %v", err)
	}

	r, err := s.CreateRoute(ctx(), CreateRouteInput{Event: EventBuildFailed, ChannelID: chID, Enabled: true})
	if err != nil {
		t.Fatalf("create route: %v", err)
	}
	if r.ProjectID != "" {
		t.Fatalf("expected empty projectId (global), got %q", r.ProjectID)
	}

	routes, err := s.ListRoutes(ctx())
	if err != nil {
		t.Fatalf("list routes: %v", err)
	}
	if len(routes) != 1 || routes[0].ID != r.ID {
		t.Fatalf("unexpected routes: %+v", routes)
	}

	if err := s.DeleteRoute(ctx(), r.ID); err != nil {
		t.Fatalf("delete route: %v", err)
	}
	if err := s.DeleteRoute(ctx(), r.ID); err != ErrRouteNotFound {
		t.Fatalf("expected ErrRouteNotFound, got %v", err)
	}
}

// ── 删除渠道 → 关联 route 级联清理(FK ON DELETE CASCADE)────────────────────────

func TestDeleteChannelCascadesRoutes(t *testing.T) {
	s, db, _ := newSvc(t, &http.Client{})
	chID := newWebhookChannel(t, s, "https://hooks.example.com/x")
	if _, err := s.CreateRoute(ctx(), CreateRouteInput{Event: EventBuildFailed, ChannelID: chID, Enabled: true}); err != nil {
		t.Fatalf("create route: %v", err)
	}
	if err := s.Delete(ctx(), chID); err != nil {
		t.Fatalf("delete channel: %v", err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(1) FROM notification_routes WHERE channel_id = ?`, chID).Scan(&n); err != nil {
		t.Fatalf("count routes: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected cascade delete of routes, got %d remaining", n)
	}
}

func TestEventPayloadShape(t *testing.T) {
	p := EventPayload("zh-CN", EventBuildSucceeded, "proj", "main", "deadbeefcafe", "success", 2500)
	if p.Title == "" || p.Body == "" {
		t.Fatalf("empty payload: %+v", p)
	}
	if p.Fields["event"] != EventBuildSucceeded {
		t.Fatalf("missing event field: %+v", p.Fields)
	}
	if p.Fields["commit"] != "deadbeef" {
		t.Fatalf("commit not shortened: %q", p.Fields["commit"])
	}
	if p.Fields["project"] != "proj" {
		t.Fatalf("missing project: %+v", p.Fields)
	}
}
