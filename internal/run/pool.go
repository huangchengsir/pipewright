package run

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
)

// defaultConcurrency 是 worker pool 默认并发上限(有界;NFR-4 内存约束)。
const defaultConcurrency = 4

// WorkerPool 是进程内 goroutine 池调度器:从内存队列取 queued 运行 → 转 running →
// 经 Runner 执行 → 按结果转终态。多 run 并行、非事务、结果独立:单 run 失败/panic
// 不连累其它(每 run 独立 goroutine + recover)。优雅停机随 HTTP server(Stop)。
//
// 不轮询 DB 取队列:Create 经 Enqueue 推入内存 channel;状态变更经事件总线推 SSE。
type WorkerPool struct {
	svc    *service
	runner Runner
	queue  chan string

	workers int
	wg      sync.WaitGroup

	startOnce sync.Once
	stopOnce  sync.Once
	ctx       context.Context
	cancel    context.CancelFunc
}

// PoolOption 配置 WorkerPool。
type PoolOption func(*WorkerPool)

// WithConcurrency 设置并发上限(<1 时用默认)。
func WithConcurrency(n int) PoolOption {
	return func(p *WorkerPool) {
		if n > 0 {
			p.workers = n
		}
	}
}

// WithRunner 注入 Runner(默认 StubRunner;真实构建=3-3 换实现)。
func WithRunner(r Runner) PoolOption {
	return func(p *WorkerPool) {
		if r != nil {
			p.runner = r
		}
	}
}

// NewWorkerPool 构造池(需 New 返回的 *service;经类型断言取内部句柄)。
// 不在此启动 goroutine(无 init 重活);调用 Start 才启动 worker。
func NewWorkerPool(svc Service, opts ...PoolOption) *WorkerPool {
	s, ok := svc.(*service)
	if !ok {
		// New 始终返回 *service;此分支仅防御。
		panic("run: NewWorkerPool requires the Service returned by run.New")
	}
	p := &WorkerPool{
		svc:     s,
		runner:  NewStubRunner(),
		queue:   make(chan string, 256),
		workers: defaultConcurrency,
	}
	for _, opt := range opts {
		opt(p)
	}
	// 注入调度回调:Service.Create 入库后据此把 run 推入队列。
	s.setEnqueue(p.Enqueue)
	return p
}

// Subscribe 暴露事件总线订阅给 HTTP/SSE 层(针对单个 runID)。
func (p *WorkerPool) Subscribe(runID string) (<-chan Event, func()) {
	return p.svc.bus.subscribe(runID)
}

// Enqueue 把已入库的 queued 运行推入调度队列(永不阻塞调用方)。
// 由 Service.Create 之后调用(或测试直接调用)。
//
// 绝不阻塞:队列满或池已停机时返回错误(ErrQueueFull / ErrPoolStopped),
// 让 Create→HTTP handler 能失败返回而非永久挂死(突发 >256 创建 / 停机后入队)。
// run 仍在库中保持 queued,可经后续重启/重试调度。
func (p *WorkerPool) Enqueue(runID string) error {
	// 未 Start 时 ctx 为 nil:仅尝试非阻塞投递(测试场景先 Create+订阅再 Start)。
	if p.ctx == nil {
		select {
		case p.queue <- runID:
			return nil
		default:
			return ErrQueueFull
		}
	}
	select {
	case <-p.ctx.Done():
		return ErrPoolStopped
	default:
	}
	select {
	case p.queue <- runID:
		return nil
	case <-p.ctx.Done():
		return ErrPoolStopped
	default:
		return ErrQueueFull
	}
}

// Start 启动 worker goroutine(幂等)。停机用 Stop。
func (p *WorkerPool) Start() {
	p.startOnce.Do(func() {
		p.ctx, p.cancel = context.WithCancel(context.Background())
		for i := 0; i < p.workers; i++ {
			p.wg.Add(1)
			go p.worker()
		}
	})
}

// Stop 优雅停机:停止取新活、取消在途运行执行、等 worker 退出。随 HTTP server 调用。
func (p *WorkerPool) Stop(ctx context.Context) {
	p.stopOnce.Do(func() {
		if p.cancel != nil {
			p.cancel() // 取消池上下文:worker 退出;在途 run 经派生 ctx 取消
		}
	})
	done := make(chan struct{})
	go func() { p.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		// 超时:放弃等待(in-flight run 已收到取消信号)。
	}
}

// worker 是单个 goroutine 循环:取队列 → 执行一次运行。池上下文取消则退出。
func (p *WorkerPool) worker() {
	defer p.wg.Done()
	for {
		select {
		case <-p.ctx.Done():
			return
		case runID := <-p.queue:
			p.execute(runID)
		}
	}
}

// execute 执行单次运行的完整生命周期:queued→running→(Runner)→终态。
// 全程 recover:单 run panic 不连累 worker/其它 run(结果独立)。
func (p *WorkerPool) execute(runID string) {
	// 每 run 独立可取消上下文(派生自池 ctx),注册取消句柄供 Service.Cancel 传播。
	runCtx, cancel := context.WithCancel(p.ctx)
	p.svc.mu.Lock()
	p.svc.cancels[runID] = cancel
	p.svc.mu.Unlock()
	defer func() {
		cancel()
		p.svc.mu.Lock()
		delete(p.svc.cancels, runID)
		p.svc.mu.Unlock()
	}()

	sink := &dbStepSink{svc: p.svc, runID: runID}

	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("[run] worker recovered from panic on run %s: %v", runID, rec)
			// 幂等置 failed:按当前状态合法转移(panic 可能发生在转 running 之前,
			// 此时来源仍是 queued,硬编码 running→failed 会破坏语义/RowsAffected=0)。
			p.svc.failToTerminal(runID)
			// 校正悬挂步骤(panic 可能留下 running/pending 步骤),与 run=failed 一致。
			sink.reconcile(StatusFailed)
		}
	}()

	// queued → running(CAS:若已被取消为 failed 则放弃)。
	if err := p.svc.transition(runCtx, runID, StatusQueued, StatusRunning, true); err != nil {
		if errors.Is(err, ErrInvalidTransition) {
			return // 已被取消/非队列态:跳过。
		}
		log.Printf("[run] run %s: to running failed: %v", runID, err)
		return
	}

	r, err := p.svc.Get(runCtx, runID)
	if err != nil {
		log.Printf("[run] run %s: load failed: %v", runID, err)
		p.svc.failToTerminal(runID)
		sink.reconcile(StatusFailed)
		return
	}

	runErr := p.runner.Run(runCtx, r, sink)

	final := StatusSuccess
	if runErr != nil {
		final = StatusFailed
	}
	// 用后台 ctx 落终态:取消场景下派生 ctx 已 Done,仍须持久化终态。
	if err := p.svc.transition(context.Background(), runID, StatusRunning, final, false); err != nil {
		log.Printf("[run] run %s: to %s failed: %v", runID, final, err)
	}
	// 落终态后校正所有非终态步骤(StepDone 失败/runner 漏置不致留下"run=终态但 step 永远 running")。
	sink.reconcile(final)
}

// dbStepSink 是 StepSink 实现:把 Runner 的步骤进展持久化到 run_steps，
// 并经事件总线发布 step 事件(供 SSE)。步骤 id 在 Plan 时分配并缓存。
type dbStepSink struct {
	svc     *service
	runID   string
	stepIDs []string
	names   []string
}

func (d *dbStepSink) Plan(ctx context.Context, names []string) error {
	d.names = names
	d.stepIDs = make([]string, len(names))
	for i, name := range names {
		id := uuid.NewString()
		d.stepIDs[i] = id
		_, err := d.svc.db.ExecContext(ctx,
			`INSERT INTO run_steps (id, run_id, name, status, ordinal, started_at, finished_at)
			 VALUES (?, ?, ?, ?, ?, NULL, NULL)`,
			id, d.runID, name, StepPending, i,
		)
		if err != nil {
			return fmt.Errorf("run: insert step: %w", err)
		}
		d.publish(i, StepPending, nil, nil)
	}
	return nil
}

func (d *dbStepSink) StepRunning(ctx context.Context, ordinal int) error {
	if ordinal < 0 || ordinal >= len(d.stepIDs) {
		return fmt.Errorf("run: step ordinal out of range: %d", ordinal)
	}
	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339)
	_, err := d.svc.db.ExecContext(ctx,
		`UPDATE run_steps SET status = ?, started_at = ? WHERE id = ?`,
		StepRunning, nowStr, d.stepIDs[ordinal],
	)
	if err != nil {
		return fmt.Errorf("run: step running: %w", err)
	}
	d.publish(ordinal, StepRunning, &now, nil)
	return nil
}

func (d *dbStepSink) StepDone(ctx context.Context, ordinal int, status string) error {
	if ordinal < 0 || ordinal >= len(d.stepIDs) {
		return fmt.Errorf("run: step ordinal out of range: %d", ordinal)
	}
	switch status {
	case StepSuccess, StepFailed, StepSkipped:
	default:
		return fmt.Errorf("run: invalid step terminal status: %s", status)
	}
	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339)
	// 用后台 ctx 落库以容忍取消场景(失败步骤须被持久化)。
	_, err := d.svc.db.ExecContext(context.Background(),
		`UPDATE run_steps SET status = ?, finished_at = ? WHERE id = ?`,
		status, nowStr, d.stepIDs[ordinal],
	)
	if err != nil {
		return fmt.Errorf("run: step done: %w", err)
	}
	d.publish(ordinal, status, nil, &now)
	return nil
}

// reconcile 在 run 落终态后校正所有仍处于非终态(pending/running)的步骤,
// 避免"run=success/failed 但某 step 永远 running/pending"的不一致(StepDone 失败/runner 漏置)。
//
//   - run 失败:残留非终态步骤 → failed(running 视为被中断,pending 同样标 failed 保守)。
//   - run 成功:残留非终态步骤 → skipped(成功路径不应有漏置;标 skipped 而非 success 以示异常)。
//
// 直接按 run_id 查 DB 校正(不依赖 stepIDs 缓存:panic 可能发生在 Plan 之前/之后),
// 并对每个被校正步骤经事件总线补发 step 事件,使 SSE 订阅者收敛到一致终态。
func (d *dbStepSink) reconcile(runFinal string) {
	target := StepFailed
	if runFinal == StatusSuccess {
		target = StepSkipped
	}
	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339)

	// 用后台 ctx:取消场景下派生 ctx 已 Done,校正仍须持久化。
	rows, err := d.svc.db.QueryContext(context.Background(),
		`SELECT id, name, ordinal FROM run_steps
		 WHERE run_id = ? AND status NOT IN (?, ?, ?)`,
		d.runID, StepSuccess, StepFailed, StepSkipped)
	if err != nil {
		log.Printf("[run] run %s: reconcile load steps failed: %v", d.runID, err)
		return
	}
	type pending struct {
		id      string
		name    string
		ordinal int
	}
	var stuck []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.name, &p.ordinal); err != nil {
			log.Printf("[run] run %s: reconcile scan step failed: %v", d.runID, err)
			_ = rows.Close()
			return
		}
		stuck = append(stuck, p)
	}
	_ = rows.Close()

	for _, p := range stuck {
		if _, err := d.svc.db.ExecContext(context.Background(),
			`UPDATE run_steps SET status = ?, finished_at = COALESCE(finished_at, ?) WHERE id = ?`,
			target, nowStr, p.id,
		); err != nil {
			log.Printf("[run] run %s: reconcile step %s failed: %v", d.runID, p.id, err)
			continue
		}
		d.svc.bus.publish(Event{
			Kind:   EventStep,
			RunID:  d.runID,
			Status: target,
			Step: Step{
				ID:         p.id,
				Name:       p.name,
				Status:     target,
				Ordinal:    p.ordinal,
				FinishedAt: &now,
			},
		})
	}
}

// publish 经事件总线发布一个 step 事件(携带步骤快照,无日志正文)。
func (d *dbStepSink) publish(ordinal int, status string, started, finished *time.Time) {
	name := ""
	id := ""
	if ordinal >= 0 && ordinal < len(d.names) {
		name = d.names[ordinal]
	}
	if ordinal >= 0 && ordinal < len(d.stepIDs) {
		id = d.stepIDs[ordinal]
	}
	d.svc.bus.publish(Event{
		Kind:   EventStep,
		RunID:  d.runID,
		Status: status,
		Step: Step{
			ID:         id,
			Name:       name,
			Status:     status,
			Ordinal:    ordinal,
			StartedAt:  started,
			FinishedAt: finished,
		},
	})
}
