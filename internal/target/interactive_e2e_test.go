package target

// interactive_e2e_test.go 是 Story 6-4 交互终端**真 SSH 传输**的端到端集成测试。
//
// 与 interactive_test.go(fake dialer/session 单测)互补:本测试用**真 sshDialer**
// 连**真 sshd**(localhost:22)跑真 PTY 会话,验证 fake 证不到的真实栈行为 ——
// 真 x/crypto/ssh 握手、真 RequestPty、真 stdin→stdout 双向泵、真 WindowChange、
// 远端进程退出→EOF、Close 干净收尾(-race 无泄漏)。
//
// 生产路径是 `docker exec -it <container> <shell>`;此处用 `/bin/sh`(去掉 docker 那层),
// 走的是**同一条** RunInteractive→interactiveSession 传输代码 —— docker 只是 argv,
// 容器层需 Docker daemon 另行验,但 WS↔SSH PTY 传输本身在此被真实覆盖。
//
// 默认 SKIP:仅当 PIPEWRIGHT_E2E_SSH=1 且本机 sshd 可免密连(CI 无此环境)时运行。
// 跑法:PIPEWRIGHT_E2E_SSH=1 go test ./internal/target/ -run E2EInteractive -race -v

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// loadE2EKey 读取本机 SSH 私钥(优先 id_ed25519,回退 id_rsa);找不到则跳过。
func loadE2EKey(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("无法定位 HOME: %v", err)
	}
	for _, name := range []string{"id_ed25519", "id_rsa"} {
		p := filepath.Join(home, ".ssh", name)
		if b, rerr := os.ReadFile(p); rerr == nil && len(b) > 0 {
			return string(b)
		}
	}
	t.Skip("未找到 ~/.ssh/id_ed25519 或 id_rsa,跳过真 SSH e2e")
	return ""
}

// TestE2EInteractiveRealSSH 对真 localhost sshd 跑真 PTY 交互会话,验双向泵 + 退出 EOF。
func TestE2EInteractiveRealSSH(t *testing.T) {
	if os.Getenv("PIPEWRIGHT_E2E_SSH") != "1" {
		t.Skip("设 PIPEWRIGHT_E2E_SSH=1 启用真 SSH 交互终端 e2e")
	}
	key := loadE2EKey(t)
	user := os.Getenv("USER")
	if user == "" {
		t.Skip("USER 为空,跳过")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// 真 sshDialer + 真 PTY;跑 /bin/sh(等价 `docker exec -it id sh` 去掉 docker 层)。
	sess, err := sshDialer{}.RunInteractive(ctx, "localhost:22",
		SSHConfig{User: user, PrivateKey: key}, []string{"/bin/sh"})
	if err != nil {
		t.Fatalf("RunInteractive 真连 localhost:22 失败(确认远程登录已开 + 免密 key 在 authorized_keys): %v", err)
	}

	// 后台读全部输出直到 EOF(远端 sh 退出关 PTY → io.Pipe 写端关 → 读端 EOF)。
	var out bytes.Buffer
	readDone := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(&out, sess)
		readDone <- copyErr
	}()

	// 真 resize(WindowChange):不致命,验不 panic / 不报错。
	if rerr := sess.Resize(120, 40); rerr != nil {
		t.Errorf("Resize 失败: %v", rerr)
	}

	// 写入:回显一个唯一标记 → 验 stdin→远端执行→stdout 双向通路;再 exit 收尾。
	marker := "PIPEWRIGHT_E2E_MARKER_7x42q"
	if _, werr := io.WriteString(sess, "echo "+marker+"\n"); werr != nil {
		t.Fatalf("写 stdin 失败: %v", werr)
	}
	if _, werr := io.WriteString(sess, "exit\n"); werr != nil {
		t.Fatalf("写 exit 失败: %v", werr)
	}

	// 等读侧 EOF(远端进程退出后传输应自然收束,验生命周期绑定正确)。
	select {
	case copyErr := <-readDone:
		if copyErr != nil && copyErr != io.EOF {
			t.Fatalf("读输出异常: %v", copyErr)
		}
	case <-ctx.Done():
		t.Fatal("等待远端退出超时(EOF 未到,可能传输未随进程收束)")
	}

	got := out.String()
	if !strings.Contains(got, marker) {
		t.Fatalf("输出未含回显标记 → 双向 PTY 通路未打通\n--- 实际输出 ---\n%s", got)
	}
	t.Logf("真 SSH PTY 双向通路 OK;回显标记命中。输出片段:\n%s", tailLines(got, 6))

	// Close 幂等 + 不泄漏(-race 守护);二次 Close 不应 panic/报错。
	if cerr := sess.Close(); cerr != nil {
		t.Errorf("首次 Close 报错: %v", cerr)
	}
	if cerr := sess.Close(); cerr != nil {
		t.Errorf("二次 Close(幂等)报错: %v", cerr)
	}
}

// TestE2EInteractiveInjectionRealSSH 验真 SSH 上命令 array 化防注入:把 shell 元字符当**字面参数**
// 传给远端,远端绝不二次解释执行(AC-SEC-02 在真传输上的端到端确认)。
func TestE2EInteractiveInjectionRealSSH(t *testing.T) {
	if os.Getenv("PIPEWRIGHT_E2E_SSH") != "1" {
		t.Skip("设 PIPEWRIGHT_E2E_SSH=1 启用真 SSH 交互终端 e2e")
	}
	key := loadE2EKey(t)
	user := os.Getenv("USER")
	if user == "" {
		t.Skip("USER 为空,跳过")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// 注入探针用命令替换 `$(id)`:无歧义判据 ——
	//   - array 化(正确):`$(id)` 是 echo 的字面参数,原样回显字符串 "$(id)",输出绝无 "uid="。
	//   - 若被拼成 shell(漏洞):远端解释 `$(id)` → 执行 id → 输出含 "uid=...".
	probe := "INJ_CANARY_$(id)"
	sess, err := sshDialer{}.RunInteractive(ctx, "localhost:22",
		SSHConfig{User: user, PrivateKey: key}, []string{"/bin/echo", probe})
	if err != nil {
		t.Fatalf("RunInteractive 失败: %v", err)
	}

	var out bytes.Buffer
	done := make(chan struct{})
	go func() { _, _ = io.Copy(&out, sess); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("等待 echo 退出超时")
	}
	_ = sess.Close()

	got := out.String()
	if strings.Contains(got, "uid=") {
		t.Fatalf("命令注入未防住!`$(id)` 被远端 shell 解释执行 → array 转义失效\n输出:\n%s", got)
	}
	// 元字符应被当字面参数原样回显(证明绝未二次解释)。
	if !strings.Contains(got, "INJ_CANARY_$(id)") {
		t.Fatalf("未见字面回显 `$(id)`,无法确认注入防护语义\n输出:\n%s", got)
	}
	t.Logf("注入防护 OK:`$(id)` 被当字面参数原样回显、未执行(无 uid=)。回显:%s", strings.TrimSpace(got))
}

// tailLines 取文本末 n 行(日志友好,避免刷屏)。
func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return fmt.Sprintf("%s", strings.Join(lines, "\n"))
}
