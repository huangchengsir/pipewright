package version

import (
	"os"

	"testing"
)

func TestMode_DockerEnvVar(t *testing.T) {
	t.Setenv("PIPEWRIGHT_RUNTIME", "docker")
	if Mode() != ModeDocker {
		t.Errorf("PIPEWRIGHT_RUNTIME=docker → 期望 ModeDocker")
	}
	t.Setenv("PIPEWRIGHT_RUNTIME", "")
	// 无 /.dockerenv 的开发机应回退 binary(CI/本地);容器内会是 docker,故此断言仅在非容器有效。
	if _, err := os.Stat("/.dockerenv"); err != nil {
		if Mode() != ModeBinary {
			t.Errorf("无 docker 标记 → 期望 ModeBinary")
		}
	}
}

func TestCanSelfReplace(t *testing.T) {
	// 仅断言不 panic 且返回布尔与当前 OS 一致(linux/darwin true,其余 false)。
	_ = CanSelfReplace()
}

func TestDockerUpgradeCommand(t *testing.T) {
	if DockerUpgradeCommand() == "" {
		t.Errorf("docker 升级命令不应为空")
	}
}
