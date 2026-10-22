package releaseguard

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/deployattempt"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/target"
)

type investigationFixture struct {
	service      *Service
	ledger       *deployattempt.Store
	runs         run.Service
	id, priorRun string
	calls        atomic.Int64
}

func newInvestigationFixture(t *testing.T, oldRole string) *investigationFixture {
	t.Helper()
	ctx := context.Background()
	st := storetest.Open(t)
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	for _, q := range []string{
		`INSERT INTO credentials(id,name,type,scope,ciphertext,masked_value,created_at,updated_at) VALUES('source-c','c','git_token','',X'00','masked','` + stamp + `','` + stamp + `')`,
		`INSERT INTO projects(id,name,repo_url,default_branch,credential_id,created_at,updated_at) VALUES('source-p','p','https://example.invalid/source','main','source-c','` + stamp + `','` + stamp + `')`,
		`INSERT INTO project_config_versions(project_id,revision) VALUES('source-p',1)`,
		`INSERT INTO pipeline_runs(id,project_id,status,resolved_environment,created_at) VALUES('source-old','source-p','success','prod','` + stamp + `')`,
		`INSERT INTO pipeline_runs(id,project_id,status,resolved_environment,created_at) VALUES('source-current','source-p','success','prod','` + stamp + `')`,
	} {
		if _, err := st.DB.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	runs := run.New(st.DB)
	oldSHA, newSHA := strings.Repeat("e", 40), strings.Repeat("d", 40)
	if err := runs.SetCommit(ctx, "source-old", oldSHA); err != nil {
		t.Fatal(err)
	}
	if err := runs.SetCommit(ctx, "source-current", newSHA); err != nil {
		t.Fatal(err)
	}
	ledger := deployattempt.New(st.DB)
	makeAttempt := func(runID, sha, role, container string) *deployattempt.Target {
		t.Helper()
		a, err := ledger.Begin(ctx, deployattempt.BeginInput{RunID: runID, Initiator: "deploy", ResolvedFullSHA: sha})
		if err != nil {
			t.Fatal(err)
		}
		tr, err := ledger.StartTarget(ctx, a.ID, deployattempt.TargetInput{ArtifactID: "artifact", ArtifactName: "app", ServerID: "source-server", ServerName: "server", ContainerName: role, ExpectedImage: deployattempt.ExpectedImage{Repository: "registry.invalid/app", Digest: ruleDigest, DigestKind: deployattempt.DigestRepositoryUnknown}})
		if err != nil {
			t.Fatal(err)
		}
		for _, kind := range []string{deployattempt.ActivationStarted, deployattempt.Activated, deployattempt.Succeeded} {
			if _, err = ledger.AppendReceipt(ctx, tr.ID, deployattempt.Receipt{Kind: kind, ActualImage: deployattempt.ActualImage{ContainerID: container}}); err != nil {
				t.Fatal(err)
			}
		}
		tr, err = ledger.GetTarget(ctx, tr.ID)
		if err != nil {
			t.Fatal(err)
		}
		return tr
	}
	oldContainer := strings.Repeat("2", 64)
	makeAttempt("source-old", oldSHA, oldRole, oldContainer)
	baselineAt := time.Now().UTC()
	baselineSnapshot := ruleSnapshot()
	baselineSnapshot.ContainerID = oldContainer
	baselineSnapshot.Running = false
	baselineSnapshot.CollectedAt = baselineAt
	current := makeAttempt("source-current", newSHA, "app", ruleContainer)
	now := time.Now().UTC()
	observed := TargetObservation{PlanTarget: PlanTarget{ID: current.ID, ServerID: current.ServerID, ContainerName: "app", ContainerID: ruleContainer, WorkloadKey: current.WorkloadKey, ExpectedImage: current.ExpectedImage}, VerificationResult: strptr("failed"), Conditions: []Condition{{Name: "liveness", Result: "failed", Reason: "container_exited_or_oom", EvidenceIDs: []string{"source-failure"}}}}
	p := Plan{ID: "source-plan", Hash: "source-plan-hash", ProjectID: "source-p", RunID: "source-current", AttemptID: current.AttemptID, Environment: "prod", ConfigRevision: 1, Mode: "attached", DurationSeconds: 300, Targets: []PlanTarget{{ID: "before-activation-target", ServerID: current.ServerID, ContainerName: "app", WorkloadKey: current.WorkloadKey}}}
	d := &document{Report: Report{ID: "source-guard", ProjectID: "source-p", RunID: "source-current", AttemptID: current.AttemptID, Plan: p, ExecutionStatus: "completed", VerificationResult: strptr("failed"), Targets: []TargetObservation{observed}, CreatedAt: now, FinishedAt: &now, EvidenceExpiresAt: now.Add(7 * 24 * time.Hour), Timeline: []TimelineItem{}}, Private: privateState{Bindings: map[string]target.ConnectionSnapshot{}}}
	body, _ := json.Marshal(d)
	if _, err := st.DB.Exec(`INSERT INTO release_guards(id,project_id,run_id,attempt_id,plan_id,idempotency_key,status,created_at,evidence_expires_at,report_expires_at,record_json) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, d.Report.ID, d.Report.ProjectID, d.Report.RunID, d.Report.AttemptID, p.ID, "source-key", "completed", now.UnixMilli(), d.Report.EvidenceExpiresAt.UnixMilli(), now.Add(30*24*time.Hour).UnixMilli(), string(body)); err != nil {
		t.Fatal(err)
	}
	s, err := New(Options{DB: st.DB, Ledger: ledger, Runs: runs})
	if err != nil {
		t.Fatal(err)
	}
	baseline := &Evidence{ID: "source-baseline", TargetID: "before-activation-target", Kind: "baseline", EventAt: baselineAt, CollectedAt: baselineAt, Value: baselineSnapshot}
	if err = s.save(ctx, d, "baseline", baseline); err != nil {
		t.Fatal(err)
	}
	if err = s.save(ctx, d, "runtime", &Evidence{ID: "source-failure", TargetID: current.ID, Kind: "runtime", EventAt: now, CollectedAt: now, Value: ruleSnapshot()}); err != nil {
		t.Fatal(err)
	}
	f := &investigationFixture{service: s, ledger: ledger, runs: runs, id: d.Report.ID, priorRun: "source-old"}
	s.compareSource = func(_ context.Context, project, base, current string) SourceComparison {
		f.calls.Add(1)
		return SourceComparison{Available: true, BaseSHA: base, CurrentSHA: current, ComparisonKind: "direct_trees", Files: []SourceFileChange{{Path: "src/app.go", Status: "modified"}}}
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	return f
}

func TestInvestigateUsesImmediateIdentityAndRealResolvedEndpoints(t *testing.T) {
	f := newInvestigationFixture(t, "app")
	ctx := context.Background()
	r, err := f.service.Investigate(ctx, "source-p", f.id)
	if err != nil {
		t.Fatal(err)
	}
	if r.Investigation.BaselineStatus != "verified_immediate" || len(r.Investigation.SourceComparisons) != 1 || !r.Investigation.SourceComparisons[0].Available || r.Investigation.SourceComparisons[0].BaseSHA != strings.Repeat("e", 40) || r.Investigation.SourceComparisons[0].CurrentSHA != strings.Repeat("d", 40) {
		t.Fatalf("investigation=%+v", r.Investigation)
	}
	if len(r.Investigation.Candidates) != 1 || len(r.Investigation.Candidates[0].CounterEvidenceIDs) != 1 || r.Investigation.Candidates[0].CounterEvidenceIDs[0] != "source-baseline" {
		t.Fatalf("pre-existing failure counter-evidence missing: %+v", r.Investigation.Candidates)
	}
	if *r.VerificationResult != "failed" || r.ExecutionStatus != "completed" {
		t.Fatal("investigation modified verification/execution")
	}
	page, err := f.service.Evidence(ctx, "source-p", f.id, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	cursor := r.EventsCursor
	again, err := f.service.Investigate(ctx, "source-p", f.id)
	if err != nil || f.calls.Load() != 1 || again.EventsCursor != cursor {
		t.Fatalf("same input not idempotent: calls=%d cursor=%d/%d err=%v", f.calls.Load(), cursor, again.EventsCursor, err)
	}
	after, _ := f.service.Evidence(ctx, "source-p", f.id, 0, 100)
	if len(after.Items) != len(page.Items) {
		t.Fatal("repeat investigation duplicated evidence")
	}
}

func TestInvestigateRejectsCrossWorkloadAndConflictingSource(t *testing.T) {
	t.Run("container from another workload", func(t *testing.T) {
		f := newInvestigationFixture(t, "other-app")
		r, err := f.service.Investigate(context.Background(), "source-p", f.id)
		if err != nil {
			t.Fatal(err)
		}
		if f.calls.Load() != 0 || r.Investigation.SourceComparisons[0].Available || r.Investigation.Baselines[0].Status == "verified_immediate" {
			t.Fatalf("cross-workload baseline linked: %+v", r.Investigation)
		}
	})
	t.Run("prior source conflict", func(t *testing.T) {
		f := newInvestigationFixture(t, "app")
		if err := f.runs.SetCommit(context.Background(), f.priorRun, strings.Repeat("a", 40)); err != nil {
			t.Fatal(err)
		}
		r, err := f.service.Investigate(context.Background(), "source-p", f.id)
		if err != nil {
			t.Fatal(err)
		}
		if f.calls.Load() != 0 || r.Investigation.SourceComparisons[0].Reason != "source_revision_conflict" {
			t.Fatalf("conflicting source accepted: %+v", r.Investigation)
		}
	})
	t.Run("missing resolved source is not trigger SHA", func(t *testing.T) {
		f := newInvestigationFixture(t, "app")
		if _, err := f.service.db.Exec(`UPDATE pipeline_runs SET resolved_commit_sha='',trigger_commit=? WHERE id=?`, strings.Repeat("e", 40), f.priorRun); err != nil {
			t.Fatal(err)
		}
		r, err := f.service.Investigate(context.Background(), "source-p", f.id)
		if err != nil {
			t.Fatal(err)
		}
		if f.calls.Load() != 0 || r.Investigation.SourceComparisons[0].Available {
			t.Fatal("unconfirmed trigger expanded to source proof")
		}
	})
}

func TestInvestigateHistoricalCandidateNeverBecomesImmediate(t *testing.T) {
	f := newInvestigationFixture(t, "app")
	d, err := f.service.load(context.Background(), "source-p", f.id)
	if err != nil {
		t.Fatal(err)
	}
	d.Report.Plan.Mode = "from_now"
	if err = f.service.save(context.Background(), d, "fixture_mode", nil); err != nil {
		t.Fatal(err)
	}
	r, err := f.service.Investigate(context.Background(), "source-p", f.id)
	if err != nil {
		t.Fatal(err)
	}
	if f.calls.Load() != 0 || r.Investigation.BaselineStatus != "historical_candidates_only" || r.Investigation.SourceComparisons[0].Available || len(r.Investigation.Baselines[0].HistoricalCandidates) == 0 || r.Investigation.Baselines[0].HistoricalCandidates[0].Status != "historical_candidate_not_immediate" {
		t.Fatalf("history became actual predeployment baseline: %+v", r.Investigation)
	}
}

func TestInvestigateReleasesLockAndDiscardsChangedReport(t *testing.T) {
	f := newInvestigationFixture(t, "app")
	ctx := context.Background()
	d, err := f.service.load(ctx, "source-p", f.id)
	if err != nil {
		t.Fatal(err)
	}
	d.Report.ExecutionStatus = "observing"
	d.Report.FinishedAt = nil
	if err = f.service.save(ctx, d, "fixture_running", nil); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	f.service.compareSource = func(_ context.Context, _ string, base, current string) SourceComparison {
		close(entered)
		<-release
		return SourceComparison{Available: true, BaseSHA: base, CurrentSHA: current, ComparisonKind: "direct_trees", Files: []SourceFileChange{}}
	}
	done := make(chan error, 1)
	go func() { _, err := f.service.Investigate(ctx, "source-p", f.id); done <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("comparison did not start")
	}
	stopped := make(chan error, 1)
	go func() { _, err := f.service.Stop(ctx, "source-p", f.id); stopped <- err }()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(250 * time.Millisecond):
		close(release)
		t.Fatal("investigation blocked Stop")
	}
	close(release)
	if err = <-done; !errors.Is(err, ErrConflict) {
		t.Fatalf("late result error=%v", err)
	}
	r, err := f.service.Snapshot(ctx, "source-p", f.id)
	if err != nil || r.ExecutionStatus != "stopped" || *r.VerificationResult != "failed" || r.Investigation.Status != "stale" || len(r.Investigation.SourceComparisons) != 0 {
		t.Fatalf("late result overwrote current state: %+v err=%v", r, err)
	}
}

func TestSourceComparisonRejectsPathsAndWrongEndpointClaims(t *testing.T) {
	degraded := validatedSourceComparison(SourceComparison{Reason: "source_cache_unavailable"}, ruleTarget(), strings.Repeat("e", 40), strings.Repeat("d", 40))
	if degraded.Available || degraded.Reason != "source_cache_unavailable" || degraded.BaseSHA != strings.Repeat("e", 40) || !retryableInvestigation(Investigation{SourceComparisons: []SourceComparison{degraded}}) {
		t.Fatalf("unavailable adapter lost its reason or caller endpoint: %+v", degraded)
	}
	for _, name := range []string{"../private", "/absolute", "a/../private", "C:/secret", "a\\b", "a\x1b.txt", string([]byte{0xff})} {
		if ValidSourceChangePath(name) {
			t.Fatalf("unsafe source path accepted: %q", name)
		}
	}
	for _, name := range []string{"src/app.go", ".env", "folder with spaces/file.txt"} {
		if !ValidSourceChangePath(name) {
			t.Fatalf("valid relative source path rejected: %q", name)
		}
	}
	target := ruleTarget()
	base, current := strings.Repeat("e", 40), strings.Repeat("d", 40)
	for _, bad := range []SourceComparison{
		{Available: true, BaseSHA: strings.Repeat("f", 40), CurrentSHA: current, ComparisonKind: "direct_trees"},
		{Available: true, BaseSHA: base, CurrentSHA: current, ComparisonKind: "first_parent"},
		{Available: true, BaseSHA: base, CurrentSHA: current, ComparisonKind: "direct_trees", Files: []SourceFileChange{{Path: "../private", Status: "modified"}}},
	} {
		if got := validatedSourceComparison(bad, target, base, current); got.Available {
			t.Fatalf("unproven source claim accepted: %+v", got)
		}
	}
}

func TestInvestigateExplicitRetryCanUseRecoveredLocalCache(t *testing.T) {
	f := newInvestigationFixture(t, "app")
	ctx := context.Background()
	ready := false
	f.service.compareSource = func(_ context.Context, _ string, base, current string) SourceComparison {
		f.calls.Add(1)
		out := SourceComparison{BaseSHA: base, CurrentSHA: current, ComparisonKind: "direct_trees", Reason: "source_cache_busy", Files: []SourceFileChange{}}
		if ready {
			out.Available = true
			out.Reason = ""
		}
		return out
	}
	first, err := f.service.Investigate(ctx, "source-p", f.id)
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.service.Investigate(ctx, "source-p", f.id)
	if err != nil || first.EventsCursor != second.EventsCursor {
		t.Fatalf("unchanged retry duplicated events: %+v %v", second, err)
	}
	ready = true
	third, err := f.service.Investigate(ctx, "source-p", f.id)
	if err != nil || !third.Investigation.SourceComparisons[0].Available || f.calls.Load() != 3 {
		t.Fatalf("explicit retry could not use recovered cache: %+v calls=%d err=%v", third.Investigation, f.calls.Load(), err)
	}
	if _, err = f.service.Investigate(ctx, "source-p", f.id); err != nil || f.calls.Load() != 3 {
		t.Fatalf("successful investigation no longer idempotent: calls=%d err=%v", f.calls.Load(), err)
	}
}
