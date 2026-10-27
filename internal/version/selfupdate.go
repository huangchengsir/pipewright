package version

import (
	"os"

	"runtime"
	"strings"
	"syscall"
)

// DeploymentMode 区分运行形态,决定自更新策略。
type DeploymentMode string

const (
	ModeBinary DeploymentMode = "binary" // 裸机 / install.sh 二进制 —— 可自替换 + 自重启
	ModeDocker DeploymentMode = "docker" // 容器 —— 不能替换自身镜像,给升级命令
)

// Mode 探测当前部署形态。容器内 Docker 会创建 /.dockerenv;另可经 PIPEWRIGHT_RUNTIME=docker 显式声明。
func Mode() DeploymentMode {
	if strings.EqualFold(os.Getenv("PIPEWRIGHT_RUNTIME"), "docker") {
		return ModeDocker
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return ModeDocker
	}
	return ModeBinary
}

// DockerUpgradeCommand 返回 Docker 部署的升级命令(compose 优先,附 docker run 兜底)。
func DockerUpgradeCommand() string {
	return "docker compose pull && docker compose up -d"
}

// CanSelfReplace 报告当前平台是否支持替换运行中的可执行文件。
// Windows 无法替换正在运行的 exe;仅 linux/darwin 支持。
func CanSelfReplace() bool {
	return runtime.GOOS == "linux" || runtime.GOOS == "darwin"
}

// Reexec 用新二进制替换当前进程映像(同 PID,重新绑定端口)。
// 监听 socket 在 exec 时关闭,新进程重新 bind;调用前须确保响应已发出。
func Reexec() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return syscall.Exec(exe, os.Args, os.Environ())
}
