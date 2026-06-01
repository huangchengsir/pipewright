package build

import (
	"context"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
)

// postRecDriver 记录每次 RunToolchain 的镜像(按调用序),并可让指定镜像返回非零(失败)。
type postRecDriver struct {
	images    []string
	failImage string
}

func (d *postRecDriver) Binary() string { return "fake" }
func (d *postRecDriver) RunToolchain(_ context.Context, image, _, _ string, _ []string, _ []string, _ pipeline.Resource, _ func(string, string)) (int, error) {
	d.images = append(d.images, image)
	if image == d.failImage {
		return 1, nil
	}
	return 0, nil
}
func (d *postRecDriver) Build(context.Context, string, string, string, []string, []string, func(string, string)) (int, error) {
	panic("Build not expected")
}
func (d *postRecDriver) Tag(context.Context, string, string, func(string, string)) (int, error) {
	panic("Tag not expected")
}
func (d *postRecDriver) Login(context.Context, string, string, string, func(string, string)) (int, error) {
	panic("Login not expected")
}
func (d *postRecDriver) Push(context.Context, string, func(string, string)) (int, error) {
	panic("Push not expected")
}
func (d *postRecDriver) InspectImage(context.Context, string) (string, int64, error) {
	panic("InspectImage not expected")
}

func postStage(jobImage string, post []pipeline.PostStep) pipeline.Stage {
	return pipeline.Stage{
		ID: "s1", Name: "构建", Kind: pipeline.KindBuild,
		Post: post,
		Jobs: []pipeline.Job{scriptJob("job", jobImage, "echo work")},
	}
}

var postSet = []pipeline.PostStep{
	{Condition: pipeline.PostAlways, Image: "post-always", Commands: []string{"echo a"}},
	{Condition: pipeline.PostOnSuccess, Image: "post-ok", Commands: []string{"echo ok"}},
	{Condition: pipeline.PostOnFailure, Image: "post-fail", Commands: []string{"echo fail"}},
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// 阶段成功 → post always + on_success 跑,on_failure 跳过。
func TestStagePostOnSuccess(t *testing.T) {
	drv := &postRecDriver{}
	b := newDAGTestBuilder(drv, &markerCloner{})
	exec := NewStageExecutor(b, nil)
	if err := exec(context.Background(), &run.Run{ProjectID: "p1"}, postStage("job-img", postSet), &fakeReporter{}); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if !contains(drv.images, "post-always") || !contains(drv.images, "post-ok") {
		t.Errorf("成功时 always+on_success 应跑;images=%v", drv.images)
	}
	if contains(drv.images, "post-fail") {
		t.Errorf("成功时 on_failure 不应跑;images=%v", drv.images)
	}
	// 顺序:job 先于 post。
	if drv.images[0] != "job-img" {
		t.Errorf("job 应先于 post;images=%v", drv.images)
	}
}

// 阶段失败 → post always + on_failure 跑,on_success 跳过;exec 返回失败(jobErr 不被 post 覆盖)。
func TestStagePostOnFailure(t *testing.T) {
	drv := &postRecDriver{failImage: "job-img"}
	b := newDAGTestBuilder(drv, &markerCloner{})
	exec := NewStageExecutor(b, nil)
	err := exec(context.Background(), &run.Run{ProjectID: "p1"}, postStage("job-img", postSet), &fakeReporter{})
	if err == nil {
		t.Fatal("job 失败,exec 应返回错误")
	}
	if !contains(drv.images, "post-always") || !contains(drv.images, "post-fail") {
		t.Errorf("失败时 always+on_failure 应跑;images=%v", drv.images)
	}
	if contains(drv.images, "post-ok") {
		t.Errorf("失败时 on_success 不应跑;images=%v", drv.images)
	}
}

// post 步骤本身失败 → best-effort,不改阶段结果(exec 仍成功)。
func TestStagePostFailureDoesNotFailStage(t *testing.T) {
	drv := &postRecDriver{failImage: "post-always"}
	b := newDAGTestBuilder(drv, &markerCloner{})
	exec := NewStageExecutor(b, nil)
	if err := exec(context.Background(), &run.Run{ProjectID: "p1"}, postStage("job-img", postSet), &fakeReporter{}); err != nil {
		t.Fatalf("post 失败不应令阶段失败,got %v", err)
	}
	if !contains(drv.images, "post-ok") {
		t.Errorf("post-always 失败后仍应继续跑后续匹配 post(post-ok);images=%v", drv.images)
	}
}

// 纯 post 阶段(无可执行 job,仅 post)也跑 post(在克隆工作区)。
func TestPostOnlyStageRuns(t *testing.T) {
	drv := &postRecDriver{}
	b := newDAGTestBuilder(drv, &markerCloner{})
	exec := NewStageExecutor(b, nil)
	stage := pipeline.Stage{ID: "s", Name: "清理", Kind: pipeline.KindCustom, Post: []pipeline.PostStep{
		{Condition: pipeline.PostAlways, Image: "cleanup", Commands: []string{"echo clean"}},
	}}
	if err := exec(context.Background(), &run.Run{ProjectID: "p1"}, stage, &fakeReporter{}); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if !contains(drv.images, "cleanup") || len(drv.images) != 1 {
		t.Errorf("纯 post 阶段应只跑 post;images=%v", drv.images)
	}
}

func TestStagePostMessageHasCondition(t *testing.T) {
	rep := &fakeReporter{}
	drv := &postRecDriver{}
	b := newDAGTestBuilder(drv, &markerCloner{})
	exec := NewStageExecutor(b, nil)
	_ = exec(context.Background(), &run.Run{ProjectID: "p1"}, postStage("job-img", postSet[:1]), rep)
	if !strings.Contains(strings.Join(rep.logs, "\n"), "阶段后置步骤") {
		t.Errorf("应有 post 步骤日志;logs=%v", rep.logs)
	}
}
