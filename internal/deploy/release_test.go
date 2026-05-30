package deploy

import (
	"context"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/target"
)

// fakeFS 模拟目标机上零停机切换关心的最小状态:current 软链指向的发布路径。
// readlink current → 返回当前指向(空 → 非零退出,模拟「无软链」首次部署)。
// ln -sfn <release> <current> → 更新指向。其余命令默认成功(可被 failCmd 覆盖)。
type fakeFS struct {
	current string // current 软链当前指向(空 = 不存在)
	// failHealthCmd 命中健康探测命令时返回非零(模拟健康失败)。
	failHealth bool
	// failLn 命中 ln 切换/回滚时返回执行错误(模拟回滚命令失败)。
	failLn bool
	// removed 记录被 prune rm -rf 的发布(断言清理)。
	calls [][]string
}

func (f *fakeFS) exec(cmd []string) (*target.ExecResult, error) {
	f.calls = append(f.calls, cmd)
	if len(cmd) == 0 {
		return &target.ExecResult{ExitCode: 1}, nil
	}
	switch cmd[0] {
	case "readlink":
		// readlink <current>:有指向 → stdout + 0;无 → 非零(GNU readlink 行为)。
		if f.current == "" {
			return &target.ExecResult{ExitCode: 1}, nil
		}
		return &target.ExecResult{ExitCode: 0, Stdout: f.current + "\n"}, nil
	case "ln":
		// ln -sfn <release> <current>:更新 current 指向(幂等)。
		if f.failLn {
			return nil, target.ErrUnreachable
		}
		if len(cmd) >= 4 && cmd[1] == "-sfn" {
			f.current = cmd[2]
		}
		return &target.ExecResult{ExitCode: 0}, nil
	case "curl":
		if f.failHealth {
			return &target.ExecResult{ExitCode: 22, Stderr: "curl: (22) 503 Service Unavailable"}, nil
		}
		return &target.ExecResult{ExitCode: 0}, nil
	default:
		return &target.ExecResult{ExitCode: 0}, nil
	}
}

// newFakeTarget 构造一个由 fakeFS 驱动 Exec 的 stubTarget(复用 deploy_test.go 的 stubTarget)。
func newFakeTarget(fs *fakeFS) *stubTarget {
	return &stubTarget{execFn: func(_ string, cmd []string) (*target.ExecResult, error) {
		return fs.exec(cmd)
	}}
}

func httpHealth() *HealthCheck {
	return &HealthCheck{Type: HealthCheckHTTP, URL: "http://127.0.0.1:8080/healthz", Retries: 1, IntervalSeconds: 0, TimeoutSeconds: 1}
}

// findCmd 在调用序列中找首条以 prog 开头且各参数包含 sub 的命令(断言用)。
func findCmd(calls [][]string, prog string) []string {
	for _, c := range calls {
		if len(c) > 0 && c[0] == prog {
			return c
		}
	}
	return nil
}

// TestReleaseDistAtomicSwitch:dist 走 release 模式 → mkdir releases/<runId> + ln -sfn current(原子切换)。
func TestReleaseDistAtomicSwitch(t *testing.T) {
	db := testDB(t)
	rsvc := run.New(db)
	fs := &fakeFS{}
	tgt := newFakeTarget(fs)
	srv := seedServer(t, tgt, "web-1")
	runID, artID := seedSuccessRunWithArtifact(t, db, rsvc, run.ArtifactDist, "dist/shop.tar.gz")

	svc := New(tgt, rsvc)
	res, err := svc.Deploy(context.Background(), DeployInput{
		RunID: runID, ArtifactID: artID, ServerIDs: []string{srv.ID},
		Config: map[string]string{"releaseBase": "/srv/shop"},
	})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if res[0].Status != run.TargetSuccess {
		t.Fatalf("want success, got %+v", res[0])
	}
	// mkdir 落 releases/<runId>。
	mkdir := findCmd(tgt.calls, "mkdir")
	if mkdir == nil || !strings.Contains(mkdir[len(mkdir)-1], "/srv/shop/releases/") {
		t.Fatalf("mkdir 未落 releases/<runId>: %v", mkdir)
	}
	// ln -sfn <release> /srv/shop/current(原子切换;array 化)。
	ln := findCmd(tgt.calls, "ln")
	if ln == nil || ln[1] != "-sfn" || ln[3] != "/srv/shop/current" {
		t.Fatalf("current 软链切换命令异常: %v", ln)
	}
	if fs.current == "" || !strings.Contains(fs.current, "/srv/shop/releases/") {
		t.Fatalf("current 未指向新发布: %q", fs.current)
	}
}

// TestReleaseHealthPassKeepsPrev:health=true → success;上一发布保留(回滚目标);健康在切换之后跑。
func TestReleaseHealthPassKeepsPrev(t *testing.T) {
	db := testDB(t)
	rsvc := run.New(db)
	fs := &fakeFS{current: "/srv/shop/releases/old-run"} // 已有上一发布
	tgt := newFakeTarget(fs)
	srv := seedServer(t, tgt, "web-1")
	runID, artID := seedSuccessRunWithArtifact(t, db, rsvc, run.ArtifactDist, "dist/shop.tar.gz")

	svc := New(tgt, rsvc)
	res, err := svc.Deploy(context.Background(), DeployInput{
		RunID: runID, ArtifactID: artID, ServerIDs: []string{srv.ID},
		Config:      map[string]string{"releaseBase": "/srv/shop"},
		HealthCheck: httpHealth(),
	})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if res[0].Status != run.TargetSuccess {
		t.Fatalf("want success, got %+v", res[0])
	}
	if !strings.Contains(res[0].Message, "健康检查通过") {
		t.Fatalf("message 应注明健康通过: %q", res[0].Message)
	}
	// current 指向新发布(非旧),旧发布仍可作回滚目标(未被切走前已保留)。
	if strings.Contains(fs.current, "old-run") {
		t.Fatalf("current 仍指向旧发布,切换未生效: %q", fs.current)
	}
	// 健康探测(curl)必须在 ln 切换之后调用(顺序断言)。
	lnIdx, curlIdx := -1, -1
	for i, c := range tgt.calls {
		if len(c) > 0 && c[0] == "ln" && lnIdx < 0 {
			lnIdx = i
		}
		if len(c) > 0 && c[0] == "curl" {
			curlIdx = i
		}
	}
	if lnIdx < 0 || curlIdx < 0 || curlIdx < lnIdx {
		t.Fatalf("健康探测应在 current 切换之后跑: lnIdx=%d curlIdx=%d", lnIdx, curlIdx)
	}
}

// TestReleaseHealthFailRollsBack:health=false + 有上一发布 → 回滚 current 切回上一 + status=rolled_back。
func TestReleaseHealthFailRollsBack(t *testing.T) {
	db := testDB(t)
	rsvc := run.New(db)
	prev := "/srv/shop/releases/old-run"
	fs := &fakeFS{current: prev, failHealth: true}
	tgt := newFakeTarget(fs)
	srv := seedServer(t, tgt, "web-1")
	runID, artID := seedSuccessRunWithArtifact(t, db, rsvc, run.ArtifactDist, "dist/shop.tar.gz")

	svc := New(tgt, rsvc)
	res, err := svc.Deploy(context.Background(), DeployInput{
		RunID: runID, ArtifactID: artID, ServerIDs: []string{srv.ID},
		Config:      map[string]string{"releaseBase": "/srv/shop"},
		HealthCheck: httpHealth(),
	})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if res[0].Status != run.TargetRolledBack {
		t.Fatalf("want rolled_back, got %+v", res[0])
	}
	// current 已切回上一发布。
	if fs.current != prev {
		t.Fatalf("current 未回滚到上一发布: %q want %q", fs.current, prev)
	}
	// message 人读:含回滚目标 + 健康原因;无明文密钥。
	if !strings.Contains(res[0].Message, "回滚") || !strings.Contains(res[0].Message, "old-run") {
		t.Fatalf("rolled_back message 应说明回滚目标: %q", res[0].Message)
	}
	// 持久化 + run 终态(rolled_back-only → run failed,target 级保留 rolled_back)。
	targets, _ := rsvc.ListDeployTargets(context.Background(), runID)
	if len(targets) != 1 || targets[0].Status != run.TargetRolledBack {
		t.Fatalf("targets 持久化异常: %+v", targets)
	}
	rn, _ := rsvc.Get(context.Background(), runID)
	if rn.Status != run.StatusFailed {
		t.Fatalf("rolled_back-only run 终态 = %q, want failed", rn.Status)
	}
}

// TestReleaseFirstDeployHealthFailNoRollback:首次部署(无上一发布)+ health=false → failed(无可回滚)。
func TestReleaseFirstDeployHealthFailNoRollback(t *testing.T) {
	db := testDB(t)
	rsvc := run.New(db)
	fs := &fakeFS{failHealth: true} // current 空 → 首次部署
	tgt := newFakeTarget(fs)
	srv := seedServer(t, tgt, "web-1")
	runID, artID := seedSuccessRunWithArtifact(t, db, rsvc, run.ArtifactDist, "dist/shop.tar.gz")

	svc := New(tgt, rsvc)
	res, err := svc.Deploy(context.Background(), DeployInput{
		RunID: runID, ArtifactID: artID, ServerIDs: []string{srv.ID},
		Config:      map[string]string{"releaseBase": "/srv/shop"},
		HealthCheck: httpHealth(),
	})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if res[0].Status != run.TargetFailed {
		t.Fatalf("want failed (no rollback), got %+v", res[0])
	}
	if !strings.Contains(res[0].Message, "无上一发布") {
		t.Fatalf("首次失败 message 应说明无可回滚: %q", res[0].Message)
	}
}

// TestReleaseRollbackCmdFailStillRecorded:回滚命令本身失败 → 仍记 rolled_back + 人读(不上抛/不 500)。
func TestReleaseRollbackCmdFailStillRecorded(t *testing.T) {
	db := testDB(t)
	rsvc := run.New(db)
	prev := "/srv/shop/releases/old-run"
	// 切换阶段需要 ln 成功;回滚阶段 ln 失败。用计数器:首条 ln(切换)成功,之后(回滚)失败。
	lnSeen := 0
	tgt := &stubTarget{execFn: func(_ string, cmd []string) (*target.ExecResult, error) {
		switch cmd[0] {
		case "readlink":
			return &target.ExecResult{ExitCode: 0, Stdout: prev + "\n"}, nil
		case "ln":
			lnSeen++
			if lnSeen >= 2 { // 第二次 ln = 回滚 → 失败
				return nil, target.ErrUnreachable
			}
			return &target.ExecResult{ExitCode: 0}, nil
		case "curl":
			return &target.ExecResult{ExitCode: 22, Stderr: "503"}, nil
		default:
			return &target.ExecResult{ExitCode: 0}, nil
		}
	}}
	srv := seedServer(t, tgt, "web-1")
	runID, artID := seedSuccessRunWithArtifact(t, db, rsvc, run.ArtifactDist, "dist/shop.tar.gz")

	svc := New(tgt, rsvc)
	res, err := svc.Deploy(context.Background(), DeployInput{
		RunID: runID, ArtifactID: artID, ServerIDs: []string{srv.ID},
		Config:      map[string]string{"releaseBase": "/srv/shop"},
		HealthCheck: httpHealth(),
	})
	if err != nil {
		t.Fatalf("Deploy 不应上抛(回滚失败也内化): %v", err)
	}
	if res[0].Status != run.TargetRolledBack {
		t.Fatalf("回滚命令失败仍应记 rolled_back, got %+v", res[0])
	}
	if !strings.Contains(res[0].Message, "回滚命令执行失败") {
		t.Fatalf("message 应说明回滚未确认: %q", res[0].Message)
	}
}

// TestReleaseJarMode:jar 也走 release 模式(放置 + java 探测 + current 切换)。
func TestReleaseJarMode(t *testing.T) {
	db := testDB(t)
	rsvc := run.New(db)
	fs := &fakeFS{}
	tgt := newFakeTarget(fs)
	srv := seedServer(t, tgt, "app-1")
	runID, artID := seedSuccessRunWithArtifact(t, db, rsvc, run.ArtifactJar, "build/shop.jar")

	svc := New(tgt, rsvc)
	res, err := svc.Deploy(context.Background(), DeployInput{
		RunID: runID, ArtifactID: artID, ServerIDs: []string{srv.ID},
		Config: map[string]string{"releaseBase": "/srv/app"},
	})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if res[0].Status != run.TargetSuccess {
		t.Fatalf("want success, got %+v", res[0])
	}
	// java -jar 探测命令 array 化。
	java := findCmd(tgt.calls, "java")
	if java == nil || java[1] != "-jar" {
		t.Fatalf("jar 启动探测命令异常: %v", java)
	}
	if fs.current == "" {
		t.Fatalf("jar 也应原子切换 current")
	}
}

// TestReleaseKeepReleasesPrune:keepReleases 触发 prune(sh -c array;目录/保留名作位置参数)。
func TestReleaseKeepReleasesPrune(t *testing.T) {
	db := testDB(t)
	rsvc := run.New(db)
	fs := &fakeFS{}
	tgt := newFakeTarget(fs)
	srv := seedServer(t, tgt, "web-1")
	runID, artID := seedSuccessRunWithArtifact(t, db, rsvc, run.ArtifactDist, "dist/shop.tar.gz")

	svc := New(tgt, rsvc)
	if _, err := svc.Deploy(context.Background(), DeployInput{
		RunID: runID, ArtifactID: artID, ServerIDs: []string{srv.ID},
		Config: map[string]string{"releaseBase": "/srv/shop", "keepReleases": "2"},
	}); err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	// prune 经单条 sh -c:脚本体固定,releasesDir / keep / cur / prev 作位置参数($0..$3)。
	var prune []string
	for _, c := range tgt.calls {
		if len(c) >= 3 && c[0] == "sh" && c[1] == "-c" && strings.Contains(c[2], "rm -rf") {
			prune = c
		}
	}
	if prune == nil {
		t.Fatalf("未发现 prune sh -c 命令: %v", tgt.calls)
	}
	// 位置参数:releasesDir、keep="2"、curRunID、prev。
	if prune[3] != "/srv/shop/releases" || prune[4] != "2" {
		t.Fatalf("prune 位置参数异常: %v", prune[3:])
	}
	// AC-SEC-02:脚本体内不得拼接目录/保留名(它们作为独立 array 元素传入)。
	if strings.Contains(prune[2], "/srv/shop") {
		t.Fatalf("prune 脚本体不应内联拼接路径(应作位置参数): %q", prune[2])
	}
}

// TestReleaseNoSecretInMessage:message / 命令绝无明文密钥(防泄漏回归)。
func TestReleaseNoSecretInMessage(t *testing.T) {
	db := testDB(t)
	rsvc := run.New(db)
	fs := &fakeFS{current: "/srv/shop/releases/old-run", failHealth: true}
	tgt := newFakeTarget(fs)
	srv := seedServer(t, tgt, "web-1")
	runID, artID := seedSuccessRunWithArtifact(t, db, rsvc, run.ArtifactDist, "dist/shop.tar.gz")

	svc := New(tgt, rsvc)
	res, _ := svc.Deploy(context.Background(), DeployInput{
		RunID: runID, ArtifactID: artID, ServerIDs: []string{srv.ID},
		Config:      map[string]string{"releaseBase": "/srv/shop"},
		HealthCheck: httpHealth(),
	})
	for _, bad := range []string{"PRIVATE KEY", "BEGIN OPENSSH", "password"} {
		if strings.Contains(res[0].Message, bad) {
			t.Fatalf("message 泄漏敏感串 %q: %q", bad, res[0].Message)
		}
	}
}
