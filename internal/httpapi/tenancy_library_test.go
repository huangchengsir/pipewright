package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/library"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTenantLibraryHTTPReadWriteBoundaries(t *testing.T) {
	ctx := context.Background()
	db := storetest.OpenDB(t)
	tpl := library.NewTemplateService(db, pipeline.New(db))
	groups := library.NewVarGroupService(db, nil)
	nodes := library.NewCustomNodeService(db)
	spec := pipeline.Spec{Stages: []pipeline.Stage{{ID: "src", Name: "Source", Kind: pipeline.KindSource, Jobs: []pipeline.Job{{ID: "git", Name: "Git", Type: "git_source"}}}}}
	type asset struct {
		kind, path, table, body string
		ids                     []string
	}
	assets := []asset{{kind: "template", path: "/api/templates", table: "pipeline_templates", body: `{"name":"Denied","stages":[{"id":"src","name":"Source","kind":"source","jobs":[{"id":"git","name":"Git","type":"git_source"}]}]}`}, {kind: "variable_group", path: "/api/variable-groups", table: "variable_groups", body: `{"name":"Denied","vars":[]}`}, {kind: "custom_node", path: "/api/custom-nodes", table: "custom_nodes", body: `{"name":"Denied","nodeType":"script","config":{}}`}}
	for i := range assets {
		for _, name := range []string{"Visible", "Outside", "Foreign"} {
			var id string
			var e error
			switch assets[i].kind {
			case "template":
				var v *library.Template
				v, e = tpl.Create(ctx, library.TemplateInput{Name: name, Spec: spec})
				if e == nil {
					id = v.ID
				}
			case "variable_group":
				var v *library.VarGroup
				v, e = groups.Create(ctx, library.VarGroupInput{Name: name})
				if e == nil {
					id = v.ID
				}
			case "custom_node":
				var v *library.CustomNode
				v, e = nodes.Create(ctx, library.CustomNodeInput{Name: name, NodeType: "script"})
				if e == nil {
					id = v.ID
				}
			}
			if e != nil {
				t.Fatal(e)
			}
			assets[i].ids = append(assets[i].ids, id)
		}
	}
	if e := auth.NewService(db, nil).Bootstrap("admin", "admin-library-http-password"); e != nil {
		t.Fatal(e)
	}
	repo := tenancy.NewRepository(db)
	mode, e := repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true)
	if e != nil {
		t.Fatal(e)
	}
	var team string
	if e = db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); e != nil {
		t.Fatal(e)
	}
	accounts := tenancy.NewAccounts(db)
	admin, e := accounts.Login(ctx, "admin", "admin-library-http-password")
	if e != nil {
		t.Fatal(e)
	}
	other, e := repo.CreateTeamAs(ctx, admin.Actor, "Other")
	if e != nil {
		t.Fatal(e)
	}
	grants := []authz.Grant{}
	refs := []authz.ResourceRef{}
	for _, a := range assets {
		grants = append(grants, authz.Grant{Action: a.kind + ".read", Scope: authz.Scope{Kind: authz.ScopeTeam}})
		refs = append(refs, authz.ResourceRef{Kind: a.kind, ID: a.ids[0]})
		if _, e = db.Exec(`UPDATE access_ownership SET tenant_id=? WHERE resource_kind=? AND resource_id=?`, other.ID, a.kind, a.ids[2]); e != nil {
			t.Fatal(e)
		}
	}
	role, e := repo.SaveRole(ctx, admin.Actor, team, "", tenancy.RoleInput{Name: "Reader", Pages: []string{"page.library"}, RequiresSelectedBinding: true, Grants: grants})
	if e != nil {
		t.Fatal(e)
	}
	initial, e := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: "reader", DisplayName: "Reader", Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeSelected, ResourceRefs: refs}}}})
	if e != nil {
		t.Fatal(e)
	}
	login, e := accounts.Login(ctx, "reader", initial.Password)
	if e != nil {
		t.Fatal(e)
	}
	login, e = accounts.ChangePassword(ctx, login.Token, initial.Password, "reader-library-http-password")
	if e != nil {
		t.Fatal(e)
	}
	router := New(testWebFSAuth(), tenancy.NewAuthenticator(accounts, mode.Epoch), WithTenancyAccounts(accounts), WithTemplates(tpl), WithVariableGroups(groups), WithCustomNodes(nodes))
	request := func(method, path, body, token, csrf, selectedTeam string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Pipewright-Team", selectedTeam)
		r.Header.Set(headerCsrf, csrf)
		r.AddCookie(&http.Cookie{Name: cookieSession, Value: token})
		r.AddCookie(&http.Cookie{Name: cookieCsrf, Value: csrf})
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	for _, a := range assets {
		t.Run(a.kind, func(t *testing.T) {
			r := request("GET", a.path, "", login.Token, login.CSRFToken, team)
			if r.Code != 200 || !strings.Contains(r.Body.String(), a.ids[0]) || strings.Contains(r.Body.String(), a.ids[1]) || strings.Contains(r.Body.String(), a.ids[2]) {
				t.Fatal("scoped HTTP list", r.Code, r.Body.String())
			}
			for _, id := range []string{a.ids[1], a.ids[2], "unknown"} {
				r = request("GET", a.path+"/"+id, "", login.Token, login.CSRFToken, team)
				if r.Code != 404 {
					t.Fatal("detail leak", r.Code, r.Body.String())
				}
			}
			r = request("GET", a.path+"/"+a.ids[0], "", login.Token, login.CSRFToken, team)
			if r.Code != 200 {
				t.Fatal("allowed detail", r.Code)
			}
			r = request("POST", a.path, a.body, login.Token, login.CSRFToken, team)
			if r.Code != 403 {
				t.Fatal("missing create", r.Code, r.Body.String())
			}
			r = request("DELETE", a.path+"/"+a.ids[0], "", login.Token, login.CSRFToken, team)
			if r.Code != 403 {
				t.Fatal("missing delete", r.Code, r.Body.String())
			}
			var n int
			if e = db.QueryRow("SELECT COUNT(*) FROM " + a.table).Scan(&n); e != nil || n != 3 {
				t.Fatal("denied mutation wrote", n, e)
			}
			if _, e = db.Exec(`UPDATE selfupdate_control SET task_id='db-migration-http' WHERE id=1`); e != nil {
				t.Fatal(e)
			}
			r = request("POST", a.path, a.body, admin.Token, admin.CSRFToken, team)
			if r.Code != 409 {
				t.Fatal("maintenance", r.Code, r.Body.String())
			}
			if _, e = db.Exec(`UPDATE selfupdate_control SET task_id='' WHERE id=1`); e != nil {
				t.Fatal(e)
			}
		})
	}
	// The same employee gains mutations only on its selected IDs. Verify each
	// callable mutation and its database result through the real router.
	for _, a := range assets {
		grants = append(grants, authz.Grant{Action: a.kind + ".delete", Scope: authz.Scope{Kind: authz.ScopeTeam}})
		if a.kind != "template" {
			grants = append(grants, authz.Grant{Action: a.kind + ".edit", Scope: authz.Scope{Kind: authz.ScopeTeam}})
		}
	}
	if _, e = repo.SaveRole(ctx, admin.Actor, team, role.ID, tenancy.RoleInput{Name: "Reader", ExpectedRevision: role.Revision, Pages: []string{"page.library"}, RequiresSelectedBinding: true, Grants: grants}); e != nil {
		t.Fatal(e)
	}
	for _, a := range assets {
		t.Run(a.kind+"-scoped-mutations", func(t *testing.T) {
			body := strings.Replace(a.body, "Denied", "Edited", 1)
			if a.kind != "template" {
				t.Run("edit-selected", func(t *testing.T) {
					w := request("PUT", a.path+"/"+a.ids[0], body, login.Token, login.CSRFToken, team)
					if w.Code != 200 || !strings.Contains(w.Body.String(), "Edited") {
						t.Fatalf("selected PUT returned %d", w.Code)
					}
					var name string
					if err := db.QueryRow("SELECT name FROM "+a.table+" WHERE id=?", a.ids[0]).Scan(&name); err != nil || name != "Edited" {
						t.Fatalf("selected edit was not persisted: %v", err)
					}
				})
			}
			for _, target := range []struct{ name, id string }{{"same-team", a.ids[1]}, {"other-team", a.ids[2]}, {"unknown", "unlisted"}} {
				if a.kind != "template" {
					t.Run("edit-"+target.name, func(t *testing.T) {
						if w := request("PUT", a.path+"/"+target.id, body, login.Token, login.CSRFToken, team); w.Code != 403 {
							t.Fatalf("unscoped PUT returned %d", w.Code)
						}
					})
				}
				t.Run("delete-"+target.name, func(t *testing.T) {
					if w := request("DELETE", a.path+"/"+target.id, "", login.Token, login.CSRFToken, team); w.Code != 403 {
						t.Fatalf("unscoped DELETE returned %d", w.Code)
					}
				})
			}
			t.Run("delete-selected", func(t *testing.T) {
				if w := request("DELETE", a.path+"/"+a.ids[0], "", login.Token, login.CSRFToken, team); w.Code != 204 {
					t.Fatalf("selected DELETE returned %d", w.Code)
				}
				var count int
				if err := db.QueryRow("SELECT COUNT(*) FROM "+a.table+" WHERE id=?", a.ids[0]).Scan(&count); err != nil || count != 0 {
					t.Fatalf("selected row did not delete: count=%d error=%v", count, err)
				}
			})
			t.Run("unscoped-rows-unchanged", func(t *testing.T) {
				for i, name := range []string{"Outside", "Foreign"} {
					var actual string
					if err := db.QueryRow("SELECT name FROM "+a.table+" WHERE id=?", a.ids[i+1]).Scan(&actual); err != nil || actual != name {
						t.Fatalf("unscoped row changed: expected=%s error=%v", name, err)
					}
				}
			})
		})
	}
	if e = accounts.Logout(ctx, login.Token); e != nil {
		t.Fatal(e)
	}
	if r := request("GET", assets[0].path, "", login.Token, login.CSRFToken, team); r.Code != 401 {
		t.Fatal("logout HTTP", r.Code)
	}
}
func TestLibraryPermissionErrorsAreStable(t *testing.T) {
	for _, write := range []func(http.ResponseWriter, error){writeTemplateError, writeVarGroupError, writeCustomNodeError} {
		for _, tc := range []struct {
			err  error
			code int
			key  string
		}{{tenancy.ErrForbidden, 403, "access_denied"}, {tenancy.ErrConflict, 409, "permission_state_changed"}, {configstate.ErrMaintenance, 409, "maintenance_active"}} {
			w := httptest.NewRecorder()
			write(w, errors.Join(tc.err, errors.New("secret-test-value")))
			var body map[string]any
			if e := json.Unmarshal(w.Body.Bytes(), &body); e != nil {
				t.Fatal(e)
			}
			if w.Code != tc.code || !strings.Contains(w.Body.String(), tc.key) || strings.Contains(w.Body.String(), "secret-test-value") {
				t.Fatal(w.Code, w.Body.String())
			}
		}
	}
}

func TestTenantPipelineApplyTemplateChecksBothResources(t *testing.T) {
	f := newTenantAuxFixture(t)
	ctx := context.Background()
	accounts := tenancy.NewAccounts(f.db)
	admin, err := accounts.Login(ctx, "admin", "auxiliary-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	employee, err := accounts.Login(ctx, "auxmember", "auxiliary-member-password")
	if err != nil {
		t.Fatal(err)
	}
	var team, foreignTeam, roleID string
	var revision uint64
	if err = f.db.QueryRow("SELECT default_team_id FROM access_control WHERE id=1").Scan(&team); err != nil {
		t.Fatal(err)
	}
	if err = f.db.QueryRow("SELECT tenant_id FROM access_ownership WHERE resource_kind='project' AND resource_id='foreign'").Scan(&foreignTeam); err != nil {
		t.Fatal(err)
	}
	if err = f.db.QueryRow("SELECT id,revision FROM access_roles WHERE tenant_id=? AND name='Auxiliary'", team).Scan(&roleID, &revision); err != nil {
		t.Fatal(err)
	}
	tpl := library.NewTemplateService(f.db, pipeline.New(f.db))
	makeTpl := func(team, name string) *library.Template {
		t.Helper()
		out, e := tpl.Create(tenancy.WithActor(ctx, admin.Actor, team), library.TemplateInput{
			Name: name, Spec: pipeline.Spec{Stages: []pipeline.Stage{{ID: "source", Name: name, Kind: pipeline.KindSource, Jobs: []pipeline.Job{{ID: "git", Name: "Git", Type: "git_source"}}}}},
		})
		if e != nil {
			t.Fatal(e)
		}
		return out
	}
	allowed := makeTpl(team, "Allowed pipeline template")
	sibling := makeTpl(team, "Sibling pipeline template")
	foreign := makeTpl(foreignTeam, "Foreign pipeline template")
	pScope := authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{"p"}}
	save := func(edit bool) {
		t.Helper()
		grants := []authz.Grant{{Action: "pipeline.read", Scope: pScope}, {Action: "template.read", Scope: authz.Scope{Kind: authz.ScopeSelected, ResourceRefs: []authz.ResourceRef{{Kind: "template", ID: allowed.ID}}}}}
		if edit {
			grants = append(grants, authz.Grant{Action: "pipeline.edit", Scope: pScope})
		}
		row, e := tenancy.NewRepository(f.db).SaveRole(ctx, admin.Actor, team, roleID, tenancy.RoleInput{
			Name: "Auxiliary", Pages: []string{"page.pipeline", "page.library"}, ExpectedRevision: revision, Grants: grants,
		})
		if e != nil {
			t.Fatal(e)
		}
		revision = row.Revision
	}
	router := New(testWebFSAuth(), tenancy.NewAuthenticator(accounts, employee.Actor.ModeEpoch), WithTenancyAccounts(accounts), WithTemplates(tpl))
	request := func(project, template string) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(map[string]string{"templateId": template})
		req := httptest.NewRequest("POST", "/api/projects/"+project+"/pipeline/apply-template", strings.NewReader(string(raw)))
		req.Header.Set("X-Pipewright-Team", team)
		req.Header.Set(headerCsrf, employee.CSRFToken)
		req.AddCookie(&http.Cookie{Name: cookieSession, Value: employee.Token})
		req.AddCookie(&http.Cookie{Name: cookieCsrf, Value: employee.CSRFToken})
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	save(false)
	if w := request("p", allowed.ID); w.Code != 403 {
		t.Fatalf("reader applied template: %d %s", w.Code, w.Body.String())
	}
	save(true)
	for _, bad := range []string{sibling.ID, foreign.ID, "missing"} {
		if w := request("p", bad); (w.Code != 403 && w.Code != 404) || strings.Contains(w.Body.String(), bad) {
			t.Fatalf("unselected template visible: %d %s", w.Code, w.Body.String())
		}
	}
	for _, pid := range []string{"outside", "foreign", "missing"} {
		if w := request(pid, allowed.ID); w.Code != 403 {
			t.Fatalf("unselected project editable: %d %s", w.Code, w.Body.String())
		}
	}
	var rows int
	if e := f.db.QueryRow("SELECT COUNT(*) FROM pipeline_configs").Scan(&rows); e != nil || rows != 0 {
		t.Fatal("denials changed config", e, rows)
	}
	if w := request("p", allowed.ID); w.Code != 200 || !strings.Contains(w.Body.String(), "Allowed pipeline template") {
		t.Fatalf("selected apply: %d %s", w.Code, w.Body.String())
	}
	var config string
	if e := f.db.QueryRow("SELECT spec_json FROM pipeline_configs WHERE project_id='p'").Scan(&config); e != nil || !strings.Contains(config, "Allowed pipeline template") {
		t.Fatal("selected apply not persisted", e)
	}
	save(false)
	if w := request("p", allowed.ID); w.Code != 403 {
		t.Fatalf("revoked role applied template: %d %s", w.Code, w.Body.String())
	}
	var final string
	if e := f.db.QueryRow("SELECT spec_json FROM pipeline_configs WHERE project_id='p'").Scan(&final); e != nil || final != config {
		t.Fatal("revoke changed current config", e)
	}
}
