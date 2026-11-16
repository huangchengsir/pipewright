package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"github.com/huangchengsir/pipewright/internal/vault"
)

func TestTenantCredentialHTTPActionsAndRowScopes(t *testing.T) {
	ctx := context.Background()
	db := storetest.OpenDB(t)
	v := vault.New(db, testMasterKey())
	if err := auth.NewService(db, nil).Bootstrap("admin", "credential-scope-admin"); err != nil {
		t.Fatal(err)
	}
	repo := tenancy.NewRepository(db)
	mode, err := repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true)
	if err != nil {
		t.Fatal(err)
	}
	accounts := tenancy.NewAccounts(db)
	admin, err := accounts.Login(ctx, "admin", "credential-scope-admin")
	if err != nil {
		t.Fatal(err)
	}
	var team string
	if err = db.QueryRowContext(ctx, "SELECT default_team_id FROM access_control WHERE id=1").Scan(&team); err != nil {
		t.Fatal(err)
	}
	foreignTeam, err := repo.CreateTeamAs(ctx, admin.Actor, "Other vault")
	if err != nil {
		t.Fatal(err)
	}
	adminVault := vault.WithContext(tenancy.WithActor(ctx, admin.Actor, team), v)
	const fixtureSecret = "synthetic-private-value"
	create := func(v vault.Vault, name string) string {
		t.Helper()
		c, err := v.Create(vault.CreateInput{Name: name, Type: vault.TypeGitToken, Secret: fixtureSecret})
		if err != nil {
			t.Fatal(err)
		}
		return c.ID
	}
	selected := create(adminVault, "Selected")
	neighbor := create(adminVault, "Same team outside scope")
	foreign := create(vault.WithContext(tenancy.WithActor(ctx, admin.Actor, foreignTeam.ID), v), "Foreign")
	teamScope := authz.Scope{Kind: authz.ScopeTeam}
	selectedScope := authz.Scope{Kind: authz.ScopeSelected, ResourceIDs: []string{selected}}
	input := tenancy.RoleInput{Name: "Vault employee", Pages: []string{"page.vault"}, Grants: []authz.Grant{{Action: "credential.metadata", Scope: selectedScope}}}
	role, err := repo.SaveRole(ctx, admin.Actor, team, "", input)
	if err != nil {
		t.Fatal(err)
	}
	setGrants := func(grants ...authz.Grant) {
		t.Helper()
		input.ExpectedRevision, input.Grants = role.Revision, grants
		next, err := repo.SaveRole(ctx, admin.Actor, team, role.ID, input)
		if err != nil {
			t.Fatal(err)
		}
		role = next
	}
	initial, err := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{
		Username: "vault-member", DisplayName: "Vault member",
		Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: teamScope}},
	})
	if err != nil {
		t.Fatal(err)
	}
	member, err := accounts.Login(ctx, "vault-member", initial.Password)
	if err != nil {
		t.Fatal(err)
	}
	member, err = accounts.ChangePassword(ctx, member.Token, initial.Password, "credential-scope-member")
	if err != nil {
		t.Fatal(err)
	}
	router := New(testWebFSAuth(), tenancy.NewAuthenticator(accounts, mode.Epoch), WithTenancyAccounts(accounts), WithVault(v))
	const root = "/api/credentials"
	request := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Pipewright-Team", team)
		req.Header.Set(headerCsrf, member.CSRFToken)
		req.AddCookie(&http.Cookie{Name: cookieSession, Value: member.Token})
		req.AddCookie(&http.Cookie{Name: cookieCsrf, Value: member.CSRFToken})
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, req)
		return resp
	}
	assertList := func(want ...string) {
		t.Helper()
		w := request("GET", root, "")
		var items []credentialDTO
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &items) != nil || strings.Contains(w.Body.String(), fixtureSecret) || strings.Contains(w.Body.String(), "ciphertext") {
			t.Fatalf("credential list: status=%d items=%d", w.Code, len(items))
		}
		ids := make([]string, 0, len(items))
		for _, item := range items {
			ids = append(ids, item.ID)
		}
		slices.Sort(ids)
		slices.Sort(want)
		if !slices.Equal(ids, want) {
			t.Fatalf("credential list IDs = %v, want %v", ids, want)
		}
	}
	assertDenied := func(t *testing.T, method, path, body string) {
		t.Helper()
		w := request(method, path, body)
		if w.Code != 403 || !strings.Contains(w.Body.String(), "access_denied") || strings.Contains(w.Body.String(), fixtureSecret) {
			t.Fatalf("%s denied action: status=%d", method, w.Code)
		}
	}
	const newCredential = `{"name":"Created","type":"git_token","secret":"synthetic-private-value"}`
	t.Run("metadata-selected", func(t *testing.T) {
		assertList(selected)
	})
	for _, action := range []struct{ name, method, suffix, body string }{
		{"create-with-metadata-only", "POST", "", newCredential},
		{"edit-with-metadata-only", "PATCH", "/" + selected, `{"name":"Unexpected edit"}`},
		{"delete-with-metadata-only", "DELETE", "/" + selected, ""},
		{"reveal-with-metadata-only", "POST", "/" + selected + "/reveal", "{}"},
	} {
		t.Run(action.name, func(t *testing.T) { assertDenied(t, action.method, root+action.suffix, action.body) })
	}
	setGrants(authz.Grant{Action: "credential.metadata", Scope: teamScope},
		authz.Grant{Action: "credential.create", Scope: teamScope},
		authz.Grant{Action: "credential.edit", Scope: selectedScope},
		authz.Grant{Action: "credential.reveal", Scope: selectedScope},
		authz.Grant{Action: "credential.delete", Scope: selectedScope})
	t.Run("metadata-team", func(t *testing.T) {
		assertList(selected, neighbor)
	})
	for _, resource := range []struct{ name, id string }{{"same-team-outside-scope", neighbor}, {"other-team", foreign}, {"unknown-id", "unknown"}} {
		t.Run(resource.name, func(t *testing.T) {
			t.Run("edit", func(t *testing.T) { assertDenied(t, "PATCH", root+"/"+resource.id, `{"name":"Unexpected edit"}`) })
			t.Run("delete", func(t *testing.T) { assertDenied(t, "DELETE", root+"/"+resource.id, "") })
			t.Run("reveal", func(t *testing.T) { assertDenied(t, "POST", root+"/"+resource.id+"/reveal", "{}") })
		})
	}
	t.Run("selected-edit-and-reveal", func(t *testing.T) {
		w := request("PATCH", root+"/"+selected, `{"name":"Updated"}`)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "Updated") || strings.Contains(w.Body.String(), fixtureSecret) {
			t.Fatalf("selected edit: status=%d", w.Code)
		}
		w = request("POST", root+"/"+selected+"/reveal", "{}")
		var out map[string]string
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || len(out) != 1 || out["secret"] != fixtureSecret {
			t.Fatalf("selected reveal: status=%d", w.Code)
		}
	})
	var owned credentialDTO
	t.Run("create-owned", func(t *testing.T) {
		w := request("POST", root, newCredential)
		if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &owned) != nil || owned.ID == "" || strings.Contains(w.Body.String(), fixtureSecret) {
			t.Fatalf("create with permission: status=%d", w.Code)
		}
	})
	t.Run("selected-delete", func(t *testing.T) {
		if w := request("DELETE", root+"/"+selected, ""); w.Code != 204 {
			t.Fatalf("selected delete: status=%d", w.Code)
		}
		var count int
		err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM credentials WHERE id=?", selected).Scan(&count)
		if err != nil || count != 0 {
			t.Fatal("deleted credential still present", count, err)
		}
	})
	t.Run("denied-actions-had-no-effect", func(t *testing.T) {
		for _, item := range []struct {
			id, name string
			reader   vault.Vault
		}{{neighbor, "Same team outside scope", adminVault}, {foreign, "Foreign", vault.WithContext(tenancy.WithActor(ctx, admin.Actor, foreignTeam.ID), v)}} {
			rows, err := item.reader.List()
			kept := slices.ContainsFunc(rows, func(c vault.Credential) bool { return c.ID == item.id && c.Name == item.name })
			if err != nil || !kept {
				t.Fatalf("denied credential changed: id=%s error=%v", item.id, err)
			}
		}
	})
	setGrants(authz.Grant{Action: "credential.metadata", Scope: authz.Scope{Kind: authz.ScopeOwn}})
	t.Run("metadata-only-owner", func(t *testing.T) {
		assertList(owned.ID)
		assertDenied(t, "POST", root+"/"+owned.ID+"/reveal", "{}")
		assertDenied(t, "DELETE", root+"/"+owned.ID, "")
	})
	setGrants()
	t.Run("metadata-revoked-same-session", func(t *testing.T) {
		assertList()
	})
}
