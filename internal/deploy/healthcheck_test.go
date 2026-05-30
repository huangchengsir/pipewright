package deploy

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/target"
)

// TestHealthCheckCommandSuccess 验证 command 探测一次通过 → 该机 success,message 含「健康检查通过」。
func TestHealthCheckCommandSuccess(t *testing.T) {
	db := testDB(t)
	rsvc := run.New(db)
	tgt := &stubTarget{execFn: func(_ string, _ []string) (*target.ExecResult, error) {
		return &target.ExecResult{ExitCode: 0}, nil
	}}
	srv := seedServer(t, tgt, "web-1")
	runID, artID := seedSuccessRunWithArtifact(t, db, rsvc, run.ArtifactDist, "dist/shop")

	svc := New(tgt, rsvc)
	res, err := svc.Deploy(context.Background(), DeployInput{
		RunID: runID, ArtifactID: artID, ServerIDs: []string{srv.ID},
		HealthCheck: &HealthCheck{Type: HealthCheckCommand, Command: []string{"true"}},
	})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if res[0].Status != run.TargetSuccess {
		t.Fatalf("want success, got %+v", res[0])
	}
	if !strings.Contains(res[0].Message, "健康检查通过") {
		t.Fatalf("message 应含「健康检查通过」: %q", res[0].Message)
	}
	// 最后一条 Exec 应为健康探测命令(array 化的 ["true"])。
	last := tgt.calls[len(tgt.calls)-1]
	if len(last) != 1 || last[0] != "true" {
		t.Fatalf("末条命令应为健康探测 [\"true\"], got %v", last)
	}
}

// TestHealthCheckCommandFailRetriesExhausted 验证 command 探测始终非零 → 重试耗尽 → failed,
// message 人读含「健康检查失败」,且按 retries 次数重试。
func TestHealthCheckCommandFailRetriesExhausted(t *testing.T) {
	db := testDB(t)
	rsvc := run.New(db)
	probeCalls := 0
	tgt := &stubTarget{execFn: func(_ string, cmd []string) (*target.ExecResult, error) {
		if len(cmd) == 1 && cmd[0] == "false" {
			probeCalls++
			return &target.ExecResult{ExitCode: 1, Stderr: "boom"}, nil
		}
		return &target.ExecResult{ExitCode: 0}, nil
	}}
	srv := seedServer(t, tgt, "web-1")
	runID, artID := seedSuccessRunWithArtifact(t, db, rsvc, run.ArtifactDist, "dist/shop")

	svc := New(tgt, rsvc)
	res, err := svc.Deploy(context.Background(), DeployInput{
		RunID: runID, ArtifactID: artID, ServerIDs: []string{srv.ID},
		HealthCheck: &HealthCheck{
			Type: HealthCheckCommand, Command: []string{"false"},
			Retries: 3, IntervalSeconds: 0,
		},
	})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if res[0].Status != run.TargetFailed {
		t.Fatalf("want failed, got %+v", res[0])
	}
	if !strings.Contains(res[0].Message, "健康检查失败") {
		t.Fatalf("message 应含「健康检查失败」: %q", res[0].Message)
	}
	if probeCalls != 3 {
		t.Fatalf("探测应重试 3 次, got %d", probeCalls)
	}
	// run 终态据健康结果置 failed。
	rn, _ := rsvc.Get(context.Background(), runID)
	if rn.Status != run.StatusFailed {
		t.Fatalf("run 终态 = %q, want failed", rn.Status)
	}
}

// TestHealthCheckHTTPBuildsCurlArray 验证 http 探测构造 curl -fsS --max-time T url（array 化,不拼 shell）。
func TestHealthCheckHTTPBuildsCurlArray(t *testing.T) {
	db := testDB(t)
	rsvc := run.New(db)
	var probeCmd []string
	tgt := &stubTarget{execFn: func(_ string, cmd []string) (*target.ExecResult, error) {
		if len(cmd) > 0 && cmd[0] == "curl" {
			probeCmd = cmd
		}
		return &target.ExecResult{ExitCode: 0}, nil
	}}
	srv := seedServer(t, tgt, "web-1")
	runID, artID := seedSuccessRunWithArtifact(t, db, rsvc, run.ArtifactDist, "dist/shop")

	svc := New(tgt, rsvc)
	res, err := svc.Deploy(context.Background(), DeployInput{
		RunID: runID, ArtifactID: artID, ServerIDs: []string{srv.ID},
		HealthCheck: &HealthCheck{
			Type: HealthCheckHTTP, URL: "http://localhost:8080/healthz", TimeoutSeconds: 7,
		},
	})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if res[0].Status != run.TargetSuccess {
		t.Fatalf("want success, got %+v", res[0])
	}
	want := []string{"curl", "-fsS", "--max-time", "7", "http://localhost:8080/healthz"}
	if len(probeCmd) != len(want) {
		t.Fatalf("curl 命令 = %v, want %v", probeCmd, want)
	}
	for i := range want {
		if probeCmd[i] != want[i] {
			t.Fatalf("curl 命令[%d] = %q, want %q (完整 %v)", i, probeCmd[i], want[i], probeCmd)
		}
	}
}

// TestHealthCheckNoneSkipped 验证 type=none / nil → 跳过健康检查(向后兼容 4-2:命令成功即 success,
// 不追加「健康检查通过」)。
func TestHealthCheckNoneSkipped(t *testing.T) {
	db := testDB(t)
	rsvc := run.New(db)
	tgt := &stubTarget{}
	srv := seedServer(t, tgt, "web-1")
	runID, artID := seedSuccessRunWithArtifact(t, db, rsvc, run.ArtifactDist, "dist/shop")

	svc := New(tgt, rsvc)
	// nil HealthCheck。
	res, err := svc.Deploy(context.Background(), DeployInput{
		RunID: runID, ArtifactID: artID, ServerIDs: []string{srv.ID},
	})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if res[0].Status != run.TargetSuccess {
		t.Fatalf("want success, got %+v", res[0])
	}
	if strings.Contains(res[0].Message, "健康检查") {
		t.Fatalf("无健康检查时 message 不应提健康检查: %q", res[0].Message)
	}

	// type=none 显式 → 同样跳过。
	res2, _ := svc.Deploy(context.Background(), DeployInput{
		RunID: runID, ArtifactID: artID, ServerIDs: []string{srv.ID},
		HealthCheck: &HealthCheck{Type: HealthCheckNone},
	})
	if strings.Contains(res2[0].Message, "健康检查") {
		t.Fatalf("type=none 时不应提健康检查: %q", res2[0].Message)
	}
}

// TestHealthCheckDefaultsAndClamps 验证默认值与上限(retries 默认 3 / ≤20;timeout 默认 5s / ≤60s)。
func TestHealthCheckDefaultsAndClamps(t *testing.T) {
	cases := []struct {
		hc          HealthCheck
		wantRetries int
		wantTimeout time.Duration
	}{
		{HealthCheck{}, defaultHealthRetries, defaultHealthTimeout},
		{HealthCheck{Retries: -1, TimeoutSeconds: -5}, defaultHealthRetries, defaultHealthTimeout},
		{HealthCheck{Retries: 100, TimeoutSeconds: 9999}, maxHealthRetries, maxHealthTimeout},
		{HealthCheck{Retries: 5, TimeoutSeconds: 10}, 5, 10 * time.Second},
	}
	for i, c := range cases {
		if got := c.hc.retries(); got != c.wantRetries {
			t.Fatalf("case %d retries = %d, want %d", i, got, c.wantRetries)
		}
		if got := c.hc.timeout(); got != c.wantTimeout {
			t.Fatalf("case %d timeout = %v, want %v", i, got, c.wantTimeout)
		}
	}
}

// TestHealthCheckMissingConfigFails 验证 http 缺 url / command 缺命令 → 该机 failed(人读,不 panic)。
func TestHealthCheckMissingConfigFails(t *testing.T) {
	db := testDB(t)
	rsvc := run.New(db)
	tgt := &stubTarget{}
	srv := seedServer(t, tgt, "web-1")
	svc := New(tgt, rsvc)

	// 独立 run/产物(各子用例健康失败会把 run 置 failed,不能复用同一 run)。
	runID1, artID1 := seedSuccessRunWithArtifact(t, db, rsvc, run.ArtifactDist, "dist/shop")
	res, _ := svc.Deploy(context.Background(), DeployInput{
		RunID: runID1, ArtifactID: artID1, ServerIDs: []string{srv.ID},
		HealthCheck: &HealthCheck{Type: HealthCheckHTTP},
	})
	if len(res) != 1 || res[0].Status != run.TargetFailed {
		t.Fatalf("http 缺 url 应 failed, got %+v", res)
	}

	runID2, artID2 := seedSuccessRunWithArtifact(t, db, rsvc, run.ArtifactDist, "dist/shop")
	res2, _ := svc.Deploy(context.Background(), DeployInput{
		RunID: runID2, ArtifactID: artID2, ServerIDs: []string{srv.ID},
		HealthCheck: &HealthCheck{Type: HealthCheckCommand},
	})
	if len(res2) != 1 || res2[0].Status != run.TargetFailed {
		t.Fatalf("command 缺命令应 failed, got %+v", res2)
	}
}
