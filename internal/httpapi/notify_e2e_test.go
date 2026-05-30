package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/notify"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/vault"
)

// TestNotifyHookEndToEndWebhook 真验(Story 5.2 / FR-20):
//   - 配「build_failed → 本地 httptest webhook 渠道」→ 触发失败 run → 本地真收到 POST(payload 含项目/状态)。
//   - 未配路由的事件(build_succeeded)→ 不发送(断言无请求)。
//   - 全链路 best-effort:失败 run 仍正常落终态。
func TestNotifyHookEndToEndWebhook(t *testing.T) {
	// 真实本地 webhook 接收端。
	var (
		mu     sync.Mutex
		bodies []string
	)
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(body))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer hook.Close()

	// 共享库(含全部迁移)。
	st, err := store.Open(t.TempDir() + "/e2e.db")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	// notify 服务(指向本地 webhook 的注入 client)。
	v := vault.New(st.DB, testMasterKey())
	notifySvc := notify.New(st.DB, v, hook.Client())

	// 配「build_failed → 本地 webhook 渠道」。
	ch, err := notifySvc.Create(context.Background(), notify.CreateInput{
		Name:    "local-hook",
		Type:    notify.TypeWebhook,
		Enabled: true,
		Config:  notify.Config{URL: hook.URL},
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if _, err := notifySvc.CreateRoute(context.Background(), notify.CreateRouteInput{
		Event: notify.EventBuildFailed, ChannelID: ch.ID, Enabled: true,
	}); err != nil {
		t.Fatalf("create route: %v", err)
	}
	// 注意:**不**为 build_succeeded 配路由 → 成功 run 不应发。

	// run 服务 + worker pool,注入 NotifyHook(被测装配)。
	runSvc := run.New(st.DB)
	pool := run.NewWorkerPool(runSvc,
		run.WithRunner(&branchFailRunner{}),
		run.WithNotifyHook(NewNotifyHook(runSvc, notifySvc, nil)),
	)
	pool.Start()
	t.Cleanup(func() { pool.Stop(context.Background()) })

	// 建项目(直接 SQL,避免引 project 包;含满足外键的 credential)。
	projID := "proj-e2e"
	credID := "cred-e2e"
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := st.DB.Exec(
		`INSERT INTO credentials (id, name, type, scope, ciphertext, masked_value, created_at, updated_at)
		 VALUES (?, 'c', 'git_token', '', X'00', 'm', ?, ?)`,
		credID, now, now,
	); err != nil {
		t.Fatalf("seed credential: %v", err)
	}
	if _, err := st.DB.Exec(
		`INSERT INTO projects (id, name, repo_url, default_branch, credential_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		projID, "demo-svc", "https://example.com/demo.git", "main", credID, now, now,
	); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	// 失败 run → 应触发 build_failed → 收到 1 个 POST。
	failRun, err := runSvc.Create(context.Background(), projID,
		run.Trigger{Type: run.TriggerManual, Branch: "fail", Commit: "abcdef1234567890"})
	if err != nil {
		t.Fatalf("create fail run: %v", err)
	}
	// 成功 run → build_succeeded 未配路由 → 不发。
	okRun, err := runSvc.Create(context.Background(), projID,
		run.Trigger{Type: run.TriggerManual, Branch: "ok", Commit: "0987654321fedcba"})
	if err != nil {
		t.Fatalf("create ok run: %v", err)
	}

	gotFail := waitRunStatusE2E(t, runSvc, failRun.ID, run.StatusFailed)
	waitRunStatusE2E(t, runSvc, okRun.ID, run.StatusSuccess)
	if gotFail.Status != run.StatusFailed {
		t.Fatalf("失败 run 终态应为 failed(通知钩子不应破坏)")
	}

	// 等通知钩子 goroutine 异步发完(它在 run 终态之后触发)。
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(bodies)
		mu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 1 {
		t.Fatalf("应恰好收到 1 个 POST(仅 build_failed),实收 %d: %v", len(bodies), bodies)
	}
	got := bodies[0]
	if !strings.Contains(got, "build_failed") {
		t.Fatalf("POST 体应含事件 build_failed: %s", got)
	}
	if !strings.Contains(got, "demo-svc") {
		t.Fatalf("POST 体应含项目名: %s", got)
	}
	if !strings.Contains(got, "failed") {
		t.Fatalf("POST 体应含状态 failed: %s", got)
	}
}

// TestNotifyHookProjectOverrideEndToEnd 真验(Story 5.4 / FR-20 细粒度):
//   - 全局 build_failed → webhookA;项目X build_failed → webhookB(覆盖)。
//   - 项目X 失败 run → 经钩子 RouteEventForProject 整体覆盖:**只 webhookB 收**,webhookA 不收。
//   - 全链路 best-effort:失败 run 仍正常落终态。
func TestNotifyHookProjectOverrideEndToEnd(t *testing.T) {
	var (
		muA sync.Mutex
		nA  int
		muB sync.Mutex
		nB  int
	)
	hookA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		muA.Lock()
		nA++
		muA.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer hookA.Close()
	hookB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		muB.Lock()
		nB++
		muB.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer hookB.Close()

	st, err := store.Open(t.TempDir() + "/e2e-override.db")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	v := vault.New(st.DB, testMasterKey())
	// 单一 client(loopback httptest 均可达)。
	notifySvc := notify.New(st.DB, v, hookA.Client())

	chA, err := notifySvc.Create(context.Background(), notify.CreateInput{
		Name: "global-hook", Type: notify.TypeWebhook, Enabled: true,
		Config: notify.Config{URL: hookA.URL},
	})
	if err != nil {
		t.Fatalf("create channel A: %v", err)
	}
	chB, err := notifySvc.Create(context.Background(), notify.CreateInput{
		Name: "project-hook", Type: notify.TypeWebhook, Enabled: true,
		Config: notify.Config{URL: hookB.URL},
	})
	if err != nil {
		t.Fatalf("create channel B: %v", err)
	}

	projID := "proj-override"
	// 全局 build_failed → A。
	if _, err := notifySvc.CreateRoute(context.Background(), notify.CreateRouteInput{
		Event: notify.EventBuildFailed, ChannelID: chA.ID, Enabled: true,
	}); err != nil {
		t.Fatalf("create global route: %v", err)
	}
	// 项目X build_failed → B(覆盖)。
	if _, err := notifySvc.CreateRoute(context.Background(), notify.CreateRouteInput{
		ProjectID: projID, Event: notify.EventBuildFailed, ChannelID: chB.ID, Enabled: true,
	}); err != nil {
		t.Fatalf("create project route: %v", err)
	}

	runSvc := run.New(st.DB)
	pool := run.NewWorkerPool(runSvc,
		run.WithRunner(&branchFailRunner{}),
		run.WithNotifyHook(NewNotifyHook(runSvc, notifySvc, nil)),
	)
	pool.Start()
	t.Cleanup(func() { pool.Stop(context.Background()) })

	credID := "cred-override"
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := st.DB.Exec(
		`INSERT INTO credentials (id, name, type, scope, ciphertext, masked_value, created_at, updated_at)
		 VALUES (?, 'c', 'git_token', '', X'00', 'm', ?, ?)`,
		credID, now, now,
	); err != nil {
		t.Fatalf("seed credential: %v", err)
	}
	if _, err := st.DB.Exec(
		`INSERT INTO projects (id, name, repo_url, default_branch, credential_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		projID, "override-svc", "https://example.com/o.git", "main", credID, now, now,
	); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	failRun, err := runSvc.Create(context.Background(), projID,
		run.Trigger{Type: run.TriggerManual, Branch: "fail", Commit: "abcdef1234567890"})
	if err != nil {
		t.Fatalf("create fail run: %v", err)
	}
	gotFail := waitRunStatusE2E(t, runSvc, failRun.ID, run.StatusFailed)
	if gotFail.Status != run.StatusFailed {
		t.Fatalf("失败 run 终态应为 failed(通知钩子不应破坏)")
	}

	// 等项目级渠道 B 收到。
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		muB.Lock()
		got := nB
		muB.Unlock()
		if got >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	muB.Lock()
	gotB := nB
	muB.Unlock()
	muA.Lock()
	gotA := nA
	muA.Unlock()
	if gotB != 1 {
		t.Fatalf("项目级渠道B 应收到 1 次,实际 %d", gotB)
	}
	if gotA != 0 {
		t.Fatalf("整体覆盖:全局渠道A 不应收到,实际 %d", gotA)
	}
}

// branchFailRunner:branch "fail" → 返回错误(run failed);否则成功。供 e2e 驱动失败/成功 run。
type branchFailRunner struct{}

func (branchFailRunner) Run(ctx context.Context, r *run.Run, sink run.StepSink) error {
	_ = sink.Plan(ctx, []string{"step"})
	_ = sink.StepRunning(ctx, 0)
	if r.Trigger.Branch == "fail" {
		_ = sink.StepDone(ctx, 0, run.StepFailed)
		return context.Canceled // 任意非 nil → run failed
	}
	_ = sink.StepDone(ctx, 0, run.StepSuccess)
	return nil
}

func waitRunStatusE2E(t *testing.T, svc run.Service, id, want string) *run.Run {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r, err := svc.Get(context.Background(), id)
		if err == nil && r.Status == want {
			return r
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("run %s 未在期限内到达状态 %s", id, want)
	return nil
}
