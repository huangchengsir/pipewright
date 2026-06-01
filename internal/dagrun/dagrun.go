// Package dagrun 把 DAG 调度内核(internal/dag)接进 run 引擎(Epic 8 · Story 8-1↔8-3 桥接)。
//
// Runner 实现 run.Runner 契约,可直接作为 WorkerPool 的执行器:它加载项目的流水线 spec
// (阶段 + 依赖 needs),用 dag 把阶段构成 DAG 调度执行——无依赖阶段并行、按拓扑序推进、
// 任一阶段失败按策略阻断下游(下游阶段记 skipped),并把每个阶段的运行/终态/日志经既有
// StepSink 持久化(复用 run_steps + SSE + 脱敏管道)。
//
// 关注点分离:**单个阶段「怎么执行」**(跑 stage 内并行 job → job 内有序 step、在隔离容器里
// 真实构建/部署)由注入的 StageExecutor 决定——那是 Story 8-2 的活;本包只负责「**按 DAG
// 把阶段编排起来并如实上报**」。未注入真实执行器时用 StubStageExecutor(仅打日志即成功),
// 便于纯单测覆盖编排逻辑、且不冒充真实执行。
//
// 向后兼容:当整个 spec 无任何阶段声明 needs 时,BuildGraph 退化为「按声明序线性」(存量
// 直线流水线行为不变);一旦有阶段声明 needs,严格按声明的 DAG 调度。
package dagrun

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/huangchengsir/pipewright/internal/dag"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
)

// SpecLoader 加载某项目「在某运行分支上」的流水线配置。
//
// branch 是本次运行的目标分支(run.Trigger.Branch),供「流水线即代码」按运行分支取
// `.pipewright.yml`(FR-8-12);库内 loader(pipeline.Service,分支无关)可经 SpecLoaderFunc
// 包成此契约并忽略 branch。branch 为空(如无分支的手动运行)时,实现应退化为既有默认分支行为。
type SpecLoader interface {
	Get(ctx context.Context, projectID, branch string) (*pipeline.Config, error)
}

// SpecLoaderFunc 把任意「按 projectID 取配置」的库内 loader(如 pipeline.Service.Get,分支无关)
// 适配成 branch 感知的 SpecLoader:它忽略 branch,直接委托底层 2 参方法。用于把存量库内 loader
// 接进 branch 感知的 dagrun 内核,而不改动其公有 2 参签名(其它调用方不受影响)。
type SpecLoaderFunc func(ctx context.Context, projectID string) (*pipeline.Config, error)

// Get 实现 SpecLoader:忽略 branch,委托底层分支无关的 loader。
func (f SpecLoaderFunc) Get(ctx context.Context, projectID, _ string) (*pipeline.Config, error) {
	return f(ctx, projectID)
}

// StageReporter 给 StageExecutor 上报「本阶段」的日志/产物(自动关联到该阶段的 run_steps 序号)。
type StageReporter interface {
	// Log 报告本阶段一行日志(stream ∈ stdout|stderr)。落库前由 StepSink 脱敏。
	Log(ctx context.Context, stream, line string) error
	// EmitArtifact 报告本阶段产出的一条构建产物。
	EmitArtifact(ctx context.Context, a run.Artifact) error
}

// StageExecutor 执行单个阶段(跑其 job/step);返回非 nil 表示该阶段失败。须响应 ctx 取消。
// 真实实现(Story 8-2)在隔离容器内跑 stage 内并行 job 的有序 step;本包默认用 StubStageExecutor。
type StageExecutor func(ctx context.Context, r *run.Run, stage pipeline.Stage, rep StageReporter) error

// StubStageExecutor 是默认占位执行器:为每个 job 打一行日志后即判该阶段成功。
// 不做任何真实构建/部署(诚实占位,真实执行 = Story 8-2)。
func StubStageExecutor(ctx context.Context, _ *run.Run, stage pipeline.Stage, rep StageReporter) error {
	if len(stage.Jobs) == 0 {
		_ = rep.Log(ctx, "stdout", fmt.Sprintf("阶段「%s」无 job,跳过执行体", stage.Name))
		return nil
	}
	for _, jb := range stage.Jobs {
		if err := ctx.Err(); err != nil {
			return err
		}
		_ = rep.Log(ctx, "stdout", fmt.Sprintf("· %s(%s)", jb.Name, jb.Type))
	}
	return nil
}

// GateFunc 在进入「审批门」阶段(Gate=true)前阻塞等待人工决定(Story 8-4)。
// 返回 (true,nil)=批准继续;(false,nil)=拒绝(该阶段失败);(_,err)=取消/超时等(阶段失败)。
// 实现(在 main/httpapi 装配)负责:登记待批 + 置 run 状态 waiting_approval + 阻塞协调器 +
// 决定后置回 running。dagrun 经此 hook 解耦,不直接 import run.Service / approval。
type GateFunc func(ctx context.Context, r *run.Run, stage pipeline.Stage) (approved bool, err error)

// ErrGateRejected 表示审批门被人工拒绝(该阶段失败 → 运行终止)。
var ErrGateRejected = errors.New("dagrun: approval gate rejected")

// Runner 是 DAG 感知的 run.Runner 实现。
type Runner struct {
	loader      SpecLoader
	exec        StageExecutor
	gate        GateFunc
	concurrency int
}

// Option 配置 Runner。
type Option func(*Runner)

// WithGate 注入审批门 hook(Story 8-4)。nil 忽略(无门:Gate 阶段直接执行)。
func WithGate(g GateFunc) Option {
	return func(r *Runner) {
		if g != nil {
			r.gate = g
		}
	}
}

// WithConcurrency 设阶段并行上限(<=0 用阶段总数,即「能并行的全并行」)。
func WithConcurrency(n int) Option {
	return func(r *Runner) { r.concurrency = n }
}

// WithStageExecutor 注入真实阶段执行器(Story 8-2)。nil 忽略(保留 Stub)。
func WithStageExecutor(e StageExecutor) Option {
	return func(r *Runner) {
		if e != nil {
			r.exec = e
		}
	}
}

// New 构造 DAG Runner。loader 加载 spec;未注入执行器时用 StubStageExecutor。
func New(loader SpecLoader, opts ...Option) *Runner {
	r := &Runner{loader: loader, exec: StubStageExecutor}
	for _, o := range opts {
		o(r)
	}
	return r
}

// Run 实现 run.Runner:加载 spec → 构 DAG → 按拓扑/并行调度阶段 → 经 StepSink 上报。
//
// StepSink 非并发安全约定:本 Runner 用单一 mutex 串行化所有 sink 调用,即便阶段并行执行,
// 对 sink 的 Plan/StepRunning/StepDone/Log/EmitArtifact 也互斥,安全复用既有持久化管道。
func (r *Runner) Run(ctx context.Context, rn *run.Run, sink run.StepSink) error {
	cfg, err := r.loader.Get(ctx, rn.ProjectID, rn.Trigger.Branch)
	if err != nil {
		return fmt.Errorf("dagrun: load pipeline spec: %w", err)
	}
	// 矩阵展开(P1):把声明了 Matrix 的阶段在纯调度层展开成笛卡尔积的并行 cell 子阶段,
	// 并重映射 needs(下游等所有 cell)。无 matrix 阶段时原样返回。展开后阶段集作为后续
	// 建图 / stageByID / 上报 / 执行的权威集——不改 dag 的并发与失败语义。
	stages := ExpandMatrix(cfg.Spec.Stages)
	if len(stages) == 0 {
		// 无阶段:声明零步,直接成功(与既有空流水线语义一致)。
		_ = sink.Plan(ctx, nil)
		return nil
	}

	graph, err := BuildGraph(stages)
	if err != nil {
		return fmt.Errorf("dagrun: build graph: %w", err)
	}

	// 拓扑序决定 run_steps 的展示序与序号;每个阶段 = 一个 step。
	order := graph.TopoOrder()
	ordinalOf := make(map[string]int, len(order))
	names := make([]string, 0, len(order))
	stageByID := make(map[string]pipeline.Stage, len(stages))
	for _, st := range stages {
		stageByID[st.ID] = st
	}
	for i, id := range order {
		ordinalOf[id] = i
		names = append(names, stageByID[id].Name)
	}

	var mu sync.Mutex // 串行化对 sink 的全部调用(阶段可能并行)
	lockedSink := func(fn func() error) error {
		mu.Lock()
		defer mu.Unlock()
		return fn()
	}

	if err := lockedSink(func() error { return sink.Plan(ctx, names) }); err != nil {
		return fmt.Errorf("dagrun: plan: %w", err)
	}

	maxc := r.concurrency
	if maxc <= 0 {
		maxc = len(order)
	}

	result := graph.Schedule(ctx, func(ctx context.Context, stageID string) error {
		ord := ordinalOf[stageID]
		stage := stageByID[stageID]
		// 条件执行(Story 8-5):when 不满足 → 跳过(非失败),其下游照「未成功」跳过。
		if !stage.When.Matches(rn.Trigger.Branch, rn.Trigger.Type) {
			_ = lockedSink(func() error {
				return sink.Log(ctx, "stdout", ord, fmt.Sprintf(
					"阶段「%s」条件不满足(分支=%q 触发=%q),跳过", stage.Name, rn.Trigger.Branch, rn.Trigger.Type))
			})
			return dag.ErrSkip
		}
		if err := lockedSink(func() error { return sink.StepRunning(ctx, ord) }); err != nil {
			return err
		}
		// 人工审批门(Story 8-4):进入该阶段前阻塞等待批准。run 状态由 gate 置 waiting_approval。
		if stage.Gate && r.gate != nil {
			_ = lockedSink(func() error {
				return sink.Log(ctx, "stdout", ord, fmt.Sprintf("⏸ 阶段「%s」等待人工审批…", stage.Name))
			})
			approved, gerr := r.gate(ctx, rn, stage)
			if gerr != nil {
				_ = lockedSink(func() error {
					return sink.Log(ctx, "stderr", ord, fmt.Sprintf("审批门中断:%v", gerr))
				})
				_ = lockedSink(func() error { return sink.StepDone(ctx, ord, run.StepFailed) })
				return gerr
			}
			if !approved {
				_ = lockedSink(func() error { return sink.Log(ctx, "stderr", ord, "⛔ 审批被拒绝,终止该阶段") })
				_ = lockedSink(func() error { return sink.StepDone(ctx, ord, run.StepFailed) })
				return ErrGateRejected
			}
			_ = lockedSink(func() error { return sink.Log(ctx, "stdout", ord, "✅ 审批通过,继续执行") })
		}
		rep := &stageReporter{sink: sink, mu: &mu, ordinal: ord}
		execErr := r.exec(ctx, rn, stage, rep)
		status := run.StepSuccess
		if execErr != nil {
			status = run.StepFailed
		}
		_ = lockedSink(func() error { return sink.StepDone(ctx, ord, status) })
		return execErr
	}, dag.Options{MaxConcurrency: maxc})

	// 被跳过/取消(从未执行)的阶段:其 step 终态记为 skipped(条件跳过 + 上游未成功皆然),避免悬挂 pending。
	for id, nr := range result {
		if nr.Status == dag.StatusSkipped || nr.Status == dag.StatusCanceled {
			ord := ordinalOf[id]
			_ = lockedSink(func() error { return sink.StepDone(ctx, ord, run.StepSkipped) })
		}
	}

	// 整体失败判定:仅当有阶段**真失败**(StatusFailed)。条件跳过 / 上游被跳过不令整体失败
	// (上游若真失败,该失败阶段本身即计入 StatusFailed)。
	if result.Counts()[dag.StatusFailed] > 0 {
		return fmt.Errorf("dagrun: pipeline failed (%v)", summarize(result))
	}
	return nil
}

// stageReporter 把阶段级日志/产物绑定到该阶段的 step 序号,并经 mutex 串行化对 sink 的调用。
type stageReporter struct {
	sink    run.StepSink
	mu      *sync.Mutex
	ordinal int
}

func (s *stageReporter) Log(ctx context.Context, stream, line string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sink.Log(ctx, stream, s.ordinal, line)
}

func (s *stageReporter) EmitArtifact(ctx context.Context, a run.Artifact) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sink.EmitArtifact(ctx, a)
}

// summarize 汇总各终态计数(用于失败错误信息,不含敏感内容)。
func summarize(res dag.Result) map[dag.Status]int { return res.Counts() }

// BuildGraph 把流水线阶段构成 dag.Graph。
//
//   - 若**任一**阶段声明了 needs:严格按声明的依赖建图。
//   - 若**所有**阶段都无 needs:退化为「按声明序线性」(stage[i] 依赖 stage[i-1]),
//     保持存量直线流水线的执行顺序不变(向后兼容)。
//
// 返回的图已通过 dag.New 的构造校验(存在性 / 自指 / 环);spec 保存时也已校验过,此处兜底。
func BuildGraph(stages []pipeline.Stage) (*dag.Graph, error) {
	anyNeeds := false
	for _, st := range stages {
		if len(st.Needs) > 0 {
			anyNeeds = true
			break
		}
	}
	nodes := make([]dag.Node, 0, len(stages))
	for i, st := range stages {
		needs := st.Needs
		if !anyNeeds && i > 0 {
			needs = []string{stages[i-1].ID} // 线性退化
		}
		nodes = append(nodes, dag.Node{ID: st.ID, Needs: needs, AllowFailure: st.AllowFailure})
	}
	return dag.New(nodes)
}
