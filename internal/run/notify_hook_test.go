package run

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestNotifyHookInvokedOnTerminal 验证 run 落终态后注入的 best-effort 通知钩子被调用:
//   - 失败 run → 钩子收到 (runID, "failed");成功 run → (runID, "success")。
//   - 钩子 panic 须被 recover,绝不连累 run 终态。
func TestNotifyHookInvokedOnTerminal(t *testing.T) {
	db := testDB(t)
	svc := New(db)

	var mu sync.Mutex
	got := map[string]string{} // runID → finalStatus
	hook := func(_ context.Context, runID, finalStatus string) {
		mu.Lock()
		got[runID] = finalStatus
		mu.Unlock()
		panic("notify hook boom") // 须被 recover,不连累 run 终态
	}
	pool := NewWorkerPool(svc, WithRunner(&branchRunner{}), WithNotifyHook(hook))
	pool.Start()
	t.Cleanup(func() { pool.Stop(context.Background()) })

	projID := seedProject(t, db)
	failRun, _ := svc.Create(context.Background(), projID, Trigger{Type: TriggerManual, Branch: "fail"})
	okRun, _ := svc.Create(context.Background(), projID, Trigger{Type: TriggerManual, Branch: "ok"})

	gotFail := waitForStatus(t, svc, failRun.ID, StatusFailed)
	waitForStatus(t, svc, okRun.ID, StatusSuccess)
	if gotFail.Status != StatusFailed {
		t.Fatalf("失败 run 终态应为 failed(钩子 panic 不应破坏)")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		f := got[failRun.ID]
		o := got[okRun.ID]
		mu.Unlock()
		if f != "" && o != "" {
			if f != StatusFailed {
				t.Fatalf("失败 run 钩子 finalStatus 应为 failed, 得 %q", f)
			}
			if o != StatusSuccess {
				t.Fatalf("成功 run 钩子 finalStatus 应为 success, 得 %q", o)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("通知钩子应在两个 run 终态后均被调用,得 %+v", got)
}

// TestNoNotifyHookNoPanic 验证未注入通知钩子时,run 正常跑完终态(不 panic)。
func TestNoNotifyHookNoPanic(t *testing.T) {
	db := testDB(t)
	svc := New(db)
	pool := NewWorkerPool(svc, WithRunner(&branchRunner{}))
	pool.Start()
	t.Cleanup(func() { pool.Stop(context.Background()) })

	projID := seedProject(t, db)
	r, _ := svc.Create(context.Background(), projID, Trigger{Type: TriggerManual, Branch: "ok"})
	waitForStatus(t, svc, r.ID, StatusSuccess)
}
