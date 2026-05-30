package run

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// seedRunRow 直接插一个指定状态的 run(满足 deploy_targets 外键),返回 run id。
func seedRunRow(t *testing.T, svc Service, projID, status string) string {
	t.Helper()
	s := svc.(*service)
	now := time.Now().UTC().Format(time.RFC3339)
	runID := uuid.NewString()
	if _, err := s.db.Exec(
		`INSERT INTO pipeline_runs
		   (id, project_id, status, trigger_type, trigger_branch, trigger_commit, trigger_actor,
		    resolved_environment, resolved_target_server_ids, created_at, started_at, finished_at)
		 VALUES (?, ?, ?, 'manual', 'main', 'abc', 'admin', '', '[]', ?, ?, ?)`,
		runID, projID, status, now, now, now); err != nil {
		t.Fatalf("seed run: %v", err)
	}
	return runID
}

// TestSaveAndListDeployTargets 验证多机结果持久化 + 读回(顺序、字段、finishedAt 可空)。
func TestSaveAndListDeployTargets(t *testing.T) {
	db := testDB(t)
	svc := New(db)
	projID := seedProject(t, db)
	runID := seedRunRow(t, svc, projID, StatusSuccess)

	fin := time.Now().UTC()
	in := []DeployTarget{
		{ServerID: "s1", ServerName: "web-1", Status: TargetSuccess, Message: "ok", StartedAt: time.Now().UTC().Add(-2 * time.Second), FinishedAt: &fin},
		{ServerID: "s2", ServerName: "web-2", Status: TargetFailed, Message: "unreachable", StartedAt: time.Now().UTC().Add(-1 * time.Second), FinishedAt: nil},
	}
	if err := svc.SaveDeployTargets(context.Background(), runID, in); err != nil {
		t.Fatalf("SaveDeployTargets: %v", err)
	}

	got, err := svc.ListDeployTargets(context.Background(), runID)
	if err != nil {
		t.Fatalf("ListDeployTargets: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 targets, got %d", len(got))
	}
	if got[0].ServerName != "web-1" || got[0].Status != TargetSuccess {
		t.Fatalf("target[0] mismatch: %+v", got[0])
	}
	if got[0].FinishedAt == nil {
		t.Fatalf("target[0] finishedAt 应非空")
	}
	if got[1].ServerName != "web-2" || got[1].Status != TargetFailed || got[1].FinishedAt != nil {
		t.Fatalf("target[1] mismatch: %+v", got[1])
	}
}

// TestSaveDeployTargetsInvalidStatus 验证非法 status → ErrInvalidTargetStatus(整批拒绝)。
func TestSaveDeployTargetsInvalidStatus(t *testing.T) {
	db := testDB(t)
	svc := New(db)
	projID := seedProject(t, db)
	runID := seedRunRow(t, svc, projID, StatusSuccess)

	err := svc.SaveDeployTargets(context.Background(), runID, []DeployTarget{
		{ServerID: "s1", ServerName: "w", Status: "bogus", StartedAt: time.Now().UTC()},
	})
	if err != ErrInvalidTargetStatus {
		t.Fatalf("want ErrInvalidTargetStatus, got %v", err)
	}
	// 整批拒绝:无残留。
	got, _ := svc.ListDeployTargets(context.Background(), runID)
	if len(got) != 0 {
		t.Fatalf("非法批应无残留, got %d", len(got))
	}
}

// TestListDeployTargetsEmpty 验证未部署 run → 空切片(非 nil error)。
func TestListDeployTargetsEmpty(t *testing.T) {
	db := testDB(t)
	svc := New(db)
	got, err := svc.ListDeployTargets(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("ListDeployTargets: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty, got %d", len(got))
	}
}

// TestSetDeployTerminal 验证部署后置 run 终态(覆盖成功终态)。
func TestSetDeployTerminal(t *testing.T) {
	db := testDB(t)
	svc := New(db)
	projID := seedProject(t, db)
	runID := seedRunRow(t, svc, projID, StatusSuccess)

	if err := svc.SetDeployTerminal(context.Background(), runID, StatusPartialFailed); err != nil {
		t.Fatalf("SetDeployTerminal: %v", err)
	}
	rn, _ := svc.Get(context.Background(), runID)
	if rn.Status != StatusPartialFailed {
		t.Fatalf("status = %q, want partial_failed", rn.Status)
	}

	// 非法终态 → ErrInvalidStatus。
	if err := svc.SetDeployTerminal(context.Background(), runID, StatusRunning); err != ErrInvalidStatus {
		t.Fatalf("want ErrInvalidStatus, got %v", err)
	}
	// run 不存在 → ErrNotFound。
	if err := svc.SetDeployTerminal(context.Background(), "nope", StatusSuccess); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}
