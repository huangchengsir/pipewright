package build

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/pipeline"
)

type workspaceCommander struct {
	commands      [][]string
	fail          string
	exit          int
	state         string
	cancel        context.CancelFunc
	cancelAt      string
	cancelFailure bool
	orphans       string
	t             *testing.T
}

func (c *workspaceCommander) Run(ctx context.Context, _ string, args []string, _ string) (string, string, int, error) {
	c.commands = append(c.commands, append([]string(nil), args...))
	if args[0] == c.cancelAt && c.cancel != nil {
		c.cancel()
	}
	if args[0] == "rm" && ctx.Err() != nil {
		c.t.Fatal("cleanup inherited canceled context")
	}
	if args[0] == c.fail {
		return "", "failure", 1, errors.New("fixture failure")
	}
	if args[0] == "inspect" {
		if c.state != "" {
			return c.state, "", 0, nil
		}
		return fmt.Sprintf(`{"Status":"exited","ExitCode":%d}`, c.exit), "", 0, nil
	}
	if args[0] == "ps" {
		return c.orphans, "", 0, nil
	}
	return "", "", 0, nil
}

func (c *workspaceCommander) Stream(ctx context.Context, _ string, args []string, _ string, onLine func(string, string)) (int, error) {
	c.commands = append(c.commands, append([]string(nil), args...))
	operation := args[0]
	if operation == "cp" && strings.HasPrefix(args[2], "pipewright-build-") {
		operation = "copy-back"
	}
	if c.fail == operation {
		return 1, errors.New("fixture failure")
	}
	if operation == "start" && c.cancel != nil && c.cancelAt == "" {
		c.cancel()
		return -1, ctx.Err()
	}
	if operation == "copy-back" {
		if err := os.WriteFile(filepath.Join(args[3], "app.jar"), []byte("built"), 0600); err != nil {
			c.t.Fatal(err)
		}
		if c.cancelAt == "copy-back" && c.cancel != nil {
			c.cancel()
			if c.cancelFailure {
				return 1, errors.New("process killed")
			}
		}
		if err := os.WriteFile(filepath.Join(args[3], ".pipewright-env"), []byte("RESULT=ok"), 0600); err != nil {
			c.t.Fatal(err)
		}
	}
	return 0, nil
}

func TestCopiedWorkspaceCleanupFailureIsReported(t *testing.T) {
	c := &workspaceCommander{t: t, fail: "rm"}
	d := &shellDriver{bin: "docker", cmdr: c, copyWorkspace: true}
	var logs strings.Builder
	code, err := d.RunToolchain(context.Background(), "alpine", t.TempDir(), "/workspace", nil, []string{"true"}, pipeline.Resource{}, func(_, line string) { logs.WriteString(line) })
	if code == 0 || err == nil || !strings.Contains(logs.String(), "cleanup failed") {
		t.Fatalf("code=%d err=%v log=%s", code, err, logs.String())
	}
	count := 0
	for _, cmd := range c.commands {
		if cmd[0] == "rm" {
			count++
		}
	}
	if count != 2 {
		t.Fatal(count)
	}
}

func TestCopiedWorkspaceLateCancellationPreservesOriginal(t *testing.T) {
	for _, operation := range []string{"inspect", "copy-back", "copy-back-error"} {
		t.Run(operation, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ws := t.TempDir()
			if err := os.WriteFile(filepath.Join(ws, "original"), []byte("safe"), 0600); err != nil {
				t.Fatal(err)
			}
			c := &workspaceCommander{t: t, cancel: cancel, cancelAt: operation}
			if operation == "copy-back-error" {
				c.cancelAt, c.cancelFailure = "copy-back", true
			}
			d := &shellDriver{bin: "docker", cmdr: c, copyWorkspace: true}
			_, err := d.RunToolchain(ctx, "alpine", ws, "/workspace", nil, []string{"true"}, pipeline.Resource{}, nil)
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(ws, "original")); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReplaceWorkspaceRollback(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "original"), []byte("safe"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := replaceWorkspace(ws, filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("expected rename failure")
	}
	data, err := os.ReadFile(filepath.Join(ws, "original"))
	if err != nil || string(data) != "safe" {
		t.Fatalf("rollback lost input: %q %v", data, err)
	}
}

func TestReconcileOnlyOldStoppedOwnedWorkspaces(t *testing.T) {
	id := strings.Repeat("a", 64)
	for _, state := range []struct {
		name, status string
		age          time.Duration
		remove       bool
	}{
		{"old-exited", "exited", 25 * time.Hour, true},
		{"old-created", "created", 25 * time.Hour, true},
		{"recent-created", "created", time.Hour, false},
		{"recent-exited", "exited", time.Hour, false},
		{"old-running", "running", 25 * time.Hour, false},
	} {
		t.Run(state.name, func(t *testing.T) {
			ageFrom := time.Now().Add(-state.age).UTC().Format(time.RFC3339Nano)
			c := &workspaceCommander{t: t, orphans: id + "\n--unsafe", state: fmt.Sprintf(`{"Status":%q,"FinishedAt":%q,"Created":%q}`, state.status, ageFrom, ageFrom)}
			d := &shellDriver{bin: "docker", cmdr: c}
			d.reconcileCopiedWorkspaces(nil)
			d.reconcileCopiedWorkspaces(nil)
			removed := false
			for _, cmd := range c.commands {
				if cmd[0] == "rm" {
					removed = true
					if !reflect.DeepEqual(cmd, []string{"rm", "-v", id}) {
						t.Fatal(cmd)
					}
				}
			}
			if removed != state.remove {
				t.Fatal(c.commands)
			}
			if !strings.Contains(strings.Join(c.commands[0], " "), "label="+copiedWorkspaceLabel) {
				t.Fatal("unscoped cleanup")
			}
			if !reflect.DeepEqual(c.commands[0], []string{"ps", "-aq", "--filter", "label=" + copiedWorkspaceLabel, "--filter", "status=created", "--filter", "status=exited"}) {
				t.Fatal("active containers can exhaust the scan budget")
			}
		})
	}
}

func TestReconcileRetriesAfterRateLimit(t *testing.T) {
	c := &workspaceCommander{t: t, fail: "ps"}
	d := &shellDriver{bin: "docker", cmdr: c}
	d.reconcileCopiedWorkspaces(nil)
	d.reconcileCopiedWorkspaces(nil)
	if len(c.commands) != 1 {
		t.Fatalf("reconciliation not rate limited: %v", c.commands)
	}
	d.workspaceReconcileAfter = time.Time{}
	c.fail = ""
	d.reconcileCopiedWorkspaces(nil)
	if len(c.commands) != 2 {
		t.Fatalf("reconciliation never retried: %v", c.commands)
	}
}

func TestCopiedWorkspaceLifecycle(t *testing.T) {
	for _, exit := range []int{0, 7} {
		t.Run(fmt.Sprint(exit), func(t *testing.T) {
			ws := t.TempDir()
			if err := os.WriteFile(filepath.Join(ws, "deleted.txt"), []byte("old"), 0600); err != nil {
				t.Fatal(err)
			}
			c := &workspaceCommander{t: t, exit: exit}
			d := &shellDriver{bin: "docker", cmdr: c, copyWorkspace: true}
			var logs strings.Builder
			code, err := d.RunToolchain(context.Background(), "maven:21", ws, "/workspace/backend", []string{"TOKEN=very-secret"}, []string{"sh", "-c", "mvn package"}, pipeline.Resource{CPU: "1", Memory: "512m", Network: "services"}, func(_, line string) { logs.WriteString(line) })
			if err != nil || code != exit {
				t.Fatalf("code=%d err=%v", code, err)
			}
			var operations []string
			for _, cmd := range c.commands {
				operations = append(operations, cmd[0])
			}
			if !reflect.DeepEqual(operations, []string{"ps", "create", "cp", "start", "inspect", "cp", "rm"}) {
				t.Fatal(operations)
			}
			create := strings.Join(c.commands[1], " ")
			for _, fragment := range []string{"type=volume,destination=/workspace,volume-nocopy", "-w /workspace/backend", "--cpus 1", "--memory 512m", "--network services"} {
				if !strings.Contains(create, fragment) {
					t.Fatalf("missing %q in %q", fragment, create)
				}
			}
			if strings.Contains(logs.String(), "very-secret") {
				t.Fatal("secret logged")
			}
			if _, err := os.Stat(filepath.Join(ws, "app.jar")); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(ws, "deleted.txt")); !os.IsNotExist(err) {
				t.Fatal("deleted workspace file survived")
			}
			cleanup := c.commands[len(c.commands)-1]
			if !reflect.DeepEqual(cleanup[:3], []string{"rm", "-f", "-v"}) {
				t.Fatal(cleanup)
			}
		})
	}
}

func TestCopiedWorkspaceFailures(t *testing.T) {
	for _, failure := range []string{"create", "cp", "start", "inspect", "copy-back"} {
		t.Run(failure, func(t *testing.T) {
			c := &workspaceCommander{t: t, fail: failure}
			d := &shellDriver{bin: "docker", cmdr: c, copyWorkspace: true}
			code, err := d.RunToolchain(context.Background(), "alpine", t.TempDir(), "/workspace", nil, []string{"true"}, pipeline.Resource{}, nil)
			if code == 0 && err == nil {
				t.Fatal("false success")
			}
			if c.commands[len(c.commands)-1][0] != "rm" {
				t.Fatal("container not cleaned")
			}
		})
	}
}

func TestCopiedWorkspaceCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &workspaceCommander{t: t, cancel: cancel}
	d := &shellDriver{bin: "docker", cmdr: c, copyWorkspace: true}
	_, err := d.RunToolchain(ctx, "alpine", t.TempDir(), "/workspace", nil, []string{"true"}, pipeline.Resource{}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if c.commands[len(c.commands)-1][0] != "rm" {
		t.Fatal("container not cleaned")
	}
	for _, cmd := range c.commands {
		if cmd[0] == "inspect" {
			t.Fatal("canceled result inspected")
		}
	}
}

func TestCopiedWorkspaceInvalidState(t *testing.T) {
	for _, state := range []string{"garbage", `{"Status":"running","ExitCode":0}`} {
		c := &workspaceCommander{t: t, state: state}
		d := &shellDriver{bin: "docker", cmdr: c, copyWorkspace: true}
		code, err := d.RunToolchain(context.Background(), "alpine", t.TempDir(), "/workspace", nil, []string{"true"}, pipeline.Resource{}, nil)
		if code == 0 || err == nil {
			t.Fatal("invalid state accepted")
		}
	}
}

func TestToolchainSubdirectoryKeepsMountRoot(t *testing.T) {
	c := newFakeCommander()
	d := &shellDriver{bin: "docker", cmdr: c}
	_, err := d.RunToolchain(context.Background(), "alpine", "/source", "/workspace/backend", nil, []string{"true"}, pipeline.Resource{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	args := c.execs[0].args
	if !reflect.DeepEqual(args[:7], []string{"run", "--rm", "-v", "/source:/workspace", "-w", "/workspace/backend", "alpine"}) {
		t.Fatal(args)
	}
}
