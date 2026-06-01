package build

// remote_stage_exec.go 把「按项目 runner 配置派发到远程构建机执行」接到 DAG 阶段执行器(FR-8-14 续):
// 项目配了远程 runner → 本阶段的 script job 下沉到该远程机执行;否则本地执行(NewStageExecutor 原状)。
//
// 远程执行模型(**token 全程只在控制机,绝不上远程**——比"远程克隆"更安全,且远程无需装 git):
//  1. 控制机本地克隆源码(复用 b.cloner;若装了 repocache 则走本地镜像增量,快)。
//  2. 把本地工作区打成 tar.gz,经 SSH(target.Upload = stdin 流,无 argv 长度限)传到远程临时目录并解包。
//  3. 在远程机用容器跑 script job(NewRemoteDriver:shellDriver 经 SSH Commander → 远程 `docker run`),
//     日志经 reporterSink 回流。
//  4. 收尾删远程工作区(尽力)。
//
// 安全/语义沿用:命令 array 化(不拼 shell);secret 经 -e 注入容器(与本地同,不更差);失败映射 ErrBuildFailed。
// 边界(后续增量):远程测试报告/质量门禁采集(报告文件在远程,本期不回采,优雅跳过);多 runner 负载均衡 /
// 按 stage 选 runner。

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/huangchengsir/pipewright/internal/dagrun"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
)

// RunnerLookup 解析某项目的远程 runner 服务器 id(空/false = 本地构建)。runner.Service 即满足。
type RunnerLookup interface {
	RunnerFor(ctx context.Context, projectID string) (serverID string, ok bool)
}

// remoteExec 抽象远程执行所需的 target 能力(Exec + Upload;target.Service 即满足;便于 fake 单测)。
type remoteExec interface {
	RemoteExecer // Exec(ctx, serverID, cmd) (*target.ExecResult, error)
	Upload(ctx context.Context, serverID string, content io.Reader, remotePath string) error
}

// NewStageExecutorWithRunner 返回「按项目 runner 配置派发本地/远程」的阶段执行器。
// lookup 或 tgt 为 nil → 退化为纯本地(NewStageExecutor)。
func NewStageExecutorWithRunner(b *Builder, reportSink TestReportSink, lookup RunnerLookup, tgt remoteExec) dagrun.StageExecutor {
	local := NewStageExecutor(b, reportSink)
	if lookup == nil || tgt == nil {
		return local
	}
	return func(ctx context.Context, r *run.Run, stage pipeline.Stage, rep dagrun.StageReporter) error {
		if serverID, ok := lookup.RunnerFor(ctx, r.ProjectID); ok && strings.TrimSpace(serverID) != "" {
			return b.runStageRemote(ctx, r, stage, rep, serverID, tgt)
		}
		return local(ctx, r, stage, rep)
	}
}

// runStageRemote 在远程 runner 上执行本阶段的 script job(见文件头模型)。
func (b *Builder) runStageRemote(ctx context.Context, r *run.Run, stage pipeline.Stage, rep dagrun.StageReporter, serverID string, tgt remoteExec) error {
	scriptJobs := make([]pipeline.Job, 0, len(stage.Jobs))
	for _, jb := range stage.Jobs {
		if isScriptJob(jb.Type) {
			scriptJobs = append(scriptJobs, jb)
		}
	}
	if len(scriptJobs) == 0 {
		for _, jb := range stage.Jobs {
			_ = rep.Log(ctx, streamStdout, fmt.Sprintf("· %s(%s)— 远程 runner 仅执行 script 类型;本阶段放行", jb.Name, jb.Type))
		}
		return nil
	}

	proj, _, perr := b.resolve(ctx, r)
	if perr != nil {
		_ = rep.Log(ctx, streamStderr, "无法加载项目构建配置:"+perr.Error())
		return ErrBuildFailed
	}

	// 1) 控制机本地克隆(token 只在控制机)。
	workspace, mkErr := mkTempWorkspace()
	if mkErr != nil {
		_ = rep.Log(ctx, streamStderr, "创建临时工作区失败:"+mkErr.Error())
		return ErrBuildFailed
	}
	defer func() { _ = os.RemoveAll(workspace) }()

	token := b.revealToken(proj.CredentialID)
	resolved, cerr := b.cloner.Clone(ctx, proj.RepoURL, token, r.Trigger.Branch, r.Trigger.Commit, workspace)
	token = ""
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

	// 2) 打包工作区 → 经 SSH 传到远程并解包。
	remoteWS := "/tmp/pipewright-remote/" + sanitizeRemoteSeg(r.ID) + "-" + sanitizeRemoteSeg(stage.ID)
	remoteTar := remoteWS + ".tar.gz"
	_ = rep.Log(ctx, streamStdout, "→ 远程 runner:打包工作区并经 SSH 传输…")
	if err := uploadWorkspace(ctx, tgt, serverID, workspace, remoteTar); err != nil {
		_ = rep.Log(ctx, streamStderr, "传输工作区到远程失败:"+err.Error())
		return ErrBuildFailed
	}
	// 远程解包 + 收尾清理(尽力)。
	if out, eerr := tgt.Exec(ctx, serverID, []string{"sh", "-c", `mkdir -p "$0" && tar -xzf "$1" -C "$0" && rm -f "$1"`, remoteWS, remoteTar}); eerr != nil || (out != nil && out.ExitCode != 0) {
		_ = rep.Log(ctx, streamStderr, "远程解包工作区失败")
		return ErrBuildFailed
	}
	defer func() { _, _ = tgt.Exec(context.WithoutCancel(ctx), serverID, []string{"rm", "-rf", remoteWS}) }()

	// 3) 在远程机用容器跑 script job(远程 driver:docker run 经 SSH)。
	driver := NewRemoteDriver(tgt, serverID, "docker")
	onLine := func(stream, line string) { _ = rep.Log(ctx, stream, line) }
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
		if err := b.runScriptOnDriver(ctx, driver, onLine, step, remoteWS); err != nil {
			return err
		}
	}

	_ = rep.Log(ctx, streamStdout, fmt.Sprintf("✓ 远程 runner(%s)执行完成;测试报告/质量门禁在远程模式暂不回采(后续增量)", serverID))
	return nil
}

// runScriptOnDriver 用给定 driver(本地或远程)跑一条 script 步骤;remoteWS 为容器挂载的工作区(远程机上的路径)。
// 与 runScriptStep 同语义(env 注入、workdir、多行 set -e 脚本、array 不拼 host shell),只是 driver 可注入。
func (b *Builder) runScriptOnDriver(ctx context.Context, driver Driver, onLine func(stream, line string), step pipeline.PipelineStep, remoteWS string) error {
	plain, secretEnv := b.buildArgs(step.Env)
	env := append(plain, secretEnv...)

	workdir := scriptWorkspaceMount
	if sub := strings.TrimSpace(step.WorkDir); sub != "" {
		workdir = joinContainerPath(scriptWorkspaceMount, sub)
	}
	script := "set -e\n" + strings.Join(step.Commands, "\n")
	cmd := []string{"sh", "-c", script}

	// 资源规格透传给远程 docker run(--cpus/--memory);远程 docker 不支持时由其自身报错(诚实边界)。
	// 远程模式本期不套 timeout/retry(留后续增量)。
	code, err := driver.RunToolchain(ctx, step.Image, remoteWS, workdir, env, cmd, step.Resource, onLine)
	if err != nil && code < 0 {
		if errors.Is(ctx.Err(), context.Canceled) {
			return run.ErrCanceled
		}
		return ErrBuildFailed
	}
	if code != 0 {
		return ErrBuildFailed
	}
	return nil
}

// uploadWorkspace 把本地工作区打成 tar.gz(流式)经 target.Upload 传到远程 remoteTar。
func uploadWorkspace(ctx context.Context, tgt remoteExec, serverID, workspace, remoteTar string) error {
	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(tarGzDir(workspace, pw)) }()
	err := tgt.Upload(ctx, serverID, pr, remoteTar)
	_ = pr.Close()
	return err
}

// sanitizeRemoteSeg 把 id 净化为安全的远程路径段(仅字母数字 . _ -;其余替 _;空 → x)。
func sanitizeRemoteSeg(s string) string {
	s = strings.TrimSpace(s)
	var b bytes.Buffer
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "x"
	}
	return b.String()
}
