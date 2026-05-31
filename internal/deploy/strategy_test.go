package deploy

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/target"
)

// ---- 纯函数单元 ------------------------------------------------------------

func TestNormalizeStrategy(t *testing.T) {
	cases := map[string]string{
		"":            StrategyRolling,
		"rolling":     StrategyRolling,
		"unknown":     StrategyRolling,
		"canary":      StrategyCanary,
		"CANARY":      StrategyCanary,
		"blue_green":  StrategyBlueGreen,
		"blue-green":  StrategyBlueGreen,
		"BlueGreen":   StrategyBlueGreen,
		" blue green": StrategyBlueGreen,
	}
	for in, want := range cases {
		if got := NormalizeStrategy(in); got != want {
			t.Errorf("NormalizeStrategy(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCanaryCount(t *testing.T) {
	cases := []struct {
		cfg   map[string]string
		total int
		want  int
	}{
		{nil, 1, 1}, // 单机:整体即金丝雀
		{nil, 3, 1}, // 默认 1 台
		{map[string]string{"canaryCount": "2"}, 5, 2},
		{map[string]string{"canaryCount": "9"}, 3, 2}, // 夹紧:至少留 1 台在其余
		{map[string]string{"canaryPercent": "50"}, 4, 2},
		{map[string]string{"canaryPercent": "10"}, 5, 1}, // ceil(0.5)=1
		{map[string]string{"canaryCount": "0"}, 3, 1},    // 非法 → 默认 1
	}
	for _, c := range cases {
		if got := canaryCount(c.cfg, c.total); got != c.want {
			t.Errorf("canaryCount(%v, %d) = %d, want %d", c.cfg, c.total, got, c.want)
		}
	}
}

// ---- 测试用可控 stub:按 serverID 注入失败,记录每机被执行的命令 -----------------

// recordingTarget 包装 stubTarget 语义:execFn 据 (serverID, cmd) 决定结果,并记录触达的 serverID。
type touchRecorder struct {
	mu       sync.Mutex
	touched  map[string]bool // 被 Exec 过的 serverID
	sawMv    bool            // 是否出现过 cutover 切换(mv -T → current)
	failOn   func(serverID string, cmd []string) bool
	prevLink string // 非空 → readlink current 返回该路径(模拟已有上一发布)
}

func (r *touchRecorder) exec(serverID string, cmd []string) (*target.ExecResult, error) {
	r.mu.Lock()
	if r.touched == nil {
		r.touched = map[string]bool{}
	}
	r.touched[serverID] = true
	if cmd[0] == "mv" {
		r.sawMv = true
	}
	r.mu.Unlock()
	// 模拟 readlink current → 上一发布(供机群回滚有 prev 可回)。
	if cmd[0] == "readlink" {
		if r.prevLink != "" {
			return &target.ExecResult{ExitCode: 0, Stdout: r.prevLink}, nil
		}
		return &target.ExecResult{ExitCode: 0}, nil
	}
	if r.failOn != nil && r.failOn(serverID, cmd) {
		return &target.ExecResult{ExitCode: 1, Stderr: "injected failure"}, nil
	}
	return &target.ExecResult{ExitCode: 0}, nil
}

// ---- canary -----------------------------------------------------------------

func TestDeployCanaryProceeds(t *testing.T) {
	db := testDB(t)
	rsvc := run.New(db)
	rec := &touchRecorder{}
	tgt := &stubTarget{execFn: rec.exec}
	s1 := seedServer(t, tgt, "web-1")
	s2 := seedServer(t, tgt, "web-2")
	s3 := seedServer(t, tgt, "web-3")
	runID, artID := seedSuccessRunWithArtifact(t, db, rsvc, run.ArtifactDist, "dist/shop.tar.gz")

	svc := New(tgt, rsvc)
	res, err := svc.Deploy(context.Background(), DeployInput{
		RunID: runID, ArtifactID: artID,
		ServerIDs: []string{s1.ID, s2.ID, s3.ID},
		Strategy:  "canary",
	})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if len(res) != 3 {
		t.Fatalf("want 3 results, got %d", len(res))
	}
	for i, r := range res {
		if r.Status != run.TargetSuccess {
			t.Fatalf("target %d status = %s (msg %q), want success", i, r.Status, r.Message)
		}
	}
	// 金丝雀全过 → 其余机也应被部署(全 3 台触达)。
	if !rec.touched[s2.ID] || !rec.touched[s3.ID] {
		t.Fatalf("金丝雀通过后其余机应被部署;touched=%v", rec.touched)
	}
}

func TestDeployCanaryAbortsOnFailure(t *testing.T) {
	db := testDB(t)
	rsvc := run.New(db)
	s1ID, s2ID, s3ID := "", "", ""
	rec := &touchRecorder{
		failOn: func(serverID string, cmd []string) bool {
			// 金丝雀(第一台)放置命令失败。
			return serverID == s1ID && cmd[0] == "mkdir"
		},
	}
	tgt := &stubTarget{execFn: rec.exec}
	s1 := seedServer(t, tgt, "web-1")
	s2 := seedServer(t, tgt, "web-2")
	s3 := seedServer(t, tgt, "web-3")
	s1ID, s2ID, s3ID = s1.ID, s2.ID, s3.ID
	_ = s2ID
	_ = s3ID
	runID, artID := seedSuccessRunWithArtifact(t, db, rsvc, run.ArtifactDist, "dist/shop.tar.gz")

	svc := New(tgt, rsvc)
	res, err := svc.Deploy(context.Background(), DeployInput{
		RunID: runID, ArtifactID: artID,
		ServerIDs: []string{s1.ID, s2.ID, s3.ID},
		Strategy:  "canary",
	})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	// 金丝雀(s1)失败,其余(s2/s3)应被中止 → 全 failed。
	if res[0].Status != run.TargetFailed {
		t.Fatalf("canary status = %s, want failed", res[0].Status)
	}
	for i := 1; i < 3; i++ {
		if res[i].Status != run.TargetFailed {
			t.Fatalf("rest target %d status = %s, want failed(aborted)", i, res[i].Status)
		}
		if !strings.Contains(res[i].Message, "未部署") {
			t.Fatalf("rest target %d message = %q, want 含「未部署」", i, res[i].Message)
		}
	}
	// 关键:被中止的其余机**绝不应被执行任何部署命令**(仍运行旧版本)。
	if rec.touched[s2.ID] || rec.touched[s3.ID] {
		t.Fatalf("金丝雀失败后其余机不应被部署;touched=%v", rec.touched)
	}
}

// ---- blue-green -------------------------------------------------------------

func TestDeployBlueGreenSuccess(t *testing.T) {
	db := testDB(t)
	rsvc := run.New(db)
	rec := &touchRecorder{}
	tgt := &stubTarget{execFn: rec.exec}
	s1 := seedServer(t, tgt, "web-1")
	s2 := seedServer(t, tgt, "web-2")
	runID, artID := seedSuccessRunWithArtifact(t, db, rsvc, run.ArtifactDist, "dist/shop.tar.gz")

	svc := New(tgt, rsvc)
	res, err := svc.Deploy(context.Background(), DeployInput{
		RunID: runID, ArtifactID: artID,
		ServerIDs: []string{s1.ID, s2.ID},
		Strategy:  "blue_green",
	})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	for i, r := range res {
		if r.Status != run.TargetSuccess {
			t.Fatalf("target %d status = %s (msg %q), want success", i, r.Status, r.Message)
		}
	}
	if !rec.sawMv {
		t.Fatalf("蓝绿成功应发生 current 原子切换(mv -T)")
	}
}

// 蓝绿安全不变量:预备阶段任一机失败 → 绝不切换任何机(无 mv → current),已就绪机标 failed 未切换。
func TestDeployBlueGreenStageFailAbortsAllCutover(t *testing.T) {
	db := testDB(t)
	rsvc := run.New(db)
	s2ID := ""
	rec := &touchRecorder{
		failOn: func(serverID string, cmd []string) bool {
			return serverID == s2ID && cmd[0] == "mkdir" // 第二台就绪失败
		},
	}
	tgt := &stubTarget{execFn: rec.exec}
	s1 := seedServer(t, tgt, "web-1")
	s2 := seedServer(t, tgt, "web-2")
	s2ID = s2.ID
	runID, artID := seedSuccessRunWithArtifact(t, db, rsvc, run.ArtifactDist, "dist/shop.tar.gz")

	svc := New(tgt, rsvc)
	res, err := svc.Deploy(context.Background(), DeployInput{
		RunID: runID, ArtifactID: artID,
		ServerIDs: []string{s1.ID, s2.ID},
		Strategy:  "blue_green",
	})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	// 任一就绪失败 → 整体不切换;两机均非 success。
	for i, r := range res {
		if r.Status == run.TargetSuccess {
			t.Fatalf("target %d 不应 success(预备失败应中止全部切换);msg=%q", i, r.Message)
		}
	}
	if rec.sawMv {
		t.Fatalf("蓝绿预备阶段失败后绝不应发生任何 current 切换(mv),但出现了 cutover")
	}
}

// 蓝绿机群级原子性:切换阶段一机健康失败 → 已切换成功且有上一发布的机一并回滚(rolled_back)。
func TestDeployBlueGreenFleetRollbackOnPartialFailure(t *testing.T) {
	db := testDB(t)
	rsvc := run.New(db)
	badID := ""
	rec := &touchRecorder{
		prevLink: "/srv/app/releases/old", // 模拟已有上一发布 → 可回滚
		failOn: func(serverID string, cmd []string) bool {
			// 仅 bad 机健康探测(command=["true"])失败;放置/切换均成功。
			return serverID == badID && cmd[0] == "true"
		},
	}
	tgt := &stubTarget{execFn: rec.exec}
	good := seedServer(t, tgt, "web-good")
	bad := seedServer(t, tgt, "web-bad")
	badID = bad.ID
	runID, artID := seedSuccessRunWithArtifact(t, db, rsvc, run.ArtifactDist, "dist/shop.tar.gz")

	svc := New(tgt, rsvc)
	hc := &HealthCheck{Type: HealthCheckCommand, Command: []string{"true"}, Retries: 1}
	res, err := svc.Deploy(context.Background(), DeployInput{
		RunID: runID, ArtifactID: artID,
		ServerIDs:   []string{good.ID, bad.ID},
		Strategy:    "blue_green",
		HealthCheck: hc,
	})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	// 两机都应为 rolled_back:bad 机健康失败自身回滚;good 机因机群失败被一并回滚。
	for i, r := range res {
		if r.Status != run.TargetRolledBack {
			t.Fatalf("target %d (%s) status = %s (msg %q), want rolled_back", i, r.ServerName, r.Status, r.Message)
		}
	}
	// good 机回滚 message 应点明机群级回滚。
	if !strings.Contains(res[0].Message, "机群") && !strings.Contains(res[0].Message, "其它机") {
		t.Fatalf("good 机 message = %q, want 含机群级回滚说明", res[0].Message)
	}
}
