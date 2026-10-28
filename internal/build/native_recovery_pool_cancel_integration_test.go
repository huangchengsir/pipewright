package build

// The root attempt uses the existing manually settled native harness. Only the
// retry child is dispatched through a
// real WorkerPool and cancelled with the real run.Service.Cancel API. No OS
// restart or HTTP/SSE coverage is claimed by this representative test.
import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/dagrun"
	"github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/runretry"
)

func TestNativeRecoveryActualPoolCancellationPreservesProducer(t *testing.T) {
	image := nativeRetryReady(t)
	producer := nativeJob(image, "seed", "printf preserved > checkpoint.txt")
	producer.Config["artifacts"] = `[{"path":"checkpoint.txt","name":"preserved-checkpoint"}]`
	work := nativeJob(image, "work", "test \"$(cat checkpoint.txt)\" = preserved\nnode -e 'process.stdout.write(\"NR_CANCEL_READY\\n\");setInterval(()=>{},1000)'", "seed")
	work.Config["timeoutSeconds"] = 120
	h, driver, _ := nativeHarness(t, retrySpec(producer, work, nativeJob(image, "after", "true", "work")))
	runretry.WithResourceVerifier(h.b.VerifyResource)(h.s)
	driver.failing("work", true)
	nativeExecute(t, h, "root", true)
	seedNode := h.node(t, "root", "stage", "seed")
	workNode := h.node(t, "root", "stage", "work")
	var producerCipher []byte
	if err := h.store.DB.QueryRow("SELECT ciphertext FROM run_retry_nodes WHERE attempt_id='root' AND node_key=? AND status='success' AND reusable=1", seedNode.Key).Scan(&producerCipher); err != nil || len(producerCipher) == 0 {
		t.Fatal("original successful producer checkpoint unavailable", err)
	}

	runs := run.New(h.store.DB)
	loader := dagrun.SpecLoaderFunc(func(context.Context, string) (*pipeline.Config, error) {
		return nil, errors.New("mutable spec loader must never execute")
	})
	// Production step sink persists actual run_logs/artifacts and seed metadata.
	// Do not wrap the child in nativePersistReporter: that would add artifacts
	// twice and bypass the worker sink being accepted here.
	runner := dagrun.New(loader, dagrun.WithStageExecutor(NewStageExecutor(h.b, nil)))
	pool := run.NewWorkerPool(runs, run.WithRunner(runner), run.WithRetryRuntime(h.s), run.WithLogMasker(mask.NewMasker()))
	creator, dispatch := run.RetryAdapters(runs)
	if creator == nil || dispatch == nil {
		t.Fatal("real retry creation/dispatch adapters unavailable")
	}
	runretry.WithChildCreator(creator)(h.s)
	runretry.WithDispatch(dispatch)(h.s)
	runretry.WithRejectedChild(run.RejectRetryQueued(runs))(h.s)
	h.b.artifactLister = runs.ListArtifacts
	child := ""
	pool.Start()
	t.Cleanup(func() {
		stop, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		pool.Stop(stop)
		if child != "" {
			nativeLogLedger(t, h, child)
		}
		// The worker owns its exact resources. No name-based or unverified
		// emergency deletion is allowed when a failing assertion exits early.
	})
	artifacts, err := runs.ListArtifacts(context.Background(), "root")
	if err != nil {
		t.Fatal(err)
	}
	var original run.Artifact
	for _, artifact := range artifacts {
		if artifact.Metadata["logicalNodeKey"] == seedNode.Key {
			if original.ID != "" {
				t.Fatal("ambiguous seed artifact")
			}
			original = artifact
		}
	}
	if original.ID == "" || original.RunID != "root" {
		t.Fatal("original persisted artifact missing")
	}
	assertProducerBytes := func() {
		t.Helper()
		reader, err := h.b.artStore.Open(original.Reference)
		if err != nil {
			t.Fatal("original CAS object missing", err)
		}
		data, readErr := io.ReadAll(io.LimitReader(reader, 64))
		closeErr := reader.Close()
		if readErr != nil || closeErr != nil || string(data) != "preserved" {
			t.Fatal("original persisted bytes changed")
		}
	}
	assertProducerBytes()
	driver.failing("work", false)
	child = h.submit(t, "root", []string{workNode.Key}, nil)
	deadline := time.Now().Add(30 * time.Second)
	var resource runretry.ResourceRecord
	ready := false
	for time.Now().Before(deadline) {
		var readyLines int
		if err := h.store.DB.QueryRow("SELECT COUNT(*) FROM run_logs WHERE run_id=? AND text='NR_CANCEL_READY'", child).Scan(&readyLines); err != nil {
			t.Fatal(err)
		}
		current, err := runs.Get(context.Background(), child)
		if err != nil {
			t.Fatal(err)
		}
		if run.IsTerminal(current.Status) {
			t.Fatal("retry terminated before an actual running cancellation", current.Status)
		}
		if readyLines == 1 && current.Status == run.StatusRunning {
			records, err := h.s.Resources(context.Background(), child)
			if err != nil {
				t.Fatal(err)
			}
			for _, record := range records {
				if record.State == "active" && record.Ref.Kind == "container" && record.Ref.NodeKey == workNode.Key && record.Ref.ID != "" {
					if resource.ID != "" {
						t.Fatal("ambiguous active script container")
					}
					resource = record
				}
			}
			if resource.ID != "" {
				ready = true
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ready || resource.Ref.RootID != "root" || resource.Ref.AttemptID != child || resource.Ref.Owner == "" || resource.Ref.RuntimeID == "" || resource.Ref.ProcessOwner == "" {
		t.Fatal("actual running script identity was never independently established")
	}
	if len(resource.Ref.ID) != 64 || !serviceObjectID.MatchString(resource.Ref.ID) || nativeDocker(t, "container", "inspect", "--format", "{{.Id}} {{.State.Running}}", resource.Ref.ID) != resource.Ref.ID+" true" {
		t.Fatal("script readiness lacked the actual full running CID")
	}
	if driver.count("seed") != 1 || driver.count("work") != 2 || driver.count("after") != 0 {
		t.Fatal("unexpected actual start counters before cancel")
	}
	// This is the real application service cancellation path. The only direct
	// context cancellation below belongs to bounded read-only verification.
	if _, err = runs.Cancel(context.Background(), child); err != nil {
		t.Fatal(err)
	}
	settled := false
	deadline = time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		current, err := runs.Get(context.Background(), child)
		if err != nil {
			t.Fatal(err)
		}
		drained, err := h.s.ResourcesDrained(context.Background(), child)
		if err != nil {
			t.Fatal(err)
		}
		var liveReaders int
		if err := h.store.DB.QueryRow("SELECT COUNT(*) FROM project_activity WHERE run_id=? AND released_at IS NULL", child).Scan(&liveReaders); err != nil {
			t.Fatal(err)
		}
		if current.Status == run.StatusFailed && drained && liveReaders == 0 {
			settled = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !settled {
		t.Fatal("actual cancellation did not finish worker/resource/reader settlement")
	}
	verify, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	absent, err := h.b.VerifyResource(verify, resource.Ref)
	cancel()
	if err != nil || !absent {
		t.Fatal("read-only runtime/owner verification could not prove the exact cancelled CID absent", err)
	}
	probe, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	out, stderr, code, err := (execCommander{}).Run(probe, "docker", []string{"container", "inspect", "--format", "{{.Id}}", resource.Ref.ID}, "")
	probeActive := probe.Err() == nil
	cancel()
	if !probeActive || strings.TrimSpace(out) != "" || !nativeStandardExactMissing(err, code, stderr, resource.Ref.ID) {
		t.Fatal("independent exact CID absence proof failed")
	}
	records, err := h.s.Resources(context.Background(), child)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, record := range records {
		if record.ID == resource.ID {
			found = record.State == "drained" && record.Ref.AttemptID == child && record.Ref.ID == resource.Ref.ID && record.Ref.Owner == resource.Ref.Owner && record.Ref.RuntimeID == resource.Ref.RuntimeID
		}
	}
	if !found {
		t.Fatal("cancelled resource did not retain its exact drained ownership proof")
	}
	var producerAfter []byte
	if err = h.store.DB.QueryRow("SELECT ciphertext FROM run_retry_nodes WHERE attempt_id='root' AND node_key=? AND status='success' AND reusable=1", seedNode.Key).Scan(&producerAfter); err != nil || !bytes.Equal(producerCipher, producerAfter) {
		t.Fatal("cancelling child changed parent checkpoint")
	}
	var originalRun, originalRef string
	if err = h.store.DB.QueryRow("SELECT run_id,reference FROM run_artifacts WHERE id=?", original.ID).Scan(&originalRun, &originalRef); err != nil || originalRun != "root" || originalRef != original.Reference {
		t.Fatal("cancelling child changed original producer artifact")
	}
	assertProducerBytes()
	if drained, err := h.s.ResourcesDrained(context.Background(), "root"); err != nil || !drained {
		t.Fatal("child cancellation affected settled parent", err)
	}
	if driver.count("seed") != 1 || driver.count("work") != 2 || driver.count("after") != 0 {
		t.Fatal("cancel repeated parent or continued the cancelled child")
	}
	var inheritedStarted, inheritedFinished any
	if err = h.store.DB.QueryRow("SELECT started_at,finished_at FROM run_steps WHERE id=(SELECT step_id FROM run_retry_nodes WHERE attempt_id=? AND node_key=?)", child, seedNode.Key).Scan(&inheritedStarted, &inheritedFinished); err != nil || inheritedStarted != nil || inheritedFinished != nil {
		t.Fatal("inherited seed received fabricated execution timing")
	}
}
