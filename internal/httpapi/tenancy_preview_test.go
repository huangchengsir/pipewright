package httpapi

import (
	"context"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/previewenv"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTenantPreviewHTTPScopesAndDeniedEffects(t *testing.T) {
	ctx := context.Background()
	db := storetest.OpenDB(t)
	first := seedDoraProject(t, db, "Allowed")
	second := seedDoraProject(t, db, "Hidden")
	for _, v := range []struct{ id, project string }{{"allowed", first}, {"hidden", second}} {
		if _, err := db.Exec(`INSERT INTO preview_envs(id,project_id,pipeline_id,pr_number,branch,server_id,route_id,subdomain,status,created_at,reclaimed_at) VALUES (?,?,'',1,'main','s','r','test.invalid','active','2026-01-01T00:00:00Z','')`, v.id, v.project); err != nil {
			t.Fatal(err)
		}
	}
	if err := auth.NewService(db, nil).Bootstrap("admin", "preview-admin-password"); err != nil {
		t.Fatal(err)
	}
	repo := tenancy.NewRepository(db)
	mode, err := repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true)
	if err != nil {
		t.Fatal(err)
	}
	accounts := tenancy.NewAccounts(db)
	admin, err := accounts.Login(ctx, "admin", "preview-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	var team string
	if err = db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); err != nil {
		t.Fatal(err)
	}
	scope := authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{first}}
	role, err := repo.SaveRole(ctx, admin.Actor, team, "", tenancy.RoleInput{Name: "Preview reader", Pages: []string{"page.previews"}, Grants: []authz.Grant{{Action: "preview.read", Scope: scope}}})
	if err != nil {
		t.Fatal(err)
	}
	initial, err := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: "preview", DisplayName: "Preview", Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
	if err != nil {
		t.Fatal(err)
	}
	login, err := accounts.Login(ctx, "preview", initial.Password)
	if err != nil {
		t.Fatal(err)
	}
	login, err = accounts.ChangePassword(ctx, login.Token, initial.Password, "preview-new-password")
	if err != nil {
		t.Fatal(err)
	}
	router := New(testWebFSAuth(), tenancy.NewAuthenticator(accounts, mode.Epoch), WithTenancyAccounts(accounts), WithPreviewEnvs(previewenv.New(db)))
	request := func(method, path, body, token string, csrf bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("X-Pipewright-Team", team)
		if token != "" {
			r.AddCookie(&http.Cookie{Name: cookieSession, Value: token})
		}
		if csrf {
			r.AddCookie(&http.Cookie{Name: cookieCsrf, Value: login.CSRFToken})
			r.Header.Set("X-CSRF-Token", login.CSRFToken)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	w := request("GET", "/api/preview-envs/projects", "", login.Token, false)
	var picker struct {
		Items []map[string]any `json:"items"`
	}
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if err = json.Unmarshal(w.Body.Bytes(), &picker); err != nil || len(picker.Items) != 1 || len(picker.Items[0]) != 2 || picker.Items[0]["id"] != first {
		t.Fatal("minimal scoped picker", picker, err)
	}
	w = request("GET", "/api/preview-envs", "", login.Token, false)
	if w.Code != 200 || strings.Contains(w.Body.String(), "hidden") || !strings.Contains(w.Body.String(), "allowed") {
		t.Fatal("scope", w.Code, w.Body.String())
	}
	for _, path := range []string{"/api/preview-envs/hidden/reclaim", "/api/preview-envs/unknown/reclaim"} {
		w = request("POST", path, "{}", login.Token, true)
		if w.Code != 404 {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	for _, csrf := range []bool{false, true} {
		w = request("POST", "/api/preview-envs/allowed/reclaim", "{}", login.Token, csrf)
		if w.Code != 403 {
			t.Fatal("reclaim", csrf, w.Code, w.Body.String())
		}
	}
	w = request("PUT", "/api/projects/"+first+"/preview-config", `{"enabled":false}`, login.Token, true)
	if w.Code != 403 {
		t.Fatal("config", w.Code, w.Body.String())
	}
	var state string
	if err = db.QueryRow(`SELECT status FROM preview_envs WHERE id='allowed'`).Scan(&state); err != nil || state != "active" {
		t.Fatal("denied mutation", state, err)
	}
	var count int
	if err = db.QueryRow(`SELECT COUNT(*) FROM preview_configs`).Scan(&count); err != nil || count != 0 {
		t.Fatal("denied config persisted", count, err)
	}
	if err = accounts.Logout(ctx, login.Token); err != nil {
		t.Fatal(err)
	}
	w = request("GET", "/api/preview-envs", "", login.Token, false)
	if w.Code != 401 {
		t.Fatal("logout", w.Code)
	}
}
