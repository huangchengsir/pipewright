package build

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/run"
)

// ─── 测试替身 ──────────────────────────────────────────────────────────────────

// fakeReporter 记录 dagrun.StageReporter 调用。
type fakeReporter struct {
	logs []string
	arts []run.Artifact
}

func (r *fakeReporter) Log(_ context.Context, _ string, line string) error {
	r.logs = append(r.logs, line)
	return nil
}
func (r *fakeReporter) EmitArtifact(_ context.Context, a run.Artifact) error {
	r.arts = append(r.arts, a)
	return nil
}

// recordingDriver 记录 RunToolchain 调用并按配置回退码/日志(其余 Driver 方法不应被调到)。
type recordingDriver struct {
	code      int
	emit      []string
	gotImage  string
	gotCmd    []string
	gotWork   string
	gotEnv    []string
	callCount int
}

func (d *recordingDriver) Binary() string { return "fake" }
func (d *recordingDriver) RunToolchain(_ context.Context, image, _, workdir string, env []string, cmd []string, onLine func(stream, line string)) (int, error) {
	d.callCount++
	d.gotImage = image
	d.gotCmd = cmd
	d.gotWork = workdir
	d.gotEnv = env
	for _, l := range d.emit {
		onLine("stdout", l)
	}
	return d.code, nil
}
func (d *recordingDriver) Build(context.Context, string, string, string, []string, []string, func(string, string)) (int, error) {
	panic("Build not expected")
}
func (d *recordingDriver) Tag(context.Context, string, string, func(string, string)) (int, error) {
	panic("Tag not expected")
}
func (d *recordingDriver) Login(context.Context, string, string, string, func(string, string)) (int, error) {
	panic("Login not expected")
}
func (d *recordingDriver) Push(context.Context, string, func(string, string)) (int, error) {
	panic("Push not expected")
}
func (d *recordingDriver) InspectImage(context.Context, string) (string, int64, error) {
	panic("InspectImage not expected")
}

// markerCloner 把一个标记文件写进工作区(替代真触网克隆)。
type markerCloner struct {
	file    string
	content string
}

func (c *markerCloner) Clone(_ context.Context, _, _, _, _, destDir string) (*CloneResolved, error) {
	if c.file != "" {
		_ = os.WriteFile(filepath.Join(destDir, c.file), []byte(c.content), 0o644)
	}
	return &CloneResolved{CommitShort: "abc1234"}, nil
}

func newDAGTestBuilder(drv Driver, cl repoCloner) *Builder {
	return &Builder{
		projects: fakeProjects{proj: &project.Project{ID: "p1", RepoURL: "https://example.com/r.git"}},
		settings: fakeSettings{settings: &pipeline.Settings{}},
		vault:    fakeVault{secrets: map[string]string{}},
		driver:   drv,
		cloner:   cl,
	}
}

func scriptStage(jobs ...pipeline.Job) pipeline.Stage {
	return pipeline.Stage{ID: "s1", Name: "构建", Kind: pipeline.KindBuild, Jobs: jobs}
}

func scriptJob(name, image, commands string) pipeline.Job {
	return pipeline.Job{Name: name, Type: pipeline.StepTypeScript, Config: map[string]any{"image": image, "commands": commands}}
}

// ─── scriptStepFromJob / splitCommands ──────────────────────────────────────────

func TestScriptStepFromJob(t *testing.T) {
	jb := pipeline.Job{
		ID: "j1", Name: "test", Type: "script",
		Config: map[string]any{"image": " node:20 ", "commands": "npm ci\n\nnpm test\n", "workDir": "app"},
	}
	step, err := scriptStepFromJob(jb)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if step.Image != "node:20" {
		t.Errorf("image = %q", step.Image)
	}
	if len(step.Commands) != 2 || step.Commands[0] != "npm ci" || step.Commands[1] != "npm test" {
		t.Errorf("commands = %v", step.Commands)
	}
	if step.WorkDir != "app" {
		t.Errorf("workDir = %q", step.WorkDir)
	}
}

func TestScriptStepFromJobMissing(t *testing.T) {
	if _, err := scriptStepFromJob(pipeline.Job{Config: map[string]any{"commands": "x"}}); err == nil {
		t.Error("expected error for missing image")
	}
	if _, err := scriptStepFromJob(pipeline.Job{Config: map[string]any{"image": "x"}}); err == nil {
		t.Error("expected error for missing commands")
	}
}

// ─── NewStageExecutor(fake driver,无 Docker)───────────────────────────────────

func TestStageExecutorRunsScriptJobInContainer(t *testing.T) {
	drv := &recordingDriver{code: 0, emit: []string{"hello from container"}}
	b := newDAGTestBuilder(drv, &markerCloner{})
	exec := NewStageExecutor(b)
	rep := &fakeReporter{}

	err := exec(context.Background(), &run.Run{ProjectID: "p1"},
		scriptStage(scriptJob("unit", "busybox", "echo hello")), rep)
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if drv.callCount != 1 {
		t.Fatalf("RunToolchain called %d times, want 1", drv.callCount)
	}
	if drv.gotImage != "busybox" {
		t.Errorf("image = %q", drv.gotImage)
	}
	// 多行命令合成 sh -c set -e 脚本。
	if len(drv.gotCmd) != 3 || drv.gotCmd[0] != "sh" || drv.gotCmd[1] != "-c" || !strings.Contains(drv.gotCmd[2], "echo hello") {
		t.Errorf("cmd = %v", drv.gotCmd)
	}
	if !strings.Contains(strings.Join(rep.logs, "\n"), "hello from container") {
		t.Errorf("container log not forwarded to reporter: %v", rep.logs)
	}
}

func TestStageExecutorInjectsRunParams(t *testing.T) {
	drv := &recordingDriver{code: 0}
	b := newDAGTestBuilder(drv, &markerCloner{})
	exec := NewStageExecutor(b)
	r := &run.Run{ProjectID: "p1", Trigger: run.Trigger{Params: map[string]string{"DEPLOY_ENV": "staging", "VER": "1.2"}}}
	if err := exec(context.Background(), r, scriptStage(scriptJob("unit", "busybox", "echo hi")), &fakeReporter{}); err != nil {
		t.Fatalf("exec: %v", err)
	}
	env := strings.Join(drv.gotEnv, " ")
	if !strings.Contains(env, "DEPLOY_ENV=staging") || !strings.Contains(env, "VER=1.2") {
		t.Errorf("run params not injected into container env: %v", drv.gotEnv)
	}
}

func TestStageExecutorNonScriptPlaceholder(t *testing.T) {
	drv := &recordingDriver{}
	b := newDAGTestBuilder(drv, &markerCloner{})
	exec := NewStageExecutor(b)
	rep := &fakeReporter{}

	stage := pipeline.Stage{ID: "s", Name: "构建", Kind: pipeline.KindBuild,
		Jobs: []pipeline.Job{{Name: "img", Type: "build_image", Config: map[string]any{}}}}
	if err := exec(context.Background(), &run.Run{ProjectID: "p1"}, stage, rep); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if drv.callCount != 0 {
		t.Error("non-script job must not invoke RunToolchain")
	}
	if !strings.Contains(strings.Join(rep.logs, "\n"), "真实执行未接入") {
		t.Errorf("expected honest placeholder log, got %v", rep.logs)
	}
}

func TestStageExecutorScriptFailureNonZero(t *testing.T) {
	drv := &recordingDriver{code: 1} // 容器非零退出
	b := newDAGTestBuilder(drv, &markerCloner{})
	exec := NewStageExecutor(b)
	if err := exec(context.Background(), &run.Run{ProjectID: "p1"},
		scriptStage(scriptJob("unit", "busybox", "false")), &fakeReporter{}); err == nil {
		t.Error("expected ErrBuildFailed on nonzero container exit")
	}
}

func TestStageExecutorInvalidScriptConfig(t *testing.T) {
	drv := &recordingDriver{}
	b := newDAGTestBuilder(drv, &markerCloner{})
	exec := NewStageExecutor(b)
	// script job 缺 image。
	bad := pipeline.Job{Name: "x", Type: "script", Config: map[string]any{"commands": "echo hi"}}
	if err := exec(context.Background(), &run.Run{ProjectID: "p1"}, scriptStage(bad), &fakeReporter{}); err == nil {
		t.Error("expected error for invalid script config")
	}
}

// ─── 真 Docker e2e(gated)─────────────────────────────────────────────────────

func TestE2EDockerStageExecutorParamsReal(t *testing.T) {
	drv := dockerReadyOrSkip(t)
	b := newDAGTestBuilder(drv, &markerCloner{})
	exec := NewStageExecutor(b)
	rep := &fakeReporter{}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// 参数化运行:容器内 echo 注入的参数,断言真值被注入并执行。
	r := &run.Run{ProjectID: "p1", Trigger: run.Trigger{Branch: "main", Params: map[string]string{"PW_PARAM": "HELLO_FROM_PARAM"}}}
	job := scriptJob("verify", "busybox", `echo "p=$PW_PARAM"`)
	if err := exec(ctx, r, scriptStage(job), rep); err != nil {
		t.Fatalf("真 Docker 参数化执行失败: %v\n日志:\n%s", err, strings.Join(rep.logs, "\n"))
	}
	if !strings.Contains(strings.Join(rep.logs, "\n"), "p=HELLO_FROM_PARAM") {
		t.Errorf("容器未读到注入的运行参数;日志:\n%s", strings.Join(rep.logs, "\n"))
	}
}

func TestE2EDockerStageExecutorReal(t *testing.T) {
	drv := dockerReadyOrSkip(t)
	b := newDAGTestBuilder(drv, &markerCloner{file: "marker.txt", content: "PIPEWRIGHT_DAG_OK"})
	exec := NewStageExecutor(b)
	rep := &fakeReporter{}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// 真容器:busybox 读回工作区里的标记文件 + echo 命令输出。
	job := scriptJob("verify", "busybox", "cat marker.txt\necho RAN-IN-CONTAINER")
	if err := exec(ctx, &run.Run{ProjectID: "p1", Trigger: run.Trigger{Branch: "main"}},
		scriptStage(job), rep); err != nil {
		t.Fatalf("真 Docker 阶段执行失败: %v\n日志:\n%s", err, strings.Join(rep.logs, "\n"))
	}
	logs := strings.Join(rep.logs, "\n")
	if !strings.Contains(logs, "PIPEWRIGHT_DAG_OK") {
		t.Errorf("容器未读到挂载的工作区文件;日志:\n%s", logs)
	}
	if !strings.Contains(logs, "RAN-IN-CONTAINER") {
		t.Errorf("容器命令未真实执行;日志:\n%s", logs)
	}
}
