package runner

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
)

type fakeExister struct{ ids map[string]bool }

func (f fakeExister) Exists(_ context.Context, id string) bool { return f.ids[id] }

func testDB(t *testing.T) *store.Store {
	return storetest.Open(t)
}

func seedProject(t *testing.T, st *store.Store) string {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	credID := uuid.NewString()
	if _, err := st.DB.Exec(`INSERT INTO credentials (id,name,type,scope,ciphertext,masked_value,created_at,updated_at) VALUES (?,'c','git_token','',X'00','m',?,?)`, credID, now, now); err != nil {
		t.Fatalf("seed cred: %v", err)
	}
	projID := uuid.NewString()
	if _, err := st.DB.Exec(`INSERT INTO projects (id,name,repo_url,default_branch,credential_id,created_at,updated_at) VALUES (?,'p','https://x/p.git','main',?,?,?)`, projID, credID, now, now); err != nil {
		t.Fatalf("seed proj: %v", err)
	}
	return projID
}

func TestGetDefaultIsLocal(t *testing.T) {
	st := testDB(t)
	svc := New(st.DB, nil)
	projID := seedProject(t, st)
	cfg, err := svc.Get(context.Background(), projID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if cfg.RunnerServerID != "" {
		t.Fatalf("默认应本地构建(空 runner),实际 %q", cfg.RunnerServerID)
	}
	if _, ok := svc.RunnerFor(context.Background(), projID); ok {
		t.Fatal("默认不应有远程 runner")
	}
}

func TestSaveAndGet(t *testing.T) {
	st := testDB(t)
	svc := New(st.DB, fakeExister{ids: map[string]bool{"srv-1": true}})
	projID := seedProject(t, st)

	if _, err := svc.Save(context.Background(), projID, "srv-1"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	cfg, _ := svc.Get(context.Background(), projID)
	if cfg.RunnerServerID != "srv-1" {
		t.Fatalf("runner = %q, want srv-1", cfg.RunnerServerID)
	}
	if id, ok := svc.RunnerFor(context.Background(), projID); !ok || id != "srv-1" {
		t.Fatalf("RunnerFor = %q/%v, want srv-1/true", id, ok)
	}
	// 清空 → 回本地。
	if _, err := svc.Save(context.Background(), projID, ""); err != nil {
		t.Fatalf("Save clear: %v", err)
	}
	if _, ok := svc.RunnerFor(context.Background(), projID); ok {
		t.Fatal("清空后应回本地构建")
	}
}

func TestSaveValidatesServerAndProject(t *testing.T) {
	st := testDB(t)
	svc := New(st.DB, fakeExister{ids: map[string]bool{"srv-1": true}})
	projID := seedProject(t, st)

	if _, err := svc.Save(context.Background(), projID, "ghost"); err != ErrServerNotFound {
		t.Fatalf("配不存在的机应 ErrServerNotFound,实际 %v", err)
	}
	if _, err := svc.Save(context.Background(), "no-such-project", "srv-1"); err != ErrProjectNotFound {
		t.Fatalf("不存在项目应 ErrProjectNotFound,实际 %v", err)
	}
}
