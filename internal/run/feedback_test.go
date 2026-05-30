package run

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// newFeedbackFixture 建临时库 + 一个项目 + 一个 run,返回 FeedbackService、run.Service、runID。
func newFeedbackFixture(t *testing.T) (FeedbackService, Service, string) {
	t.Helper()
	db := testDB(t)
	projID := seedProject(t, db)
	rsvc := New(db)
	r, err := rsvc.Create(context.Background(), projID, Trigger{Type: TriggerManual, Branch: "main"})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	return NewFeedbackService(db), rsvc, r.ID
}

func TestSaveFeedbackUpsert(t *testing.T) {
	fb, _, runID := newFeedbackFixture(t)
	ctx := context.Background()

	// 首次 👍。
	f1, err := fb.SaveFeedback(ctx, SaveFeedbackInput{RunID: runID, Verdict: VerdictUp})
	if err != nil {
		t.Fatalf("SaveFeedback up: %v", err)
	}
	if f1.Verdict != VerdictUp || f1.CorrectRootCause != "" {
		t.Fatalf("up 反馈异常: %+v", f1)
	}

	// 同 run 改判 👎 + 根因 → 覆盖(upsert),不新增。
	f2, err := fb.SaveFeedback(ctx, SaveFeedbackInput{RunID: runID, Verdict: VerdictDown, CorrectRootCause: "真正根因是磁盘满"})
	if err != nil {
		t.Fatalf("SaveFeedback down: %v", err)
	}
	if f2.Verdict != VerdictDown || f2.CorrectRootCause != "真正根因是磁盘满" {
		t.Fatalf("down 覆盖异常: %+v", f2)
	}
	if f2.ID != f1.ID {
		t.Fatalf("upsert 应同一行,id 变了: %s → %s", f1.ID, f2.ID)
	}

	stats, err := fb.GetStats(ctx)
	if err != nil {
		t.Fatalf("GetStats: %v", err)
	}
	if stats.TotalFeedback != 1 || stats.ThumbsDown != 1 || stats.ThumbsUp != 0 {
		t.Fatalf("upsert 后应只 1 条 down: %+v", stats)
	}
}

func TestSaveFeedbackInvalidVerdict(t *testing.T) {
	fb, _, runID := newFeedbackFixture(t)
	if _, err := fb.SaveFeedback(context.Background(), SaveFeedbackInput{RunID: runID, Verdict: "maybe"}); err != ErrInvalidVerdict {
		t.Fatalf("非法 verdict 应 ErrInvalidVerdict, got %v", err)
	}
}

func TestSaveFeedbackRunNotFound(t *testing.T) {
	fb, _, _ := newFeedbackFixture(t)
	if _, err := fb.SaveFeedback(context.Background(), SaveFeedbackInput{RunID: uuid.NewString(), Verdict: VerdictUp}); err != ErrNotFound {
		t.Fatalf("不存在 run 应 ErrNotFound(FK), got %v", err)
	}
}

func TestSaveFeedbackUpDropsRootCause(t *testing.T) {
	fb, _, runID := newFeedbackFixture(t)
	f, err := fb.SaveFeedback(context.Background(), SaveFeedbackInput{RunID: runID, Verdict: VerdictUp, CorrectRootCause: "不该保留"})
	if err != nil {
		t.Fatalf("SaveFeedback: %v", err)
	}
	if f.CorrectRootCause != "" {
		t.Fatalf("👍 不应携带根因: %q", f.CorrectRootCause)
	}
}

func TestSaveFeedbackTruncatesLongRootCause(t *testing.T) {
	fb, _, runID := newFeedbackFixture(t)
	long := make([]rune, MaxCorrectRootCauseLen+500)
	for i := range long {
		long[i] = '甲'
	}
	f, err := fb.SaveFeedback(context.Background(), SaveFeedbackInput{RunID: runID, Verdict: VerdictDown, CorrectRootCause: string(long)})
	if err != nil {
		t.Fatalf("SaveFeedback: %v", err)
	}
	if got := len([]rune(f.CorrectRootCause)); got != MaxCorrectRootCauseLen {
		t.Fatalf("根因应截断到 %d 字符, got %d", MaxCorrectRootCauseLen, got)
	}
}

func TestGetStatsEmpty(t *testing.T) {
	fb, _, _ := newFeedbackFixture(t)
	stats, err := fb.GetStats(context.Background())
	if err != nil {
		t.Fatalf("GetStats empty: %v", err)
	}
	if stats.TotalFeedback != 0 || stats.ThumbsUp != 0 || stats.ThumbsDown != 0 {
		t.Fatalf("空库计数应全 0: %+v", stats)
	}
	if stats.Accuracy != nil {
		t.Fatalf("空库 accuracy 应 nil, got %v", *stats.Accuracy)
	}
	if stats.RecentTrend == nil || stats.RecentCorrections == nil {
		t.Fatalf("空库 trend/corrections 应空切片非 nil: %+v", stats)
	}
	if len(stats.RecentTrend) != 0 || len(stats.RecentCorrections) != 0 {
		t.Fatalf("空库 trend/corrections 应为空: %+v", stats)
	}
}

func TestGetStatsAccuracyAndCorrections(t *testing.T) {
	db := testDB(t)
	projID := seedProject(t, db)
	rsvc := New(db)
	fb := NewFeedbackService(db)
	ctx := context.Background()

	// 3 个 run:2 个 👍、1 个 👎(附根因)。
	var downRun string
	for i := 0; i < 3; i++ {
		r, err := rsvc.Create(ctx, projID, Trigger{Type: TriggerManual, Branch: "main"})
		if err != nil {
			t.Fatalf("create run: %v", err)
		}
		if i < 2 {
			if _, err := fb.SaveFeedback(ctx, SaveFeedbackInput{RunID: r.ID, Verdict: VerdictUp}); err != nil {
				t.Fatalf("save up: %v", err)
			}
		} else {
			downRun = r.ID
			if _, err := fb.SaveFeedback(ctx, SaveFeedbackInput{RunID: r.ID, Verdict: VerdictDown, CorrectRootCause: "OOM 杀进程"}); err != nil {
				t.Fatalf("save down: %v", err)
			}
		}
	}

	stats, err := fb.GetStats(ctx)
	if err != nil {
		t.Fatalf("GetStats: %v", err)
	}
	if stats.TotalFeedback != 3 || stats.ThumbsUp != 2 || stats.ThumbsDown != 1 {
		t.Fatalf("计数异常: %+v", stats)
	}
	if stats.Accuracy == nil || *stats.Accuracy < 0.66 || *stats.Accuracy > 0.67 {
		t.Fatalf("accuracy 应 ≈0.666: %v", stats.Accuracy)
	}
	if len(stats.RecentCorrections) != 1 || stats.RecentCorrections[0].RunID != downRun {
		t.Fatalf("corrections 应只 1 条且为 downRun: %+v", stats.RecentCorrections)
	}
	if stats.RecentCorrections[0].CorrectRootCause != "OOM 杀进程" {
		t.Fatalf("correction 根因异常: %q", stats.RecentCorrections[0].CorrectRootCause)
	}
	if len(stats.RecentTrend) == 0 {
		t.Fatalf("trend 不应空")
	}
}
