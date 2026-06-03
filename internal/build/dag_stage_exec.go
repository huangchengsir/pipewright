package build

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/huangchengsir/pipewright/internal/dagrun"
	"github.com/huangchengsir/pipewright/internal/notify"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/project"
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

// scriptJobTypes 是会触发真实容器执行的 job 类型。build_frontend/build_backend 是「前端/后端构建」
// 模板节点,本质是带预填命令的 script;templated 是「用户自定义节点」(参数 + 命令模板),
// 渲染 {{参数}} 后同样在隔离容器跑。三者都按 script 路径执行(容器跑 + 收 artifactPath 产物)。
func isScriptJob(jobType string) bool {
	t := strings.TrimSpace(jobType)
	return t == pipeline.StepTypeScript || t == "custom" || t == "build_frontend" || t == "build_backend" || t == "templated"
}

// tplPlaceholder 匹配 {{key}} 占位(key 为标识符)。自定义节点参数渲染用。
var tplPlaceholder = regexp.MustCompile(`\{\{\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*\}\}`)

// templateContext 收集自定义节点的渲染上下文:config 里的字符串值 + 自由「参数表」(params 字段,
// 每行 `key=value` 或 `key: value`)。参数完全自由(用户随便定义键),不锁死 schema。
func templateContext(cfg map[string]any) map[string]string {
	ctx := make(map[string]string, len(cfg)+4)
	for k, v := range cfg {
		if s, ok := v.(string); ok {
			ctx[k] = s
		}
	}
	for _, line := range splitCommands(cfgString(cfg, "params")) {
		i := strings.IndexAny(line, "=:")
		if i <= 0 {
			continue
		}
		if k := strings.TrimSpace(line[:i]); k != "" {
			ctx[k] = strings.TrimSpace(line[i+1:])
		}
	}
	return ctx
}

// renderTemplate 把 {{key}} 替换为 ctx[key]。未知占位原样保留(不报错;$ENV 交给容器内 shell;
// 无 {{ 则零开销直返)。ctx 由 templateContext 收集(config 字段 + params 自由参数)。
func renderTemplate(tpl string, ctx map[string]string) string {
	if tpl == "" || !strings.Contains(tpl, "{{") {
		return tpl
	}
	return tplPlaceholder.ReplaceAllStringFunc(tpl, func(m string) string {
		key := tplPlaceholder.FindStringSubmatch(m)[1]
		if v, ok := ctx[key]; ok {
			return v
		}
		return m
	})
}

// isDeployJob 判断是否「SSH 部署」类节点(deploy_ssh 通用 / deploy_frontend 前端部署模板)。
func isDeployJob(jobType string) bool {
	t := strings.TrimSpace(jobType)
	return t == "deploy_ssh" || t == "deploy_frontend"
}

// isBuildImageJob 判断是否「构建产物(镜像/JAR/dist)」节点(画布 build_image 类型)。
func isBuildImageJob(jobType string) bool {
	return strings.TrimSpace(jobType) == "build_image"
}

// NewStageExecutor 返回一个复用 Builder 容器基建的真实 dagrun.StageExecutor。
// 仅对 script/custom 类型 job 做真实容器执行;其余类型打占位日志放行。
// reportSink 可选(nil = 不持久化测试报告,但质量门禁阻断仍生效;Story 8-6 / FR-8-6)。
func NewStageExecutor(b *Builder, reportSink TestReportSink) dagrun.StageExecutor {
	return func(ctx context.Context, r *run.Run, stage pipeline.Stage, rep dagrun.StageReporter) error {
		scriptJobs := make([]pipeline.Job, 0, len(stage.Jobs))
		buildImageJobs := make([]pipeline.Job, 0, len(stage.Jobs))
		deployJobs := make([]pipeline.Job, 0, len(stage.Jobs))
		notifyJobs := make([]pipeline.Job, 0, len(stage.Jobs))
		hasPushJob := false
		for _, jb := range stage.Jobs {
			switch {
			case isScriptJob(jb.Type):
				scriptJobs = append(scriptJobs, jb)
			case isBuildImageJob(jb.Type):
				buildImageJobs = append(buildImageJobs, jb)
			case strings.TrimSpace(jb.Type) == "push_image":
				hasPushJob = true
			case isDeployJob(jb.Type):
				deployJobs = append(deployJobs, jb)
			case strings.TrimSpace(jb.Type) == "notify":
				notifyJobs = append(notifyJobs, jb)
			}
		}

		needsBuild := len(scriptJobs) > 0 || len(buildImageJobs) > 0
		// 没有任何可执行节点(script/build_image/deploy_ssh/notify)且无 post → 诚实占位放行。
		if !needsBuild && len(deployJobs) == 0 && len(notifyJobs) == 0 && len(stage.Post) == 0 {
			for _, jb := range stage.Jobs {
				_ = rep.Log(ctx, streamStdout, fmt.Sprintf("· %s(%s)— 真实执行未接入;本阶段放行", jb.Name, jb.Type))
			}
			if len(stage.Jobs) == 0 {
				_ = rep.Log(ctx, streamStdout, fmt.Sprintf("阶段「%s」无 job", stage.Name))
			}
			return nil
		}

		sink := &reporterSink{rep: rep}
		hasPost := len(stage.Post) > 0

		// 工作区:有 script/build_image **或** 有 post 步骤时都需克隆(post 在同一工作区跑,可访问
		// 构建产物,对齐 Jenkins post 语义);纯部署/通知阶段无工作区。
		var (
			proj      *project.Project
			settings  *pipeline.Settings
			workspace string
			commitTag = "latest"
		)
		if needsBuild || hasPost {
			p, s, perr := b.resolve(ctx, r)
			if perr != nil {
				_ = rep.Log(ctx, streamStderr, "无法加载项目构建配置:"+perr.Error())
				return ErrBuildFailed
			}
			proj, settings = p, s
			ws, mkErr := mkTempWorkspace()
			if mkErr != nil {
				_ = rep.Log(ctx, streamStderr, "创建临时工作区失败:"+mkErr.Error())
				return ErrBuildFailed
			}
			workspace = ws
			defer func() { _ = os.RemoveAll(workspace) }() // 宿主零污染

			token := b.revealToken(proj.CredentialID)
			resolved, cerr := b.cloner.Clone(ctx, proj.RepoURL, token, r.Trigger.Branch, r.Trigger.Commit, workspace)
			token = "" // 明文用完即弃
			_ = token
			if cerr != nil {
				if errors.Is(ctx.Err(), context.Canceled) {
					return run.ErrCanceled
				}
				_ = rep.Log(ctx, streamStderr, "源码克隆失败(鉴权/网络/ref 不存在或被 SSRF 拒绝)")
				return ErrBuildFailed
			}
			if resolved != nil && resolved.CommitShort != "" {
				commitTag = resolved.CommitShort
				if b.recordCommit != nil {
					b.recordCommit(ctx, r.ID, resolved.CommitShort)
				}
			}
			// 跨阶段产物传递:把上游阶段已归档的 jar/dist 真字节恢复回本阶段新工作区的原相对路径,
			// 使「构建/打包/部署」拆成独立串行阶段时,下游(如 build_image)仍能拿到上游产物。
			// best-effort(首阶段无上游产物即 no-op;失败仅记日志,不阻断)。
			b.restorePriorArtifacts(ctx, r, workspace, rep)
		}

		// 阶段主体(job 执行):捕获错误而非提前返回,以便其后无论成败都按条件跑 post 步骤。
		jobErr := func() error {
			if needsBuild {
				// 旁挂服务(P1):阶段声明 services 时,起服务容器到临时网络,脚本容器加入同网按服务名互访;
				// 阶段结束(成败/取消)拆除。驱动不支持容器网络能力 → 直接失败(不在缺依赖下假跑)。
				var svcNetwork string
				if len(stage.Services) > 0 {
					net, ok := b.startStageServices(ctx, r, stage, rep)
					if !ok {
						return ErrBuildFailed
					}
					svcNetwork = net
					defer b.stopStageServices(context.WithoutCancel(ctx), r, stage, net, rep)
				}

				// 步骤输出→下游变量(P1):同阶段 job 间经 $PIPEWRIGHT_ENV 文件传值,carriedEnv 累积注入后续 job。
				var carriedEnv []pipeline.BuildVar
				for _, jb := range scriptJobs {
					if canceled(ctx) {
						return run.ErrCanceled
					}
					step, verr := scriptStepFromJob(jb)
					if verr != nil {
						_ = rep.Log(ctx, streamStderr, fmt.Sprintf("script job「%s」配置无效:%v", jb.Name, verr))
						return ErrBuildFailed
					}
					// 注入顺序:运行参数 → 上游 job 输出(carriedEnv)→ job 自身 env(后者覆盖同名)→ PIPEWRIGHT_ENV(系统,末位防覆盖)。
					base := append(runParamsAsEnv(r.Trigger.Params), carriedEnv...)
					step.Env = append(base, step.Env...)
					step.Env = append(step.Env, pipewrightEnvVar())
					// 旁挂服务网络:脚本容器加入,按服务名互访。
					if svcNetwork != "" {
						step.Resource.Network = svcNetwork
					}
					// 构建依赖缓存(#61):执行前恢复(暖构建)、成功后保存(best-effort,缓存问题绝不让构建失败)。
					// 任务级 timeout/retry(#63):零值时 runScriptStepWithOpts 退化为单次无超时执行(旧行为)。
					b.restoreJobCache(ctx, rep, jb, r.Trigger.Branch, workspace)
					if err := b.runScriptStepWithOpts(ctx, sink, 0, step, workspace); err != nil {
						return err // ErrBuildFailed / run.ErrCanceled
					}
					b.saveJobCache(ctx, rep, jb, r.Trigger.Branch, workspace)
					// 捕获本 job 写入 $PIPEWRIGHT_ENV 的变量,供后续同阶段 job 引用。
					carriedEnv = append(carriedEnv, captureStageEnv(ctx, rep, workspace)...)
				}

				for _, jb := range buildImageJobs {
					if canceled(ctx) {
						return run.ErrCanceled
					}
					if err := b.runBuildImageJob(ctx, sink, rep, jb, stage.Name, proj, settings, r.Trigger.ResolvedEnvironment, workspace, commitTag, hasPushJob); err != nil {
						return err
					}
				}

				b.collectScriptArtifacts(ctx, scriptJobs, workspace, slugify(proj.Name), stage.Name, rep)
				if err := collectStageReport(ctx, reportSink, r, stage, workspace, rep); err != nil {
					return err
				}
			}

			// ── 部署节点(deploy_ssh):把本 run 已产出的产物经 SSH 部署到目标机(中途部署,不动 run 终态)──
			for _, jb := range deployJobs {
				if canceled(ctx) {
					return run.ErrCanceled
				}
				if err := b.runDeployJob(ctx, rep, jb, r.ID); err != nil {
					return err
				}
			}

			// ── 通知节点(notify):按节点配的渠道发通知(best-effort,不因通知失败而失败本阶段)──
			for _, jb := range notifyJobs {
				if canceled(ctx) {
					return run.ErrCanceled
				}
				b.runNotifyJob(ctx, rep, jb, r)
			}

			return nil
		}()

		// 阶段后置步骤(P1 · 对标 Jenkins post):无论 job 成败,按 condition 在同工作区跑清理/通知/归档
		// (best-effort,post 失败只记日志、不改阶段结果)。用 WithoutCancel,使取消/失败后清理仍能跑。
		if hasPost && workspace != "" {
			b.runStagePost(context.WithoutCancel(ctx), sink, r, stage, workspace, jobErr != nil, rep)
		}
		return jobErr
	}
}

// runDeployJob 执行一个 deploy_ssh 节点:把本 run 已产出的产物经 SSH 部署到节点配置的目标机
// (复用 deploy.Service.DeployForStage 中途部署,不动 run 终态)。任一目标失败 → 阶段失败、阻断下游。
func (b *Builder) runDeployJob(ctx context.Context, rep dagrun.StageReporter, jb pipeline.Job, runID string) error {
	if b.deployer == nil {
		_ = rep.Log(ctx, streamStdout, "· 部署节点:部署服务未注入,跳过")
		return nil
	}
	serverID := cfgString(jb.Config, "serverId")
	if serverID == "" {
		_ = rep.Log(ctx, streamStderr, fmt.Sprintf("部署节点「%s」未选目标服务器(serverId 空)", jb.Name))
		return ErrBuildFailed
	}
	cfg := map[string]string{}
	if dp := cfgString(jb.Config, "deployPath"); dp != "" {
		cfg["releaseBase"] = dp
	}
	if rc := cfgString(jb.Config, "restartCommand"); rc != "" {
		cfg["restartCommand"] = rc
	}
	// 镜像产物部署参数(#51)透传:deploy.DeployForStage 经这些键挑镜像产物并组装
	// `docker run`(artifactType=image 选镜像;containerName/ports/runArgs 驱动容器名与端口/运行参数)。
	// 各值原样搬运(deploy 层 array 化、绝不拼 shell,守 AC-SEC-02);空值不入 cfg 保持默认。
	for _, k := range []string{"artifactType", "containerName", "ports", "runArgs"} {
		if v := cfgString(jb.Config, k); v != "" {
			cfg[k] = v
		}
	}
	strategy := cfgString(jb.Config, "strategy")
	stratLabel := strategy
	if stratLabel == "" {
		stratLabel = "rolling(默认)"
	}
	_ = rep.Log(ctx, streamStdout, fmt.Sprintf("→ SSH 部署本次产物到服务器 %s(策略 %s)…", serverID, stratLabel))
	results, err := b.deployer.DeployForStage(ctx, runID, []string{serverID}, cfg, strategy)
	if err != nil {
		_ = rep.Log(ctx, streamStderr, "部署失败:"+err.Error())
		return ErrBuildFailed
	}
	failed := false
	for _, dr := range results {
		line := fmt.Sprintf("· %s:%s — %s", dr.ServerName, dr.Status, dr.Message)
		if dr.Status == "success" {
			_ = rep.Log(ctx, streamStdout, line)
		} else {
			_ = rep.Log(ctx, streamStderr, line)
			failed = true
		}
	}
	if failed {
		return ErrBuildFailed
	}
	return nil
}

// runNotifyJob 执行一个 notify 节点:按节点配的渠道(id 或名称)发一条通知。
// best-effort:通知失败只记日志、不令阶段失败(与终态通知钩子一致)。
func (b *Builder) runNotifyJob(ctx context.Context, rep dagrun.StageReporter, jb pipeline.Job, r *run.Run) {
	if b.notifier == nil {
		_ = rep.Log(ctx, streamStdout, "· 通知节点:通知服务未注入,跳过")
		return
	}
	chRef := cfgString(jb.Config, "channel")
	if chRef == "" {
		_ = rep.Log(ctx, streamStdout, "· 通知节点:未配渠道,跳过")
		return
	}
	chID, chName := b.resolveChannel(ctx, chRef)
	if chID == "" {
		_ = rep.Log(ctx, streamStderr, "通知节点:找不到渠道「"+chRef+"」,跳过")
		return
	}
	commit := strings.TrimSpace(r.Trigger.Commit)
	if len(commit) > 7 {
		commit = commit[:7]
	}
	payload := notify.Payload{
		Title:  "流水线通知",
		Body:   fmt.Sprintf("流水线执行到通知节点:分支 %s,commit %s。", r.Trigger.Branch, commit),
		Fields: map[string]string{"branch": r.Trigger.Branch, "commit": commit, "runId": r.ID, "stage": "notify"},
	}
	if err := b.notifier.SendVia(ctx, chID, payload); err != nil {
		_ = rep.Log(ctx, streamStderr, "通知发送失败(best-effort):"+err.Error())
		return
	}
	_ = rep.Log(ctx, streamStdout, "· 已发通知 → 渠道「"+chName+"」")
}

// resolveChannel 把节点配的「渠道 id 或名称」解析为 (id, name);找不到返回空。
func (b *Builder) resolveChannel(ctx context.Context, ref string) (id, name string) {
	if ch, err := b.notifier.Get(ctx, ref); err == nil && ch != nil {
		return ch.ID, ch.Name
	}
	chs, err := b.notifier.List(ctx)
	if err != nil {
		return "", ""
	}
	for _, ch := range chs {
		if ch.Name == ref || ch.ID == ref {
			return ch.ID, ch.Name
		}
	}
	return "", ""
}

// runBuildImageJob 真实执行一个 build_image 节点:据节点 config 构造 BuildConfig,复用 Builder.build
// (模型 A=docker build / 模型 B=工具链构建)在宿主驱动上真实构建,产出镜像/jar/dist 产物。
// 镜像产物:环境绑定了 registry(或本阶段含 push_image 节点示意要推)→ b.push 推送并改写为远端引用 + digest;
// 无 registry → 镜像留本地,emit 本地 tag。其它产物(jar/dist)直接 emit。失败映射 ErrBuildFailed / 取消。
// 边界:docker build 上下文沿用工作区根(子目录上下文 = 后续);buildCommand 覆盖工具链默认命令 = 后续。
func (b *Builder) runBuildImageJob(ctx context.Context, sink run.StepSink, rep dagrun.StageReporter, jb pipeline.Job, stageName string, proj *project.Project, settings *pipeline.Settings, envName, workspace, commitTag string, hasPushJob bool) error {
	cfg := pipeline.BuildConfig{
		Model:          cfgString(jb.Config, "buildModel"),
		DockerfilePath: cfgString(jb.Config, "dockerfilePath"),
		Toolchain:      pipeline.Toolchain{Language: cfgString(jb.Config, "toolchainLanguage"), Version: cfgString(jb.Config, "toolchainVersion")},
		ArtifactType:   cfgString(jb.Config, "artifactType"),
	}
	if cfg.ArtifactType == "" {
		cfg.ArtifactType = pipeline.ArtifactImage
	}
	if cfg.Model == "" {
		cfg.Model = pipeline.BuildModelDockerfile
	}

	localTag, art, berr := b.build(ctx, sink, 0, proj, cfg, workspace, commitTag)
	if berr != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return run.ErrCanceled
		}
		_ = rep.Log(ctx, streamStderr, fmt.Sprintf("构建节点「%s」失败:%v", jb.Name, berr))
		return ErrBuildFailed
	}
	if art == nil {
		return nil
	}

	// 镜像产物 + 绑定了 registry → 推送并登记远端引用(与非-dag Builder 同语义)。
	if art.Type == run.ArtifactImage && localTag != "" {
		registry := b.resolveRegistry(settings, envName)
		switch {
		case registry != nil:
			remoteTag, digest, perr := b.push(ctx, sink, 0, localTag, registry, proj, commitTag)
			if perr != nil {
				if errors.Is(ctx.Err(), context.Canceled) {
					return run.ErrCanceled
				}
				_ = rep.Log(ctx, streamStderr, fmt.Sprintf("镜像推送失败:%v", perr))
				return ErrBuildFailed
			}
			art.Reference = remoteTag
			if digest != "" {
				if art.Metadata == nil {
					art.Metadata = map[string]any{}
				}
				art.Metadata["digest"] = digest
			}
		case hasPushJob:
			_ = rep.Log(ctx, streamStdout, "配了 push_image 但环境未绑定镜像仓库(registry),镜像留本地;到「触发设置 → 环境」绑定仓库后即自动推送")
		}
	}

	// 记来源节点(阶段 + job 名),供运行详情标注「哪个节点产的」。
	if art.Metadata == nil {
		art.Metadata = map[string]any{}
	}
	art.Metadata["sourceStage"] = stageName
	art.Metadata["sourceJob"] = jb.Name

	if err := rep.EmitArtifact(ctx, *art); err != nil {
		_ = rep.Log(ctx, streamStderr, "登记产物失败:"+err.Error())
	}
	return nil
}

// collectScriptArtifacts 收集 script job 声明的文件产物(job.Config["artifactPath"],**多行、每行一条**):
// 逐条定位工作区内路径 → 按类型(目录=dist、*.jar=jar、其它文件=archive)归档进制品库真字节 →
// EmitArtifact 登记。一个 job 可声明多条(既出 jar 又出 dist 等);未声明则跳过。
// 镜像类产物不走这里(走 build_image 节点);文件类才在此收集。
func (b *Builder) collectScriptArtifacts(ctx context.Context, jobs []pipeline.Job, workspace, slug, stageName string, rep dagrun.StageReporter) {
	onLine := func(stream, line string) { _ = rep.Log(ctx, stream, line) }
	for _, jb := range jobs {
		for _, rel := range splitCommands(renderTemplate(cfgString(jb.Config, "artifactPath"), templateContext(jb.Config))) { // 渲染 {{参数}} + 按行拆分
			// 通配(如 backend/target/*.jar)→ 展开为实际文件逐个收集;否则按原路径收集。
			if strings.ContainsAny(rel, "*?[") {
				clean := filepath.Clean(rel)
				if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
					onLine(streamStderr, "产物路径越界,已拒绝:"+rel)
					continue
				}
				matches, _ := filepath.Glob(filepath.Join(workspace, clean))
				if len(matches) == 0 {
					onLine(streamStderr, "产物通配未匹配,跳过:"+rel)
					continue
				}
				for _, m := range matches {
					if relMatch, rerr := filepath.Rel(workspace, m); rerr == nil {
						b.collectOneFileArtifact(ctx, workspace, relMatch, slug, jb.Name, stageName, rep, onLine)
					}
				}
				continue
			}
			b.collectOneFileArtifact(ctx, workspace, rel, slug, jb.Name, stageName, rep, onLine)
		}
	}
}

// collectOneFileArtifact 收集单条文件产物路径:越界(.. / 绝对)拒绝;定位不到打日志跳过(不致命);
// 类型按路径自动判(目录=dist、*.jar=jar、其它文件=archive)。产物 metadata 记来源节点(阶段 + job 名),
// 供运行详情标注「哪个节点产的」。制品库未注入时 emit 占位引用,向后兼容。
func (b *Builder) collectOneFileArtifact(ctx context.Context, workspace, rel, slug, jobName, stageName string, rep dagrun.StageReporter, onLine func(stream, line string)) {
	clean := filepath.Clean(rel)
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		onLine(streamStderr, "产物路径越界,已拒绝:"+rel)
		return
	}
	full := filepath.Join(workspace, clean)
	fi, err := os.Stat(full)
	if err != nil {
		onLine(streamStderr, "产物路径未找到,跳过:"+rel)
		return
	}
	// 产物名带上路径基名,避免一个 job 多产物同名(如多个 dist 目录)难以区分。
	base := filepath.Base(full)
	// metadata 记来源节点:sourceStage/sourceJob 供 UI 标注「哪个节点产的」(storeXxx 只补 stored/format,不清这些)。
	// workspacePath 记原始工作区相对路径,供跨阶段产物传递:下游阶段据此把本产物真字节恢复回原位
	// (如 backend/target/x.jar),使被拆到下游阶段的 build_image「COPY target/x.jar」仍能命中。
	art := &run.Artifact{Name: slug + "-" + base, Reference: base, Metadata: map[string]any{"sourceStage": stageName, "sourceJob": jobName, "workspacePath": clean}}
	switch {
	case fi.IsDir():
		art.Type = run.ArtifactDist
		art.SizeBytes = dirSize(full)
		b.storeDistDir(art, full, onLine)
	case strings.HasSuffix(strings.ToLower(full), ".jar"):
		art.Type = run.ArtifactJar
		art.SizeBytes = fileSize(full)
		b.storeJarBytes(art, full, onLine)
	default:
		art.Type = run.ArtifactArchive
		art.SizeBytes = fileSize(full)
		b.storeJarBytes(art, full, onLine) // 单文件原样字节归档(format=file)
	}
	if err := rep.EmitArtifact(ctx, *art); err != nil {
		onLine(streamStderr, "登记产物失败:"+err.Error())
		return
	}
	onLine(streamStdout, "已产出产物:"+art.Name+"("+art.Type+","+rel+")")
}

// scriptStepFromJob 从画布 job.Config 构造一条 script 步骤(image + 多行 commands + 可选 workDir)。
// image 缺失或 commands 全空 → 错误(诚实失败,不静默跳过)。
func scriptStepFromJob(jb pipeline.Job) (pipeline.PipelineStep, error) {
	ctx := templateContext(jb.Config)
	image := renderTemplate(cfgString(jb.Config, "image"), ctx)
	if image == "" {
		return pipeline.PipelineStep{}, errors.New("缺少运行镜像(image)")
	}
	// 自定义节点(templated):有 commandTemplate 则渲染 {{参数}} 作命令;否则用原始 commands。
	rawCmds := cfgString(jb.Config, "commands")
	if tpl := cfgString(jb.Config, "commandTemplate"); tpl != "" {
		rawCmds = renderTemplate(tpl, ctx)
	}
	cmds := splitCommands(rawCmds)
	if len(cmds) == 0 {
		return pipeline.PipelineStep{}, errors.New("缺少执行命令(commands / commandTemplate)")
	}
	return pipeline.PipelineStep{
		ID:       jb.ID,
		Name:     jb.Name,
		Type:     pipeline.StepTypeScript,
		Image:    image,
		Commands: cmds,
		Env:      matrixEnvVars(jb.Config), // 矩阵 cell 注入的 axis 环境变量(MATRIX_<AXIS>),空时 nil
		WorkDir:  renderTemplate(cfgString(jb.Config, "workDir"), ctx),
		// 任务级 timeout/retry/资源规格(P0 引擎能力):从 job.Config 自由 KV 读取(非负;非法/缺失→零值=旧行为)。
		TimeoutSeconds: cfgNonNegInt(jb.Config, "timeoutSeconds"),
		Retries:        cfgNonNegInt(jb.Config, "retries"),
		Resource: pipeline.Resource{
			CPU:    cfgString(jb.Config, "cpu"),
			Memory: cfgString(jb.Config, "memory"),
		},
	}, nil
}

// cfgNonNegInt 从自由 KV config 取非负整数(支持 JSON number 与字符串两种存法;
// 缺失/非法/负数 → 0,即「不限/不重试」的旧行为,向后兼容)。
func cfgNonNegInt(cfg map[string]any, key string) int {
	if cfg == nil {
		return 0
	}
	switch v := cfg[key].(type) {
	case float64: // encoding/json 把数字反序列化为 float64
		if v > 0 {
			return int(v)
		}
	case int:
		if v > 0 {
			return v
		}
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

// matrixEnvVars 读取矩阵展开(dagrun.ExpandMatrix)注入 job.Config 的 cell 环境变量,转为非 secret
// BuildVar(`MATRIX_<AXIS>=<值>`,容器内命令可 `$MATRIX_GO` 引用)。键 "__matrixEnv" 由调度层合成、
// 非用户配置;值通常是 map[string]string(内存构造),JSON 回环时为 map[string]any,两者皆容错。
// 普通(非矩阵)job 无此键 → 返回 nil(零行为变化)。
func matrixEnvVars(cfg map[string]any) []pipeline.BuildVar {
	if cfg == nil {
		return nil
	}
	raw, ok := cfg["__matrixEnv"]
	if !ok {
		return nil
	}
	collect := func(k, v string) pipeline.BuildVar {
		return pipeline.BuildVar{Key: k, Value: v, Secret: false}
	}
	switch m := raw.(type) {
	case map[string]string:
		out := make([]pipeline.BuildVar, 0, len(m))
		for k, v := range m {
			out = append(out, collect(k, v))
		}
		return out
	case map[string]any:
		out := make([]pipeline.BuildVar, 0, len(m))
		for k, v := range m {
			if s, ok := v.(string); ok {
				out = append(out, collect(k, s))
			}
		}
		return out
	default:
		return nil
	}
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
