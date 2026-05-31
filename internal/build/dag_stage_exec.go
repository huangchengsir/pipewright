package build

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
// 模板节点,本质就是带预填命令的 script(在隔离容器跑 + 收 artifactPath 产物),故按 script 执行。
func isScriptJob(jobType string) bool {
	t := strings.TrimSpace(jobType)
	return t == pipeline.StepTypeScript || t == "custom" || t == "build_frontend" || t == "build_backend"
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
		// 没有任何可执行节点(script/build_image/deploy_ssh/notify)→ 诚实占位放行。
		if !needsBuild && len(deployJobs) == 0 && len(notifyJobs) == 0 {
			for _, jb := range stage.Jobs {
				_ = rep.Log(ctx, streamStdout, fmt.Sprintf("· %s(%s)— 真实执行未接入;本阶段放行", jb.Name, jb.Type))
			}
			if len(stage.Jobs) == 0 {
				_ = rep.Log(ctx, streamStdout, fmt.Sprintf("阶段「%s」无 job", stage.Name))
			}
			return nil
		}

		sink := &reporterSink{rep: rep}

		// ── 构建部分:仅当有 script / build_image 才克隆工作区并执行(部署/通知不需要工作区)──
		if needsBuild {
			proj, settings, perr := b.resolve(ctx, r)
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
			if resolved != nil && resolved.CommitShort != "" && b.recordCommit != nil {
				b.recordCommit(ctx, r.ID, resolved.CommitShort)
			}

			for _, jb := range scriptJobs {
				if canceled(ctx) {
					return run.ErrCanceled
				}
				step, verr := scriptStepFromJob(jb)
				if verr != nil {
					_ = rep.Log(ctx, streamStderr, fmt.Sprintf("script job「%s」配置无效:%v", jb.Name, verr))
					return ErrBuildFailed
				}
				step.Env = append(runParamsAsEnv(r.Trigger.Params), step.Env...)
				if err := b.runScriptStep(ctx, sink, 0, step, workspace); err != nil {
					return err // ErrBuildFailed / run.ErrCanceled
				}
			}

			commitTag := "latest"
			if resolved != nil && resolved.CommitShort != "" {
				commitTag = resolved.CommitShort
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
		for _, rel := range splitCommands(cfgString(jb.Config, "artifactPath")) { // 复用按行拆分(trim + 去空行)
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
	art := &run.Artifact{Name: slug + "-" + base, Reference: base, Metadata: map[string]any{"sourceStage": stageName, "sourceJob": jobName}}
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
