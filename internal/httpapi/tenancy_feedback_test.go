package httpapi

import (
	"context"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTenantFeedbackHTTPButtonsRequireIndependentWritePermission(t *testing.T) {
	ctx := context.Background()
	db := storetest.OpenDB(t)
	first := seedDoraProject(t, db, "Allowed")
	second := seedDoraProject(t, db, "Hidden same team")
	foreign := seedDoraProject(t, db, "Foreign team")
	started := time.Now().UTC().Add(-time.Hour)
	finished := started.Add(time.Minute)
	seedDoraRun(t, db, first, run.StatusSuccess, started, &finished)
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
	rsvc := run.New(db)
	router := New(testWebFSAuth(), tenancy.NewAuthenticator(accounts, mode.Epoch), WithTenancyAccounts(accounts), WithRuns(rsvc, nil), WithDiagnosisFeedback(run.NewFeedbackService(db)))
	request := func(method, path string, csrf bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(`{"verdict":"down","correctRootCause":"scoped correction"}`))
		r.Header.Set("X-Pipewright-Team", team)
		r.AddCookie(&http.Cookie{Name: cookieSession, Value: login.Token})
		r.AddCookie(&http.Cookie{Name: cookieCsrf, Value: login.CSRFToken})
		if csrf {
			r.Header.Set(headerCsrf, login.CSRFToken)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	count := func(want int) {
		t.Helper()
		var n int
		if e := db.QueryRow(`SELECT COUNT(*) FROM diagnosis_feedback`).Scan(&n); e != nil || n != want {
			t.Fatal("unexpected feedback effects", n, want, e)
		}
	}
	w := request("POST", "/api/runs/"+runID+"/diagnosis/feedback", false)
	if w.Code != 403 {
		t.Fatal("csrf", w.Code, w.Body.String())
	}
	count(0)
	w = request("POST", "/api/runs/"+runID+"/diagnosis/feedback", true)
	if w.Code != 403 {
		t.Fatal("read-only feedback", w.Code, w.Body.String())
	}
	count(0)
	role, e = repo.SaveRole(ctx, admin.Actor, team, role.ID, tenancy.RoleInput{ExpectedRevision: role.Revision, Name: "Metrics", Pages: []string{"page.runs"}, Grants: []authz.Grant{{Action: "run.read", Scope: authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{first}}}, {Action: "run.diagnose", Scope: authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{first}}}}})
	if e != nil {
		t.Fatal(e)
	}
	w = request("POST", "/api/runs/"+runID+"/diagnosis/feedback", true)
	if w.Code != 200 {
		t.Fatal("authorized feedback", w.Code, w.Body.String())
	}
	count(1)
	for _, id := range []string{hiddenID, foreignID} {
		w = request("POST", "/api/runs/"+id+"/diagnosis/feedback", true)
		if w.Code != 404 {
			t.Fatal("hidden feedback target", w.Code, w.Body.String())
		}
		count(1)
	}
	w = request("GET", "/api/settings/diagnosis-stats", false)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var stats diagnosisStatsDTO
	if e = json.Unmarshal(w.Body.Bytes(), &stats); e != nil || stats.TotalFeedback != 1 || len(stats.RecentCorrections) != 1 {
		t.Fatal("scoped stats", stats, e)
	}
	if _, e = repo.SaveRole(ctx, admin.Actor, team, role.ID, tenancy.RoleInput{ExpectedRevision: role.Revision, Name: "Metrics", Pages: []string{"page.runs"}, Grants: []authz.Grant{{Action: "run.read", Scope: authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{first}}}}}); e != nil {
		t.Fatal(e)
	}
	w = request("POST", "/api/runs/"+runID+"/diagnosis/feedback", true)
	if w.Code != 403 {
		t.Fatal("revoked write", w.Code, w.Body.String())
	}
	count(1)
	if e = accounts.Logout(ctx, login.Token); e != nil {
		t.Fatal(e)
	}
	w = request("GET", "/api/settings/diagnosis-stats", false)
	if w.Code != 401 {
		t.Fatal("logout", w.Code)
	}
}
