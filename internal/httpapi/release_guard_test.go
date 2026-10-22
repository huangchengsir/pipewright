package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/deploy"
	"github.com/huangchengsir/pipewright/internal/deployattempt"
	"github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/releaseguard"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/target"
	"github.com/huangchengsir/pipewright/internal/vault"
)

type guardHTTPObserver struct {
	executor target.LimitedExecutor
	reads    atomic.Int64
}

func (o *guardHTTPObserver) Capture(c context.Context, id string) (target.ConnectionSnapshot, error) {
	return o.executor.Capture(c, id)
}
func (o *guardHTTPObserver) Inspect(context.Context, target.ConnectionSnapshot, string) releaseguard.RuntimeSnapshot {
	o.reads.Add(1)
	return releaseguard.RuntimeSnapshot{Outcome: "unavailable", Reason: "fixture_unavailable", CollectedAt: time.Now().UTC(), RepoDigests: []string{}}
}
func (o *guardHTTPObserver) Baseline(c context.Context, s target.ConnectionSnapshot, id string) releaseguard.RuntimeSnapshot {
	return o.Inspect(c, s, id)
}
func (o *guardHTTPObserver) Logs(context.Context, target.ConnectionSnapshot, string, time.Time, time.Time) (string, bool, string) {
	o.reads.Add(1)
	return "", false, "logs_unavailable"
}

type guardHTTPFixture struct {
	http                                              http.Handler
	service                                           *releaseguard.Service
	ledger                                            *deployattempt.Store
	observer                                          *guardHTTPObserver
	projectID, runID, artifactID, serverID, attemptID string
	runs                                              run.Service
}

func newGuardHTTPFixture(t *testing.T, models ...releaseguard.ExplanationModel) guardHTTPFixture {
	t.Helper()
	ctx := context.Background()
	st := testStoreAuth(t)
	v := vault.New(st.DB, testMasterKey())
	c, err := v.Create(vault.CreateInput{Name: "git", Type: vault.TypeGitToken, Secret: "fixture-private-git-secret"})
	if err != nil {
		t.Fatal(err)
	}
	projects := project.New(st.DB, v, stubProber{branch: "main"})
	p, err := projects.Create(ctx, project.CreateInput{Name: "guard", RepoURL: "https://example.invalid/app", CredentialID: c.ID, DefaultBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	ssh, err := v.Create(vault.CreateInput{Name: "ssh", Type: vault.TypeSSHPassword, Secret: "fixture-private-ssh-secret"})
	if err != nil {
		t.Fatal(err)
	}
	targets := target.New(st.DB, v, stubDialer{res: &target.ExecResult{Stdout: strings.Repeat("1", 64)}})
	server, err := targets.Create(ctx, target.CreateInput{Name: "fixture-server", Host: "127.0.0.1", User: "fixture", CredentialID: ssh.ID})
	if err != nil {
		t.Fatal(err)
	}
	runs := run.New(st.DB)
	runID := "guard-http-run"
	if _, err = st.DB.Exec(`INSERT INTO pipeline_runs(id,project_id,status,resolved_environment,created_at) VALUES(?,?,'success','prod',?)`, runID, p.ID, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	a, err := runs.AddArtifact(ctx, run.Artifact{RunID: runID, Name: "app", Type: "image", Reference: "reg.invalid/app:stable", Metadata: map[string]any{"pushed": true, "digestRepository": "reg.invalid/app", "digest": "sha256:" + strings.Repeat("a", 64)}})
	if err != nil {
		t.Fatal(err)
	}
	ledger := deployattempt.New(st.DB)
	attempt, err := ledger.Begin(ctx, deployattempt.BeginInput{RunID: runID, Initiator: "deploy"})
	if err != nil {
		t.Fatal(err)
	}
	cap, err := targets.(target.LimitedExecutor).Capture(ctx, server.ID)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := ledger.StartTarget(ctx, attempt.ID, deployattempt.TargetInput{ArtifactID: a.ID, ArtifactName: a.Name, ServerID: server.ID, ServerName: server.Name, ContainerName: "app", ConnectionFingerprint: cap.Fingerprint(), ExpectedImage: deployattempt.ExpectedImage{Reference: "reg.invalid/app@sha256:" + strings.Repeat("a", 64), Repository: "reg.invalid/app", Digest: "sha256:" + strings.Repeat("a", 64), DigestKind: deployattempt.DigestRepositoryUnknown}})
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{deployattempt.ActivationStarted, deployattempt.Activated, deployattempt.Succeeded} {
		if _, err = ledger.AppendReceipt(ctx, tr.ID, deployattempt.Receipt{Kind: kind, ActualImage: deployattempt.ActualImage{ContainerID: strings.Repeat("1", 64)}}); err != nil {
			t.Fatal(err)
		}
	}
	observer := &guardHTTPObserver{executor: targets.(target.LimitedExecutor)}
	var model releaseguard.ExplanationModel
	if len(models) > 0 {
		model = models[0]
	}
	guards, err := releaseguard.New(releaseguard.Options{DB: st.DB, Ledger: ledger, Runs: runs, Targets: targets, Observer: observer, ResolveImageTarget: deploy.ObservationImageTarget, Model: model, Masker: mask.NewMasker()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if e := guards.Close(ctx); e != nil {
			t.Error(e)
		}
	})
	deployer := deploy.New(targets, runs, deploy.WithAttemptLedger(ledger), deploy.WithImageObserver(guards))
	h := New(nil, onboardingAuth{}, WithReleaseGuard(guards), WithRuns(runs, nil), WithProjects(projects), WithDeploy(deployer), WithAudit(audit.New(st.DB, mask.NewMasker(), nil)))
	return guardHTTPFixture{h, guards, ledger, observer, p.ID, runID, a.ID, server.ID, attempt.ID, runs}
}

func guardHTTPRequest(h http.Handler, method, path, body, token, csrf string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		r.AddCookie(&http.Cookie{Name: cookieSession, Value: token})
	}
	if csrf != "" {
		r.Header.Set(headerCsrf, csrf)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestReleaseGuardRoutesRequireAuthenticationAndCSRF(t *testing.T) {
	f := newGuardHTTPFixture(t)
	path := "/api/projects/" + f.projectID + "/release-guard-plans"
	for _, tc := range []struct {
		token, csrf string
		status      int
	}{{"", "", 401}, {"valid", "", 403}, {"valid", "wrong", 403}} {
		w := guardHTTPRequest(f.http, "POST", path, `{"mode":"from_now","attemptId":"`+f.attemptID+`"}`, tc.token, tc.csrf)
		if w.Code != tc.status {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if f.observer.reads.Load() != 0 {
		t.Fatal("unauthorized request reached observer")
	}
}

func TestReleaseGuardStrictBodiesQueriesAndProjectScope(t *testing.T) {
	f := newGuardHTTPFixture(t)
	path := "/api/projects/" + f.projectID + "/release-guard-plans"
	for _, body := range []string{`{"mode":"from_now","attemptId":"` + f.attemptID + `","snapshot":{}}`, `{"mode":"from_now","mode":"attached"}`, `{} {}`, `null`, strings.Repeat("x", 65537)} {
		w := guardHTTPRequest(f.http, "POST", path, body, "valid", "csrf")
		if w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for _, query := range []string{"?after=-1", "?unknown=1", "?after=0&after=1", "?limit=101"} {
		w := guardHTTPRequest(f.http, "GET", "/api/projects/"+f.projectID+"/release-guards/nope/evidence"+query, "", "valid", "")
		if w.Code != 400 {
			t.Fatal(query, w.Code, w.Body.String())
		}
	}
	w := guardHTTPRequest(f.http, "POST", "/api/projects/missing/release-guard-plans", `{"mode":"from_now","attemptId":"`+f.attemptID+`"}`, "valid", "csrf")
	if w.Code != 404 {
		t.Fatal(w.Code, w.Body.String())
	}
	if f.observer.reads.Load() != 0 {
		t.Fatal("preview made remote reads")
	}
}

func TestReleaseGuardHTTPPlanStartSnapshotsAndAttempts(t *testing.T) {
	f := newGuardHTTPFixture(t)
	base := "/api/projects/" + f.projectID
	w := guardHTTPRequest(f.http, "POST", base+"/release-guard-plans", `{"mode":"from_now","attemptId":"`+f.attemptID+`","durationSeconds":300}`, "valid", "csrf")
	if w.Code != 200 || f.observer.reads.Load() != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
	var plan releaseguard.Plan
	if json.Unmarshal(w.Body.Bytes(), &plan) != nil || plan.ID == "" {
		t.Fatal(w.Body.String())
	}
	input := `{"planId":"` + plan.ID + `","planHash":"` + plan.Hash + `","idempotencyKey":"http-once"}`
	w = guardHTTPRequest(f.http, "POST", base+"/release-guards", input, "valid", "csrf")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var report releaseguard.Report
	if json.Unmarshal(w.Body.Bytes(), &report) != nil || report.ID == "" {
		t.Fatal(w.Body.String())
	}
	w = guardHTTPRequest(f.http, "POST", base+"/release-guards", input, "valid", "csrf")
	var duplicate releaseguard.Report
	_ = json.Unmarshal(w.Body.Bytes(), &duplicate)
	if w.Code != 200 || duplicate.ID != report.ID {
		t.Fatal(w.Code, w.Body.String())
	}
	w = guardHTTPRequest(f.http, "GET", base+"/release-guards/"+report.ID, "", "valid", "")
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, secret := range []string{"fixture-private-ssh-secret", "credentialVersion", "bindings"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("private data exposed", secret)
		}
	}
	w = guardHTTPRequest(f.http, "GET", "/api/runs/"+f.runID+"/deployment-attempts", "", "valid", "")
	var list struct {
		Items []struct {
			ID     string                       `json:"id"`
			Guards []releaseguard.ReportSummary `json:"guards"`
		} `json:"items"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &list) != nil || len(list.Items) != 1 || len(list.Items[0].Guards) != 1 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = guardHTTPRequest(f.http, "GET", "/api/projects/other/release-guards/"+report.ID, "", "valid", "")
	if w.Code != 404 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = guardHTTPRequest(f.http, "POST", base+"/release-guards/"+report.ID+"/stop", `{}`, "valid", "csrf")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestReleaseGuardAttachedRequestIsConsumedOnce(t *testing.T) {
	f := newGuardHTTPFixture(t)
	base := "/api/projects/" + f.projectID
	body := `{"mode":"attached","runId":"` + f.runID + `","artifactId":"` + f.artifactID + `","serverIds":["` + f.serverID + `"],"deployConfig":{"containerName":"app"},"durationSeconds":300}`
	w := guardHTTPRequest(f.http, "POST", base+"/release-guard-plans", body, "valid", "csrf")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var plan releaseguard.Plan
	_ = json.Unmarshal(w.Body.Bytes(), &plan)
	body = `{"artifactId":"` + f.artifactID + `","serverIds":["` + f.serverID + `"],"deployConfig":{"containerName":"app"},"releaseGuard":{"planId":"` + plan.ID + `","planHash":"` + plan.Hash + `","idempotencyKey":"attached-once"}}`
	path := "/api/runs/" + f.runID + "/deploy"
	changed := strings.TrimSuffix(body, "}") + `,"strategy":"canary"}`
	w = guardHTTPRequest(f.http, "POST", path, changed, "valid", "csrf")
	if w.Code != 409 {
		t.Fatal("different deployment strategy consumed frozen plan", w.Code, w.Body.String())
	}
	changed = strings.TrimSuffix(body, "}") + `,"healthCheck":{"type":"command","command":["false"]}}`
	w = guardHTTPRequest(f.http, "POST", path, changed, "valid", "csrf")
	if w.Code != 409 {
		t.Fatal("different health command consumed frozen plan", w.Code, w.Body.String())
	}
	w = guardHTTPRequest(f.http, "POST", path, body, "valid", "csrf")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var response struct {
		GuardID string `json:"guardId"`
	}
	if json.Unmarshal(w.Body.Bytes(), &response) != nil || response.GuardID == "" {
		t.Fatal(w.Body.String())
	}
	first, err := f.ledger.ListByRun(context.Background(), f.runID)
	if err != nil || len(first) != 2 {
		t.Fatal(len(first), err)
	}
	w = guardHTTPRequest(f.http, "POST", path, body, "valid", "csrf")
	if w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	second, err := f.ledger.ListByRun(context.Background(), f.runID)
	if err != nil || len(second) != len(first) {
		t.Fatal("duplicate request redeployed", len(second), err)
	}
}

func TestReleaseGuardProjectDeletionStopsWorkersAndCleansReports(t *testing.T) {
	f := newGuardHTTPFixture(t)
	ctx := context.Background()
	plan, err := f.service.Prepare(ctx, f.projectID, releaseguard.PrepareInput{Mode: "from_now", AttemptID: f.attemptID, DurationSeconds: 300})
	if err != nil {
		t.Fatal(err)
	}
	report, err := f.service.Start(ctx, f.projectID, releaseguard.StartInput{PlanID: plan.ID, PlanHash: plan.Hash, IdempotencyKey: "project-delete"})
	if err != nil {
		t.Fatal(err)
	}
	w := guardHTTPRequest(f.http, "DELETE", "/api/projects/"+f.projectID, "", "valid", "csrf")
	if w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = guardHTTPRequest(f.http, "GET", "/api/projects/"+f.projectID+"/release-guards/"+report.ID, "", "valid", "")
	if w.Code != 404 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, err = f.service.Start(ctx, f.projectID, releaseguard.StartInput{PlanID: plan.ID, PlanHash: plan.Hash, IdempotencyKey: "after-delete"}); err == nil {
		t.Fatal("deleted project accepted an observation")
	}
	// Deployment receipt snapshots deliberately outlive their deleted source.
	if a, e := f.ledger.Get(ctx, f.attemptID); e != nil || len(a.Targets) != 1 {
		t.Fatal(a, e)
	}
}

func TestReleaseGuardFailedProjectDeletionReleasesBarrier(t *testing.T) {
	f := newGuardHTTPFixture(t)
	active, err := f.runs.Create(context.Background(), f.projectID, run.Trigger{Type: run.TriggerManual, Branch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	w := guardHTTPRequest(f.http, "DELETE", "/api/projects/"+f.projectID, "", "valid", "csrf")
	if w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, err = f.runs.Cancel(context.Background(), active.ID); err != nil {
		t.Fatal(err)
	}
	w = guardHTTPRequest(f.http, "POST", "/api/projects/"+f.projectID+"/release-guard-plans", `{"mode":"from_now","attemptId":"`+f.attemptID+`"}`, "valid", "csrf")
	if w.Code != 200 {
		t.Fatal("failed deletion left project observation barrier", w.Code, w.Body.String())
	}
}

type guardHTTPModel struct{ calls atomic.Int64 }

func (m *guardHTTPModel) Info(context.Context) (releaseguard.ModelInfo, error) {
	return releaseguard.ModelInfo{Name: "fixture", Model: "fake", Destination: "https://fixture.invalid", ConfigHash: strings.Repeat("c", 64)}, nil
}
func (m *guardHTTPModel) Explain(_ context.Context, _ releaseguard.ModelInfo, payload string) (string, error) {
	m.calls.Add(1)
	var body struct {
		Items []struct {
			ID string `json:"evidenceId"`
		} `json:"items"`
	}
	_ = json.Unmarshal([]byte(payload), &body)
	ids := []string{}
	if len(body.Items) > 0 {
		ids = append(ids, body.Items[0].ID)
	}
	b, _ := json.Marshal(releaseguard.Explanation{Summary: "候选解释，原因未确定", Candidates: []releaseguard.Candidate{{ID: "candidate", Title: "仅限本次结构化证据", SupportingEvidenceIDs: ids, CounterEvidenceIDs: []string{}, MissingEvidence: []string{"business_effect_unknown"}, NextReadOnlyCheck: "人工检查本地证据"}}})
	return string(b), nil
}

func TestReleaseGuardAnalysisRequiresExactSingleUseConfirmation(t *testing.T) {
	model := &guardHTTPModel{}
	f := newGuardHTTPFixture(t, model)
	ctx := context.Background()
	plan, err := f.service.Prepare(ctx, f.projectID, releaseguard.PrepareInput{Mode: "from_now", AttemptID: f.attemptID, DurationSeconds: 300})
	if err != nil {
		t.Fatal(err)
	}
	report, err := f.service.Start(ctx, f.projectID, releaseguard.StartInput{PlanID: plan.ID, PlanHash: plan.Hash, IdempotencyKey: "analysis-http"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		page, e := f.service.Evidence(ctx, f.projectID, report.ID, 0, 100)
		if e != nil {
			t.Fatal(e)
		}
		if len(page.Items) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fixture sample missing")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err = f.service.Stop(ctx, f.projectID, report.ID); err != nil {
		t.Fatal(err)
	}
	for {
		r, e := f.service.Snapshot(ctx, f.projectID, report.ID)
		if e != nil {
			t.Fatal(e)
		}
		if r.ExecutionStatus == "stopped" {
			report = r
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("stop did not converge")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if model.calls.Load() != 0 {
		t.Fatal("local observation sent model payload")
	}
	base := "/api/projects/" + f.projectID + "/release-guards/" + report.ID
	readsBefore := f.observer.reads.Load()
	w := guardHTTPRequest(f.http, "POST", base+"/investigate", `{"exec":"forbidden"}`, "valid", "csrf")
	if w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = guardHTTPRequest(f.http, "POST", base+"/investigate", `{}`, "valid", "csrf")
	if w.Code != 200 || model.calls.Load() != 0 || f.observer.reads.Load() != readsBefore {
		t.Fatal("local investigation dispatched a remote/model operation", w.Code, w.Body.String())
	}
	w = guardHTTPRequest(f.http, "POST", base+"/analysis/preview", `{"includeRuntime":true,"includeLogs":false}`, "valid", "csrf")
	if w.Code != 200 || model.calls.Load() != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
	var analysis releaseguard.Analysis
	if json.Unmarshal(w.Body.Bytes(), &analysis) != nil || analysis.ID == "" || strings.Contains(analysis.Payload, "fixture-private") {
		t.Fatal(w.Body.String())
	}
	path := base + "/analysis/" + analysis.ID
	body := `{"payloadHash":"` + analysis.PayloadHash + `","providerConfigHash":"` + analysis.Provider.ConfigHash + `","confirmed":false}`
	w = guardHTTPRequest(f.http, "POST", path+"/send", body, "valid", "csrf")
	if w.Code != 400 || model.calls.Load() != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
	body = strings.Replace(body, `"confirmed":false`, `"confirmed":true`, 1)
	w = guardHTTPRequest(f.http, "POST", path+"/send", body, "valid", "csrf")
	if w.Code != 200 || model.calls.Load() != 1 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = guardHTTPRequest(f.http, "POST", path+"/send", body, "valid", "csrf")
	if w.Code != 200 || model.calls.Load() != 1 {
		t.Fatal("duplicate dispatch", w.Code, w.Body.String())
	}
	w = guardHTTPRequest(f.http, "POST", path+"/revoke", `{}`, "valid", "csrf")
	if w.Code != 200 || model.calls.Load() != 1 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = guardHTTPRequest(f.http, "GET", base+"/analysis", "", "valid", "")
	var list struct {
		Items []releaseguard.Analysis `json:"items"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &list) != nil || len(list.Items) != 1 || !list.Items[0].Revoked {
		t.Fatal(w.Code, w.Body.String())
	}
	after, err := f.service.Snapshot(ctx, f.projectID, report.ID)
	if err != nil || after.VerificationResult == nil || report.VerificationResult == nil || *after.VerificationResult != *report.VerificationResult {
		t.Fatal("model changed deterministic result", after, err)
	}
}

type guardCancelWriter struct {
	*httptest.ResponseRecorder
	cancel context.CancelFunc
}

func (w guardCancelWriter) Flush() { w.ResponseRecorder.Flush(); w.cancel() }

func TestReleaseGuardEventsDisconnectDoesNotExecuteCollection(t *testing.T) {
	f := newGuardHTTPFixture(t)
	ctx := context.Background()
	plan, err := f.service.Prepare(ctx, f.projectID, releaseguard.PrepareInput{Mode: "from_now", AttemptID: f.attemptID, DurationSeconds: 300})
	if err != nil {
		t.Fatal(err)
	}
	report, err := f.service.Start(ctx, f.projectID, releaseguard.StartInput{PlanID: plan.ID, PlanHash: plan.Hash, IdempotencyKey: "event-once"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.Stop(ctx, f.projectID, report.ID); err != nil {
		t.Fatal(err)
	}
	closeCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err = f.service.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
	before := f.observer.reads.Load()
	streamCtx, stop := context.WithCancel(ctx)
	defer stop()
	r := httptest.NewRequest("GET", "/api/projects/"+f.projectID+"/release-guards/"+report.ID+"/events?after=0", nil).WithContext(streamCtx)
	r.AddCookie(&http.Cookie{Name: cookieSession, Value: "valid"})
	r.Header.Set("Accept", "text/event-stream")
	w := guardCancelWriter{httptest.NewRecorder(), stop}
	f.http.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "event:") || f.observer.reads.Load() != before {
		t.Fatal(w.Code, w.Body.String())
	}
}
