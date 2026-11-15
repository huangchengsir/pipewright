package httpapi

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/ai"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"github.com/huangchengsir/pipewright/internal/vault"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTenantDiagnosisHTTPModelBoundaryAndRevocation(t *testing.T) {
	ctx := context.Background()
	db := storetest.OpenDB(t)
	first := seedDoraProject(t, db, "Allowed")
	second := seedDoraProject(t, db, "Hidden same team")
	foreign := seedDoraProject(t, db, "Foreign team")
	started := time.Now().UTC().Add(-time.Hour)
	finished := started.Add(time.Minute)
	seedDoraRun(t, db, first, run.StatusFailed, started, &finished)
	seedDoraRun(t, db, second, run.StatusFailed, started, &finished)
	seedDoraRun(t, db, foreign, run.StatusFailed, started, &finished)
	if e := auth.NewService(db, nil).Bootstrap("admin", "dora-admin-password"); e != nil {
		t.Fatal(e)
	}
	repo := tenancy.NewRepository(db)
	mode, e := repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true)
	if e != nil {
		t.Fatal(e)
	}
	accounts := tenancy.NewAccounts(db)
	admin, e := accounts.Login(ctx, "admin", "dora-admin-password")
	if e != nil {
		t.Fatal(e)
	}
	var team string
	if e = db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); e != nil {
		t.Fatal(e)
	}
	other, e := repo.CreateTeamAs(ctx, admin.Actor, "Other")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE access_ownership SET tenant_id=? WHERE resource_kind='project' AND resource_id=?`, other.ID, foreign); e != nil {
		t.Fatal(e)
	}
	role, e := repo.SaveRole(ctx, admin.Actor, team, "", tenancy.RoleInput{Name: "Metrics", Pages: []string{"page.runs"}, Grants: []authz.Grant{{Action: "run.read", Scope: authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{first}}}}})
	if e != nil {
		t.Fatal(e)
	}
	initial, e := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: "metrics", DisplayName: "Metrics", Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
	if e != nil {
		t.Fatal(e)
	}
	login, e := accounts.Login(ctx, "metrics", initial.Password)
	if e != nil {
		t.Fatal(e)
	}
	login, e = accounts.ChangePassword(ctx, login.Token, initial.Password, "metrics-new-password")
	if e != nil {
		t.Fatal(e)
	}

	var runID, hiddenID, foreignID string
	for _, pair := range []struct {
		p  string
		id *string
	}{{first, &runID}, {second, &hiddenID}, {foreign, &foreignID}} {
		if e = db.QueryRow(`SELECT id FROM pipeline_runs WHERE project_id=?`, pair.p).Scan(pair.id); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = db.Exec(`UPDATE pipeline_runs SET diagnosis_json=?`, `{"status":"ready","hypothesis":"fixture","confidence":"high","alternateCauses":[],"fixSuggestions":[],"evidence":[],"generatedAt":"2026-11-13T00:00:00Z"}`); e != nil {
		t.Fatal(e)
	}

	if _, e = db.Exec(`UPDATE pipeline_runs SET failure_log=? WHERE id=?`, "failure "+run.StubFailureSecret, runID); e != nil {
		t.Fatal(e)
	}
	rsvc := run.New(db)
	model := &permissionDiagnosisModel{}
	source := NewRunSecretSource(rsvc, nil, nil, &blockedDiagnosisVault{})
	router := New(testWebFSAuth(), tenancy.NewAuthenticator(accounts, mode.Epoch), WithTenancyAccounts(accounts), WithRuns(rsvc, nil), WithAISettings(model))
	request := func(id string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/runs/"+id+"/diagnose", strings.NewReader("{}"))
		r.Header.Set("X-Pipewright-Team", team)
		r.Header.Set(headerCsrf, login.CSRFToken)
		r.AddCookie(&http.Cookie{Name: cookieSession, Value: login.Token})
		r.AddCookie(&http.Cookie{Name: cookieCsrf, Value: login.CSRFToken})
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	if w := request(runID); w.Code != 403 || model.calls != 0 {
		t.Fatal("read implies model call", w.Code, model.calls)
	}
	role, e = repo.SaveRole(ctx, admin.Actor, team, role.ID, tenancy.RoleInput{ExpectedRevision: role.Revision, Name: "Diagnosis", Pages: []string{"page.runs"}, Grants: []authz.Grant{{Action: "run.read", Scope: authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{first}}}, {Action: "run.diagnose", Scope: authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{first}}}}})
	if e != nil {
		t.Fatal(e)
	}
	if w := request(runID); w.Code != 200 || model.calls != 1 || strings.Contains(w.Body.String(), run.StubFailureSecret) || strings.Contains(model.input, run.StubFailureSecret) {
		t.Fatal("authorized masked diagnosis", w.Code, model.calls)
	}
	for _, id := range []string{hiddenID, foreignID} {
		if w := request(id); w.Code != 404 || model.calls != 1 {
			t.Fatal("hidden diagnosis", w.Code, model.calls)
		}
	}
	// A required masking failure blocks the adapter before any model request.
	if _, e = diagnoseRun(tenancy.WithActor(ctx, login.Actor, team), rsvc, model, source, runID, true); e == nil || model.calls != 1 {
		t.Fatal("mask registration failure called model", e, model.calls)
	}
	var before string
	db.QueryRow(`SELECT diagnosis_json FROM pipeline_runs WHERE id=?`, runID).Scan(&before)
	model.before = func() {
		_, e = repo.SaveRole(ctx, admin.Actor, team, role.ID, tenancy.RoleInput{ExpectedRevision: role.Revision, Name: "Read only", Pages: []string{"page.runs"}, Grants: []authz.Grant{{Action: "run.read", Scope: authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{first}}}}})
		if e != nil {
			t.Fatal(e)
		}
	}
	if w := request(runID); w.Code != 403 || model.calls != 2 || strings.Contains(w.Body.String(), "model output") {
		t.Fatal("revoked model result returned", w.Code, model.calls)
	}
	var after string
	db.QueryRow(`SELECT diagnosis_json FROM pipeline_runs WHERE id=?`, runID).Scan(&after)
	if before != after {
		t.Fatal("revoked diagnosis persisted")
	}
	if w := request(runID); w.Code != 403 || model.calls != 2 {
		t.Fatal("revoked request called model", w.Code, model.calls)
	}
}

type permissionDiagnosisModel struct {
	diagStubAI
	calls  int
	input  string
	before func()
}

func (s *permissionDiagnosisModel) Diagnose(_ context.Context, in ai.DiagnoseInput) (*ai.Diagnosis, error) {
	s.calls++
	s.input = in.FailureLog
	if s.before != nil {
		s.before()
	}
	return &ai.Diagnosis{Status: "ready", Hypothesis: "model output " + run.StubFailureSecret, Confidence: "high", GeneratedAt: time.Now()}, nil
}

type blockedDiagnosisVault struct{ vault.Vault }

func (*blockedDiagnosisVault) RegisterRelatedMaskSecrets(context.Context, string, string, *mask.Masker) (bool, error) {
	return true, tenancy.ErrForbidden
}
