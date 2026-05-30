package target

import (
	"context"
	"database/sql"
	"io"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/huangjiawei/devopstool/internal/store"
	"github.com/huangjiawei/devopstool/internal/vault"
)

// 泄漏标记:任何错误/响应中出现它都说明私钥/口令明文外泄。
const leakMarker = "LEAKMARKER_SECRET_DO_NOT_EXPOSE"

func testMasterKey() *[32]byte {
	var k [32]byte
	for i := range k {
		k[i] = byte(i + 23)
	}
	return &k
}

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st.DB
}

// newSSHCred 在 vault 建一条 ssh_key 凭据,返回 id。
func newSSHCred(t *testing.T, v vault.Vault, secret string) string {
	t.Helper()
	c, err := v.Create(vault.CreateInput{Name: "ssh key", Type: vault.TypeSSHKey, Secret: secret})
	if err != nil {
		t.Fatalf("vault.Create: %v", err)
	}
	return c.ID
}

// capturingDialer 记录收到的 addr/cfg/cmd,并返回可编程结果/错误。
type capturingDialer struct {
	gotAddr   string
	gotCfg    SSHConfig
	gotCmd    []string
	res       *ExecResult
	err       error
	streamOut string // RunStream 返回的流内容
	streamErr error  // RunStream 返回的错误
}

func (d *capturingDialer) Run(_ context.Context, addr string, cfg SSHConfig, cmd []string) (*ExecResult, error) {
	d.gotAddr = addr
	d.gotCfg = cfg
	d.gotCmd = append([]string(nil), cmd...)
	return d.res, d.err
}

func (d *capturingDialer) RunStream(_ context.Context, addr string, cfg SSHConfig, cmd []string) (io.ReadCloser, error) {
	d.gotAddr = addr
	d.gotCfg = cfg
	d.gotCmd = append([]string(nil), cmd...)
	if d.streamErr != nil {
		return nil, d.streamErr
	}
	return io.NopCloser(strings.NewReader(d.streamOut)), nil
}

func TestCRUD(t *testing.T) {
	db := testDB(t)
	v := vault.New(db, testMasterKey())
	credID := newSSHCred(t, v, "secret-pw")
	svc := New(db, v, &capturingDialer{})

	srv, err := svc.Create(context.Background(), CreateInput{
		Name: "web-prod-1", Host: "10.0.0.5", Port: 0, User: "deploy", CredentialID: credID,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if srv.Port != DefaultPort {
		t.Fatalf("port = %d, want default %d", srv.Port, DefaultPort)
	}
	if srv.CredentialName != "ssh key" {
		t.Fatalf("credentialName = %q, want join 出 ssh key", srv.CredentialName)
	}

	got, err := svc.Get(context.Background(), srv.ID)
	if err != nil || got.Host != "10.0.0.5" {
		t.Fatalf("Get: %v / host=%q", err, got.Host)
	}

	list, err := svc.List(context.Background())
	if err != nil || len(list) != 1 {
		t.Fatalf("List: %v / len=%d", err, len(list))
	}

	newName := "web-prod-renamed"
	newPort := 2222
	upd, err := svc.Update(context.Background(), srv.ID, UpdateInput{Name: &newName, Port: &newPort})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if upd.Name != newName || upd.Port != newPort {
		t.Fatalf("update mismatch: name=%q port=%d", upd.Name, upd.Port)
	}

	if err := svc.Delete(context.Background(), srv.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := svc.Get(context.Background(), srv.ID); err != ErrNotFound {
		t.Fatalf("Get after delete = %v, want ErrNotFound", err)
	}
}

func TestCreateValidation(t *testing.T) {
	db := testDB(t)
	v := vault.New(db, testMasterKey())
	svc := New(db, v, &capturingDialer{})
	cases := []struct {
		name string
		in   CreateInput
		want error
	}{
		{"no name", CreateInput{Host: "h", User: "u", CredentialID: "c"}, ErrEmptyName},
		{"no host", CreateInput{Name: "n", User: "u", CredentialID: "c"}, ErrEmptyHost},
		{"no user", CreateInput{Name: "n", Host: "h", CredentialID: "c"}, ErrEmptyUser},
		{"no cred", CreateInput{Name: "n", Host: "h", User: "u"}, ErrEmptyCredentialID},
		{"bad port", CreateInput{Name: "n", Host: "h", User: "u", CredentialID: "c", Port: 70000}, ErrInvalidPort},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := svc.Create(context.Background(), tc.in); err != tc.want {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestExecCmdArrayNotShellInjected 验证 AC-SEC-02:cmd 以 array 原样传到 dialer,
// 注入字符不被本层拼成 shell 串(本层不拼接;真正转义在 ssh.go shellQuote,见单测)。
func TestExecCmdArrayNotShellInjected(t *testing.T) {
	db := testDB(t)
	v := vault.New(db, testMasterKey())
	credID := newSSHCred(t, v, "-----BEGIN OPENSSH PRIVATE KEY-----\nfake\n-----END OPENSSH PRIVATE KEY-----")
	dialer := &capturingDialer{res: &ExecResult{Stdout: "ok", ExitCode: 0}}
	svc := New(db, v, dialer)

	srv, err := svc.Create(context.Background(), CreateInput{
		Name: "n", Host: "h", User: "u", CredentialID: credID,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	injection := []string{"echo", "hello; rm -rf /", "$(whoami)", "`id`"}
	if _, err := svc.Exec(context.Background(), srv.ID, injection); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if len(dialer.gotCmd) != len(injection) {
		t.Fatalf("dialer 收到 cmd 元素数 = %d, want %d(array 必须原样、未被拼成 shell 串)", len(dialer.gotCmd), len(injection))
	}
	for i := range injection {
		if dialer.gotCmd[i] != injection[i] {
			t.Fatalf("cmd[%d] = %q, want %q(array 元素被改写,可能拼了 shell)", i, dialer.gotCmd[i], injection[i])
		}
	}
	// 私钥型凭据 → 走 PrivateKey 分支。
	if dialer.gotCfg.PrivateKey == "" || dialer.gotCfg.Password != "" {
		t.Fatalf("PEM 凭据应走私钥认证")
	}
}

// TestShellQuoteBlocksInjection 验证 ssh.go 的 array→shell 转义把注入字符当字面量。
func TestShellQuoteBlocksInjection(t *testing.T) {
	got := quoteArgs([]string{"echo", "a; rm -rf /", "$(id)", "`whoami`", "x'y"})
	// 危险元字符(分隔符、命令替换、反引号)必被单引号包住,不在引号外裸露,
	// 故远端 shell 把它们当字面量、绝不解释执行。
	if !strings.Contains(got, `'a; rm -rf /'`) {
		t.Fatalf("注入参数未被单引号转义: %q", got)
	}
	if !strings.Contains(got, `'$(id)'`) {
		t.Fatalf("命令替换未被转义: %q", got)
	}
	if !strings.Contains(got, "'x'\\''y'") {
		t.Fatalf("内部单引号未被正确转义: %q", got)
	}
}

// TestExecErrorNeverLeaksSecret 验证认证失败错误体绝不含凭据明文。
func TestExecErrorNeverLeaksSecret(t *testing.T) {
	db := testDB(t)
	v := vault.New(db, testMasterKey())
	credID := newSSHCred(t, v, leakMarker)
	dialer := &capturingDialer{err: ErrAuth}
	svc := New(db, v, dialer)

	srv, err := svc.Create(context.Background(), CreateInput{
		Name: "n", Host: "h", User: "u", CredentialID: credID,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, execErr := svc.Exec(context.Background(), srv.ID, []string{"uname", "-a"})
	if execErr == nil {
		t.Fatalf("Exec 应返回认证错误")
	}
	if strings.Contains(execErr.Error(), leakMarker) {
		t.Fatalf("Exec 错误泄漏凭据明文: %v", execErr)
	}

	// Test 也不得泄漏:失败映射为 TestResult.err(人读),无明文。
	res, terr := svc.Test(context.Background(), srv.ID)
	if terr != nil {
		t.Fatalf("Test 不应上抛连接类错误: %v", terr)
	}
	if res.OK {
		t.Fatalf("Test.ok 应为 false")
	}
	if strings.Contains(res.Err, leakMarker) {
		t.Fatalf("Test 错误泄漏凭据明文: %q", res.Err)
	}
}

// TestExecVaultUnconfigured 验证未配 master key → 干净错误(不 panic、不泄漏)。
func TestExecVaultUnconfigured(t *testing.T) {
	db := testDB(t)
	// nil key ⇒ 未配置态。
	v := vault.New(db, nil)
	// 直接插一行服务器(绕过 vault),引用任意 credential_id。
	mustInsertServer(t, db, "srv1", "missingcred")
	svc := New(db, v, &capturingDialer{})

	if _, err := svc.Exec(context.Background(), "srv1", []string{"uname"}); err != ErrVaultUnconfigured {
		t.Fatalf("Exec = %v, want ErrVaultUnconfigured", err)
	}
}

// mustInsertServer 直接插服务器行(测试用;credential_id 可指向不存在凭据需先关外键)。
func mustInsertServer(t *testing.T, db *sql.DB, id, credID string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	// 关外键以便插引用不存在凭据的行(仅测试)。
	_, _ = db.Exec(`PRAGMA foreign_keys = OFF`)
	if _, err := db.Exec(
		`INSERT INTO servers (id, name, host, port, user, credential_id, created_at, updated_at)
		 VALUES (?, 'n', 'h', 22, 'u', ?, ?, ?)`, id, credID, now, now,
	); err != nil {
		t.Fatalf("insert server: %v", err)
	}
	_, _ = db.Exec(`PRAGMA foreign_keys = ON`)
}

// ─── 本机真连(localhost:22):本机 sshd 在则真测,否则 t.Skip ─────────────────────

// localSSHAvailable 探测本机 22 端口是否可连(macOS 远程登录)。
func localSSHAvailable() bool {
	conn, err := net.DialTimeout("tcp", "127.0.0.1:22", 800*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// TestRealLocalhostSSH 用本机 ~/.ssh 私钥对 localhost:22 真连跑 uname -a。
// 需要本机 sshd 开 + 当前用户配好 authorized_keys;任一前置缺失则 Skip(不算失败)。
func TestRealLocalhostSSH(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode:跳过真连")
	}
	if !localSSHAvailable() {
		t.Skip("本机 22 端口不可连(sshd 未开):跳过真连")
	}
	keyPEM, ok := loadLocalPrivateKey()
	if !ok {
		t.Skip("未找到本机 ~/.ssh 私钥:跳过真连")
	}
	cur, err := user.Current()
	if err != nil {
		t.Skip("无法取当前用户:跳过真连")
	}

	db := testDB(t)
	v := vault.New(db, testMasterKey())
	credID := newSSHCred(t, v, keyPEM)
	svc := New(db, v, nil) // 真实 x/crypto/ssh dialer

	srv, err := svc.Create(context.Background(), CreateInput{
		Name: "localhost", Host: "127.0.0.1", Port: 22, User: cur.Username, CredentialID: credID,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	res, err := svc.Test(ctx, srv.ID)
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if !res.OK {
		t.Skipf("本机 SSH 认证未通过(可能 authorized_keys 未配/key 有口令):%s", res.Err)
	}
	if !strings.Contains(res.Output, "Darwin") && !strings.Contains(res.Output, "Linux") {
		t.Fatalf("uname 输出异常: %q", res.Output)
	}
	t.Logf("真连成功 latency=%dms output=%q", res.LatencyMs, strings.TrimSpace(res.Output))

	// Exec 通用层也真跑一条 array 命令。
	ex, err := svc.Exec(ctx, srv.ID, []string{"echo", "hi from exec"})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if !strings.Contains(ex.Stdout, "hi from exec") || ex.ExitCode != 0 {
		t.Fatalf("Exec 输出异常: %+v", ex)
	}
}

// loadLocalPrivateKey 尝试读本机常见私钥文件(无口令的);返回 PEM 文本。
func loadLocalPrivateKey() (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	for _, name := range []string{"id_ed25519", "id_rsa", "id_ecdsa"} {
		b, err := os.ReadFile(filepath.Join(home, ".ssh", name))
		if err == nil && len(b) > 0 {
			return string(b), true
		}
	}
	return "", false
}

// TestExecStream 验证流式执行返回 stdout 流、命令 array 原样传递、PEM 走私钥认证。
func TestExecStream(t *testing.T) {
	db := testDB(t)
	v := vault.New(db, testMasterKey())
	credID := newSSHCred(t, v, "-----BEGIN OPENSSH PRIVATE KEY-----\nfake\n-----END OPENSSH PRIVATE KEY-----")
	dialer := &capturingDialer{streamOut: "a\nb\nc\n"}
	svc := New(db, v, dialer)

	srv, err := svc.Create(context.Background(), CreateInput{Name: "n", Host: "h", User: "u", CredentialID: credID})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	cmd := []string{"tail", "-n", "10", "-f", "/var/log/a.log"}
	rc, err := svc.ExecStream(context.Background(), srv.ID, cmd)
	if err != nil {
		t.Fatalf("ExecStream: %v", err)
	}
	got, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(got) != "a\nb\nc\n" {
		t.Fatalf("stream = %q, want a\\nb\\nc\\n", got)
	}
	if len(dialer.gotCmd) != len(cmd) {
		t.Fatalf("cmd array 未原样传递: %v", dialer.gotCmd)
	}
	if dialer.gotCfg.PrivateKey == "" {
		t.Fatalf("PEM 凭据应走私钥认证")
	}
}

// TestExecStreamErrorNoLeak 验证流式失败错误体不含凭据明文。
func TestExecStreamErrorNoLeak(t *testing.T) {
	db := testDB(t)
	v := vault.New(db, testMasterKey())
	credID := newSSHCred(t, v, leakMarker)
	dialer := &capturingDialer{streamErr: ErrUnreachable}
	svc := New(db, v, dialer)

	srv, err := svc.Create(context.Background(), CreateInput{Name: "n", Host: "h", User: "u", CredentialID: credID})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err = svc.ExecStream(context.Background(), srv.ID, []string{"tail", "-f", "/x"})
	if err == nil {
		t.Fatalf("want error")
	}
	if strings.Contains(err.Error(), leakMarker) {
		t.Fatalf("错误体泄漏凭据明文: %v", err)
	}
}

// TestExecStreamEmptyCmd 验证空命令被拒。
func TestExecStreamEmptyCmd(t *testing.T) {
	db := testDB(t)
	v := vault.New(db, testMasterKey())
	credID := newSSHCred(t, v, "pw")
	svc := New(db, v, &capturingDialer{})
	srv, _ := svc.Create(context.Background(), CreateInput{Name: "n", Host: "h", User: "u", CredentialID: credID})
	if _, err := svc.ExecStream(context.Background(), srv.ID, nil); err == nil {
		t.Fatalf("空命令应被拒")
	}
}
