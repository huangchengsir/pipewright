package httpapi

import (
	"context"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/dnsprovider"
	"github.com/huangchengsir/pipewright/internal/previewenv"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"github.com/huangchengsir/pipewright/internal/vault"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTenantDNSUsableHTTPIntersectsCredentialAndProvider(t *testing.T) {
	ctx := context.Background()
	db := storetest.OpenDB(t)
	v := vault.New(db, testMasterKey())
	svc := dnsprovider.New(db, v, nil)
	if err := auth.NewService(db, nil).Bootstrap("admin", "dns-http-admin-password"); err != nil {
		t.Fatal(err)
	}
	repo := tenancy.NewRepository(db)
	mode, err := repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true)
	if err != nil {
		t.Fatal(err)
	}
	accounts := tenancy.NewAccounts(db)
	admin, err := accounts.Login(ctx, "admin", "dns-http-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	var team string
	if err = db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); err != nil {
		t.Fatal(err)
	}
	role, err := repo.SaveRole(ctx, admin.Actor, team, "", tenancy.RoleInput{Name: "Project reader", Pages: []string{"page.projects"}, Grants: []authz.Grant{{Action: "project.read", Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
	if err != nil {
		t.Fatal(err)
	}
	initial, err := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: "dnshttp", DisplayName: "DNS", Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
	if err != nil {
		t.Fatal(err)
	}
	login, err := accounts.Login(ctx, "dnshttp", initial.Password)
	if err != nil {
		t.Fatal(err)
	}
	login, err = accounts.ChangePassword(ctx, login.Token, initial.Password, "dns-http-user-password")
	if err != nil {
		t.Fatal(err)
	}
	router := New(testWebFSAuth(), tenancy.NewAuthenticator(accounts, mode.Epoch), WithTenancyAccounts(accounts), WithVault(v), WithDNSProviders(svc), WithPreviewEnvs(previewenv.New(db)))
	request := func(method, path, body, token, csrf string) *httptest.ResponseRecorder {
		if !strings.HasPrefix(path, "/api/") {
			path = "/api/dns/providers" + path
		}
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("X-Pipewright-Team", team)
		r.AddCookie(&http.Cookie{Name: cookieSession, Value: token})
		if csrf != "" {
			r.AddCookie(&http.Cookie{Name: cookieCsrf, Value: csrf})
			r.Header.Set("X-CSRF-Token", csrf)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}

	adminCtx := tenancy.WithActor(ctx, admin.Actor, team)
	creator := svc.(interface {
		CreateWithToken(context.Context, dnsprovider.CreateInput, string) (*dnsprovider.Provider, error)
	})
	a, e := creator.CreateWithToken(adminCtx, dnsprovider.CreateInput{Type: dnsprovider.TypeCloudflare, Name: "Runtime only", BaseDomain: "example.invalid"}, "synthetic-private-runtime-value")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = creator.CreateWithToken(adminCtx, dnsprovider.CreateInput{Type: dnsprovider.TypeCloudflare, Name: "Outside", BaseDomain: "outside.invalid"}, "other-private-value"); e != nil {
		t.Fatal(e)
	}
	gitCred, err := vault.WithContext(adminCtx, v).Create(vault.CreateInput{Name: "Fixture source", Type: vault.TypeGitToken, Secret: "fixture-source"})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"preview-p", "preview-other"} {
		if _, e = db.Exec(`INSERT INTO projects(id,name,repo_url,credential_id,created_at,updated_at) VALUES (?,?,'https://example.invalid/source',?,'now','now')`, id, id, gitCred.ID); e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec(`INSERT INTO access_ownership(resource_kind,resource_id,tenant_id,owner_id,project_id) VALUES ('project',?,?,?,?)`, id, team, admin.Actor.UserID, id); e != nil {
			t.Fatal(e)
		}
	}
	input := tenancy.RoleInput{Name: "Runtime DNS", Pages: []string{"page.containers", "page.vault", "page.pipeline"}, Grants: []authz.Grant{{Action: "dns.use", Scope: authz.Scope{Kind: authz.ScopeSelected, ResourceIDs: []string{a.ID}}}}}
	save := func() {
		input.ExpectedRevision = role.Revision
		var err error
		role, err = repo.SaveRole(ctx, admin.Actor, team, role.ID, input)
		if err != nil {
			t.Fatal(err)
		}
	}
	input.Grants = append(input.Grants, authz.Grant{Action: "pipeline.read", Scope: authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{"preview-p"}}})
	save()
	w := request("GET", "/usable", "", login.Token, "")
	if w.Code != 200 || strings.Contains(w.Body.String(), a.ID) {
		t.Fatal("credential-denied picker", w.Code, w.Body.String())
	}
	input.Grants = append(input.Grants, authz.Grant{Action: "credential.metadata", Scope: authz.Scope{Kind: authz.ScopeSelected, ResourceIDs: []string{a.CredentialID}}}, authz.Grant{Action: "credential.use", Scope: authz.Scope{Kind: authz.ScopeSelected, ResourceIDs: []string{a.CredentialID}}})
	save()
	w = request("GET", "/usable", "", login.Token, "")
	var result struct {
		Items []map[string]any `json:"items"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || len(result.Items) != 1 || result.Items[0]["id"] != a.ID || len(result.Items[0]) != 4 {
		t.Fatal("minimal scoped picker", w.Code, w.Body.String())
	}
	for _, secret := range []string{a.CredentialID, "synthetic-private-runtime-value", "Outside"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("picker exposed hidden field")
		}
	}
	w = request("GET", "", "", login.Token, "")
	if w.Code != 403 {
		t.Fatal("use opened global settings", w.Code)
	}
	t.Run("project-preview-provider-picker", func(t *testing.T) {
		for _, check := range []struct {
			project string
			status  int
		}{{"preview-p", 200}, {"preview-other", 403}, {"missing", 403}} {
			w := request("GET", "/api/projects/"+check.project+"/preview-providers", "", login.Token, "")
			if w.Code != check.status || (w.Code == 200 && !strings.Contains(w.Body.String(), a.ID)) || strings.Contains(w.Body.String(), a.CredentialID) || strings.Contains(w.Body.String(), "Outside") {
				t.Fatalf("wrong scoped picker/project %s: %d %s", check.project, w.Code, w.Body.String())
			}
		}
	})
	input.Grants = nil
	save()
	w = request("GET", "/usable", "", login.Token, "")
	if w.Code != 200 || strings.Contains(w.Body.String(), a.ID) {
		t.Fatal("revoked picker", w.Code)
	}
	if e = accounts.Logout(ctx, login.Token); e != nil {
		t.Fatal(e)
	}
	w = request("GET", "/usable", "", login.Token, "")
	if w.Code != 401 {
		t.Fatal("logout picker", w.Code)
	}
}
