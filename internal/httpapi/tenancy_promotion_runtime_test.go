package httpapi

import (
	"context"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/approval"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/promotion"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTenantPromotionHTTPRequiresIndependentTargetAndApproval(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db := storetest.OpenDB(t)
	project := seedDoraProject(t, db, "promotion")
	outside := seedDoraProject(t, db, "outside")
	now := time.Now().UTC()
	for _, p := range []string{project, outside} {
		seedDoraRun(t, db, p, run.StatusSuccess, now, &now)
	}
	var id, otherID string
	if e := db.QueryRow(`SELECT id FROM pipeline_runs WHERE project_id=?`, project).Scan(&id); e != nil {
		t.Fatal(e)
	}
	if e := db.QueryRow(`SELECT id FROM pipeline_runs WHERE project_id=?`, outside).Scan(&otherID); e != nil {
		t.Fatal(e)
	}
	ps := promotion.NewStore(db)
	if _, e := ps.SaveConfiguration(ctx, project, promotion.Chain{Environments: []promotion.EnvStage{{Name: "test", Gated: true}, {Name: "prod"}}}, map[string][]promotion.Variable{"test": {{Key: "NOT_FOR_PLAN", Value: "configuration-only-value"}}}); e != nil {
		t.Fatal(e)
	}
	if e := auth.NewService(db, nil).Bootstrap("admin", "promotion-http-admin"); e != nil {
		t.Fatal(e)
	}
	repo := tenancy.NewRepository(db)
	mode, e := repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true)
	if e != nil {
		t.Fatal(e)
	}
	accounts := tenancy.NewAccounts(db)
	admin, e := accounts.Login(ctx, "admin", "promotion-http-admin")
	if e != nil {
		t.Fatal(e)
	}
	var team, env string
	if e = db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); e != nil {
		t.Fatal(e)
	}
	if e = db.QueryRow(`SELECT id FROM access_environments WHERE project_id=? AND name='test'`, project).Scan(&env); e != nil {
		t.Fatal(e)
	}
	scope := authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{project}}
	input := tenancy.RoleInput{Name: "HTTP promoter", Pages: []string{"page.runs", "page.environments"}, Grants: []authz.Grant{{Action: "run.read", Scope: scope}, {Action: "environment.read", Scope: scope}}}
	role, e := repo.SaveRole(ctx, admin.Actor, team, "", input)
	if e != nil {
		t.Fatal(e)
	}
	initial, e := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: "http-promoter", DisplayName: "Promoter", Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeTeam, EnvironmentIDs: []string{env}}}}})
	if e != nil {
		t.Fatal(e)
	}
	login, e := accounts.Login(ctx, "http-promoter", initial.Password)
	if e != nil {
		t.Fatal(e)
	}
	login, e = accounts.ChangePassword(ctx, login.Token, initial.Password, "promotion-http-employee")
	if e != nil {
		t.Fatal(e)
	}
	coord := approval.New()
	router := New(testWebFSAuth(), tenancy.NewAuthenticator(accounts, mode.Epoch), WithTenancyAccounts(accounts), WithRuns(run.New(db), nil), WithPromotion(ps), WithApprovals(coord, approval.NewStore(db)))
	request := func(method, path, body, token, csrf string, include bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
		r.Header.Set("X-Pipewright-Team", team)
		r.AddCookie(&http.Cookie{Name: cookieSession, Value: token})
		r.AddCookie(&http.Cookie{Name: cookieCsrf, Value: csrf})
		if include {
			r.Header.Set(headerCsrf, csrf)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	call := func(method, path, body string, csrf bool) *httptest.ResponseRecorder {
		return request(method, path, body, login.Token, login.CSRFToken, csrf)
	}
	path := "/api/runs/" + id
	w := call("GET", path+"/promotion-plan", "", false)
	if w.Code != 200 || strings.Contains(w.Body.String(), "configuration-only-value") || strings.Contains(w.Body.String(), "NOT_FOR_PLAN") {
		t.Fatal("plan response", w.Code, w.Body.String())
	}
	var plan promotion.Plan
	if e = json.Unmarshal(w.Body.Bytes(), &plan); e != nil || plan.Next == nil || plan.Next.EnvironmentID != env {
		t.Fatal("next target", plan, e)
	}
	for _, csrf := range []bool{false, true} {
		w = call("POST", path+"/promote", `{"targetEnvironment":"test"}`, csrf)
		if w.Code != 403 {
			t.Fatal("reader or missing csrf", w.Code, w.Body.String())
		}
	}
	var count int
	if e = db.QueryRow(`SELECT COUNT(*) FROM run_promotions`).Scan(&count); e != nil || count != 0 {
		t.Fatal("denial inserted records", count, e)
	}
	w = call("GET", "/api/runs/"+otherID+"/promotion-plan", "", false)
	if w.Code != 403 {
		t.Fatal("outside plan", w.Code)
	}
	grant := scope
	grant.EnvironmentIDs = []string{env}
	input.ExpectedRevision = role.Revision
	input.Grants = append(input.Grants, authz.Grant{Action: "release.promote", Scope: grant})
	role, e = repo.SaveRole(ctx, admin.Actor, team, role.ID, input)
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- call("POST", path+"/promote", `{"targetEnvironment":"test"}`, true) }()
	deadline := time.Now().Add(3 * time.Second)
	for !coord.IsWaiting(approval.Key(id, "promote:test")) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !coord.IsWaiting(approval.Key(id, "promote:test")) {
		cancel()
		t.Fatal("gate was not registered")
	}
	w = call("POST", path+"/approve", `{"stageId":"promote:test"}`, true)
	if w.Code != 403 {
		t.Fatal("promoter self approved without approval action", w.Code)
	}
	w = request("POST", path+"/approve", `{"stageId":"promote:test"}`, admin.Token, admin.CSRFToken, true)
	if w.Code != 200 {
		t.Fatal("independent approver", w.Code, w.Body.String())
	}
	select {
	case w = <-done:
		if w.Code != 200 {
			t.Fatal("approved promotion", w.Code, w.Body.String())
		}
	case <-ctx.Done():
		t.Fatal("promotion did not finish")
	}
	w = call("POST", path+"/promote", `{"targetEnvironment":"prod"}`, true)
	if w.Code != 403 {
		t.Fatal("ungranted production", w.Code, w.Body.String())
	}
	w = call("GET", path+"/promotions", "", false)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"promoted"`) {
		t.Fatal("history", w.Code, w.Body.String())
	}
	if e = accounts.Logout(ctx, login.Token); e != nil {
		t.Fatal(e)
	}
	w = call("GET", path+"/promotion-plan", "", false)
	if w.Code != 401 {
		t.Fatal("logout plan", w.Code)
	}
}
