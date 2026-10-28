package build

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/artifactstore"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/dagrun"
	"github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/runretry"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/target"
	"github.com/huangchengsir/pipewright/internal/vault"
)

type retryEvidenceSink struct {
	mu      sync.Mutex
	decls   []run.StepDecl
	running []int
	arts    []run.Artifact
	logs    []string
	masker  *mask.Masker
}

func (s *retryEvidenceSink) Plan(_ context.Context, d []run.StepDecl) error { s.decls = d; return nil }
func (s *retryEvidenceSink) StepRunning(_ context.Context, n int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.decls[n].SeedStatus != "" {
		return errors.New("inherited node started")
	}
	s.running = append(s.running, n)
	return nil
}
func (s *retryEvidenceSink) StepDone(_ context.Context, n int, _ string) error {
	if s.decls[n].SeedStatus != "" {
		return errors.New("inherited node finished")
	}
	return nil
}
func (s *retryEvidenceSink) Log(_ context.Context, _ string, _ int, line string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.masker != nil {
		line = s.masker.Scrub(line)
	}
	s.logs = append(s.logs, line)
	return nil
}
func (s *retryEvidenceSink) SetFailureLog(context.Context, string) error         { return nil }
func (s *retryEvidenceSink) SetSpecSource(context.Context, run.SpecSource) error { return nil }
func (s *retryEvidenceSink) EmitArtifact(_ context.Context, a run.Artifact) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.arts = append(s.arts, a)
	return nil
}

type retryCountingDriver struct {
	Driver
	mu             sync.Mutex
	counts         map[string]int
	fail           map[string]bool
	hidden         bool
	inspectMissing bool
}

func (d *retryCountingDriver) Binary() string { return "fixture" }
func (d *retryCountingDriver) RunToolchain(_ context.Context, _ string, ws, _ string, env, cmd []string, _ pipeline.Resource, onLine func(string, string)) (int, error) {
	name := strings.TrimSpace(strings.TrimPrefix(cmd[2], "set -e\n"))
	for _, v := range env {
		if strings.HasPrefix(v, "MATRIX_OS=") {
			name += "/" + strings.TrimPrefix(v, "MATRIX_OS=")
		}
	}
	d.mu.Lock()
	d.counts[name]++
	fail := d.fail[name]
	d.mu.Unlock()
	if fail {
		return 1, nil
	}
	switch strings.Split(name, "/")[0] {
	case "A":
		_ = os.WriteFile(filepath.Join(ws, "out.txt"), []byte("persisted payload"), 0644)
		_ = os.WriteFile(filepath.Join(ws, pipewrightEnvFileName), []byte("OUTPUT_TOKEN=private-dynamic-value\n"), 0644)
	case "implicit":
		_ = os.WriteFile(filepath.Join(ws, "scratch.txt"), []byte("temporary handoff"), 0644)
	case "consume":
		if data, e := os.ReadFile(filepath.Join(ws, "scratch.txt")); e != nil || string(data) != "temporary handoff" {
			return 1, nil
		}
	case "B":
		if data, e := os.ReadFile(filepath.Join(ws, "out.txt")); e != nil || string(data) != "persisted payload" {
			return 1, nil
		}
		found := false
		for _, v := range env {
			found = found || v == "OUTPUT_TOKEN=private-dynamic-value"
			if v == "OUTPUT_TOKEN=private-dynamic-value" {
				onLine("stdout", v)
			}
		}
		if !found {
			return 1, nil
		}
	}
	return 0, nil
}
func (d *retryCountingDriver) Build(context.Context, string, string, string, []string, []string, func(string, string)) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.counts["image"]++
	return 0, nil
}
func (d *retryCountingDriver) Tag(context.Context, string, string, func(string, string)) (int, error) {
	return 0, nil
}
func (d *retryCountingDriver) Push(context.Context, string, func(string, string)) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.counts["push"]++
	if d.fail["push"] {
		return 1, nil
	}
	return 0, nil
}
func (d *retryCountingDriver) InspectImage(context.Context, string) (string, int64, error) {
	return "sha256:" + strings.Repeat("d", 64), 100, nil
}
func (d *retryCountingDriver) ImageIdentity(_ context.Context, ref string) (runretry.ImageRef, error) {
	if d.inspectMissing {
		return runretry.ImageRef{}, runretry.ErrUnavailable
	}
	return runretry.ImageRef{Ref: ref, ImageID: "sha256:" + strings.Repeat("e", 64), RuntimeID: "fixture-runtime", ConfigSHA: strings.Repeat("f", 64)}, nil
}

type retryHarness struct {
	s         *runretry.Service
	store     *store.Store
	b         *Builder
	spec      pipeline.Spec
	sink      *retryEvidenceSink
	driver    *retryCountingDriver
	artRoot   string
	settleErr error
	executor  dagrun.StageExecutor
	gate      dagrun.GateFunc
}

func retryJob(id string, needs ...string) pipeline.Job {
	return pipeline.Job{ID: id, Name: id, Type: "script", Needs: needs, Config: map[string]any{"image": "fixture:1", "commands": id}}
}
func retrySpec(jobs ...pipeline.Job) pipeline.Spec {
	return pipeline.Spec{Stages: []pipeline.Stage{{ID: "stage", Name: "stage", Kind: "build", Jobs: jobs}}}
}
func newRetryHarness(t *testing.T, spec pipeline.Spec, modify ...func(*runretry.Snapshot)) *retryHarness {
	t.Helper()
	st := storetest.OpenForDialect(t, store.SQLite)
	db := st.DB
	for _, q := range []string{`INSERT INTO credentials(id,name,type,scope,ciphertext,masked_value,created_at,updated_at) VALUES('cred','fixture','git_token','',X'00','','old','old')`, `INSERT INTO projects(id,name,repo_url,default_branch,credential_id,created_at,updated_at) VALUES('p1','fixture','https://example.invalid/r','main','cred','old','old')`, `INSERT INTO pipeline_runs(id,project_id,status,created_at) VALUES('root','p1','running','old')`} {
		if _, e := db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	artRoot := t.TempDir()
	art, e := artifactstore.New(artRoot)
	if e != nil {
		t.Fatal(e)
	}
	driver := &retryCountingDriver{counts: map[string]int{}, fail: map[string]bool{}}
	b := newDAGTestBuilder(driver, resolvedEvidenceCloner{full: checkoutTestSHA, short: checkoutTestSHA[:7]})
	b.artStore = art
	var key [32]byte
	key[0] = 17
	svc := runretry.New(db, vault.New(db, &key), runretry.WithBlobStore(art), runretry.WithImageVerifier(b.ValidateCheckpointImage), runretry.WithDispatch(func(context.Context, string) error { return nil }), runretry.WithChildCreator(func(ctx context.Context, q configstate.DBTX, in runretry.ChildInput) error {
		_, e := q.ExecContext(ctx, "INSERT INTO pipeline_runs(id,project_id,status,created_at) VALUES(?,?,'queued',?)", in.ID, in.ProjectID, time.Now().UTC().Format(time.RFC3339))
		return e
	}))
	projectBytes, _ := json.Marshal(project.Project{ID: "p1", Name: "fixture", RepoURL: "https://example.invalid/r"})
	var frozenProject map[string]any
	_ = json.Unmarshal(projectBytes, &frozenProject)
	settingsBytes, _ := json.Marshal(pipeline.Settings{Build: pipeline.BuildConfig{ArtifactType: "image"}, Environments: []pipeline.Environment{{Name: "test", ImageRegistry: pipeline.ImageRegistry{Type: "docker", URL: "registry.invalid/example"}}}})
	var frozenSettings map[string]any
	_ = json.Unmarshal(settingsBytes, &frozenSettings)
	snapshot := runretry.Snapshot{ProjectID: "p1", Spec: spec, Project: frozenProject, Settings: frozenSettings, RequestedRef: "main", CommitSHA: checkoutTestSHA, Trigger: runretry.Trigger{Type: "manual", Branch: "main", Environment: "test"}}
	for _, fn := range modify {
		fn(&snapshot)
	}
	if e := svc.SaveManifest(context.Background(), "root", snapshot); e != nil {
		t.Fatal(e)
	}
	return &retryHarness{s: svc, store: st, b: b, spec: spec, sink: &retryEvidenceSink{}, driver: driver, artRoot: artRoot}
}
func (h *retryHarness) execute(t *testing.T, id string, observeBlockedSettlement ...bool) error {
	t.Helper()
	ctx, e := h.s.Prepare(context.Background(), id)
	if e != nil {
		t.Fatalf("retry prepare: %v", e)
	}
	m := mask.NewMasker()
	h.sink = &retryEvidenceSink{masker: m}
	ctx = mask.WithSecretRegistrar(ctx, m.RegisterExactSecret)
	loader := dagrun.SpecLoaderFunc(func(context.Context, string) (*pipeline.Config, error) {
		return nil, errors.New("mutable loader must not run")
	})
	executor := h.executor
	if executor == nil {
		executor = NewStageExecutor(h.b, nil)
	}
	runner := dagrun.New(loader, dagrun.WithStageExecutor(executor), dagrun.WithGate(h.gate))
	err := runner.Run(ctx, &run.Run{ID: id, ProjectID: "p1", Trigger: run.Trigger{Branch: "WRONG"}}, h.sink)
	status := "success"
	if err != nil {
		status = "failed"
	}
	if _, e = h.store.DB.Exec("UPDATE pipeline_runs SET status=? WHERE id=?", status, id); e != nil {
		t.Fatalf("retry persist terminal: %v", e)
	}
	if e = h.s.Complete(ctx, id, status); e != nil {
		t.Fatalf("retry complete: %v", e)
	}
	h.settleErr = h.s.Settle(ctx, id)
	if e = h.settleErr; e != nil && !errors.Is(e, runretry.ErrUnknown) && !(len(observeBlockedSettlement) == 1 && observeBlockedSettlement[0] && errors.Is(e, runretry.ErrBlocked)) {
		t.Fatalf("retry settle: %v", e)
	}
	return err
}
func (h *retryHarness) node(t *testing.T, id, stage, job string) runretry.NodeRef {
	ctx, e := h.s.Context(context.Background(), id)
	if e != nil {
		t.Fatal(e)
	}
	for _, n := range ctx.Graph.Nodes {
		if n.ExecutionStageID == stage && n.ExecutionJobID == job && n.ControlKey == "" {
			return n
		}
	}
	t.Fatal("missing node", stage, job)
	return runretry.NodeRef{}
}
func (h *retryHarness) submit(t *testing.T, id string, targets []string, rebuild []string) string {
	t.Helper()
	p, e := h.s.Plan(context.Background(), id, targets, runretry.PlanOptions{RebuildNodeIDs: rebuild})
	if e != nil || !p.CanSubmit {
		t.Fatalf("plan err=%v plan=%+v", e, p)
	}
	r, e := h.s.Submit(context.Background(), id, runretry.SubmitInput{Scope: "fixture", RequestID: "request-" + id, BaseAttemptID: p.BaseAttemptID, BaseRevision: p.BaseRevision, PlanFingerprint: p.PlanFingerprint, TargetNodeIDs: targets, RebuildNodeIDs: rebuild})
	if e != nil {
		t.Fatal(e)
	}
	return r.AttemptRunID
}
func TestRecoveryPersistsFilesAndEnvWithoutReexecutingProducer(t *testing.T) {
	a := retryJob("A")
	a.Config["artifacts"] = `[{"path":"out.txt","name":"payload"}]`
	h := newRetryHarness(t, retrySpec(a, retryJob("B", "A"), retryJob("C", "B")))
	h.driver.fail["B"] = true
	if h.execute(t, "root") == nil {
		t.Fatal("expected initial failure")
	}
	h.driver.fail["B"] = false
	b := h.node(t, "root", "stage", "B")
	child := h.submit(t, "root", []string{b.Key}, nil)
	if e := h.execute(t, child); e != nil {
		t.Fatal(e)
	}
	if h.driver.counts["A"] != 1 || h.driver.counts["B"] != 2 || h.driver.counts["C"] != 1 {
		t.Fatal(h.driver.counts)
	}
	if h.sink.decls[0].SeedStatus != "success" {
		t.Fatal("producer not inherited")
	}
	for _, ordinal := range h.sink.running {
		if ordinal == 0 {
			t.Fatal("fake inherited execution time")
		}
	}
	var ciphertext []byte
	if e := h.store.DB.QueryRow("SELECT ciphertext FROM run_retry_nodes WHERE attempt_id='root' AND node_key=?", h.node(t, "root", "stage", "A").Key).Scan(&ciphertext); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(ciphertext), "private-dynamic-value") {
		t.Fatal("dynamic secret stored clear")
	}
	if strings.Contains(strings.Join(h.sink.logs, "\n"), "private-dynamic-value") {
		t.Fatal("restored dynamic value leaked into logs")
	}
}
func TestRecoveryTwoFailuresRepairSeparatelyAndJoinWaits(t *testing.T) {
	a := retryJob("A")
	a.Config["artifacts"] = `[{"path":"out.txt"}]`
	h := newRetryHarness(t, retrySpec(a, retryJob("B", "A"), retryJob("X", "A"), retryJob("join", "B", "X")))
	h.driver.fail["B"], h.driver.fail["X"] = true, true
	if h.execute(t, "root") == nil {
		t.Fatal("expected failure")
	}
	h.driver.fail["B"] = false
	b := h.node(t, "root", "stage", "B")
	child := h.submit(t, "root", []string{b.Key}, nil)
	if h.execute(t, child) == nil {
		t.Fatal("unselected failure hidden")
	}
	if h.driver.counts["join"] != 0 {
		t.Fatal("join ran early")
	}
	h.driver.fail["X"] = false
	x := h.node(t, child, "stage", "X")
	next := h.submit(t, child, []string{x.Key}, nil)
	if e := h.execute(t, next); e != nil {
		t.Fatal(e)
	}
	if h.driver.counts["A"] != 1 || h.driver.counts["B"] != 2 || h.driver.counts["X"] != 2 || h.driver.counts["join"] != 1 {
		t.Fatal(h.driver.counts)
	}
}
func TestRecoveryImplicitWorkspaceRequiresExplicitRebuild(t *testing.T) {
	h := newRetryHarness(t, retrySpec(retryJob("implicit"), retryJob("consume", "implicit")))
	if h.execute(t, "root") == nil {
		t.Fatal("missing undeclared handoff should fail")
	}
	producer := h.node(t, "root", "stage", "implicit")
	consumer := h.node(t, "root", "stage", "consume")
	p, e := h.s.Plan(context.Background(), "root", []string{consumer.Key})
	if e != nil || p.CanSubmit || len(p.RebuildGroups) != 1 {
		t.Fatalf("unsafe implicit reuse: %+v %v", p, e)
	}
	child := h.submit(t, "root", []string{consumer.Key}, []string{producer.Key})
	if e := h.execute(t, child); e != nil {
		t.Fatal(e)
	}
	if h.driver.counts["implicit"] != 2 || h.driver.counts["consume"] != 2 {
		t.Fatal(h.driver.counts)
	}
}
func TestRecoveryHiddenLocalImageSurvivesFailedPush(t *testing.T) {
	image := pipeline.Job{ID: "image", Name: "image", Type: "build_image", Config: map[string]any{"artifactType": "image", "dockerfile": "Dockerfile"}}
	push := pipeline.Job{ID: "push", Name: "push", Type: "push_image", Needs: []string{"image"}, Config: map[string]any{"image": "registry.invalid/example/fixture", "tag": "fixed"}}
	h := newRetryHarness(t, retrySpec(image, push, retryJob("after", "push")))
	h.driver.fail["push"] = true
	if h.execute(t, "root") == nil {
		t.Fatal("expected failed push")
	}
	if len(h.sink.arts) != 0 {
		t.Fatal("hidden local image leaked into public artifacts")
	}
	h.driver.fail["push"] = false
	child := h.submit(t, "root", []string{h.node(t, "root", "stage", "push").Key}, nil)
	if e := h.execute(t, child); e != nil {
		t.Fatal(e)
	}
	if h.driver.counts["image"] != 1 || h.driver.counts["push"] != 2 || h.driver.counts["after"] != 1 {
		t.Fatal(h.driver.counts)
	}
}
func TestRecoveryMissingFileRequiresProducerRebuild(t *testing.T) {
	a := retryJob("A")
	a.Config["artifacts"] = `[{"path":"out.txt"}]`
	h := newRetryHarness(t, retrySpec(a, retryJob("B", "A")))
	h.driver.fail["B"] = true
	_ = h.execute(t, "root")
	key := h.sink.arts[0].Reference
	reader, e := h.b.artStore.Open(key)
	if e != nil {
		t.Fatal(e)
	}
	data, _ := io.ReadAll(reader)
	_ = reader.Close()
	if string(data) != "persisted payload" {
		t.Fatal("wrong persisted bytes")
	}
	// Locate only this test's CAS object; deleting the temporary fixture is not
	// an operation on a user's artifact store.
	if e := os.Remove(filepath.Join(h.artRoot, key[:2], key)); e != nil {
		t.Fatal(e)
	}
	p, e := h.s.Plan(context.Background(), "root", []string{h.node(t, "root", "stage", "B").Key})
	if e != nil || p.CanSubmit {
		t.Fatalf("missing bytes accepted: %+v %v", p, e)
	}
}

func TestRecoverySingleMatrixCellKeepsOtherCells(t *testing.T) {
	a := retryJob("A")
	a.Config["artifacts"] = `[{"path":"out.txt"}]`
	spec := retrySpec(a, retryJob("B", "A"))
	spec.Stages[0].Matrix = map[string][]string{"os": {"linux", "windows"}}
	h := newRetryHarness(t, spec)
	h.driver.fail["B/linux"] = true
	if h.execute(t, "root") == nil {
		t.Fatal("expected cell failure")
	}
	h.driver.fail["B/linux"] = false
	stageID := pipeline.MatrixCellID("stage", map[string]string{"os": "linux"})
	var failed runretry.NodeRef
	current, e := h.s.Context(context.Background(), "root")
	if e != nil {
		t.Fatal(e)
	}
	for _, node := range current.Graph.Nodes {
		if node.ExecutionStageID == stageID && node.JobID == "B" {
			failed = node
		}
	}
	for _, node := range current.Nodes {
		if node.Node.Key == failed.Key && node.Decision.Status != "failed" {
			t.Fatalf("matrix target status=%s counts=%v logs=%v", node.Decision.Status, h.driver.counts, h.sink.logs)
		}
	}
	if failed.Key == "" {
		t.Fatalf("matrix identity missing id=%q graph=%+v", stageID, current.Graph.Nodes)
	}
	child := h.submit(t, "root", []string{failed.Key}, nil)
	if e := h.execute(t, child); e != nil {
		view, _ := h.s.Context(context.Background(), child)
		t.Fatalf("error=%v counts=%v nodes=%+v logs=%v", e, h.driver.counts, view.Nodes, h.sink.logs)
	}
	if h.driver.counts["A/linux"] != 1 || h.driver.counts["A/windows"] != 1 || h.driver.counts["B/linux"] != 2 || h.driver.counts["B/windows"] != 1 {
		t.Fatal(h.driver.counts)
	}
}
func TestRecoveryStageDAGAndAllowFailureInvalidatesDescendants(t *testing.T) {
	for _, allowFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "join", true: "allowFailure"}[allowFailure], func(t *testing.T) {
			spec := pipeline.Spec{Stages: []pipeline.Stage{
				{ID: "source", Name: "source", Kind: "build", Jobs: []pipeline.Job{retryJob("source")}},
				{ID: "left", Name: "left", Kind: "build", Needs: []string{"source"}, AllowFailure: allowFailure, Jobs: []pipeline.Job{retryJob("left")}},
				{ID: "right", Name: "right", Kind: "build", Needs: []string{"source"}, Jobs: []pipeline.Job{retryJob("right")}},
				{ID: "join", Name: "join", Kind: "build", Needs: []string{"left", "right"}, Jobs: []pipeline.Job{retryJob("joined")}},
			}}
			h := newRetryHarness(t, spec)
			h.driver.fail["left"] = true
			if h.execute(t, "root") == nil {
				t.Fatal("expected branch failure")
			}
			if allowFailure && h.driver.counts["joined"] != 1 {
				t.Fatal("normal allowFailure changed")
			}
			h.driver.fail["left"] = false
			child := h.submit(t, "root", []string{h.node(t, "root", "left", "left").Key}, nil)
			if e := h.execute(t, child); e != nil {
				t.Fatal(e)
			}
			wantJoin := 1
			if allowFailure {
				wantJoin = 2
			}
			if h.driver.counts["source"] != 1 || h.driver.counts["right"] != 1 || h.driver.counts["left"] != 2 || h.driver.counts["joined"] != wantJoin {
				t.Fatal(h.driver.counts)
			}
		})
	}
}

type retryServicesDriver struct {
	*retryCountingDriver
	starts, stops          int
	networks               []string
	failCleanup, failReady bool
}

func (d *retryServicesDriver) CreateNetwork(_ context.Context, name string, _ func(string, string)) (int, error) {
	d.networks = append(d.networks, name)
	return 0, nil
}
func (d *retryServicesDriver) RunService(context.Context, string, string, string, string, []string, []string, func(string, string)) (int, error) {
	d.starts++
	return 0, nil
}
func (d *retryServicesDriver) StopService(context.Context, string, func(string, string)) (int, error) {
	d.stops++
	if d.failCleanup {
		return 1, nil
	}
	return 0, nil
}
func (d *retryServicesDriver) RemoveNetwork(context.Context, string, func(string, string)) (int, error) {
	if d.failCleanup {
		return 1, nil
	}
	return 0, nil
}
func (d *retryServicesDriver) ReadyService(context.Context, string) error {
	if d.failReady {
		return runretry.ErrUnavailable
	}
	return nil
}
func TestRecoveryServiceStateRequiresExplicitSeedRebuildAndPostUsesAttemptResult(t *testing.T) {
	spec := retrySpec(retryJob("seed"), retryJob("work", "seed"))
	spec.Stages[0].Services = []pipeline.ServiceSpec{{Name: "db", Image: "fixture"}}
	spec.Stages[0].Post = []pipeline.PostStep{{Condition: "always", Image: "fixture", Commands: []string{"always"}}, {Condition: "on_failure", Image: "fixture", Commands: []string{"failure"}}, {Condition: "on_success", Image: "fixture", Commands: []string{"success"}}}
	h := newRetryHarness(t, spec)
	driver := &retryServicesDriver{retryCountingDriver: h.driver}
	h.b.driver = driver
	h.driver.fail["work"] = true
	if h.execute(t, "root") == nil {
		t.Fatal("expected work failure")
	}
	work := h.node(t, "root", "stage", "work")
	seed := h.node(t, "root", "stage", "seed")
	p, e := h.s.Plan(context.Background(), "root", []string{work.Key})
	if e != nil || p.CanSubmit {
		t.Fatalf("empty service state reused: %+v %v", p, e)
	}
	h.driver.fail["work"] = false
	child := h.submit(t, "root", []string{work.Key}, []string{seed.Key})
	if e := h.execute(t, child); e != nil {
		t.Fatal(e)
	}
	if h.driver.counts["seed"] != 2 || h.driver.counts["always"] != 2 || h.driver.counts["failure"] != 1 || h.driver.counts["success"] != 1 || driver.starts != 2 || driver.stops != 2 || driver.networks[0] == driver.networks[1] {
		t.Fatalf("counts %v services %+v", h.driver.counts, driver)
	}
}
func TestRecoveryCleanupUnknownKeepsHoldWithoutChangingBusinessSuccess(t *testing.T) {
	spec := retrySpec(retryJob("work"))
	spec.Stages[0].Services = []pipeline.ServiceSpec{{Name: "db", Image: "fixture"}}
	h := newRetryHarness(t, spec)
	h.b.driver = &retryServicesDriver{retryCountingDriver: h.driver, failCleanup: true}
	if e := h.execute(t, "root"); e != nil {
		t.Fatalf("cleanup changed completed business result: %v", e)
	}
	if !errors.Is(h.settleErr, runretry.ErrUnknown) {
		t.Fatal("unknown cleanup released hold", h.settleErr)
	}
	current, e := h.s.Context(context.Background(), "root")
	if e != nil || !current.OutcomeUnknown {
		t.Fatalf("unknown lost: %+v %v", current, e)
	}
}
func TestRecoveryPostFailureIsSeparateAndRetryDoesNotRepeatBusinessNode(t *testing.T) {
	spec := retrySpec(retryJob("work"))
	spec.Stages[0].Post = []pipeline.PostStep{{Condition: "always", Image: "fixture", Commands: []string{"post"}}}
	h := newRetryHarness(t, spec)
	h.driver.fail["post"] = true
	if e := h.execute(t, "root"); e != nil {
		t.Fatal("post changed business status", e)
	}
	current, e := h.s.Context(context.Background(), "root")
	if e != nil {
		t.Fatal(e)
	}
	var key string
	for _, n := range current.Graph.Nodes {
		if n.ControlKey == "post:0" {
			key = n.Key
		}
	}
	h.driver.fail["post"] = false
	child := h.submit(t, "root", []string{key}, nil)
	if e := h.execute(t, child); e != nil {
		t.Fatal(e)
	}
	if h.driver.counts["work"] != 1 || h.driver.counts["post"] != 2 {
		t.Fatal(h.driver.counts)
	}
}

type retryRemoteFixture struct {
	mu         sync.Mutex
	counts     map[string]int
	fail       bool
	servers    []string
	workspaces map[string]string
}

func (f *retryRemoteFixture) Capture(_ context.Context, id string) (target.ConnectionSnapshot, error) {
	return target.ConnectionSnapshot{Server: target.Server{ID: id, Host: "127.0.0.1", Port: 22, User: "fixture", CredentialID: "fixture"}}, nil
}

func (f *retryRemoteFixture) Upload(context.Context, string, io.Reader, string) error { return nil }
func (f *retryRemoteFixture) Exec(_ context.Context, server string, args []string) (*target.ExecResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.servers = append(f.servers, server)
	if len(args) == 2 && args[0] == "cat" && args[1] == "/proc/sys/kernel/random/boot_id" {
		return &target.ExecResult{Stdout: "12345678-1234-1234-1234-123456789abc"}, nil
	}
	if len(args) > 1 && args[0] == "docker" && args[1] == "info" {
		return &target.ExecResult{Stdout: "fixture-daemon"}, nil
	}
	if len(args) > 1 && args[0] == "docker" && args[1] == "inspect" {
		return &target.ExecResult{ExitCode: 1, Stderr: "Error: No such object: " + args[len(args)-1]}, nil
	}
	if len(args) > 5 && args[0] == "sh" && args[3] == "pw-remote-workspace-create" {
		if f.workspaces == nil {
			f.workspaces = map[string]string{}
		}
		if _, exists := f.workspaces[args[5]]; exists {
			return &target.ExecResult{ExitCode: 1}, nil
		}
		f.workspaces[args[5]] = ""
		return &target.ExecResult{}, nil
	}
	if len(args) > 3 && args[0] == "mkdir" {
		if f.workspaces == nil {
			f.workspaces = map[string]string{}
		}
		f.workspaces[args[len(args)-1]] = ""
	}
	if len(args) > 4 && args[0] == "sh" && args[1] == "-c" && strings.Contains(args[3], ".pipewright-owner") {
		f.workspaces[strings.TrimSuffix(args[3], "/.pipewright-owner")] = args[4]
	}
	if len(args) > 5 && args[0] == "sh" && args[3] == "pw-resource-inspect" {
		owner, exists := f.workspaces[args[4]]
		code := 0
		if exists {
			code = 2
			if owner == args[5] {
				code = 1
			}
		}
		return &target.ExecResult{ExitCode: code}, nil
	}
	if len(args) > 5 && args[0] == "sh" && args[3] == "pw-resource-cleanup" {
		if f.workspaces[args[4]] != args[5] {
			return &target.ExecResult{ExitCode: 2}, nil
		}
		delete(f.workspaces, args[4])
		return &target.ExecResult{}, nil
	}
	if len(args) > 1 && args[0] == "docker" && args[1] == "run" {
		name := strings.TrimSpace(strings.TrimPrefix(args[len(args)-1], "set -e\n"))
		f.counts[name]++
		if name == "remoteB" && f.fail {
			return &target.ExecResult{ExitCode: 1}, nil
		}
	}
	return &target.ExecResult{ExitCode: 0}, nil
}
func TestRecoveryRemoteScriptsUseFrozenRunnerAndSelectOnlyFailedJob(t *testing.T) {
	h := newRetryHarness(t, retrySpec(retryJob("remoteA"), retryJob("remoteB", "remoteA")), func(s *runretry.Snapshot) { s.RunnerRefs = map[string]string{"serverId": "frozen-fixture"} })
	remote := &retryRemoteFixture{counts: map[string]int{}, fail: true}
	h.executor = NewStageExecutorWithRunner(h.b, nil, fakeRunnerLookup{serverID: "new-mutable-runner"}, remote)
	if h.execute(t, "root") == nil {
		t.Fatal("expected remote failure")
	}
	remote.fail = false
	child := h.submit(t, "root", []string{h.node(t, "root", "stage", "remoteB").Key}, nil)
	if e := h.execute(t, child); e != nil {
		t.Fatal(e)
	}
	if remote.counts["remoteA"] != 1 || remote.counts["remoteB"] != 2 || len(h.driver.counts) != 0 {
		t.Fatalf("remote %v local %v", remote.counts, h.driver.counts)
	}
	for _, id := range remote.servers {
		if id != "frozen-fixture" {
			t.Fatal("runner drift", id)
		}
	}
}
