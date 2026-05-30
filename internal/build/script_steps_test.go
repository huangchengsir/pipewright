package build

import (
	"context"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/run"
)

// imageSettingsWithSteps 复用 image 构建配置(build 走 docker build,不占 "run" 子命令),
// 并挂上若干自定义脚本步骤(脚本经 docker run 执行 → 子命令 "run",与 build 不冲突)。
func imageSettingsWithSteps(steps []pipeline.PipelineStep) *pipeline.Settings {
	st := imageSettings(nil)
	st.Steps = steps
	return st
}

// scriptRunArgs 返回所有 `docker run`(args[0]=="run")执行的命令 array(供断言脚本/挂载/env)。
func (c *fakeCommander) scriptRunArgs() [][]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out [][]string
	for _, e := range c.execs {
		if len(e.args) > 0 && e.args[0] == "run" {
			out = append(out, append([]string(nil), e.args...))
		}
	}
	return out
}

// TestScriptStepSuccessEmitsStepAndLogs 验证 script 步骤成功:计划含步骤名、置 running→success、
// 经 docker run 在隔离容器跑脚本、stdout 经 StepSink.Log 流式喂出。
func TestScriptStepSuccessEmitsStepAndLogs(t *testing.T) {
	cmdr := newFakeCommander()
	cmdr.script("build", fakeCmd{stdoutLines: []string{"built"}, exitCode: 0})
	cmdr.script("inspect", fakeCmd{stdoutLines: []string{`{"Id":"sha256:x","Size":1}`}, exitCode: 0})
	cmdr.script("run", fakeCmd{stdoutLines: []string{"=== running tests ===", "ok  pkg  0.5s"}, exitCode: 0})

	steps := []pipeline.PipelineStep{{
		ID: "s1", Name: "运行测试", Type: pipeline.StepTypeScript,
		Image: "golang:1.23", Commands: []string{"go test ./..."},
	}}
	proj := &project.Project{ID: "p1", Name: "app", RepoURL: "https://example.com/r.git"}
	b := newTestBuilder(t, cmdr, proj, imageSettingsWithSteps(steps), nil)
	b.cloner = newSuccessCloner("abc1234")

	sink := newFakeSink()
	r := &run.Run{ID: "run1", ProjectID: "p1", Trigger: run.Trigger{Commit: "abc1234"}}
	if err := b.Run(context.Background(), r, sink); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	// 计划:拉取源码、构建、运行测试(无推送)。
	if len(sink.plan) != 3 || sink.plan[2] != "运行测试" {
		t.Fatalf("plan 应含脚本步骤名, got %v", sink.plan)
	}
	if sink.done[2] != run.StepSuccess {
		t.Fatalf("脚本步骤应 success, got %v", sink.done)
	}
	logs := sink.allLogText()
	if !strings.Contains(logs, "running tests") || !strings.Contains(logs, "ok  pkg") {
		t.Fatalf("脚本 stdout 应经 StepSink.Log 流式喂出, got:\n%s", logs)
	}
	// 脚本经 docker run 在隔离容器执行:sh -c <script>,挂载工作区,-w 挂载点。
	runs := cmdr.scriptRunArgs()
	if len(runs) != 1 {
		t.Fatalf("应有 1 次 docker run, got %d", len(runs))
	}
	joined := strings.Join(runs[0], " ")
	if !strings.Contains(joined, "golang:1.23") || !strings.Contains(joined, "sh -c") {
		t.Fatalf("应经隔离容器 sh -c 跑脚本: %v", runs[0])
	}
	if !strings.Contains(joined, scriptWorkspaceMount) {
		t.Fatalf("应挂载工作区到容器: %v", runs[0])
	}
	// set -e + 用户命令进了脚本(在 sh -c 的参数里,即容器内执行,非宿主 argv 解释)。
	last := runs[0][len(runs[0])-1]
	if !strings.Contains(last, "set -e") || !strings.Contains(last, "go test ./...") {
		t.Fatalf("脚本体应含 set -e + 用户命令: %q", last)
	}
}

// TestScriptStepFailureSetsFailureLog 验证脚本非零退出 → 步骤 failed + SetFailureLog + 返回错误。
func TestScriptStepFailureSetsFailureLog(t *testing.T) {
	cmdr := newFakeCommander()
	cmdr.script("build", fakeCmd{stdoutLines: []string{"built"}, exitCode: 0})
	cmdr.script("inspect", fakeCmd{stdoutLines: []string{`{"Id":"sha256:x","Size":1}`}, exitCode: 0})
	cmdr.script("run", fakeCmd{stderrLines: []string{"FAIL  pkg  build constraints exclude all"}, exitCode: 1})

	steps := []pipeline.PipelineStep{{
		Name: "运行测试", Type: pipeline.StepTypeScript, Image: "golang:1.23", Commands: []string{"go test ./..."},
	}}
	proj := &project.Project{ID: "p1", Name: "app", RepoURL: "https://example.com/r.git"}
	b := newTestBuilder(t, cmdr, proj, imageSettingsWithSteps(steps), nil)
	b.cloner = newSuccessCloner("abc1234")

	sink := newFakeSink()
	r := &run.Run{ID: "run1", ProjectID: "p1", Trigger: run.Trigger{Commit: "abc1234"}}
	if err := b.Run(context.Background(), r, sink); err != ErrBuildFailed {
		t.Fatalf("expected ErrBuildFailed, got %v", err)
	}
	if sink.done[2] != run.StepFailed {
		t.Fatalf("脚本步骤应 failed, got %v", sink.done)
	}
	if strings.TrimSpace(sink.failureLog) == "" {
		t.Fatalf("失败应 SetFailureLog")
	}
	// 失败步骤之后不应 emit 产物。
	if len(sink.artifacts) != 0 {
		t.Fatalf("脚本步骤失败不应 emit 产物")
	}
}

// TestScriptStepSecretNotInVisibleLogs 验证脚本步骤 secret env:明文绝不进可见日志/命令回显,但确实注入容器 argv。
func TestScriptStepSecretNotInVisibleLogs(t *testing.T) {
	const secretVal = "STEP_ENV_SECRET_zzz_marker"
	cmdr := newFakeCommander()
	cmdr.script("build", fakeCmd{stdoutLines: []string{"built"}, exitCode: 0})
	cmdr.script("inspect", fakeCmd{stdoutLines: []string{`{"Id":"sha256:x","Size":1}`}, exitCode: 0})
	cmdr.script("run", fakeCmd{stdoutLines: []string{"migrating..."}, exitCode: 0})

	steps := []pipeline.PipelineStep{{
		Name: "迁移", Type: pipeline.StepTypeScript, Image: "postgres:16", Commands: []string{"psql -f m.sql"},
		Env: []pipeline.BuildVar{
			{Key: "PGPASSWORD", Secret: true, CredentialID: "cred-pg"},
			{Key: "PGHOST", Secret: false, Value: "db.local"},
		},
	}}
	proj := &project.Project{ID: "p1", Name: "app", RepoURL: "https://example.com/r.git"}
	b := newTestBuilder(t, cmdr, proj, imageSettingsWithSteps(steps), map[string]string{"cred-pg": secretVal})
	b.cloner = newSuccessCloner("abc1234")

	sink := newFakeSink()
	r := &run.Run{ID: "run1", ProjectID: "p1", Trigger: run.Trigger{Commit: "abc1234"}}
	if err := b.Run(context.Background(), r, sink); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	logs := sink.allLogText()
	if strings.Contains(logs, secretVal) {
		t.Fatalf("step secret 明文泄漏进可见日志:\n%s", logs)
	}
	// 命令回显里 -e 一律只列 key(PGPASSWORD=***):secret 绝不回显值(over-mask 非 secret 亦安全)。
	if !strings.Contains(logs, "PGPASSWORD=***") {
		t.Fatalf("secret env 回显应只列 key, got:\n%s", logs)
	}
	// 但 secret 与非 secret 都确实经 -e 注入了真实容器 argv(脚本运行需要它们)。
	args := cmdr.allArgsText()
	if !strings.Contains(args, "PGPASSWORD="+secretVal) {
		t.Fatalf("secret 应注入真实容器 argv")
	}
	if !strings.Contains(args, "PGHOST=db.local") {
		t.Fatalf("非 secret env 应注入真实容器 argv")
	}
}

// TestScriptStepCancel 验证 ctx 取消时脚本步骤阶段返回 run.ErrCanceled。
func TestScriptStepCancel(t *testing.T) {
	cmdr := newFakeCommander()
	cmdr.script("build", fakeCmd{stdoutLines: []string{"built"}, exitCode: 0})
	cmdr.script("inspect", fakeCmd{stdoutLines: []string{`{"Id":"sha256:x","Size":1}`}, exitCode: 0})
	cmdr.script("run", fakeCmd{stdoutLines: []string{"x"}, exitCode: 0})

	steps := []pipeline.PipelineStep{{
		Name: "step", Type: pipeline.StepTypeScript, Image: "node:20", Commands: []string{"echo hi"},
	}}
	proj := &project.Project{ID: "p1", Name: "app", RepoURL: "https://example.com/r.git"}
	b := newTestBuilder(t, cmdr, proj, imageSettingsWithSteps(steps), nil)
	b.cloner = newSuccessCloner("abc1234")

	// 克隆 + 构建成功后、进入脚本阶段前取消:脚本步骤循环顶部的 canceled(ctx) 命中 → ErrCanceled。
	ctx, cancel := context.WithCancel(context.Background())
	cmdr.onBuild = func() { cancel() }

	sink := newFakeSink()
	r := &run.Run{ID: "run1", ProjectID: "p1", Trigger: run.Trigger{Commit: "abc1234"}}
	if err := b.Run(ctx, r, sink); err != run.ErrCanceled {
		t.Fatalf("expected run.ErrCanceled, got %v", err)
	}
}

// TestScriptStepsOrderedExecution 验证多脚本步骤按声明顺序执行,且各步 success。
func TestScriptStepsOrderedExecution(t *testing.T) {
	cmdr := newFakeCommander()
	cmdr.script("build", fakeCmd{stdoutLines: []string{"built"}, exitCode: 0})
	cmdr.script("inspect", fakeCmd{stdoutLines: []string{`{"Id":"sha256:x","Size":1}`}, exitCode: 0})
	cmdr.script("run", fakeCmd{stdoutLines: []string{"step out"}, exitCode: 0})

	steps := []pipeline.PipelineStep{
		{Name: "lint", Type: pipeline.StepTypeScript, Image: "node:20", Commands: []string{"npm run lint"}},
		{Name: "test", Type: pipeline.StepTypeScript, Image: "node:20", Commands: []string{"npm test"}},
		{Name: "build-assets", Type: pipeline.StepTypeScript, Image: "node:20", Commands: []string{"npm run build"}},
	}
	proj := &project.Project{ID: "p1", Name: "app", RepoURL: "https://example.com/r.git"}
	b := newTestBuilder(t, cmdr, proj, imageSettingsWithSteps(steps), nil)
	b.cloner = newSuccessCloner("abc1234")

	sink := newFakeSink()
	r := &run.Run{ID: "run1", ProjectID: "p1", Trigger: run.Trigger{Commit: "abc1234"}}
	if err := b.Run(context.Background(), r, sink); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	// 计划:拉取源码、构建、lint、test、build-assets。
	want := []string{stepClone, stepBuild, "lint", "test", "build-assets"}
	if len(sink.plan) != len(want) {
		t.Fatalf("plan = %v, want %v", sink.plan, want)
	}
	for i, n := range want {
		if sink.plan[i] != n {
			t.Fatalf("plan[%d] = %q, want %q (plan=%v)", i, sink.plan[i], n, sink.plan)
		}
	}
	for _, ord := range []int{2, 3, 4} {
		if sink.done[ord] != run.StepSuccess {
			t.Fatalf("步骤 %d 应 success, got %v", ord, sink.done)
		}
	}
	// 3 次 docker run,镜像注入顺序对应步骤顺序。
	runs := cmdr.scriptRunArgs()
	if len(runs) != 3 {
		t.Fatalf("应有 3 次 docker run, got %d", len(runs))
	}
}
