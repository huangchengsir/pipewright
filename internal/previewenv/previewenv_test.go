package previewenv

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/huangchengsir/pipewright/internal/storetest"
)

// fakeAllocator 是注入用的假分配器:记录分配调用,按需返回 routeID / 错误。
type fakeAllocator struct {
	mu     sync.Mutex
	calls  []AllocateInput
	nextID string
	err    error
}

func (f *fakeAllocator) Allocate(_ context.Context, in AllocateInput) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, in)
	if f.err != nil {
		return "", f.err
	}
	id := f.nextID
	if id == "" {
		id = "route-" + in.Subdomain
	}
	return id, nil
}

// fakeRouteDeleter 记录删路由调用。
type fakeRouteDeleter struct {
	mu      sync.Mutex
	deleted []string
}

func (f *fakeRouteDeleter) DeleteRoute(_ context.Context, routeID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, routeID)
	return nil
}

func newTestSvc(t *testing.T) (*service, *fakeAllocator, *fakeRouteDeleter) {
	t.Helper()
	db := storetest.OpenDB(t)
	svc := New(db)
	alloc := &fakeAllocator{}
	del := &fakeRouteDeleter{}
	svc.SetAllocator(alloc)
	svc.SetRouteDeleter(del)
	return svc, alloc, del
}

func enablePreview(t *testing.T, svc *service, projectID string) {
	t.Helper()
	if _, err := svc.SetConfig(context.Background(), Config{
		ProjectID: projectID, Enabled: true, DNSProviderID: "prov-1", BaseDomain: "preview.example.com",
	}); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
}

// --- 配置 CRUD + 校验 -----------------------------------------------------

func TestSetConfigValidation(t *testing.T) {
	svc, _, _ := newTestSvc(t)
	ctx := context.Background()

	if _, err := svc.SetConfig(ctx, Config{ProjectID: "p1", Enabled: true, BaseDomain: "preview.example.com"}); err != ErrConfigMissingProvider {
		t.Fatalf("开启无提供商应报 ErrConfigMissingProvider, got %v", err)
	}
	if _, err := svc.SetConfig(ctx, Config{ProjectID: "p1", Enabled: true, DNSProviderID: "prov-1", BaseDomain: "not a domain"}); err != ErrConfigInvalidBaseDomain {
		t.Fatalf("开启非法根域应报 ErrConfigInvalidBaseDomain, got %v", err)
	}
	// 关闭时不强制提供商/根域。
	if _, err := svc.SetConfig(ctx, Config{ProjectID: "p1", Enabled: false}); err != nil {
		t.Fatalf("关闭配置应放行, got %v", err)
	}
	// 无配置项目 → GetConfig 返回未启用零值。
	cfg, err := svc.GetConfig(ctx, "never-set")
	if err != nil || cfg == nil || cfg.Enabled {
		t.Fatalf("无配置应返回未启用零值, got %+v err %v", cfg, err)
	}
}

func TestSetConfigUpsert(t *testing.T) {
	svc, _, _ := newTestSvc(t)
	ctx := context.Background()
	enablePreview(t, svc, "p1")
	// 再次设置 → 更新同一行(不重复)。
	if _, err := svc.SetConfig(ctx, Config{ProjectID: "p1", Enabled: true, DNSProviderID: "prov-2", BaseDomain: "pv.example.com"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	cfg, _ := svc.GetConfig(ctx, "p1")
	if cfg.DNSProviderID != "prov-2" || cfg.BaseDomain != "pv.example.com" {
		t.Fatalf("upsert 应更新, got %+v", cfg)
	}
}

// --- Provision 流程 -------------------------------------------------------

func TestProvisionDisabledNoOp(t *testing.T) {
	svc, alloc, _ := newTestSvc(t)
	ctx := context.Background()
	// 项目未开启预览 → no-op,不分配、不报错。
	env, err := svc.Provision(ctx, ProvisionInput{
		ProjectID: "p1", PRNumber: 7, ServerID: "srv-1",
		UpstreamContainer: "web", UpstreamPort: 8080, HostIP: "1.2.3.4",
	})
	if err != nil {
		t.Fatalf("未开启预览的 Provision 不应报错, got %v", err)
	}
	if env != nil {
		t.Fatalf("未开启预览不应分配环境, got %+v", env)
	}
	if len(alloc.calls) != 0 {
		t.Fatalf("未开启预览不应调用分配器, got %d", len(alloc.calls))
	}
	// 列表为空。
	envs, _ := svc.List(ctx, "p1")
	if len(envs) != 0 {
		t.Fatalf("不应有预览环境, got %d", len(envs))
	}
}

func TestProvisionCreatesEnv(t *testing.T) {
	svc, alloc, _ := newTestSvc(t)
	ctx := context.Background()
	enablePreview(t, svc, "p1")

	env, err := svc.Provision(ctx, ProvisionInput{
		ProjectID: "p1", PipelineID: "pl-1", PRNumber: 12, Branch: "pr-12",
		ServerID: "srv-1", UpstreamContainer: "web", UpstreamPort: 8080, HostIP: "1.2.3.4",
	})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if env == nil {
		t.Fatalf("应创建预览环境")
	}
	if env.Subdomain != "pr-12-p1.preview.example.com" {
		t.Fatalf("子域名不符: %q", env.Subdomain)
	}
	if env.Status != StatusActive || env.RouteID == "" {
		t.Fatalf("环境应 active 且带 routeID: %+v", env)
	}
	if len(alloc.calls) != 1 || alloc.calls[0].Subdomain != "pr-12-p1.preview.example.com" {
		t.Fatalf("分配器应被调用一次且带预览子域名: %+v", alloc.calls)
	}
	if alloc.calls[0].ProviderID != "prov-1" {
		t.Fatalf("应用配置里的 DNS 提供商: %+v", alloc.calls[0])
	}
}

func TestProvisionIdempotentOnRedeploy(t *testing.T) {
	svc, alloc, del := newTestSvc(t)
	ctx := context.Background()
	enablePreview(t, svc, "p1")

	in := ProvisionInput{
		ProjectID: "p1", PRNumber: 5, Branch: "pr-5",
		ServerID: "srv-1", UpstreamContainer: "web", UpstreamPort: 8080, HostIP: "1.2.3.4",
	}
	first, err := svc.Provision(ctx, in)
	if err != nil || first == nil {
		t.Fatalf("first provision: %v", err)
	}
	// 同 PR 重新部署 → 更新同一行(routeID 新分配器值不同),不新增。
	alloc.nextID = "route-redeploy"
	second, err := svc.Provision(ctx, in)
	if err != nil || second == nil {
		t.Fatalf("second provision: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("重新部署应复用同一环境行, first=%s second=%s", first.ID, second.ID)
	}
	if second.RouteID != "route-redeploy" {
		t.Fatalf("重新部署应刷新 routeID, got %q", second.RouteID)
	}
	// 应只剩一条环境(幂等)。
	envs, _ := svc.List(ctx, "p1")
	if len(envs) != 1 {
		t.Fatalf("同 PR 重复部署应只 1 条环境, got %d", len(envs))
	}
	// 旧路由应被 best-effort 删除(避免孤儿)。
	if len(del.deleted) != 1 || del.deleted[0] != first.RouteID {
		t.Fatalf("重新部署应删旧路由, got %+v", del.deleted)
	}
}

func TestProvisionAllocatorFailureReturnsErrNoEnv(t *testing.T) {
	svc, alloc, _ := newTestSvc(t)
	ctx := context.Background()
	enablePreview(t, svc, "p1")
	alloc.err = errors.New("dns boom")

	env, err := svc.Provision(ctx, ProvisionInput{
		ProjectID: "p1", PRNumber: 3, ServerID: "srv-1",
		UpstreamContainer: "web", UpstreamPort: 8080, HostIP: "1.2.3.4",
	})
	if err == nil {
		t.Fatalf("分配失败应返回错误供钩子记日志")
	}
	if env != nil {
		t.Fatalf("分配失败不应落环境, got %+v", env)
	}
	envs, _ := svc.List(ctx, "p1")
	if len(envs) != 0 {
		t.Fatalf("分配失败不应落库, got %d", len(envs))
	}
}

func TestProvisionNonPRNoOp(t *testing.T) {
	svc, alloc, _ := newTestSvc(t)
	ctx := context.Background()
	enablePreview(t, svc, "p1")
	// PRNumber=0(非 PR)→ 静默 no-op。
	env, err := svc.Provision(ctx, ProvisionInput{ProjectID: "p1", PRNumber: 0, UpstreamContainer: "web", UpstreamPort: 80, HostIP: "1.2.3.4", ServerID: "s"})
	if err != nil || env != nil || len(alloc.calls) != 0 {
		t.Fatalf("非 PR 应 no-op, env=%+v err=%v calls=%d", env, err, len(alloc.calls))
	}
}

func TestProvisionNoAllocatorNoOp(t *testing.T) {
	db := storetest.OpenDB(t)
	svc := New(db) // 不装配 allocator
	ctx := context.Background()
	enablePreview(t, svc, "p1")
	env, err := svc.Provision(ctx, ProvisionInput{
		ProjectID: "p1", PRNumber: 1, ServerID: "s", UpstreamContainer: "web", UpstreamPort: 80, HostIP: "1.2.3.4",
	})
	if err != nil || env != nil {
		t.Fatalf("无分配器应优雅 no-op, env=%+v err=%v", env, err)
	}
}

// --- Reclaim --------------------------------------------------------------

func TestReclaim(t *testing.T) {
	svc, _, del := newTestSvc(t)
	ctx := context.Background()
	enablePreview(t, svc, "p1")

	env, err := svc.Provision(ctx, ProvisionInput{
		ProjectID: "p1", PRNumber: 9, ServerID: "srv-1",
		UpstreamContainer: "web", UpstreamPort: 8080, HostIP: "1.2.3.4",
	})
	if err != nil || env == nil {
		t.Fatalf("provision: %v", err)
	}
	routeID := env.RouteID

	if err := svc.Reclaim(ctx, "p1", 9); err != nil {
		t.Fatalf("Reclaim: %v", err)
	}
	// 删了路由。
	if len(del.deleted) != 1 || del.deleted[0] != routeID {
		t.Fatalf("回收应删路由, got %+v", del.deleted)
	}
	// 环境标记 reclaimed + 记录回收时刻。
	got, err := svc.Get(ctx, env.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != StatusReclaimed || got.ReclaimedAt == nil {
		t.Fatalf("环境应标记 reclaimed 且记回收时刻, got %+v", got)
	}
	// 再次回收无 active 环境 → ErrNotFound。
	if err := svc.Reclaim(ctx, "p1", 9); err != ErrNotFound {
		t.Fatalf("重复回收应 ErrNotFound, got %v", err)
	}
}

func TestReclaimInvalidArgs(t *testing.T) {
	svc, _, _ := newTestSvc(t)
	ctx := context.Background()
	if err := svc.Reclaim(ctx, "", 1); err != ErrInvalidProject {
		t.Fatalf("空项目应 ErrInvalidProject, got %v", err)
	}
	if err := svc.Reclaim(ctx, "p1", 0); err != ErrInvalidPR {
		t.Fatalf("非法 PR 号应 ErrInvalidPR, got %v", err)
	}
}

// TestReclaimThenRedeployReactivates 验证回收后同 PR 重新部署会重新 active(状态复用同行)。
func TestReclaimThenRedeployReactivates(t *testing.T) {
	svc, alloc, _ := newTestSvc(t)
	ctx := context.Background()
	enablePreview(t, svc, "p1")
	in := ProvisionInput{ProjectID: "p1", PRNumber: 4, ServerID: "s", UpstreamContainer: "web", UpstreamPort: 80, HostIP: "1.2.3.4"}
	env1, _ := svc.Provision(ctx, in)
	if err := svc.Reclaim(ctx, "p1", 4); err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	alloc.nextID = "route-new"
	env2, err := svc.Provision(ctx, in)
	if err != nil || env2 == nil {
		t.Fatalf("redeploy after reclaim: %v", err)
	}
	if env2.ID != env1.ID || env2.Status != StatusActive {
		t.Fatalf("回收后重部署应复用同行并回 active, got %+v", env2)
	}
	if env2.ReclaimedAt != nil {
		t.Fatalf("回 active 应清空 reclaimedAt, got %+v", env2.ReclaimedAt)
	}
}
