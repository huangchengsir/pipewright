package build

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/huangchengsir/pipewright/internal/dagrun"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
)

// dag_stage_exec.go 是 DAG 调度器(dagrun)的**真实阶段执行器**(Epic 8 · Story 8-2)。
//
// dagrun.Runner 按阶段 DAG 编排;本文件提供「单个阶段怎么执行」的真实实现:复用 Builder 的
// 容器基建(cloner + driver.RunToolchain)在隔离容器内真实跑 script 类型 job 的命令,而非 stub。
//
// 执行模型(自包含、并行安全):每个含 script job 的阶段**独立克隆一份临时工作区**(即用即销),
// 在其中按 job 声明序逐个执行 script job 的命令(多行 → set -e 单脚本 → 容器内 sh -c)。
// 不同阶段并行执行时各持各的工作区,互不干扰;宿主零落地(RunToolchain = docker run --rm)。
//
// job.Config 取值(对齐前端节点表单的 script 类型字段):image(必填)、commands(多行,必填)、
// workDir(可选,相对工作区根)。非 script/custom 类型的 job 本期不做真实执行(build_image/
// push_image/deploy_ssh 的真实化是后续 increment),仅打一行诚实占位日志后放行——**不冒充**
// 真实构建/部署。
//
// 安全边界(复用 runScriptStep 的铁律):脚本只在隔离容器跑、绝不在宿主执行;命令注入防护
// (宿主侧 array,用户命令仅容器内 sh 解释);ctx 取消即 kill;secret 注入但出网/落库脱敏。
//
// 工作区共享(跨阶段复用同一 clone)是性能优化项,留后续;本期每阶段独立 clone 以求简单与并行安全。

// scriptJobTypes 是会触发真实容器执行的 job 类型。
func isScriptJob(jobType string) bool {
	t := strings.TrimSpace(jobType)
	return t == pipeline.StepTypeScript || t == "custom"
}

// NewStageExecutor 返回一个复用 Builder 容器基建的真实 dagrun.StageExecutor。
// 仅对 script/custom 类型 job 做真实容器执行;其余类型打占位日志放行。
// reportSink 可选(nil = 不持久化测试报告,但质量门禁阻断仍生效;Story 8-6 / FR-8-6)。
func NewStageExecutor(b *Builder, reportSink TestReportSink) dagrun.StageExecutor {
	return func(ctx context.Context, r *run.Run, stage pipeline.Stage, rep dagrun.StageReporter) error {
		scriptJobs := make([]pipeline.Job, 0, len(stage.Jobs))
		for _, jb := range stage.Jobs {
			if isScriptJob(jb.Type) {
				scriptJobs = append(scriptJobs, jb)
			}
		}

		// 无 script job:对每个 job 打诚实占位日志(非 script 类型真实执行 = 后续 increment),放行。
		if len(scriptJobs) == 0 {
			for _, jb := range stage.Jobs {
				_ = rep.Log(ctx, streamStdout, fmt.Sprintf("· %s(%s)— 真实执行未接入(仅 script 类型);本阶段放行", jb.Name, jb.Type))
			}
			if len(stage.Jobs) == 0 {
				_ = rep.Log(ctx, streamStdout, fmt.Sprintf("阶段「%s」无 job", stage.Name))
			}
			return nil
		}

		// 有 script job:克隆一份临时工作区(即用即销)。
		proj, _, perr := b.resolve(ctx, r)
		if perr != nil {
			_ = rep.Log(ctx, streamStderr, "无法加载项目构建配置:"+perr.Error())
			return ErrBuildFailed
		}
		workspace, mkErr := mkTempWorkspace()
		if mkErr != nil {
			_ = rep.Log(ctx, streamStderr, "创建临时工作区失败:"+mkErr.Error())
			return ErrBuildFailed
		}
		defer func() { _ = os.RemoveAll(workspace) }() // 宿主零污染

		token := b.revealToken(proj.CredentialID)
		_, cerr := b.cloner.Clone(ctx, proj.RepoURL, token, r.Trigger.Branch, r.Trigger.Commit, workspace)
		token = "" // 明文用完即弃
		_ = token
		if cerr != nil {
			if errors.Is(ctx.Err(), context.Canceled) {
				return run.ErrCanceled
			}
			_ = rep.Log(ctx, streamStderr, "源码克隆失败(鉴权/网络/ref 不存在或被 SSRF 拒绝)")
			return ErrBuildFailed
		}

		sink := &reporterSink{rep: rep}
		for _, jb := range scriptJobs {
			if canceled(ctx) {
				return run.ErrCanceled
			}
			step, verr := scriptStepFromJob(jb)
			if verr != nil {
				_ = rep.Log(ctx, streamStderr, fmt.Sprintf("script job「%s」配置无效:%v", jb.Name, verr))
				return ErrBuildFailed
			}
			// 参数化运行(Story 8-11):把本次运行参数作明文环境变量注入容器(置于 step env 前,
			// step 自身 env 同名可覆盖)。明文 K=V,经 buildArgs 进容器。
			step.Env = append(runParamsAsEnv(r.Trigger.Params), step.Env...)
			// runScriptStep 用 lineSink(sink, ordinal) 喂日志;reporterSink 忽略 ordinal、转 rep(已绑定本阶段序号)。
			if err := b.runScriptStep(ctx, sink, 0, step, workspace); err != nil {
				return err // ErrBuildFailed / run.ErrCanceled
			}
		}

		// 测试报告采集 + 质量门禁(Story 8-6 / FR-8-6):阶段 script job 全部成功后,据声明的
		// 报告配置读工作区内的 JUnit/覆盖率文件 → 解析 → 持久化汇总 → 门禁裁决。门禁不过 →
		// ErrQualityGate 令本阶段失败,阻断下游部署阶段(复用 dagrun「阶段失败→下游不执行」语义)。
		if err := collectStageReport(ctx, reportSink, r, stage, workspace, rep); err != nil {
			return err
		}
		return nil
	}
}

// scriptStepFromJob 从画布 job.Config 构造一条 script 步骤(image + 多行 commands + 可选 workDir)。
// image 缺失或 commands 全空 → 错误(诚实失败,不静默跳过)。
func scriptStepFromJob(jb pipeline.Job) (pipeline.PipelineStep, error) {
	image := cfgString(jb.Config, "image")
	if image == "" {
		return pipeline.PipelineStep{}, errors.New("缺少运行镜像(image)")
	}
	cmds := splitCommands(cfgString(jb.Config, "commands"))
	if len(cmds) == 0 {
		return pipeline.PipelineStep{}, errors.New("缺少执行命令(commands)")
	}
	return pipeline.PipelineStep{
		ID:       jb.ID,
		Name:     jb.Name,
		Type:     pipeline.StepTypeScript,
		Image:    image,
		Commands: cmds,
		WorkDir:  cfgString(jb.Config, "workDir"),
	}, nil
}

// runParamsAsEnv 把运行参数(明文 K=V)转为非 secret BuildVar(注入容器环境)。键序确定性不保证(map),
// 但同名键不会重复(map 天然去重)。
func runParamsAsEnv(params map[string]string) []pipeline.BuildVar {
	if len(params) == 0 {
		return nil
	}
	out := make([]pipeline.BuildVar, 0, len(params))
	for k, v := range params {
		out = append(out, pipeline.BuildVar{Key: k, Value: v, Secret: false})
	}
	return out
}

// cfgString 从自由 KV config 取字符串值(非字符串/缺失 → "")。
func cfgString(cfg map[string]any, key string) string {
	if cfg == nil {
		return ""
	}
	if v, ok := cfg[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// splitCommands 把多行命令字符串拆成逐行命令(trim 尾随 CR、剔空行)。
func splitCommands(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	out := make([]string, 0, 4)
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}

// reporterSink 把 runScriptStep 期望的 run.StepSink 适配到 dagrun.StageReporter:
// Log/EmitArtifact 转发到已绑定本阶段序号的 reporter,其余步骤生命周期方法为 no-op
// (阶段的 StepRunning/StepDone 由 dagrun.Runner 负责)。
type reporterSink struct {
	rep dagrun.StageReporter
}

func (s *reporterSink) Plan(context.Context, []string) error        { return nil }
func (s *reporterSink) StepRunning(context.Context, int) error      { return nil }
func (s *reporterSink) StepDone(context.Context, int, string) error { return nil }
func (s *reporterSink) SetFailureLog(context.Context, string) error { return nil }
func (s *reporterSink) Log(ctx context.Context, stream string, _ int, line string) error {
	return s.rep.Log(ctx, stream, line)
}
func (s *reporterSink) EmitArtifact(ctx context.Context, a run.Artifact) error {
	return s.rep.EmitArtifact(ctx, a)
}
