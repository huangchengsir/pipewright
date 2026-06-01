package build

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
)

// script_steps.go 是「自定义脚本步骤」执行器(Epic 8 地基 · Story 8-2)。
//
// 它把流水线 settings 里配置的有序脚本步骤(pipeline.PipelineStep,type=script)接进既有 run 流程:
// 在「拉取源码 → 构建 →(可选)推送镜像」之后,按声明顺序逐个执行每个 script 步骤。
//
// 复用既有容器基建(不另造):
//   - 执行经 Driver.RunToolchain —— 即「用指定镜像挂载克隆出的工作区跑命令 + 流式日志」,
//     与模型 B 构建走同一条隔离容器路径(docker run --rm -v ws:/ws -w /ws -e K=V image sh -c <script>)。
//   - 日志/失败日志经同一 run.StepSink 喂出,无缝复用既有持久化/SSE/脱敏管道(3-1 地基)。
//   - secret env 经 vault.Reveal 明文注入容器(即取即用),但命令回显只列 key,出网/落库前由
//     per-run Masker 脱敏(main 经 WithLogMaskerFunc 注入;step env secret 已登记进该 run Masker)。
//
// 安全边界铁律:
//   - 脚本只在隔离构建容器跑,绝不在中控机宿主执行(RunToolchain = docker run,宿主零落地)。
//   - 命令注入防护:多行命令合成单个脚本字符串,经 `sh -c <script>` 在容器内由容器自身 shell 执行;
//     宿主侧调用一律 array []string(driver/commander 不拼 host shell),用户命令永不进宿主 argv 解释。
//   - ctx 取消即 kill 容器子进程(RunToolchain 经 exec.CommandContext;每步前后查 ctx)。
//   - workdir 用克隆工作区作为脚本上下文(WorkDir 相对工作区根的子目录)。

// scriptWorkspaceMount 是脚本步骤在容器内的工作区挂载点(克隆出的源码挂到这里)。
const scriptWorkspaceMount = "/workspace"

// scriptSteps 取该 run 配置里的全部 script 类型步骤(保序;非 script 类型本期跳过)。
func scriptSteps(settings *pipeline.Settings) []pipeline.PipelineStep {
	if settings == nil || len(settings.Steps) == 0 {
		return nil
	}
	out := make([]pipeline.PipelineStep, 0, len(settings.Steps))
	for _, st := range settings.Steps {
		if st.Type == pipeline.StepTypeScript {
			out = append(out, st)
		}
	}
	return out
}

// runScriptStep 在隔离容器内执行一个 script 步骤(挂载克隆工作区为脚本上下文)。
//
// ordinal 是该步在整次运行步骤计划里的序号(已 StepRunning)。返回 nil=成功;
// 非零退出 → ErrBuildFailed(调用方置 failed + SetFailureLog);ctx 取消 → run.ErrCanceled。
func (b *Builder) runScriptStep(ctx context.Context, sink run.StepSink, ordinal int, step pipeline.PipelineStep, workspace string) error {
	onLine := b.lineSink(sink, ordinal)

	// env:非 secret → 明文 K=V;secret → vault.Reveal 明文 K=V(注入容器,绝不回显值)。
	// 复用与构建变量同款拆分(buildArgs):secret 明文即取即用,脱敏由 per-run Masker 兜底。
	plain, secretEnv := b.buildArgs(step.Env)
	env := append(plain, secretEnv...)

	// 容器内工作目录:挂载点 + 可选相对子目录(WorkDir)。WorkDir 经 join 去穿越,落在挂载点内。
	workdir := scriptWorkspaceMount
	if sub := strings.TrimSpace(step.WorkDir); sub != "" {
		workdir = joinContainerPath(scriptWorkspaceMount, sub)
	}

	// 多行命令合成单脚本(set -e:任一行失败即整步失败;失败语义清晰)。脚本经容器内 sh -c 执行,
	// 宿主侧仍是 array(driver 不拼 host shell);用户命令永不被宿主 shell 解释(注入防护)。
	script := "set -e\n" + strings.Join(step.Commands, "\n")
	cmd := []string{"sh", "-c", script}

	code, err := b.driver.RunToolchain(ctx, step.Image, workspace, workdir, env, cmd, step.Resource, onLine)
	if err != nil && code < 0 {
		// 容器无法启动(镜像拉取失败/CLI 不可用等);ctx 取消时 code<0 但 ctx.Err 命中,由调用方归一。
		if errors.Is(ctx.Err(), context.Canceled) {
			return run.ErrCanceled
		}
		return ErrBuildFailed
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return run.ErrCanceled
	}
	if code != 0 {
		return ErrBuildFailed
	}
	return nil
}

// runScriptStepWithOpts 在 runScriptStep 之上叠加「任务级超时 + 失败重试」(P0 引擎能力)。
//
// 超时(step.TimeoutSeconds>0):用 context.WithTimeout 套住每次尝试;超时即取消子 ctx → kill 容器,
// 该步判失败(ErrBuildFailed)。注意:用 context.Cause 区分「本步超时」与「上层取消(用户取消/上层超时)」——
//   - 上层 ctx 已取消(parent.Err()!=nil)→ run.ErrCanceled,不重试(尊重取消语义)。
//   - 仅本步超时(子 ctx 超时但 parent 未取消)→ 记一行诚实日志,按 ErrBuildFailed 走重试逻辑。
//
// 重试(step.Retries>0):整步非零退出/容器启动失败时重跑,达上限(共 1+Retries 次尝试)才判失败。
// run.ErrCanceled(取消)绝不重试。每次重试前再查一次 parent ctx。
func (b *Builder) runScriptStepWithOpts(ctx context.Context, sink run.StepSink, ordinal int, step pipeline.PipelineStep, workspace string) error {
	onLine := b.lineSink(sink, ordinal)
	attempts := step.Retries + 1
	if attempts < 1 {
		attempts = 1
	}

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if ctx.Err() != nil {
			return run.ErrCanceled
		}
		if attempt > 1 {
			onLine("stdout", fmt.Sprintf("· 步骤「%s」失败,第 %d/%d 次重试…", step.Name, attempt-1, step.Retries))
		}

		// 每次尝试套一层可选超时子 ctx(>0 才套;=0 直接用 parent)。
		attemptCtx := ctx
		cancel := context.CancelFunc(func() {})
		if step.TimeoutSeconds > 0 {
			attemptCtx, cancel = context.WithTimeout(ctx, time.Duration(step.TimeoutSeconds)*time.Second)
		}
		err := b.runScriptStep(attemptCtx, sink, ordinal, step, workspace)
		timedOut := step.TimeoutSeconds > 0 && errors.Is(attemptCtx.Err(), context.DeadlineExceeded)
		cancel()

		// 上层取消优先:用户取消 / 上层超时 → 立即终止,不重试。
		if ctx.Err() != nil {
			return run.ErrCanceled
		}
		if errors.Is(err, run.ErrCanceled) && !timedOut {
			// runScriptStep 因子 ctx 取消返回 Canceled,但若那是本步超时所致(timedOut),
			// 不算运行取消,转成可重试的步失败;否则尊重取消。
			return run.ErrCanceled
		}
		if err == nil {
			return nil
		}
		if timedOut {
			onLine("stderr", fmt.Sprintf("步骤「%s」执行超时(>%ds),已终止本次尝试", step.Name, step.TimeoutSeconds))
		}
		lastErr = ErrBuildFailed // 归一为构建失败(供上层置 failed)
	}
	return lastErr
}

// joinContainerPath 把容器内挂载点与用户给的相对子目录拼成容器内绝对路径,去掉前导/尾随斜杠与
// `.`/`..` 段(防穿越逃出挂载点;容器内仍是隔离 rootfs,但语义上把脚本上下文钉在工作区内)。
func joinContainerPath(mount, sub string) string {
	parts := []string{}
	for _, seg := range strings.Split(sub, "/") {
		seg = strings.TrimSpace(seg)
		if seg == "" || seg == "." || seg == ".." {
			continue
		}
		parts = append(parts, seg)
	}
	if len(parts) == 0 {
		return mount
	}
	return strings.TrimRight(mount, "/") + "/" + strings.Join(parts, "/")
}
