package build

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/runretry"
	"github.com/huangchengsir/pipewright/internal/target"
	"strings"
	"testing"
)

type resourceObject struct{ id, owner, kind string }
type ownedResourceCommander struct {
	objects                  map[string]resourceObject
	calls                    [][]string
	leave, foreign, lost     bool
	notStarted, knownFailure bool
	removes                  int
}

func (c *ownedResourceCommander) Run(_ context.Context, _ string, args []string, _ string) (string, string, int, error) {
	c.calls = append(c.calls, append([]string(nil), args...))
	if args[0] == "info" {
		return "daemon-fixture", "", 0, nil
	}
	create := args[0] == "create" || len(args) > 1 && args[0] == "network" && args[1] == "create"
	if create {
		if c.notStarted {
			return "", "", -1, target.NotStarted(target.ErrUnreachable)
		}
		if c.knownFailure {
			return "", "create rejected", 9, nil
		}
		kind, name, owner := "container", "", ""
		if args[0] == "network" {
			kind = "network"
			name = args[len(args)-1]
		}
		for i := range args {
			if args[i] == "--name" && i+1 < len(args) {
				name = args[i+1]
			}
			if args[i] == "--label" && i+1 < len(args) {
				owner = strings.TrimPrefix(args[i+1], serviceOwnerLabel+"=")
			}
		}
		id := strings.Repeat("a", 64)
		if kind == "container" {
			id = strings.Repeat("b", 64)
		}
		if c.objects == nil {
			c.objects = map[string]resourceObject{}
		}
		c.objects[name] = resourceObject{id, owner, kind}
		if c.lost {
			return "", "", -1, errors.New("lost receipt")
		}
		return id, "", 0, nil
	}
	lookup := args[len(args)-1]
	kind := "container"
	if args[0] == "network" {
		kind = "network"
	}
	for name, o := range c.objects {
		if lookup == name || lookup == o.id {
			owner := o.owner
			if c.foreign {
				owner = "foreign"
			}
			return o.id + " " + owner, "", 0, nil
		}
	}
	if kind == "network" {
		return "", "Error: No such network: " + lookup, 1, nil
	}
	return "", "Error: No such object: " + lookup, 1, nil
}
func (c *ownedResourceCommander) Stream(_ context.Context, _ string, args []string, _ string, _ func(string, string)) (int, error) {
	c.calls = append(c.calls, append([]string(nil), args...))
	if args[0] == "run" && c.notStarted {
		return -1, target.NotStarted(target.ErrUnreachable)
	}
	if args[0] == "rm" || len(args) > 1 && args[0] == "network" && args[1] == "rm" {
		c.removes++
		if !c.leave {
			for name, o := range c.objects {
				if o.id == args[len(args)-1] {
					delete(c.objects, name)
				}
			}
		}
	}
	return 0, nil
}

func TestRecoveryKnownCreateFailureDoesNotInventUnknownCreator(t *testing.T) {
	for _, mode := range []string{"not-started", "known-exit"} {
		t.Run(mode, func(t *testing.T) {
			h, ctx, d, c := resourceDriverFixture(t)
			c.notStarted = mode == "not-started"
			c.knownFailure = mode == "known-exit"
			code, err := d.CreateNetwork(ctx, "owned-network", nil)
			if code == 0 || c.removes != 0 || len(c.objects) != 0 {
				t.Fatal(code, err, c.removes, c.objects)
			}
			if mode == "not-started" && !target.IsNotStarted(err) {
				t.Fatal("lost not-started classification", err)
			}
			records, err := h.s.Resources(ctx, "root")
			if err != nil || len(records) != 1 || records[0].Ref.CreationUnknown {
				t.Fatal(records, err)
			}
			if mode == "not-started" && records[0].State != "drained" {
				t.Fatal("unstarted resource retained", records[0])
			}
			// A known exited creator cannot subsequently create the resource.
			// Independent inspect-absent is therefore sufficient for reconciliation.
			drained, err := d.VerifyResource(ctx, records[0].Ref)
			if err != nil || !drained || c.removes != 0 {
				t.Fatal(drained, err, c.removes)
			}
		})
	}
}

func TestRecoveryToolchainNotStartedResourceIsDrained(t *testing.T) {
	h, ctx, d, c := resourceDriverFixture(t)
	c.notStarted = true
	code, err := d.runOwnedToolchain(ctx, []string{"run", "--rm", "fixture"}, []string{"run", "--rm", "fixture"}, nil)
	if code >= 0 || !target.IsNotStarted(err) {
		t.Fatal(code, err)
	}
	records, err := h.s.Resources(ctx, "root")
	if err != nil || len(records) != 1 || records[0].State != "drained" || records[0].Ref.CreationUnknown || c.removes != 0 {
		t.Fatal(records, err, c.removes)
	}
}
func resourceDriverFixture(t *testing.T) (*retryHarness, context.Context, *shellDriver, *ownedResourceCommander) {
	t.Helper()
	spec := retrySpec(retryJob("A"))
	spec.Stages[0].Services = []pipeline.ServiceSpec{{Name: "db", Image: "fixture"}}
	h := newRetryHarness(t, spec)
	ctx, e := h.s.Prepare(context.Background(), "root")
	if e != nil {
		t.Fatal(e)
	}
	ex, _ := runretry.ExecutionFrom(ctx)
	n, _ := ex.ControlNode("stage", "services")
	if e = ex.Begin(ctx, n); e != nil {
		t.Fatal(e)
	}
	ctx, e = runretry.WithNode(ctx, n)
	if e != nil {
		t.Fatal(e)
	}
	c := &ownedResourceCommander{}
	d := &shellDriver{bin: "docker", cmdr: c}
	return h, ctx, d, c
}

func TestRecoveryServiceCleanupUsesExactOwnerAndReadBack(t *testing.T) {
	h, ctx, d, c := resourceDriverFixture(t)
	if code, e := d.CreateNetwork(ctx, "owned-network", nil); e != nil || code != 0 {
		t.Fatal(code, e)
	}
	if code, e := d.RunService(ctx, "owned-db", "db", "fixture", "owned-network", nil, nil, nil); e != nil || code != 0 {
		t.Fatal(code, e)
	}
	if code, e := d.StopService(ctx, "owned-db", nil); e != nil || code != 0 {
		t.Fatal(code, e)
	}
	if code, e := d.RemoveNetwork(ctx, "owned-network", nil); e != nil || code != 0 {
		t.Fatal(code, e)
	}
	records, e := h.s.Resources(ctx, "root")
	if e != nil || len(records) != 2 {
		t.Fatal(records, e)
	}
	for _, r := range records {
		if r.State != "drained" || r.Ref.ID == "" || r.Ref.Owner == "" || r.Ref.RuntimeID == "" {
			t.Fatal(r)
		}
	}
	if c.removes != 2 {
		t.Fatal(c.removes)
	}
}
func TestRecoveryServiceForeignAndFalseCleanupReceiptsStayBlocked(t *testing.T) {
	for _, mode := range []string{"foreign", "still-present", "lost-create"} {
		t.Run(mode, func(t *testing.T) {
			h, ctx, d, c := resourceDriverFixture(t)
			if mode == "lost-create" {
				c.lost = true
			}
			code, e := d.CreateNetwork(ctx, "owned-network", nil)
			if mode == "lost-create" {
				if e == nil || code == 0 || c.removes != 0 {
					t.Fatal(code, e, c.removes)
				}
			} else {
				if e != nil {
					t.Fatal(e)
				}
				c.foreign = mode == "foreign"
				c.leave = mode == "still-present"
				if code, e = d.RemoveNetwork(ctx, "owned-network", nil); e == nil || code == 0 {
					t.Fatal(code, e)
				}
				if mode == "foreign" && c.removes != 0 {
					t.Fatal("foreign object removed")
				}
			}
			records, e := h.s.Resources(ctx, "root")
			if e != nil || len(records) != 1 || records[0].State == "drained" {
				t.Fatal(records, e)
			}
			before := c.removes
			drained, e := d.VerifyResource(ctx, records[0].Ref)
			if e == nil && drained {
				t.Fatal("unknown resource accepted")
			}
			if before != c.removes {
				t.Fatal("read-only verifier deleted")
			}
		})
	}
}

func TestRemoteResourceVerifierChecksBindingMarkerAndAbsence(t *testing.T) {
	target := &retryRemoteFixture{counts: map[string]int{}, workspaces: map[string]string{"/tmp/pipewright-remote/owned": "owner"}}
	ctx := context.Background()
	binding, e := remoteBinding(ctx, target, "server")
	if e != nil {
		t.Fatal(e)
	}
	boot, e := remoteBoot(ctx, target, "server")
	if e != nil {
		t.Fatal(e)
	}
	r := runretry.ResourceRef{Kind: "remote_directory", ServerID: "server", ConnectionFingerprint: binding, RuntimeID: boot, Path: "/tmp/pipewright-remote/owned", Owner: "owner"}
	gone, e := VerifyRemoteResource(ctx, target, r)
	if e != nil || gone {
		t.Fatal(gone, e)
	}
	r.Owner = "foreign"
	if gone, e = VerifyRemoteResource(ctx, target, r); e == nil || gone {
		t.Fatal(gone, e)
	}
	r.Owner = "owner"
	delete(target.workspaces, r.Path)
	if gone, e = VerifyRemoteResource(ctx, target, r); e != nil || !gone {
		t.Fatal(gone, e)
	}
	r.CreationUnknown = true
	if gone, e = VerifyRemoteResource(ctx, target, r); !errors.Is(e, runretry.ErrUnknown) || gone {
		t.Fatal("late-create risk erased", gone, e)
	}
}
