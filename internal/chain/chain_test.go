package chain

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/store"
)

// testDB 打开临时 SQLite(含全部迁移,含 0030 串联表/列)。
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st.DB
}

// seedProject 插一个项目(满足外键),返回 project id。
func seedProject(t *testing.T, db *sql.DB) string {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	credID := uuid.NewString()
	if _, err := db.Exec(
		`INSERT INTO credentials (id, name, type, scope, ciphertext, masked_value, created_at, updated_at)
		 VALUES (?, 'c', 'git_token', '', X'00', 'm', ?, ?)`,
		credID, now, now); err != nil {
		t.Fatalf("seed credential: %v", err)
	}
	projID := uuid.NewString()
	if _, err := db.Exec(
		`INSERT INTO projects (id, name, repo_url, default_branch, credential_id, created_at, updated_at)
		 VALUES (?, ?, 'https://example.com/p.git', 'main', ?, ?, ?)`,
		projID, "proj-"+projID[:8], credID, now, now); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return projID
}

func TestSaveAndGet(t *testing.T) {
	db := testDB(t)
	svc := NewService(db)
	up := seedProject(t, db)
	d1 := seedProject(t, db)
	d2 := seedProject(t, db)

	cfg, err := svc.Save(context.Background(), up, []Target{
		{DownstreamProjectID: d1, Branch: "main", Enabled: true},
		{DownstreamProjectID: d2, Branch: "", Enabled: false},
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if len(cfg.Targets) != 2 {
		t.Fatalf("期望 2 个目标,得 %d", len(cfg.Targets))
	}

	got, err := svc.Get(context.Background(), up)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Targets) != 2 {
		t.Fatalf("Get 期望 2 个目标,得 %d", len(got.Targets))
	}

	enabled, err := svc.ListEnabled(context.Background(), up)
	if err != nil {
		t.Fatalf("ListEnabled: %v", err)
	}
	if len(enabled) != 1 || enabled[0].DownstreamProjectID != d1 {
		t.Fatalf("ListEnabled 应只含启用的 d1,得 %+v", enabled)
	}
}

func TestSaveReplacesAll(t *testing.T) {
	db := testDB(t)
	svc := NewService(db)
	up := seedProject(t, db)
	d1 := seedProject(t, db)
	d2 := seedProject(t, db)

	if _, err := svc.Save(context.Background(), up, []Target{{DownstreamProjectID: d1, Enabled: true}}); err != nil {
		t.Fatalf("Save#1: %v", err)
	}
	// 第二次保存应整批替换(d1 消失,只剩 d2)。
	if _, err := svc.Save(context.Background(), up, []Target{{DownstreamProjectID: d2, Enabled: true}}); err != nil {
		t.Fatalf("Save#2: %v", err)
	}
	got, _ := svc.Get(context.Background(), up)
	if len(got.Targets) != 1 || got.Targets[0].DownstreamProjectID != d2 {
		t.Fatalf("Save 应全量替换,得 %+v", got.Targets)
	}
}

func TestSaveEmptyClears(t *testing.T) {
	db := testDB(t)
	svc := NewService(db)
	up := seedProject(t, db)
	d1 := seedProject(t, db)
	_, _ = svc.Save(context.Background(), up, []Target{{DownstreamProjectID: d1, Enabled: true}})
	if _, err := svc.Save(context.Background(), up, nil); err != nil {
		t.Fatalf("Save empty: %v", err)
	}
	got, _ := svc.Get(context.Background(), up)
	if len(got.Targets) != 0 {
		t.Fatalf("空保存应清空,得 %+v", got.Targets)
	}
}

func TestSaveRejectsSelfChain(t *testing.T) {
	db := testDB(t)
	svc := NewService(db)
	up := seedProject(t, db)
	_, err := svc.Save(context.Background(), up, []Target{{DownstreamProjectID: up, Enabled: true}})
	if err != ErrSelfChain {
		t.Fatalf("自环应被拒,得 %v", err)
	}
}

func TestSaveRejectsMissingDownstream(t *testing.T) {
	db := testDB(t)
	svc := NewService(db)
	up := seedProject(t, db)
	_, err := svc.Save(context.Background(), up, []Target{{DownstreamProjectID: "no-such-project", Enabled: true}})
	if err != ErrDownstreamNotFound {
		t.Fatalf("不存在下游应被拒,得 %v", err)
	}
}

func TestSaveRejectsDuplicate(t *testing.T) {
	db := testDB(t)
	svc := NewService(db)
	up := seedProject(t, db)
	d1 := seedProject(t, db)
	_, err := svc.Save(context.Background(), up, []Target{
		{DownstreamProjectID: d1, Branch: "main", Enabled: true},
		{DownstreamProjectID: d1, Branch: "main", Enabled: false},
	})
	if err != ErrDuplicateTarget {
		t.Fatalf("重复 (下游,分支) 应被拒,得 %v", err)
	}
}

func TestSaveRejectsMissingUpstream(t *testing.T) {
	db := testDB(t)
	svc := NewService(db)
	_, err := svc.Save(context.Background(), "no-such-upstream", nil)
	if err != ErrProjectNotFound {
		t.Fatalf("不存在上游应被拒,得 %v", err)
	}
}

func TestGetUnconfiguredEmpty(t *testing.T) {
	db := testDB(t)
	svc := NewService(db)
	up := seedProject(t, db)
	got, err := svc.Get(context.Background(), up)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Targets) != 0 {
		t.Fatalf("未配置应返回空列表,得 %+v", got.Targets)
	}
}
