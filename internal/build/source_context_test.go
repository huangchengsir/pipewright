package build

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/huangchengsir/pipewright/internal/dagrun"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/runretry"
)

type sourceRecordingCloner struct {
	mu                sync.Mutex
	commits, branches []string
	actual            string
	err               error
}

func (c *sourceRecordingCloner) Clone(_ context.Context, _, _, _, branch, commit, _ string) (*CloneResolved, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.commits = append(c.commits, commit)
	c.branches = append(c.branches, branch)
	if c.err != nil {
		return nil, c.err
	}
	return &CloneResolved{CommitFull: c.actual, CommitShort: shortSHA(c.actual), CommitTitle: "fixture title", CommitAuthor: "fixture author", MetadataReady: true}, nil
}
func sourceStage() pipeline.Stage {
	return pipeline.Stage{ID: "src", Name: "source", Kind: pipeline.KindSource, Jobs: []pipeline.Job{{ID: "git", Name: "source", Type: "git_source"}}}
}

func TestSourceContextSourceAndDownstream(t *testing.T) {
	cl := &sourceRecordingCloner{actual: checkoutTestSHA}
	drv := &recordingDriver{}
	b := newDAGTestBuilder(drv, cl)
	b.projects = fakeProjects{proj: &project.Project{ID: "p1", RepoURL: "https://example.com/r", DefaultBranch: "main"}}
	var saved string
	b.bindSourceCommit = func(_ context.Context, id, sha string) error {
		if id != "run1" {
			t.Fatal(id)
		}
		saved = sha
		return nil
	}
	ctx := run.WithSourceContext(context.Background(), run.SourceContext{})
	r := &run.Run{ID: "run1", ProjectID: "p1", Trigger: run.Trigger{Params: map[string]string{"CI_COMMIT_SHA": "fake", "CI_COMMIT_ID": "fake", "CI_COMMIT_REF_NAME": "fake"}}}
	execute := NewStageExecutor(b, nil)
	if err := execute(ctx, r, sourceStage(), &fakeReporter{}); err != nil {
		t.Fatal(err)
	}
	got, _ := run.SourceFromContext(ctx)
	if saved != checkoutTestSHA || got.CommitSHA != checkoutTestSHA || got.RefName != "main" {
		t.Fatalf("saved=%s context=%+v", saved, got)
	}
	if err := execute(ctx, r, scriptStage(scriptJob("build", "fake", "echo ok")), &fakeReporter{}); err != nil {
		t.Fatal(err)
	}
	if len(cl.commits) != 2 || cl.commits[0] != "" || cl.commits[1] != checkoutTestSHA {
		t.Fatalf("checkouts %v", cl.commits)
	}
	vars := envMap(t, drv.gotEnv)
	if vars["CI_COMMIT_SHA"] != checkoutTestSHA || vars["CI_COMMIT_ID"] != checkoutTestSHA[:7] || vars["CI_COMMIT_REF_NAME"] != "main" {
		t.Fatalf("env=%v", vars)
	}
	if _, exists := vars["CI_COMMIT_SHORT_SHA"]; exists {
		t.Fatal("old short SHA variable must not be injected")
	}
}

func TestSourceContextFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		sha     string
		bindErr error
	}{
		{"short", "abc", nil}, {"non-hex", strings.Repeat("z", 40), nil}, {"persist", checkoutTestSHA, errors.New("unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := newDAGTestBuilder(&recordingDriver{}, &sourceRecordingCloner{actual: tc.sha})
			b.bindSourceCommit = func(context.Context, string, string) error { return tc.bindErr }
			ctx := run.WithSourceContext(context.Background(), run.SourceContext{})
			if err := NewStageExecutor(b, nil)(ctx, &run.Run{ID: "run", ProjectID: "p1"}, sourceStage(), &fakeReporter{}); err == nil {
				t.Fatal("expected source failure")
			}
			if source, _ := run.SourceFromContext(ctx); source.CommitSHA != "" {
				t.Fatalf("published failed source: %+v", source)
			}
		})
	}
	b := newDAGTestBuilder(&recordingDriver{}, &sourceRecordingCloner{actual: strings.Repeat("b", 40)})
	ctx := run.WithSourceContext(context.Background(), run.SourceContext{CommitSHA: checkoutTestSHA})
	_, _, cleanup, err := b.cloneJobWorkspace(ctx, &run.Run{ProjectID: "p1"}, &project.Project{}, &fakeReporter{})
	defer cleanup()
	if !errors.Is(err, ErrBuildFailed) {
		t.Fatalf("mismatch must fail checkout: %v", err)
	}
}
func TestSourceContextRemoteRunner(t *testing.T) {
	local := &recordingDriver{}
	cl := &sourceRecordingCloner{actual: checkoutTestSHA}
	b := newRemoteTestBuilder(local)
	b.cloner = cl
	b.bindSourceCommit = func(context.Context, string, string) error { return nil }
	target := &fakeRemoteTarget{}
	exec := NewStageExecutorWithRunner(b, nil, fakeRunnerLookup{serverID: "remote"}, target)
	ctx := run.WithSourceContext(context.Background(), run.SourceContext{})
	r := &run.Run{ID: "remote-run", ProjectID: "p1", Trigger: run.Trigger{Branch: "main"}}
	if err := exec(ctx, r, sourceStage(), &fakeReporter{}); err != nil {
		t.Fatal(err)
	}
	if len(target.cmds) != 0 {
		t.Fatal("source checkout should be on controller")
	}
	if err := exec(ctx, r, scriptStage(scriptJob("remote-job", "alpine", "true")), &fakeReporter{}); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, cmd := range target.cmds {
		if len(cmd) > 1 && cmd[0] == "docker" && cmd[1] == "run" {
			found = true
			joined := strings.Join(cmd, " ")
			if !strings.Contains(joined, "CI_COMMIT_SHA="+checkoutTestSHA) || !strings.Contains(joined, "CI_COMMIT_ID="+checkoutTestSHA[:7]) || !strings.Contains(joined, "CI_COMMIT_REF_NAME=main") {
				t.Fatalf("missing source env %v", cmd)
			}
		}
	}
	if !found || len(cl.commits) != 2 || cl.commits[1] != checkoutTestSHA {
		t.Fatalf("remote docker=%v commits=%v", found, cl.commits)
	}
}

type retrySourceDriver struct {
	*retryCountingDriver
	muEnv sync.Mutex
	env   []string
}

func (d *retrySourceDriver) RunToolchain(ctx context.Context, image, ws, wd string, env, cmd []string, res pipeline.Resource, opts ToolchainRunOptions, line func(string, string)) (int, error) {
	d.muEnv.Lock()
	d.env = append([]string(nil), env...)
	d.muEnv.Unlock()
	return d.retryCountingDriver.RunToolchain(ctx, image, ws, wd, env, cmd, res, opts, line)
}
func TestSourceContextCapturedExecution(t *testing.T) {
	spec := pipeline.Spec{Stages: []pipeline.Stage{sourceStage(), {ID: "build", Name: "build", Kind: pipeline.KindBuild, Needs: []string{"src"}, Jobs: []pipeline.Job{retryJob("C")}}}}
	h := newRetryHarness(t, spec)
	h.b.cloner = &sourceRecordingCloner{actual: checkoutTestSHA}
	driver := &retrySourceDriver{retryCountingDriver: h.driver}
	h.b.driver = driver
	h.b.bindSourceCommit = func(context.Context, string, string) error { return nil }
	ctx, err := h.s.Prepare(context.Background(), "root")
	if err != nil {
		t.Fatal(err)
	}
	loader := dagrun.SpecLoaderFunc(func(context.Context, string) (*pipeline.Config, error) {
		return nil, errors.New("unexpected mutable loader")
	})
	runner := dagrun.New(loader, dagrun.WithGitSourceContext(), dagrun.WithStageExecutor(NewStageExecutor(h.b, nil)))
	if err = runner.Run(ctx, &run.Run{ID: "root", ProjectID: "p1"}, h.sink); err != nil {
		t.Fatal(err)
	}
	vars := envMap(t, driver.env)
	if vars["CI_COMMIT_SHA"] != checkoutTestSHA || vars["CI_COMMIT_ID"] != checkoutTestSHA[:7] || vars["CI_COMMIT_REF_NAME"] != "main" {
		t.Fatalf("captured env=%v", vars)
	}
}
func TestSourceContextRetryReusesBoundCommit(t *testing.T) {
	spec := pipeline.Spec{Stages: []pipeline.Stage{sourceStage(), {ID: "build", Name: "build", Kind: pipeline.KindBuild, Needs: []string{"src"}, Jobs: []pipeline.Job{retryJob("C")}}}}
	h := newRetryHarness(t, spec)
	h.b.cloner = &sourceRecordingCloner{actual: checkoutTestSHA}
	driver := &retrySourceDriver{retryCountingDriver: h.driver}
	h.b.driver = driver
	calls := 0
	h.b.bindSourceCommit = func(context.Context, string, string) error { calls++; return nil }
	loader := dagrun.SpecLoaderFunc(func(context.Context, string) (*pipeline.Config, error) { return nil, errors.New("unexpected loader") })
	execute := func(id string) error {
		ctx, err := h.s.Prepare(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		sink := &retryEvidenceSink{}
		runner := dagrun.New(loader, dagrun.WithGitSourceContext(), dagrun.WithStageExecutor(NewStageExecutor(h.b, nil)))
		err = runner.Run(ctx, &run.Run{ID: id, ProjectID: "p1"}, sink)
		status := "success"
		if err != nil {
			status = "failed"
		}
		if _, e := h.store.DB.Exec("UPDATE pipeline_runs SET status=? WHERE id=?", status, id); e != nil {
			t.Fatal(e)
		}
		if e := h.s.Complete(ctx, id, status); e != nil {
			t.Fatal(e)
		}
		if e := h.s.Settle(ctx, id); e != nil && !errors.Is(e, runretry.ErrUnknown) {
			t.Fatal(e)
		}
		return err
	}
	h.driver.fail["C"] = true
	if execute("root") == nil {
		t.Fatal("expected failure")
	}
	h.driver.fail["C"] = false
	child := h.submit(t, "root", []string{h.node(t, "root", "build", "C").Key}, nil)
	if err := execute(child); err != nil {
		t.Fatal(err)
	}
	vars := envMap(t, driver.env)
	if vars["CI_COMMIT_SHA"] != checkoutTestSHA || vars["CI_COMMIT_ID"] != checkoutTestSHA[:7] || vars["CI_COMMIT_REF_NAME"] != "main" {
		t.Fatalf("retry env=%v", vars)
	}
	if calls != 1 {
		t.Fatalf("source should be inherited rather than re-executed, calls=%d", calls)
	}
}

// Concurrent jobs must receive the same immutable source without sharing a workspace.
type sourceNoopReporter struct{}

func (sourceNoopReporter) Log(context.Context, string, string) error        { return nil }
func (sourceNoopReporter) EmitArtifact(context.Context, run.Artifact) error { return nil }
func (sourceNoopReporter) JobRunning(context.Context, string) error         { return nil }
func (sourceNoopReporter) JobDone(context.Context, string, string) error    { return nil }
func (sourceNoopReporter) JobReporter(string) dagrun.StageReporter          { return sourceNoopReporter{} }
func TestSourceContextParallelJobs(t *testing.T) {
	cl := &sourceRecordingCloner{actual: checkoutTestSHA}
	drv := &defaultsDriver{}
	b := newDAGTestBuilder(drv, cl)
	b.bindSourceCommit = func(context.Context, string, string) error { return nil }
	ctx := run.WithSourceContext(context.Background(), run.SourceContext{})
	r := &run.Run{ID: "parallel", ProjectID: "p1", Trigger: run.Trigger{Branch: "main"}}
	exec := NewStageExecutor(b, nil)
	if err := exec(ctx, r, sourceStage(), sourceNoopReporter{}); err != nil {
		t.Fatal(err)
	}
	a := scriptJob("a", "image-a", "true")
	a.ID = "a"
	z := scriptJob("z", "image-z", "true")
	z.ID = "z"
	if err := exec(ctx, r, scriptStage(a, z), sourceNoopReporter{}); err != nil {
		t.Fatal(err)
	}
	if len(drv.calls) != 2 {
		t.Fatalf("calls=%d", len(drv.calls))
	}
	for _, call := range drv.calls {
		vars := envMap(t, call.env)
		if vars["CI_COMMIT_SHA"] != checkoutTestSHA || vars["CI_COMMIT_TITLE"] != "fixture title" || vars["PIPELINE_ID"] != "parallel" {
			t.Fatalf("parallel env=%v", vars)
		}
	}
	if len(cl.commits) != 3 || cl.commits[1] != checkoutTestSHA || cl.commits[2] != checkoutTestSHA {
		t.Fatalf("commits=%v", cl.commits)
	}
}
