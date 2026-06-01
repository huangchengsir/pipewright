package deploy

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/run"
)

// 交互式分批部署:首批成功 → 其余 pending + 暂停(run 不置失败终态),首批未触达其余机。
func TestDeployInteractivePausesAfterFirstBatch(t *testing.T) {
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
		Strategy:  "interactive",
	})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if res[0].Status != run.TargetSuccess {
		t.Fatalf("first batch status = %s, want success", res[0].Status)
	}
	for i := 1; i < 3; i++ {
		if res[i].Status != run.TargetPending {
			t.Fatalf("rest target %d status = %s, want pending", i, res[i].Status)
		}
	}
	// 暂停态:其余机绝不应被执行任何部署命令。
	if rec.touched[s2.ID] || rec.touched[s3.ID] {
		t.Fatalf("暂停态其余机不应被部署;touched=%v", rec.touched)
	}
	// 暂停态:run 不置失败终态(保持成功,等续发/中止)。
	rn, _ := rsvc.Get(context.Background(), runID)
	if rn.Status != run.StatusSuccess {
		t.Fatalf("暂停态 run 状态 = %s, want success", rn.Status)
	}
	// 持久化目标里有 pending。
	targets, _ := rsvc.ListDeployTargets(context.Background(), runID)
	pending := 0
	for _, tg := range targets {
		if tg.Status == run.TargetPending {
			pending++
		}
	}
	if pending != 2 {
		t.Fatalf("want 2 pending targets, got %d", pending)
	}
}

// 续发:暂停后 ContinueDeploy → 其余机被部署 → 全成功,run 终态 success。
func TestContinueDeploy(t *testing.T) {
	db := testDB(t)
	rsvc := run.New(db)
	rec := &touchRecorder{}
	tgt := &stubTarget{execFn: rec.exec}
	s1 := seedServer(t, tgt, "web-1")
	s2 := seedServer(t, tgt, "web-2")
	s3 := seedServer(t, tgt, "web-3")
	runID, artID := seedSuccessRunWithArtifact(t, db, rsvc, run.ArtifactDist, "dist/shop.tar.gz")
	svc := New(tgt, rsvc)
	if _, err := svc.Deploy(context.Background(), DeployInput{
		RunID: runID, ArtifactID: artID, ServerIDs: []string{s1.ID, s2.ID, s3.ID}, Strategy: "interactive",
	}); err != nil {
		t.Fatalf("Deploy: %v", err)
	}

	out, err := svc.ContinueDeploy(context.Background(), ContinueInput{RunID: runID, ArtifactID: artID})
	if err != nil {
		t.Fatalf("ContinueDeploy: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("want 3 targets, got %d", len(out))
	}
	for _, r := range out {
		if r.Status != run.TargetSuccess {
			t.Fatalf("after continue, target %s status = %s, want success", r.ServerName, r.Status)
		}
	}
	if !rec.touched[s2.ID] || !rec.touched[s3.ID] {
		t.Fatalf("续发后其余机应被部署;touched=%v", rec.touched)
	}
}

// 中止:暂停后 AbortDeploy → 其余机标记已中止(failed)、不触达;首批保留成功。
func TestAbortDeploy(t *testing.T) {
	db := testDB(t)
	rsvc := run.New(db)
	rec := &touchRecorder{}
	tgt := &stubTarget{execFn: rec.exec}
	s1 := seedServer(t, tgt, "web-1")
	s2 := seedServer(t, tgt, "web-2")
	s3 := seedServer(t, tgt, "web-3")
	runID, artID := seedSuccessRunWithArtifact(t, db, rsvc, run.ArtifactDist, "dist/shop.tar.gz")
	svc := New(tgt, rsvc)
	if _, err := svc.Deploy(context.Background(), DeployInput{
		RunID: runID, ArtifactID: artID, ServerIDs: []string{s1.ID, s2.ID, s3.ID}, Strategy: "interactive",
	}); err != nil {
		t.Fatalf("Deploy: %v", err)
	}

	out, err := svc.AbortDeploy(context.Background(), AbortInput{RunID: runID})
	if err != nil {
		t.Fatalf("AbortDeploy: %v", err)
	}
	var s1res, restFailed int
	for _, r := range out {
		if r.ServerID == s1.ID && r.Status == run.TargetSuccess {
			s1res++
		}
		if (r.ServerID == s2.ID || r.ServerID == s3.ID) && r.Status == run.TargetFailed && strings.Contains(r.Message, "保留旧版本") {
			restFailed++
		}
	}
	if s1res != 1 {
		t.Fatalf("首批应保留成功;out=%+v", out)
	}
	if restFailed != 2 {
		t.Fatalf("其余应标记已中止(failed+保留旧版本);out=%+v", out)
	}
	// 中止绝不部署其余机。
	if rec.touched[s2.ID] || rec.touched[s3.ID] {
		t.Fatalf("中止时其余机不应被部署;touched=%v", rec.touched)
	}
}

// 首批失败 → 不暂停,直接中止其余(failed,不触达);run 置失败终态。
func TestDeployInteractiveFirstBatchFailAborts(t *testing.T) {
	db := testDB(t)
	rsvc := run.New(db)
	s1ID := ""
	rec := &touchRecorder{failOn: func(serverID string, cmd []string) bool {
		return serverID == s1ID && cmd[0] == "mkdir"
	}}
	tgt := &stubTarget{execFn: rec.exec}
	s1 := seedServer(t, tgt, "web-1")
	s2 := seedServer(t, tgt, "web-2")
	s3 := seedServer(t, tgt, "web-3")
	s1ID = s1.ID
	runID, artID := seedSuccessRunWithArtifact(t, db, rsvc, run.ArtifactDist, "dist/shop.tar.gz")
	svc := New(tgt, rsvc)
	res, err := svc.Deploy(context.Background(), DeployInput{
		RunID: runID, ArtifactID: artID, ServerIDs: []string{s1.ID, s2.ID, s3.ID}, Strategy: "interactive",
	})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if res[0].Status != run.TargetFailed {
		t.Fatalf("first batch status = %s, want failed", res[0].Status)
	}
	for i := 1; i < 3; i++ {
		if res[i].Status != run.TargetFailed {
			t.Fatalf("rest target %d = %s, want failed(aborted), not pending", i, res[i].Status)
		}
	}
	if rec.touched[s2.ID] || rec.touched[s3.ID] {
		t.Fatalf("首批失败后其余不应被部署;touched=%v", rec.touched)
	}
}

// 无 pending 时 Continue/Abort → ErrNoPendingTargets。
func TestContinueAbortNoPending(t *testing.T) {
	db := testDB(t)
	rsvc := run.New(db)
	rec := &touchRecorder{}
	tgt := &stubTarget{execFn: rec.exec}
	s1 := seedServer(t, tgt, "web-1")
	runID, artID := seedSuccessRunWithArtifact(t, db, rsvc, run.ArtifactDist, "dist/shop.tar.gz")
	svc := New(tgt, rsvc)
	// rolling 部署单机 → 无 pending。
	if _, err := svc.Deploy(context.Background(), DeployInput{
		RunID: runID, ArtifactID: artID, ServerIDs: []string{s1.ID}, Strategy: "rolling",
	}); err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if _, err := svc.ContinueDeploy(context.Background(), ContinueInput{RunID: runID, ArtifactID: artID}); !errors.Is(err, ErrNoPendingTargets) {
		t.Fatalf("ContinueDeploy want ErrNoPendingTargets, got %v", err)
	}
	if _, err := svc.AbortDeploy(context.Background(), AbortInput{RunID: runID}); !errors.Is(err, ErrNoPendingTargets) {
		t.Fatalf("AbortDeploy want ErrNoPendingTargets, got %v", err)
	}
}
