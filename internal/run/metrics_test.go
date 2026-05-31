package run

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
)

// seedRun 直接插一条 pipeline_runs(绕过状态机/worker),便于构造 DORA 测试数据。
// finished 为 nil 时存 NULL(非终态)。
func seedRun(t *testing.T, db *sql.DB, projID, status string, created time.Time, finished *time.Time) {
	t.Helper()
	var finishedVal any
	if finished != nil {
		finishedVal = finished.UTC().Format(time.RFC3339)
	}
	_, err := db.Exec(
		`INSERT INTO pipeline_runs
		   (id, project_id, status, trigger_type, trigger_branch, trigger_commit, trigger_actor,
		    created_at, started_at, finished_at)
		 VALUES (?, ?, ?, 'manual', 'main', '', 'admin', ?, NULL, ?)`,
		uuid.NewString(), projID, status, created.UTC().Format(time.RFC3339), finishedVal,
	)
	if err != nil {
		t.Fatalf("seed run: %v", err)
	}
}

func TestMetricsRecords_FilterAndProjection(t *testing.T) {
	db := testDB(t)
	svc := newService(db)
	ms := NewMetricsService(svc)
	if ms == nil {
		t.Fatal("NewMetricsService returned nil")
	}

	projA := seedProject(t, db)
	projB := seedProject(t, db)

	base := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	fin := base.Add(time.Hour)

	// projA: 1 成功(终态)+ 1 running(非终态)。
	seedRun(t, db, projA, StatusSuccess, base, &fin)
	seedRun(t, db, projA, StatusRunning, base.Add(2*time.Hour), nil)
	// projB: 1 failed(终态)。
	seedRun(t, db, projB, StatusFailed, base.Add(time.Hour), &fin)

	ctx := context.Background()

	// 全项目:3 条。
	all, err := ms.MetricsRecords(ctx, "", time.Time{})
	if err != nil {
		t.Fatalf("MetricsRecords all: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("all records = %d, want 3", len(all))
	}

	// 仅 projA:2 条,且 running 的 FinishedAt 为 nil。
	a, err := ms.MetricsRecords(ctx, projA, time.Time{})
	if err != nil {
		t.Fatalf("MetricsRecords projA: %v", err)
	}
	if len(a) != 2 {
		t.Fatalf("projA records = %d, want 2", len(a))
	}
	var sawRunningNil, sawSuccessFin bool
	for _, r := range a {
		switch r.Status {
		case StatusRunning:
			if r.FinishedAt != nil {
				t.Fatalf("running record should have nil FinishedAt")
			}
			sawRunningNil = true
		case StatusSuccess:
			if r.FinishedAt == nil {
				t.Fatalf("success record should have FinishedAt")
			}
			sawSuccessFin = true
		}
	}
	if !sawRunningNil || !sawSuccessFin {
		t.Fatalf("projection missing expected rows: running=%v success=%v", sawRunningNil, sawSuccessFin)
	}
}

func TestMetricsRecords_SinceWindow(t *testing.T) {
	db := testDB(t)
	svc := newService(db)
	ms := NewMetricsService(svc)

	proj := seedProject(t, db)
	base := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	fin := base.Add(time.Hour)

	old := base.Add(-10 * 24 * time.Hour) // 窗口前
	seedRun(t, db, proj, StatusSuccess, old, &fin)
	seedRun(t, db, proj, StatusSuccess, base, &fin)

	since := base.Add(-1 * time.Hour)
	recs, err := ms.MetricsRecords(context.Background(), "", since)
	if err != nil {
		t.Fatalf("MetricsRecords: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("windowed records = %d, want 1 (old excluded)", len(recs))
	}
}

func TestMetricsRecords_Empty(t *testing.T) {
	db := testDB(t)
	svc := newService(db)
	ms := NewMetricsService(svc)

	recs, err := ms.MetricsRecords(context.Background(), "", time.Time{})
	if err != nil {
		t.Fatalf("MetricsRecords: %v", err)
	}
	if len(recs) != 0 {
		t.Fatalf("expected empty, got %d", len(recs))
	}
}
