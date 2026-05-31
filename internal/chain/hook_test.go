package chain

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/run"
)

// setRunSuccess 直接把某 run 置为 success 终态(测试用:绕过桩 runner / 状态机驱动,直击钩子逻辑)。
func setRunSuccess(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(
		`UPDATE pipeline_runs SET status = 'success', finished_at = ? WHERE id = ?`, now, id); err != nil {
		t.Fatalf("set run success: %v", err)
	}
}

// runsForProject 返回某项目全部运行 id(按创建时间升序)。
func runsForProject(t *testing.T, db *sql.DB, projectID string) []string {
	t.Helper()
	rows, err := db.Query(
		`SELECT id FROM pipeline_runs WHERE project_id = ? ORDER BY created_at ASC, id ASC`, projectID)
	if err != nil {
		t.Fatalf("query runs: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan run id: %v", err)
		}
		out = append(out, id)
	}
	return out
}

// totalRuns 返回库中全部运行总数(用于无限链兜底:总数有界即证明无 runaway)。
func totalRuns(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(1) FROM pipeline_runs`).Scan(&n); err != nil {
		t.Fatalf("count runs: %v", err)
	}
	return n
}

// childRunOf 返回由 sourceID 串联触发的那一个下游运行 id(chain_source_run_id = sourceID)。
// 确定性取子运行,避免依赖秒粒度 created_at 排序(同秒多运行会非确定)。
func childRunOf(t *testing.T, db *sql.DB, sourceID string) string {
	t.Helper()
	var id string
	if err := db.QueryRow(
		`SELECT id FROM pipeline_runs WHERE chain_source_run_id = ? LIMIT 1`, sourceID,
	).Scan(&id); err != nil {
		t.Fatalf("child run of %s: %v", sourceID, err)
	}
	return id
}

// TestHookTriggersDownstreamOnSuccess 验证:上游 run 成功 → 钩子为每个启用下游目标创建一次
// TriggerChain 运行,且携带 chain_source_run_id = 上游 + chain_depth = 上游+1。
func TestHookTriggersDownstreamOnSuccess(t *testing.T) {
	db := testDB(t)
	chains := NewService(db)
	runs := run.New(db)

	up := seedProject(t, db)
	down := seedProject(t, db)
	if _, err := chains.Save(context.Background(), up, []Target{{DownstreamProjectID: down, Branch: "main", Enabled: true}}); err != nil {
		t.Fatalf("Save chain: %v", err)
	}

	// 建上游 run(根:depth 0)并置 success。
	upRun, err := runs.Create(context.Background(), up, run.Trigger{Type: run.TriggerManual, Branch: "main"})
	if err != nil {
		t.Fatalf("create upstream run: %v", err)
	}
	setRunSuccess(t, db, upRun.ID)

	NewHook(chains, runs, DefaultMaxDepth)(context.Background(), upRun.ID, run.StatusSuccess)

	dRuns := runsForProject(t, db, down)
	if len(dRuns) != 1 {
		t.Fatalf("期望下游创建 1 个串联运行,得 %d", len(dRuns))
	}
	got, err := runs.Get(context.Background(), dRuns[0])
	if err != nil {
		t.Fatalf("get downstream run: %v", err)
	}
	if got.Trigger.Type != run.TriggerChain {
		t.Fatalf("下游运行触发类型应为 chain,得 %q", got.Trigger.Type)
	}
	if got.Trigger.ChainSourceRunID != upRun.ID {
		t.Fatalf("下游 chain_source_run_id 应为上游 %s,得 %q", upRun.ID, got.Trigger.ChainSourceRunID)
	}
	if got.Trigger.ChainDepth != 1 {
		t.Fatalf("下游 chain_depth 应为 1,得 %d", got.Trigger.ChainDepth)
	}
}

// TestHookOnlyOnSuccess 验证:非 success 终态不触发任何串联。
func TestHookOnlyOnSuccess(t *testing.T) {
	db := testDB(t)
	chains := NewService(db)
	runs := run.New(db)
	up := seedProject(t, db)
	down := seedProject(t, db)
	_, _ = chains.Save(context.Background(), up, []Target{{DownstreamProjectID: down, Enabled: true}})

	upRun, _ := runs.Create(context.Background(), up, run.Trigger{Type: run.TriggerManual})
	hook := NewHook(chains, runs, DefaultMaxDepth)
	hook(context.Background(), upRun.ID, run.StatusFailed)
	hook(context.Background(), upRun.ID, run.StatusPartialFailed)
	hook(context.Background(), upRun.ID, run.StatusRolledBack)

	if n := len(runsForProject(t, db, down)); n != 0 {
		t.Fatalf("非成功终态不应触发串联,得 %d 个下游运行", n)
	}
}

// TestHookDisabledTargetSkipped 验证:禁用的下游目标不触发。
func TestHookDisabledTargetSkipped(t *testing.T) {
	db := testDB(t)
	chains := NewService(db)
	runs := run.New(db)
	up := seedProject(t, db)
	down := seedProject(t, db)
	_, _ = chains.Save(context.Background(), up, []Target{{DownstreamProjectID: down, Enabled: false}})

	upRun, _ := runs.Create(context.Background(), up, run.Trigger{Type: run.TriggerManual})
	setRunSuccess(t, db, upRun.ID)
	NewHook(chains, runs, DefaultMaxDepth)(context.Background(), upRun.ID, run.StatusSuccess)

	if n := len(runsForProject(t, db, down)); n != 0 {
		t.Fatalf("禁用目标不应触发,得 %d", n)
	}
}

// TestHookDepthGuardStopsRunaway 是核心环路安全测试(深度门):一条足够长的「不同项目」串联链
// P0→P1→…→P9(每个项目都各异,路径门不会触发),反复跑钩子。深度门应在 depth 达 maxDepth 后
// 停止创建下游 —— 链严格有界(至多 maxDepth 层),绝不无限触发,即便后面仍有可触发的下游配置。
func TestHookDepthGuardStopsRunaway(t *testing.T) {
	db := testDB(t)
	chains := NewService(db)
	runs := run.New(db)

	const numProjects = 10
	projs := make([]string, numProjects)
	for i := range projs {
		projs[i] = seedProject(t, db)
	}
	// 配 P0→P1→…→P8(线性,各项目相异),链长 9 > maxDepth,确保停止靠深度门而非项目耗尽。
	for i := 0; i < numProjects-1; i++ {
		if _, err := chains.Save(context.Background(), projs[i],
			[]Target{{DownstreamProjectID: projs[i+1], Enabled: true}}); err != nil {
			t.Fatalf("save %d→%d: %v", i, i+1, err)
		}
	}

	maxDepth := 3
	hook := NewHook(chains, runs, maxDepth)

	// 根运行 P0(depth 0)成功 → 沿链触发。每轮取本轮新建下游、置 success、再跑钩子。
	root, _ := runs.Create(context.Background(), projs[0], run.Trigger{Type: run.TriggerManual, Branch: "main"})
	setRunSuccess(t, db, root.ID)

	cur := root.ID
	created := []string{root.ID}
	for i := 0; i < 50; i++ { // 远超 maxDepth:若深度门失效会一直造到项目耗尽(≥9 层)
		before := totalRuns(t, db)
		hook(context.Background(), cur, run.StatusSuccess)
		after := totalRuns(t, db)
		if after == before {
			break // 深度门生效:不再创建下游,链终止。
		}
		next := childRunOf(t, db, cur)
		setRunSuccess(t, db, next)
		created = append(created, next)
		cur = next
	}

	// 总运行数 = 根 + 深度门允许的层数;depth 0..maxDepth → 至多 maxDepth+1 个运行。
	if n := totalRuns(t, db); n > maxDepth+1 {
		t.Fatalf("环路安全失败:总运行数 %d 超出上界 %d(深度门未生效)", n, maxDepth+1)
	}
	// 最深下游 depth 应恰为 maxDepth(达上限后停止深入)。
	maxSeen := 0
	for _, id := range created {
		r, _ := runs.Get(context.Background(), id)
		if r.Trigger.ChainDepth > maxSeen {
			maxSeen = r.Trigger.ChainDepth
		}
	}
	if maxSeen != maxDepth {
		t.Fatalf("最深串联深度应为 %d,得 %d", maxDepth, maxSeen)
	}
}

// TestHookPathGuardStopsCycle 验证路径门:A→B 且 B→A 配置成环时,B 成功触发 A 的串联被
// 「A 已在本链路径中」拒绝(即便深度未到上限),互环不 runaway。
func TestHookPathGuardStopsCycle(t *testing.T) {
	db := testDB(t)
	chains := NewService(db)
	runs := run.New(db)
	a := seedProject(t, db)
	b := seedProject(t, db)
	if _, err := chains.Save(context.Background(), a, []Target{{DownstreamProjectID: b, Enabled: true}}); err != nil {
		t.Fatalf("save A→B: %v", err)
	}
	if _, err := chains.Save(context.Background(), b, []Target{{DownstreamProjectID: a, Enabled: true}}); err != nil {
		t.Fatalf("save B→A: %v", err)
	}

	maxDepth := 10 // 故意放大,确保停止靠路径门而非深度门
	hook := NewHook(chains, runs, maxDepth)

	// 根 A 运行成功 → 触发 B(depth 1)。
	aRun, _ := runs.Create(context.Background(), a, run.Trigger{Type: run.TriggerManual, Branch: "main"})
	setRunSuccess(t, db, aRun.ID)
	hook(context.Background(), aRun.ID, run.StatusSuccess)

	bRuns := runsForProject(t, db, b)
	if len(bRuns) != 1 {
		t.Fatalf("A 成功应触发 1 个 B 运行,得 %d", len(bRuns))
	}
	// B 运行成功 → B→A;但 A 已在 B 运行的祖先链路径中(B.source=A 根)→ 路径门拒绝触发 A。
	setRunSuccess(t, db, bRuns[0])
	hook(context.Background(), bRuns[0], run.StatusSuccess)

	// A 应仍只有根那 1 个运行(互环被路径门掐断,没有第二个 A 运行被创建)。
	if n := len(runsForProject(t, db, a)); n != 1 {
		t.Fatalf("路径门应阻止 B→A 回环创建新 A 运行,A 运行数应为 1,得 %d", n)
	}
}
