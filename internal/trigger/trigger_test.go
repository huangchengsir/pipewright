package trigger

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/huangjiawei/devopstool/internal/store"
	"github.com/huangjiawei/devopstool/internal/vault"
)

// testMasterKey 返回确定性测试用 master key。
func testMasterKey() *[32]byte {
	var k [32]byte
	for i := range k {
		k[i] = byte(i + 7)
	}
	return &k
}

// testDB 打开临时 SQLite(含全部迁移),返回 *sql.DB 与库文件路径(供整库 dump)。
func testDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st.DB, dbPath
}

// seedProject 直接插一个项目(本测试不关心仓库校验),返回 project id。
// 先建一个最小凭据满足外键。
func seedProject(t *testing.T, db *sql.DB) string {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	credID := uuid.NewString()
	_, err := db.Exec(
		`INSERT INTO credentials (id, name, type, scope, ciphertext, masked_value, created_at, updated_at)
		 VALUES (?, 'c', 'git_token', '', X'00', 'm', ?, ?)`,
		credID, now, now,
	)
	if err != nil {
		t.Fatalf("seed credential: %v", err)
	}
	projID := uuid.NewString()
	_, err = db.Exec(
		`INSERT INTO projects (id, name, repo_url, default_branch, credential_id, created_at, updated_at)
		 VALUES (?, 'p', 'https://example.com/p.git', 'main', ?, ?, ?)`,
		projID, credID, now, now,
	)
	if err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return projID
}

func newSvc(t *testing.T) (Service, *sql.DB, string, string) {
	t.Helper()
	db, dbPath := testDB(t)
	v := vault.New(db, testMasterKey())
	svc := New(db, v)
	projID := seedProject(t, db)
	return svc, db, dbPath, projID
}

func TestGetLazyDefault(t *testing.T) {
	svc, _, _, projID := newSvc(t)
	cfg, err := svc.Get(context.Background(), projID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if cfg.WebhookToken == "" {
		t.Fatal("默认应生成 webhook token")
	}
	if cfg.Events.Push || cfg.Events.Tag || cfg.Events.PullRequest {
		t.Fatalf("默认 events 应全 false, got %+v", cfg.Events)
	}
	if len(cfg.BranchMappings) != 0 {
		t.Fatalf("默认映射应为空, got %d", len(cfg.BranchMappings))
	}
	if cfg.UnmatchedPolicy != PolicyRecord {
		t.Fatalf("默认策略应为 record, got %s", cfg.UnmatchedPolicy)
	}
	if !strings.HasPrefix(cfg.WebhookSecretMasked, secretPrefix) || !strings.Contains(cfg.WebhookSecretMasked, "••••") {
		t.Fatalf("密钥应掩码, got %q", cfg.WebhookSecretMasked)
	}
	if strings.Contains(cfg.WebhookSecretMasked, "whsec_"+strings.Repeat("0", 10)) {
		t.Fatal("掩码不应含完整密钥体")
	}

	// 二次 Get 幂等:不应重新生成 token。
	cfg2, err := svc.Get(context.Background(), projID)
	if err != nil {
		t.Fatalf("Get 2: %v", err)
	}
	if cfg2.WebhookToken != cfg.WebhookToken {
		t.Fatalf("重复 Get 不应换 token: %s != %s", cfg2.WebhookToken, cfg.WebhookToken)
	}
}

func TestGetProjectNotFound(t *testing.T) {
	svc, _, _, _ := newSvc(t)
	_, err := svc.Get(context.Background(), uuid.NewString())
	if err != ErrProjectNotFound {
		t.Fatalf("err = %v, want ErrProjectNotFound", err)
	}
}

func TestSaveRoundTrip(t *testing.T) {
	svc, _, _, projID := newSvc(t)
	in := SaveInput{
		Events: Events{Push: true, Tag: false, PullRequest: true},
		BranchMappings: []BranchMapping{
			{BranchPattern: "main", Environment: "生产", TargetServerIDs: []string{"srv-1", "srv-2"}},
			{BranchPattern: "release/*", Environment: "预发"},
		},
		UnmatchedPolicy: PolicyIgnore,
	}
	saved, err := svc.Save(context.Background(), projID, in)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if !saved.Events.Push || saved.Events.Tag || !saved.Events.PullRequest {
		t.Fatalf("events 未持久化正确: %+v", saved.Events)
	}
	if saved.UnmatchedPolicy != PolicyIgnore {
		t.Fatalf("policy = %s, want ignore", saved.UnmatchedPolicy)
	}
	if len(saved.BranchMappings) != 2 {
		t.Fatalf("映射数 = %d, want 2", len(saved.BranchMappings))
	}
	for _, m := range saved.BranchMappings {
		if m.ID == "" {
			t.Fatal("映射应补全 id")
		}
		if m.TargetServerIDs == nil {
			t.Fatal("targetServerIds 应非 nil")
		}
	}

	// 回读验证持久化。
	got, err := svc.Get(context.Background(), projID)
	if err != nil {
		t.Fatalf("Get after save: %v", err)
	}
	if len(got.BranchMappings) != 2 || got.BranchMappings[0].Environment != "生产" {
		t.Fatalf("回读映射不符: %+v", got.BranchMappings)
	}
}

func TestSaveInvalidBranchPattern(t *testing.T) {
	svc, _, _, projID := newSvc(t)
	cases := []string{"", "  ", "feature branch", "*", "**"}
	for _, p := range cases {
		_, err := svc.Save(context.Background(), projID, SaveInput{
			BranchMappings:  []BranchMapping{{BranchPattern: p, Environment: "e"}},
			UnmatchedPolicy: PolicyRecord,
		})
		if err != ErrInvalidBranchPattern {
			t.Fatalf("pattern %q: err = %v, want ErrInvalidBranchPattern", p, err)
		}
	}
}

func TestSaveValidWildcards(t *testing.T) {
	svc, _, _, projID := newSvc(t)
	for _, p := range []string{"main", "dev", "release/*", "feature/*-hotfix", "v*.*"} {
		_, err := svc.Save(context.Background(), projID, SaveInput{
			BranchMappings:  []BranchMapping{{BranchPattern: p, Environment: "e"}},
			UnmatchedPolicy: PolicyRecord,
		})
		if err != nil {
			t.Fatalf("pattern %q should be valid, got %v", p, err)
		}
	}
}

func TestSaveInvalidPolicy(t *testing.T) {
	svc, _, _, projID := newSvc(t)
	_, err := svc.Save(context.Background(), projID, SaveInput{UnmatchedPolicy: "deploy"})
	if err != ErrInvalidPolicy {
		t.Fatalf("err = %v, want ErrInvalidPolicy", err)
	}
}

func TestResetSecretInvalidatesOld(t *testing.T) {
	svc, db, _, projID := newSvc(t)

	// 触发惰性默认,拿到首版密文。
	if _, err := svc.Get(context.Background(), projID); err != nil {
		t.Fatalf("Get: %v", err)
	}
	oldCipher := readCipher(t, db, projID)

	res, err := svc.ResetSecret(context.Background(), projID)
	if err != nil {
		t.Fatalf("ResetSecret: %v", err)
	}
	if !strings.HasPrefix(res.Secret, secretPrefix) {
		t.Fatalf("回显密钥应带前缀: %q", res.Secret)
	}
	if !strings.Contains(res.Masked, "••••") {
		t.Fatalf("应同时给掩码: %q", res.Masked)
	}

	newCipher := readCipher(t, db, projID)
	if string(oldCipher) == string(newCipher) {
		t.Fatal("重置后密文应改变(旧值失效)")
	}

	// 新密文应能解密回显的明文。
	v := vault.New(db, testMasterKey())
	plain, err := v.OpenSecret(newCipher)
	if err != nil {
		t.Fatalf("OpenSecret: %v", err)
	}
	if string(plain) != res.Secret {
		t.Fatalf("新密文解密 != 回显明文")
	}
	// 旧密文解出的明文与回显明文不同(旧值确已失效)。
	oldPlain, err := v.OpenSecret(oldCipher)
	if err != nil {
		t.Fatalf("OpenSecret old: %v", err)
	}
	if string(oldPlain) == res.Secret {
		t.Fatal("旧密钥应已失效")
	}
}

// TestSecAfterDBNoPlaintext 断言:整库二进制内容不含任何 webhook 密钥明文(AC-SEC)。
func TestSecAfterDBNoPlaintext(t *testing.T) {
	svc, db, dbPath, projID := newSvc(t)

	// 默认密钥的明文(经 Get 惰性生成),再 reset 一次确保覆盖两版。
	if _, err := svc.Get(context.Background(), projID); err != nil {
		t.Fatalf("Get: %v", err)
	}
	res, err := svc.ResetSecret(context.Background(), projID)
	if err != nil {
		t.Fatalf("ResetSecret: %v", err)
	}
	// 检出点 + WAL 全部落盘后再 dump:用 wal_checkpoint 强制写回主库文件。
	_, _ = db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)

	for _, suffix := range []string{"", "-wal", "-shm"} {
		raw := readFileMaybe(t, dbPath+suffix)
		if strings.Contains(string(raw), res.Secret) {
			t.Fatalf("整库文件 %s 含 webhook 密钥明文!", dbPath+suffix)
		}
		// 密钥体(去前缀)也不应出现。
		body := strings.TrimPrefix(res.Secret, secretPrefix)
		if strings.Contains(string(raw), body) {
			t.Fatalf("整库文件 %s 含密钥体明文!", dbPath+suffix)
		}
	}
}

func TestResetVaultUnconfigured(t *testing.T) {
	db, _ := testDB(t)
	projID := seedProject(t, db)
	// vault 未配置 master key。
	svc := New(db, vault.New(db, nil))
	if _, err := svc.Get(context.Background(), projID); err != ErrVaultUnconfigured {
		t.Fatalf("Get err = %v, want ErrVaultUnconfigured", err)
	}
	if _, err := svc.ResetSecret(context.Background(), projID); err != ErrVaultUnconfigured {
		t.Fatalf("Reset err = %v, want ErrVaultUnconfigured", err)
	}
}

func TestDeleteProjectCascadesTrigger(t *testing.T) {
	svc, db, _, projID := newSvc(t)
	if _, err := svc.Get(context.Background(), projID); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM projects WHERE id = ?`, projID); err != nil {
		t.Fatalf("delete project: %v", err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(1) FROM pipeline_triggers WHERE project_id = ?`, projID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("删项目应级联删触发配置, 残留 %d 行", n)
	}
}

// readFileMaybe 读取文件;不存在返回 nil(WAL/SHM 可能尚未生成)。
func readFileMaybe(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

func readCipher(t *testing.T, db *sql.DB, projID string) []byte {
	t.Helper()
	var c []byte
	if err := db.QueryRow(
		`SELECT webhook_secret_ciphertext FROM pipeline_triggers WHERE project_id = ?`, projID,
	).Scan(&c); err != nil {
		t.Fatalf("read cipher: %v", err)
	}
	return c
}
