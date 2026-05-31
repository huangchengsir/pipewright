package dora

import (
	"testing"
	"time"
)

func TestComputeTrend_Empty(t *testing.T) {
	start := base
	now := base.Add(7 * 24 * time.Hour)
	pts := ComputeTrend(nil, start, now, 7)
	if len(pts) == 0 {
		t.Fatalf("expected non-empty bucket skeleton, got 0")
	}
	for _, p := range pts {
		if p.Deployments != 0 || p.ChangeFailureRate != 0 {
			t.Fatalf("empty trend bucket not zero: %+v", p)
		}
	}
}

func TestComputeTrend_ZeroBucketsOrBadWindow(t *testing.T) {
	if got := ComputeTrend(nil, base, base.Add(time.Hour), 0); len(got) != 0 {
		t.Fatalf("bucketCount 0 should yield empty, got %d", len(got))
	}
	if got := ComputeTrend(nil, base, base, 7); len(got) != 0 {
		t.Fatalf("now==start should yield empty, got %d", len(got))
	}
	if got := ComputeTrend(nil, base.Add(time.Hour), base, 7); len(got) != 0 {
		t.Fatalf("now<start should yield empty, got %d", len(got))
	}
}

func TestComputeTrend_BucketsByDay(t *testing.T) {
	// 7 天窗口、7 桶 → 每桶 1 天。在第 0 天放 2 成功 1 失败,第 3 天放 1 成功。
	start := base
	now := base.Add(6*24*time.Hour + 23*time.Hour)
	recs := []Record{
		success(0, 1*time.Hour),                // day 0
		success(0, 2*time.Hour),                // day 0
		failed(statusFailed, 0, 3*time.Hour),   // day 0
		success(0, 3*24*time.Hour+1*time.Hour), // day 3
	}
	pts := ComputeTrend(recs, start, now, 7)
	if len(pts) != 7 {
		t.Fatalf("expected 7 daily buckets, got %d", len(pts))
	}
	if pts[0].Deployments != 3 || pts[0].Successes != 2 || pts[0].Failures != 1 {
		t.Fatalf("day 0 bucket = %+v", pts[0])
	}
	if !almostEqual(pts[0].ChangeFailureRate, 1.0/3) {
		t.Fatalf("day 0 CFR = %v, want 1/3", pts[0].ChangeFailureRate)
	}
	if pts[3].Deployments != 1 || pts[3].Successes != 1 {
		t.Fatalf("day 3 bucket = %+v", pts[3])
	}
	// 其余桶应为空。
	for i, p := range pts {
		if i == 0 || i == 3 {
			continue
		}
		if p.Deployments != 0 {
			t.Fatalf("bucket %d expected empty, got %+v", i, p)
		}
	}
}

func TestComputeTrend_IgnoresNonTerminalAndOutOfWindow(t *testing.T) {
	start := base
	now := base.Add(3 * 24 * time.Hour)
	recs := []Record{
		{Status: "running", CreatedAt: base}, // 非终态 → 忽略
		{Status: statusSuccess, CreatedAt: base.Add(-48 * time.Hour), FinishedAt: at(-24 * time.Hour)}, // 窗口前 → 忽略
		success(0, 1*time.Hour), // 计入
	}
	pts := ComputeTrend(recs, start, now, 3)
	total := 0
	for _, p := range pts {
		total += p.Deployments
	}
	if total != 1 {
		t.Fatalf("expected 1 in-window terminal deployment, got %d", total)
	}
}

func TestComputeTrend_Ascending(t *testing.T) {
	pts := ComputeTrend([]Record{success(0, time.Hour)}, base, base.Add(5*24*time.Hour), 5)
	for i := 1; i < len(pts); i++ {
		if !pts[i].BucketStart.After(pts[i-1].BucketStart) {
			t.Fatalf("buckets not strictly ascending at %d: %v <= %v", i, pts[i].BucketStart, pts[i-1].BucketStart)
		}
	}
}
