package httpapi

import (
	"context"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"github.com/huangchengsir/pipewright/internal/vault"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestTenantManagementHTTPReadAndWrite(t *testing.T) {
	db := storetest.OpenDB(t)
	ctx := context.Background()
	legacy := auth.NewService(db, nil)
	if err := legacy.Bootstrap("admin", "admin-password"); err != nil {
		t.Fatal(err)
	}
	repo := tenancy.NewRepository(db)
	mode, err := repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true)
	if err != nil {
		t.Fatal(err)
	}
	accounts := tenancy.NewAccounts(db)
	admin, err := accounts.Login(ctx, "admin", "admin-password")
	if err != nil {
		t.Fatal(err)
	}
	router := New(testWebFSAuth(), tenancy.NewAuthenticator(accounts, mode.Epoch), WithTenancyAccounts(accounts), WithTenancyRepository(repo))
	request := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(headerCsrf, admin.CSRFToken)
		req.AddCookie(&http.Cookie{Name: cookieSession, Value: admin.Token})
		req.AddCookie(&http.Cookie{Name: cookieCsrf, Value: admin.CSRFToken})
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		return out
	}
	created := request("POST", "/api/access/teams", `{"name":"Delivery"}`)
	if created.Code != 201 {
		t.Fatal(created.Code, created.Body.String())
	}
	var team tenancy.TeamView
	if err = json.Unmarshal(created.Body.Bytes(), &team); err != nil {
		t.Fatal(err)
	}
	base := "/api/access/teams/" + team.ID
	if out := request("GET", base+"/navigation", ""); out.Code != 200 || !strings.Contains(out.Body.String(), `"page.system"`) {
		t.Fatal("navigation", out.Code, out.Body.String())
	}
	listed := request("GET", base+"/roles", "")
	if listed.Code != 200 {
		t.Fatal(listed.Code, listed.Body.String())
	}
	var roles []tenancy.RoleView
	if err = json.Unmarshal(listed.Body.Bytes(), &roles); err != nil || len(roles) != 4 {
		t.Fatal(err, len(roles))
	}
	saved := request("POST", base+"/roles", `{"name":"Review","requiresSelectedBinding":true,"pages":["page.projects"],"grants":[{"action":"project.read","scope":{"kind":"team"}}]}`)
	if saved.Code != 200 {
		t.Fatal(saved.Code, saved.Body.String())
	}

	invalid := request("POST", base+"/roles", `{"name":"Unsafe","pages":["page.system"],"grants":[]}`)
	if invalid.Code != 400 {
		t.Fatal(invalid.Code, invalid.Body.String())
	}
	if out := request("GET", base+"/members", ""); out.Code != 200 || strings.TrimSpace(out.Body.String()) != "[]" {
		t.Fatal(out.Code, out.Body.String())
	}
	if out := request("GET", base+"/capabilities", ""); out.Code != 200 || !strings.Contains(out.Body.String(), `"member.manage":true`) {
		t.Fatal(out.Code, out.Body.String())
	}
	if out := request("GET", base+"/choices", ""); out.Code != 200 {
		t.Fatal(out.Code, out.Body.String())
	}
	if out := request("GET", "/api/access/catalog", ""); out.Code != 200 || strings.Contains(out.Body.String(), `"system.manage"`) {
		t.Fatal(out.Code, out.Body.String())
	}
	var readerRole tenancy.RoleView
	if err = json.Unmarshal(saved.Body.Bytes(), &readerRole); err != nil {
		t.Fatal(err)
	}
	employee, err := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: "menu-reader", DisplayName: "Menu Reader", Assignments: []tenancy.MemberAssignment{{TeamID: team.ID, RoleID: readerRole.ID, Scope: authz.Scope{Kind: authz.ScopeSelected}}}})
	if err != nil {
		t.Fatal(err)
	}
	member, err := accounts.Login(ctx, "menu-reader", employee.Password)
	if err != nil {
		t.Fatal(err)
	}
	member, err = accounts.ChangePassword(ctx, member.Token, employee.Password, "menu-reader-password")
	if err != nil {
		t.Fatal(err)
	}
	originalAdmin := admin
	admin = member
	out := request("GET", base+"/navigation", "")
	if out.Code != 200 || !strings.Contains(out.Body.String(), `"pages":["page.projects"]`) {
		t.Fatal("member menu only", out.Code, out.Body.String())
	}
	if out = request("GET", "/api/access/teams/unknown/navigation", ""); out.Code != 403 {
		t.Fatal("foreign menus", out.Code)
	}
	if denied := request(http.MethodPut, base+"/roles/"+readerRole.ID, "{\"name\":\"Denied\",\"expectedRevision\":1}"); denied.Code != http.StatusForbidden {
		t.Fatalf("ordinary team member role edit returned %d", denied.Code)
	}
	if scoped := request(http.MethodGet, base+"/members", ""); scoped.Code != http.StatusOK || strings.TrimSpace(scoped.Body.String()) != "[]" {
		t.Fatalf("ordinary team member directory returned %d %s", scoped.Code, scoped.Body.String())
	}
	if err = accounts.Logout(ctx, member.Token); err != nil {
		t.Fatal(err)
	}
	if out = request("GET", base+"/navigation", ""); out.Code != 401 {
		t.Fatal("logout menus", out.Code)
	}
	admin = originalAdmin
	t.Run("role-and-member-writes", func(t *testing.T) {
		call := func(method, path string, data any, want int) []byte {
			t.Helper()
			body, err := json.Marshal(data)
			if err != nil {
				t.Fatal(err)
			}
			out := request(method, path, string(body))
			if out.Code != want {
				t.Fatalf("%s %s returned %d, want %d: %s", method, path, out.Code, want, out.Body.String())
			}
			return out.Body.Bytes()
		}
		roleInput := tenancy.RoleInput{Name: "Temporary reviewer", Pages: []string{"page.projects"}}
		var createdRole, updatedRole tenancy.RoleView
		if err := json.Unmarshal(call(http.MethodPost, base+"/roles", roleInput, http.StatusOK), &createdRole); err != nil {
			t.Fatal(err)
		}
		rolePath := base + "/roles/" + createdRole.ID
		roleInput.Name = "Temporary reviewer updated"
		roleInput.ExpectedRevision = createdRole.Revision
		if err := json.Unmarshal(call(http.MethodPut, rolePath, roleInput, http.StatusOK), &updatedRole); err != nil || updatedRole.ID != createdRole.ID || updatedRole.NameKey != roleInput.Name {
			t.Fatalf("unexpected role edit: %#v %v", updatedRole, err)
		}
		call(http.MethodPut, rolePath, roleInput, http.StatusConflict)
		call(http.MethodDelete, rolePath, map[string]uint64{"expectedRevision": createdRole.Revision}, http.StatusConflict)
		call(http.MethodDelete, rolePath, map[string]uint64{"expectedRevision": updatedRole.Revision}, http.StatusNoContent)
		roles := request(http.MethodGet, base+"/roles", "")
		if roles.Code != http.StatusOK || strings.Contains(roles.Body.String(), updatedRole.ID) {
			t.Fatalf("deleted role still appears: %d", roles.Code)
		}

		person, err := accounts.Create(ctx, originalAdmin.Actor, tenancy.CreateUserInput{Username: "new-member", DisplayName: "New Member"})
		if err != nil {
			t.Fatal(err)
		}
		in := tenancy.MemberInput{UserID: person.User.ID, Enabled: true}
		var member tenancy.MemberView
		if err := json.Unmarshal(call(http.MethodPost, base+"/members", in, http.StatusOK), &member); err != nil || !member.Enabled || member.UserID != in.UserID {
			t.Fatalf("unexpected created membership: %#v %v", member, err)
		}
		memberPath := base + "/members/" + member.ID
		in.Enabled = false
		in.ExpectedRevision = member.Revision
		var updated tenancy.MemberView
		if err := json.Unmarshal(call(http.MethodPut, memberPath, in, http.StatusOK), &updated); err != nil || updated.Enabled || updated.ID != member.ID {
			t.Fatalf("unexpected member update: %#v %v", updated, err)
		}
		in.Enabled = true
		call(http.MethodPut, memberPath, in, http.StatusConflict)
		var enabled bool
		if err := db.QueryRow("SELECT enabled FROM access_members WHERE id=?", member.ID).Scan(&enabled); err != nil || enabled {
			t.Fatalf("stale authorization updated membership: %v %v", enabled, err)
		}
	})
	for i := 0; i < 60; i++ {
		out := request("POST", base+"/lookup-account", `{"username":"missing"}`)
		if out.Code != 400 {
			t.Fatal("lookup before budget", i, out.Code, out.Body.String())
		}
	}
	limited := request("POST", base+"/lookup-account", `{"username":"admin"}`)
	if limited.Code != 429 || limited.Header().Get("Retry-After") == "" {
		t.Fatal("lookup budget missing", limited.Code, limited.Header())
	}
	if _, err = db.Exec(`UPDATE selfupdate_control SET task_id='maintenance' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if out := request("POST", "/api/access/teams", `{"name":"Blocked"}`); out.Code != 409 {
		t.Fatal(out.Code, out.Body.String())
	}
}

func TestTenantManagementChoicesRespectEveryTypedScope(t *testing.T) {
	f := newTenantAuxFixture(t)
	ctx := context.Background()
	accounts := tenancy.NewAccounts(f.db)
	admin, err := accounts.Login(ctx, "admin", "auxiliary-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	member, err := accounts.Login(ctx, "auxmember", "auxiliary-member-password")
	if err != nil {
		t.Fatal(err)
	}
	repo := tenancy.NewRepository(f.db)
	var team, foreignTeam, roleID string
	var roleRevision uint64
	if err = f.db.QueryRow("SELECT default_team_id FROM access_control WHERE id=1").Scan(&team); err != nil {
		t.Fatal(err)
	}
	if err = f.db.QueryRow("SELECT tenant_id FROM access_ownership WHERE resource_kind='project' AND resource_id='foreign'").Scan(&foreignTeam); err != nil {
		t.Fatal(err)
	}
	if err = f.db.QueryRow("SELECT id,revision FROM access_roles WHERE name='Auxiliary' AND tenant_id=?", team).Scan(&roleID, &roleRevision); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct{ id, project, tenant, purpose string }{
		{"pick-test", "p", team, "test"}, {"pick-prod", "p", team, "production"},
		{"pick-outside", "outside", team, "test"}, {"pick-foreign", "foreign", foreignTeam, "test"},
	} {
		if _, err = f.db.Exec("INSERT INTO access_environments(id,tenant_id,project_id,name,purpose,revision) VALUES(?,?,?,?,?,1)", item.id, item.tenant, item.project, item.id, item.purpose); err != nil {
			t.Fatal(err)
		}
		if _, err = f.db.Exec("INSERT INTO access_ownership(resource_kind,resource_id,tenant_id,project_id,environment_id) VALUES('environment',?,?,?,?)", item.id, item.tenant, item.project, item.id); err != nil {
			t.Fatal(err)
		}
	}
	const secret = "synthetic-choice-secret"
	v := vault.New(f.db, testMasterKey())
	newCred := func(tenant, name string) string {
		t.Helper()
		row, err := vault.WithContext(tenancy.WithActor(ctx, admin.Actor, tenant), v).Create(vault.CreateInput{Name: name, Type: vault.TypeGitToken, Secret: secret})
		if err != nil {
			t.Fatal(err)
		}
		return row.ID
	}
	allowedCred := newCred(team, "Allowed credential label")
	_ = newCred(team, "Not selected credential label")
	_ = newCred(foreignTeam, "Other tenant credential label")
	// Deliberately reuse the exact server ID under a different resource kind.
	if _, err = f.db.Exec("INSERT INTO access_ownership(resource_kind,resource_id,tenant_id) VALUES('template','s1',?)", team); err != nil {
		t.Fatal(err)
	}
	selected := func(id string) authz.Scope { return authz.Scope{Kind: authz.ScopeSelected, ResourceIDs: []string{id}} }
	projectGrant := authz.Grant{Action: "project.read", Scope: authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{"p"}}}
	input := tenancy.RoleInput{Name: "Auxiliary", Pages: []string{"page.projects", "page.environments", "page.vault", "page.servers"}}
	save := func(grants ...authz.Grant) {
		t.Helper()
		input.ExpectedRevision, input.Grants = roleRevision, grants
		row, err := repo.SaveRole(ctx, admin.Actor, team, roleID, input)
		if err != nil {
			t.Fatal(err)
		}
		roleRevision = row.Revision
	}
	save(projectGrant, authz.Grant{Action: "environment.read", Scope: selected("pick-test")},
		authz.Grant{Action: "credential.metadata", Scope: selected(allowedCred)}, authz.Grant{Action: "server.inspect", Scope: selected("s1")})
	router := New(testWebFSAuth(), tenancy.NewAuthenticator(accounts, member.Actor.ModeEpoch), WithTenancyAccounts(accounts), WithTenancyRepository(repo))
	request := func(target string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/access/teams/"+target+"/choices", nil)
		req.AddCookie(&http.Cookie{Name: cookieSession, Value: member.Token})
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	read := func(t *testing.T) tenancy.ResourceChoices {
		t.Helper()
		w := request(team)
		var got tenancy.ResourceChoices
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || strings.Contains(w.Body.String(), secret) || strings.Contains(w.Body.String(), "ciphertext") {
			t.Fatalf("choice response: status=%d", w.Code)
		}
		return got
	}
	scopedRequest := func(session tenancy.Session, method, path string, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(headerCsrf, session.CSRFToken)
		req.AddCookie(&http.Cookie{Name: cookieSession, Value: session.Token})
		req.AddCookie(&http.Cookie{Name: cookieCsrf, Value: session.CSRFToken})
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		return out
	}
	t.Run("team-list-and-decision-batch", func(t *testing.T) {
		w := scopedRequest(member, "GET", "/api/access/teams", nil)
		var teams []tenancy.TeamView
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &teams) != nil || len(teams) != 1 || teams[0].ID != team {
			t.Fatalf("teams must list current employee memberships only: %d %s", w.Code, w.Body.String())
		}
		input := map[string]any{"queries": []tenancy.DecisionQuery{
			{Action: "project.read", ResourceID: "p"}, {Action: "project.read", ResourceID: "outside"},
			{Action: "project.read", ResourceID: "foreign"}, {Action: "project.read", ResourceID: "unknown"},
			{Action: "project.edit", ResourceID: "p"}, {Action: "server.inspect", ResourceID: "s1"},
			{Action: "template.read", ResourceID: "s1"}, {Action: "environment.read", ResourceID: "pick-test"},
			{Action: "environment.read", ResourceID: "pick-prod"},
		}}
		w = scopedRequest(member, "POST", "/api/access/teams/"+team+"/decisions", input)
		var decisions tenancy.DecisionBatch
		want := []bool{true, false, false, false, false, true, false, true, false}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &decisions) != nil || decisions.TeamID != team ||
			decisions.UserID != member.Actor.UserID || !reflect.DeepEqual(decisions.Allowed, want) {
			t.Fatalf("exact action decisions: %d %s", w.Code, w.Body.String())
		}
		w = scopedRequest(member, "POST", "/api/access/teams/"+foreignTeam+"/decisions", input)
		if w.Code != 403 || strings.Contains(w.Body.String(), "allowed") {
			t.Fatalf("foreign tenant decision exposed: %d %s", w.Code, w.Body.String())
		}
	})
	t.Run("role-preview-is-scoped-and-read-only", func(t *testing.T) {
		var memberID string
		if e := f.db.QueryRow("SELECT id FROM access_members WHERE user_id=? AND tenant_id=?", member.Actor.UserID, team).Scan(&memberID); e != nil {
			t.Fatal(e)
		}
		edit := authz.Grant{Action: "project.edit", Scope: projectGrant.Scope}
		draft := input
		draft.Grants = append(append([]authz.Grant{}, input.Grants...), edit)
		draft.ExpectedRevision = roleRevision
		body := tenancy.PreviewInput{MemberID: memberID, Action: "project.edit", ResourceID: "p", DraftRoleID: roleID, Draft: &draft}
		path := "/api/access/teams/" + team + "/preview"
		if w := scopedRequest(member, "POST", path, body); w.Code != 403 {
			t.Fatalf("member without role read previewed access: %d %s", w.Code, w.Body.String())
		}
		w := scopedRequest(admin, "POST", path, body)
		var preview tenancy.PreviewResult
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &preview) != nil || preview.CurrentAllowed ||
			preview.DraftAllowed == nil || !*preview.DraftAllowed {
			t.Fatalf("authorized preview should only report hypothetical change: %d %s", w.Code, w.Body.String())
		}
		var stored int64
		if e := f.db.QueryRow("SELECT revision FROM access_roles WHERE id=?", roleID).Scan(&stored); e != nil || uint64(stored) != roleRevision {
			t.Fatal("preview wrote role", e, stored, roleRevision)
		}
		w = scopedRequest(member, "POST", "/api/access/teams/"+team+"/decisions", map[string]any{"queries": []tenancy.DecisionQuery{{Action: "project.edit", ResourceID: "p"}}})
		var actual tenancy.DecisionBatch
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &actual) != nil || !reflect.DeepEqual(actual.Allowed, []bool{false}) {
			t.Fatalf("preview altered effective permissions: %d %s", w.Code, w.Body.String())
		}
	})

	choices := read(t)
	t.Run("project-only-selected", func(t *testing.T) {
		if len(choices.Projects) != 1 || choices.Projects[0].ID != "p" {
			t.Fatalf("project choices = %+v", choices.Projects)
		}
	})
	t.Run("environment-only-selected", func(t *testing.T) {
		if len(choices.Environments) != 1 || choices.Environments[0].ID != "pick-test" || choices.Environments[0].ProjectID != "p" || choices.Environments[0].Purpose != "test" {
			t.Fatalf("environment choices = %+v", choices.Environments)
		}
	})
	t.Run("resources-only-selected-and-typed", func(t *testing.T) {
		if len(choices.Resources) != 2 || choices.Resources[0].Kind != "credential" || choices.Resources[0].ID != allowedCred || choices.Resources[0].Name != "Allowed credential label" || choices.Resources[1].Kind != "server" || choices.Resources[1].ID != "s1" {
			t.Fatal("unexpected typed resource choices")
		}
	})
	t.Run("other-team-rejected", func(t *testing.T) {
		if w := request(foreignTeam); w.Code != 403 {
			t.Fatalf("other team choices returned %d", w.Code)
		}
	})
	save(projectGrant)
	t.Run("revoked-same-session", func(t *testing.T) {
		choices := read(t)
		if len(choices.Projects) != 1 || choices.Projects[0].ID != "p" || len(choices.Resources) != 0 || len(choices.Environments) != 0 {
			t.Fatal("revoked choice was still returned")
		}
	})
}
