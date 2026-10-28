package runretry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/vault"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type memoryBlobs map[string][]byte

func (b memoryBlobs) Open(key string) (io.ReadCloser, error) {
	v, ok := b[key]
	if !ok {
		return nil, ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(v)), nil
}

type fixture struct {
	s          *Service
	db         *sql.DB
	root       string
	ex         Execution
	nodes      map[string]NodeRef
	snap       Snapshot
	dispatched atomic.Int32
}

func newFixture(t *testing.T, st *store.Store, opts ...Option) *fixture {
	t.Helper()
	ctx := context.Background()
	db := st.DB
	now := time.Now().UTC().Format(time.RFC3339)
	for _, q := range []string{
		`INSERT INTO credentials(id,name,type,scope,ciphertext,masked_value,created_at,updated_at) VALUES('cred','test','git_token','',X'00','m','old','old')`,
		`INSERT INTO projects(id,name,repo_url,default_branch,credential_id,created_at,updated_at) VALUES('project','test','https://example.invalid/p','main','cred','old','old')`,
	} {
		if _, e := db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := db.Exec("INSERT INTO pipeline_runs(id,project_id,status,created_at) VALUES('root','project','failed',?)", now); e != nil {
		t.Fatal(e)
	}
	f := &fixture{db: db, root: "root", nodes: map[string]NodeRef{}}
	var key [32]byte
	key[0] = 42
	child := func(ctx context.Context, q configstate.DBTX, in ChildInput) error {
		if _, e := q.ExecContext(ctx, "INSERT INTO pipeline_runs(id,project_id,status,created_at) VALUES(?,?,'queued',?)", in.ID, in.ProjectID, in.CreatedAt.Format(time.RFC3339)); e != nil {
			return e
		}
		_, e := configstate.AcquireActivityTx(ctx, q, db, configstate.Activity{ID: in.ID, ProjectID: in.ProjectID, RunID: in.ID, OwnerEpoch: "test-owner", Kind: "run", Phase: "queued"})
		return e
	}
	reject := func(ctx context.Context, id string) error {
		q := configstate.Queryer(ctx, db)
		if _, e := q.ExecContext(ctx, "UPDATE pipeline_runs SET status='failed' WHERE id=? AND status='queued'", id); e != nil {
			return e
		}
		return configstate.ReleaseActivityTx(ctx, q, id, "test-owner")
	}
	base := []Option{WithChildCreator(child), WithDispatch(func(context.Context, string) error { f.dispatched.Add(1); return nil }), WithRejectedChild(reject)}
	base = append(base, opts...)
	f.s = New(db, vault.New(db, &key), base...)
	f.snap = Snapshot{
		SchemaVersion: 1, ProjectID: "project", RequestedRef: "main", CommitSHA: strings.Repeat("a", 40),
		Trigger: Trigger{Type: "manual", Branch: "main"}, Settings: map[string]any{"private": "DO_NOT_STORE_CLEAR_TEXT"},
		Spec: pipeline.Spec{Stages: []pipeline.Stage{
			{ID: "stage", Name: "Build", Kind: "build", Jobs: []pipeline.Job{
				{ID: "a", Name: "A", Type: "script"},
				{ID: "b", Name: "B", Type: "script", Needs: []string{"a"}},
				{ID: "c", Name: "C", Type: "script", Needs: []string{"b"}},
			}},
		}},
	}
	if e := f.s.SaveManifest(ctx, f.root, f.snap); e != nil {
		t.Fatal(e)
	}
	execCtx, e := f.s.Prepare(ctx, f.root)
	if e != nil {
		t.Fatal(e)
	}
	f.ex, _ = ExecutionFrom(execCtx)
	if f.ex == nil {
		t.Fatal("missing execution")
	}
	for _, id := range []string{"a", "b", "c"} {
		n, ok := f.ex.Node("stage", id)
		if !ok {
			t.Fatal(id)
		}
		f.nodes[id] = n
	}
	for _, row := range []struct{ id, status string }{{"a", "success"}, {"b", "failed"}, {"c", "skipped"}} {
		o := Outcome{Status: row.status, ExecutionMode: "real", Complete: true, Reusable: true, ExternalOutcome: "none"}
		if row.id == "a" {
			o.Outputs.Env = map[string]string{"TOKEN": "DYNAMIC_TOKEN_NEVER_PLAINTEXT"}
		}
		if e = f.ex.Finish(ctx, f.nodes[row.id], o); e != nil {
			t.Fatal(e)
		}
	}
	return f
}
func (f *fixture) plan(t *testing.T) *Plan {
	t.Helper()
	p, e := f.s.Plan(context.Background(), f.root, []string{f.nodes["b"].Key})
	if e != nil || !p.CanSubmit {
		t.Fatalf("plan=%+v err=%v", p, e)
	}
	return p
}
func submitFor(p *Plan, id string) SubmitInput {
	return SubmitInput{Scope: "user:test", RequestID: id, BaseAttemptID: p.BaseAttemptID, BaseRevision: p.BaseRevision, PlanFingerprint: p.PlanFingerprint, TargetNodeIDs: p.TargetNodeIDs}
}

func TestPersistenceIdempotencyAndFrozenInputs(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		f := newFixture(t, st)
		ctx := context.Background()
		p := f.plan(t)
		in := submitFor(p, "request-1")
		r, e := f.s.Submit(ctx, f.root, in)
		if e != nil || r.State != "accepted" {
			t.Fatal(r, e)
		}
		if f.dispatched.Load() != 1 {
			t.Fatal("dispatch", f.dispatched.Load())
		}
		again, e := f.s.Submit(ctx, f.root, in)
		if e != nil || again.AttemptRunID != r.AttemptRunID || f.dispatched.Load() != 1 {
			t.Fatal(again, e)
		}
		changed := in
		changed.TargetNodeIDs = []string{f.nodes["c"].Key}
		if _, e = f.s.Submit(ctx, f.root, changed); !errors.Is(e, ErrConflict) {
			t.Fatal("different input", e)
		}
		if _, e = f.s.Submit(ctx, f.root, submitFor(p, "stale-request")); !errors.Is(e, ErrConflict) {
			t.Fatal("stale head", e)
		}
		execCtx, e := f.s.Prepare(ctx, r.AttemptRunID)
		if e != nil {
			t.Fatal(e)
		}
		ex, _ := ExecutionFrom(execCtx)
		if ex.Decision(f.nodes["a"]).Action != "reuse" {
			t.Fatal("A reexecuted")
		}
		inputs, e := ex.Inputs(ctx, f.nodes["b"])
		if e != nil || inputs.Env["TOKEN"] != "DYNAMIC_TOKEN_NEVER_PLAINTEXT" {
			t.Fatal(inputs, e)
		}
		snap := ex.Snapshot()
		snap.Settings["private"] = "mutated"
		if ex.Snapshot().Settings["private"] != "DO_NOT_STORE_CLEAR_TEXT" {
			t.Fatal("snapshot aliased")
		}
		var cipher []byte
		if e = st.DB.QueryRow("SELECT ciphertext FROM run_retry_manifests WHERE root_id='root'").Scan(&cipher); e != nil || bytes.Contains(cipher, []byte("DO_NOT_STORE_CLEAR_TEXT")) {
			t.Fatal("plaintext manifest", e)
		}
		rows, e := st.DB.Query("SELECT ciphertext FROM run_retry_nodes WHERE ciphertext IS NOT NULL")
		if e != nil {
			t.Fatal(e)
		}
		for rows.Next() {
			if e = rows.Scan(&cipher); e != nil || bytes.Contains(cipher, []byte("DYNAMIC_TOKEN_NEVER_PLAINTEXT")) {
				t.Fatal("plaintext checkpoint", e)
			}
		}
		_ = rows.Close()
		detail, e := f.s.Context(ctx, r.AttemptRunID)
		if e != nil || detail.HeadAttemptID != r.AttemptRunID {
			t.Fatal(detail, e)
		}
		attempts, e := f.s.Attempts(ctx, f.root, 1, 20)
		if e != nil || attempts.Total != 2 {
			t.Fatal(attempts, e)
		}
		var original string
		_ = st.DB.QueryRow("SELECT status FROM pipeline_runs WHERE id='root'").Scan(&original)
		if original != "failed" {
			t.Fatal("rewrote parent")
		}
	})
}

func TestDrainGateAndUnknownAreNotReplayed(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		f := newFixture(t, st)
		ctx := context.Background()
		if _, e := st.DB.Exec("INSERT INTO project_activity(activity_id,project_id,run_id,owner_epoch,kind,phase,created_at,heartbeat_at) VALUES('root-hold','project','root','foreign','run','finalizing',0,0)"); e != nil {
			t.Fatal(e)
		}
		p, e := f.s.Plan(ctx, f.root, []string{f.nodes["b"].Key})
		if e != nil || p.CanSubmit {
			t.Fatal(p, e)
		}
		if _, e = st.DB.Exec("UPDATE project_activity SET released_at=1 WHERE activity_id='root-hold'"); e != nil {
			t.Fatal(e)
		}
		p = f.plan(t)
		f.s.dispatch = func(context.Context, string) error { return errors.New("receipt lost") }
		r, e := f.s.Submit(ctx, f.root, submitFor(p, "unknown-request"))
		if e != nil || r.State != "outcome_unknown" {
			t.Fatal(r, e)
		}
		if _, e = f.s.Prepare(ctx, r.AttemptRunID); !errors.Is(e, ErrUnknown) {
			t.Fatal("unknown ran", e)
		}
		if e = f.s.ReconcileStartup(ctx); e != nil {
			t.Fatal(e)
		}
		same, e := f.s.Submit(ctx, f.root, submitFor(p, "unknown-request"))
		if e != nil || same.AttemptRunID != r.AttemptRunID {
			t.Fatal(same, e)
		}
	})
}

func TestDispatchRollbackAndDefiniteRejection(t *testing.T) {
	t.Run("rollback", func(t *testing.T) {
		f := newFixture(t, storetest.OpenForDialect(t, store.SQLite))
		p := f.plan(t)
		f.s.child = func(ctx context.Context, q configstate.DBTX, in ChildInput) error {
			_, _ = q.ExecContext(ctx, "INSERT INTO pipeline_runs(id,project_id,status,created_at) VALUES(?,'project','queued','old')", in.ID)
			return errors.New("injected transaction failure")
		}
		if _, e := f.s.Submit(context.Background(), f.root, submitFor(p, "rollback")); e == nil {
			t.Fatal("expected failure")
		}
		var n int
		_ = f.db.QueryRow("SELECT COUNT(*) FROM pipeline_runs").Scan(&n)
		if n != 1 || f.dispatched.Load() != 0 {
			t.Fatal(n, f.dispatched.Load())
		}
	})
	t.Run("known-rejection", func(t *testing.T) {
		f := newFixture(t, storetest.OpenForDialect(t, store.SQLite))
		p := f.plan(t)
		f.s.dispatch = func(context.Context, string) error { return NewDispatchRejected(errors.New("full")) }
		r, e := f.s.Submit(context.Background(), f.root, submitFor(p, "rejected"))
		if e != nil || r.State != "failed" || r.ReasonCode != "dispatch_rejected" {
			t.Fatal(r, e)
		}
		var hold int
		_ = f.db.QueryRow("SELECT COUNT(*) FROM project_activity WHERE run_id=? AND released_at IS NULL", r.AttemptRunID).Scan(&hold)
		if hold != 0 {
			t.Fatal("rejected hold", hold)
		}
		p, e = f.s.Plan(context.Background(), r.AttemptRunID, []string{f.nodes["b"].Key})
		if e != nil || !p.CanSubmit {
			t.Fatal("rejected head not recoverable", p, e)
		}
	})
}

func TestLineageCompetitionOnlyOneDispatch(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		f := newFixture(t, st)
		p := f.plan(t)
		var wg sync.WaitGroup
		results := make(chan error, 2)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				_, e := f.s.Submit(context.Background(), f.root, submitFor(p, fmt.Sprintf("r-%d", i)))
				results <- e
			}(i)
		}
		wg.Wait()
		close(results)
		good, conflicts := 0, 0
		for e := range results {
			if e == nil {
				good++
			} else if errors.Is(e, ErrConflict) {
				conflicts++
			} else {
				t.Fatal(e)
			}
		}
		if good != 1 || conflicts != 1 || f.dispatched.Load() != 1 {
			t.Fatal(good, conflicts, f.dispatched.Load())
		}
	})
}

func TestEnvelopeBindingTamperAndCheckpointFailure(t *testing.T) {
	f := newFixture(t, storetest.OpenForDialect(t, store.SQLite))
	ctx := context.Background()
	var cipher []byte
	_ = f.db.QueryRow("SELECT ciphertext FROM run_retry_nodes WHERE attempt_id='root' AND node_key=?", f.nodes["a"].Key).Scan(&cipher)
	var o Outcome
	if e := f.s.open(cipher, "checkpoint", "root", "root:"+f.nodes["b"].Key, &o); !errors.Is(e, ErrUnavailable) {
		t.Fatal("cross-node accepted", e)
	}
	cipher[len(cipher)-1] ^= 1
	if e := f.s.open(cipher, "checkpoint", "root", "root:"+f.nodes["a"].Key, &o); !errors.Is(e, ErrUnavailable) {
		t.Fatal("tamper accepted", e)
	}
	if e := f.s.SaveManifest(ctx, f.root, f.snap); e != nil {
		t.Fatal(e)
	}
	changed := f.snap
	changed.RequestedRef = "future"
	if e := f.s.SaveManifest(ctx, f.root, changed); !errors.Is(e, ErrConflict) {
		t.Fatal("manifest mutable", e)
	}
	var wrong [32]byte
	wrong[0] = 99
	other := New(f.db, vault.New(f.db, &wrong))
	if _, e := other.Context(ctx, f.root); !errors.Is(e, ErrUnavailable) {
		t.Fatal("wrong key", e)
	}
	// A budget/key failure only loses recoverability. The completed business result
	// remains success and the existing parent run status is untouched.
	f.s.sealer = nil
	control, _ := f.ex.ControlNode("stage", "init")
	e := f.ex.Control(ctx, control, Outcome{Status: "success", ExecutionMode: "real", Complete: true, Reusable: true})
	if !errors.Is(e, ErrCheckpoint) {
		t.Fatal(e)
	}
	var status string
	var complete int
	_ = f.db.QueryRow("SELECT status,complete FROM run_retry_nodes WHERE attempt_id='root' AND node_key=?", control.Key).Scan(&status, &complete)
	if status != "success" || complete != 0 {
		t.Fatal(status, complete)
	}
}

func TestGraphRawMatrixIdentityAndEnvOrder(t *testing.T) {
	spec := pipeline.Spec{Stages: []pipeline.Stage{{ID: "matrix", Name: "M", Matrix: map[string][]string{"os": {"linux", "windows"}, "version": {"1", "2"}}, Jobs: []pipeline.Job{{ID: "a", Type: "build_image", Config: map[string]any{"artifactType": " image "}}, {ID: "b", Type: "script", Needs: []string{"a"}}, {ID: "c", Type: "push_image", Needs: []string{"b"}}}}, {ID: "end", Name: "End", Needs: []string{"matrix"}, Jobs: []pipeline.Job{{ID: "notify", Type: "notify"}}}}}
	g, e := Compile(spec)
	if e != nil {
		t.Fatal(e)
	}
	seen := map[string]bool{}
	cells := 0
	for _, n := range g.Nodes {
		if seen[n.Key] {
			t.Fatal("duplicate tuple key")
		}
		seen[n.Key] = true
		if n.JobID == "a" {
			cells++
		}
	}
	if cells != 4 {
		t.Fatal(cells)
	}
	collision := spec
	collision.Stages = []pipeline.Stage{{ID: "m", Matrix: map[string][]string{"axis": {"a b", "a/b"}}, Jobs: []pipeline.Job{{ID: "j", Type: "script"}}}}
	if _, e = Compile(collision); !errors.Is(e, ErrInvalid) {
		t.Fatal("ambiguous legacy IDs accepted", e)
	}
	parallel := pipeline.Spec{Stages: []pipeline.Stage{{ID: "s", Jobs: []pipeline.Job{{ID: "a"}, {ID: "b"}}}}}
	g, e = Compile(parallel)
	if e != nil {
		t.Fatal(e)
	}
	for _, edge := range g.Edges {
		var from, to NodeRef
		for _, n := range g.Nodes {
			if n.Key == edge.From {
				from = n
			}
			if n.Key == edge.To {
				to = n
			}
		}
		if from.JobID != "" && to.JobID != "" {
			t.Fatal("invented job order")
		}
	}
}

func TestSourceBindingAndFileVerification(t *testing.T) {
	f := newFixture(t, storetest.OpenForDialect(t, store.SQLite))
	ctx := context.Background()
	if e := f.ex.BindCommit(ctx, strings.Repeat("a", 40)); e != nil {
		t.Fatal(e)
	}
	if e := f.ex.BindCommit(ctx, strings.Repeat("b", 40)); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	data := []byte("verified artifact")
	key := digestBytes(data)
	f.s.blobs = memoryBlobs{key: data}
	ref := FileRef{Key: key, SHA256: key, Size: int64(len(data)), Path: "dist/app.jar"}
	if e := f.s.validateInputs(Inputs{Files: []FileRef{ref}}); e != nil {
		t.Fatal(e)
	}
	if e := f.s.verifyFiles(ctx, []FileRef{ref}); e != nil {
		t.Fatal(e)
	}
	f.s.blobs = memoryBlobs{key: []byte("tampered")}
	if e := f.s.verifyFiles(ctx, []FileRef{ref}); !errors.Is(e, ErrUnavailable) {
		t.Fatal(e)
	}
	ref.Path = "../outside"
	if e := f.s.validateInputs(Inputs{Files: []FileRef{ref}}); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
}
func digestBytes(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func TestDirectEnvAndTransitiveImageInputs(t *testing.T) {
	f := newFixture(t, storetest.OpenForDialect(t, store.SQLite))
	ctx := context.Background()
	snap := f.snap
	snap.Spec = pipeline.Spec{Stages: []pipeline.Stage{{ID: "s", Jobs: []pipeline.Job{
		{ID: "producer", Type: "build_image"}, {ID: "test", Type: "script", Needs: []string{"producer"}},
		{ID: "other", Type: "script"}, {ID: "push", Type: "push_image", Needs: []string{"test"}},
		{ID: "merge", Type: "script", Needs: []string{"other", "test"}},
	}}}}
	if _, e := f.db.Exec("INSERT INTO pipeline_runs(id,project_id,status,created_at) VALUES('inputs','project','failed','old')"); e != nil {
		t.Fatal(e)
	}
	if e := f.s.SaveManifest(ctx, "inputs", snap); e != nil {
		t.Fatal(e)
	}
	prepared, e := f.s.Prepare(ctx, "inputs")
	if e != nil {
		t.Fatal(e)
	}
	ex, _ := ExecutionFrom(prepared)
	for _, id := range []string{"producer", "test", "other"} {
		n, _ := ex.Node("s", id)
		o := Outcome{Status: "success", ExecutionMode: "real", Complete: true, Reusable: true, ExternalOutcome: "none", Outputs: Inputs{Env: map[string]string{"KEY": id}}}
		if id == "producer" {
			o.Outputs.Env["PRODUCER_PRIVATE"] = "do-not-propagate-transitively"
			o.Outputs.Images = map[string]ImageRef{"image": {Ref: "local:test", ImageID: "sha256:" + strings.Repeat("b", 64), RuntimeID: "test-runtime"}}
		}
		if e = ex.Finish(ctx, n, o); e != nil {
			t.Fatal(e)
		}
	}
	push, _ := ex.Node("s", "push")
	in, e := ex.Inputs(ctx, push)
	if e != nil || in.Env["KEY"] != "test" || in.Env["PRODUCER_PRIVATE"] != "" || in.Images["image"].ProducerNodeKey == "" {
		t.Fatal("lost transitive image or leaked env", in, e)
	}
	merge, _ := ex.Node("s", "merge")
	in, e = ex.Inputs(ctx, merge)
	if e != nil || in.Env["KEY"] != "test" {
		t.Fatal("declared overwrite order", in, e)
	}
}

func TestRestartOwnsUnknownOriginalNodeAndReferencesSurviveParent(t *testing.T) {
	t.Run("original-node", func(t *testing.T) {
		f := newFixture(t, storetest.OpenForDialect(t, store.SQLite))
		ctx := context.Background()
		init, _ := f.ex.ControlNode("stage", "init")
		if e := f.ex.Begin(ctx, init); e != nil {
			t.Fatal(e)
		}
		next := New(f.db, f.s.sealer)
		if e := next.ReconcileStartup(ctx); e != nil {
			t.Fatal(e)
		}
		p, e := next.Plan(ctx, f.root, []string{f.nodes["b"].Key})
		if e != nil || p.CanSubmit {
			t.Fatal("unknown original control replayed", p, e)
		}
	})
	t.Run("references", func(t *testing.T) {
		f := newFixture(t, storetest.OpenForDialect(t, store.SQLite))
		ctx := context.Background()
		if _, e := f.db.Exec("INSERT INTO run_steps(id,run_id,name,status,ordinal) VALUES('source-step','root','A','success',0)"); e != nil {
			t.Fatal(e)
		}
		if e := f.s.BindStep(ctx, f.root, f.nodes["a"].Key, "source-step", 0); e != nil {
			t.Fatal(e)
		}
		p := f.plan(t)
		r, e := f.s.Submit(ctx, f.root, submitFor(p, "keep-proof"))
		if e != nil {
			t.Fatal(e)
		}
		c, e := f.s.Context(ctx, r.AttemptRunID)
		if e != nil {
			t.Fatal(e)
		}
		found := false
		for _, n := range c.Nodes {
			if n.Node.Key == f.nodes["a"].Key {
				found = n.SourceStepID == "source-step" && n.SourceOrdinal == 0
			}
		}
		if !found {
			t.Fatal("lost stable source step")
		}
		var pins int
		if e = f.db.QueryRow("SELECT COUNT(*) FROM run_retry_refs WHERE owner_attempt_id=? AND producer_attempt_id='root' AND released_at IS NULL", r.AttemptRunID).Scan(&pins); e != nil || pins == 0 {
			t.Fatal(pins, e)
		}
		// Direct deletion deliberately bypasses retention: this asserts only the new
		// schema's non-cascade guarantee, not acceptance of retention's pin protocol.
		if _, e = f.db.Exec("DELETE FROM pipeline_runs WHERE id='root'"); e != nil {
			t.Fatal(e)
		}
		if _, e = f.s.Context(ctx, r.AttemptRunID); e != nil {
			t.Fatal("parent cascade broke child evidence", e)
		}
	})
}

func TestFirstSourceBindingAndInterruptedNodeRemainDurable(t *testing.T) {
	f := newFixture(t, storetest.OpenForDialect(t, store.SQLite))
	ctx := context.Background()
	snap := f.snap
	snap.CommitSHA = ""
	if _, e := f.db.Exec("INSERT INTO pipeline_runs(id,project_id,status,created_at) VALUES('first-source','project','failed','old')"); e != nil {
		t.Fatal(e)
	}
	if e := f.s.SaveManifest(ctx, "first-source", snap); e != nil {
		t.Fatal(e)
	}
	prepared, e := f.s.Prepare(ctx, "first-source")
	if e != nil {
		t.Fatal(e)
	}
	ex, _ := ExecutionFrom(prepared)
	if e = ex.BindCommit(ctx, strings.Repeat("b", 40)); e != nil {
		t.Fatal(e)
	}
	if e = ex.BindCommit(ctx, strings.Repeat("c", 40)); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	l, e := f.s.load(ctx, "first-source")
	if e != nil || !l.sourceConflict || l.snapshot.CommitSHA != strings.Repeat("b", 40) {
		t.Fatal("source moved or conflict disappeared", l.snapshot.CommitSHA, l.sourceConflict, e)
	}
	n, _ := f.ex.ControlNode("stage", "init")
	if e = f.ex.Begin(ctx, n); e != nil {
		t.Fatal(e)
	}
	if e = f.s.Complete(ctx, f.root, "failed"); e != nil {
		t.Fatal(e)
	}
	c, e := f.s.Context(ctx, f.root)
	if e != nil || !c.OutcomeUnknown {
		t.Fatal(c, e)
	}
	a, e := f.s.Attempts(ctx, f.root, 1, 20)
	if e != nil || !a.Items[0].OutcomeUnknown || a.Items[0].Status != "failed" {
		t.Fatal(a, e)
	}
	if e = f.s.Settle(ctx, f.root); !errors.Is(e, ErrUnknown) {
		t.Fatal("interrupted operation released", e)
	}
}
