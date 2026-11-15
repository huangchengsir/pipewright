package httpapi

import (
	"context"
	"fmt"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/environments"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTenantEnvironmentHTTPBoundaries(t *testing.T) {
	ctx := context.Background()
	db := storetest.OpenDB(t)
	if _, err := db.Exec(`INSERT INTO credentials(id,name,type,scope,ciphertext,masked_value,created_at,updated_at) VALUES('c','fixture','git_token','',X'00','***','now','now')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO projects(id,name,repo_url,default_branch,credential_id,created_at,updated_at) VALUES('p','Visible','https://example.invalid/repo','main','c','now','now')`); err != nil {
		t.Fatal(err)
	}
	runs := run.New(db)
	for _, env := range []string{"test", "prod"} {
		for i := 0; i < 2; i++ {
			id := fmt.Sprintf("%s-%d", env, i)
			at := time.Now().Add(time.Duration(i) * time.Minute).UTC().Format(time.RFC3339)
			if _, err := db.Exec(`INSERT INTO pipeline_runs(id,project_id,status,trigger_type,trigger_branch,trigger_commit,trigger_actor,resolved_environment,created_at) VALUES(?,'p','success','manual','main','commit','fixture',?,?)`, id, env, at); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO deploy_targets(id,run_id,server_id,server_name,status,message,started_at,finished_at) VALUES(?,?,?,'server','success','',?,?)`, id, id, "server-"+env, at, at); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO run_artifacts(id,run_id,type,name,reference,size_bytes,metadata_json,created_at) VALUES(?,?,'dist','dist','ref',0,'',?)`, id, id, at); err != nil {
				t.Fatal(err)
			}
		}
	}

	if err := auth.NewService(db, nil).Bootstrap("admin", "admin-environment-http"); err != nil {
		t.Fatal(err)
	}
	repo := tenancy.NewRepository(db)
	mode, err := repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true)
	if err != nil {
		t.Fatal(err)
	}
	var team, envID string
	if err = db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT id FROM access_environments WHERE project_id='p' AND name='test'`).Scan(&envID); err != nil {
		t.Fatal(err)
	}
	accounts := tenancy.NewAccounts(db)
	admin, err := accounts.Login(ctx, "admin", "admin-environment-http")
	if err != nil {
		t.Fatal(err)
	}
	scope := authz.Scope{Kind: authz.ScopeSelected, ResourceIDs: []string{envID}, EnvironmentIDs: []string{envID}}
	role, err := repo.SaveRole(ctx, admin.Actor, team, "", tenancy.RoleInput{Name: "Env reader", Pages: []string{"page.environments"}, Grants: []authz.Grant{{Action: "environment.read", Scope: scope}}})
	if err != nil {
		t.Fatal(err)
	}
	initial, err := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: "reader", DisplayName: "Reader", Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeTeam, EnvironmentIDs: []string{envID}}}}})
	if err != nil {
		t.Fatal(err)
	}
	login, err := accounts.Login(ctx, "reader", initial.Password)
	if err != nil {
		t.Fatal(err)
	}
	login, err = accounts.ChangePassword(ctx, login.Token, initial.Password, "reader-environment-http")
	if err != nil {
		t.Fatal(err)
	}
	dep := &stubDeploy{}
	pool := run.NewWorkerPool(runs)
	router := New(testWebFSAuth(), tenancy.NewAuthenticator(accounts, mode.Epoch), WithTenancyAccounts(accounts), WithTenancyRepository(repo), WithEnvironments(environments.NewService(db)), WithRuns(runs, pool), WithDeploy(dep))
	request := func(method, path, selectedTeam string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("X-Pipewright-Team", selectedTeam)
		req.Header.Set(headerCsrf, login.CSRFToken)
		req.AddCookie(&http.Cookie{Name: cookieSession, Value: login.Token})
		req.AddCookie(&http.Cookie{Name: cookieCsrf, Value: login.CSRFToken})
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	t.Run("purpose-mutation-requires-exact-instance-admin", func(t *testing.T) {
		change := func(actor tenancy.Session, teamID, env string) *httptest.ResponseRecorder {
			req := httptest.NewRequest("PUT", "/api/access/teams/"+teamID+"/environments/"+env+"/purpose", strings.NewReader(`{"purpose":"production"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(headerCsrf, actor.CSRFToken)
			req.AddCookie(&http.Cookie{Name: cookieSession, Value: actor.Token})
			req.AddCookie(&http.Cookie{Name: cookieCsrf, Value: actor.CSRFToken})
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			return w
		}
		var prodID string
		if e := db.QueryRow("SELECT id FROM access_environments WHERE project_id='p' AND name='prod'").Scan(&prodID); e != nil {
			t.Fatal(e)
		}
		var originalPurpose string
		if e := db.QueryRow("SELECT purpose FROM access_environments WHERE id=?", envID).Scan(&originalPurpose); e != nil {
			t.Fatal(e)
		}
		if w := change(login, team, envID); w.Code != 403 {
			t.Fatalf("reader changed purpose: %d %s", w.Code, w.Body.String())
		}
		if w := change(admin, "unknown-team", prodID); w.Code != 403 {
			t.Fatalf("wrong parent changed purpose: %d %s", w.Code, w.Body.String())
		}
		if w := change(admin, team, prodID); w.Code != 204 {
			t.Fatalf("administrator change on precise target failed: %d %s", w.Code, w.Body.String())
		}
		for id, want := range map[string]string{prodID: "production", envID: originalPurpose} {
			var actual string
			if e := db.QueryRow("SELECT purpose FROM access_environments WHERE id=?", id).Scan(&actual); e != nil || actual != want {
				t.Fatal("unexpected purpose", actual, want, e)
			}
		}
	})
	w := request("GET", "/api/environments/projects", team)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Visible") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request("GET", "/api/projects/p/environments/deployments", team)
	if w.Code != 200 || !strings.Contains(w.Body.String(), envID) || strings.Contains(w.Body.String(), "prod") {
		t.Fatal("scope before aggregate", w.Code, w.Body.String())
	}
	w = request("GET", "/api/projects/p/environments/test/history", team)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "test-0") || !strings.Contains(w.Body.String(), "test-1") || strings.Contains(w.Body.String(), "prod-") {
		t.Fatalf("visible deployment history = %d %s", w.Code, w.Body.String())
	}
	for _, name := range []string{"prod", "unknown"} {
		w = request("GET", "/api/projects/p/environments/"+name+"/history", team)
		if w.Code != 404 {
			t.Fatal("out of scope", w.Code, w.Body.String())
		}
	}
	w = request("POST", "/api/projects/p/environments/test/rollback", team)
	if w.Code != 403 || dep.lastInput.RunID != "" {
		t.Fatal("read cannot execute", w.Code, dep.lastInput)
	}
	other, err := repo.CreateTeamAs(ctx, admin.Actor, "Other")
	if err != nil {
		t.Fatal(err)
	}
	w = request("GET", "/api/environments/projects", other.ID)
	if w.Code != 403 {
		t.Fatal("forged team", w.Code, w.Body.String())
	}
	_, err = repo.SaveRole(ctx, admin.Actor, team, role.ID, tenancy.RoleInput{ExpectedRevision: role.Revision, Name: "Env reader", Pages: []string{"page.environments"}, Grants: []authz.Grant{{Action: "environment.read", Scope: scope}, {Action: "release.rollback", Scope: scope}}})
	if err != nil {
		t.Fatal(err)
	}
	w = request("POST", "/api/projects/p/environments/test/rollback", team)
	if w.Code != 200 || dep.lastInput.RunID == "" {
		t.Fatal("explicit action", w.Code, w.Body.String())
	}
	dep.lastInput.RunID = ""
	w = request("POST", "/api/projects/p/environments/prod/rollback", team)
	if w.Code != 403 || dep.lastInput.RunID != "" {
		t.Fatal("outside action wrote", w.Code)
	}
	if err = accounts.Logout(ctx, login.Token); err != nil {
		t.Fatal(err)
	}
	if w = request("GET", "/api/environments/projects", team); w.Code != 401 {
		t.Fatal("logged out", w.Code)
	}
}
