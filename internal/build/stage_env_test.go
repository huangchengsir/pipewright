package build

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
)

func TestCaptureStageEnv(t *testing.T) {
	ws := t.TempDir()
	body := "VERSION=1.2.3\n# comment\n\nBAD LINE NO EQ\n=novalue\nTAG=v9\n9bad=x\n"
	if err := os.WriteFile(filepath.Join(ws, pipewrightEnvFileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got := captureStageEnv(context.Background(), nil, ws)
	want := map[string]string{"VERSION": "1.2.3", "TAG": "v9"}
	if len(got) != len(want) {
		t.Fatalf("got %d vars %+v, want %d", len(got), got, len(want))
	}
	for _, v := range got {
		if v.Value != want[v.Key] {
			t.Errorf("%s=%q, want %q", v.Key, v.Value, want[v.Key])
		}
		if v.Secret {
			t.Errorf("captured var %s should be plain, not secret", v.Key)
		}
	}
	// 即捕即清:文件应被移除。
	if _, err := os.Stat(filepath.Join(ws, pipewrightEnvFileName)); !os.IsNotExist(err) {
		t.Error("env file should be removed after capture")
	}
}

func TestCaptureStageEnvNoFile(t *testing.T) {
	if got := captureStageEnv(context.Background(), nil, t.TempDir()); got != nil {
		t.Fatalf("no file → nil, got %+v", got)
	}
}

func TestPipewrightEnvVar(t *testing.T) {
	v := pipewrightEnvVar()
	if v.Key != "PIPEWRIGHT_ENV" || !strings.HasPrefix(v.Value, scriptWorkspaceMount) || v.Secret {
		t.Fatalf("unexpected PIPEWRIGHT_ENV var: %+v", v)
	}
}

// envEmitDriver:job1(首调)向工作区写 .pipewright_env(模拟 job 产出),job2(次调)记录收到的 env。
type envEmitDriver struct {
	calls   int
	job2Env []string
}

func (d *envEmitDriver) Binary() string { return "fake" }
func (d *envEmitDriver) RunToolchain(_ context.Context, _, hostDir, _ string, env []string, _ []string, _ pipeline.Resource, _ func(string, string)) (int, error) {
	d.calls++
	if d.calls == 1 {
		_ = os.WriteFile(filepath.Join(hostDir, pipewrightEnvFileName), []byte("VERSION=1.2.3\n"), 0o644)
	} else {
		d.job2Env = append([]string(nil), env...)
	}
	return 0, nil
}
func (d *envEmitDriver) Build(context.Context, string, string, string, []string, []string, func(string, string)) (int, error) {
	panic("Build not expected")
}
func (d *envEmitDriver) Tag(context.Context, string, string, func(string, string)) (int, error) {
	panic("Tag not expected")
}
func (d *envEmitDriver) Login(context.Context, string, string, string, func(string, string)) (int, error) {
	panic("Login not expected")
}
func (d *envEmitDriver) Push(context.Context, string, func(string, string)) (int, error) {
	panic("Push not expected")
}
func (d *envEmitDriver) InspectImage(context.Context, string) (string, int64, error) {
	panic("InspectImage not expected")
}

// 证步骤输出跨 job 传递:job1 写 VERSION → job2 的容器 env 收到 VERSION=1.2.3。
func TestStageEnvCarriesToDownstreamJob(t *testing.T) {
	drv := &envEmitDriver{}
	b := newDAGTestBuilder(drv, &markerCloner{})
	exec := NewStageExecutor(b, nil)
	stage := scriptStage(
		scriptJob("build", "busybox", "echo build"),
		scriptJob("deploy", "busybox", "echo deploy"),
	)
	if err := exec(context.Background(), &run.Run{ProjectID: "p1"}, stage, &fakeReporter{}); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if drv.calls != 2 {
		t.Fatalf("want 2 job runs, got %d", drv.calls)
	}
	joined := strings.Join(drv.job2Env, " ")
	if !strings.Contains(joined, "VERSION=1.2.3") {
		t.Errorf("下游 job 未收到上游输出 VERSION;job2 env=%v", drv.job2Env)
	}
	if !strings.Contains(joined, "PIPEWRIGHT_ENV=") {
		t.Errorf("job 未注入 PIPEWRIGHT_ENV;job2 env=%v", drv.job2Env)
	}
}
