package build

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/runretry"
	"os"
	"strings"
	"testing"
)

func TestReviewKnownExitProcess(t *testing.T) {
	if os.Getenv("PIPEWRIGHT_REVIEW_EXIT_HELPER") == "1" {
		os.Exit(23)
	}
}

type reviewExitCommander struct {
	*ownedResourceCommander
	transport bool
}

func (c reviewExitCommander) Stream(ctx context.Context, _ string, _ []string, _ string, _ func(string, string)) (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return -1, err
	}
	code, err := (execCommander{}).Stream(ctx, exe, []string{"-test.run=^TestReviewKnownExitProcess$"}, "", nil)
	if c.transport {
		return code, errors.New("completion receipt lost")
	}
	return code, err
}
func TestReviewToolchainKnownExitCanDrain(t *testing.T) {
	t.Setenv("PIPEWRIGHT_REVIEW_EXIT_HELPER", "1")
	for _, unknown := range []bool{false, true} {
		t.Run(map[bool]string{false: "real-exit", true: "lost-receipt"}[unknown], func(t *testing.T) {
			h, ctx, d, c := resourceDriverFixture(t)
			d.cmdr = reviewExitCommander{c, unknown}
			code, err := d.runOwnedToolchain(ctx, []string{"run", "--rm", "fixture"}, []string{"run", "--rm", "fixture"}, nil)
			if code != 23 || err == nil {
				t.Fatal(code, err)
			}
			records, err := h.s.Resources(ctx, "root")
			if err != nil || len(records) != 1 {
				t.Fatal(records, err)
			}
			if records[0].Ref.CreationUnknown != unknown || (records[0].State == "drained") == unknown {
				t.Fatalf("known exit/unknown transport conflated: %+v", records[0])
			}
		})
	}
}
func reviewBranchSpec(skip bool, emptyGate bool) pipeline.Spec {
	branch := pipeline.Stage{ID: "branch", Name: "branch", Kind: "build", Needs: []string{"base"}, Jobs: []pipeline.Job{retryJob("untouched")}}
	if skip {
		branch.When = pipeline.When{Branches: []string{"never-matches"}}
	}
	if emptyGate {
		branch.Gate = true
		branch.Jobs = nil
	}
	return pipeline.Spec{Stages: []pipeline.Stage{
		{ID: "base", Name: "base", Kind: "build", Jobs: []pipeline.Job{retryJob("base")}},
		branch,
		{ID: "repair", Name: "repair", Kind: "build", Needs: []string{"base"}, Jobs: []pipeline.Job{retryJob("repair")}},
		{ID: "after-branch", Name: "after", Kind: "build", Needs: []string{"branch"}, Jobs: []pipeline.Job{retryJob("after")}},
	}}
}
func TestReviewConditionSkipRemainsSkippedAfterIndependentRepair(t *testing.T) {
	h := newRetryHarness(t, reviewBranchSpec(true, false))
	h.driver.fail["repair"] = true
	if h.execute(t, "root") == nil {
		t.Fatal("missing first failure")
	}
	if drained, err := h.s.ResourcesDrained(context.Background(), "root"); err != nil || !drained {
		t.Fatal("known never-started skipped stage retained cleanup ownership", drained, err)
	}

	h.driver.fail["repair"] = false
	child := h.submit(t, "root", []string{h.node(t, "root", "repair", "repair").Key}, nil)
	if err := h.execute(t, child); err != nil {
		t.Fatal("intentional skip made repaired attempt fail", err)
	}
	if h.driver.counts["base"] != 1 || h.driver.counts["repair"] != 2 || h.driver.counts["untouched"] != 0 || h.driver.counts["after"] != 0 {
		t.Fatal(h.driver.counts)
	}
	for _, id := range []string{"root", child} {
		var status string
		n := h.node(t, id, "branch", "untouched")
		if err := h.store.DB.QueryRow("SELECT status FROM run_retry_nodes WHERE attempt_id=? AND node_key=?", id, n.Key).Scan(&status); err != nil || status != "skipped" {
			t.Fatal(id, status, err)
		}
	}
}
func TestReviewEmptyRejectedGateCannotBecomeInheritedSuccess(t *testing.T) {
	spec := reviewBranchSpec(false, true)
	spec.Stages = spec.Stages[:3] // No second blocked stage can mask the empty gate outcome.
	h := newRetryHarness(t, spec)
	h.gate = func(context.Context, *run.Run, pipeline.Stage) (bool, error) { return false, nil }
	h.driver.fail["repair"] = true
	if h.execute(t, "root") == nil {
		t.Fatal("missing gate/repair failure")
	}
	h.driver.fail["repair"] = false
	child := h.submit(t, "root", []string{h.node(t, "root", "repair", "repair").Key}, nil)
	if err := h.execute(t, child); err == nil {
		t.Fatal("unselected rejected gate became success")
	}
	if h.driver.counts["repair"] != 2 || h.driver.counts["after"] != 0 {
		t.Fatal(h.driver.counts)
	}
}
func TestReviewSingleJobPostRetainsCurrentWorkspace(t *testing.T) {
	spec := retrySpec(retryJob("implicit"))
	spec.Stages[0].Post = []pipeline.PostStep{{Condition: "always", Image: "fixture", Commands: []string{"consume"}}}
	h := newRetryHarness(t, spec)
	if err := h.execute(t, "root"); err != nil {
		t.Fatal(err)
	}
	key := nativeControlKey(t, h, "root", "stage", "post:0")
	var status string
	if err := h.store.DB.QueryRow("SELECT status FROM run_retry_nodes WHERE attempt_id='root' AND node_key=?", key).Scan(&status); err != nil || status != "success" {
		t.Fatal("Post lost the current job's scratch file", status, err)
	}
}
func TestReviewRemotePostUsesFrozenControlNode(t *testing.T) {
	spec := retrySpec(retryJob("remoteA"))
	spec.Stages[0].Post = []pipeline.PostStep{{Condition: "always", Image: "fixture", Commands: []string{"remotePost"}}}
	h := newRetryHarness(t, spec, func(s *runretry.Snapshot) { s.RunnerRefs = map[string]string{"serverId": "frozen"} })
	remote := &retryRemoteFixture{counts: map[string]int{}}
	h.executor = NewStageExecutorWithRunner(h.b, nil, fakeRunnerLookup{serverID: "mutable"}, remote)
	if err := h.execute(t, "root"); err != nil {
		t.Fatal(err)
	}
	if remote.counts["remoteA"] != 1 || remote.counts["remotePost"] != 1 || len(remote.workspaces) != 0 {
		t.Fatal("remote Post omitted", remote.counts, len(remote.workspaces))
	}
	key := nativeControlKey(t, h, "root", "stage", "post:0")
	var status string
	if err := h.store.DB.QueryRow("SELECT status FROM run_retry_nodes WHERE attempt_id='root' AND node_key=?", key).Scan(&status); err != nil || status != "success" {
		t.Fatal(status, err)
	}
}
func TestReviewRemoteWrapperPreservesServiceCapabilities(t *testing.T) {
	h := newRetryHarness(t, retrySpec(retryJob("work")))
	driver := &retryServicesDriver{retryCountingDriver: h.driver}
	wrapper := &recoveryRemoteDriver{Driver: driver}
	var anyDriver Driver = wrapper
	services, ok := anyDriver.(ServiceRunner)
	if !ok {
		t.Fatal("remote wrapper erased ServiceRunner")
	}
	ready, ok := anyDriver.(interface {
		ReadyService(context.Context, string) error
	})
	if !ok {
		t.Fatal("remote wrapper erased readiness")
	}
	ctx := context.Background()
	if code, err := services.CreateNetwork(ctx, "owned", nil); code != 0 || err != nil {
		t.Fatal(code, err)
	}
	if code, err := services.RunService(ctx, "db", "db", "fixture", "owned", nil, nil, nil); code != 0 || err != nil {
		t.Fatal(code, err)
	}
	if err := ready.ReadyService(ctx, "db"); err != nil {
		t.Fatal(err)
	}
	if code, err := services.StopService(ctx, "db", nil); code != 0 || err != nil {
		t.Fatal(code, err)
	}
	if code, err := services.RemoveNetwork(ctx, "owned", nil); code != 0 || err != nil {
		t.Fatal(code, err)
	}
	if driver.starts != 1 || driver.stops != 1 || len(driver.networks) != 1 || !strings.Contains(driver.networks[0], "owned") {
		t.Fatal(driver)
	}
}
