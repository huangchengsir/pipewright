package retention

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/storetest"
)

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

func seedProject(t *testing.T, db *sql.DB) string {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	credID := uuid.NewString()
	mustExec(t, db, `INSERT INTO credentials (id, name, type, scope, ciphertext, masked_value, created_at, updated_at)
		VALUES (?, 'c', 'git_token', '', X'00', 'm', ?, ?)`, credID, now, now)
	projID := uuid.NewString()
	mustExec(t, db, `INSERT INTO projects (id, name, repo_url, default_branch, credential_id, created_at, updated_at)
		VALUES (?, 'acme', 'https://example.com/p.git', 'main', ?, ?, ?)`, projID, credID, now, now)
	return projID
}

// insertRun 插一条运行(指定状态 + created_at);返回 runID。
func insertRun(t *testing.T, db *sql.DB, projID, status string, createdAt time.Time) string {
	t.Helper()
	id := uuid.NewString()
	ts := createdAt.UTC().Format(time.RFC3339)
	mustExec(t, db, `INSERT INTO pipeline_runs (id, project_id, status, trigger_type, created_at)
		VALUES (?, ?, ?, 'manual', ?)`, id, projID, status, ts)
	return id
}

func countRuns(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(1) FROM pipeline_runs`).Scan(&n); err != nil {
		t.Fatalf("count runs: %v", err)
	}
	return n
}

func runExists(t *testing.T, db *sql.DB, id string) bool {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(1) FROM pipeline_runs WHERE id = ?`, id).Scan(&n); err != nil {
		t.Fatalf("exists: %v", err)
	}
	return n > 0
}

// TestPruneKeepPerProject 验证每项目保留最近 N 条终态运行,多出的(更旧的)被删。
func TestPruneKeepPerProject(t *testing.T) {
	db := storetest.OpenDB(t)
	svc := NewService(db)
	proj := seedProject(t, db)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var ids []string
	for i := 0; i < 5; i++ { // 5 条,created_at 递增(i 越大越新)
		ids = append(ids, insertRun(t, db, proj, "success", base.Add(time.Duration(i)*time.Hour)))
	}
	if err := svc.SetConfig(context.Background(), Config{Enabled: true, KeepPerProject: 2}); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	n, err := svc.Prune(context.Background(), base.Add(100*time.Hour))
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if n != 3 {
		t.Fatalf("应删除 3 条(保留最新 2),实删 %d", n)
	}
	// 最新两条(ids[3], ids[4])应保留;最旧三条删除。
	if !runExists(t, db, ids[4]) || !runExists(t, db, ids[3]) {
		t.Fatal("最新 2 条应保留")
	}
	if runExists(t, db, ids[0]) || runExists(t, db, ids[1]) || runExists(t, db, ids[2]) {
		t.Fatal("较旧 3 条应被删")
	}
}

// TestPruneSkipsNonTerminal 验证进行中的运行(queued/running/waiting_approval)绝不被删。
func TestPruneSkipsNonTerminal(t *testing.T) {
	db := storetest.OpenDB(t)
	svc := NewService(db)
	proj := seedProject(t, db)
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	running := insertRun(t, db, proj, "running", old)
	queued := insertRun(t, db, proj, "queued", old)
	waiting := insertRun(t, db, proj, "waiting_approval", old)
	done := insertRun(t, db, proj, "success", old)

	if err := svc.SetConfig(context.Background(), Config{Enabled: true, MaxAgeDays: 1}); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	n, err := svc.Prune(context.Background(), time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if n != 1 {
		t.Fatalf("仅终态(success)应被删,实删 %d", n)
	}
	if runExists(t, db, done) {
		t.Fatal("旧的 success 应被删")
	}
	for _, id := range []string{running, queued, waiting} {
		if !runExists(t, db, id) {
			t.Fatalf("进行中运行 %s 不应被删", id)
		}
	}
}

// TestPruneMaxAge 验证按年龄删除:早于 cutoff 的删,之后的留。
func TestPruneMaxAge(t *testing.T) {
	db := storetest.OpenDB(t)
	svc := NewService(db)
	proj := seedProject(t, db)
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	oldRun := insertRun(t, db, proj, "failed", now.AddDate(0, 0, -40)) // 40 天前
	newRun := insertRun(t, db, proj, "failed", now.AddDate(0, 0, -3))  // 3 天前

	if err := svc.SetConfig(context.Background(), Config{Enabled: true, MaxAgeDays: 30}); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	n, err := svc.Prune(context.Background(), now)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if n != 1 || runExists(t, db, oldRun) || !runExists(t, db, newRun) {
		t.Fatalf("应只删 40 天前那条(n=%d, old存在=%v, new存在=%v)", n, runExists(t, db, oldRun), runExists(t, db, newRun))
	}
}

// TestPruneDisabledNoop 验证总开关关 / 无任何条件时不删。
func TestPruneDisabledNoop(t *testing.T) {
	db := storetest.OpenDB(t)
	svc := NewService(db)
	proj := seedProject(t, db)
	old := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		insertRun(t, db, proj, "success", old)
	}
	// 关闭 → 不删
	_ = svc.SetConfig(context.Background(), Config{Enabled: false, KeepPerProject: 1, MaxAgeDays: 1})
	if n, _ := svc.Prune(context.Background(), time.Now()); n != 0 {
		t.Fatalf("关闭时应不删,实删 %d", n)
	}
	// 开但无条件(keep=0 且 age=0) → 不删
	_ = svc.SetConfig(context.Background(), Config{Enabled: true})
	if n, _ := svc.Prune(context.Background(), time.Now()); n != 0 {
		t.Fatalf("无条件时应不删,实删 %d", n)
	}
	if countRuns(t, db) != 3 {
		t.Fatal("3 条都应保留")
	}
}

// TestPruneCleansNonCascadeChildren 验证删 run 时连带清掉无级联的 run_logs + deploy_targets
// (不留孤儿、不因 deploy_targets RESTRICT 外键报错)。
func TestPruneCleansNonCascadeChildren(t *testing.T) {
	db := storetest.OpenDB(t)
	svc := NewService(db)
	proj := seedProject(t, db)
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	run := insertRun(t, db, proj, "success", old)
	// run_logs(无外键)
	mustExec(t, db, `INSERT INTO run_logs (id, run_id, seq, ts, stream, step_ordinal, text)
		VALUES (?, ?, 1, ?, 'stdout', 0, 'hello')`, uuid.NewString(), run, old.Format(time.RFC3339))
	// deploy_targets(RESTRICT 外键)
	mustExec(t, db, `INSERT INTO deploy_targets (id, run_id, server_id, server_name, status, started_at)
		VALUES (?, ?, 'srv', 'srv-1', 'success', ?)`, uuid.NewString(), run, old.Format(time.RFC3339))

	if err := svc.SetConfig(context.Background(), Config{Enabled: true, MaxAgeDays: 1}); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	n, err := svc.Prune(context.Background(), time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Prune 应成功(不被 deploy_targets RESTRICT 卡住): %v", err)
	}
	if n != 1 || runExists(t, db, run) {
		t.Fatal("run 应被删")
	}
	for _, tbl := range []string{"run_logs", "deploy_targets"} {
		var c int
		if err := db.QueryRow(`SELECT COUNT(1) FROM `+tbl+` WHERE run_id = ?`, run).Scan(&c); err != nil {
			t.Fatalf("count %s: %v", tbl, err)
		}
		if c != 0 {
			t.Fatalf("%s 应被连带清空,残留 %d 条(孤儿)", tbl, c)
		}
	}
}

// TestConfigRoundTripNormalizesNegatives 验证配置往返 + 负值归零。
func TestConfigRoundTripNormalizesNegatives(t *testing.T) {
	db := storetest.OpenDB(t)
	svc := NewService(db)
	if err := svc.SetConfig(context.Background(), Config{Enabled: true, KeepPerProject: -5, MaxAgeDays: -1}); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	got, err := svc.GetConfig(context.Background())
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if !got.Enabled || got.KeepPerProject != 0 || got.MaxAgeDays != 0 {
		t.Fatalf("负值应归零, got %+v", got)
	}
}
