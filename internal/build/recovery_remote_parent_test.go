package build

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/runretry"
	"github.com/huangchengsir/pipewright/internal/target"
)

// This filesystem model rejects mkdir(child) when the runner's shared parent
// has never existed. SQLite execution/checkpoint/cleanup are the real domains;
// Docker and SSH are modeled here, not claimed as physical acceptance.
type retryParentRemoteFixture struct {
	*retryRemoteFixture
	parent        string
	parentCreates int
}

func (f *retryParentRemoteFixture) Exec(ctx context.Context, server string, args []string) (*target.ExecResult, error) {
	if len(args) > 5 && args[0] == "sh" && args[3] == "pw-remote-workspace-create" {
		if f.parent == "missing" {
			f.parent = "safe"
			f.parentCreates++
		}
		if f.parent != "safe" {
			return &target.ExecResult{ExitCode: 1}, nil
		}
		child := args[5]
		if _, exists := f.workspaces[child]; exists {
			return &target.ExecResult{ExitCode: 1}, nil
		}
		return f.retryRemoteFixture.Exec(ctx, server, []string{"mkdir", "-m", "700", "--", child})
	}
	if len(args) > 3 && args[0] == "mkdir" && f.parent == "missing" {
		return &target.ExecResult{ExitCode: 1, Stderr: "parent does not exist"}, nil
	}
	return f.retryRemoteFixture.Exec(ctx, server, args)
}
func TestRecoveryRemoteFreshRunnerCreatesParent(t *testing.T) {
	h := newRetryHarness(t, retrySpec(retryJob("remoteA")), func(s *runretry.Snapshot) { s.RunnerRefs = map[string]string{"serverId": "fresh-runner"} })
	remote := &retryParentRemoteFixture{retryRemoteFixture: &retryRemoteFixture{counts: map[string]int{}}, parent: "missing"}
	h.executor = NewStageExecutorWithRunner(h.b, nil, fakeRunnerLookup{serverID: "mutable-runner"}, remote)
	if err := h.execute(t, "root"); err != nil {
		t.Fatalf("fresh remote runner did not initialize a usable parent before the child workspace: %v; actual remote executions=%v", err, remote.counts)
	}
	if remote.parentCreates != 1 || remote.parent != "safe" || remote.counts["remoteA"] != 1 || len(remote.workspaces) != 0 {
		t.Fatalf("parent/child lifecycle mismatch: creates=%d state=%s executions=%v ownedChildren=%d", remote.parentCreates, remote.parent, remote.counts, len(remote.workspaces))
	}
	if drained, err := h.s.ResourcesDrained(context.Background(), "root"); err != nil || !drained {
		t.Fatal("fresh runner resources did not drain", err)
	}
}

func TestRecoveryRemoteRefusesUnsafeParentWithoutExecuting(t *testing.T) {
	for _, state := range []string{"symlink", "regular_file", "group_writable", "other_writable", "foreign_owner"} {
		t.Run(state, func(t *testing.T) {
			h := newRetryHarness(t, retrySpec(retryJob("remoteA")), func(s *runretry.Snapshot) { s.RunnerRefs = map[string]string{"serverId": "unsafe-runner"} })
			remote := &retryParentRemoteFixture{retryRemoteFixture: &retryRemoteFixture{counts: map[string]int{}}, parent: state}
			h.executor = NewStageExecutorWithRunner(h.b, nil, fakeRunnerLookup{serverID: "mutable"}, remote)
			if err := h.execute(t, "root"); err == nil {
				t.Fatal("unsafe existing parent accepted")
			}
			if remote.parent != state || remote.parentCreates != 0 || len(remote.counts) != 0 || len(remote.workspaces) != 0 {
				t.Fatal("unsafe parent mutated or business ran")
			}
		})
	}
}

// Real shell/filesystem checks run on Linux in the same native test binary.
// They touch only each test's temporary directory, never the real shared root.
func TestRemoteWorkspaceParentShellBoundaries(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("real Linux shell/stat/permission semantics")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Fatal(err)
	}
	runCreate := func(parent, child string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return exec.CommandContext(ctx, "sh", "-c", recoveryRemoteWorkspaceCreateScript, "pw-remote-workspace-create", parent, child).Run()
	}
	for _, kind := range []string{"missing", "safe_existing", "symlink", "regular_file", "group_writable", "other_writable", "occupied_child", "child_symlink", "foreign_owner"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			parent := filepath.Join(root, "parent")
			child := filepath.Join(parent, "child")
			allowed := kind == "missing" || kind == "safe_existing"
			if kind != "missing" {
				if kind == "regular_file" {
					if err := os.WriteFile(parent, []byte("preserve-parent"), 0600); err != nil {
						t.Fatal(err)
					}
				} else if kind == "symlink" {
					target := filepath.Join(root, "target")
					if err := os.Mkdir(target, 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(target, parent); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := os.Mkdir(parent, 0755); err != nil {
						t.Fatal(err)
					}
				}
			}
			switch kind {
			case "group_writable":
				if err := os.Chmod(parent, 0770); err != nil {
					t.Fatal(err)
				}
			case "other_writable":
				if err := os.Chmod(parent, 0777); err != nil {
					t.Fatal(err)
				}
			case "occupied_child":
				if err := os.Mkdir(child, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(child, "sentinel"), []byte("preserve-child"), 0600); err != nil {
					t.Fatal(err)
				}
			case "child_symlink":
				target := filepath.Join(root, "target")
				if err := os.Mkdir(target, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, child); err != nil {
					t.Fatal(err)
				}
			case "foreign_owner":
				if os.Geteuid() != 0 {
					t.Skip("changing owner of test-owned directory requires root")
				}
				if err := os.Chown(parent, 1, -1); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.Lstat(parent)
			err := runCreate(parent, child)
			if allowed != (err == nil) {
				t.Fatalf("parent kind=%s allowed=%t actual=%v", kind, allowed, err)
			}
			after, statErr := os.Lstat(parent)
			if statErr != nil {
				t.Fatal(statErr)
			}
			if before != nil && (before.Mode() != after.Mode() || !os.SameFile(before, after)) {
				t.Fatal("existing parent identity or mode changed")
			}
			if kind == "missing" && after.Mode().Perm() != 0700 {
				t.Fatal("new parent is not private")
			}
			if allowed {
				info, e := os.Lstat(child)
				if e != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
					t.Fatal("private exclusive child missing", e)
				}
			}
			if kind == "occupied_child" {
				if b, e := os.ReadFile(filepath.Join(child, "sentinel")); e != nil || string(b) != "preserve-child" {
					t.Fatal("occupied child modified")
				}
			}
			if kind == "regular_file" {
				if b, e := os.ReadFile(parent); e != nil || string(b) != "preserve-parent" {
					t.Fatal("existing file modified")
				}
			}
			if kind == "child_symlink" {
				if info, e := os.Lstat(child); e != nil || info.Mode()&os.ModeSymlink == 0 {
					t.Fatal("existing child symlink modified")
				}
			}
		})
	}
	t.Run("concurrent_parent_initialization", func(t *testing.T) {
		root := t.TempDir()
		parent := filepath.Join(root, "parent")
		var wg sync.WaitGroup
		failures := make(chan error, 2)
		for _, name := range []string{"first", "second"} {
			wg.Add(1)
			go func(name string) { defer wg.Done(); failures <- runCreate(parent, filepath.Join(parent, name)) }(name)
		}
		wg.Wait()
		close(failures)
		for err := range failures {
			if err != nil {
				t.Fatal("safe concurrent parent initialization failed", err)
			}
		}
		for _, name := range []string{"first", "second"} {
			if info, e := os.Stat(filepath.Join(parent, name)); e != nil || !info.IsDir() {
				t.Fatal("exclusive child lost", e)
			}
		}
	})
}
