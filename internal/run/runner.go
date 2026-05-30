package run

import (
	"context"
	"errors"
)

// StepSink 是 Runner 向外报告步骤进展的回调汇。Runner 不直接触库/发事件;
// 它声明步骤计划、置步骤运行/终态,由 worker 负责持久化 + 经事件总线发布。
// 这样真实构建(3-3)只需实现 Runner、复用同一持久化/SSE 管道。
type StepSink interface {
	// Plan 声明本次运行的全部步骤名(按顺序);worker 据此落 run_steps(pending)。
	// 必须在任何 StepRunning/StepDone 之前调用一次。
	Plan(ctx context.Context, names []string) error
	// StepRunning 将第 ordinal 个步骤置为 running 并记录开始时刻。
	StepRunning(ctx context.Context, ordinal int) error
	// StepDone 将第 ordinal 个步骤置为终态(success|failed|skipped)并记录结束时刻。
	StepDone(ctx context.Context, ordinal int, status string) error
}

// Runner 抽象「执行一次运行」的能力(可插拔)。本期为桩;真实构建/部署=3-3/4-x 换实现。
// Run 须响应 ctx 取消(取消传播);返回 error 表示运行失败(worker 据此转 failed)。
type Runner interface {
	Run(ctx context.Context, r *Run, sink StepSink) error
}

// ErrCanceled 表示运行因 context 取消而中止(worker 将其归一为 StatusFailed)。
var ErrCanceled = errors.New("run: canceled")

// StubRunner 是本期桩 runner:顺序跑若干占位步骤,每步置 success。
// 不做真实构建/部署、不 sleep(测试快);可注入失败步骤或在取消时中止。
//
//   - Steps:占位步骤名;为空时用默认三步。
//   - FailAt:>=0 时该序号步骤置 failed 并使整次运行失败;<0 表示全成功。
type StubRunner struct {
	Steps  []string
	FailAt int
}

// NewStubRunner 构造默认成功的桩 runner(占位三步)。
func NewStubRunner() *StubRunner {
	return &StubRunner{
		Steps:  []string{"拉取源码", "构建镜像", "部署"},
		FailAt: -1,
	}
}

// Run 实现 Runner:声明步骤 → 逐步 running→success;遇 FailAt 置 failed 并返回错误;
// 每步前检查 ctx 取消(取消则当前步骤 failed 并返回 ErrCanceled)。
func (s *StubRunner) Run(ctx context.Context, _ *Run, sink StepSink) error {
	names := s.Steps
	if len(names) == 0 {
		names = []string{"拉取源码", "构建镜像", "部署"}
	}
	if err := sink.Plan(ctx, names); err != nil {
		return err
	}
	for i := range names {
		if ctxCanceled(ctx) {
			_ = sink.StepDone(ctx, i, StepFailed)
			return ErrCanceled
		}
		if err := sink.StepRunning(ctx, i); err != nil {
			return err
		}
		// 取消可在「步骤运行中」发生:再次检查,使取消可被及时观察。
		if ctxCanceled(ctx) {
			_ = sink.StepDone(ctx, i, StepFailed)
			return ErrCanceled
		}
		if s.FailAt >= 0 && i == s.FailAt {
			if err := sink.StepDone(ctx, i, StepFailed); err != nil {
				return err
			}
			return errors.New("run: stub step failed")
		}
		if err := sink.StepDone(ctx, i, StepSuccess); err != nil {
			return err
		}
	}
	return nil
}
