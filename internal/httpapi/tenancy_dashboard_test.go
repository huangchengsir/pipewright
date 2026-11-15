package httpapi

import (
	"context"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTenantDashboardHTTPMinimalProjectionsAndIntersection(t *testing.T) {
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
	role, e := repo.SaveRole(ctx, admin.Actor, team, "", tenancy.RoleInput{Name: "Metrics", Pages: []string{"page.dora", "page.dashboard", "page.runs", "page.projects"}, Grants: []authz.Grant{{Action: "dora.read", Scope: authz.Scope{Kind: authz.ScopeTeam}}, {Action: "run.read", Scope: authz.Scope{Kind: authz.ScopeTeam}}, {Action: "project.read", Scope: authz.Scope{Kind: authz.ScopeTeam}}, {Action: "dashboard.read", Scope: authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{first}}}}})
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
	rsvc := run.New(db)
	router := New(testWebFSAuth(), tenancy.NewAuthenticator(accounts, mode.Epoch), WithTenancyAccounts(accounts), WithConfigManagement(db, nil, false), WithRuns(rsvc, nil), WithProjects(project.New(db, nil, nil)), WithDoraMetrics(run.NewMetricsService(rsvc)), WithDiagnosisFeedback(run.NewFeedbackService(db)))
	request := func(section, selected, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/api/dashboard/"+section, nil)
		r.Header.Set("X-Pipewright-Team", selected)
		if token != "" {
			r.AddCookie(&http.Cookie{Name: cookieSession, Value: token})
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	w := request("projects", team, login.Token)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var projects []map[string]any
	if e = json.Unmarshal(w.Body.Bytes(), &projects); e != nil || len(projects) != 1 || len(projects[0]) != 2 || projects[0]["id"] != first {
		t.Fatal("minimal scoped projects", projects, e)
	}
	w = request("recent", team, login.Token)
	var runs runListDTO
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if e = json.Unmarshal(w.Body.Bytes(), &runs); e != nil || runs.Total != 1 || len(runs.Items) != 1 || runs.Items[0].ProjectID != first {
		t.Fatal("run intersection", runs, e)
	}
	w = request("dora", team, login.Token)
	var metrics doraResponseDTO
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if e = json.Unmarshal(w.Body.Bytes(), &metrics); e != nil || metrics.TotalDeployments != 1 || metrics.FailedDeployments != 0 {
		t.Fatal("metrics intersection", metrics, e)
	}
	w = request("diagnosis", team, login.Token)
	var stats map[string]any
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if e = json.Unmarshal(w.Body.Bytes(), &stats); e != nil || len(stats) != 4 {
		t.Fatal("diagnosis exposed corrections or snapshots", stats, e)
	}
	for _, section := range []string{"projects", "recent", "running", "dora", "diagnosis", "servers", "metrics", "environments", "alerts"} {
		if w = request(section, other.ID, login.Token); w.Code != 403 {
			t.Fatal("foreign header", section, w.Code, w.Body.String())
		}
		if w = request(section, team, ""); w.Code != 401 {
			t.Fatal("anonymous", section, w.Code)
		}
	}
	if _, e = repo.SaveRole(ctx, admin.Actor, team, role.ID, tenancy.RoleInput{ExpectedRevision: role.Revision, Name: "Metrics", Pages: []string{"page.dora", "page.projects", "page.runs"}, Grants: []authz.Grant{{Action: "dora.read", Scope: authz.Scope{Kind: authz.ScopeTeam}}, {Action: "run.read", Scope: authz.Scope{Kind: authz.ScopeTeam}}, {Action: "project.read", Scope: authz.Scope{Kind: authz.ScopeTeam}}}}); e != nil {
		t.Fatal(e)
	}
	for _, section := range []string{"projects", "recent", "dora", "diagnosis", "servers"} {
		if w = request(section, team, login.Token); w.Code != 403 {
			t.Fatal("revoked dashboard", section, w.Code, w.Body.String())
		}
	}
	if e = accounts.Logout(ctx, login.Token); e != nil {
		t.Fatal(e)
	}
	if w = request("projects", team, login.Token); w.Code != 401 {
		t.Fatal("logout", w.Code)
	}
}
