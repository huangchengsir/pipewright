package build

// This tests the real copied-workspace adapter and encrypted SQLite resource
// ledger with a stateful CLI model. It is not physical Docker evidence.
import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/runretry"
)

type recoveryWorkspaceCommander struct {
	resources ownedResourceCommander
	workspace workspaceCommander
}

func (c *recoveryWorkspaceCommander) Run(ctx context.Context, name string, args []string, stdin string) (string, string, int, error) {
	if args[0] == "inspect" && strings.Contains(strings.Join(args, " "), "{{json .State}}") {
		return c.workspace.Run(ctx, name, args, stdin)
	}
	return c.resources.Run(ctx, name, args, stdin)
}
func (c *recoveryWorkspaceCommander) Stream(ctx context.Context, name string, args []string, stdin string, line func(string, string)) (int, error) {
	if args[0] == "create" {
		_, _, code, err := c.resources.Run(ctx, name, args, stdin)
		return code, err
	}
	if args[0] == "rm" {
		return c.resources.Stream(ctx, name, args, stdin, line)
	}
	return c.workspace.Stream(ctx, name, args, stdin, line)
}

func TestRecoveryCopiedWorkspacePreservesLedgerContextDuringCleanup(t *testing.T) {
	for _, mode := range []string{"success", "failed-command", "canceled", "object-still-present", "foreign-owner"} {
		t.Run(mode, func(t *testing.T) {
			h := newRetryHarness(t, retrySpec(retryJob("work")))
			prepared, err := h.s.Prepare(context.Background(), "root")
			if err != nil {
				t.Fatal(err)
			}
			ex, _ := runretry.ExecutionFrom(prepared)
			node, _ := ex.Node("stage", "work")
			if err = ex.Begin(prepared, node); err != nil {
				t.Fatal(err)
			}
			prepared, err = runretry.WithNode(prepared, node)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(prepared)
			defer cancel()
			c := &recoveryWorkspaceCommander{workspace: workspaceCommander{t: t}}
			if mode == "failed-command" {
				c.workspace.exit = 23
			}
			if mode == "canceled" {
				c.workspace.cancel = cancel
			}
			if mode == "object-still-present" {
				c.resources.leave = true
			}
			if mode == "foreign-owner" {
				c.resources.foreign = true
			}
			d := &shellDriver{bin: "docker", cmdr: c, copyWorkspace: true, workspaceReconcileAfter: time.Now().Add(time.Hour)}
			code, runErr := d.RunToolchain(ctx, "fixture", t.TempDir(), "/workspace", nil, []string{"true"}, pipeline.Resource{}, nil)
			if mode == "success" && (runErr != nil || code != 0) {
				t.Fatal(code, runErr)
			}
			if mode == "failed-command" && (runErr != nil || code != 23) {
				t.Fatal(code, runErr)
			}
			if mode == "canceled" && !errors.Is(runErr, context.Canceled) {
				t.Fatal("cancellation lost", code, runErr)
			}
			records, err := h.s.Resources(context.WithoutCancel(ctx), "root")
			if err != nil || len(records) != 1 {
				t.Fatal("actual resource ledger missing", len(records), err)
			}
			wantDrained := mode != "object-still-present" && mode != "foreign-owner"
			if (records[0].State == "drained") != wantDrained {
				t.Fatalf("removed workspace ledger state=%s wantDrained=%t actualMode=%s", records[0].State, wantDrained, mode)
			}
			if wantDrained && (records[0].Ref.ID != strings.Repeat("b", 64) || records[0].Ref.RuntimeID == "" || records[0].Ref.Owner == "" || !records[0].Ref.RemoveVolumes || len(c.resources.objects) != 0) {
				t.Fatal("exact lifecycle proof incomplete")
			}
			if mode == "foreign-owner" && c.resources.removes != 0 {
				t.Fatal("foreign object removed")
			}
			if mode == "object-still-present" && len(c.resources.objects) != 1 {
				t.Fatal("unverified cleanup hidden")
			}
			t.Logf("SQLite owned workspace lifecycle %s: state=%s removed=%s", mode, records[0].State, fmt.Sprint(c.resources.removes))
		})
	}
}
