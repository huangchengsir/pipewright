package project

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/huangjiawei/devopstool/internal/store"
	"github.com/huangjiawei/devopstool/internal/vault"
)

// testMasterKey 返回确定性测试用 master key。
func testMasterKey() *[32]byte {
	var k [32]byte
	for i := range k {
		k[i] = byte(i + 11)
	}
	return &k
}

// testDB 打开临时 SQLite(含全部迁移),返回 *sql.DB。
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st.DB
}

// stubProber 是可编程的远端探测器(避免测试触网)。
type stubProber struct {
	branch string
	err    error
	calls  int
	lastTo string // 最近一次的 token(用于断言不为空 / 进程内取用)
}

func (s *stubProber) Probe(_ context.Context, _ /*repoURL*/ string, token string) (string, error) {
	s.calls++
	s.lastTo = token
	return s.branch, s.err
}

// newCred 在 vault 建一个 git_token 凭据,返回 id。
func newCred(t *testing.T, v vault.Vault, secret string) string {
	t.Helper()
	c, err := v.Create(vault.CreateInput{Name: "ci token", Type: vault.TypeGitToken, Secret: secret})
	if err != nil {
		t.Fatalf("vault.Create: %v", err)
	}
	return c.ID
}

func TestCreateAndList(t *testing.T) {
	db := testDB(t)
	v := vault.New(db, testMasterKey())
	pr := &stubProber{branch: "develop"}
	svc := New(db, v, pr)
	credID := newCred(t, v, "ghp_secrettoken123")

	p, err := svc.Create(context.Background(), CreateInput{
		Name:         "shop",
		RepoURL:      "https://gitee.com/acme/shop.git",
		CredentialID: credID,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if p.DefaultBranch != "develop" {
		t.Fatalf("defaultBranch = %q, want探测到的 develop", p.DefaultBranch)
	}
	if p.CredentialName != "ci token" {
		t.Fatalf("credentialName = %q, want join 出 ci token", p.CredentialName)
	}
	if pr.lastTo != "ghp_secrettoken123" {
		t.Fatalf("prober 未收到 vault 取出的明文 token")
	}

	list, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].ID != p.ID {
		t.Fatalf("List 结果异常: %+v", list)
	}
}

func TestCreateExplicitDefaultBranchWins(t *testing.T) {
	db := testDB(t)
	v := vault.New(db, testMasterKey())
	svc := New(db, v, &stubProber{branch: "main"})
	credID := newCred(t, v, "tok")

	p, err := svc.Create(context.Background(), CreateInput{
		Name: "x", RepoURL: "https://gitee.com/a/b.git", CredentialID: credID, DefaultBranch: "release",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if p.DefaultBranch != "release" {
		t.Fatalf("显式 defaultBranch 应优先, got %q", p.DefaultBranch)
	}
}

func TestCreateMissingCredential(t *testing.T) {
	db := testDB(t)
	v := vault.New(db, testMasterKey())
	svc := New(db, v, &stubProber{branch: "main"})

	_, err := svc.Create(context.Background(), CreateInput{
		Name: "x", RepoURL: "https://gitee.com/a/b.git", CredentialID: "no-such-id",
	})
	if !errors.Is(err, ErrCredentialNotFound) {
		t.Fatalf("err = %v, want ErrCredentialNotFound", err)
	}
}

func TestCreateVaultUnconfigured(t *testing.T) {
	db := testDB(t)
	v := vault.New(db, nil) // 未配置 master key
	svc := New(db, v, &stubProber{branch: "main"})

	_, err := svc.Create(context.Background(), CreateInput{
		Name: "x", RepoURL: "https://gitee.com/a/b.git", CredentialID: "any",
	})
	if !errors.Is(err, ErrVaultUnconfigured) {
		t.Fatalf("err = %v, want ErrVaultUnconfigured", err)
	}
}

func TestCreateCredentialError(t *testing.T) {
	db := testDB(t)
	v := vault.New(db, testMasterKey())
	svc := New(db, v, &stubProber{err: ErrCredentialError})
	credID := newCred(t, v, "badtoken")

	_, err := svc.Create(context.Background(), CreateInput{
		Name: "x", RepoURL: "https://gitee.com/a/b.git", CredentialID: credID,
	})
	if !errors.Is(err, ErrCredentialError) {
		t.Fatalf("err = %v, want ErrCredentialError", err)
	}
}

func TestCreateRepoUnreachable(t *testing.T) {
	db := testDB(t)
	v := vault.New(db, testMasterKey())
	svc := New(db, v, &stubProber{err: ErrRepoUnreachable})
	credID := newCred(t, v, "tok")

	_, err := svc.Create(context.Background(), CreateInput{
		Name: "x", RepoURL: "https://bad.invalid/a/b.git", CredentialID: credID,
	})
	if !errors.Is(err, ErrRepoUnreachable) {
		t.Fatalf("err = %v, want ErrRepoUnreachable", err)
	}
}

func TestValidation(t *testing.T) {
	db := testDB(t)
	v := vault.New(db, testMasterKey())
	svc := New(db, v, &stubProber{})
	ctx := context.Background()

	cases := []struct {
		name string
		in   CreateInput
		want error
	}{
		{"empty name", CreateInput{RepoURL: "u", CredentialID: "c"}, ErrEmptyName},
		{"empty repo", CreateInput{Name: "n", CredentialID: "c"}, ErrEmptyRepoURL},
		{"empty cred", CreateInput{Name: "n", RepoURL: "u"}, ErrEmptyCredentialID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := svc.Create(ctx, tc.in); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestUpdateRename(t *testing.T) {
	db := testDB(t)
	v := vault.New(db, testMasterKey())
	svc := New(db, v, &stubProber{branch: "main"})
	credID := newCred(t, v, "tok")
	p, _ := svc.Create(context.Background(), CreateInput{Name: "old", RepoURL: "https://g/a.git", CredentialID: credID})

	newName := "renamed"
	updated, err := svc.Update(context.Background(), p.ID, UpdateInput{Name: &newName})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Name != "renamed" {
		t.Fatalf("name = %q, want renamed", updated.Name)
	}
}

func TestUpdateRebindCredentialReprobes(t *testing.T) {
	db := testDB(t)
	v := vault.New(db, testMasterKey())
	pr := &stubProber{branch: "main"}
	svc := New(db, v, pr)
	credID := newCred(t, v, "tok1")
	p, _ := svc.Create(context.Background(), CreateInput{Name: "x", RepoURL: "https://g/a.git", CredentialID: credID})

	cred2 := newCred(t, v, "tok2")
	callsBefore := pr.calls
	if _, err := svc.Update(context.Background(), p.ID, UpdateInput{CredentialID: &cred2}); err != nil {
		t.Fatalf("Update rebind: %v", err)
	}
	if pr.calls != callsBefore+1 {
		t.Fatalf("改绑凭据应重新探测一次, calls before=%d after=%d", callsBefore, pr.calls)
	}
	if pr.lastTo != "tok2" {
		t.Fatalf("改绑后应使用新凭据明文, got token mismatch")
	}
}

func TestDeleteAndNotFound(t *testing.T) {
	db := testDB(t)
	v := vault.New(db, testMasterKey())
	svc := New(db, v, &stubProber{branch: "main"})
	credID := newCred(t, v, "tok")
	p, _ := svc.Create(context.Background(), CreateInput{Name: "x", RepoURL: "https://g/a.git", CredentialID: credID})

	if err := svc.Delete(context.Background(), p.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := svc.Delete(context.Background(), p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("再删 err = %v, want ErrNotFound", err)
	}
	if _, err := svc.Get(context.Background(), p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get 已删 err = %v, want ErrNotFound", err)
	}
}

// TestDeleteRejectedWithActiveRuns 验证有 queued/running 运行的项目拒绝删除(409 语义)。
func TestDeleteRejectedWithActiveRuns(t *testing.T) {
	db := testDB(t)
	v := vault.New(db, testMasterKey())
	svc := New(db, v, &stubProber{branch: "main"})
	credID := newCred(t, v, "tok")
	p, _ := svc.Create(context.Background(), CreateInput{Name: "x", RepoURL: "https://g/a.git", CredentialID: credID})

	// 注入一条 running 运行。
	if _, err := db.Exec(
		`INSERT INTO pipeline_runs (id, project_id, status, created_at) VALUES (?, ?, 'running', ?)`,
		"run-1", p.ID, "2026-01-01T00:00:00Z",
	); err != nil {
		t.Fatalf("insert run: %v", err)
	}
	if err := svc.Delete(context.Background(), p.ID); !errors.Is(err, ErrProjectHasActiveRuns) {
		t.Fatalf("有活跃运行删项目 err = %v, want ErrProjectHasActiveRuns", err)
	}

	// 终态运行不阻止删除。
	if _, err := db.Exec(`UPDATE pipeline_runs SET status = 'success' WHERE id = 'run-1'`); err != nil {
		t.Fatalf("update run: %v", err)
	}
	if err := svc.Delete(context.Background(), p.ID); err != nil {
		t.Fatalf("无活跃运行应可删, err = %v", err)
	}
}

// TestDeleteCredentialRestrictedWhileReferenced 验证 FK RESTRICT:被项目引用的凭据不可删。
func TestDeleteCredentialRestrictedWhileReferenced(t *testing.T) {
	db := testDB(t)
	v := vault.New(db, testMasterKey())
	svc := New(db, v, &stubProber{branch: "main"})
	credID := newCred(t, v, "tok")
	if _, err := svc.Create(context.Background(), CreateInput{Name: "x", RepoURL: "https://g/a.git", CredentialID: credID}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// vault.Delete 直接 DELETE credentials,应被外键 RESTRICT 阻止(返回错误)。
	if err := v.Delete(credID); err == nil {
		t.Fatalf("删除仍被引用的凭据应失败(FK RESTRICT),但成功了")
	}
}

// TestTestCloneSuccessAndFailure 覆盖 test-clone 的成功与失败路径(stub)。
func TestTestCloneSuccessAndFailure(t *testing.T) {
	db := testDB(t)
	v := vault.New(db, testMasterKey())
	credID := newCred(t, v, "tok")

	okSvc := New(db, v, &stubProber{branch: "main"})
	res, err := okSvc.TestClone(context.Background(), "https://g/a.git", credID)
	if err != nil {
		t.Fatalf("TestClone ok: %v", err)
	}
	if res.DefaultBranch != "main" {
		t.Fatalf("defaultBranch = %q, want main", res.DefaultBranch)
	}

	badSvc := New(db, v, &stubProber{err: ErrRepoUnreachable})
	if _, err := badSvc.TestClone(context.Background(), "https://bad.invalid/a.git", credID); !errors.Is(err, ErrRepoUnreachable) {
		t.Fatalf("err = %v, want ErrRepoUnreachable", err)
	}
}

// TestRealLsRemoteSkipped 真实网络 ls-remote 成功路径需真实 repo+token,CI 不便:跳过。
// 失败路径(坏 URL / 无效凭据)已由真实 go-git prober 在 prober_test.go 覆盖。
func TestRealLsRemoteSkipped(t *testing.T) {
	t.Skip("真实私有仓库 ls-remote 成功路径需真实 repo+token,CI 不便;失败路径见 prober_test.go")
}
