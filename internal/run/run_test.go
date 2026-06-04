package run

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/store"
)

// testDB 打开临时 SQLite(含全部迁移)。
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st.DB
}

// seedProject 直接插一个项目(满足运行外键),返回 project id。
func seedProject(t *testing.T, db *sql.DB) string {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	credID := uuid.NewString()
	if _, err := db.Exec(
		`INSERT INTO credentials (id, name, type, scope, ciphertext, masked_value, created_at, updated_at)
		 VALUES (?, 'c', 'git_token', '', X'00', 'm', ?, ?)`,
		credID, now, now,
	); err != nil {
		t.Fatalf("seed credential: %v", err)
	}
	projID := uuid.NewString()
	if _, err := db.Exec(
		`INSERT INTO projects (id, name, repo_url, default_branch, credential_id, created_at, updated_at)
		 VALUES (?, 'acme', 'https://example.com/p.git', 'main', ?, ?, ?)`,
		projID, credID, now, now,
	); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return projID
}

// waitForStatus 轮询 Service.Get 直到 run 达到期望状态或超时(测试用,非生产路径)。
func waitForStatus(t *testing.T, svc Service, id, want string) *Run {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r, err := svc.Get(context.Background(), id)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if r.Status == want {
			return r
		}
		time.Sleep(5 * time.Millisecond)
	}
	r, _ := svc.Get(context.Background(), id)
	t.Fatalf("run %s 未在期限内达到 %q,当前 %q", id, want, r.Status)
	return nil
}

// TestCreateScheduleSuccess 验证 Create→worker 调度→success 全流程 + 步骤落库。
func TestCreateScheduleSuccess(t *testing.T) {
	db := testDB(t)
	svc := New(db)
	pool := NewWorkerPool(svc, WithConcurrency(2))
	pool.Start()
	t.Cleanup(func() { pool.Stop(context.Background()) })

	projID := seedProject(t, db)
	r, err := svc.Create(context.Background(), projID, Trigger{Type: TriggerManual, Branch: "main", Actor: "admin"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if r.Status != StatusQueued {
		t.Fatalf("初始状态应为 queued, got %q", r.Status)
	}

	done := waitForStatus(t, svc, r.ID, StatusSuccess)
	if done.StartedAt == nil || done.FinishedAt == nil {
		t.Fatalf("success run 应有 started/finished 时间")
	}
	if len(done.Steps) != 3 {
		t.Fatalf("默认桩 runner 应跑 3 步, got %d", len(done.Steps))
	}
	for _, s := range done.Steps {
		if s.Status != StepSuccess {
			t.Fatalf("步骤 %q 应 success, got %q", s.Name, s.Status)
		}
		if s.StartedAt == nil || s.FinishedAt == nil {
			t.Fatalf("步骤 %q 应有起止时间", s.Name)
		}
	}
}

// TestConcurrentRunsIndependent 验证并发多 run 独立:一个失败不影响另一个成功。
func TestConcurrentRunsIndependent(t *testing.T) {
	db := testDB(t)
	svc := New(db)
	// 用注入的可控 runner:按 trigger.Branch 决定成功/失败。
	pool := NewWorkerPool(svc, WithConcurrency(4), WithRunner(&branchRunner{}))
	pool.Start()
	t.Cleanup(func() { pool.Stop(context.Background()) })

	projID := seedProject(t, db)
	okRun, _ := svc.Create(context.Background(), projID, Trigger{Type: TriggerManual, Branch: "ok"})
	failRun, _ := svc.Create(context.Background(), projID, Trigger{Type: TriggerManual, Branch: "fail"})

	gotOK := waitForStatus(t, svc, okRun.ID, StatusSuccess)
	gotFail := waitForStatus(t, svc, failRun.ID, StatusFailed)

	if gotOK.Status != StatusSuccess {
		t.Fatalf("ok run 应 success")
	}
	if gotFail.Status != StatusFailed {
		t.Fatalf("fail run 应 failed")
	}
}

// branchRunner 按 r.Trigger.Branch=="fail" 注入失败,否则成功(快速,无 sleep)。
type branchRunner struct{}

func (branchRunner) Run(ctx context.Context, r *Run, sink StepSink) error {
	if err := sink.Plan(ctx, []StepDecl{{Name: "step1"}, {Name: "step2"}}); err != nil {
		return err
	}
	for i := 0; i < 2; i++ {
		_ = sink.StepRunning(ctx, i)
		if r.Trigger.Branch == "fail" && i == 1 {
			_ = sink.StepDone(ctx, i, StepFailed)
			return context.Canceled // 任意非 nil error → failed
		}
		_ = sink.StepDone(ctx, i, StepSuccess)
	}
	return nil
}

// TestInvalidTransitionRejected 验证非法状态转移被拒。
func TestInvalidTransitionRejected(t *testing.T) {
	db := testDB(t)
	svc := newService(db)
	projID := seedProject(t, db)

	r, err := svc.Create(context.Background(), projID, Trigger{Type: TriggerManual})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// queued → success 非法(必须经 running)。
	if err := svc.transition(context.Background(), r.ID, StatusQueued, StatusSuccess, true); err == nil {
		t.Fatal("queued→success 应被拒")
	}
	// success(终态)→ running 非法。
	if err := svc.transition(context.Background(), r.ID, StatusSuccess, StatusRunning, false); err == nil {
		t.Fatal("终态→running 应被拒")
	}
	// 合法:queued→running→success。
	if err := svc.transition(context.Background(), r.ID, StatusQueued, StatusRunning, true); err != nil {
		t.Fatalf("queued→running 应合法: %v", err)
	}
	if err := svc.transition(context.Background(), r.ID, StatusRunning, StatusSuccess, true); err != nil {
		t.Fatalf("running→success 应合法: %v", err)
	}
}

// TestCancelQueued 验证取消尚未调度的运行 → failed。
func TestCancelQueued(t *testing.T) {
	db := testDB(t)
	svc := newService(db) // 无 pool:Create 仅入库,run 保持 queued。
	projID := seedProject(t, db)

	r, err := svc.Create(context.Background(), projID, Trigger{Type: TriggerManual})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := svc.Cancel(context.Background(), r.ID)
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if got.Status != StatusFailed {
		t.Fatalf("取消 queued 应为 failed, got %q", got.Status)
	}
	// 终态再取消 → ErrNotCancelable。
	if _, err := svc.Cancel(context.Background(), r.ID); err == nil {
		t.Fatal("终态取消应被拒")
	}
}

// TestCancelRunning 验证取消运行中:context 传播到 runner,最终 failed。
func TestCancelRunning(t *testing.T) {
	db := testDB(t)
	svc := New(db)
	gate := make(chan struct{})
	pool := NewWorkerPool(svc, WithRunner(&blockingRunner{gate: gate}))
	pool.Start()
	t.Cleanup(func() { pool.Stop(context.Background()) })

	projID := seedProject(t, db)
	r, _ := svc.Create(context.Background(), projID, Trigger{Type: TriggerManual})

	// 等到 run 进入 running。
	waitForStatus(t, svc, r.ID, StatusRunning)
	if _, err := svc.Cancel(context.Background(), r.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	close(gate) // 放行 runner(它应已观察到取消)
	got := waitForStatus(t, svc, r.ID, StatusFailed)
	if got.Status != StatusFailed {
		t.Fatalf("取消 running 应为 failed, got %q", got.Status)
	}
}

// blockingRunner 在第一步后阻塞直到 ctx 取消或 gate 关闭,用于测试运行中取消。
type blockingRunner struct{ gate chan struct{} }

func (b *blockingRunner) Run(ctx context.Context, _ *Run, sink StepSink) error {
	if err := sink.Plan(ctx, []StepDecl{{Name: "long"}}); err != nil {
		return err
	}
	_ = sink.StepRunning(ctx, 0)
	select {
	case <-ctx.Done():
		_ = sink.StepDone(ctx, 0, StepFailed)
		return ErrCanceled
	case <-b.gate:
		select {
		case <-ctx.Done():
			_ = sink.StepDone(ctx, 0, StepFailed)
			return ErrCanceled
		default:
			_ = sink.StepDone(ctx, 0, StepSuccess)
			return nil
		}
	}
}

// TestListFilterAndPaging 验证列表筛选/分页。
func TestListFilterAndPaging(t *testing.T) {
	db := testDB(t)
	svc := newService(db) // 无 pool:全部保持 queued,便于断言。
	projA := seedProject(t, db)
	projB := seedProject(t, db)

	for i := 0; i < 3; i++ {
		if _, err := svc.Create(context.Background(), projA, Trigger{Type: TriggerManual}); err != nil {
			t.Fatalf("Create A: %v", err)
		}
	}
	if _, err := svc.Create(context.Background(), projB, Trigger{Type: TriggerManual}); err != nil {
		t.Fatalf("Create B: %v", err)
	}

	// 按 projectId 筛选。
	resA, err := svc.List(context.Background(), ListFilter{ProjectID: projA})
	if err != nil {
		t.Fatalf("List A: %v", err)
	}
	if resA.Total != 3 || len(resA.Items) != 3 {
		t.Fatalf("projA 应 3 条, got total=%d items=%d", resA.Total, len(resA.Items))
	}

	// 按 status 筛选(全部 queued)。
	resQ, err := svc.List(context.Background(), ListFilter{Status: StatusQueued})
	if err != nil {
		t.Fatalf("List queued: %v", err)
	}
	if resQ.Total != 4 {
		t.Fatalf("queued 总数应 4, got %d", resQ.Total)
	}

	// 分页:页大小 2,projA 共 3 条 → 第 1 页 2 条,第 2 页 1 条。
	p1, _ := svc.List(context.Background(), ListFilter{ProjectID: projA, Page: 1, PageSize: 2})
	if len(p1.Items) != 2 || p1.Total != 3 || p1.Page != 1 {
		t.Fatalf("第1页应 2 条/total3/page1, got items=%d total=%d page=%d", len(p1.Items), p1.Total, p1.Page)
	}
	p2, _ := svc.List(context.Background(), ListFilter{ProjectID: projA, Page: 2, PageSize: 2})
	if len(p2.Items) != 1 {
		t.Fatalf("第2页应 1 条, got %d", len(p2.Items))
	}

	// 非法 status → ErrInvalidStatus。
	if _, err := svc.List(context.Background(), ListFilter{Status: "bogus"}); err == nil {
		t.Fatal("非法 status 应被拒")
	}
}

// TestEnqueueNonBlocking 验证 Enqueue 队列满时返回 ErrQueueFull 而非阻塞(不挂死 Create)。
func TestEnqueueNonBlocking(t *testing.T) {
	db := testDB(t)
	svc := New(db)
	pool := NewWorkerPool(svc) // 不 Start:dispatcher 不消费,队列会被填满。

	// 填满待调度 FIFO(深度 maxQueueDepth)。
	for i := 0; i < maxQueueDepth; i++ {
		if err := pool.Enqueue(uuid.NewString()); err != nil {
			t.Fatalf("第 %d 次入队应成功: %v", i, err)
		}
	}
	// 再入一个:队列满 → ErrQueueFull(不阻塞)。
	done := make(chan error, 1)
	go func() { done <- pool.Enqueue(uuid.NewString()) }()
	select {
	case err := <-done:
		if !errors.Is(err, ErrQueueFull) {
			t.Fatalf("队列满应返回 ErrQueueFull, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Enqueue 队列满时阻塞了(应立即返回 ErrQueueFull)")
	}
}

// TestEnqueueAfterStop 验证池停机后 Enqueue 返回 ErrPoolStopped(不阻塞)。
func TestEnqueueAfterStop(t *testing.T) {
	db := testDB(t)
	svc := New(db)
	pool := NewWorkerPool(svc)
	pool.Start()
	pool.Stop(context.Background())

	done := make(chan error, 1)
	go func() { done <- pool.Enqueue(uuid.NewString()) }()
	select {
	case err := <-done:
		if !errors.Is(err, ErrPoolStopped) {
			t.Fatalf("停机后应返回 ErrPoolStopped, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Enqueue 停机后阻塞了(应立即返回 ErrPoolStopped)")
	}
}

// TestReconcileHangingSteps 验证 run 落终态后悬挂(running)步骤被校正,无残留非终态步骤。
func TestReconcileHangingSteps(t *testing.T) {
	db := testDB(t)
	svc := New(db)
	// hangingRunner:置步骤 running 后不落终态(模拟 StepDone 失败/漏置)却让 run 成功返回。
	pool := NewWorkerPool(svc, WithRunner(&hangingRunner{}))
	pool.Start()
	t.Cleanup(func() { pool.Stop(context.Background()) })

	projID := seedProject(t, db)
	r, _ := svc.Create(context.Background(), projID, Trigger{Type: TriggerManual})
	done := waitForStatus(t, svc, r.ID, StatusSuccess)
	for _, s := range done.Steps {
		if !IsStepTerminal(s.Status) {
			t.Fatalf("run 终态后步骤 %q 仍非终态 %q(悬挂未校正)", s.Name, s.Status)
		}
	}
}

// hangingRunner 置首步 running 后既不落终态、也不报错(run 会被判 success),留悬挂步骤供校正测试。
type hangingRunner struct{}

func (hangingRunner) Run(ctx context.Context, _ *Run, sink StepSink) error {
	if err := sink.Plan(ctx, []StepDecl{{Name: "hang"}}); err != nil {
		return err
	}
	_ = sink.StepRunning(ctx, 0)
	return nil // 不调 StepDone:步骤永远 running,除非 reconcile 校正。
}

// TestCreateUnknownProject 验证创建引用不存在项目 → ErrProjectNotFound。
func TestCreateUnknownProject(t *testing.T) {
	db := testDB(t)
	svc := New(db)
	if _, err := svc.Create(context.Background(), uuid.NewString(), Trigger{Type: TriggerManual}); err == nil {
		t.Fatal("未知项目应被拒")
	}
}

// TestSubscribeReceivesEvents 验证事件总线在调度时推出状态/步骤事件(SSE 数据源)。
func TestSubscribeReceivesEvents(t *testing.T) {
	db := testDB(t)
	svc := newService(db) // 无 pool 自动入队:手动控制订阅先于调度,避免错过事件。
	pool := NewWorkerPool(svc)
	// 不调 Start;改为先创建+订阅,再启动 worker,确保订阅早于事件产生。
	projID := seedProject(t, db)

	// newService + NewWorkerPool 已注入 enqueue;但未 Start 前 worker 不消费队列。
	r, _ := svc.Create(context.Background(), projID, Trigger{Type: TriggerManual})
	events, cancel := pool.Subscribe(r.ID)
	defer cancel()

	pool.Start()
	t.Cleanup(func() { pool.Stop(context.Background()) })

	sawStatus := false
	sawStep := false
	deadline := time.After(2 * time.Second)
	for !(sawStatus && sawStep) {
		select {
		case ev := <-events:
			if ev.Kind == EventStatus {
				sawStatus = true
			}
			if ev.Kind == EventStep {
				sawStep = true
			}
		case <-deadline:
			t.Fatalf("未在期限内收到 status+step 事件 (status=%v step=%v)", sawStatus, sawStep)
		}
	}
}
