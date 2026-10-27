package build

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/dagrun"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMatrixServiceNetworkUsesFullUnambiguousIdentity(t *testing.T) {
	a := stageNetworkName("12345678-run-a", "stage__go-1.21")
	b := stageNetworkName("12345678-run-a", "stage__go-1-21")
	c := stageNetworkName("12345678-run-b", "stage__go-1.21")
	if a == b || a == c || b == c {
		t.Fatalf("distinct cells/runs share service names: %q %q %q", a, b, c)
	}
	if a != stageNetworkName("12345678-run-a", "stage__go-1.21") {
		t.Fatal("network identity not stable")
	}
}
func TestServicesOnlyStageRejectsUnsupportedDriver(t *testing.T) {
	drv := &recordingDriver{code: 0}
	b := newDAGTestBuilder(drv, &markerCloner{})
	stage := pipeline.Stage{ID: "only", Name: "Only", Kind: pipeline.KindCustom, Services: []pipeline.ServiceSpec{{Name: "service", Image: "fixture"}}}
	if err := NewStageExecutor(b, nil)(context.Background(), &run.Run{ID: "run", ProjectID: "p1"}, stage, &fakeReporter{}); err == nil {
		t.Fatal("services-only unsupported stage silently passed")
	}
	if drv.callCount != 0 {
		t.Fatal("unsupported stage executed scripts")
	}
}
func TestServicesOnlyStageStartsAndCleansUp(t *testing.T) {
	drv := &svcDriver{}
	b := newDAGTestBuilder(drv, &markerCloner{})
	stage := pipeline.Stage{ID: "only", Name: "Only", Kind: pipeline.KindCustom, Services: []pipeline.ServiceSpec{{Name: "service", Image: "fixture"}}}
	if err := NewStageExecutor(b, nil)(context.Background(), &run.Run{ID: "run", ProjectID: "p1"}, stage, &fakeReporter{}); err != nil {
		t.Fatal(err)
	}
	if len(drv.ranServices) != 1 || len(drv.stopped) != 1 || drv.removedNetwork == "" {
		t.Fatalf("services-only lifecycle ignored: %+v", drv)
	}
}

// RunService must distinguish creation from start: a failed port bind can leave
// a created object, while a failed create can refer to somebody else's name.
type matrixIdentityCommander struct {
	calls                 [][]string
	failCreate, failStart bool
}

func (c *matrixIdentityCommander) Run(_ context.Context, _ string, args []string, _ string) (string, string, int, error) {
	c.calls = append(c.calls, append([]string(nil), args...))
	if len(args) > 1 && args[0] == "network" && args[1] == "create" {
		return strings.Repeat("b", 64), "", 0, nil
	}
	if len(args) > 0 && args[0] == "create" {
		if c.failCreate {
			return strings.Repeat("f", 64), "name already in use", 125, errors.New("conflict")
		}
		return strings.Repeat("a", 64), "", 0, nil
	}
	return "", "", 0, nil
}
func (c *matrixIdentityCommander) Stream(_ context.Context, _ string, args []string, _ string, _ func(string, string)) (int, error) {
	c.calls = append(c.calls, append([]string(nil), args...))
	if len(args) > 0 && args[0] == "start" && c.failStart {
		return 125, errors.New("port is already allocated")
	}
	return 0, nil
}
func TestMatrixServiceStartFailureCleansExactCreatedCID(t *testing.T) {
	command := &matrixIdentityCommander{failStart: true}
	driver := &shellDriver{bin: "docker", cmdr: command}
	if code, err := driver.CreateNetwork(context.Background(), "network", nil); code != 0 || err != nil {
		t.Fatal(code, err)
	}
	var logs []string
	code, err := driver.RunService(context.Background(), "service", "alias", "image", "network", []string{"PASSWORD=must-not-log"}, []string{"127.0.0.1:8123:8080"}, func(_, line string) { logs = append(logs, line) })
	if code == 0 || err == nil {
		t.Fatal("port conflict silently succeeded")
	}
	removed := false
	for _, args := range command.calls {
		if len(args) > 0 && args[0] == "rm" {
			if len(args) != 3 || args[2] != strings.Repeat("a", 64) {
				t.Fatalf("cleanup used a mutable name: %v", args)
			}
			removed = true
		}
	}
	if !removed {
		t.Fatal("created failed container leaked")
	}
	if strings.Contains(strings.Join(logs, "\n"), "must-not-log") {
		t.Fatal("service environment leaked into display")
	}
	if code, err := driver.RemoveNetwork(context.Background(), "network", nil); code != 0 || err != nil {
		t.Fatal(code, err)
	}
	last := command.calls[len(command.calls)-1]
	if last[len(last)-1] != strings.Repeat("b", 64) {
		t.Fatalf("network cleanup used name: %v", last)
	}
}
func TestMatrixServiceCreateConflictPreservesUnknownName(t *testing.T) {
	command := &matrixIdentityCommander{failCreate: true}
	driver := &shellDriver{bin: "docker", cmdr: command}
	_, _ = driver.CreateNetwork(context.Background(), "network", nil)
	if code, _ := driver.RunService(context.Background(), "existing", "alias", "image", "network", nil, nil, nil); code == 0 {
		t.Fatal("create conflict passed")
	}
	if _, err := driver.StopService(context.Background(), "existing", nil); err == nil {
		t.Fatal("unknown ownership accepted")
	}
	for _, args := range command.calls {
		if len(args) > 0 && args[0] == "rm" {
			t.Fatalf("unknown container deleted after failed create: %v", args)
		}
	}
}

type matrixOutputDriver struct {
	recordingDriver
	seen []string
}

func (d *matrixOutputDriver) RunToolchain(_ context.Context, image, hostDir, _ string, env []string, _ []string, _ pipeline.Resource, _ func(string, string)) (int, error) {
	values := map[string]string{}
	for _, s := range env {
		if i := strings.IndexByte(s, '='); i >= 0 {
			values[s[:i]] = s[i+1:]
		}
	}
	token := values["MATRIX_RUNTIME"] + "|" + values["MATRIX_GROUP"]
	if image == "emit" {
		return 0, os.WriteFile(filepath.Join(hostDir, pipewrightEnvFileName), []byte("CELL_TOKEN="+token+"\n"), 0o600)
	}
	if image == "read" {
		if values["CELL_TOKEN"] != token {
			return 1, errors.New("cross-cell or missing output")
		}
		d.seen = append(d.seen, token)
	}
	return 0, nil
}
func TestMatrixJobDAGCarriesOnlySameCellOutput(t *testing.T) {
	input := pipeline.Stage{ID: "matrix", Name: "Matrix", Kind: pipeline.KindBuild, Matrix: map[string][]string{"runtime": {"1.21", "1-21"}, "group": {"a", "b"}}, Jobs: []pipeline.Job{scriptJobID("a", "emit"), scriptJobID("b", "read", "a")}}
	seen := map[string]bool{}
	for _, cell := range dagrun.ExpandMatrix([]pipeline.Stage{input}) {
		driver := &matrixOutputDriver{}
		builder := newDAGTestBuilder(driver, &markerCloner{})
		if err := NewStageExecutor(builder, nil)(context.Background(), &run.Run{ID: "run-matrix-output", ProjectID: "p1"}, cell, &fakeReporter{}); err != nil {
			t.Fatal(err)
		}
		if len(driver.seen) != 1 {
			t.Fatalf("downstream did not run: %v", driver.seen)
		}
		seen[driver.seen[0]] = true
	}
	if len(seen) != 4 {
		t.Fatalf("outputs crossed matrix cells: %v", seen)
	}
}

type matrixLifecycleDriver struct {
	svcDriver
	images    []string
	failImage string
	cancel    context.CancelFunc
}

func (d *matrixLifecycleDriver) RunToolchain(ctx context.Context, image, _, _ string, _ []string, _ []string, _ pipeline.Resource, _ func(string, string)) (int, error) {
	d.images = append(d.images, image)
	if image == "body" && d.cancel != nil {
		d.cancel()
		return -1, ctx.Err()
	}
	if image == d.failImage {
		return 17, nil
	}
	return 0, nil
}
func TestMatrixCellPreservesPostAndCleanupSemantics(t *testing.T) {
	for _, mode := range []string{"success", "failure", "cancel", "post-failure", "post-only"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			drv := &matrixLifecycleDriver{}
			if mode == "failure" {
				drv.failImage = "body"
			}
			if mode == "cancel" {
				drv.cancel = cancel
			}
			if mode == "post-failure" {
				drv.failImage = "post-always"
			}
			stage := pipeline.Stage{ID: "matrix", Name: "Matrix", Kind: pipeline.KindBuild, Matrix: map[string][]string{"x": {"one"}}, Jobs: []pipeline.Job{scriptJobID("body", "body")}, Post: append([]pipeline.PostStep(nil), postSet...), Services: []pipeline.ServiceSpec{{Name: "svc", Image: "fixture"}}}
			if mode == "post-only" {
				stage.Jobs = nil
			}
			cell := dagrun.ExpandMatrix([]pipeline.Stage{stage})[0]
			rep := &fakeReporter{}
			err := NewStageExecutor(newDAGTestBuilder(drv, &markerCloner{}), nil)(ctx, &run.Run{ID: "matrix-" + mode, ProjectID: "p1"}, cell, rep)
			failed := mode == "failure" || mode == "cancel"
			if (err != nil) != failed {
				t.Fatalf("primary result changed: %v", err)
			}
			if !contains(drv.images, "post-always") || contains(drv.images, "post-ok") == failed || contains(drv.images, "post-fail") != failed {
				t.Fatalf("post condition changed: %v", drv.images)
			}
			if len(drv.ranServices) != 1 || len(drv.stopped) != 1 || drv.removedNetwork == "" {
				t.Fatalf("services not cleaned: %+v", drv)
			}
			if mode == "post-failure" && !strings.Contains(strings.Join(rep.logs, "\n"), "不影响阶段结果") {
				t.Fatalf("post failure warning lost: %v", rep.logs)
			}
		})
	}
}

type matrixCleanupExitDriver struct {
	svcDriver
	failBody, failStart bool
}

func (d *matrixCleanupExitDriver) RunToolchain(context.Context, string, string, string, []string, []string, pipeline.Resource, func(string, string)) (int, error) {
	if d.failBody {
		return 23, nil
	}
	return 0, nil
}
func (d *matrixCleanupExitDriver) RunService(ctx context.Context, name, alias, image, network string, env, ports []string, log func(string, string)) (int, error) {
	code, err := d.svcDriver.RunService(ctx, name, alias, image, network, env, ports, log)
	if d.failStart && len(d.ranServices) == 2 {
		return 125, nil
	}
	return code, err
}
func (d *matrixCleanupExitDriver) StopService(ctx context.Context, name string, log func(string, string)) (int, error) {
	_, _ = d.svcDriver.StopService(ctx, name, log)
	return 17, nil
}
func (d *matrixCleanupExitDriver) RemoveNetwork(ctx context.Context, name string, log func(string, string)) (int, error) {
	_, _ = d.svcDriver.RemoveNetwork(ctx, name, log)
	return 19, nil
}
func TestMatrixCleanupNonzeroExitWarnsWithoutChangingPrimaryResult(t *testing.T) {
	for _, mode := range []string{"success", "body-failure", "start-failure"} {
		t.Run(mode, func(t *testing.T) {
			driver := &matrixCleanupExitDriver{failBody: mode == "body-failure", failStart: mode == "start-failure"}
			stage := pipeline.Stage{
				ID: "matrix", Name: "Matrix", Kind: pipeline.KindBuild,
				Matrix:   map[string][]string{"x": {"one"}},
				Jobs:     []pipeline.Job{scriptJobID("body", "body")},
				Services: []pipeline.ServiceSpec{{Name: "first", Image: "fixture"}, {Name: "second", Image: "fixture"}},
			}
			rep := &fakeReporter{}
			err := NewStageExecutor(newDAGTestBuilder(driver, &markerCloner{}), nil)(context.Background(), &run.Run{ID: "cleanup-" + mode, ProjectID: "p1"}, dagrun.ExpandMatrix([]pipeline.Stage{stage})[0], rep)
			if (err != nil) != (mode != "success") {
				t.Fatalf("cleanup failure changed primary result: %v", err)
			}
			wantStops := 2
			if mode == "start-failure" {
				wantStops = 1
			}
			if len(driver.stopped) != wantStops || driver.removedNetwork == "" {
				t.Fatalf("cleanup did not attempt all confirmed started resources: %+v", driver)
			}
			logs := strings.Join(rep.logs, "\n")
			if !strings.Contains(logs, "best-effort, code=17") || !strings.Contains(logs, "best-effort, code=19") {
				t.Fatalf("nonzero cleanup exit was silent: %v", rep.logs)
			}
		})
	}
}
