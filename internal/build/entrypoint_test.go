package build

// These tests exercise production argv builders and recovery with modeled CLI/SSH.
// They do not claim that any real image ENTRYPOINT or container was executed.
import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/runretry"
	"github.com/huangchengsir/pipewright/internal/target"
)

func assertEntrypointInvocation(t *testing.T, args []string, image string, clear bool, cmd []string) {
	t.Helper()
	at := slices.Index(args, image)
	if at < 0 || !slices.Equal(args[at+1:], cmd) {
		t.Fatalf("image/command boundary changed: %q", args)
	}
	found := 0
	for i, arg := range args[:at] {
		if strings.HasPrefix(arg, "--entrypoint") {
			found++
			if arg != "--entrypoint=" || i >= at {
				t.Fatalf("not a single empty-entrypoint argv before image: %q", args)
			}
		}
	}
	if found != 0 && !clear || found != 1 && clear {
		t.Fatalf("clear=%t entrypoint flags=%d: %q", clear, found, args)
	}
}

func TestToolchainEntrypointArgvAcrossAdapters(t *testing.T) {
	for _, adapter := range []string{"docker", "nerdctl", "podman", "copy", "remote"} {
		for _, clear := range []bool{false, true} {
			t.Run(adapter+map[bool]string{false: "/inherit", true: "/clear"}[clear], func(t *testing.T) {
				image := "entrypoint-fixture:local"
				cmd := []string{"sh", "-c", "set -e\nprintf '%s\\n' 'a;$(literal)'\necho done"}
				res := pipeline.Resource{CPU: "2", Memory: "512m", Network: "test-net"}
				var logs []string
				line := func(_, s string) { logs = append(logs, s) }
				var driver Driver
				var calls func() [][]string
				switch adapter {
				case "copy":
					c := &workspaceCommander{t: t}
					driver = &shellDriver{bin: "docker", cmdr: c, copyWorkspace: true, workspaceReconcileAfter: time.Now().Add(time.Hour)}
					calls = func() [][]string { return c.commands }
				case "remote":
					c := &fakeRemoteExecer{result: &target.ExecResult{ExitCode: 0}}
					driver = NewRemoteDriver(c, "fixture-runner", "docker")
					calls = func() [][]string { return c.gotCmds }
				default:
					c := newFakeCommander()
					driver = &shellDriver{bin: adapter, cmdr: c}
					calls = c.scriptRunArgs
				}
				code, err := driver.RunToolchain(context.Background(), image, t.TempDir(), "/workspace/backend", []string{"TOKEN=test-secret"}, cmd, res, ToolchainRunOptions{ClearEntrypoint: clear}, line)
				if code != 0 || err != nil {
					t.Fatal(code, err)
				}
				found := 0
				for _, args := range calls() {
					if slices.Contains(args, image) {
						found++
						assertEntrypointInvocation(t, args, image, clear, cmd)
						for _, value := range []string{"--cpus", "2", "--memory", "512m", "--network", "test-net", "/workspace/backend", "TOKEN=test-secret"} {
							if !slices.Contains(args, value) {
								t.Fatal("resource/env/workdir lost", value, args)
							}
						}
					}
				}
				if found != 1 {
					t.Fatal("unexpected business launch count", found)
				}
				visible := strings.Join(logs, "\n")
				if strings.Contains(visible, "test-secret") || !strings.Contains(visible, "TOKEN=***") || strings.Contains(visible, "--entrypoint=") != clear {
					t.Fatal("display diverged from actual policy or leaked environment", visible)
				}
			})
		}
	}
}

func TestScriptEntrypointFailureDoesNotChangePolicyOrImplicitlyRetry(t *testing.T) {
	for _, code := range []int{2, 127} {
		t.Run(map[int]string{2: "entrypoint-error", 127: "shell-unavailable"}[code], func(t *testing.T) {
			c := newFakeCommander()
			c.script("run", fakeCmd{exitCode: code, stderrLines: []string{"fixture execution failed"}})
			b := &Builder{driver: &shellDriver{bin: "docker", cmdr: c}}
			step := pipeline.PipelineStep{Image: "cli:local", Commands: []string{"echo once"}}
			if err := b.runScriptStepWithOpts(context.Background(), newFakeSink(), 0, step, t.TempDir()); !errors.Is(err, ErrBuildFailed) {
				t.Fatal(err)
			}
			calls := c.scriptRunArgs()
			if len(calls) != 1 {
				t.Fatal("implicit fallback execution", len(calls))
			}
			assertEntrypointInvocation(t, calls[0], step.Image, true, []string{"sh", "-c", "set -e\necho once"})
		})
	}
}

type entrypointSnapshotExecution struct {
	runretry.Execution
	snap runretry.Snapshot
}

func (e entrypointSnapshotExecution) Snapshot() runretry.Snapshot { return e.snap }

func TestUnknownScriptEntrypointPolicyNeverLaunches(t *testing.T) {
	ctx := runretry.WithExecution(context.Background(), entrypointSnapshotExecution{snap: runretry.Snapshot{ScriptEntrypointMode: "future-v9"}})
	for _, remote := range []bool{false, true} {
		d := &recordingDriver{}
		b := &Builder{driver: d}
		step := pipeline.PipelineStep{Image: "cli:local", Commands: []string{"echo side-effect"}}
		var err error
		if remote {
			err = b.runScriptOnDriver(ctx, d, nil, step, t.TempDir())
		} else {
			err = b.runScriptStep(ctx, newFakeSink(), 0, step, t.TempDir())
		}
		if !errors.Is(err, runretry.ErrUnavailable) || d.callCount != 0 {
			t.Fatal("unsupported policy launched", err, d.callCount)
		}
	}
}

func TestRecoveryScriptEntrypointContractSurvivesRootAndRetry(t *testing.T) {
	for _, mode := range []string{"", runretry.ScriptEntrypointClearV1} {
		for _, remote := range []bool{false, true} {
			label := map[bool]string{false: "legacy", true: "new"}[mode != ""] + map[bool]string{false: "/local", true: "/remote"}[remote]
			t.Run(label, func(t *testing.T) {
				h := newRetryHarness(t, retrySpec(retryJob("remoteA"), retryJob("remoteB", "remoteA")), func(s *runretry.Snapshot) {
					s.ScriptEntrypointMode = mode
					if remote {
						s.RunnerRefs = map[string]string{"serverId": "frozen-runner"}
					}
				})
				var calls []ToolchainRunOptions
				var remoteFixture *retryRemoteFixture
				if remote {
					remoteFixture = &retryRemoteFixture{counts: map[string]int{}, fail: true}
					h.executor = NewStageExecutorWithRunner(h.b, nil, fakeRunnerLookup{serverID: "mutable"}, remoteFixture)
				} else {
					h.driver.fail["remoteB"] = true
					h.b.driver = &entrypointRecordingDriver{Driver: h.driver, record: func(opts ToolchainRunOptions) { calls = append(calls, opts) }}
				}
				if h.execute(t, "root") == nil {
					t.Fatal("expected original failure")
				}
				if remote {
					remoteFixture.fail = false
				} else {
					h.driver.fail["remoteB"] = false
				}
				child := h.submit(t, "root", []string{h.node(t, "root", "stage", "remoteB").Key}, nil)
				if err := h.execute(t, child); err != nil {
					t.Fatal(err)
				}
				if remote {
					for _, args := range remoteFixture.commands {
						if len(args) > 1 && args[0] == "docker" && args[1] == "run" {
							assertEntrypointInvocation(t, args, "fixture:1", mode != "", args[len(args)-3:])
							calls = append(calls, ToolchainRunOptions{ClearEntrypoint: slices.Contains(args, "--entrypoint=")})
						}
					}
				}
				if len(calls) != 3 {
					t.Fatal("successful predecessor reexecuted or retry missing", len(calls))
				}
				for _, opts := range calls {
					if opts.ClearEntrypoint != (mode != "") {
						t.Fatal("frozen execution contract changed", mode, opts)
					}
				}
			})
		}
	}
}

type entrypointRecordingDriver struct {
	Driver
	record func(ToolchainRunOptions)
}

func (d *entrypointRecordingDriver) RunToolchain(ctx context.Context, image, ws, work string, env, cmd []string, res pipeline.Resource, opts ToolchainRunOptions, line func(string, string)) (int, error) {
	d.record(opts)
	return d.Driver.RunToolchain(ctx, image, ws, work, env, cmd, res, opts, line)
}

func TestOwnedToolchainEntrypointAndServiceIsolation(t *testing.T) {
	h, ctx, d, c := resourceDriverFixture(t)
	cmd := []string{"sh", "-c", "echo once"}
	if code, err := d.RunToolchain(ctx, "cli:local", t.TempDir(), "/workspace", nil, cmd, pipeline.Resource{}, ToolchainRunOptions{ClearEntrypoint: true}, nil); err != nil || code != 0 {
		t.Fatal(code, err)
	}
	if code, err := d.CreateNetwork(ctx, "test-network", nil); err != nil || code != 0 {
		t.Fatal(code, err)
	}
	if code, err := d.RunService(ctx, "test-service", "db", "service:local", "test-network", nil, nil, nil); err != nil || code != 0 {
		t.Fatal(code, err)
	}
	launched, service := 0, 0
	for _, args := range c.calls {
		if slices.Contains(args, "cli:local") {
			launched++
			assertEntrypointInvocation(t, args, "cli:local", true, cmd)
		}
		if slices.Contains(args, "service:local") {
			service++
			assertEntrypointInvocation(t, args, "service:local", false, nil)
		}
	}
	if launched != 1 || service != 1 {
		t.Fatal("unexpected container lifecycle", launched, service)
	}
	if code, err := d.StopService(ctx, "test-service", nil); err != nil || code != 0 {
		t.Fatal(code, err)
	}
	if code, err := d.RemoveNetwork(ctx, "test-network", nil); err != nil || code != 0 {
		t.Fatal(code, err)
	}
	if drained, err := h.s.ResourcesDrained(ctx, "root"); err != nil || !drained {
		t.Fatal("resource cleanup contract changed", err)
	}
}

func TestStageScriptAndPostClearEntrypointThroughEvidenceWrapper(t *testing.T) {
	c := newFakeCommander()
	b := newDAGTestBuilder(&shellDriver{bin: "docker", cmdr: c}, &markerCloner{})
	stage := postStage("job:local", []pipeline.PostStep{{Condition: pipeline.PostAlways, Image: "post:local", Commands: []string{"echo post"}}})
	if err := NewStageExecutor(b, nil)(context.Background(), &run.Run{ProjectID: "p1"}, stage, &fakeReporter{}); err != nil {
		t.Fatal(err)
	}
	calls := c.scriptRunArgs()
	if len(calls) != 2 {
		t.Fatal("job/post launch count", len(calls))
	}
	for i, image := range []string{"job:local", "post:local"} {
		assertEntrypointInvocation(t, calls[i], image, true, calls[i][len(calls[i])-3:])
	}
}
