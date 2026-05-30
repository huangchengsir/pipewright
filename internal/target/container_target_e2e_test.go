package target

// container_target_e2e_test.go 验「目标服务器领域层 + 通用 SSH 执行基座」对一台**真 Linux
// SSH 容器**(alpine+sshd,startSSHContainer)端到端跑通 —— Epic 4 部署 / Epic 6 运维都构建在
// 这条 Exec/Test/ExecStream 链路之上。fake dialer 单测证不到真握手/真凭据经 vault 取用/真远端执行。
//
// 默认 SKIP(PIPEWRIGHT_E2E_DEPLOY=1 启用)。跑法:
//
//	PIPEWRIGHT_E2E_DEPLOY=1 go test ./internal/target/ -run E2EContainerTarget -v

import (
	"context"
	"database/sql"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/vault"
)

// newE2EService 建一个真 vault + 真 dialer 的 target.Service,把容器登记为目标服务器,返回 (svc, serverID)。
func newE2EService(t *testing.T, c *sshContainer) (Service, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "e2e.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return newE2EServiceWithDB(t, st.DB, c)
}

// newE2EServiceWithDB 同上但用给定 *sql.DB(供需共用一个 DB 的用例)。
func newE2EServiceWithDB(t *testing.T, db *sql.DB, c *sshContainer) (Service, string) {
	t.Helper()
	var key [32]byte
	copy(key[:], []byte("0123456789abcdef0123456789abcdef"))
	v := vault.New(db, &key)
	cred, err := v.Create(vault.CreateInput{Name: "e2e-ssh", Type: vault.TypeSSHKey, Secret: c.PrivateKey})
	if err != nil {
		t.Fatalf("vault.Create: %v", err)
	}
	svc := New(db, v, nil) // nil dialer → 真 x/crypto/ssh
	srv, err := svc.Create(context.Background(), CreateInput{
		Name: "e2e-target", Host: c.Host, Port: c.Port, User: "root", CredentialID: cred.ID,
	})
	if err != nil {
		t.Fatalf("target.Create: %v", err)
	}
	return svc, srv.ID
}

// TestE2EContainerTargetExec 验真容器目标:Test 连通、Exec 真执行、退出码透传、ExecStream 真流式。
func TestE2EContainerTargetExec(t *testing.T) {
	c := startSSHContainer(t)
	svc, id := newE2EService(t, c)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Test():真 SSH 连通 + uname 探测。
	tr, err := svc.Test(ctx, id)
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if !tr.OK {
		t.Fatalf("Test 应连通,got ok=false err=%q", tr.Err)
	}
	if !strings.Contains(tr.Output, "Linux") {
		t.Fatalf("uname 输出应含 Linux(真 Linux 目标),got %q", tr.Output)
	}

	// Exec:真执行 echo,stdout 透传。
	res, err := svc.Exec(ctx, id, []string{"echo", "hello-e2e"})
	if err != nil {
		t.Fatalf("Exec echo: %v", err)
	}
	if res.ExitCode != 0 || !strings.Contains(res.Stdout, "hello-e2e") {
		t.Fatalf("Exec echo 异常: exit=%d stdout=%q", res.ExitCode, res.Stdout)
	}

	// 退出码真透传(false → 1)。
	res2, err := svc.Exec(ctx, id, []string{"false"})
	if err != nil {
		t.Fatalf("Exec false: %v", err)
	}
	if res2.ExitCode == 0 {
		t.Fatalf("false 应非零退出,got %d", res2.ExitCode)
	}

	// ExecStream:真流式读 stdout。
	rc, err := svc.ExecStream(ctx, id, []string{"printf", "L1\\nL2\\n"})
	if err != nil {
		t.Fatalf("ExecStream: %v", err)
	}
	b, _ := io.ReadAll(rc)
	_ = rc.Close()
	if !strings.Contains(string(b), "L1") || !strings.Contains(string(b), "L2") {
		t.Fatalf("ExecStream 输出异常: %q", string(b))
	}
	t.Logf("真容器目标 Exec/Test/ExecStream OK;uname=%s", strings.TrimSpace(tr.Output))
}
