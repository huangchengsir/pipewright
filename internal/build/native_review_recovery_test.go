package build

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/dagrun"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/runretry"
	"os"
	"regexp"
	"testing"
	"time"
)

func TestNativeReviewBindMountKnownFailureRetry(t *testing.T) {
	image := nativeRetryReady(t)
	h, d, _ := nativeHarness(t, retrySpec(nativeJob(image, "work", "true"), nativeJob(image, "after", "true", "work")))
	d.copyWorkspace = false
	d.failing("work", true)
	nativeExecute(t, h, "root", true)
	d.failing("work", false)
	child := h.submit(t, "root", []string{h.node(t, "root", "stage", "work").Key}, nil)
	nativeExecute(t, h, child, false)
	nativeAssertCounts(t, d, map[string]int{"work": 2, "after": 1})
}
func TestNativeReviewSingleJobPostImplicitWorkspace(t *testing.T) {
	image := nativeRetryReady(t)
	spec := retrySpec(nativeJob(image, "work", "printf 'current-attempt' > scratch.txt"))
	post := nativeJob(image, "after", `test "$(cat scratch.txt)" = current-attempt`)
	spec.Stages[0].Post = []pipeline.PostStep{{Condition: "always", Image: image, Commands: []string{post.Config["commands"].(string)}}}
	h, d, _ := nativeHarness(t, spec)
	nativeExecute(t, h, "root", false)
	nativeAssertCounts(t, d, map[string]int{"work": 1, "after": 1})
	var status string
	if err := h.store.DB.QueryRow("SELECT status FROM run_retry_nodes WHERE attempt_id='root' AND node_key=?", nativeControlKey(t, h, "root", "stage", "post:0")).Scan(&status); err != nil || status != "success" {
		t.Fatal("real Post lost same-attempt scratch file", status, err)
	}
}
func TestNativeReviewRemoteServicesPostRetry(t *testing.T) {
	image := nativeRetryReady(t)
	for _, services := range []bool{false, true} {
		t.Run(map[bool]string{false: "post-only", true: "services-and-post"}[services], func(t *testing.T) {
			body := "printf 'remote-bytes' > scratch.txt"
			spec := retrySpec(nativeJob(image, "produce", body))
			spec.Stages[0].Jobs[0].Config["artifacts"] = `[{"path":"scratch.txt","name":"scratch"}]`
			if services {
				service := os.Getenv("PIPEWRIGHT_TEST_MATRIX_SERVICE_IMAGE")
				if !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(service) {
					t.Fatal("requires exact cached fixture image")
				}
				nativeRequireImage(t, service)
				spec.Stages[0].Services = []pipeline.ServiceSpec{{Name: "fixture", Image: service}}
				spec.Stages[0].Jobs[0].Config["commands"] = spec.Stages[0].Jobs[0].Config["commands"].(string) + "\nnode -e 'fetch(\"http://fixture:8080/state\").then(r=>{if(!r.ok)process.exit(3)}).catch(()=>process.exit(4))'"
			}
			post := nativeJob(image, "after", `test "$(cat scratch.txt)" = remote-bytes`)
			spec.Stages[0].Post = []pipeline.PostStep{{Condition: "always", Image: image, Commands: []string{post.Config["commands"].(string)}}}
			h, _, _ := nativeHarness(t, spec, func(s *runretry.Snapshot) { s.RunnerRefs = map[string]string{"serverId": "native-runner"} })
			port, password, dir := nativeSSHFixture(t)
			targets, runs := nativeInstallDeployer(t, h)
			nativeAddServer(t, h, targets, "native-runner", port, password)
			observer := &nativeRemoteRunnerObserver{Service: targets, serverID: "native-runner", image: image, scope: "pw-native-review-" + uuid.NewString(), cidRoot: dir, fail: map[string]bool{"after": true}, counts: map[string]int{}, containers: map[string]nativeRemoteContainerEvidence{}}
			runretry.WithResourceVerifier(func(ctx context.Context, ref runretry.ResourceRef) (bool, error) {
				if ref.ServerID != "" {
					return VerifyRemoteResource(ctx, observer, ref)
				}
				return h.b.VerifyResource(ctx, ref)
			})(h.s)
			executor := NewStageExecutorWithRunner(h.b, nil, nativeDifferentRunnerLookup{}, observer)
			h.executor = func(ctx context.Context, r *run.Run, stage pipeline.Stage, rep dagrun.StageReporter) error {
				return executor(ctx, r, stage, &nativePersistReporter{StageReporter: rep, runs: runs, id: r.ID})
			}
			nativeExecute(t, h, "root", false)
			key := nativeControlKey(t, h, "root", "stage", "post:0")
			var original string
			if err := h.store.DB.QueryRow("SELECT status FROM run_retry_nodes WHERE attempt_id='root' AND node_key=?", key).Scan(&original); err != nil || original != "failed" {
				t.Fatal("actual Post failure missing", original, err)
			}
			observer.failing("after", false)
			var rebuild []string
			if services {
				rebuild = []string{h.node(t, "root", "stage", "produce").Key}
			}
			child := h.submit(t, "root", []string{key}, rebuild)
			nativeExecute(t, h, child, false)
			var status string
			if err := h.store.DB.QueryRow("SELECT status FROM run_retry_nodes WHERE attempt_id=? AND node_key=?", child, key).Scan(&status); err != nil || status != "success" {
				t.Fatal("actual Post retry failed", status, err)
			}
			counts, containers, diag := observer.snapshot()
			wantProduce := 1
			if services {
				wantProduce = 2
			}
			if counts["produce"] != wantProduce || counts["after"] != 2 || len(diag) != 0 {
				t.Fatal(counts, diag)
			}
			for _, id := range []string{"root", child} {
				records, err := h.s.Resources(context.Background(), id)
				if err != nil {
					t.Fatal(err)
				}
				if len(records) < 2 {
					t.Fatal("missing actual resource receipts")
				}
				for _, record := range records {
					if record.State != "drained" || record.Ref.ServerID != "native-runner" {
						t.Fatal("resource not drained")
					}
					absent, err := VerifyRemoteResource(context.Background(), observer, record.Ref)
					if err != nil || !absent {
						t.Fatal("independent remote resource observation failed", err)
					}
					if record.Ref.Kind == "container" && record.Ref.ID == "" {
						actual, ok := containers[record.Ref.Name]
						if !ok || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(actual.ID) || actual.Owner != record.Ref.Owner || actual.NodeKey != record.Ref.NodeKey {
							t.Fatal("missing actual toolchain CID")
						}
					}
				}
			}
			if err := h.store.DB.QueryRow("SELECT status FROM run_retry_nodes WHERE attempt_id='root' AND node_key=?", key).Scan(&original); err != nil || original != "failed" {
				t.Fatal("original Post history changed", original, err)
			}
		})
	}
}

// The child is dispatched through the real worker, including project_activity
// release; a hand-set terminal result cannot conceal a skipped cleanup owner.
func TestNativeReviewSkippedStageWorkerReleasesActivity(t *testing.T) {
	image := nativeRetryReady(t)
	spec := pipeline.Spec{Stages: []pipeline.Stage{
		{ID: "base", Name: "base", Kind: "build", Jobs: []pipeline.Job{nativeJob(image, "produce", "true")}},
		{ID: "skip", Name: "skip", Kind: "build", Needs: []string{"base"}, When: pipeline.When{Branches: []string{"never-matches"}}, Jobs: []pipeline.Job{nativeJob(image, "untouched", "true")}},
		{ID: "repair", Name: "repair", Kind: "build", Needs: []string{"base"}, Jobs: []pipeline.Job{nativeJob(image, "consume", "true")}},
		{ID: "after-skip", Name: "after", Kind: "build", Needs: []string{"skip"}, Jobs: []pipeline.Job{nativeJob(image, "after", "true")}},
	}}
	h, d, _ := nativeHarness(t, spec)
	d.failing("consume", true)
	nativeExecute(t, h, "root", true)
	runs := run.New(h.store.DB)
	loader := dagrun.SpecLoaderFunc(func(context.Context, string) (*pipeline.Config, error) {
		return nil, errors.New("mutable loader must not run")
	})
	pool := run.NewWorkerPool(runs, run.WithRunner(dagrun.New(loader, dagrun.WithStageExecutor(NewStageExecutor(h.b, nil)))), run.WithRetryRuntime(h.s))
	creator, dispatch := run.RetryAdapters(runs)
	runretry.WithChildCreator(creator)(h.s)
	runretry.WithDispatch(dispatch)(h.s)
	runretry.WithRejectedChild(run.RejectRetryQueued(runs))(h.s)
	pool.Start()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		pool.Stop(ctx)
	}()
	d.failing("consume", false)
	child := h.submit(t, "root", []string{h.node(t, "root", "repair", "consume").Key}, nil)
	deadline := time.Now().Add(45 * time.Second)
	settled := false
	for time.Now().Before(deadline) {
		result, err := runs.Get(context.Background(), child)
		if err != nil {
			t.Fatal(err)
		}
		if run.IsTerminal(result.Status) {
			if result.Status != run.StatusSuccess {
				t.Fatal("repaired child failed", result.Status)
			}
			var active int
			if err := h.store.DB.QueryRow("SELECT COUNT(*) FROM project_activity WHERE run_id=? AND released_at IS NULL", child).Scan(&active); err != nil {
				t.Fatal(err)
			}
			drained, err := h.s.ResourcesDrained(context.Background(), child)
			if err != nil {
				t.Fatal(err)
			}
			if active == 0 && drained {
				settled = true
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !settled {
		t.Fatal("real worker retained activity for known skipped stage")
	}
	nativeAssertCounts(t, d, map[string]int{"produce": 1, "consume": 2, "untouched": 0, "after": 0})
}
