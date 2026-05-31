package run

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// gatedRunner 在进入 Run 时经 entered 通知,然后阻塞在 release 上(或 ctx 取消)。
// 用于精确控制「同时有几个 run 在执行中」,无真实 sleep(不 flake)。
// 进入时记录峰值并发(经 mu 保护),供断言「上限成立」。
type gatedRunner struct {
	entered chan struct{} // 每个 run 进入 Run 时投递一次
	release chan struct{} // 关闭后放行全部阻塞中的 run
	mu      sync.Mutex
	cur     int
	peak    int
}

func (g *gatedRunner) Run(ctx context.Context, _ *Run, sink StepSink) error {
	g.mu.Lock()
	g.cur++
	if g.cur > g.peak {
		g.peak = g.cur
	}
	g.mu.Unlock()

	g.entered <- struct{}{}
	select {
	case <-g.release:
	case <-ctx.Done():
	}

	g.mu.Lock()
	g.cur--
	g.mu.Unlock()
	if err := sink.Plan(ctx, []string{"s"}); err != nil {
		return err
	}
	_ = sink.StepRunning(ctx, 0)
	_ = sink.StepDone(ctx, 0, StepSuccess)
	return nil
}

func (g *gatedRunner) peakConcurrency() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.peak
}

// drainEntered 在期限内尝试收 want 个「已进入执行」信号,返回实际收到数(超时即返回)。
func drainEntered(t *testing.T, g *gatedRunner, want int, timeout time.Duration) int {
	t.Helper()
	got := 0
	deadline := time.After(timeout)
	for got < want {
		select {
		case <-g.entered:
			got++
		case <-deadline:
			return got
		}
	}
	return got
}

// TestGlobalConcurrencyLimitHolds 验证全局上限成立:提交 N>limit 个 run,断言同时执行的
// run 数绝不超过 globalLimit(其余保持 queued 排队)。用 gatedRunner 卡住执行中 run,
// 精确观察峰值并发,无真实 sleep(确定性、不 flake)。
func TestGlobalConcurrencyLimitHolds(t *testing.T) {
	const limit = 2
	const total = 6

	db := testDB(t)
	svc := New(db)
	g := &gatedRunner{entered: make(chan struct{}, total), release: make(chan struct{})}
	pool := NewWorkerPool(svc, WithMaxConcurrent(limit), WithRunner(g))
	pool.Start()
	t.Cleanup(func() {
		safeClose(g.release)
		pool.Stop(context.Background())
	})

	projID := seedProject(t, db)
	ids := make([]string, 0, total)
	for i := 0; i < total; i++ {
		r, err := svc.Create(context.Background(), projID, Trigger{Type: TriggerManual})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		ids = append(ids, r.ID)
	}

	// 至多 limit 个会进入执行;断言恰好 limit 个进入、不多一个。
	if got := drainEntered(t, g, limit, time.Second); got != limit {
		t.Fatalf("应有 %d 个 run 进入执行, got %d", limit, got)
	}
	// 再尝试收第 (limit+1) 个进入信号:应超时(被排队挡住,绝不超限)。
	if extra := drainEntered(t, g, 1, 150*time.Millisecond); extra != 0 {
		t.Fatalf("超过全局上限 %d 仍有 run 进入执行(读到额外 %d 个)", limit, extra)
	}
	if peak := g.peakConcurrency(); peak > limit {
		t.Fatalf("峰值并发 %d 超过全局上限 %d", peak, limit)
	}

	// 放行:全部应陆续完成为 success(排队者递补)。safeClose 幂等(Cleanup 再调跳过)。
	safeClose(g.release)
	for _, id := range ids {
		waitForStatus(t, svc, id, StatusSuccess)
	}
	if peak := g.peakConcurrency(); peak > limit {
		t.Fatalf("全程峰值并发 %d 超过全局上限 %d", peak, limit)
	}
}

// fakeLimits 是 ConcurrencyLimits 的内存 fake:按 projectID 返回固定上限。
type fakeLimits map[string]int

func (f fakeLimits) LimitFor(_ context.Context, projectID string) int { return f[projectID] }

// TestPerProjectConcurrencyLimitHolds 验证项目级上限成立:两个项目,A 限 1、B 不限。
// 同时提交 A 的多个 run + B 的多个 run;断言 A 同时至多 1 个在执行,B 不受 A 拖累照常放行
// (受全局上限约束)。证明项目级准入正确且不发生队头阻塞。
func TestPerProjectConcurrencyLimitHolds(t *testing.T) {
	db := testDB(t)
	svc := New(db)
	g := &gatedRunner{entered: make(chan struct{}, 16), release: make(chan struct{})}

	projA := seedProject(t, db)
	projB := seedProject(t, db)
	limits := fakeLimits{projA: 1} // A 限 1;B 缺省 0 = 不限项目级。

	// 全局上限给足(4),让项目级成为约束源。
	pool := NewWorkerPool(svc, WithMaxConcurrent(4), WithRunner(g), WithConcurrencyLimits(limits))
	pool.Start()
	t.Cleanup(func() {
		safeClose(g.release)
		pool.Stop(context.Background())
	})

	// 先入 3 个 A(项目级限 1),再入 2 个 B(不限,受全局 4 约束)。
	var aIDs, bIDs []string
	for i := 0; i < 3; i++ {
		r, _ := svc.Create(context.Background(), projA, Trigger{Type: TriggerManual})
		aIDs = append(aIDs, r.ID)
	}
	for i := 0; i < 2; i++ {
		r, _ := svc.Create(context.Background(), projB, Trigger{Type: TriggerManual})
		bIDs = append(bIDs, r.ID)
	}

	// 全局有 4 槽:A 占 1(项目限),B 占 2 → 共 3 个进入执行。断言恰好 3 个、不更多。
	if got := drainEntered(t, g, 3, time.Second); got != 3 {
		t.Fatalf("应有 3 个 run 进入执行(A=1 + B=2), got %d", got)
	}
	if extra := drainEntered(t, g, 1, 150*time.Millisecond); extra != 0 {
		t.Fatalf("项目级/全局上限被突破(读到额外 %d 个)", extra)
	}

	// 断言执行中恰有 1 个属于 A(项目级上限成立),其余属于 B。
	assertRunningCountForProject(t, svc, projA, 1)

	// 放行全部 → 排队者递补,全部 success;A 始终至多 1 个并发(峰值不超)。
	safeClose(g.release)
	all := append(append([]string{}, aIDs...), bIDs...)
	for _, id := range all {
		waitForStatus(t, svc, id, StatusSuccess)
	}
}

// assertRunningCountForProject 断言某项目当前处于 running 的运行数等于 want。
func assertRunningCountForProject(t *testing.T, svc Service, projectID string, want int) {
	t.Helper()
	res, err := svc.List(context.Background(), ListFilter{ProjectID: projectID, Status: StatusRunning, PageSize: 100})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if res.Total != want {
		t.Fatalf("项目 %s running 数应为 %d, got %d", projectID, want, res.Total)
	}
}

// safeCloseMu 串行化 safeClose 的检查-关闭,避免并发双关 panic。
var safeCloseMu sync.Mutex

// safeClose 幂等关闭 ch(测试中可能放行后又在 Cleanup 关闭)。
func safeClose(ch chan struct{}) {
	safeCloseMu.Lock()
	defer safeCloseMu.Unlock()
	select {
	case <-ch:
		// 已关闭(关闭的 channel 接收立即返回零值)。
	default:
		close(ch)
	}
}

// TestConcurrencyServiceSaveGet 验证项目并发配置的校验 + 持久化 + 回读。
func TestConcurrencyServiceSaveGet(t *testing.T) {
	db := testDB(t)
	svc := NewConcurrencyService(db)
	projID := seedProject(t, db)

	// 缺省未配置 → 0(不限项目级)。
	cfg, err := svc.Get(context.Background(), projID)
	if err != nil || cfg.MaxConcurrent != 0 {
		t.Fatalf("缺省应为 0,得 cfg=%+v err=%v", cfg, err)
	}

	// 保存合法值 → 回读一致。
	if _, err := svc.Save(context.Background(), projID, 3); err != nil {
		t.Fatalf("Save: %v", err)
	}
	cfg, _ = svc.Get(context.Background(), projID)
	if cfg.MaxConcurrent != 3 {
		t.Fatalf("应为 3, got %d", cfg.MaxConcurrent)
	}
	if svc.LimitFor(context.Background(), projID) != 3 {
		t.Fatalf("LimitFor 应为 3")
	}

	// 非法值被拒。
	if _, err := svc.Save(context.Background(), projID, -1); err != ErrInvalidConcurrency {
		t.Fatalf("负值应 ErrInvalidConcurrency, got %v", err)
	}
	if _, err := svc.Save(context.Background(), projID, maxProjectConcurrency+1); err != ErrInvalidConcurrency {
		t.Fatalf("超上界应 ErrInvalidConcurrency, got %v", err)
	}

	// 项目不存在 → ErrConcProjectNotFound。
	if _, err := svc.Save(context.Background(), uuid.NewString(), 2); err != ErrConcProjectNotFound {
		t.Fatalf("未知项目应 ErrConcProjectNotFound, got %v", err)
	}
}
