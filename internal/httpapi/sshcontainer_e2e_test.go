package httpapi

// sshcontainer_e2e_test.go 为 Epic 6 运维真 e2e 提供一台**自起自清的 alpine+sshd 目标容器**
//(与 internal/target、internal/deploy 的同形 helper 等价;各包独立测试二进制故各持一份)。
//
// 默认 SKIP(PIPEWRIGHT_E2E_DEPLOY=1 启用)。容器 t.Cleanup 兜底 docker rm -f,绝不留垃圾。

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/target"
	"github.com/huangchengsir/pipewright/internal/vault"
	"golang.org/x/crypto/ssh"
)

type sshContainer struct {
	Name       string
	Host       string
	Port       int
	PrivateKey string
	dockerBin  string
}

func requireE2EDeploy(t *testing.T) {
	t.Helper()
	if os.Getenv("PIPEWRIGHT_E2E_DEPLOY") != "1" {
		t.Skip("设 PIPEWRIGHT_E2E_DEPLOY=1 启用真 Linux SSH 目标运维 e2e")
	}
}

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

func startSSHContainer(t *testing.T) *sshContainer {
	t.Helper()
	requireE2EDeploy(t)
	dockerBin := dockerBinOrSkip(t)

	privPEM, authLine := genTestSSHKey(t)
	name := fmt.Sprintf("pw-e2e-ops-%d-%d", os.Getpid(), time.Now().UnixNano()%100000)
	_ = exec.Command(dockerBin, "rm", "-f", name).Run()

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

	c.waitReady(t, 60*time.Second)
	return c
}

func (c *sshContainer) waitReady(t *testing.T, timeout time.Duration) {
	t.Helper()
	signer, err := ssh.ParsePrivateKey([]byte(c.PrivateKey))
	if err != nil {
		t.Fatalf("解析测试私钥: %v", err)
	}
	cfg := &ssh.ClientConfig{
		User:            "root",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         4 * time.Second,
	}
	addr := net.JoinHostPort(c.Host, fmt.Sprintf("%d", c.Port))
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		conn, derr := net.DialTimeout("tcp", addr, 3*time.Second)
		if derr == nil {
			cc, chans, reqs, herr := ssh.NewClientConn(conn, addr, cfg)
			if herr == nil {
				client := ssh.NewClient(cc, chans, reqs)
				_ = client.Close()
				return
			}
			_ = conn.Close()
			lastErr = herr
		} else {
			lastErr = derr
		}
		time.Sleep(700 * time.Millisecond)
	}
	c.dumpLogs(t)
	t.Fatalf("等待容器 sshd 就绪超时(%s): %v", timeout, lastErr)
}

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

// ExecDetached 在容器内后台跑命令(docker exec -d),供起写日志的常驻进程。
func (c *sshContainer) ExecDetached(t *testing.T, shellCmd string) {
	t.Helper()
	out, err := exec.Command(c.dockerBin, "exec", "-d", c.Name, "sh", "-c", shellCmd).CombinedOutput()
	if err != nil {
		t.Fatalf("容器内起后台进程失败: %v\n%s", err, string(out))
	}
}

func (c *sshContainer) dumpLogs(t *testing.T) {
	t.Helper()
	if out, err := exec.Command(c.dockerBin, "logs", c.Name).CombinedOutput(); err == nil {
		lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
		if len(lines) > 15 {
			lines = lines[len(lines)-15:]
		}
		t.Logf("=== 容器 %s 日志 ===\n%s", c.Name, strings.Join(lines, "\n"))
	}
}

// newE2ETargetService 建真 vault + 真 dialer 的 target.Service,把容器登记为目标,返回 (svc, serverID)。
func newE2ETargetService(t *testing.T, c *sshContainer) (target.Service, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "ops-e2e.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	var key [32]byte
	copy(key[:], []byte("0123456789abcdef0123456789abcdef"))
	v := vault.New(st.DB, &key)
	cred, err := v.Create(vault.CreateInput{Name: "e2e-ssh", Type: vault.TypeSSHKey, Secret: c.PrivateKey})
	if err != nil {
		t.Fatalf("vault.Create: %v", err)
	}
	svc := target.New(st.DB, v, nil)
	srv, err := svc.Create(context.Background(), target.CreateInput{
		Name: "e2e-ops-target", Host: c.Host, Port: c.Port, User: "root", CredentialID: cred.ID,
	})
	if err != nil {
		t.Fatalf("target.Create: %v", err)
	}
	return svc, srv.ID
}
