package target

// container_exec_e2e_test.go 是 Story 6-4 容器终端**真 docker exec** 端到端测试。
//
// interactive_e2e_test.go 已验真 SSH PTY 传输;本测试再补最后一环:经真 SSH PTY 对真
// **运行中容器**跑 `docker exec` 并确认进到了容器内部(读回容器内预置标记)。串起
// 「WS ↔ SSH → docker exec -it <container> <shell>」生产路径里 docker 那一层。
//
// 需同时具备:本机 sshd(免密)+ docker daemon。默认 SKIP:仅 PIPEWRIGHT_E2E_DOCKER=1 时跑。
// 跑法:PIPEWRIGHT_E2E_DOCKER=1 go test ./internal/target/ -run E2EContainerExec -race -v
//
// 真实发现(已记 deferred-work):生产 6-4 用相对 `docker` 作命令名,而非交互 SSH 的 PATH 常
// 只有 /usr/bin:/bin:/usr/sbin:/sbin(本机 docker 在 /usr/local/bin)→ 远端「command not
// found」。本测试用 docker 绝对路径验证 exec 机制本身;PATH 健壮性留作改进项。

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// dockerAbsOrSkip 找 docker 可执行绝对路径(供 SSH 远端用,绕开非交互 PATH 缺失)。
func dockerAbsOrSkip(t *testing.T) string {
	t.Helper()
	if os.Getenv("PIPEWRIGHT_E2E_DOCKER") != "1" {
		t.Skip("设 PIPEWRIGHT_E2E_DOCKER=1 启用真 docker exec 容器终端 e2e")
	}
	for _, p := range []string{"/usr/local/bin/docker", "/opt/homebrew/bin/docker"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath("docker"); err == nil {
		return p
	}
	t.Skip("未找到 docker 可执行")
	return ""
}

// TestE2EContainerExecRealSSH 经真 SSH PTY 对真容器跑 docker exec,读回容器内标记确认进入。
func TestE2EContainerExecRealSSH(t *testing.T) {
	dockerBin := dockerAbsOrSkip(t)
	key := loadE2EKey(t)
	user := os.Getenv("USER")
	if user == "" {
		t.Skip("USER 为空")
	}

	// 起一个真容器(自管生命周期),容器内预置唯一标记文件。
	const name = "pw-e2e-term-exec"
	const marker = "CONTAINER_INSIDE_MARKER_9q7"
	_ = exec.Command(dockerBin, "rm", "-f", name).Run()
	if out, err := exec.Command(dockerBin, "run", "-d", "--name", name, "busybox", "sleep", "600").CombinedOutput(); err != nil {
		t.Skipf("起测试容器失败: %v\n%s", err, string(out))
	}
	defer func() { _ = exec.Command(dockerBin, "rm", "-f", name).Run() }()
	if out, err := exec.Command(dockerBin, "exec", name, "sh", "-c", "echo "+marker+" > /inside").CombinedOutput(); err != nil {
		t.Fatalf("容器内预置标记失败: %v\n%s", err, string(out))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	// 生产同形命令(docker 用绝对路径绕 SSH PATH):docker exec -it <container> cat /inside。
	cmd := []string{dockerBin, "exec", "-i", name, "cat", "/inside"}
	sess, err := sshDialer{}.RunInteractive(ctx, "localhost:22",
		SSHConfig{User: user, PrivateKey: key}, cmd)
	if err != nil {
		t.Fatalf("经 SSH 开 docker exec 交互会话失败: %v", err)
	}

	var out bytes.Buffer
	done := make(chan struct{})
	go func() { _, _ = io.Copy(&out, sess); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("等待 docker exec 输出超时")
	}
	_ = sess.Close()

	got := out.String()
	if !strings.Contains(got, marker) {
		t.Fatalf("未读回容器内标记 → docker exec 未真正进入容器\n输出:\n%s", got)
	}
	t.Logf("✅ 经真 SSH PTY → docker exec 进真容器,读回容器内标记 OK:%s", strings.TrimSpace(got))
}
