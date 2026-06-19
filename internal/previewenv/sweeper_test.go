package previewenv

import (
	"context"
	"errors"
	"testing"
)

// fakeChecker 是注入用的假 PR 状态读取器:按 (projectID, prNumber) 返回预设状态 / 错误,
// 并记录被查询的次数,用于断言「绝不在 open/unknown/error 时回收」。
type fakeChecker struct {
	states map[int]PRState
	errs   map[int]error
	calls  int
}

func (c *fakeChecker) State(_ context.Context, _ string, prNumber int) (PRState, error) {
	c.calls++
	if c.errs != nil {
		if err, ok := c.errs[prNumber]; ok {
			return PRStateUnknown, err
		}
	}
	if c.states != nil {
		if st, ok := c.states[prNumber]; ok {
			return st, nil
		}
	}
	return PRStateUnknown, nil
}

// provisionActive 经真实 Provision 流程为某 PR 建一个 active 预览环境,返回其 routeID。
func provisionActive(t *testing.T, svc *service, projectID string, pr int) string {
	t.Helper()
	env, err := svc.Provision(context.Background(), ProvisionInput{
		ProjectID: projectID, PRNumber: pr, ServerID: "srv-1",
		UpstreamContainer: "web", UpstreamPort: 8080, HostIP: "1.2.3.4",
	})
	if err != nil || env == nil {
		t.Fatalf("Provision pr=%d: env=%v err=%v", pr, env, err)
	}
	if env.Status != StatusActive {
		t.Fatalf("Provision pr=%d: status=%q, want active", pr, env.Status)
	}
	return env.RouteID
}

func statusOf(t *testing.T, svc *service, projectID string, pr int) string {
	t.Helper()
	env, err := svc.store.getByPR(context.Background(), projectID, pr)
	if err != nil {
		t.Fatalf("getByPR pr=%d: %v", pr, err)
	}
	return env.Status
}

func TestSweepReclaim_ClosedAndMergedReclaimedOthersUntouched(t *testing.T) {
	svc, _, del := newTestSvc(t)
	ctx := context.Background()
	enablePreview(t, svc, "p1")

	// PR 1 closed, PR 2 merged → 应回收;PR 3 open, PR 4 unknown, PR 5 checker-error → 不动。
	r1 := provisionActive(t, svc, "p1", 1)
	r2 := provisionActive(t, svc, "p1", 2)
	provisionActive(t, svc, "p1", 3)
	provisionActive(t, svc, "p1", 4)
	provisionActive(t, svc, "p1", 5)

	checker := &fakeChecker{
		states: map[int]PRState{
			1: PRStateClosed,
			2: PRStateMerged,
			3: PRStateOpen,
			4: PRStateUnknown,
		},
		errs: map[int]error{5: errors.New("boom")},
	}

	n, err := svc.SweepReclaim(ctx, checker)
	if err != nil {
		t.Fatalf("SweepReclaim: %v", err)
	}
	if n != 2 {
		t.Fatalf("reclaimed = %d, want 2", n)
	}

	if got := statusOf(t, svc, "p1", 1); got != StatusReclaimed {
		t.Fatalf("pr1 (closed) status = %q, want reclaimed", got)
	}
	if got := statusOf(t, svc, "p1", 2); got != StatusReclaimed {
		t.Fatalf("pr2 (merged) status = %q, want reclaimed", got)
	}
	for _, pr := range []int{3, 4, 5} {
		if got := statusOf(t, svc, "p1", pr); got != StatusActive {
			t.Fatalf("pr%d status = %q, want active (untouched)", pr, got)
		}
	}

	// 回收的两个环境路由都被删,且仅这两个。
	deleted := map[string]bool{}
	for _, id := range del.deleted {
		deleted[id] = true
	}
	if !deleted[r1] || !deleted[r2] {
		t.Fatalf("expected routes %q,%q deleted, got %v", r1, r2, del.deleted)
	}
	if len(del.deleted) != 2 {
		t.Fatalf("expected exactly 2 route deletes, got %v", del.deleted)
	}
}

func TestSweepReclaim_DisabledProjectSkipped(t *testing.T) {
	svc, _, del := newTestSvc(t)
	ctx := context.Background()
	enablePreview(t, svc, "p1")
	provisionActive(t, svc, "p1", 1)

	// 关闭预览配置后再 sweep:即使 PR 已 closed 也不回收(配置门控)。
	if _, err := svc.SetConfig(ctx, Config{ProjectID: "p1", Enabled: false}); err != nil {
		t.Fatalf("disable: %v", err)
	}

	checker := &fakeChecker{states: map[int]PRState{1: PRStateClosed}}
	n, err := svc.SweepReclaim(ctx, checker)
	if err != nil {
		t.Fatalf("SweepReclaim: %v", err)
	}
	if n != 0 {
		t.Fatalf("reclaimed = %d, want 0 (disabled project skipped)", n)
	}
	if got := statusOf(t, svc, "p1", 1); got != StatusActive {
		t.Fatalf("disabled project env should stay active, got %q", got)
	}
	if checker.calls != 0 {
		t.Fatalf("disabled project should be skipped before checker call, calls=%d", checker.calls)
	}
	if len(del.deleted) != 0 {
		t.Fatalf("no route deletes expected, got %v", del.deleted)
	}
}

func TestSweepReclaim_AlreadyReclaimedIdempotent(t *testing.T) {
	svc, _, del := newTestSvc(t)
	ctx := context.Background()
	enablePreview(t, svc, "p1")
	provisionActive(t, svc, "p1", 1)

	// 先手动回收一次。
	if err := svc.Reclaim(ctx, "p1", 1); err != nil {
		t.Fatalf("manual Reclaim: %v", err)
	}
	deletesAfterManual := len(del.deleted)

	// 已 reclaimed 的环境不在 active 列表内 → sweep 不重复回收(即便 checker 报 closed)。
	checker := &fakeChecker{states: map[int]PRState{1: PRStateClosed}}
	n, err := svc.SweepReclaim(ctx, checker)
	if err != nil {
		t.Fatalf("SweepReclaim: %v", err)
	}
	if n != 0 {
		t.Fatalf("reclaimed = %d, want 0 (already reclaimed)", n)
	}
	if checker.calls != 0 {
		t.Fatalf("already-reclaimed env should not be in active list, checker.calls=%d", checker.calls)
	}
	if len(del.deleted) != deletesAfterManual {
		t.Fatalf("no extra route deletes expected, got %v", del.deleted)
	}
}

func TestSweepReclaim_NilCheckerNoOp(t *testing.T) {
	svc, _, _ := newTestSvc(t)
	ctx := context.Background()
	enablePreview(t, svc, "p1")
	provisionActive(t, svc, "p1", 1)

	n, err := svc.SweepReclaim(ctx, nil)
	if err != nil {
		t.Fatalf("SweepReclaim(nil): %v", err)
	}
	if n != 0 {
		t.Fatalf("nil checker should be no-op, got n=%d", n)
	}
	if got := statusOf(t, svc, "p1", 1); got != StatusActive {
		t.Fatalf("nil checker should leave env active, got %q", got)
	}
}
