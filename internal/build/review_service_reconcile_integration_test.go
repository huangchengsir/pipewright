package build

import (
	"context"
	"errors"
	"os"
	"runtime"
	"testing"
	"time"
)

type reviewDropRealCreate struct {
	Commander
	kind, mode string
	dropped    bool
	cancel     context.CancelFunc
}

func (c *reviewDropRealCreate) Run(ctx context.Context, bin string, args []string, in string) (string, string, int, error) {
	out, stderr, code, err := c.Commander.Run(ctx, bin, args, in)
	matches := c.kind == "container" && len(args) > 0 && args[0] == "create" || c.kind == "network" && len(args) > 1 && args[0] == "network" && args[1] == "create"
	if matches && !c.dropped && code == 0 && err == nil {
		c.dropped = true
		switch c.mode {
		case "missing-id":
			return "", stderr, 0, nil
		case "canceled":
			c.cancel()
			return "", "", -1, context.Canceled
		default:
			return "", "", -1, errors.New("injected lost create receipt")
		}
	}
	return out, stderr, code, err
}
func TestRealServiceAmbiguousCreateCleanup(t *testing.T) {
	if os.Getenv("PIPEWRIGHT_TEST_SERVICE_RECONCILE") != "1" {
		t.Skip("set PIPEWRIGHT_TEST_SERVICE_RECONCILE=1 on the authorized isolated Docker node")
	}
	if runtime.GOOS != "linux" {
		t.Skip("isolated Linux Docker acceptance only")
	}
	for _, kind := range []string{"network", "container"} {
		for _, mode := range []string{"lost-response", "missing-id", "canceled"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				driver, real, _, image, caseID := matrixRealSetup(t)
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				drop := &reviewDropRealCreate{Commander: real, kind: kind, mode: mode, cancel: cancel}
				driver.cmdr = drop
				network := "pw-reconcile-" + caseID
				container := network + "-service"
				var code int
				var err error
				if kind == "network" {
					code, err = driver.CreateNetwork(ctx, network, nil)
				} else {
					if c, e := driver.CreateNetwork(ctx, network, nil); c != 0 || e != nil {
						t.Fatal(c, e)
					}
					code, err = driver.RunService(ctx, container, "fixture", image, network, nil, nil, nil)
				}
				if !drop.dropped || code == 0 || err == nil {
					t.Fatalf("real daemon create response was not intercepted: dropped=%v code=%d err=%v", drop.dropped, code, err)
				}
				if kind == "container" {
					cleanup, done := context.WithTimeout(context.Background(), 30*time.Second)
					defer done()
					if c, e := driver.RemoveNetwork(cleanup, network, nil); c != 0 || e != nil {
						t.Fatal("confirmed empty network cleanup", c, e)
					}
				}
				real.assertClean(t, nil)
				containers, networks := real.snapshot()
				if len(networks) != 1 || kind == "container" && len(containers) != 1 || kind == "network" && len(containers) != 0 {
					t.Fatal("fixture did not create exact expected resources")
				}
				t.Logf("SERVICE_RECONCILE_PROOF kind=%s mode=%s daemonCreated=true responseDropped=true ownedFullIDCleanup=true containers=%v networks=%v", kind, mode, containers, networks)
			})
		}
	}
}
