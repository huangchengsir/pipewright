package run

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// insertRun 直插一条运行(全控 status/branch/commit/created_at),返回 run id。
// 绕过 Create 的状态机/调度,便于构造 baseline 选取场景。
func insertRun(t *testing.T, db *sql.DB, projectID, branch, commit, status string, createdAt time.Time) string {
	t.Helper()
	id := uuid.NewString()
	if _, err := db.Exec(
		`INSERT INTO pipeline_runs
		   (id, project_id, status, trigger_type, trigger_branch, trigger_commit, trigger_actor,
		    resolved_environment, resolved_target_server_ids, created_at, started_at, finished_at)
		 VALUES (?, ?, ?, 'manual', ?, ?, 'admin', '', '[]', ?, NULL, NULL)`,
		id, projectID, status, branch, commit, createdAt.UTC().Format(time.RFC3339),
	); err != nil {
		t.Fatalf("insert run: %v", err)
	}
	return id
}

// TestLastSuccessfulRunPicksNearestEarlierSuccess 验证 baseline 选取:
// 同项目 + 同分支 + 更早 + 最近成功。
func TestLastSuccessfulRunPicksNearestEarlierSuccess(t *testing.T) {
	db := testDB(t)
	svc := New(db)
	proj := seedProject(t, db)

	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	// 更早的两次成功:t0(更早)与 t1(更近);应选 t1。
	insertRun(t, db, proj, "main", "sha-old", StatusSuccess, base.Add(-3*time.Hour))
	wantID := insertRun(t, db, proj, "main", "sha-recent", StatusSuccess, base.Add(-1*time.Hour))
	// 干扰项:同分支但失败(不应选)。
	insertRun(t, db, proj, "main", "sha-failed", StatusFailed, base.Add(-30*time.Minute))

	current := base // 本次失败运行的 created_at

	got, err := svc.LastSuccessfulRun(context.Background(), proj, "main", current)
	if err != nil {
		t.Fatalf("LastSuccessfulRun: %v", err)
	}
	if got.ID != wantID {
		t.Fatalf("picked %s (commit=%s), want %s", got.ID, got.Trigger.Commit, wantID)
	}
	if got.Trigger.Commit != "sha-recent" {
		t.Fatalf("commit = %q, want sha-recent", got.Trigger.Commit)
	}
}

// TestLastSuccessfulRunBranchScoped 验证只取同分支(不同分支的成功不算 baseline)。
func TestLastSuccessfulRunBranchScoped(t *testing.T) {
	db := testDB(t)
	svc := New(db)
	proj := seedProject(t, db)

	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	// 别的分支有更近的成功,但不该被选。
	insertRun(t, db, proj, "develop", "sha-dev", StatusSuccess, base.Add(-10*time.Minute))
	wantID := insertRun(t, db, proj, "main", "sha-main", StatusSuccess, base.Add(-2*time.Hour))

	got, err := svc.LastSuccessfulRun(context.Background(), proj, "main", base)
	if err != nil {
		t.Fatalf("LastSuccessfulRun: %v", err)
	}
	if got.ID != wantID {
		t.Fatalf("picked %s, want %s (branch scoping failed)", got.ID, wantID)
	}
}

// TestLastSuccessfulRunNoBaseline 验证无更早成功 → ErrNotFound。
func TestLastSuccessfulRunNoBaseline(t *testing.T) {
	db := testDB(t)
	svc := New(db)
	proj := seedProject(t, db)

	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	// 只有一次失败 + 一次更晚的成功(均不该被选)。
	insertRun(t, db, proj, "main", "sha-failed", StatusFailed, base.Add(-1*time.Hour))
	insertRun(t, db, proj, "main", "sha-later", StatusSuccess, base.Add(1*time.Hour)) // 晚于 before

	_, err := svc.LastSuccessfulRun(context.Background(), proj, "main", base)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// TestLastSuccessfulRunStrictlyEarlier 验证「更早」为严格小于(同时刻不算 baseline)。
func TestLastSuccessfulRunStrictlyEarlier(t *testing.T) {
	db := testDB(t)
	svc := New(db)
	proj := seedProject(t, db)

	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	insertRun(t, db, proj, "main", "sha-equal", StatusSuccess, base) // 同时刻,不该选

	_, err := svc.LastSuccessfulRun(context.Background(), proj, "main", base)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound (equal time must not match)", err)
	}
}
