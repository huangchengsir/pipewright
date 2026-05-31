package build

// remote.go 是「远程构建 runner 池」(Story 8-14 / FR-8-14)的**远程执行地基**:把构建工具链命令
// 从中控机下沉到一台已登记的远程构建机执行,复用 target 层的 SSH 通道(Epic 4 / Story 4.1)。
//
// 设计:沿用本包既有的 Commander 抽象(driver.go)——shellDriver 经 Commander 跑 docker/nerdctl/podman
// 子命令。本文件提供 **SSH 版 Commander**(sshCommander):同一条 array 命令不在本机 os/exec 跑,
// 而是经 target.Service.Exec 投递到远程机执行,退出码/输出原样取回。于是
//
//	NewRemoteDriver(execer, serverID, "docker")  →  shellDriver{bin:"docker", cmdr: sshCommander}
//
// 即得到一个「在远程机上跑容器工具链」的 Driver,可注入 Builder(WithDriver),无需改 Builder/DAG。
//
// 边界(本期地基,后续增量另起):
//   - **流式**:target.Service.ExecStream 只回 io.ReadCloser(无退出码/无 stdout-stderr 分流),
//     而 Commander.Stream 需退出码。故本期 Stream 走「同步 Exec + 逐行回放」(命令完成后整段回放
//     日志再返回退出码);实时逐字流式留待 target 层补「带退出码的流」后再接。
//   - **stdin**:target.Exec 不收 stdin,故 docker login --password-stdin 这类 secret-stdin 操作
//     在远程模式暂不支持(Run/Stream stdin 非空 → 明确报错)。镜像推送的远程化留后续增量。
//   - **远程克隆 + runner 调度/标签**:把源码安全送上远程机(token 不进 argv/日志,需 target 层加
//     stdin/env 注入)与「按 stage 选 runner」的调度,是后续增量;本文件只交付**远程命令执行内核**
//     (已单测 + 真容器 e2e)。

import (
	"context"
	"errors"
	"strings"

	"github.com/huangchengsir/pipewright/internal/target"
)

// ErrRemoteStdinUnsupported 表示远程模式暂不支持 stdin(如 docker login --password-stdin)。
var ErrRemoteStdinUnsupported = errors.New("build: remote runner does not support stdin (e.g. --password-stdin)")

// RemoteExecer 抽象「在某远程机上执行一条 array 命令」的能力(target.Service 即满足)。
// 便于 fake 单测注入(不触真 SSH);array 命令经 target 层 shell 转义后交 SSH session(AC-SEC-02)。
type RemoteExecer interface {
	Exec(ctx context.Context, serverID string, cmd []string) (*target.ExecResult, error)
}

// sshCommander 是经 SSH 投递到远程机执行的 Commander(实现 build.Commander)。
// 同一条命令不在本机跑,而是 execer.Exec(serverID, cmd) 投到远程;退出码/输出原样取回。
type sshCommander struct {
	execer   RemoteExecer
	serverID string
}

// NewRemoteCommander 构造一个把命令投递到 serverID 远程机执行的 Commander。
func NewRemoteCommander(execer RemoteExecer, serverID string) Commander {
	return &sshCommander{execer: execer, serverID: serverID}
}

// NewRemoteDriver 构造一个「在远程机上跑容器工具链」的 Driver:shellDriver 经 SSH Commander 执行,
// bin 为远程机上的容器 CLI 名(空 → 默认 "docker")。可注入 Builder(WithDriver)使构建在远程机发生。
func NewRemoteDriver(execer RemoteExecer, serverID, bin string) Driver {
	if strings.TrimSpace(bin) == "" {
		bin = "docker"
	}
	return &shellDriver{bin: bin, cmdr: NewRemoteCommander(execer, serverID)}
}

// Run 实现 Commander.Run:把 name+args 投到远程机同步执行,取回 stdout/stderr/退出码。
// stdin 非空 → ErrRemoteStdinUnsupported(远程模式本期不支持 secret-stdin)。
// exitCode<0 表示无法在远程启动 / SSH 连接错误(err 非 nil)。
func (c *sshCommander) Run(ctx context.Context, name string, args []string, stdin string) (string, string, int, error) {
	if stdin != "" {
		return "", "", -1, ErrRemoteStdinUnsupported
	}
	cmd := make([]string, 0, len(args)+1)
	cmd = append(cmd, name)
	cmd = append(cmd, args...)
	res, err := c.execer.Exec(ctx, c.serverID, cmd)
	if err != nil {
		return "", "", -1, err
	}
	if res == nil {
		return "", "", -1, nil
	}
	return res.Stdout, res.Stderr, res.ExitCode, nil
}

// Stream 实现 Commander.Stream:本期走「同步 Exec + 逐行回放」(见文件头边界说明)。
// 命令在远程机完成后,把 stdout/stderr 逐行回调 onLine,再返回退出码。stdin 非空 → 报错。
func (c *sshCommander) Stream(ctx context.Context, name string, args []string, stdin string, onLine func(stream, line string)) (int, error) {
	if stdin != "" {
		return -1, ErrRemoteStdinUnsupported
	}
	cmd := make([]string, 0, len(args)+1)
	cmd = append(cmd, name)
	cmd = append(cmd, args...)
	res, err := c.execer.Exec(ctx, c.serverID, cmd)
	if err != nil {
		return -1, err
	}
	if res == nil {
		return -1, nil
	}
	if onLine != nil {
		replayLines(res.Stdout, "stdout", onLine)
		replayLines(res.Stderr, "stderr", onLine)
	}
	return res.ExitCode, nil
}

// replayLines 把整段输出按行回调 onLine(去掉末尾空行;保持与逐行流式同形,供 sink 落库/SSE)。
func replayLines(out, stream string, onLine func(stream, line string)) {
	if out == "" {
		return
	}
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		onLine(stream, line)
	}
}
