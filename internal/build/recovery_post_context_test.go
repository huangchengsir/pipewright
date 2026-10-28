package build

// Real recovery/Post/copied-workspace adapters and encrypted SQLite ledgers;
// the stateful CLI model is deliberately not physical Docker acceptance.
import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/runretry"
)

type postWorkspaceCommander struct {
	recoveryWorkspaceCommander
	workRuns, postRuns            int
	failWork, failPost, leavePost bool
}

func (c *postWorkspaceCommander) Stream(ctx context.Context, name string, args []string, stdin string, line func(string, string)) (int, error) {
	if args[0] == "start" {
		node, ok := runretry.NodeFrom(ctx)
		if !ok {
			return -1, runretry.ErrInvalid
		}
		c.workspace.exit = 0
		if node.ControlKey == "post:0" {
			c.postRuns++
			if c.failPost {
				c.workspace.exit = 23
			}
			c.resources.leave = c.leavePost
		} else {
			c.workRuns++
			if c.failWork {
				c.workspace.exit = 23
			}
		}
	}
	// Both scripts are no-output commands in an initially empty source fixture.
	// Returning an empty staging directory models their exact file semantics,
	// avoiding the unrelated app.jar written by the general workspace fixture.
	if args[0] == "cp" && strings.HasPrefix(args[2], "pipewright-build-") {
		return 0, nil
	}
	return c.recoveryWorkspaceCommander.Stream(ctx, name, args, stdin, line)
}

func TestRecoveryPostCopiedWorkspaceUsesControlOwnership(t *testing.T) {
	for _, mode := range []string{"post-failure-then-retry", "condition-skipped", "business-failure-condition", "cleanup-unverified"} {
		t.Run(mode, func(t *testing.T) {
			condition := "always"
			if mode == "condition-skipped" || mode == "business-failure-condition" {
				condition = "on_failure"
			}
			spec := retrySpec(retryJob("work"))
			spec.Stages[0].Post = []pipeline.PostStep{{Condition: condition, Image: "fixture", Commands: []string{"true"}}}
			h := newRetryHarness(t, spec)
			c := &postWorkspaceCommander{recoveryWorkspaceCommander: recoveryWorkspaceCommander{workspace: workspaceCommander{t: t}}, failPost: mode == "post-failure-then-retry", failWork: mode == "business-failure-condition", leavePost: mode == "cleanup-unverified"}
			h.b.driver = &shellDriver{bin: "docker", cmdr: c, copyWorkspace: true, workspaceReconcileAfter: time.Now().Add(time.Hour)}
			err := h.execute(t, "root")
			if (err != nil) != (mode == "business-failure-condition") {
				t.Fatal("Post changed original business result", err)
			}
			key := nativeControlKey(t, h, "root", "stage", "post:0")
			wantRuns := 1
			wantStatus := "success"
			if mode == "condition-skipped" {
				wantRuns, wantStatus = 0, "skipped"
			}
			if mode == "post-failure-then-retry" {
				wantStatus = "failed"
			}
			var status string
			if err = h.store.DB.QueryRow("SELECT status FROM run_retry_nodes WHERE attempt_id='root' AND node_key=?", key).Scan(&status); err != nil || status != wantStatus {
				t.Fatal("Post result not persisted", status, err)
			}
			if c.workRuns != 1 || c.postRuns != wantRuns {
				t.Fatalf("actual copied adapter starts work=%d post=%d wantPost=%d", c.workRuns, c.postRuns, wantRuns)
			}
			records, err := h.s.Resources(context.Background(), "root")
			if err != nil {
				t.Fatal(err)
			}
			postResources := 0
			for _, r := range records {
				if r.Ref.NodeKey == key {
					postResources++
					if r.Ref.AttemptID != "root" || r.Ref.RootID != "root" || r.Ref.ID == "" || r.Ref.Owner == "" || r.Ref.ProcessOwner == "" {
						t.Fatal("Post resource ownership incomplete")
					}
					if (r.State == "drained") != (mode != "cleanup-unverified") {
						t.Fatal("Post cleanup proof changed", r.State)
					}
				}
			}
			if postResources != wantRuns {
				t.Fatalf("Post-owned resource count=%d want=%d", postResources, wantRuns)
			}
			drained, err := h.s.ResourcesDrained(context.Background(), "root")
			if err != nil || drained == (mode == "cleanup-unverified") {
				t.Fatal("resource guard changed", drained, err)
			}
			if mode == "post-failure-then-retry" {
				c.failPost = false
				child := h.submit(t, "root", []string{key}, nil)
				if err = h.execute(t, child); err != nil {
					t.Fatal(err)
				}
				if c.workRuns != 1 || c.postRuns != 2 {
					t.Fatal("Post retry reexecuted business or skipped Post", c.workRuns, c.postRuns)
				}
				childResources, err := h.s.Resources(context.Background(), child)
				if err != nil || len(childResources) != 1 || childResources[0].Ref.NodeKey != key || childResources[0].State != "drained" {
					t.Fatal("child Post resource proof incorrect", len(childResources), err)
				}
			}
		})
	}
}
