package dagrun

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
)

// ─── 测试替身 ──────────────────────────────────────────────────────────────────

type fakeLoader struct {
	cfg        *pipeline.Config
	gotBranch  string
	gotProject string
}

func (f *fakeLoader) Get(_ context.Context, projectID string, branch string) (*pipeline.Config, error) {
	f.gotProject = projectID
	f.gotBranch = branch
	return f.cfg, nil
}

// fakeSink 记录 StepSink 调用(dagrun 已串行化调用,这里再加锁防御)。
type fakeSink struct {
	mu      sync.Mutex
	planned []string
	running []int
	done    map[int]string
	logs    []string
}

func newFakeSink() *fakeSink { return &fakeSink{done: map[int]string{}} }

func (s *fakeSink) Plan(_ context.Context, steps []run.StepDecl) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.planned = s.planned[:0]
	for _, st := range steps {
		s.planned = append(s.planned, st.Name)
	}
	return nil
}
func (s *fakeSink) StepRunning(_ context.Context, ord int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = append(s.running, ord)
	return nil
}
func (s *fakeSink) StepDone(_ context.Context, ord int, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.done[ord] = status
	return nil
}
func (s *fakeSink) SetFailureLog(_ context.Context, _ string) error { return nil }
func (s *fakeSink) Log(_ context.Context, _ string, _ int, line string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logs = append(s.logs, line)
	return nil
}
func (s *fakeSink) EmitArtifact(_ context.Context, _ run.Artifact) error { return nil }

func cfgWith(stages ...pipeline.Stage) *pipeline.Config {
	return &pipeline.Config{Spec: pipeline.Spec{Stages: stages}}
}

func TestRunnerWhenSkipsStageAndDownstreamWithoutFailing(t *testing.T) {
	// src → deploy(when branches=[main]) → notify。 触发分支=dev:
	// deploy 条件不满足 → skipped;notify(下游)→ skipped;但整体 run **不失败**(无真失败阶段)。
	cfg := cfgWith(
		pipeline.Stage{ID: "src", Name: "源"},
		pipeline.Stage{ID: "deploy", Name: "部署", Needs: []string{"src"},
			When: pipeline.When{Branches: []string{"main"}}},
		pipeline.Stage{ID: "notify", Name: "通知", Needs: []string{"deploy"}},
	)
	var ranDeploy bool
	sink := newFakeSink()
	r := New(&fakeLoader{cfg: cfg}, WithStageExecutor(
		func(_ context.Context, _ *run.Run, st pipeline.Stage, _ StageReporter) error {
			if st.ID == "deploy" {
				ranDeploy = true
			}
			return nil
		}))
	// 触发分支 dev(不匹配 when branches=[main])。
	err := r.Run(context.Background(), &run.Run{ProjectID: "p", Trigger: run.Trigger{Type: "manual", Branch: "dev"}}, sink)
	if err != nil {
		t.Fatalf("条件跳过不应使 Run 失败,得 %v", err)
	}
	if ranDeploy {
		t.Error("deploy 阶段条件不满足,executor 不应被调用")
	}
	ord := func(name string) int {
		for i, n := range sink.planned {
			if n == name {
				return i
			}
		}
		return -1
	}
	if sink.done[ord("源")] != run.StepSuccess {
		t.Errorf("源 = %q, want success", sink.done[ord("源")])
	}
	if sink.done[ord("部署")] != run.StepSkipped {
		t.Errorf("部署 = %q, want skipped(条件不满足)", sink.done[ord("部署")])
	}
	if sink.done[ord("通知")] != run.StepSkipped {
		t.Errorf("通知 = %q, want skipped(上游被跳过)", sink.done[ord("通知")])
	}
}

func TestRunnerGateApprovedRunsStage(t *testing.T) {
	cfg := cfgWith(
		pipeline.Stage{ID: "src", Name: "源"},
		pipeline.Stage{ID: "deploy", Name: "部署", Needs: []string{"src"}, Gate: true},
	)
	var ran bool
	r := New(&fakeLoader{cfg: cfg},
		WithGate(func(_ context.Context, _ *run.Run, _ pipeline.Stage) (bool, error) {
			return true, nil // 批准
		}),
		WithStageExecutor(func(_ context.Context, _ *run.Run, st pipeline.Stage, _ StageReporter) error {
			if st.ID == "deploy" {
				ran = true
			}
			return nil
		}))
	sink := newFakeSink()
	if err := r.Run(context.Background(), &run.Run{ProjectID: "p"}, sink); err != nil {
		t.Fatalf("批准后 Run 不应失败:%v", err)
	}
	if !ran {
		t.Error("批准后 deploy 应执行")
	}
}

func TestRunnerGateRejectedFailsStage(t *testing.T) {
	cfg := cfgWith(
		pipeline.Stage{ID: "src", Name: "源"},
		pipeline.Stage{ID: "deploy", Name: "部署", Needs: []string{"src"}, Gate: true},
	)
	var ran bool
	r := New(&fakeLoader{cfg: cfg},
		WithGate(func(_ context.Context, _ *run.Run, _ pipeline.Stage) (bool, error) {
			return false, nil // 拒绝
		}),
		WithStageExecutor(func(_ context.Context, _ *run.Run, st pipeline.Stage, _ StageReporter) error {
			if st.ID == "deploy" {
				ran = true
			}
			return nil
		}))
	sink := newFakeSink()
	err := r.Run(context.Background(), &run.Run{ProjectID: "p"}, sink)
	if err == nil {
		t.Fatal("拒绝应使 Run 失败")
	}
	if ran {
		t.Error("拒绝后 deploy executor 不应被调用")
	}
	ord := func(name string) int {
		for i, n := range sink.planned {
			if n == name {
				return i
			}
		}
		return -1
	}
	if sink.done[ord("部署")] != run.StepFailed {
		t.Errorf("部署 step = %q, want failed(被拒绝)", sink.done[ord("部署")])
	}
}

func TestRunnerWhenMatchedRunsStage(t *testing.T) {
	cfg := cfgWith(
		pipeline.Stage{ID: "src", Name: "源"},
		pipeline.Stage{ID: "deploy", Name: "部署", Needs: []string{"src"},
			When: pipeline.When{Branches: []string{"main"}, Events: []string{"manual"}}},
	)
	var ranDeploy bool
	sink := newFakeSink()
	r := New(&fakeLoader{cfg: cfg}, WithStageExecutor(
		func(_ context.Context, _ *run.Run, st pipeline.Stage, _ StageReporter) error {
			if st.ID == "deploy" {
				ranDeploy = true
			}
			return nil
		}))
	if err := r.Run(context.Background(), &run.Run{ProjectID: "p", Trigger: run.Trigger{Type: "manual", Branch: "main"}}, sink); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !ranDeploy {
		t.Error("分支与触发类型均匹配,deploy 应执行")
	}
}

// ─── BuildGraph ────────────────────────────────────────────────────────────────

func TestBuildGraphLinearFallback(t *testing.T) {
	// 无 needs → 线性:a → b → c。
	stages := []pipeline.Stage{
		{ID: "a", Name: "A"}, {ID: "b", Name: "B"}, {ID: "c", Name: "C"},
	}
	g, err := BuildGraph(stages)
	if err != nil {
		t.Fatalf("BuildGraph: %v", err)
	}
	if got := g.TopoOrder(); !equal(got, []string{"a", "b", "c"}) {
		t.Errorf("topo = %v, want linear [a b c]", got)
	}
}

func TestBuildGraphExplicitNeeds(t *testing.T) {
	// a → {b,c} → d(钻石)。
	stages := []pipeline.Stage{
		{ID: "a", Name: "A"},
		{ID: "b", Name: "B", Needs: []string{"a"}},
		{ID: "c", Name: "C", Needs: []string{"a"}},
		{ID: "d", Name: "D", Needs: []string{"b", "c"}},
	}
	g, err := BuildGraph(stages)
	if err != nil {
		t.Fatalf("BuildGraph: %v", err)
	}
	order := g.TopoOrder()
	pos := map[string]int{}
	for i, id := range order {
		pos[id] = i
	}
	if pos["a"] > pos["b"] || pos["b"] > pos["d"] || pos["c"] > pos["d"] {
		t.Errorf("diamond order violated: %v", order)
	}
}

// ─── SpecLoader 分支贯穿(FR-8-12)──────────────────────────────────────────────

// Runner 须把本次运行的分支(run.Trigger.Branch)透传给 SpecLoader.Get,
// 供「流水线即代码」按运行分支取 `.pipewright.yml`。
func TestRunnerThreadsRunBranchToLoader(t *testing.T) {
	cfg := cfgWith(pipeline.Stage{ID: "src", Name: "源"})
	ld := &fakeLoader{cfg: cfg}
	r := New(ld)
	err := r.Run(context.Background(),
		&run.Run{ProjectID: "proj-x", Trigger: run.Trigger{Type: "push", Branch: "feature/x"}}, newFakeSink())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if ld.gotProject != "proj-x" {
		t.Errorf("loader 应收到 projectID=proj-x,实际 %q", ld.gotProject)
	}
	if ld.gotBranch != "feature/x" {
		t.Errorf("loader 应收到运行分支 feature/x,实际 %q", ld.gotBranch)
	}
}

// SpecLoaderFunc 把分支无关的 2 参库内 loader 适配成 branch 感知契约:忽略 branch,委托底层。
func TestSpecLoaderFuncIgnoresBranchAndDelegates(t *testing.T) {
	cfg := cfgWith(pipeline.Stage{ID: "src", Name: "源"})
	var gotProject string
	var adapter SpecLoader = SpecLoaderFunc(func(_ context.Context, projectID string) (*pipeline.Config, error) {
		gotProject = projectID
		return cfg, nil
	})
	got, err := adapter.Get(context.Background(), "proj-y", "any-branch")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != cfg {
		t.Errorf("应委托底层返回同一 cfg")
	}
	if gotProject != "proj-y" {
		t.Errorf("应透传 projectID=proj-y,实际 %q", gotProject)
	}
}

// ─── Runner.Run ────────────────────────────────────────────────────────────────

func TestRunnerLinearSuccess(t *testing.T) {
	cfg := cfgWith(
		pipeline.Stage{ID: "src", Name: "源", Jobs: []pipeline.Job{{Name: "clone", Type: "git_source"}}},
		pipeline.Stage{ID: "build", Name: "构建"},
		pipeline.Stage{ID: "deploy", Name: "部署"},
	)
	sink := newFakeSink()
	r := New(&fakeLoader{cfg: cfg})
	err := r.Run(context.Background(), &run.Run{ProjectID: "p"}, sink)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// 节点级:src 阶段的 job「clone」各占一步;无 job 的阶段用阶段名占位。
	if !equal(sink.planned, []string{"clone", "构建", "部署"}) {
		t.Errorf("planned = %v", sink.planned)
	}
	for ord := 0; ord < 3; ord++ {
		if sink.done[ord] != run.StepSuccess {
			t.Errorf("step %d = %q, want success", ord, sink.done[ord])
		}
	}
}

func TestRunnerFailBlocksDownstreamAsSkipped(t *testing.T) {
	// src → build → deploy ;build 失败 ⇒ deploy 记 skipped,Run 返回错误。
	cfg := cfgWith(
		pipeline.Stage{ID: "src", Name: "源"},
		pipeline.Stage{ID: "build", Name: "构建"},
		pipeline.Stage{ID: "deploy", Name: "部署"},
	)
	sink := newFakeSink()
	r := New(&fakeLoader{cfg: cfg}, WithStageExecutor(
		func(_ context.Context, _ *run.Run, st pipeline.Stage, _ StageReporter) error {
			if st.ID == "build" {
				return errors.New("build boom")
			}
			return nil
		}))
	err := r.Run(context.Background(), &run.Run{ProjectID: "p"}, sink)
	if err == nil {
		t.Fatal("expected Run to fail")
	}
	// ordinals: src=0, build=1, deploy=2
	if sink.done[0] != run.StepSuccess {
		t.Errorf("src = %q, want success", sink.done[0])
	}
	if sink.done[1] != run.StepFailed {
		t.Errorf("build = %q, want failed", sink.done[1])
	}
	if sink.done[2] != run.StepSkipped {
		t.Errorf("deploy = %q, want skipped", sink.done[2])
	}
	// deploy 不应被置 running(从未执行)。
	for _, ord := range sink.running {
		if ord == 2 {
			t.Error("deploy must not have been marked running")
		}
	}
}

func TestRunnerParallelSiblingsContinueOnFailure(t *testing.T) {
	// src → {a,b} ; a 失败,b 应照常成功(互不依赖)。
	cfg := cfgWith(
		pipeline.Stage{ID: "src", Name: "源"},
		pipeline.Stage{ID: "a", Name: "A", Needs: []string{"src"}},
		pipeline.Stage{ID: "b", Name: "B", Needs: []string{"src"}},
	)
	var ranB atomic.Bool
	sink := newFakeSink()
	r := New(&fakeLoader{cfg: cfg}, WithStageExecutor(
		func(_ context.Context, _ *run.Run, st pipeline.Stage, _ StageReporter) error {
			if st.ID == "a" {
				return errors.New("a boom")
			}
			if st.ID == "b" {
				ranB.Store(true)
			}
			return nil
		}))
	_ = r.Run(context.Background(), &run.Run{ProjectID: "p"}, sink)
	if !ranB.Load() {
		t.Error("sibling b should run despite a's failure")
	}
	// find ordinals by planned name
	ord := func(name string) int {
		for i, n := range sink.planned {
			if n == name {
				return i
			}
		}
		return -1
	}
	if sink.done[ord("A")] != run.StepFailed {
		t.Errorf("A = %q, want failed", sink.done[ord("A")])
	}
	if sink.done[ord("B")] != run.StepSuccess {
		t.Errorf("B = %q, want success", sink.done[ord("B")])
	}
}

func TestRunnerExecutesIndependentStagesInParallel(t *testing.T) {
	// src → {a,b,c} 三个独立分支;若串行会超时(每个等所有都开始)。
	cfg := cfgWith(
		pipeline.Stage{ID: "src", Name: "源"},
		pipeline.Stage{ID: "a", Name: "A", Needs: []string{"src"}},
		pipeline.Stage{ID: "b", Name: "B", Needs: []string{"src"}},
		pipeline.Stage{ID: "c", Name: "C", Needs: []string{"src"}},
	)
	var started, maxInFlight, inFlight int32
	allStarted := make(chan struct{})
	var once sync.Once
	const par = 3
	sink := newFakeSink()
	r := New(&fakeLoader{cfg: cfg}, WithStageExecutor(
		func(_ context.Context, _ *run.Run, st pipeline.Stage, _ StageReporter) error {
			if st.ID == "src" {
				return nil
			}
			cur := atomic.AddInt32(&inFlight, 1)
			for {
				m := atomic.LoadInt32(&maxInFlight)
				if cur <= m || atomic.CompareAndSwapInt32(&maxInFlight, m, cur) {
					break
				}
			}
			if atomic.AddInt32(&started, 1) == par {
				once.Do(func() { close(allStarted) })
			}
			select {
			case <-allStarted:
			case <-time.After(2 * time.Second):
			}
			atomic.AddInt32(&inFlight, -1)
			return nil
		}))
	if err := r.Run(context.Background(), &run.Run{ProjectID: "p"}, sink); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if maxInFlight < par {
		t.Errorf("max parallel stages = %d, want %d", maxInFlight, par)
	}
}

// equal 比较两个字符串切片。
func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
