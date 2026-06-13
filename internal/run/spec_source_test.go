package run

import (
	"context"
	"testing"
)

// TestSpecSourceRoundTrip 证 Slice 2 持久化链路:dbStepSink.SetSpecSource 写入 pipeline_runs
// 的 spec_source* 列,service.Get 如实回读到 Run.SpecSource(仓库来源 + 库内回退两态)。
func TestSpecSourceRoundTrip(t *testing.T) {
	db := testDB(t)
	svc := New(db)
	projID := seedProject(t, db)

	rn, err := svc.Create(context.Background(), projID, Trigger{Type: TriggerManual, Branch: "feature/x"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// 新建运行默认无来源。
	got, err := svc.Get(context.Background(), rn.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.SpecSource.Source != "" {
		t.Fatalf("新建运行不应有 spec_source, got %q", got.SpecSource.Source)
	}

	sink := &dbStepSink{svc: svc.(*service), runID: rn.ID}

	// 仓库来源写入 → 回读 repo + ref + file。
	if err := sink.SetSpecSource(context.Background(),
		SpecSource{Source: SpecSourceRepo, Ref: "feature/x", File: ".pipewright.yml"}); err != nil {
		t.Fatalf("SetSpecSource(repo): %v", err)
	}
	got, err = svc.Get(context.Background(), rn.ID)
	if err != nil {
		t.Fatalf("Get(repo): %v", err)
	}
	if got.SpecSource.Source != SpecSourceRepo || got.SpecSource.Ref != "feature/x" || got.SpecSource.File != ".pipewright.yml" {
		t.Fatalf("仓库来源回读错误: %+v", got.SpecSource)
	}

	// 库内回退写入 → 回读 stored + fallback(ref/file 清空)。
	if err := sink.SetSpecSource(context.Background(),
		SpecSource{Source: SpecSourceStored, Fallback: "invalid_yaml"}); err != nil {
		t.Fatalf("SetSpecSource(stored): %v", err)
	}
	got, err = svc.Get(context.Background(), rn.ID)
	if err != nil {
		t.Fatalf("Get(stored): %v", err)
	}
	if got.SpecSource.Source != SpecSourceStored || got.SpecSource.Fallback != "invalid_yaml" {
		t.Fatalf("库内回退来源回读错误: %+v", got.SpecSource)
	}
}
