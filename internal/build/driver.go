// Package build 是「隔离构建 + 镜像推送」的领域执行层(FR-5/FR-6/FR-7 · Story 3-3/3-5)。
//
// 它实现 run.Runner:拿到一次运行 → 取项目(RepoURL/凭据)+ 流水线构建配置(BuildConfig)
// → 在临时工作区克隆源码 → 经容器 CLI 隔离构建产 image/jar/dist → 镜像产物且配了仓库时推送
// → 经同一 run.StepSink 喂真实日志/产物/失败日志,无缝复用既有持久化/SSE/脱敏管道(3-1 地基)。
//
// 设计原则:
//   - 不强绑 docker daemon:容器 CLI 抽象成 Driver(自动探测 docker→nerdctl→podman 取首个可用),
//     底层执行抽象成 Commander(便于 fake 单测注入,仿 target.SSHDialer 模式)。
//   - 命令一律 array []string,绝不拼 shell(AC-SEC-02 防注入);secret 经 stdin/--build-arg
//     注入但绝不回显进可见日志(脱敏由 sink 侧 Masker 兜底,本包亦不主动打印明文)。
//   - 宿主零污染:工作区即用即销(MkdirTemp + defer RemoveAll,FR-5)。
//   - 无 Docker 降级:探测不到任何 CLI → 诚实失败(不假装成功),由 main 经开关退回 StubRunner。
//
// 无 init 副作用、无包级重对象(探测惰性,首次构建才执行),避免抬高空载内存。
package build

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
)

// 领域错误。错误体绝不含明文凭据/master key。
var (
	// ErrNoContainerCLI 表示宿主未找到任何受支持的容器 CLI(docker/nerdctl/podman),构建器不可用。
	ErrNoContainerCLI = errors.New("build: no container CLI found (docker/nerdctl/podman)")
	// ErrBuildFailed 表示某构建/推送命令以非零退出(失败日志已经 StepSink 报告)。
	ErrBuildFailed = errors.New("build: command failed")
)

// candidateBinaries 是容器 CLI 探测顺序(取 PATH 首个可用)。docker 优先(最常见),
// 其后 nerdctl(containerd 生态)、podman(无 daemon)。三者命令面在本包用到的子集兼容。
var candidateBinaries = []string{"docker", "nerdctl", "podman"}

// Commander 抽象「执行一条 array 命令」的底层能力(便于 fake 单测注入,仿 target.SSHDialer)。
//
// Run 同步执行 name+args,收集 stdout/stderr,返回退出码;ctx 取消即 kill 子进程。
// Stream 执行并把 stdout/stderr 逐行回调 onLine(stream ∈ "stdout"|"stderr"),用于实时喂日志;
// stdin 非空时作为子进程标准输入(用于 docker login --password-stdin,口令绝不进 argv/日志)。
type Commander interface {
	// Run 执行命令,返回合并后的 stdout、stderr 与退出码。exitCode<0 表示无法启动(命令不存在等)。
	Run(ctx context.Context, name string, args []string, stdin string) (stdout, stderr string, exitCode int, err error)
	// Stream 执行命令,逐行回调 onLine(stream, line)。返回退出码;exitCode<0 表示无法启动。
	Stream(ctx context.Context, name string, args []string, stdin string, onLine func(stream, line string)) (exitCode int, err error)
}

// execCommander 是基于 os/exec 的默认 Commander 实现。
//
// 子进程经 exec.CommandContext 绑 ctx:ctx 取消即 kill(SIGKILL)子进程(构建/推送可被取消)。
// 命令名 + 参数以 array 传入,绝不经 shell 解释(无注入面)。stdin 仅用于 --password-stdin。
type execCommander struct{}

func (execCommander) Run(ctx context.Context, name string, args []string, stdin string) (string, string, int, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	exitCode := exitCodeOf(err)
	return outBuf.String(), errBuf.String(), exitCode, err
}

func (execCommander) Stream(ctx context.Context, name string, args []string, stdin string, onLine func(stream, line string)) (int, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return -1, err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return -1, err
	}
	if err := cmd.Start(); err != nil {
		return -1, err
	}

	// 并发逐行抽取 stdout/stderr(逐行回调 onLine);两路读完才 Wait,避免管道缓冲填满阻塞子进程。
	done := make(chan struct{}, 2)
	scan := func(r io.Reader, stream string) {
		defer func() { done <- struct{}{} }()
		sc := bufio.NewScanner(r)
		// 放大单行上限(默认 64KB 易被超长构建行截断)。
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			if onLine != nil {
				onLine(stream, sc.Text())
			}
		}
	}
	go scan(stdoutPipe, "stdout")
	go scan(stderrPipe, "stderr")
	<-done
	<-done

	err = cmd.Wait()
	return exitCodeOf(err), err
}

// exitCodeOf 从 exec 错误提取退出码:nil → 0;ExitError → 真实退出码;其它(无法启动/被 kill)→ -1。
func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if code := ee.ExitCode(); code >= 0 {
			return code
		}
		return -1 // 被信号杀(如 ctx 取消)
	}
	return -1
}

// Driver 抽象「容器 CLI 能力」:构建、打标、推送、登录、取镜像 digest/size。
// 默认实现 shellDriver shell 到探测出的 binary;命令一律 array,经 Commander 执行。
type Driver interface {
	// Binary 返回选中的容器 CLI 名(docker/nerdctl/podman),供日志展示(不含路径)。
	Binary() string
	// Build 在 contextDir 下用 dockerfile 构建并打 localTag(模型 A)。buildArgs 为非 secret
	// 构建变量(K=V);secretArgs 为 secret 构建变量(K=V,经 --build-arg 注入但**绝不**回显到
	// 可见日志,命令回显里只列 key)。逐行日志经 onLine 回调。返回退出码。
	Build(ctx context.Context, contextDir, dockerfile, localTag string, buildArgs, secretArgs []string, onLine func(stream, line string)) (int, error)
	// RunToolchain 模型 B:用工具链镜像挂载工作区跑构建命令产 jar/dist。
	// image=工具链镜像:版本;workdir=容器内挂载点;hostDir=宿主工作区;cmd=构建命令 array。
	RunToolchain(ctx context.Context, image, hostDir, workdir string, env []string, cmd []string, onLine func(stream, line string)) (int, error)
	// Tag 给 localTag 打远端 remoteTag(推送前)。
	Tag(ctx context.Context, localTag, remoteTag string, onLine func(stream, line string)) (int, error)
	// Login 经 --password-stdin 登录仓库(password 经 stdin,**绝不**进 argv/日志)。
	Login(ctx context.Context, registryURL, username, password string, onLine func(stream, line string)) (int, error)
	// Push 推送 remoteTag,逐行喂日志。返回退出码。
	Push(ctx context.Context, remoteTag string, onLine func(stream, line string)) (int, error)
	// InspectImage 取镜像的 digest(repoDigest 或 image id)与字节大小(用于产物 metadata)。
	InspectImage(ctx context.Context, ref string) (digest string, sizeBytes int64, err error)
}

// DetectDriver 探测容器 CLI(取 PATH 首个可用),返回默认 shellDriver。
// 全部不可用 → ErrNoContainerCLI(供 main/Builder 据此降级,诚实不假装)。
// cmdr 为 nil 时用默认 execCommander(生产路径);测试注入 fake。
func DetectDriver(cmdr Commander) (Driver, error) {
	if cmdr == nil {
		cmdr = execCommander{}
	}
	bin, err := detectBinary(lookPath)
	if err != nil {
		return nil, err
	}
	return &shellDriver{bin: bin, cmdr: cmdr}, nil
}

// lookPath 是默认 PATH 探测(可注入便于测试)。
var lookPath = exec.LookPath

// detectBinary 按 candidateBinaries 顺序在 PATH 找首个可用 CLI;全无 → ErrNoContainerCLI。
// look 参数化便于测试注入(避免依赖宿主真装 docker)。
func detectBinary(look func(string) (string, error)) (string, error) {
	for _, name := range candidateBinaries {
		if _, err := look(name); err == nil {
			return name, nil
		}
	}
	return "", ErrNoContainerCLI
}
