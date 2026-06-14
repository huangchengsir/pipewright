package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/storetest"
	_ "modernc.org/sqlite"
)

func fptr(f float64) *float64 { return &f }

// TestRecordAndQueryRange:写入多台多时刻样本 → 按服务器 + since 升序回读,null 指标保持 nil。
func TestRecordAndQueryRange(t *testing.T) {
	svc := New(storetest.OpenDB(t))
	ctx := context.Background()
	base := time.Date(2026, 6, 14, 10, 0, 0, 0, time.UTC)

	err := svc.Record(ctx, []Sample{
		{ServerID: "s1", CPU: fptr(10), Memory: fptr(40), Disk: fptr(50), At: base},
		{ServerID: "s2", CPU: fptr(90), Memory: nil, Disk: fptr(88), At: base}, // s2 内存不可得
	})
	if err != nil {
		t.Fatalf("record1: %v", err)
	}
	if err := svc.Record(ctx, []Sample{
		{ServerID: "s1", CPU: fptr(20), Memory: fptr(42), Disk: fptr(51), At: base.Add(time.Minute)},
	}); err != nil {
		t.Fatalf("record2: %v", err)
	}

	// s1:两点,升序。
	pts, err := svc.QueryRange(ctx, "s1", base.Add(-time.Hour))
	if err != nil {
		t.Fatalf("query s1: %v", err)
	}
	if len(pts) != 2 {
		t.Fatalf("want 2 points, got %d", len(pts))
	}
	if !pts[0].At.Before(pts[1].At) {
		t.Fatalf("points must be ascending")
	}
	if pts[0].CPU == nil || *pts[0].CPU != 10 || pts[1].CPU == nil || *pts[1].CPU != 20 {
		t.Fatalf("cpu series wrong: %+v %+v", pts[0].CPU, pts[1].CPU)
	}

	// s2:内存 null 保持 nil。
	s2, _ := svc.QueryRange(ctx, "s2", base.Add(-time.Hour))
	if len(s2) != 1 || s2[0].Memory != nil || s2[0].CPU == nil || *s2[0].CPU != 90 {
		t.Fatalf("s2 null-handling wrong: %+v", s2)
	}

	// since 过滤:只取第二个点之后。
	recent, _ := svc.QueryRange(ctx, "s1", base.Add(30*time.Second))
	if len(recent) != 1 || *recent[0].CPU != 20 {
		t.Fatalf("since filter wrong: %+v", recent)
	}
}

// TestSweep:删除保留窗口外样本,窗口内保留。
func TestSweep(t *testing.T) {
	svc := New(storetest.OpenDB(t))
	ctx := context.Background()
	now := time.Now().UTC()
	_ = svc.Record(ctx, []Sample{
		{ServerID: "s1", CPU: fptr(1), At: now.Add(-48 * time.Hour)}, // 旧
		{ServerID: "s1", CPU: fptr(2), At: now.Add(-1 * time.Hour)},  // 新
	})
	n, err := svc.Sweep(ctx, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if n != 1 {
		t.Fatalf("want 1 deleted, got %d", n)
	}
	left, _ := svc.QueryRange(ctx, "s1", now.Add(-72*time.Hour))
	if len(left) != 1 || *left[0].CPU != 2 {
		t.Fatalf("sweep kept wrong rows: %+v", left)
	}
}

// TestRecordEmptyNoop:空样本 → 不报错、不写。
func TestRecordEmptyNoop(t *testing.T) {
	svc := New(storetest.OpenDB(t))
	if err := svc.Record(context.Background(), nil); err != nil {
		t.Fatalf("empty record should noop: %v", err)
	}
}
