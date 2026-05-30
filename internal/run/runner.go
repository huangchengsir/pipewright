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
	// SetFailureLog 报告本次运行的失败日志原文(脱敏前;持久化到 run.failure_log,
	// 供后续 AI 诊断消费)。Runner 在失败路径调用;best-effort,失败不应阻断 run 终态。
	SetFailureLog(ctx context.Context, log string) error
	// Log 报告一行运行日志(Story 3.6)。worker 侧实现负责**先脱敏**→落 run_logs(分配
	// run 内单调 seq)→ 经事件总线发 EventLog。stream ∈ stdout|stderr;stepOrdinal 关联
	// 步骤(-1 表示运行级);line 为单行文本(无尾换行)。3-3 真实构建只经此接口喂行、不改形状。
	Log(ctx context.Context, stream string, stepOrdinal int, line string) error
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

// StubFailureSecret 是桩失败日志内嵌的**假** secret(导出供 httpapi 诊断登记 Masker 验脱敏)。
// 用于验证「出网前脱敏」铁律:worker 自动诊断 / diagnose 端点须先把它登记进 mask.Masker
// 再出网,自动化测试断言诊断 prompt/响应/evidence/DB 绝无此明文(被替换为 [MASKED])。
// 3-3/3-6 真实日志落地后,凭据登记改为从 vault 取该 run 用到的凭据明文。
const StubFailureSecret = "SECRET_LEAK_zzz9k3"

// 步骤 stream 取值(StubRunner 逐行 emit 用;真实构建经同一 StepSink.Log 喂行)。
const (
	streamStdout = "stdout"
	streamStderr = "stderr"
)

// stubStepLogs 合成某步骤的真实感 stdout 日志行(每步若干行)。
// 其中一行内嵌假 secret(StubFailureSecret),用以验证「落库/出网前脱敏」铁律:
// dbStepSink.Log 先经 mask.Masker.Scrub 再落 run_logs/发 SSE,自动化测试断言裸 DB 无明文。
// 3-3 真实构建落地后由真实日志经同一接口替换,日志形状不变。
func stubStepLogs(stepName string) []string {
	switch stepName {
	case "构建镜像":
		return []string{
			"> app@1.0.0 build",
			"> vite build",
			"vite v5.0.0 building for production...",
			"npm config set //registry.npmjs.org/:_authToken=" + StubFailureSecret,
			"✓ 142 modules transformed.",
			"dist/index.html  0.46 kB",
			"✓ built in 1.84s",
		}
	case "部署":
		return []string{
			"$ kubectl apply -f deploy.yaml",
			"deployment.apps/app configured",
			"service/app unchanged",
			"waiting for rollout to finish...",
			"deployment \"app\" successfully rolled out",
		}
	default:
		return []string{
			"$ git fetch --depth=1 origin",
			"$ npm install",
			"added 218 packages in 3s",
			"$ npm run lint",
			"✓ no lint errors",
		}
	}
}

// stubStepStderr 合成失败步骤的 stderr 日志行(追加在 stdout 之后)。
func stubStepStderr(stepName string) []string {
	return []string{
		"npm ERR! code E404",
		"npm ERR! 404 Not Found - GET https://registry.npmjs.org/leftpad - Not found",
		"npm ERR! 404 'leftpad@^1.0.0' is not in this registry.",
		"Build failed with exit code 1",
		"步骤「" + stepName + "」失败",
	}
}

// stubFailureLog 合成一段真实感构建失败日志(多行,含一个假 secret),供 AI 诊断消费。
// 诚实标注「桩日志」:3-3 真实构建 / 3-6 实时日志落地后由真实日志替换。
// failedStep 为命中失败的步骤名(用于日志上下文)。
func stubFailureLog(failedStep string) string {
	return "" +
		"[桩日志 stub] 步骤「" + failedStep + "」失败,以下为合成的真实感错误日志(无真实构建,3-3/3-6 后换真实日志)\n" +
		"npm ERR! code E404\n" +
		"npm ERR! 404 Not Found - GET https://registry.npmjs.org/leftpad - Not found\n" +
		"npm ERR! 404 'leftpad@^1.0.0' is not in this registry.\n" +
		"npm ERR! 404 missing: leftpad@^1.0.0 from the root project\n" +
		"npm config set //registry.npmjs.org/:_authToken=" + StubFailureSecret + "\n" +
		"npm ERR! A complete log of this run can be found in: /root/.npm/_logs/2026-05-30T00_00_00_000Z-debug.log\n" +
		"Build failed with exit code 1\n"
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
		// 逐行 emit 本步骤 stdout 日志(驱动实时流 + 持久化 + 回放;含一个假 secret 验脱敏)。
		// best-effort:单行落库失败仅忽略,不阻断步骤/运行(日志不应连累 run 终态)。
		for _, line := range stubStepLogs(names[i]) {
			_ = sink.Log(ctx, streamStdout, i, line)
		}
		// 取消可在「步骤运行中」发生:再次检查,使取消可被及时观察。
		if ctxCanceled(ctx) {
			_ = sink.StepDone(ctx, i, StepFailed)
			return ErrCanceled
		}
		if s.FailAt >= 0 && i == s.FailAt {
			// 失败步骤追加 stderr 日志行(真实感错误输出)。
			for _, line := range stubStepStderr(names[i]) {
				_ = sink.Log(ctx, streamStderr, i, line)
			}
			if err := sink.StepDone(ctx, i, StepFailed); err != nil {
				return err
			}
			// 合成失败日志(含假 secret 以验脱敏)并报告;best-effort,失败仅忽略不阻断终态。
			_ = sink.SetFailureLog(ctx, stubFailureLog(names[i]))
			return errors.New("run: stub step failed")
		}
		if err := sink.StepDone(ctx, i, StepSuccess); err != nil {
			return err
		}
	}
	return nil
}
