package build

import (
	"context"
	"testing"

	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
)

// fakeCache 记录 Restore/Save 调用(验缓存被接进 dag 执行;不触真实磁盘)。
type fakeCache struct {
	restoreKeys []string
	restorePath [][]string
	saveKeys    []string
	savePaths   [][]string
	hit         bool // Restore 返回的命中标志
}

func (c *fakeCache) Restore(_ context.Context, key string, paths []string, _ string) (bool, error) {
	c.restoreKeys = append(c.restoreKeys, key)
	c.restorePath = append(c.restorePath, paths)
	return c.hit, nil
}

func (c *fakeCache) Save(_ context.Context, key string, paths []string, _ string) error {
	c.saveKeys = append(c.saveKeys, key)
	c.savePaths = append(c.savePaths, paths)
	return nil
}

// 配了 cachePaths 的 script job → 执行前 Restore、成功后 Save,各一次,key 非空且确定。
func TestStageExecutorRestoresAndSavesCache(t *testing.T) {
	drv := &recordingDriver{code: 0}
	cache := &fakeCache{}
	b := newDAGTestBuilder(drv, &markerCloner{})
	b.buildCache = cache
	exec := NewStageExecutor(b, nil)

	jb := pipeline.Job{
		Name: "build", Type: pipeline.StepTypeScript,
		Config: map[string]any{"image": "node:20", "commands": "npm ci", "cachePaths": "node_modules\n.npm"},
	}
	r := &run.Run{ProjectID: "p1", Trigger: run.Trigger{Branch: "main"}}
	if err := exec(context.Background(), r, scriptStage(jb), &fakeReporter{}); err != nil {
		t.Fatalf("exec: %v", err)
	}

	if len(cache.restoreKeys) != 1 {
		t.Fatalf("Restore called %d times, want 1", len(cache.restoreKeys))
	}
	if len(cache.saveKeys) != 1 {
		t.Fatalf("Save called %d times, want 1", len(cache.saveKeys))
	}
	if cache.restoreKeys[0] == "" || cache.restoreKeys[0] != cache.saveKeys[0] {
		t.Errorf("restore/save key mismatch or empty: %q vs %q", cache.restoreKeys[0], cache.saveKeys[0])
	}
	if len(cache.savePaths[0]) != 2 || cache.savePaths[0][0] != "node_modules" || cache.savePaths[0][1] != ".npm" {
		t.Errorf("cache paths = %v, want [node_modules .npm]", cache.savePaths[0])
	}
}

// 未配 cachePaths → 不碰缓存(零开销)。
func TestStageExecutorSkipsCacheWhenUnconfigured(t *testing.T) {
	drv := &recordingDriver{code: 0}
	cache := &fakeCache{}
	b := newDAGTestBuilder(drv, &markerCloner{})
	b.buildCache = cache
	exec := NewStageExecutor(b, nil)

	if err := exec(context.Background(), &run.Run{ProjectID: "p1"},
		scriptStage(scriptJob("unit", "busybox", "echo hi")), &fakeReporter{}); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if len(cache.restoreKeys) != 0 || len(cache.saveKeys) != 0 {
		t.Errorf("cache touched without cachePaths: restore=%v save=%v", cache.restoreKeys, cache.saveKeys)
	}
}

// 脚本失败 → 不保存缓存(避免缓存半截/坏依赖)。
func TestStageExecutorNoSaveOnScriptFailure(t *testing.T) {
	drv := &recordingDriver{code: 1} // 非零退出 → 步骤失败
	cache := &fakeCache{}
	b := newDAGTestBuilder(drv, &markerCloner{})
	b.buildCache = cache
	exec := NewStageExecutor(b, nil)

	jb := pipeline.Job{
		Name: "build", Type: pipeline.StepTypeScript,
		Config: map[string]any{"image": "node:20", "commands": "false", "cachePaths": "node_modules"},
	}
	r := &run.Run{ProjectID: "p1", Trigger: run.Trigger{Branch: "main"}}
	if err := exec(context.Background(), r, scriptStage(jb), &fakeReporter{}); err == nil {
		t.Fatal("expected build failure")
	}
	if len(cache.restoreKeys) != 1 {
		t.Errorf("Restore should still run before script: %v", cache.restoreKeys)
	}
	if len(cache.saveKeys) != 0 {
		t.Errorf("Save must not run on script failure: %v", cache.saveKeys)
	}
}
