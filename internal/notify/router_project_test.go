package notify

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// countingServer 起一个只数请求次数的 httptest server,返回 server + 取数闭包。
func countingServer(t *testing.T) (*httptest.Server, func() int) {
	t.Helper()
	var (
		mu sync.Mutex
		n  int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		n++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, func() int {
		mu.Lock()
		defer mu.Unlock()
		return n
	}
}

// emptyVars 返回最小 TemplateVars(本测关注路由分流,不关注渲染内容)。
func emptyVars(event string) TemplateVars {
	return TemplateVars{Event: event, Status: "failed"}
}

// ── AC-1:项目级覆盖全局 —— 项目X 失败只走项目级渠道B,全局渠道A 不收 ─────────────

func TestRouteEventForProjectOverridesGlobal(t *testing.T) {
	srvA, hitA := countingServer(t)
	srvB, hitB := countingServer(t)

	// 一个 service 实例;A 用 srvA.Client(),B 用 srvB.Client() ——
	// 但 service 注入单一 client。两 server 共用默认传输即可(均为 loopback)。
	s, _, _ := newSvc(t, srvA.Client())
	chA := newWebhookChannel(t, s, srvA.URL)
	chB := newWebhookChannel(t, s, srvB.URL)

	const projX = "proj-x"
	// 全局 build_failed → A。
	if _, err := s.CreateRoute(ctx(), CreateRouteInput{Event: EventBuildFailed, ChannelID: chA, Enabled: true}); err != nil {
		t.Fatalf("create global route: %v", err)
	}
	// 项目X build_failed → B(覆盖)。
	if _, err := s.CreateRoute(ctx(), CreateRouteInput{ProjectID: projX, Event: EventBuildFailed, ChannelID: chB, Enabled: true}); err != nil {
		t.Fatalf("create project route: %v", err)
	}

	if err := s.RouteEventForProject(ctx(), projX, EventBuildFailed, emptyVars(EventBuildFailed)); err != nil {
		t.Fatalf("route event for project: %v", err)
	}

	if got := hitB(); got != 1 {
		t.Fatalf("项目级渠道B 应收到 1 次,实际 %d", got)
	}
	if got := hitA(); got != 0 {
		t.Fatalf("全局渠道A 不应收到(整体覆盖),实际 %d", got)
	}
}

// ── AC-2:项目无覆盖 → 继承全局 —— 项目Y 失败走全局渠道A ──────────────────────────

func TestRouteEventForProjectInheritsGlobal(t *testing.T) {
	srvA, hitA := countingServer(t)

	s, _, _ := newSvc(t, srvA.Client())
	chA := newWebhookChannel(t, s, srvA.URL)

	if _, err := s.CreateRoute(ctx(), CreateRouteInput{Event: EventBuildFailed, ChannelID: chA, Enabled: true}); err != nil {
		t.Fatalf("create global route: %v", err)
	}

	// 项目Y 无任何项目级路由 → 继承全局。
	if err := s.RouteEventForProject(ctx(), "proj-y", EventBuildFailed, emptyVars(EventBuildFailed)); err != nil {
		t.Fatalf("route event for project: %v", err)
	}

	if got := hitA(); got != 1 {
		t.Fatalf("继承全局:渠道A 应收到 1 次,实际 %d", got)
	}
}

// ── AC-4:删除项目级路由 → 回退继承全局 ──────────────────────────────────────────

func TestRouteEventForProjectDeleteFallsBackToGlobal(t *testing.T) {
	srvA, hitA := countingServer(t)
	srvB, hitB := countingServer(t)

	s, _, _ := newSvc(t, srvA.Client())
	chA := newWebhookChannel(t, s, srvA.URL)
	chB := newWebhookChannel(t, s, srvB.URL)

	const projX = "proj-x"
	if _, err := s.CreateRoute(ctx(), CreateRouteInput{Event: EventBuildFailed, ChannelID: chA, Enabled: true}); err != nil {
		t.Fatalf("create global route: %v", err)
	}
	projRoute, err := s.CreateRoute(ctx(), CreateRouteInput{ProjectID: projX, Event: EventBuildFailed, ChannelID: chB, Enabled: true})
	if err != nil {
		t.Fatalf("create project route: %v", err)
	}

	// 删除项目级路由后,项目X 应回退继承全局(A)。
	if err := s.DeleteRoute(ctx(), projRoute.ID); err != nil {
		t.Fatalf("delete project route: %v", err)
	}
	if err := s.RouteEventForProject(ctx(), projX, EventBuildFailed, emptyVars(EventBuildFailed)); err != nil {
		t.Fatalf("route event for project: %v", err)
	}

	if got := hitA(); got != 1 {
		t.Fatalf("删项目级后回退全局:渠道A 应收到 1 次,实际 %d", got)
	}
	if got := hitB(); got != 0 {
		t.Fatalf("删除后项目级渠道B 不应收到,实际 %d", got)
	}
}

// ── AC-3:整体覆盖(非合并)—— 项目级仅 enabled 时不叠加全局 ──────────────────────

func TestRouteEventForProjectNoMergeWithGlobal(t *testing.T) {
	srvA, hitA := countingServer(t)
	srvB, hitB := countingServer(t)

	s, _, _ := newSvc(t, srvA.Client())
	chA := newWebhookChannel(t, s, srvA.URL)
	chB := newWebhookChannel(t, s, srvB.URL)

	const projX = "proj-x"
	// 全局两个渠道(A + B 都全局)。
	if _, err := s.CreateRoute(ctx(), CreateRouteInput{Event: EventBuildFailed, ChannelID: chA, Enabled: true}); err != nil {
		t.Fatalf("create global route A: %v", err)
	}
	if _, err := s.CreateRoute(ctx(), CreateRouteInput{Event: EventBuildFailed, ChannelID: chB, Enabled: true}); err != nil {
		t.Fatalf("create global route B: %v", err)
	}
	// 项目X 只覆盖到 A。
	if _, err := s.CreateRoute(ctx(), CreateRouteInput{ProjectID: projX, Event: EventBuildFailed, ChannelID: chA, Enabled: true}); err != nil {
		t.Fatalf("create project route: %v", err)
	}

	if err := s.RouteEventForProject(ctx(), projX, EventBuildFailed, emptyVars(EventBuildFailed)); err != nil {
		t.Fatalf("route event for project: %v", err)
	}

	// 整体覆盖:项目级只含 A → 只 A 收,B(仅全局)不收。
	if got := hitA(); got != 1 {
		t.Fatalf("渠道A 应收到 1 次,实际 %d", got)
	}
	if got := hitB(); got != 0 {
		t.Fatalf("整体覆盖:仅全局的渠道B 不应收到,实际 %d", got)
	}
}

// ── projectID 空 → 等价全局路径(RouteEventForProject 与 RouteEvent 一致)──────────

func TestRouteEventForProjectEmptyProjectIsGlobal(t *testing.T) {
	srvA, hitA := countingServer(t)

	s, _, _ := newSvc(t, srvA.Client())
	chA := newWebhookChannel(t, s, srvA.URL)
	if _, err := s.CreateRoute(ctx(), CreateRouteInput{Event: EventBuildFailed, ChannelID: chA, Enabled: true}); err != nil {
		t.Fatalf("create global route: %v", err)
	}

	if err := s.RouteEventForProject(ctx(), "", EventBuildFailed, emptyVars(EventBuildFailed)); err != nil {
		t.Fatalf("route event for empty project: %v", err)
	}
	if got := hitA(); got != 1 {
		t.Fatalf("projectID 空走全局:渠道A 应收到 1 次,实际 %d", got)
	}
}

// ── List 作用域:全局 List 不含项目级路由;项目 List 仅含该项目 ─────────────────────

func TestListRoutesForProjectScope(t *testing.T) {
	s, _, _ := newSvc(t, &http.Client{})
	chID := newWebhookChannel(t, s, "https://hooks.example.com/x")

	const projX = "proj-x"
	if _, err := s.CreateRoute(ctx(), CreateRouteInput{Event: EventBuildFailed, ChannelID: chID, Enabled: true}); err != nil {
		t.Fatalf("create global route: %v", err)
	}
	pr, err := s.CreateRoute(ctx(), CreateRouteInput{ProjectID: projX, Event: EventBuildFailed, ChannelID: chID, Enabled: true})
	if err != nil {
		t.Fatalf("create project route: %v", err)
	}
	if pr.ProjectID != projX {
		t.Fatalf("CreateRoute 应回显 projectId=%q,实际 %q", projX, pr.ProjectID)
	}

	// 全局 List:只含全局路由(project_id IS NULL)。
	global, err := s.ListRoutes(ctx())
	if err != nil {
		t.Fatalf("list global routes: %v", err)
	}
	if len(global) != 1 || global[0].ProjectID != "" {
		t.Fatalf("全局 List 应只含 1 条全局路由,实际 %+v", global)
	}

	// 项目 List:只含该项目级路由。
	proj, err := s.ListRoutesForProject(ctx(), projX)
	if err != nil {
		t.Fatalf("list project routes: %v", err)
	}
	if len(proj) != 1 || proj[0].ProjectID != projX || proj[0].ID != pr.ID {
		t.Fatalf("项目 List 应只含该项目级路由,实际 %+v", proj)
	}
}
