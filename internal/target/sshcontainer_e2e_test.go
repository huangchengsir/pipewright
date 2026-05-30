package target

// sshcontainer_e2e_test.go 提供一个**自起自清的真 Linux + openssh 目标容器** helper,
// 供 Epic 4 部署 / Epic 6 运维的真 e2e 复用(本包内的 e2e 直接用;deploy / httpapi 各有同形 helper)。
//
// 与 interactive_e2e_test.go(连本机 localhost sshd)不同:本 helper 用 docker 起一个最小
// **alpine + sshd** 容器当「真目标服务器」,注入一对**仅测试用**的 ed25519 key,root 免密登录。
// 这样 fake/localhost 证不到的真实部署/运维(真 /proc 指标、真文件落地、真零停机切换)可对一台
// 干净的 Linux 目标真跑。
//
// 默认 SKIP:仅 PIPEWRIGHT_E2E_DEPLOY=1 且本机有 docker 时运行(CI 无 docker)。
// 容器生命周期由 t.Cleanup 兜底 docker rm -f,绝不留垃圾容器。

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// sshContainer 是一台已就绪的真 SSH 目标容器(host/port/私钥)。
type sshContainer struct {
	Name       string
	Host       string
	Port       int
	PrivateKey string // PEM(OpenSSH ed25519),仅测试用
	dockerBin  string
}

// dockerBinOrSkip 找 docker 绝对路径;无则 skip(CI 无 docker)。
func dockerBinOrSkip(t *testing.T) string {
	t.Helper()
	for _, p := range []string{"/usr/local/bin/docker", "/opt/homebrew/bin/docker", "/usr/bin/docker"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath("docker"); err == nil {
		return p
	}
	t.Skip("未找到 docker 可执行,跳过真 SSH 容器 e2e")
	return ""
}

// requireE2EDeploy 是所有部署/运维真 e2e 的统一门禁:默认 SKIP,仅 PIPEWRIGHT_E2E_DEPLOY=1 启用。
func requireE2EDeploy(t *testing.T) {
	t.Helper()
	if os.Getenv("PIPEWRIGHT_E2E_DEPLOY") != "1" {
		t.Skip("设 PIPEWRIGHT_E2E_DEPLOY=1 启用真 Linux SSH 目标部署/运维 e2e")
	}
}

// genTestSSHKey 生成一对仅测试用的 ed25519 key,返回 (PEM 私钥, authorized_keys 行)。
func genTestSSHKey(t *testing.T) (privPEM string, authLine string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成测试 ed25519 key: %v", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatalf("封装 OpenSSH 私钥: %v", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("封装 SSH 公钥: %v", err)
	}
	return string(pem.EncodeToMemory(block)), strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))
}

// startSSHContainer 起一个 alpine + sshd 容器当真目标,注入测试 key 后轮询直至 SSH 可连。
// 返回就绪容器 + 自动 t.Cleanup 销毁。任一步失败 → t.Fatalf(已过门禁,失败即真问题)。
func startSSHContainer(t *testing.T) *sshContainer {
	t.Helper()
	requireE2EDeploy(t)
	dockerBin := dockerBinOrSkip(t)

	privPEM, authLine := genTestSSHKey(t)
	name := fmt.Sprintf("pw-e2e-sshd-%d-%d", os.Getpid(), time.Now().UnixNano()%100000)
	_ = exec.Command(dockerBin, "rm", "-f", name).Run()

	// 容器内:装 openssh → 生成 host key → 写 authorized_keys → 允许 root pubkey 登录 → 前台跑 sshd。
	// authorized_keys 经位置参数 $0 注入(不拼进脚本体,纵深防御)。
	bootstrap := `set -e
apk add --no-cache openssh >/dev/null
ssh-keygen -A
mkdir -p /root/.ssh
printf '%s\n' "$0" > /root/.ssh/authorized_keys
chmod 700 /root/.ssh
chmod 600 /root/.ssh/authorized_keys
sed -i 's/^#\?PermitRootLogin.*/PermitRootLogin yes/' /etc/ssh/sshd_config
sed -i 's/^#\?PubkeyAuthentication.*/PubkeyAuthentication yes/' /etc/ssh/sshd_config
exec /usr/sbin/sshd -D -e`

	args := []string{"run", "-d", "--name", name, "-p", "0:22", "alpine:3.20",
		"sh", "-c", bootstrap, authLine}
	if out, err := exec.Command(dockerBin, args...).CombinedOutput(); err != nil {
		t.Skipf("起 alpine+sshd 容器失败(可能镜像不可拉): %v\n%s", err, string(out))
	}
	c := &sshContainer{Name: name, Host: "127.0.0.1", PrivateKey: privPEM, dockerBin: dockerBin}
	t.Cleanup(func() { _ = exec.Command(dockerBin, "rm", "-f", name).Run() })

	// 取宿主映射端口。
	portOut, err := exec.Command(dockerBin, "port", name, "22/tcp").Output()
	if err != nil {
		c.dumpLogs(t)
		t.Fatalf("取容器映射端口失败: %v", err)
	}
	line := strings.TrimSpace(strings.Split(string(portOut), "\n")[0])
	if i := strings.LastIndex(line, ":"); i >= 0 {
		_, _ = fmt.Sscanf(line[i+1:], "%d", &c.Port)
	}
	if c.Port == 0 {
		c.dumpLogs(t)
		t.Fatalf("解析容器端口失败: %q", line)
	}

	// 轮询 SSH 真可连(apk add openssh 约需 ~10s;给足 60s)。
	c.waitReady(t, 60*time.Second)
	return c
}

// waitReady 用真 x/crypto/ssh 拨号轮询直至连通(sshd 起来 + key 生效)。
func (c *sshContainer) waitReady(t *testing.T, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	addr := fmt.Sprintf("%s:%d", c.Host, c.Port)
	var lastErr error
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		res, err := sshDialer{}.Run(ctx, addr, SSHConfig{User: "root", PrivateKey: c.PrivateKey}, []string{"true"})
		cancel()
		if err == nil && res != nil && res.ExitCode == 0 {
			return
		}
		lastErr = err
		time.Sleep(800 * time.Millisecond)
	}
	c.dumpLogs(t)
	t.Fatalf("等待容器 sshd 就绪超时(%s): %v", timeout, lastErr)
}

// Exec 在容器内直接跑命令(docker exec;用于在测试侧旁路断言文件真落地 / 预置进程,非经 SSH)。
func (c *sshContainer) Exec(t *testing.T, args ...string) (string, error) {
	t.Helper()
	full := append([]string{"exec", c.Name}, args...)
	var out bytes.Buffer
	cmd := exec.Command(c.dockerBin, full...)
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

// dumpLogs 打印容器日志(诊断起容器/连接失败用)。
func (c *sshContainer) dumpLogs(t *testing.T) {
	t.Helper()
	if out, err := exec.Command(c.dockerBin, "logs", c.Name).CombinedOutput(); err == nil {
		t.Logf("=== 容器 %s 日志 ===\n%s", c.Name, tailLines(string(out), 15))
	}
}
