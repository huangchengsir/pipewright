package build

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type reviewLostCreate struct {
	kind, mode, owner string
	cancel            context.CancelFunc
	creates, lookups  int
	calls, removed    [][]string
}

func (c *reviewLostCreate) Run(ctx context.Context, _ string, args []string, _ string) (string, string, int, error) {
	c.calls = append(c.calls, append([]string(nil), args...))
	create := len(args) > 0 && args[0] == "create" || len(args) > 1 && args[0] == "network" && args[1] == "create"
	if create {
		c.creates++
		for i := 0; i+1 < len(args); i++ {
			if args[i] == "--label" && strings.HasPrefix(args[i+1], serviceOwnerLabel+"=") {
				c.owner = strings.TrimPrefix(args[i+1], serviceOwnerLabel+"=")
			}
		}
		if c.mode == "canceled" {
			c.cancel()
			return "", "", -1, context.Canceled
		}
		if c.mode == "missing-id" {
			return "", "", 0, nil
		}
		if c.mode == "podman-name" {
			return "owned-network", "", 0, nil
		}
		return "", "", 1, errors.New("synthetic lost create response")
	}
	if len(args) > 0 && (args[0] == "inspect" || len(args) > 1 && args[0] == "network" && args[1] == "inspect") {
		c.lookups++
		if ctx.Err() != nil {
			return "", "", 1, fmt.Errorf("reconcile inherited canceled context")
		}
		if _, ok := ctx.Deadline(); !ok {
			return "", "", 1, errors.New("reconcile had no budget")
		}
		if c.mode == "lookup-failure" {
			return "", "sensitive lookup detail", 1, errors.New("sensitive lookup detail")
		}
		owner := c.owner
		if c.mode == "foreign" {
			owner = "another-invocation"
		}
		return strings.Repeat("a", 64) + " " + owner, "", 0, nil
	}
	return "", "", 0, nil
}
func (c *reviewLostCreate) Stream(ctx context.Context, _ string, args []string, _ string, _ func(string, string)) (int, error) {
	c.calls = append(c.calls, append([]string(nil), args...))
	if ctx.Err() != nil {
		return -1, ctx.Err()
	}
	if len(args) > 0 && (args[0] == "rm" || len(args) > 1 && args[0] == "network" && args[1] == "rm") {
		c.removed = append(c.removed, append([]string(nil), args...))
		if c.mode == "cleanup-failure" {
			return 1, errors.New("synthetic cleanup failure")
		}
	}
	return 0, nil
}
func TestReviewServiceAmbiguousCreateReconcilesAndCleansOwnedID(t *testing.T) {
	for _, kind := range []string{"network", "container"} {
		for _, mode := range []string{"lost-response", "missing-id", "canceled", "foreign", "lookup-failure", "cleanup-failure"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				command := &reviewLostCreate{kind: kind, mode: mode, cancel: cancel}
				driver := &shellDriver{bin: "docker", cmdr: command}
				var code int
				var err error
				if kind == "network" {
					code, err = driver.CreateNetwork(ctx, "owned-network", nil)
				} else {
					driver.serviceNetworks.Store("known-network", strings.Repeat("b", 64))
					code, err = driver.RunService(ctx, "owned-container", "alias", "fixture", "known-network", nil, nil, nil)
				}
				if err == nil || code == 0 {
					t.Fatalf("ambiguous create passed: code=%d err=%v", code, err)
				}
				if command.creates != 1 || command.lookups != 1 {
					t.Fatalf("create replayed or identity not reconciled: creates=%d lookups=%d", command.creates, command.lookups)
				}
				owned := mode != "foreign" && mode != "lookup-failure"
				want := 0
				if owned {
					want = 1
				}
				if len(command.removed) != want {
					t.Fatalf("cleanup mismatch: %v; owner=%s err=%v", command.removed, command.owner, err)
				}
				for _, args := range command.removed {
					if args[len(args)-1] != strings.Repeat("a", 64) {
						t.Fatalf("cleanup used mutable name: %v", args)
					}
				}
				for _, args := range command.calls {
					if args[0] == "start" {
						t.Fatal("ambiguous create launched a new operation")
					}
				}
				if !owned && (!strings.Contains(err.Error(), command.owner) || strings.Contains(err.Error(), "sensitive lookup detail")) {
					t.Fatalf("unconfirmed ownership lacked safe evidence: %v", err)
				}
			})
		}
	}
}
func TestReviewServicePodmanNetworkNameKeepsVerifiedCompatibility(t *testing.T) {
	command := &reviewLostCreate{mode: "podman-name"}
	driver := &shellDriver{bin: "podman", cmdr: command}
	if code, e := driver.CreateNetwork(context.Background(), "owned-network", nil); code != 0 || e != nil {
		t.Fatal(code, e)
	}
	if id, ok := driver.serviceNetworks.Load("owned-network"); !ok || id != strings.Repeat("a", 64) {
		t.Fatal(id, ok)
	}
	if len(command.removed) != 0 {
		t.Fatal("valid named network response was treated as a failed create")
	}
}
