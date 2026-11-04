package build

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/dagrun"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/runretry"
)

func TestSourceContextSystemVariables(t *testing.T) {
	cl := &sourceRecordingCloner{actual: checkoutTestSHA}
	drv := &recordingDriver{}
	b := newDAGTestBuilder(drv, cl)
	b.projects = fakeProjects{proj: &project.Project{ID: "p1", RepoURL: "https://user:secret@example.com/repo.git?token=hidden#fragment", DefaultBranch: "main"}}
	b.bindSourceCommit = func(context.Context, string, string) error { return nil }
	ctx := run.WithSourceContext(context.Background(), run.SourceContext{})
	created := time.Date(2026, 11, 4, 9, 32, 31, 0, time.FixedZone("CST", 8*3600))
	params := map[string]string{"PIPELINE_ID": "fake", "DATETIME": "fake", "TIMESTAMP": "fake", "CI_COMMIT_TITLE": "fake", "CI_COMMIT_AUTHOR": "fake", "CI_COMMIT_SOURCE": "fake"}
	r := &run.Run{ID: "run-1", ProjectID: "p1", CreatedAt: created, Trigger: run.Trigger{Params: params}}
	execute := NewStageExecutor(b, nil)
	if err := execute(ctx, r, sourceStage(), &fakeReporter{}); err != nil {
		t.Fatal(err)
	}
	if err := execute(ctx, r, scriptStage(scriptJob("build", "fake", "echo ok")), &fakeReporter{}); err != nil {
		t.Fatal(err)
	}
	vars := envMap(t, drv.gotEnv)
	want := map[string]string{"PIPELINE_ID": "run-1", "DATETIME": "2026-11-04 01:32:31", "TIMESTAMP": "1793755951", "CI_COMMIT_TITLE": "fixture title", "CI_COMMIT_AUTHOR": "fixture author", "CI_COMMIT_SOURCE": "https://example.com/repo.git", "CI_COMMIT_ID": checkoutTestSHA[:7], "CI_COMMIT_SHA": checkoutTestSHA, "CI_COMMIT_REF_NAME": "main"}
	for k, v := range want {
		if vars[k] != v {
			t.Errorf("%s=%q want %q", k, vars[k], v)
		}
	}
}

func TestSourceContextSanitizesURL(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{"https://user:password@example.com/repo?token=secret#fragment", "https://example.com/repo"},
		{"ssh://git:password@example.com:2222/org/repo?token=secret", "ssh://example.com:2222/org/repo"},
		{"git@example.com:org/repo.git", "example.com:org/repo.git"},
		{"https://%invalid", ""},
	} {
		if got := safeSourceURL(tt.in); got != tt.want {
			t.Errorf("safeSourceURL(%q)=%q want %q", tt.in, got, tt.want)
		}
	}
}

func TestSourceContextRejectsMissingOrConflictingMetadata(t *testing.T) {
	ctx := run.WithSourceContext(context.Background(), run.SourceContext{})
	if err := bindCheckoutSource(ctx, "main", "https://example.com/r", &CloneResolved{CommitFull: checkoutTestSHA}); !errors.Is(err, run.ErrSourceUnavailable) {
		t.Fatalf("missing metadata: %v", err)
	}
	first := &CloneResolved{CommitFull: checkoutTestSHA, CommitTitle: "first", CommitAuthor: "a", MetadataReady: true}
	if err := bindCheckoutSource(ctx, "main", "https://example.com/r", first); err != nil {
		t.Fatal(err)
	}
	second := *first
	second.CommitTitle = "other"
	if err := bindCheckoutSource(ctx, "main", "https://example.com/r", &second); !errors.Is(err, run.ErrSourceUnavailable) {
		t.Fatalf("conflicting metadata: %v", err)
	}
}

func TestSourceContextRetryUsesPinnedCommitAndOwnRunTime(t *testing.T) {
	spec := pipeline.Spec{Stages: []pipeline.Stage{sourceStage(), {ID: "build", Name: "build", Kind: pipeline.KindBuild, Needs: []string{"src"}, Jobs: []pipeline.Job{retryJob("C")}}}}
	h := newRetryHarness(t, spec)
	cl := &sourceRecordingCloner{actual: checkoutTestSHA}
	h.b.cloner = cl
	driver := &retrySourceDriver{retryCountingDriver: h.driver}
	h.b.driver = driver
	calls := 0
	h.b.bindSourceCommit = func(context.Context, string, string) error { calls++; return nil }
	loader := dagrun.SpecLoaderFunc(func(context.Context, string) (*pipeline.Config, error) { return nil, errors.New("unexpected loader") })
	created := time.Date(2026, 11, 4, 1, 32, 31, 0, time.UTC)
	execute := func(id string, when time.Time) error {
		ctx, err := h.s.Prepare(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		runner := dagrun.New(loader, dagrun.WithGitSourceContext(), dagrun.WithStageExecutor(NewStageExecutor(h.b, nil)))
		err = runner.Run(ctx, &run.Run{ID: id, ProjectID: "p1", CreatedAt: when}, &retryEvidenceSink{})
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
	if execute("root", created) == nil {
		t.Fatal("expected failure")
	}
	h.driver.fail["C"] = false
	if _, err := h.store.DB.Exec("UPDATE projects SET repo_url='https://changed.invalid/other' WHERE id='p1'"); err != nil {
		t.Fatal(err)
	}
	child := h.submit(t, "root", []string{h.node(t, "root", "build", "C").Key}, nil)
	if err := execute(child, created.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	vars := envMap(t, driver.env)
	if vars["PIPELINE_ID"] != child || vars["DATETIME"] != "2026-11-04 02:32:31" || vars["CI_COMMIT_SHA"] != checkoutTestSHA || vars["CI_COMMIT_TITLE"] != "fixture title" || vars["CI_COMMIT_AUTHOR"] != "fixture author" || vars["CI_COMMIT_SOURCE"] != "https://example.invalid/r" {
		t.Fatalf("retry env: %+v", vars)
	}
	if calls != 1 || len(cl.commits) < 3 || !strings.EqualFold(cl.commits[len(cl.commits)-1], checkoutTestSHA) {
		t.Fatalf("source calls=%d checkouts=%v", calls, cl.commits)
	}
}

func TestSourceContextRemoteRunnerMetadata(t *testing.T) {
	local := &recordingDriver{}
	cl := &sourceRecordingCloner{actual: checkoutTestSHA}
	b := newRemoteTestBuilder(local)
	b.cloner = cl
	b.bindSourceCommit = func(context.Context, string, string) error { return nil }
	target := &fakeRemoteTarget{}
	exec := NewStageExecutorWithRunner(b, nil, fakeRunnerLookup{serverID: "remote"}, target)
	ctx := run.WithSourceContext(context.Background(), run.SourceContext{})
	r := &run.Run{ID: "remote-run", ProjectID: "p1", CreatedAt: time.Date(2026, 11, 4, 1, 32, 31, 0, time.UTC), Trigger: run.Trigger{Branch: "main"}}
	if err := exec(ctx, r, sourceStage(), &fakeReporter{}); err != nil {
		t.Fatal(err)
	}
	if err := exec(ctx, r, scriptStage(scriptJob("remote-job", "alpine", "true")), &fakeReporter{}); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range target.cmds {
		if len(cmd) > 1 && cmd[0] == "docker" && cmd[1] == "run" {
			joined := strings.Join(cmd, " ")
			for _, want := range []string{"PIPELINE_ID=remote-run", "DATETIME=2026-11-04 01:32:31", "TIMESTAMP=1793755951", "CI_COMMIT_TITLE=fixture title", "CI_COMMIT_AUTHOR=fixture author", "CI_COMMIT_ID=" + checkoutTestSHA[:7]} {
				if !strings.Contains(joined, want) {
					t.Fatalf("missing %q: %v", want, cmd)
				}
			}
			return
		}
	}
	t.Fatal("remote docker command missing")
}
