package deploy

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/target"
)

func activeDeploys(t *testing.T, st *store.Store, runID string) int {
	t.Helper()
	var n int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM project_activity WHERE run_id=? AND kind='deploy' AND released_at IS NULL`, runID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func deploySignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("deployment barrier timed out")
	}
}

func TestIndependentDeployRetainsActivityAcrossIOAndDiagnosis(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		runs := run.New(st.DB)
		targets := &stubTarget{}
		server := seedServer(t, targets, "test")
		runID, artID := seedSuccessRunWithArtifact(t, st.DB, runs, run.ArtifactDist, "dist/x")
		rn, err := runs.Get(ctx, runID)
		if err != nil {
			t.Fatal(err)
		}
		ioEntered, ioExit, hookEntered, hookExit := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
		var once sync.Once
		targets.execFn = func(string, []string) (*target.ExecResult, error) {
			once.Do(func() { close(ioEntered); <-ioExit })
			return &target.ExecResult{ExitCode: 1}, nil
		}
		s := New(targets, runs, WithBatchDB(st.DB), WithDiagnoseHook(func(context.Context, string) { close(hookEntered); <-hookExit })).(*service)
		done := make(chan error, 1)
		ioReleased, hookReleased := false, false
		t.Cleanup(func() {
			if !ioReleased {
				close(ioExit)
			}
			if !hookReleased {
				close(hookExit)
			}
		})
		go func() {
			_, err := s.Deploy(ctx, DeployInput{RunID: runID, ArtifactID: artID, ServerIDs: []string{server.ID}, Config: map[string]string{"path": "/srv/test"}})
			done <- err
		}()
		deploySignal(t, ioEntered)
		if activeDeploys(t, st, runID) != 1 {
			t.Fatal("SSH has no durable reader hold")
		}
		if err := configstate.MutateProject(ctx, st.DB, rn.ProjectID, func(context.Context, configstate.DBTX) error { return nil }); !errors.Is(err, configstate.ErrBusy) {
			t.Fatalf("config mutation during SSH: %v", err)
		}
		close(ioExit)
		ioReleased = true
		deploySignal(t, hookEntered)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if activeDeploys(t, st, runID) != 1 {
			t.Fatal("return released asynchronous diagnosis reader")
		}
		close(hookExit)
		hookReleased = true
		deadline := time.Now().Add(5 * time.Second)
		for activeDeploys(t, st, runID) != 0 {
			if time.Now().After(deadline) {
				t.Fatal("exited diagnosis retained activity")
			}
			time.Sleep(time.Millisecond)
		}
		if err := configstate.CheckIdle(ctx, st.DB, rn.ProjectID); err != nil {
			t.Fatal(err)
		}
	})
}

func TestDeployNestedAdmissionAndCanceledReaderExit(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		runs := run.New(st.DB)
		runID, _ := seedSuccessRunWithArtifact(t, st.DB, runs, run.ArtifactDist, "dist/x")
		s := New(&stubTarget{}, runs, WithBatchDB(st.DB)).(*service)
		admitted, release, err := s.beginActivity(ctx, runID)
		if err != nil {
			t.Fatal(err)
		}
		nested, nestedRelease, err := s.beginActivity(admitted, runID)
		if err != nil {
			t.Fatal(err)
		}
		_ = nested
		if activeDeploys(t, st, runID) != 1 {
			t.Fatal("nested call created duplicate durable hold")
		}
		nestedRelease()
		if activeDeploys(t, st, runID) != 1 {
			t.Fatal("nested exit released outer reader")
		}
		// A new service and startup recovery cannot expire an active reader.
		if err := New(&stubTarget{}, runs, WithBatchDB(st.DB)).(*service).RecoverInterruptedBatches(ctx); err != nil {
			t.Fatal(err)
		}
		if activeDeploys(t, st, runID) != 1 {
			t.Fatal("recovery expired activity")
		}
		release()
		if activeDeploys(t, st, runID) != 0 {
			t.Fatal("outer exit did not release")
		}
		if _, _, err := s.beginActivity(ctx, "missing"); !errors.Is(err, ErrRunNotFound) {
			t.Fatal(err)
		}
	})
}

func TestDeployAmbiguousAdmissionRetainsExitedReaderEvidence(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		runs := run.New(st.DB)
		runID, _ := seedSuccessRunWithArtifact(t, st.DB, runs, run.ArtifactDist, "dist/x")
		rn, err := runs.Get(ctx, runID)
		if err != nil {
			t.Fatal(err)
		}
		s := New(&stubTarget{}, runs, WithBatchDB(st.DB)).(*service)
		a := &deployActivity{readers: 1, svc: s, id: uuid.NewString(), projectID: rn.ProjectID, runID: runID}
		if err := configstate.StartActivityReaders(a.id, s.activityOwner); err != nil {
			t.Fatal(err)
		}
		// The admission commit is durable, but its acknowledgement is lost. No
		// configuration/SSH reader ever starts and the caller follows its error exit.
		if err := configstate.WithWriteTxContext(ctx, st.DB, func(ctx context.Context, q configstate.DBTX) error {
			if err := configstate.LockEpochs(ctx, q, st.DB, configstate.EpochPolicy, configstate.EpochSecrets, configstate.EpochExecutor); err != nil {
				return err
			}
			_, err := configstate.AcquireActivityTx(ctx, q, st.DB, configstate.Activity{ID: a.id, ProjectID: rn.ProjectID, RunID: runID, OwnerEpoch: s.activityOwner, Kind: "deploy", Phase: "executing"})
			return err
		}); err != nil {
			t.Fatal(err)
		}
		a.admissionFailed()
		if activeDeploys(t, st, runID) != 1 {
			t.Fatal("ambiguous admission was claimed uncommitted/released")
		}
		maintenance := configstate.NewMaintenance(st.DB, &configstate.MaintenanceOptions{AuthorizeAdmin: func(context.Context, configstate.DBTX, string) error { return nil }})
		snapshot, err := maintenance.List(ctx, "admin")
		if err != nil {
			t.Fatal(err)
		}
		var item configstate.MaintenanceItem
		for _, i := range snapshot.Items {
			if i.ActivityID == a.id {
				item = i
			}
		}
		if !item.Recoverable || item.EvidenceID == "" {
			t.Fatal("ambiguous admission stranded a readerless durable hold")
		}
		if _, err := maintenance.Recover(ctx, "admin", configstate.MaintenanceRecoverRequest{ActivityID: item.ActivityID, ExpectedDigest: item.Digest, EvidenceID: item.EvidenceID}); err != nil {
			t.Fatal(err)
		}
		if activeDeploys(t, st, runID) != 0 {
			t.Fatal("proven exited admission could not be recovered")
		}
	})
}
