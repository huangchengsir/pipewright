package run

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/mask"
)

// defaultConcurrency 是 worker pool 默认并发上限(有界;NFR-4 内存约束)。
const defaultConcurrency = 4

// maxConcurrentDiagnoses 是 best-effort 自动诊断的并发上限(有界;NFR-4)。
// 自动诊断脱离 worker pool 在独立 goroutine 跑,若无界,AI 配置时批量失败会无界并发
// 打 LLM + 多份日志/解密 key 同时驻留。超限则跳过本次自动诊断(用户仍可手动触发)。
const maxConcurrentDiagnoses = 2

// maxConcurrentNotifies 是 best-effort 通知钩子的并发上限(有界;NFR-4;code-review P8 对齐 diagnose)。
// 通知脱离 worker pool 在独立 goroutine 跑,每个最长 ~8s(webhook client 超时)× 渠道数;
// 批量 run 同时落终态会瞬时堆起大量发送 goroutine 各占一条 HTTP 连接。超限跳过(通知 best-effort)。
const maxConcurrentNotifies = 4

// WorkerPool 是进程内 goroutine 池调度器:从内存 FIFO 队列取 queued 运行 → 转 running →
// 经 Runner 执行 → 按结果转终态。多 run 并行、非事务、结果独立:单 run 失败/panic
// 不连累其它(每 run 独立 goroutine + recover)。优雅停机随 HTTP server(Stop)。
//
// 不轮询 DB 取队列:Create 经 Enqueue 推入内存 FIFO;状态变更经事件总线推 SSE。
//
// 并发/队列控制(FR-8-10):单 dispatcher goroutine 按 FIFO 扫描待调度队列,对每个待调度
// 运行做**双重准入**——全局在途 < globalLimit(PIPEWRIGHT_MAX_CONCURRENT)且 该项目在途 <
// 项目级上限(ConcurrencyLimits.LimitFor,0=不限)。准入则计数 +1 并起独立 goroutine 执行;
// 否则保持 queued 留在队列,待某运行收尾释放槽位后重扫(FIFO 序;某项目满则跳过该项目的
// 后续运行继续放行其它项目,避免队头阻塞拖垮全局吞吐)。计数器经 mu 串行化(并发正确)。
type WorkerPool struct {
	svc    *service
	runner Runner

	// 待调度 FIFO:Enqueue 追加 runID,dispatcher 按序做准入并出队。受 mu 保护。
	pending []string
	// signal 唤醒 dispatcher 重扫(Enqueue 入队 / run 收尾释放槽位时各发一次,带缓冲不阻塞)。
	signal chan struct{}

	// globalLimit 是全局同时 running 上限(PIPEWRIGHT_MAX_CONCURRENT;<1 时回落 workers)。
	globalLimit int
	// limits 查项目级并发上限(nil 则仅受全局上限约束);经接口解耦,pool 不直接触库。
	limits ConcurrencyLimits

	// mu 保护 pending / globalInflight / projInflight / stopped(并发计数正确性铁律)。
	mu             sync.Mutex
	globalInflight int
	projInflight   map[string]int
	// stopped 在 Stop 后置位:Enqueue 返回 ErrPoolStopped、takeAdmissible 不再放行新活。
	stopped bool

	workers int
	wg      sync.WaitGroup

	// diagnoseHook 是 run→failed 后的 best-effort 自动诊断钩子(由 main 经 Option 注入)。
	// nil 则跳过(无 AI 时核心 CI/CD 不依赖此服务,NFR-10)。**run 包不 import ai**:
	// 钩子内部(在 main 装配)负责「取失败日志 → 脱敏 → ai.Diagnose → 保存」,仿 RunCreator 解耦。
	// 钩子失败仅记日志,**绝不影响 run 终态**。
	diagnoseHook func(ctx context.Context, runID string)
	// diagnoseSem 为自动诊断并发上限信号量(有界;NFR-4);满则跳过本次自动诊断。
	diagnoseSem chan struct{}
	// notifySem 为通知钩子并发上限信号量(有界;NFR-4;P8);满则跳过本次通知(best-effort)。
	notifySem chan struct{}

	// notifyHook 是 run 终态后的 best-effort 通知钩子(Story 5.2;由 main 经 Option 注入)。
	// nil 则跳过(未配通知时核心 CI/CD 不依赖,NFR-10)。**run 包不 import notify**:钩子内部
	// (在 main 装配)负责「run 终态 → event → 构 Payload → Router.RouteEvent」,仿 diagnoseHook 解耦。
	// 钩子在独立 goroutine + recover 中调用,失败仅记日志,**绝不阻断 run 终态**。
	notifyHook func(ctx context.Context, runID, finalStatus string)

	// logMasker 是日志行落库/出网前的**静态**脱敏器(AC-SEC-04;由 main 经 WithLogMasker 注入)。
	// nil 则 dbStepSink.Log 不脱敏(纯单测可省)。被 logMaskerFunc 优先(若设)。
	logMasker *mask.Masker
	// logMaskerFunc 是**按 run** 构建脱敏器的工厂(红线修;由 main 经 WithLogMaskerFunc 注入)。
	// 每个 run 执行时调一次构建 per-run masker(登记该 run 真实用到的凭据),供该 run 全部日志行复用。
	// 设了则优先于静态 logMasker;让真实凭据(非仅桩假 secret)在真实日志里也被脱敏。
	logMaskerFunc func(ctx context.Context, runID string) *mask.Masker

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

// WithMaxConcurrent 设置全局同时 running 上限(FR-8-10;PIPEWRIGHT_MAX_CONCURRENT)。
// <1 时不生效(回落到 workers 数)。突发创建超此上限的运行保持 queued,待槽位释放再调度。
func WithMaxConcurrent(n int) PoolOption {
	return func(p *WorkerPool) {
		if n > 0 {
			p.globalLimit = n
		}
	}
}

// WithConcurrencyLimits 注入项目级并发上限查询(FR-8-10)。nil 则仅受全局上限约束。
// dispatcher 对每个待调度运行经此查该项目上限(0=不限项目级)做准入。
func WithConcurrencyLimits(l ConcurrencyLimits) PoolOption {
	return func(p *WorkerPool) {
		if l != nil {
			p.limits = l
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

// WithLogMasker 注入日志脱敏器(Story 3.6 / AC-SEC-04)。dbStepSink.Log 在落 run_logs /
// 发 EventLog 前一律经此 Masker.Scrub,确保日志表/SSE/响应绝无明文 secret。
//
// 本期同 7-2 的 registerRunSecrets 债:只登记桩假 secret(StubFailureSecret 等),由 main
// 装配时登记;3-3/3-6 真实日志落地后改为从 vault 取该 run 实际凭据明文登记。m 为 nil 时不注入。
func WithLogMasker(m *mask.Masker) PoolOption {
	return func(p *WorkerPool) {
		if m != nil {
			p.logMasker = m
		}
	}
}

// WithLogMaskerFunc 注入**按 run** 的日志脱敏器工厂(红线修;优先于 WithLogMasker)。
// main 传 RunSecretSource.MaskerForRun:每个 run 执行时构建登记了该 run 真实凭据的 Masker,
// 让真实日志里的真凭据(非仅桩假 secret)也被脱敏。fn 为 nil 时不注入。
func WithLogMaskerFunc(fn func(ctx context.Context, runID string) *mask.Masker) PoolOption {
	return func(p *WorkerPool) {
		if fn != nil {
			p.logMaskerFunc = fn
		}
	}
}

// WithDiagnoseHook 注入 run→failed 后的 best-effort 自动诊断钩子(Story 7.2)。
// 由 main 装配(钩子内做「取失败日志 → 脱敏 → ai.Diagnose → 保存」),使 run 包不 import ai。
// 钩子在独立 goroutine 中调用、自带 recover + 超时:失败/超时仅记日志,绝不阻断 run 终态。
// fn 为 nil 时不注入(等价于无诊断)。
func WithDiagnoseHook(fn func(ctx context.Context, runID string)) PoolOption {
	return func(p *WorkerPool) {
		if fn != nil {
			p.diagnoseHook = fn
		}
	}
}

// WithNotifyHook 注入 run 终态后的 best-effort 通知钩子(Story 5.2;FR-20)。
// 由 main 装配(钩子内做「run 终态 → event → 构 Payload → notify.Router.RouteEvent」),
// 使 run 包不 import notify。钩子在独立 goroutine 中调用、自带 recover:失败仅记日志,
// 绝不阻断 run 终态(未配路由的事件不发送由 Router 内部保证)。fn 为 nil 时不注入。
func WithNotifyHook(fn func(ctx context.Context, runID, finalStatus string)) PoolOption {
	return func(p *WorkerPool) {
		if fn != nil {
			p.notifyHook = fn
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
		svc:          s,
		runner:       NewStubRunner(),
		signal:       make(chan struct{}, 1),
		projInflight: make(map[string]int),
		workers:      defaultConcurrency,
		diagnoseSem:  make(chan struct{}, maxConcurrentDiagnoses),
		notifySem:    make(chan struct{}, maxConcurrentNotifies),
	}
	for _, opt := range opts {
		opt(p)
	}
	// 全局上限缺省回落到 workers 数(保持既有「至多 workers 个并行」行为;FR-8-10 默认)。
	if p.globalLimit < 1 {
		p.globalLimit = p.workers
	}
	// 注入调度回调:Service.Create 入库后据此把 run 推入队列。
	s.setEnqueue(p.Enqueue)
	return p
}

// Subscribe 暴露事件总线订阅给 HTTP/SSE 层(针对单个 runID)。
func (p *WorkerPool) Subscribe(runID string) (<-chan Event, func()) {
	return p.svc.bus.subscribe(runID)
}

// maxQueueDepth 是待调度 FIFO 的有界深度(NFR-4 内存约束;突发创建超此返回 ErrQueueFull)。
const maxQueueDepth = 1024

// Enqueue 把已入库的 queued 运行追加到待调度 FIFO(永不阻塞调用方)。
// 由 Service.Create 之后调用(或测试直接调用)。
//
// 绝不阻塞:队列满或池已停机时返回错误(ErrQueueFull / ErrPoolStopped),
// 让 Create→HTTP handler 能失败返回而非永久挂死(突发 >maxQueueDepth 创建 / 停机后入队)。
// run 仍在库中保持 queued,可经后续重启/重试调度。
func (p *WorkerPool) Enqueue(runID string) error {
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return ErrPoolStopped
	}
	if len(p.pending) >= maxQueueDepth {
		p.mu.Unlock()
		return ErrQueueFull
	}
	p.pending = append(p.pending, runID)
	p.mu.Unlock()
	p.wake()
	return nil
}

// wake 非阻塞地唤醒 dispatcher 重扫待调度队列(signal 带缓冲 1:合并多次唤醒)。
func (p *WorkerPool) wake() {
	select {
	case p.signal <- struct{}{}:
	default:
	}
}

// Start 启动 dispatcher goroutine(幂等)。停机用 Stop。
func (p *WorkerPool) Start() {
	p.startOnce.Do(func() {
		p.ctx, p.cancel = context.WithCancel(context.Background())
		p.wg.Add(1)
		go p.dispatch()
		// Start 前可能已 Enqueue(测试/启动恢复):立即触发一次调度。
		p.wake()
	})
}

// Stop 优雅停机:停止取新活、取消在途运行执行、等在途 run 与 dispatcher 退出。随 HTTP server 调用。
func (p *WorkerPool) Stop(ctx context.Context) {
	p.stopOnce.Do(func() {
		p.mu.Lock()
		p.stopped = true
		p.mu.Unlock()
		if p.cancel != nil {
			p.cancel() // 取消池上下文:dispatcher 退出;在途 run 经派生 ctx 取消
		}
		p.wake() // 唤醒 dispatcher 让其观察到 ctx.Done 后退出
	})
	done := make(chan struct{})
	go func() { p.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		// 超时:放弃等待(in-flight run 已收到取消信号)。
	}
}

// dispatch 是单 dispatcher 循环:被唤醒后按 FIFO 扫描待调度队列,对每个运行做全局 + 项目级
// 双重准入;准入则计数并起独立 goroutine 执行,否则保持 queued 待下次唤醒重扫。
// 池上下文取消则退出(已起的在途 run 经其派生 ctx 取消、由 wg 等待收尾)。
func (p *WorkerPool) dispatch() {
	defer p.wg.Done()
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-p.signal:
			p.drain()
		}
	}
}

// drain 按 FIFO 扫描待调度队列一遍,放行所有当前可准入的运行(全局未满 且 项目未满);
// 某项目本轮已满则跳过其后续运行继续放行其它项目(避免队头阻塞)。受 mu 串行保证计数正确。
func (p *WorkerPool) drain() {
	for {
		runID, projectID, ok := p.takeAdmissible()
		if !ok {
			return
		}
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			defer p.release(projectID)
			p.execute(runID)
		}()
	}
}

// takeAdmissible 在锁内扫描 pending,取出**第一个**满足全局 + 项目级双重准入的运行并计数 +1;
// 找到返回 (runID, projectID, true) 并已从 pending 摘除;无可放行返回 ("","",false)。
//
// 全局已满 → 直接返回(本轮无人可放行)。某项目满则跳过其在 pending 中的后续运行继续往后找,
// 既保证项目级上限,又不让该项目的队头拖住其它项目(FIFO 序在各项目内仍保持)。
func (p *WorkerPool) takeAdmissible() (string, string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		return "", "", false
	}
	if p.globalInflight >= p.globalLimit {
		return "", "", false
	}
	for i, runID := range p.pending {
		projectID := p.projectIDFor(runID)
		limit := p.limitFor(projectID)
		if limit > 0 && p.projInflight[projectID] >= limit {
			continue // 该项目本轮已满:跳过,继续找其它项目的运行。
		}
		// 准入:计数 +1 并从 pending 摘除该元素(保持其余顺序)。
		p.globalInflight++
		p.projInflight[projectID]++
		p.pending = append(p.pending[:i], p.pending[i+1:]...)
		return runID, projectID, true
	}
	return "", "", false
}

// projectIDFor 查某 run 的 project_id(用于项目级准入)。读失败/不存在返回空串
// (空串项目不参与项目级限流,仅受全局约束;优雅降级,绝不卡死调度)。锁内调用、查询轻量。
func (p *WorkerPool) projectIDFor(runID string) string {
	var pid string
	_ = p.svc.db.QueryRowContext(context.Background(),
		`SELECT project_id FROM pipeline_runs WHERE id = ?`, runID).Scan(&pid)
	return pid
}

// limitFor 查项目级并发上限(未注入 limits 或空项目 → 0 不限项目级)。锁内调用,实现须无阻塞重活。
func (p *WorkerPool) limitFor(projectID string) int {
	if p.limits == nil || projectID == "" {
		return 0
	}
	return p.limits.LimitFor(context.Background(), projectID)
}

// release 在某运行收尾后释放全局 + 项目级计数,并唤醒 dispatcher 重扫(空出的槽位让排队者递补)。
func (p *WorkerPool) release(projectID string) {
	p.mu.Lock()
	if p.globalInflight > 0 {
		p.globalInflight--
	}
	if projectID != "" {
		if n := p.projInflight[projectID]; n > 0 {
			p.projInflight[projectID] = n - 1
		}
		if p.projInflight[projectID] == 0 {
			delete(p.projInflight, projectID)
		}
	}
	p.mu.Unlock()
	p.wake()
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

	// per-run 脱敏器:有工厂则按 run 构建(登记该 run 真实凭据),否则用静态 masker。构建一次、
	// 该 run 全部日志行复用(避免每行重解析/解密)。
	logMasker := p.logMasker
	if p.logMaskerFunc != nil {
		if m := p.logMaskerFunc(runCtx, runID); m != nil {
			logMasker = m
		}
	}
	sink := &dbStepSink{svc: p.svc, runID: runID, masker: logMasker}

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
	// **先**校正所有非终态步骤,**再**落 run 终态:保证「run 一旦观测到终态,其步骤必已一致」
	// (StepDone 失败/runner 漏置不致留下"run=终态但 step 永远 running")。顺序反了会有竞态窗口
	// ——观测方(SSE/轮询/waitForStatus)在 run 终态与步骤校正之间读到悬挂步骤。
	sink.reconcile(final)
	// 用后台 ctx 落终态:取消场景下派生 ctx 已 Done,仍须持久化终态。
	if err := p.svc.transition(context.Background(), runID, StatusRunning, final, false); err != nil {
		log.Printf("[run] run %s: to %s failed: %v", runID, final, err)
	}

	// run 落 failed 后触发 best-effort 自动诊断(已注入钩子时)。绝不阻断 run 终态:
	// 在独立 goroutine + recover 中调用,钩子失败仅记日志(NFR-10 优雅降级)。
	//
	// 取消复用 StatusFailed(无独立 StatusCanceled),故须排除「被取消/停机」的 failed:
	// 用户取消或 pool 停机会取消 runCtx(此处尚未触发 defer cancel),runCtx.Err()!=nil 即此类。
	// 这些 failed 非真实执行失败,自动诊断会误触发——白打 LLM + 解密 key,且诊断「被取消的 run」无意义。
	// (手动诊断仍可在详情页触发;真实失败的 runCtx.Err()==nil 不受影响。)
	if final == StatusFailed && p.diagnoseHook != nil && runCtx.Err() == nil {
		p.runDiagnoseHook(runID)
	}

	// run 落任意终态后触发 best-effort 通知(Story 5.2;已注入钩子时)。绝不阻断 run 终态:
	// 在独立 goroutine + recover 中调用,钩子失败仅记日志。未配路由的事件由 Router 内部保证不发送。
	if p.notifyHook != nil {
		p.runNotifyHook(runID, final)
	}
}

// runDiagnoseHook 在独立 goroutine 中调用注入的诊断钩子(纳入 WaitGroup 以便 Stop 等待),
// 自带 recover:钩子 panic/失败绝不连累 worker 或 run 终态。钩子内部自带超时/脱敏(在 main 装配)。
//
// 并发有界(NFR-4):经 diagnoseSem 信号量限流;try-acquire 失败(已达上限)则跳过本次自动诊断
// 并记日志——best-effort 语义不变,用户仍可在详情页手动触发诊断。
func (p *WorkerPool) runDiagnoseHook(runID string) {
	select {
	case p.diagnoseSem <- struct{}{}:
		// 取得槽位,继续。
	default:
		log.Printf("[run] run %s: auto-diagnose skipped (并发上限 %d 已满);手动诊断仍可用", runID, maxConcurrentDiagnoses)
		return
	}
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		defer func() { <-p.diagnoseSem }() // 释放槽位
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("[run] diagnose hook recovered from panic on run %s: %v", runID, rec)
			}
		}()
		p.diagnoseHook(p.ctx, runID)
	}()
}

// runNotifyHook 在独立 goroutine 中调用注入的通知钩子(纳入 WaitGroup 以便 Stop 等待),
// 自带 recover:钩子 panic/失败绝不连累 worker 或 run 终态。钩子内部自带超时(在 main 装配)。
//
// 通知本身经 Router 已是 best-effort(单渠道失败续发、未配路由不发);此处再套独立 goroutine +
// recover,确保「构 Payload / 调 RouteEvent」任何环节出错都不冒泡阻断 run 终态(NFR-10)。
func (p *WorkerPool) runNotifyHook(runID, finalStatus string) {
	// 并发有界(NFR-4;P8):try-acquire,满则跳过本次通知(best-effort 语义不变)。
	select {
	case p.notifySem <- struct{}{}:
	default:
		log.Printf("[run] run %s: notify skipped (并发上限 %d 已满)", runID, maxConcurrentNotifies)
		return
	}
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		defer func() { <-p.notifySem }() // 释放槽位
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("[run] notify hook recovered from panic on run %s: %v", runID, rec)
			}
		}()
		p.notifyHook(p.ctx, runID, finalStatus)
	}()
}

// dbStepSink 是 StepSink 实现:把 Runner 的步骤进展持久化到 run_steps，
// 并经事件总线发布 step 事件(供 SSE)。步骤 id 在 Plan 时分配并缓存。
type dbStepSink struct {
	svc     *service
	runID   string
	stepIDs []string
	names   []string
	stages  []string // 各 step 所属阶段名(节点级分组;与 names/stepIDs 同序)
	// masker 在日志行落库/出网前脱敏(AC-SEC-04);nil 则不脱敏(纯单测可省)。
	masker *mask.Masker
}

func (d *dbStepSink) Plan(ctx context.Context, steps []StepDecl) error {
	d.names = make([]string, len(steps))
	d.stages = make([]string, len(steps))
	d.stepIDs = make([]string, len(steps))
	for i, st := range steps {
		d.names[i] = st.Name
		d.stages[i] = st.Stage
		id := uuid.NewString()
		d.stepIDs[i] = id
		_, err := d.svc.db.ExecContext(ctx,
			`INSERT INTO run_steps (id, run_id, name, stage, status, ordinal, started_at, finished_at)
			 VALUES (?, ?, ?, ?, ?, ?, NULL, NULL)`,
			id, d.runID, st.Name, st.Stage, StepPending, i,
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

// SetFailureLog 持久化本次运行的失败日志原文(脱敏前)到 pipeline_runs.failure_log。
// 用后台 ctx 落库(取消场景下派生 ctx 已 Done,失败日志仍须持久化)。参数化 SQL。
func (d *dbStepSink) SetFailureLog(_ context.Context, logText string) error {
	_, err := d.svc.db.ExecContext(context.Background(),
		`UPDATE pipeline_runs SET failure_log = ? WHERE id = ?`, logText, d.runID)
	if err != nil {
		return fmt.Errorf("run: set failure log: %w", err)
	}
	return nil
}

// Log 实现 StepSink.Log(Story 3.6):**先脱敏**(注入的 mask.Masker)→ 落 run_logs(分配
// run 内单调 seq)→ 经事件总线发 EventLog(text 已脱敏)。AC-SEC-04:落库/出网前一律 Scrub,
// 桩假 secret 在 run_logs 表/SSE 中一律 [MASKED]、绝无明文。
//
// 用后台 ctx 落库以容忍取消场景(日志须被持久化);best-effort:落库失败返回 error 由
// 调用方(StubRunner)忽略,绝不阻断步骤/运行终态。
func (d *dbStepSink) Log(_ context.Context, stream string, stepOrdinal int, line string) error {
	text := line
	if d.masker != nil {
		text = d.masker.Scrub(line)
	}
	seq, err := d.svc.AppendLog(context.Background(), d.runID, stream, stepOrdinal, text)
	if err != nil {
		return fmt.Errorf("run: append log: %w", err)
	}
	d.svc.bus.publish(Event{
		Kind:  EventLog,
		RunID: d.runID,
		Log: LogLine{
			Seq:         seq,
			Ts:          time.Now().UTC(),
			Stream:      stream,
			StepOrdinal: stepOrdinal,
			Text:        text,
		},
	})
	return nil
}

// EmitArtifact 实现 StepSink.EmitArtifact(Story 3.4 / FR-6):把 runner 报告的构建产物落
// run_artifacts(经 service.AddArtifact)。RunID 由本 sink 填充(runner 无需关心)。
// 用后台 ctx 落库以容忍取消场景(成功产物须被持久化)。best-effort:落库失败返回 error 由
// 调用方(StubRunner)忽略,绝不阻断步骤/运行终态。3-3 真实构建经同一接口喂真实产物、契约不变。
func (d *dbStepSink) EmitArtifact(_ context.Context, a Artifact) error {
	a.RunID = d.runID
	if _, err := d.svc.AddArtifact(context.Background(), a); err != nil {
		return fmt.Errorf("run: emit artifact: %w", err)
	}
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
		`SELECT id, name, COALESCE(stage, ''), ordinal FROM run_steps
		 WHERE run_id = ? AND status NOT IN (?, ?, ?)`,
		d.runID, StepSuccess, StepFailed, StepSkipped)
	if err != nil {
		log.Printf("[run] run %s: reconcile load steps failed: %v", d.runID, err)
		return
	}
	type pending struct {
		id      string
		name    string
		stage   string
		ordinal int
	}
	var stuck []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.name, &p.stage, &p.ordinal); err != nil {
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
				Stage:      p.stage,
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
	stage := ""
	id := ""
	if ordinal >= 0 && ordinal < len(d.names) {
		name = d.names[ordinal]
	}
	if ordinal >= 0 && ordinal < len(d.stages) {
		stage = d.stages[ordinal]
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
			Stage:      stage,
			Status:     status,
			Ordinal:    ordinal,
			StartedAt:  started,
			FinishedAt: finished,
		},
	})
}
