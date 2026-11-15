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
	"testing"
	"time"
)

func TestTenantDoraHTTPAggregatePickerAndRevocation(t *testing.T) {
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
	role, e := repo.SaveRole(ctx, admin.Actor, team, "", tenancy.RoleInput{Name: "Metrics", Pages: []string{"page.dora"}, Grants: []authz.Grant{{Action: "dora.read", Scope: authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{first}}}}})
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
	router := New(testWebFSAuth(), tenancy.NewAuthenticator(accounts, mode.Epoch), WithTenancyAccounts(accounts), WithDoraMetrics(run.NewMetricsService(run.New(db))))
	request := func(path, selected, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("X-Pipewright-Team", selected)
		if token != "" {
			r.AddCookie(&http.Cookie{Name: cookieSession, Value: token})
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	checkCount := func(path string, want int) {
		t.Helper()
		w := request(path, team, login.Token)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var out doraResponseDTO
		if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil {
			t.Fatal(e)
		}
		if out.TotalDeployments != want || out.FailedDeployments != 0 {
			t.Fatal("aggregate scope", out)
		}
		sum := 0
		for _, p := range out.Trend {
			sum += p.Deployments
		}
		if sum != want {
			t.Fatal("trend scope", sum, want)
		}
	}
	checkCount("/api/metrics/dora", 1)
	for _, p := range []string{second, foreign, "unknown"} {
		checkCount("/api/metrics/dora?projectId="+p, 0)
	}
	w := request("/api/metrics/dora/projects", team, login.Token)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var choices []map[string]any
	if e = json.Unmarshal(w.Body.Bytes(), &choices); e != nil {
		t.Fatal(e)
	}
	if len(choices) != 1 || len(choices[0]) != 2 || choices[0]["id"] != first || choices[0]["name"] != "Allowed" {
		t.Fatal("minimal picker", choices)
	}
	for _, path := range []string{"/api/metrics/dora", "/api/metrics/dora/projects"} {
		if w = request(path, other.ID, login.Token); w.Code != 403 {
			t.Fatal("foreign team", path, w.Code, w.Body.String())
		}
		if w = request(path, team, ""); w.Code != 401 {
			t.Fatal("anonymous", path, w.Code)
		}
	}
	if _, e = repo.SaveRole(ctx, admin.Actor, team, role.ID, tenancy.RoleInput{ExpectedRevision: role.Revision, Name: "Metrics", Pages: []string{"page.dora"}, Grants: []authz.Grant{}}); e != nil {
		t.Fatal(e)
	}
	checkCount("/api/metrics/dora", 0)
	w = request("/api/metrics/dora/projects", team, login.Token)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if e = json.Unmarshal(w.Body.Bytes(), &choices); e != nil || len(choices) != 0 {
		t.Fatal("revoked picker", choices, e)
	}
	if e = accounts.Logout(ctx, login.Token); e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{"/api/metrics/dora", "/api/metrics/dora/projects"} {
		if w = request(path, team, login.Token); w.Code != 401 {
			t.Fatal("logged out", path, w.Code)
		}
	}
}
