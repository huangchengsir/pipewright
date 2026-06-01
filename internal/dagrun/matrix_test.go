package dagrun

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
)

// errStageFail 是矩阵测试里用的「阶段失败」哨兵。
var errStageFail = errors.New("matrix cell boom")

// stageIDs 取展开后阶段的 id 集合(排序,便于断言)。
func stageIDs(stages []pipeline.Stage) []string {
	out := make([]string, 0, len(stages))
	for _, st := range stages {
		out = append(out, st.ID)
	}
	sort.Strings(out)
	return out
}

func TestExpandMatrixNoMatrixUnchanged(t *testing.T) {
	in := []pipeline.Stage{
		{ID: "src", Name: "源"},
		{ID: "build", Name: "构建", Needs: []string{"src"}},
	}
	out := ExpandMatrix(in)
	if len(out) != 2 {
		t.Fatalf("空 matrix 应原样保留,得 %d 阶段", len(out))
	}
	if out[1].ID != "build" || !equal(out[1].Needs, []string{"src"}) {
		t.Errorf("无 matrix 阶段不应被改写: %+v", out[1])
	}
}

func TestExpandMatrix2x1ToTwoCells(t *testing.T) {
	in := []pipeline.Stage{
		{ID: "src", Name: "源"},
		{
			ID:   "test",
			Name: "测试",
			Kind: pipeline.KindBuild,
			Matrix: map[string][]string{
				"go": {"1.21", "1.22"},
				"os": {"linux"},
			},
			Needs: []string{"src"},
			Jobs:  []pipeline.Job{{ID: "j1", Name: "run", Type: "script", Config: map[string]any{"image": "golang"}}},
		},
	}
	out := ExpandMatrix(in)
	// src + 2 个 cell。
	if len(out) != 3 {
		t.Fatalf("2×1 应展开成 src+2 cell = 3,得 %d", len(out))
	}
	got := stageIDs(out)
	want := []string{"src", "test__go-1.21_os-linux", "test__go-1.22_os-linux"}
	if !equal(got, want) {
		t.Fatalf("cell id 集 = %v, want %v", got, want)
	}
	// 每个 cell:needs 对齐(指向 src)、Matrix 清空、job 注入 MATRIX_GO/MATRIX_OS。
	for _, st := range out {
		if st.ID == "src" {
			continue
		}
		if !equal(st.Needs, []string{"src"}) {
			t.Errorf("cell %s needs 应对齐 [src],得 %v", st.ID, st.Needs)
		}
		if len(st.Matrix) != 0 {
			t.Errorf("cell %s Matrix 应清空,得 %v", st.ID, st.Matrix)
		}
		if len(st.Jobs) != 1 {
			t.Fatalf("cell %s 应含 1 job", st.ID)
		}
		env, _ := st.Jobs[0].Config["__matrixEnv"].(map[string]string)
		if env["MATRIX_OS"] != "linux" {
			t.Errorf("cell %s 应注入 MATRIX_OS=linux,得 %v", st.ID, env)
		}
		if env["MATRIX_GO"] == "" {
			t.Errorf("cell %s 应注入 MATRIX_GO,得 %v", st.ID, env)
		}
		// 镜像等原 config 应保留。
		if st.Jobs[0].Config["image"] != "golang" {
			t.Errorf("cell %s 应保留原 config image", st.ID)
		}
		// job id 追加 cell 后缀,保证全局唯一。
		if !strings.HasPrefix(st.Jobs[0].ID, "j1__") {
			t.Errorf("cell %s job id 应带 cell 后缀,得 %q", st.ID, st.Jobs[0].ID)
		}
	}
}

func TestExpandMatrixDownstreamNeedsAllCells(t *testing.T) {
	in := []pipeline.Stage{
		{ID: "src", Name: "源"},
		{
			ID:     "build",
			Name:   "构建",
			Matrix: map[string][]string{"go": {"1.21", "1.22"}},
			Needs:  []string{"src"},
		},
		{ID: "deploy", Name: "部署", Needs: []string{"build"}},
	}
	out := ExpandMatrix(in)
	var deploy *pipeline.Stage
	for i := range out {
		if out[i].ID == "deploy" {
			deploy = &out[i]
		}
	}
	if deploy == nil {
		t.Fatal("未找到 deploy 阶段")
	}
	needs := append([]string(nil), deploy.Needs...)
	sort.Strings(needs)
	want := []string{"build__go-1.21", "build__go-1.22"}
	if !equal(needs, want) {
		t.Errorf("下游 deploy.needs 应展开为所有 cell,得 %v want %v", needs, want)
	}
}

func TestExpandMatrixCartesian2x2(t *testing.T) {
	in := []pipeline.Stage{
		{ID: "src", Name: "源"},
		{
			ID:     "m",
			Name:   "矩阵",
			Matrix: map[string][]string{"a": {"1", "2"}, "b": {"x", "y"}},
		},
	}
	out := ExpandMatrix(in)
	// src + 2×2 = 5。
	if len(out) != 5 {
		t.Fatalf("2×2 应展开 4 cell,总 5 阶段,得 %d", len(out))
	}
}

// ─── Run 级:cell 间并行 + cell 失败按既有 needs/allowFailure 传播 ────────────────

func TestRunnerMatrixCellFailureBlocksDownstream(t *testing.T) {
	cfg := cfgWith(
		pipeline.Stage{ID: "src", Name: "源"},
		pipeline.Stage{
			ID:     "build",
			Name:   "构建",
			Matrix: map[string][]string{"go": {"1.21", "1.22"}},
			Needs:  []string{"src"},
		},
		pipeline.Stage{ID: "deploy", Name: "部署", Needs: []string{"build"}},
	)
	ld := &fakeLoader{cfg: cfg}

	// 执行器:cell go-1.22 失败,其余成功。
	var mu sync.Mutex
	ran := map[string]bool{}
	exec := func(_ context.Context, _ *run.Run, stage pipeline.Stage, _ StageReporter) error {
		mu.Lock()
		ran[stage.ID] = true
		mu.Unlock()
		if strings.Contains(stage.ID, "go-1.22") {
			return errStageFail
		}
		return nil
	}
	r := New(ld, WithStageExecutor(exec))
	err := r.Run(context.Background(), &run.Run{ProjectID: "p"}, newFakeSink())
	if err == nil {
		t.Fatal("有 cell 失败,整体应失败")
	}
	mu.Lock()
	defer mu.Unlock()
	// 成功的 cell 应执行;失败 cell 也执行过;deploy 因上游 cell 失败应被跳过(未执行)。
	if !ran["build__go-1.21"] || !ran["build__go-1.22"] {
		t.Errorf("两个 cell 都应被调度执行,得 %v", ran)
	}
	if ran["deploy"] {
		t.Error("上游 cell 失败,deploy 不应执行")
	}
}

func TestRunnerMatrixAllowFailureCellDoesNotBlock(t *testing.T) {
	cfg := cfgWith(
		pipeline.Stage{ID: "src", Name: "源"},
		pipeline.Stage{
			ID:           "build",
			Name:         "构建",
			Matrix:       map[string][]string{"go": {"1.21", "1.22"}},
			Needs:        []string{"src"},
			AllowFailure: true,
		},
		pipeline.Stage{ID: "deploy", Name: "部署", Needs: []string{"build"}},
	)
	ld := &fakeLoader{cfg: cfg}
	var mu sync.Mutex
	ran := map[string]bool{}
	exec := func(_ context.Context, _ *run.Run, stage pipeline.Stage, _ StageReporter) error {
		mu.Lock()
		ran[stage.ID] = true
		mu.Unlock()
		if strings.Contains(stage.ID, "go-1.22") {
			return errStageFail
		}
		return nil
	}
	r := New(ld, WithStageExecutor(exec))
	_ = r.Run(context.Background(), &run.Run{ProjectID: "p"}, newFakeSink())
	mu.Lock()
	defer mu.Unlock()
	// AllowFailure cell 失败不阻断下游:deploy 仍执行。
	if !ran["deploy"] {
		t.Errorf("allowFailure cell 失败不应阻断 deploy,得 %v", ran)
	}
}
