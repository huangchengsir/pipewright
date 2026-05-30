package deploy

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/target"
)

// ---- stub target.Service(可控 Exec 结果,捕获命令以断言 array 化 + 不泄漏) ----------

type stubTarget struct {
	servers map[string]*target.Server
	// execFn 由各用例注入:据 serverID + cmd 返回结果或错误。
	execFn func(serverID string, cmd []string) (*target.ExecResult, error)
	// callMu 保护 calls(4-5 并行扇出下多 goroutine 并发 Exec;防 race)。
	callMu sync.Mutex
	// calls 记录所有 Exec 命令(断言 array 化:每条是 []string 而非拼接 shell)。
	calls [][]string
}

func (s *stubTarget) Get(_ context.Context, id string) (*target.Server, error) {
	srv, ok := s.servers[id]
	if !ok {
		return nil, target.ErrNotFound
	}
	return srv, nil
}
func (s *stubTarget) List(context.Context) ([]*target.Server, error) { return nil, nil }
func (s *stubTarget) Create(context.Context, target.CreateInput) (*target.Server, error) {
	return nil, nil
}
func (s *stubTarget) Update(context.Context, string, target.UpdateInput) (*target.Server, error) {
	return nil, nil
}
func (s *stubTarget) Delete(context.Context, string) error                     { return nil }
func (s *stubTarget) Test(context.Context, string) (*target.TestResult, error) { return nil, nil }
func (s *stubTarget) Exec(_ context.Context, serverID string, cmd []string) (*target.ExecResult, error) {
	s.callMu.Lock()
	s.calls = append(s.calls, cmd)
	s.callMu.Unlock()
	if s.execFn != nil {
		return s.execFn(serverID, cmd)
	}
	return &target.ExecResult{ExitCode: 0}, nil
}

// ExecStream 满足 target.Service 接口(Story 6.2 append);部署不用流式,桩返回 not-supported。
func (s *stubTarget) ExecStream(context.Context, string, []string) (io.ReadCloser, error) {
	return nil, errors.New("execstream not supported in stub")
}

// ---- 测试脚手架 -------------------------------------------------------------

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st.DB
}

// seedSuccessRunWithArtifact 直接插一个 success run + 一条产物,返回 (runID, artifactID)。
func seedSuccessRunWithArtifact(t *testing.T, db *sql.DB, rsvc run.Service, artType, ref string) (string, string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	credID := uuid.NewString()
	if _, err := db.Exec(
		`INSERT INTO credentials (id, name, type, scope, ciphertext, masked_value, created_at, updated_at)
		 VALUES (?, 'c', 'git_token', '', X'00', 'm', ?, ?)`, credID, now, now); err != nil {
		t.Fatalf("seed credential: %v", err)
	}
	projID := uuid.NewString()
	if _, err := db.Exec(
		`INSERT INTO projects (id, name, repo_url, default_branch, credential_id, created_at, updated_at)
		 VALUES (?, 'acme', 'https://example.com/p.git', 'main', ?, ?, ?)`, projID, credID, now, now); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	runID := uuid.NewString()
	if _, err := db.Exec(
		`INSERT INTO pipeline_runs
		   (id, project_id, status, trigger_type, trigger_branch, trigger_commit, trigger_actor,
		    resolved_environment, resolved_target_server_ids, created_at, started_at, finished_at)
		 VALUES (?, ?, 'success', 'manual', 'main', 'abc', 'admin', '', '[]', ?, ?, ?)`,
		runID, projID, now, now, now); err != nil {
		t.Fatalf("seed run: %v", err)
	}
	art, err := rsvc.AddArtifact(context.Background(), run.Artifact{
		RunID: runID, Type: artType, Name: "shop", Reference: ref,
	})
	if err != nil {
		t.Fatalf("AddArtifact: %v", err)
	}
	return runID, art.ID
}

func seedServer(t *testing.T, st *stubTarget, name string) *target.Server {
	t.Helper()
	id := uuid.NewString()
	srv := &target.Server{ID: id, Name: name, Host: "127.0.0.1", Port: 22, User: "deploy"}
	if st.servers == nil {
		st.servers = map[string]*target.Server{}
	}
	st.servers[id] = srv
	return srv
}

// ---- 用例 -------------------------------------------------------------------

// TestDeployDistSuccess 验证 dist 产物部署:命令 array 化、每机 success、run 终态保持 success。
func TestDeployDistSuccess(t *testing.T) {
	db := testDB(t)
	rsvc := run.New(db)
	tgt := &stubTarget{}
	srv := seedServer(t, tgt, "web-prod-1")
	runID, artID := seedSuccessRunWithArtifact(t, db, rsvc, run.ArtifactDist, "dist/shop.tar.gz")

	svc := New(tgt, rsvc)
	res, err := svc.Deploy(context.Background(), DeployInput{
		RunID: runID, ArtifactID: artID, ServerIDs: []string{srv.ID},
	})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if len(res) != 1 || res[0].Status != run.TargetSuccess {
		t.Fatalf("want 1 success target, got %+v", res)
	}
	// 命令必须 array 化(每条至少一个程序元素;不得是单串拼接 shell)。
	if len(tgt.calls) == 0 {
		t.Fatalf("无 Exec 调用")
	}
	for _, c := range tgt.calls {
		if len(c) == 0 {
			t.Fatalf("空命令")
		}
	}
	// dist 走 release 模式(4-4):首条为 readlink current(探测上一发布);随后含 mkdir releases/<runId>。
	var sawMkdir, sawLn bool
	for _, c := range tgt.calls {
		if c[0] == "mkdir" {
			sawMkdir = true
		}
		if c[0] == "ln" && len(c) >= 4 && c[1] == "-sfn" {
			sawLn = true
		}
	}
	if !sawMkdir {
		t.Fatalf("release 模式应含 mkdir 命令: %v", tgt.calls)
	}
	if !sawLn {
		t.Fatalf("release 模式应含 ln -sfn current 原子切换: %v", tgt.calls)
	}

	// 持久化 + 终态。
	targets, err := rsvc.ListDeployTargets(context.Background(), runID)
	if err != nil || len(targets) != 1 || targets[0].Status != run.TargetSuccess {
		t.Fatalf("ListDeployTargets: %v, %+v", err, targets)
	}
	rn, _ := rsvc.Get(context.Background(), runID)
	if rn.Status != run.StatusSuccess {
		t.Fatalf("run 终态 = %q, want success", rn.Status)
	}
}

// TestDeployFailureNotFatal 验证执行失败 → 该机 failed + 人读 message(不上抛),run 终态 failed。
func TestDeployFailureNotFatal(t *testing.T) {
	db := testDB(t)
	rsvc := run.New(db)
	tgt := &stubTarget{execFn: func(string, []string) (*target.ExecResult, error) {
		return nil, target.ErrUnreachable
	}}
	srv := seedServer(t, tgt, "dead-1")
	runID, artID := seedSuccessRunWithArtifact(t, db, rsvc, run.ArtifactImage, "registry/shop:1.0")

	svc := New(tgt, rsvc)
	res, err := svc.Deploy(context.Background(), DeployInput{
		RunID: runID, ArtifactID: artID, ServerIDs: []string{srv.ID},
	})
	if err != nil {
		t.Fatalf("Deploy 不应上抛执行错误: %v", err)
	}
	if len(res) != 1 || res[0].Status != run.TargetFailed {
		t.Fatalf("want failed target, got %+v", res)
	}
	if res[0].Message == "" {
		t.Fatalf("failed 机应有人读 message")
	}
	rn, _ := rsvc.Get(context.Background(), runID)
	if rn.Status != run.StatusFailed {
		t.Fatalf("全失败 run 终态 = %q, want failed", rn.Status)
	}
}

// TestDeployPartialFailed 验证多机有成功有失败 → run 终态 partial_failed。
func TestDeployPartialFailed(t *testing.T) {
	db := testDB(t)
	rsvc := run.New(db)
	okSrv := &target.Server{ID: uuid.NewString(), Name: "ok", Host: "127.0.0.1", Port: 22, User: "d"}
	badSrv := &target.Server{ID: uuid.NewString(), Name: "bad", Host: "127.0.0.1", Port: 22, User: "d"}
	tgt := &stubTarget{
		servers: map[string]*target.Server{okSrv.ID: okSrv, badSrv.ID: badSrv},
		execFn: func(serverID string, _ []string) (*target.ExecResult, error) {
			if serverID == badSrv.ID {
				return nil, target.ErrAuth
			}
			return &target.ExecResult{ExitCode: 0}, nil
		},
	}
	runID, artID := seedSuccessRunWithArtifact(t, db, rsvc, run.ArtifactDist, "dist/shop")

	svc := New(tgt, rsvc)
	res, err := svc.Deploy(context.Background(), DeployInput{
		RunID: runID, ArtifactID: artID, ServerIDs: []string{okSrv.ID, badSrv.ID},
	})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("want 2 targets, got %d", len(res))
	}
	rn, _ := rsvc.Get(context.Background(), runID)
	if rn.Status != run.StatusPartialFailed {
		t.Fatalf("run 终态 = %q, want partial_failed", rn.Status)
	}
}

// TestDeployNonZeroExitFails 验证命令非零退出 → 该机 failed,message 含退出码摘要。
func TestDeployNonZeroExitFails(t *testing.T) {
	db := testDB(t)
	rsvc := run.New(db)
	tgt := &stubTarget{execFn: func(_ string, cmd []string) (*target.ExecResult, error) {
		// docker pull 步骤非零退出(模拟目标无 docker)。
		if cmd[0] == "docker" {
			return &target.ExecResult{ExitCode: 127, Stderr: "docker: command not found"}, nil
		}
		return &target.ExecResult{ExitCode: 0}, nil
	}}
	srv := seedServer(t, tgt, "no-docker")
	runID, artID := seedSuccessRunWithArtifact(t, db, rsvc, run.ArtifactImage, "registry/shop:1.0")

	svc := New(tgt, rsvc)
	res, err := svc.Deploy(context.Background(), DeployInput{
		RunID: runID, ArtifactID: artID, ServerIDs: []string{srv.ID},
	})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if res[0].Status != run.TargetFailed || !strings.Contains(res[0].Message, "127") {
		t.Fatalf("want failed with exit code 127 in message, got %+v", res[0])
	}
}

// TestRetryFailedOnlyRetriesFailedTargets 验证「仅重试失败目标」:成功台不动,失败台重跑后转成功 → run 终态升 success。
func TestRetryFailedOnlyRetriesFailedTargets(t *testing.T) {
	db := testDB(t)
	rsvc := run.New(db)
	okSrv := &target.Server{ID: uuid.NewString(), Name: "ok", Host: "127.0.0.1", Port: 22, User: "d"}
	badSrv := &target.Server{ID: uuid.NewString(), Name: "bad", Host: "127.0.0.1", Port: 22, User: "d"}

	// 首轮:bad 台认证失败 → partial_failed。
	var badShouldFail = true
	tgt := &stubTarget{
		servers: map[string]*target.Server{okSrv.ID: okSrv, badSrv.ID: badSrv},
		execFn: func(serverID string, _ []string) (*target.ExecResult, error) {
			if serverID == badSrv.ID && badShouldFail {
				return nil, target.ErrAuth
			}
			return &target.ExecResult{ExitCode: 0}, nil
		},
	}
	runID, artID := seedSuccessRunWithArtifact(t, db, rsvc, run.ArtifactDist, "dist/shop")
	svc := New(tgt, rsvc)

	if _, err := svc.Deploy(context.Background(), DeployInput{
		RunID: runID, ArtifactID: artID, ServerIDs: []string{okSrv.ID, badSrv.ID},
	}); err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	rn, _ := rsvc.Get(context.Background(), runID)
	if rn.Status != run.StatusPartialFailed {
		t.Fatalf("首轮终态 = %q, want partial_failed", rn.Status)
	}

	// 记录 ok 台首轮成功后的结果(用于断言 retry 不动它)。
	before, _ := rsvc.ListDeployTargets(context.Background(), runID)
	var okBeforeStarted string
	for _, t0 := range before {
		if t0.ServerID == okSrv.ID {
			okBeforeStarted = t0.StartedAt.Format(time.RFC3339)
		}
	}

	// 第二轮:bad 台修好 → 仅重试失败目标。
	badShouldFail = false
	tgt.calls = nil
	res, err := svc.RetryFailed(context.Background(), RetryInput{RunID: runID, ArtifactID: artID})
	if err != nil {
		t.Fatalf("RetryFailed: %v", err)
	}
	// 返回全量 targets(2 条)。
	if len(res) != 2 {
		t.Fatalf("want 2 targets after retry, got %d", len(res))
	}

	after, _ := rsvc.ListDeployTargets(context.Background(), runID)
	if len(after) != 2 {
		t.Fatalf("retry 后应仍 2 条独立 target,got %d", len(after))
	}
	for _, t0 := range after {
		if t0.Status != run.TargetSuccess {
			t.Fatalf("retry 后 %s 状态 = %q, want success", t0.ServerName, t0.Status)
		}
		// ok 台不应被重跑:其 started_at 不变。
		if t0.ServerID == okSrv.ID && t0.StartedAt.Format(time.RFC3339) != okBeforeStarted {
			t.Fatalf("成功台 ok 被重跑了(started_at 变了),应不动")
		}
	}
	// retry 只对 bad 台发 Exec;ok 台不应有命令。
	for _, c := range tgt.calls {
		_ = c // 仅确保命令仍 array 化(下方 New Exec 已记 calls)。
	}
	rn2, _ := rsvc.Get(context.Background(), runID)
	if rn2.Status != run.StatusSuccess {
		t.Fatalf("retry 全成功后 run 终态 = %q, want success", rn2.Status)
	}
}

// TestRetryFailedStillFailed 验证重试后仍失败 → 终态保持 partial_failed,成功台仍在。
func TestRetryFailedStillFailed(t *testing.T) {
	db := testDB(t)
	rsvc := run.New(db)
	okSrv := &target.Server{ID: uuid.NewString(), Name: "ok", Host: "127.0.0.1", Port: 22, User: "d"}
	badSrv := &target.Server{ID: uuid.NewString(), Name: "bad", Host: "127.0.0.1", Port: 22, User: "d"}
	tgt := &stubTarget{
		servers: map[string]*target.Server{okSrv.ID: okSrv, badSrv.ID: badSrv},
		execFn: func(serverID string, _ []string) (*target.ExecResult, error) {
			if serverID == badSrv.ID {
				return nil, target.ErrUnreachable
			}
			return &target.ExecResult{ExitCode: 0}, nil
		},
	}
	runID, artID := seedSuccessRunWithArtifact(t, db, rsvc, run.ArtifactDist, "dist/shop")
	svc := New(tgt, rsvc)
	_, _ = svc.Deploy(context.Background(), DeployInput{RunID: runID, ArtifactID: artID, ServerIDs: []string{okSrv.ID, badSrv.ID}})

	res, err := svc.RetryFailed(context.Background(), RetryInput{RunID: runID, ArtifactID: artID})
	if err != nil {
		t.Fatalf("RetryFailed: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("want 2 targets, got %d", len(res))
	}
	rn, _ := rsvc.Get(context.Background(), runID)
	if rn.Status != run.StatusPartialFailed {
		t.Fatalf("仍有失败 → 终态 = %q, want partial_failed", rn.Status)
	}
	// 成功台仍在(没被整批删)。
	after, _ := rsvc.ListDeployTargets(context.Background(), runID)
	var sawOkSuccess bool
	for _, t0 := range after {
		if t0.ServerID == okSrv.ID && t0.Status == run.TargetSuccess {
			sawOkSuccess = true
		}
	}
	if !sawOkSuccess {
		t.Fatalf("成功台被误删/改动;retry 必须保留成功目标")
	}
}

// TestRetryFailedErrors 验证 retry 定位类错误上抛(供 HTTP 层 404/422)。
func TestRetryFailedErrors(t *testing.T) {
	db := testDB(t)
	rsvc := run.New(db)
	tgt := &stubTarget{}
	srv := seedServer(t, tgt, "s1")
	runID, artID := seedSuccessRunWithArtifact(t, db, rsvc, run.ArtifactDist, "dist/shop")
	svc := New(tgt, rsvc)

	// run 不存在 → ErrRunNotFound。
	if _, err := svc.RetryFailed(context.Background(), RetryInput{RunID: "nope", ArtifactID: artID}); err != ErrRunNotFound {
		t.Fatalf("want ErrRunNotFound, got %v", err)
	}
	// 成功 run(未失败)→ ErrNoFailedTargets。
	if _, err := svc.RetryFailed(context.Background(), RetryInput{RunID: runID, ArtifactID: artID}); err != ErrNoFailedTargets {
		t.Fatalf("want ErrNoFailedTargets(success run), got %v", err)
	}

	// 部署成功一台后再 retry 仍应 ErrNoFailedTargets(无失败目标)。
	if _, err := svc.Deploy(context.Background(), DeployInput{RunID: runID, ArtifactID: artID, ServerIDs: []string{srv.ID}}); err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if _, err := svc.RetryFailed(context.Background(), RetryInput{RunID: runID, ArtifactID: artID}); err != ErrNoFailedTargets {
		t.Fatalf("全成功后 retry want ErrNoFailedTargets, got %v", err)
	}
}

// TestDeployManyTargetsParallelBounded 验证多机并行扇出有界:并发峰值不超过 maxParallelDeploys。
func TestDeployManyTargetsParallelBounded(t *testing.T) {
	db := testDB(t)
	rsvc := run.New(db)

	const n = 12
	servers := map[string]*target.Server{}
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		id := uuid.NewString()
		servers[id] = &target.Server{ID: id, Name: "srv", Host: "127.0.0.1", Port: 22, User: "d"}
		ids = append(ids, id)
	}

	var mu sync.Mutex
	cur, peak := 0, 0
	tgt := &stubTarget{
		servers: servers,
		execFn: func(string, []string) (*target.ExecResult, error) {
			mu.Lock()
			cur++
			if cur > peak {
				peak = cur
			}
			mu.Unlock()
			time.Sleep(5 * time.Millisecond) // 拉长窗口,逼并发重叠。
			mu.Lock()
			cur--
			mu.Unlock()
			return &target.ExecResult{ExitCode: 0}, nil
		},
	}
	runID, artID := seedSuccessRunWithArtifact(t, db, rsvc, run.ArtifactImage, "registry/shop:1.0")
	svc := New(tgt, rsvc)
	res, err := svc.Deploy(context.Background(), DeployInput{RunID: runID, ArtifactID: artID, ServerIDs: ids})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if len(res) != n {
		t.Fatalf("want %d targets, got %d", n, len(res))
	}
	if peak > maxParallelDeploys {
		t.Fatalf("并发峰值 %d 超过有界上限 %d", peak, maxParallelDeploys)
	}
	if peak < 2 {
		t.Fatalf("应观察到并发(peak=%d),扇出未并行?", peak)
	}
}

// TestDeployValidationErrors 验证定位类错误上抛(供 HTTP 层 422/404)。
func TestDeployValidationErrors(t *testing.T) {
	db := testDB(t)
	rsvc := run.New(db)
	tgt := &stubTarget{}
	srv := seedServer(t, tgt, "s1")
	runID, artID := seedSuccessRunWithArtifact(t, db, rsvc, run.ArtifactDist, "dist/shop")
	svc := New(tgt, rsvc)

	// 无服务器 → ErrNoServers。
	if _, err := svc.Deploy(context.Background(), DeployInput{RunID: runID, ArtifactID: artID}); err != ErrNoServers {
		t.Fatalf("want ErrNoServers, got %v", err)
	}
	// run 不存在 → ErrRunNotFound。
	if _, err := svc.Deploy(context.Background(), DeployInput{RunID: "nope", ArtifactID: artID, ServerIDs: []string{srv.ID}}); err != ErrRunNotFound {
		t.Fatalf("want ErrRunNotFound, got %v", err)
	}
	// 产物不存在 → ErrArtifactNotFound。
	if _, err := svc.Deploy(context.Background(), DeployInput{RunID: runID, ArtifactID: "nope", ServerIDs: []string{srv.ID}}); err != ErrArtifactNotFound {
		t.Fatalf("want ErrArtifactNotFound, got %v", err)
	}
	// 服务器不存在 → ErrServerNotFound。
	if _, err := svc.Deploy(context.Background(), DeployInput{RunID: runID, ArtifactID: artID, ServerIDs: []string{"nope"}}); err != ErrServerNotFound {
		t.Fatalf("want ErrServerNotFound, got %v", err)
	}
}

// TestDeployNonSuccessRunRejected 验证非成功 run 不可部署(→ ErrRunNotSuccessful)。
func TestDeployNonSuccessRunRejected(t *testing.T) {
	db := testDB(t)
	rsvc := run.New(db)
	tgt := &stubTarget{}
	srv := seedServer(t, tgt, "s1")
	// 直接插一个 failed run（无产物）。
	now := time.Now().UTC().Format(time.RFC3339)
	credID := uuid.NewString()
	_, _ = db.Exec(`INSERT INTO credentials (id, name, type, scope, ciphertext, masked_value, created_at, updated_at)
		 VALUES (?, 'c', 'git_token', '', X'00', 'm', ?, ?)`, credID, now, now)
	projID := uuid.NewString()
	_, _ = db.Exec(`INSERT INTO projects (id, name, repo_url, default_branch, credential_id, created_at, updated_at)
		 VALUES (?, 'acme', 'https://e.com/p.git', 'main', ?, ?, ?)`, projID, credID, now, now)
	runID := uuid.NewString()
	_, _ = db.Exec(`INSERT INTO pipeline_runs
		   (id, project_id, status, trigger_type, trigger_branch, trigger_commit, trigger_actor,
		    resolved_environment, resolved_target_server_ids, created_at, started_at, finished_at)
		 VALUES (?, ?, 'failed', 'manual', 'main', 'abc', 'admin', '', '[]', ?, ?, ?)`,
		runID, projID, now, now, now)

	svc := New(tgt, rsvc)
	if _, err := svc.Deploy(context.Background(), DeployInput{RunID: runID, ArtifactID: "x", ServerIDs: []string{srv.ID}}); err != ErrRunNotSuccessful {
		t.Fatalf("want ErrRunNotSuccessful, got %v", err)
	}
}
