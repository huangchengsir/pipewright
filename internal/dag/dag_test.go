package dag

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// okRun 是恒成功的 RunFunc。
func okRun(_ context.Context, _ string) error { return nil }

// ─── 构造校验 ──────────────────────────────────────────────────────────────────

func TestNewValidation(t *testing.T) {
	cases := []struct {
		name  string
		nodes []Node
		want  error
	}{
		{"empty id", []Node{{ID: ""}}, ErrEmptyID},
		{"dup id", []Node{{ID: "a"}, {ID: "a"}}, ErrDuplicateID},
		{"unknown dep", []Node{{ID: "a", Needs: []string{"x"}}}, ErrUnknownDep},
		{"self dep", []Node{{ID: "a", Needs: []string{"a"}}}, ErrSelfDep},
		{"2-cycle", []Node{{ID: "a", Needs: []string{"b"}}, {ID: "b", Needs: []string{"a"}}}, ErrCycle},
		{"3-cycle", []Node{
			{ID: "a", Needs: []string{"c"}},
			{ID: "b", Needs: []string{"a"}},
			{ID: "c", Needs: []string{"b"}},
		}, ErrCycle},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := New(c.nodes)
			if !errors.Is(err, c.want) {
				t.Fatalf("New() err = %v, want %v", err, c.want)
			}
		})
	}
}

func TestNewValidGraph(t *testing.T) {
	g, err := New([]Node{
		{ID: "build"},
		{ID: "test", Needs: []string{"build"}},
		{ID: "deploy", Needs: []string{"test", "test"}}, // 重复 need 应被去重
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(g.nodes["deploy"].Needs) != 1 {
		t.Errorf("duplicate need not deduped: %v", g.nodes["deploy"].Needs)
	}
}

// ─── TopoOrder ────────────────────────────────────────────────────────────────

func TestTopoOrderLinear(t *testing.T) {
	g, _ := New([]Node{
		{ID: "c", Needs: []string{"b"}},
		{ID: "b", Needs: []string{"a"}},
		{ID: "a"},
	})
	got := g.TopoOrder()
	want := []string{"a", "b", "c"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("TopoOrder = %v, want %v", got, want)
	}
}

func TestTopoOrderRespectsDeps(t *testing.T) {
	// 钻石:a → {b,c} → d
	g, _ := New([]Node{
		{ID: "a"},
		{ID: "b", Needs: []string{"a"}},
		{ID: "c", Needs: []string{"a"}},
		{ID: "d", Needs: []string{"b", "c"}},
	})
	order := g.TopoOrder()
	pos := map[string]int{}
	for i, id := range order {
		pos[id] = i
	}
	if pos["a"] > pos["b"] || pos["a"] > pos["c"] || pos["b"] > pos["d"] || pos["c"] > pos["d"] {
		t.Errorf("topo order violates deps: %v", order)
	}
}

// ─── Schedule: 成功路径 ─────────────────────────────────────────────────────────

func TestScheduleLinearSuccess(t *testing.T) {
	var mu sync.Mutex
	var seq []string
	g, _ := New([]Node{
		{ID: "a"},
		{ID: "b", Needs: []string{"a"}},
		{ID: "c", Needs: []string{"b"}},
	})
	res := g.Schedule(context.Background(), func(_ context.Context, id string) error {
		mu.Lock()
		seq = append(seq, id)
		mu.Unlock()
		return nil
	}, Options{})
	if !res.Succeeded() {
		t.Fatalf("expected success, got %v", res)
	}
	if fmt.Sprint(seq) != "[a b c]" {
		t.Errorf("exec order = %v, want [a b c]", seq)
	}
}

func TestScheduleEmptyGraph(t *testing.T) {
	g, _ := New(nil)
	res := g.Schedule(context.Background(), okRun, Options{})
	if !res.Succeeded() || len(res) != 0 {
		t.Errorf("empty graph: got %v", res)
	}
}

// ─── Schedule: 失败阻断 ─────────────────────────────────────────────────────────

func TestScheduleFailBlocksDownstreamButNotSiblings(t *testing.T) {
	// a → b → c ;  a → d (独立分支)
	// b 失败:c 应 skipped,d 应照常 success。
	g, _ := New([]Node{
		{ID: "a"},
		{ID: "b", Needs: []string{"a"}},
		{ID: "c", Needs: []string{"b"}},
		{ID: "d", Needs: []string{"a"}},
	})
	var ranC atomic.Bool
	res := g.Schedule(context.Background(), func(_ context.Context, id string) error {
		if id == "b" {
			return errors.New("boom")
		}
		if id == "c" {
			ranC.Store(true)
		}
		return nil
	}, Options{})

	if res["a"].Status != StatusSuccess {
		t.Errorf("a = %v, want success", res["a"].Status)
	}
	if res["b"].Status != StatusFailed {
		t.Errorf("b = %v, want failed", res["b"].Status)
	}
	if res["c"].Status != StatusSkipped {
		t.Errorf("c = %v, want skipped", res["c"].Status)
	}
	if res["d"].Status != StatusSuccess {
		t.Errorf("d = %v, want success", res["d"].Status)
	}
	if ranC.Load() {
		t.Error("c must not have executed (upstream failed)")
	}
	if res.Succeeded() {
		t.Error("Succeeded() must be false when a node failed")
	}
}

func TestScheduleSkipPropagatesTransitively(t *testing.T) {
	// a → b → c → d ; b 失败 ⇒ c、d 都 skipped
	g, _ := New([]Node{
		{ID: "a"},
		{ID: "b", Needs: []string{"a"}},
		{ID: "c", Needs: []string{"b"}},
		{ID: "d", Needs: []string{"c"}},
	})
	res := g.Schedule(context.Background(), func(_ context.Context, id string) error {
		if id == "b" {
			return errors.New("boom")
		}
		return nil
	}, Options{})
	for _, id := range []string{"c", "d"} {
		if res[id].Status != StatusSkipped {
			t.Errorf("%s = %v, want skipped", id, res[id].Status)
		}
	}
}

func TestScheduleAllowFailureUnblocksDownstream(t *testing.T) {
	// b 失败但 AllowFailure ⇒ c 仍执行;b 自身仍记 failed。
	g, _ := New([]Node{
		{ID: "a"},
		{ID: "b", Needs: []string{"a"}, AllowFailure: true},
		{ID: "c", Needs: []string{"b"}},
	})
	var ranC atomic.Bool
	res := g.Schedule(context.Background(), func(_ context.Context, id string) error {
		if id == "b" {
			return errors.New("boom")
		}
		if id == "c" {
			ranC.Store(true)
		}
		return nil
	}, Options{})
	if res["b"].Status != StatusFailed {
		t.Errorf("b = %v, want failed (recorded)", res["b"].Status)
	}
	if res["c"].Status != StatusSuccess || !ranC.Load() {
		t.Errorf("c should run despite b's allowed failure; c=%v ran=%v", res["c"].Status, ranC.Load())
	}
}

// ─── Schedule: 并发 ─────────────────────────────────────────────────────────────

func TestScheduleParallelBarrier(t *testing.T) {
	const n = 5
	nodes := make([]Node, n)
	for i := range nodes {
		nodes[i] = Node{ID: fmt.Sprintf("n%d", i)}
	}
	g, _ := New(nodes)

	var inFlight, maxInFlight int32
	var startedCount int32
	allStarted := make(chan struct{})
	var once sync.Once

	res := g.Schedule(context.Background(), func(_ context.Context, _ string) error {
		cur := atomic.AddInt32(&inFlight, 1)
		for {
			m := atomic.LoadInt32(&maxInFlight)
			if cur <= m || atomic.CompareAndSwapInt32(&maxInFlight, m, cur) {
				break
			}
		}
		if atomic.AddInt32(&startedCount, 1) == n {
			once.Do(func() { close(allStarted) })
		}
		select {
		case <-allStarted:
		case <-time.After(2 * time.Second):
		}
		atomic.AddInt32(&inFlight, -1)
		return nil
	}, Options{MaxConcurrency: n})

	if !res.Succeeded() {
		t.Fatalf("got %v", res)
	}
	if maxInFlight < n {
		t.Errorf("max in-flight = %d, want %d (insufficient parallelism)", maxInFlight, n)
	}
}

func TestScheduleRespectsMaxConcurrency(t *testing.T) {
	const n = 8
	const limit = 3
	nodes := make([]Node, n)
	for i := range nodes {
		nodes[i] = Node{ID: fmt.Sprintf("n%d", i)}
	}
	g, _ := New(nodes)

	var inFlight, maxInFlight int32
	res := g.Schedule(context.Background(), func(_ context.Context, _ string) error {
		cur := atomic.AddInt32(&inFlight, 1)
		for {
			m := atomic.LoadInt32(&maxInFlight)
			if cur <= m || atomic.CompareAndSwapInt32(&maxInFlight, m, cur) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		atomic.AddInt32(&inFlight, -1)
		return nil
	}, Options{MaxConcurrency: limit})

	if !res.Succeeded() {
		t.Fatalf("got %v", res)
	}
	if maxInFlight > limit {
		t.Errorf("max in-flight = %d, exceeds limit %d", maxInFlight, limit)
	}
}

// ─── Schedule: 取消 ─────────────────────────────────────────────────────────────

func TestScheduleContextCanceled(t *testing.T) {
	g, _ := New([]Node{
		{ID: "a"},
		{ID: "b", Needs: []string{"a"}},
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 调度前即取消

	res := g.Schedule(ctx, func(ctx context.Context, _ string) error {
		return ctx.Err()
	}, Options{})

	// a 取消;b 因上游未成功 → 跳过或取消(均非 success)。
	if res["a"].Status == StatusSuccess {
		t.Errorf("a should not succeed under canceled ctx, got %v", res["a"].Status)
	}
	if res["b"].Status == StatusSuccess {
		t.Errorf("b should not succeed under canceled ctx, got %v", res["b"].Status)
	}
	if res.Succeeded() {
		t.Error("Succeeded() must be false under cancellation")
	}
	if len(res) != 2 {
		t.Errorf("all nodes must be terminal, got %d", len(res))
	}
}

// ─── 结果辅助 ──────────────────────────────────────────────────────────────────

func TestResultCounts(t *testing.T) {
	g, _ := New([]Node{
		{ID: "a"},
		{ID: "b", Needs: []string{"a"}},
		{ID: "c", Needs: []string{"b"}},
	})
	res := g.Schedule(context.Background(), func(_ context.Context, id string) error {
		if id == "b" {
			return errors.New("x")
		}
		return nil
	}, Options{})
	counts := res.Counts()
	if counts[StatusSuccess] != 1 || counts[StatusFailed] != 1 || counts[StatusSkipped] != 1 {
		t.Errorf("counts = %v", counts)
	}
}
