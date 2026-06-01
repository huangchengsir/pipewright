package environments

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/store"
)

// ---- 测试地基:真 SQLite store + 种子项目/运行/部署目标/产物 -----------------

func testService(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return NewService(st.DB), st.DB
}

func seedProject(t *testing.T, db *sql.DB) string {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	credID := uuid.NewString()
	if _, err := db.Exec(
		`INSERT INTO credentials (id, name, type, scope, ciphertext, masked_value, created_at, updated_at)
		 VALUES (?, 'c', 'git_token', '', X'00', 'm', ?, ?)`,
		credID, now, now,
	); err != nil {
		t.Fatalf("seed credential: %v", err)
	}
	projID := uuid.NewString()
	if _, err := db.Exec(
		`INSERT INTO projects (id, name, repo_url, default_branch, credential_id, created_at, updated_at)
		 VALUES (?, 'acme', 'https://example.com/p.git', 'main', ?, ?, ?)`,
		projID, credID, now, now,
	); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return projID
}

// seedRun 插一个指定环境的成功 run。
func seedRun(t *testing.T, db *sql.DB, projectID, env, commit string) string {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	id := uuid.NewString()
	if _, err := db.Exec(
		`INSERT INTO pipeline_runs
		   (id, project_id, status, trigger_type, trigger_branch, trigger_commit, trigger_actor,
		    resolved_environment, resolved_target_server_ids, params_json, created_at)
		 VALUES (?, ?, 'success', 'webhook', 'main', ?, 'alice', ?, '[]', '{}', ?)`,
		id, projectID, commit, env, now,
	); err != nil {
		t.Fatalf("seed run: %v", err)
	}
	return id
}

// seedTarget 给某 run 插一台目标机部署结果(at 决定部署时刻,递增以控制时间线顺序)。
func seedTarget(t *testing.T, db *sql.DB, runID, serverID, status string, at time.Time) {
	t.Helper()
	ts := at.UTC().Format(time.RFC3339)
	if _, err := db.Exec(
		`INSERT INTO deploy_targets
		   (id, run_id, server_id, server_name, status, message, started_at, finished_at)
		 VALUES (?, ?, ?, ?, ?, '', ?, ?)`,
		uuid.NewString(), runID, serverID, serverID+"-name", status, ts, ts,
	); err != nil {
		t.Fatalf("seed target: %v", err)
	}
}

func seedArtifact(t *testing.T, db *sql.DB, runID, typ, name, ref string) string {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	id := uuid.NewString()
	if _, err := db.Exec(
		`INSERT INTO run_artifacts (id, run_id, type, name, reference, size_bytes, metadata_json, created_at)
		 VALUES (?, ?, ?, ?, ?, 0, '', ?)`,
		id, runID, typ, name, ref, now,
	); err != nil {
		t.Fatalf("seed artifact: %v", err)
	}
	return id
}

// ---- 聚合:多 run 多环境正确分组 + 排序 + 活跃版本 --------------------------

func TestListEnvironments_GroupsAndOrders(t *testing.T) {
	svc, db := testService(t)
	proj := seedProject(t, db)
	base := time.Now().UTC().Add(-time.Hour)

	// staging:两次部署(t0 成功、t1 成功)。
	sRun0 := seedRun(t, db, proj, "staging", "aaa111")
	seedTarget(t, db, sRun0, "srv-s1", "success", base)
	sRun1 := seedRun(t, db, proj, "staging", "bbb222")
	seedTarget(t, db, sRun1, "srv-s1", "success", base.Add(10*time.Minute))

	// prod:一次部署(t2 成功,最近)。
	pRun := seedRun(t, db, proj, "prod", "ccc333")
	seedTarget(t, db, pRun, "srv-p1", "success", base.Add(20*time.Minute))

	// 未部署的 run(无 deploy_targets)→ 不应出现。
	_ = seedRun(t, db, proj, "dev", "ddd444")

	tls, err := svc.ListEnvironments(context.Background(), proj, 0)
	if err != nil {
		t.Fatalf("ListEnvironments: %v", err)
	}
	if len(tls) != 2 {
		t.Fatalf("want 2 environments (staging, prod), got %d: %+v", len(tls), tls)
	}
	// prod 最近部署在前。
	if tls[0].Environment != "prod" {
		t.Fatalf("want prod first (most recent), got %q", tls[0].Environment)
	}
	if tls[1].Environment != "staging" {
		t.Fatalf("want staging second, got %q", tls[1].Environment)
	}

	// staging 时间线:最近在前 = sRun1。
	staging := tls[1]
	if len(staging.Deployments) != 2 {
		t.Fatalf("staging want 2 deployments, got %d", len(staging.Deployments))
	}
	if staging.Deployments[0].RunID != sRun1 {
		t.Fatalf("staging newest deployment should be sRun1, got %q", staging.Deployments[0].RunID)
	}
	// 活跃版本 = 最近一次全成功 = sRun1。
	if staging.Active == nil || staging.Active.RunID != sRun1 {
		t.Fatalf("staging active should be sRun1, got %+v", staging.Active)
	}
	if !staging.Deployments[0].Active {
		t.Fatalf("newest staging deployment should be marked active")
	}
	if staging.Deployments[1].Active {
		t.Fatalf("older staging deployment should not be active")
	}
}

func TestListEnvironments_PartialAndFailedAggregation(t *testing.T) {
	svc, db := testService(t)
	proj := seedProject(t, db)
	base := time.Now().UTC().Add(-time.Hour)

	// 一次混合(一成功一失败)→ partial_failed。
	run := seedRun(t, db, proj, "prod", "mix")
	seedTarget(t, db, run, "srv-1", "success", base)
	seedTarget(t, db, run, "srv-2", "failed", base.Add(time.Minute))

	// 一次全失败 → failed。
	run2 := seedRun(t, db, proj, "prod", "allbad")
	seedTarget(t, db, run2, "srv-1", "failed", base.Add(2*time.Minute))

	tl, err := svc.EnvironmentHistory(context.Background(), proj, "prod", 0)
	if err != nil {
		t.Fatalf("EnvironmentHistory: %v", err)
	}
	if len(tl.Deployments) != 2 {
		t.Fatalf("want 2, got %d", len(tl.Deployments))
	}
	// 最近 = allbad(failed)。
	if got := tl.Deployments[0].Status; got != DeployStatusFailed {
		t.Fatalf("newest should be failed, got %q", got)
	}
	if got := tl.Deployments[1].Status; got != DeployStatusPartialFailed {
		t.Fatalf("older should be partial_failed, got %q", got)
	}
	// 无全成功 → 无活跃版本。
	if tl.Active != nil {
		t.Fatalf("expected no active version, got %+v", tl.Active)
	}
}

func TestListEnvironments_ProjectNotFound(t *testing.T) {
	svc, _ := testService(t)
	_, err := svc.ListEnvironments(context.Background(), "nope", 0)
	if !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("want ErrProjectNotFound, got %v", err)
	}
}

func TestListEnvironments_NoDeployments(t *testing.T) {
	svc, db := testService(t)
	proj := seedProject(t, db)
	tls, err := svc.ListEnvironments(context.Background(), proj, 0)
	if err != nil {
		t.Fatalf("ListEnvironments: %v", err)
	}
	if len(tls) != 0 {
		t.Fatalf("want empty, got %+v", tls)
	}
}

func TestEnvironmentHistory_NotFound(t *testing.T) {
	svc, db := testService(t)
	proj := seedProject(t, db)
	run := seedRun(t, db, proj, "staging", "x")
	seedTarget(t, db, run, "s1", "success", time.Now())

	if _, err := svc.EnvironmentHistory(context.Background(), proj, "prod", 0); !errors.Is(err, ErrEnvNotFound) {
		t.Fatalf("want ErrEnvNotFound, got %v", err)
	}
}

// ---- 回滚定位:上一次成功部署 ------------------------------------------------

func TestResolveRollback_HappyPath(t *testing.T) {
	svc, db := testService(t)
	proj := seedProject(t, db)
	base := time.Now().UTC().Add(-time.Hour)

	// 旧成功部署(回滚目标):产物 + 两台机。
	old := seedRun(t, db, proj, "prod", "old-sha")
	oldArt := seedArtifact(t, db, old, "dist", "web", "ref-old")
	seedTarget(t, db, old, "srv-1", "success", base)
	seedTarget(t, db, old, "srv-2", "success", base.Add(time.Minute))

	// 当前活跃部署(最近全成功)。
	cur := seedRun(t, db, proj, "prod", "new-sha")
	seedArtifact(t, db, cur, "dist", "web", "ref-new")
	seedTarget(t, db, cur, "srv-1", "success", base.Add(30*time.Minute))

	rt, err := svc.ResolveRollback(context.Background(), proj, "prod")
	if err != nil {
		t.Fatalf("ResolveRollback: %v", err)
	}
	if rt.RunID != old {
		t.Fatalf("rollback target run should be old, got %q", rt.RunID)
	}
	if rt.CurrentRunID != cur {
		t.Fatalf("current run should be cur, got %q", rt.CurrentRunID)
	}
	if rt.ArtifactID != oldArt {
		t.Fatalf("rollback artifact should be oldArt, got %q", rt.ArtifactID)
	}
	if len(rt.ServerIDs) != 2 {
		t.Fatalf("want 2 server ids, got %v", rt.ServerIDs)
	}
}

func TestResolveRollback_NoPreviousSuccess(t *testing.T) {
	svc, db := testService(t)
	proj := seedProject(t, db)
	base := time.Now().UTC().Add(-time.Hour)

	// 只有一次成功部署 → 无上一次成功可回滚。
	cur := seedRun(t, db, proj, "prod", "only")
	seedArtifact(t, db, cur, "dist", "web", "r")
	seedTarget(t, db, cur, "srv-1", "success", base)

	if _, err := svc.ResolveRollback(context.Background(), proj, "prod"); !errors.Is(err, ErrNoRollbackTarget) {
		t.Fatalf("want ErrNoRollbackTarget, got %v", err)
	}
}

func TestResolveRollback_SkipsFailedBetween(t *testing.T) {
	svc, db := testService(t)
	proj := seedProject(t, db)
	base := time.Now().UTC().Add(-2 * time.Hour)

	// 旧成功(回滚目标)。
	old := seedRun(t, db, proj, "prod", "old")
	seedArtifact(t, db, old, "dist", "web", "ref-old")
	seedTarget(t, db, old, "srv-1", "success", base)

	// 中间一次失败部署(不应被选为回滚目标)。
	bad := seedRun(t, db, proj, "prod", "bad")
	seedArtifact(t, db, bad, "dist", "web", "ref-bad")
	seedTarget(t, db, bad, "srv-1", "failed", base.Add(20*time.Minute))

	// 当前活跃成功。
	cur := seedRun(t, db, proj, "prod", "cur")
	seedArtifact(t, db, cur, "dist", "web", "ref-cur")
	seedTarget(t, db, cur, "srv-1", "success", base.Add(40*time.Minute))

	rt, err := svc.ResolveRollback(context.Background(), proj, "prod")
	if err != nil {
		t.Fatalf("ResolveRollback: %v", err)
	}
	if rt.RunID != old {
		t.Fatalf("rollback should skip failed and target old, got %q", rt.RunID)
	}
	_ = bad
}

func TestResolveRollback_EnvNotFound(t *testing.T) {
	svc, db := testService(t)
	proj := seedProject(t, db)
	if _, err := svc.ResolveRollback(context.Background(), proj, "ghost"); !errors.Is(err, ErrEnvNotFound) {
		t.Fatalf("want ErrEnvNotFound, got %v", err)
	}
}

func TestAggregateTargetStatus(t *testing.T) {
	cases := []struct {
		name string
		in   []TargetSummary
		want string
	}{
		{"all success", []TargetSummary{{Status: "success"}, {Status: "success"}}, DeployStatusSuccess},
		{"all failed", []TargetSummary{{Status: "failed"}, {Status: "rolled_back"}}, DeployStatusFailed},
		{"mixed", []TargetSummary{{Status: "success"}, {Status: "failed"}}, DeployStatusPartialFailed},
		{"in progress", []TargetSummary{{Status: "deploying"}}, DeployStatusPartialFailed},
		{"empty", nil, DeployStatusFailed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := aggregateTargetStatus(c.in); got != c.want {
				t.Fatalf("aggregateTargetStatus(%v) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
