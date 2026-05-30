package target

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"

	"github.com/huangchengsir/pipewright/internal/vault"
)

// fakeSession 是一个内存版 Session,供单测交互泵/resize/close/命令 array 化(不触网)。
//   - Read 从 out 缓冲读(预置远端输出),读尽后返回 EOF(除非 closed 前阻塞)。
//   - Write 把 stdin 累积到 in,供断言「输入泵」。
//   - Resize 记录最后一次窗口尺寸,供断言。
//   - Close 幂等,记录关闭次数与状态。
type fakeSession struct {
	mu        sync.Mutex
	in        bytes.Buffer // 收到的 stdin
	out       *bytes.Reader
	lastCols  int
	lastRows  int
	closed    bool
	closeN    int
	resizeErr error
}

func newFakeSession(remoteOut string) *fakeSession {
	return &fakeSession{out: bytes.NewReader([]byte(remoteOut))}
}

func (f *fakeSession) Read(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return 0, io.EOF
	}
	return f.out.Read(p)
}

func (f *fakeSession) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return 0, io.ErrClosedPipe
	}
	return f.in.Write(p)
}

func (f *fakeSession) Resize(cols, rows int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastCols = cols
	f.lastRows = rows
	return f.resizeErr
}

func (f *fakeSession) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeN++
	f.closed = true
	return nil
}

func (f *fakeSession) stdin() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.in.String()
}

// TestExecInteractiveArrayCommand 证明:命令以 array 透传给 dialer(不拼 shell),凭据装配正确,
// 且容器 ID/shell 作为独立 argv 元素传入(防注入到 docker exec 参数)。
func TestExecInteractiveArrayCommand(t *testing.T) {
	db := testDB(t)
	v := vault.New(db, testMasterKey())
	credID := newSSHCred(t, v, "secret-pw")
	d := &capturingDialer{}
	svc := New(db, v, d)

	srv, err := svc.Create(context.Background(), CreateInput{
		Name: "host", Host: "10.0.0.9", Port: 22, User: "deploy", CredentialID: credID,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// 含 shell 元字符的容器名:必须作为单个 argv 元素整体传递,绝不被拆/解释。
	evilContainer := "app; rm -rf /"
	cmd := []string{"docker", "exec", "-it", evilContainer, "/bin/sh"}
	sess, err := svc.ExecInteractive(context.Background(), srv.ID, cmd)
	if err != nil {
		t.Fatalf("ExecInteractive: %v", err)
	}
	defer func() { _ = sess.Close() }()

	if d.gotAddr != "10.0.0.9:22" {
		t.Fatalf("addr = %q, want 10.0.0.9:22", d.gotAddr)
	}
	if d.gotCfg.Password != "secret-pw" {
		t.Fatalf("password not assembled from vault: %q", d.gotCfg.Password)
	}
	if len(d.gotCmd) != 5 || d.gotCmd[3] != evilContainer || d.gotCmd[4] != "/bin/sh" {
		t.Fatalf("cmd not passed as array: %#v", d.gotCmd)
	}
}

// TestExecInteractivePumpAndResizeAndClose 证明:输入泵(Write→stdin)、resize 透传、
// Close 幂等、读出远端输出。
func TestExecInteractivePumpAndResizeAndClose(t *testing.T) {
	db := testDB(t)
	v := vault.New(db, testMasterKey())
	credID := newSSHCred(t, v, "secret-pw")
	fs := newFakeSession("hello from container\n")
	d := &capturingDialer{interactive: fs}
	svc := New(db, v, d)

	srv, err := svc.Create(context.Background(), CreateInput{
		Name: "host", Host: "h", Port: 22, User: "u", CredentialID: credID,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	sess, err := svc.ExecInteractive(context.Background(), srv.ID, []string{"docker", "exec", "-it", "c1", "/bin/sh"})
	if err != nil {
		t.Fatalf("ExecInteractive: %v", err)
	}

	// 输入泵。
	if _, err := sess.Write([]byte("ls -la\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := fs.stdin(); got != "ls -la\n" {
		t.Fatalf("stdin = %q, want ls -la", got)
	}

	// resize 透传。
	if err := sess.Resize(120, 40); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	if fs.lastCols != 120 || fs.lastRows != 40 {
		t.Fatalf("resize = %dx%d, want 120x40", fs.lastCols, fs.lastRows)
	}

	// 读出远端输出。
	buf := make([]byte, 64)
	n, _ := sess.Read(buf)
	if string(buf[:n]) != "hello from container\n" {
		t.Fatalf("read = %q", string(buf[:n]))
	}

	// Close 幂等。
	_ = sess.Close()
	_ = sess.Close()
	if fs.closeN < 1 {
		t.Fatalf("Close not called")
	}
}

// TestExecInteractiveCredentialErrors 证明:vault 未配 / 凭据缺失 / 服务器不存在 映射为干净领域错误,
// 且错误体绝不含凭据明文。
func TestExecInteractiveErrors(t *testing.T) {
	db := testDB(t)
	v := vault.New(db, testMasterKey())
	credID := newSSHCred(t, v, "topsecret-pw")

	// 服务器不存在。
	svc := New(db, v, &capturingDialer{})
	if _, err := svc.ExecInteractive(context.Background(), "nope", []string{"docker"}); err != ErrNotFound {
		t.Fatalf("missing server: got %v want ErrNotFound", err)
	}

	// 空命令。
	srv, _ := svc.Create(context.Background(), CreateInput{Name: "h", Host: "h", Port: 22, User: "u", CredentialID: credID})
	if _, err := svc.ExecInteractive(context.Background(), srv.ID, nil); err == nil {
		t.Fatalf("empty cmd should error")
	}

	// vault 未配置(nil vault)。
	svcNoVault := New(db, nil, &capturingDialer{})
	srv2, _ := svcNoVault.Create(context.Background(), CreateInput{Name: "h2", Host: "h", Port: 22, User: "u", CredentialID: credID})
	if _, err := svcNoVault.ExecInteractive(context.Background(), srv2.ID, []string{"docker", "exec"}); err != ErrVaultUnconfigured {
		t.Fatalf("nil vault: got %v want ErrVaultUnconfigured", err)
	}

	// dialer 返回连接错误,错误体不含明文。
	dErr := &capturingDialer{interactiveErr: ErrUnreachable}
	svcErr := New(db, v, dErr)
	srv3, _ := svcErr.Create(context.Background(), CreateInput{Name: "h3", Host: "h", Port: 22, User: "u", CredentialID: credID})
	_, err := svcErr.ExecInteractive(context.Background(), srv3.ID, []string{"docker", "exec", "-it", "c", "/bin/sh"})
	if err != ErrUnreachable {
		t.Fatalf("dialer err: got %v want ErrUnreachable", err)
	}
}
