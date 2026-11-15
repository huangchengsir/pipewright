package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/kubernetes"
	"github.com/huangchengsir/pipewright/internal/tenancy"
)

func TestTenantKubernetesConfigurationHTTPMatrix(t *testing.T) {
	f := newK8sHTTP(t)
	ctx := context.Background()
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	exec := func(query string, args ...any) { t.Helper(); _, e := f.db.DB.Exec(query, args...); must(e) }
	connIn := kubernetes.ConnectionInput{Name: "sample", CredentialID: f.cred.ID, Enabled: true, Profile: f.conn.Profile, Executors: []kubernetes.Executor{{Kind: "local"}}}
	sibling, e := f.s.SaveConnection(ctx, "", 0, connIn)
	must(e)
	foreign, e := f.s.SaveConnection(ctx, "", 0, connIn)
	must(e)
	siblingCred, e := f.s.SaveCredential(ctx, "", 0, f.input)
	must(e)
	foreignCred, e := f.s.SaveCredential(ctx, "", 0, f.input)
	must(e)
	exec(`INSERT INTO projects(id,name,repo_url,credential_id,created_at,updated_at) SELECT 'project-sibling',name,repo_url,credential_id,created_at,updated_at FROM projects WHERE id='project-1'`)
	siblingGrantIn := kubernetes.GrantInput{ProjectID: "project-sibling", Environment: "test", Namespace: "app", Deployment: "backend", Containers: []string{"app"}, Executor: kubernetes.Executor{Kind: "local"}, Enabled: true}
	siblingGrant, e := f.s.SaveGrant(ctx, f.conn.ID, "", 0, siblingGrantIn)
	must(e)
	base := f.conn.Profile
	base.Protocol = kubernetes.ReleaseProfileProtocol
	profile := kubernetes.ReleaseToolProfile{Runtime: base, HelperPath: "/usr/bin/pipewright-kube-tool", HelmPath: "/usr/bin/helm", HelmVersion: kubernetes.ReleaseHelmVersion}
	for _, id := range []string{f.conn.ID, sibling.ID, foreign.ID} {
		_, e = f.s.SaveReleaseProfile(ctx, id, 0, profile, true)
		must(e)
	}
	releaseIn := kubernetes.ReleaseGrantInput{ProjectID: "project-1", Environment: "test", Executor: kubernetes.Executor{Kind: "local"}, Enabled: true, Resources: []kubernetes.ReleaseAccess{{ID: kubernetes.ReleaseResourceID{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment", Namespace: "app", Name: "backend"}, Actions: []string{"get"}}}}
	release1, e := f.s.SaveReleaseGrant(ctx, f.conn.ID, "", 0, releaseIn)
	must(e)
	releaseSiblingIn := releaseIn
	releaseSiblingIn.ProjectID = "project-sibling"
	release2, e := f.s.SaveReleaseGrant(ctx, f.conn.ID, "", 0, releaseSiblingIn)
	must(e)
	adminHash, e := auth.HashPassword("kubernetes-http-admin-password")
	must(e)
	exec(`UPDATE admin_user SET password_hash=? WHERE id=1`, adminHash)
	repo := tenancy.NewRepository(f.db.DB)
	mode, e := repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true)
	must(e)
	accounts := tenancy.NewAccounts(f.db.DB)
	admin, e := accounts.Login(ctx, "admin", "kubernetes-http-admin-password")
	must(e)
	var team string
	must(f.db.DB.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team))
	other, e := repo.CreateTeamAs(ctx, admin.Actor, "other cluster team")
	must(e)
	for _, row := range []struct{ kind, id string }{{"kubernetes", foreign.ID}, {"kubernetes_credential", foreignCred.ID}} {
		exec(`UPDATE access_ownership SET tenant_id=? WHERE resource_kind=? AND resource_id=?`, other.ID, row.kind, row.id)
	}
	for _, project := range []string{"project-1", "project-sibling"} {
		exec(`INSERT INTO access_environments(id,tenant_id,project_id,name,purpose,revision) VALUES(?,?,?,'test','test',1)`, "env-"+project, team, project)
	}
	grants := []authz.Grant{}
	for _, action := range []string{"project.read", "kubernetes.read", "kubernetes_credential.read"} {
		grants = append(grants, authz.Grant{Action: action, Scope: authz.Scope{Kind: authz.ScopeTeam}})
	}
	role, e := repo.SaveRole(ctx, admin.Actor, team, "", tenancy.RoleInput{Name: "selected cluster reader", Pages: []string{"page.projects", "page.kubernetes"}, RequiresSelectedBinding: true, Grants: grants})
	must(e)
	initial, e := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: "k8sviewer", DisplayName: "Cluster viewer", Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeSelected, ResourceRefs: []authz.ResourceRef{{Kind: "kubernetes", ID: f.conn.ID}, {Kind: "kubernetes_credential", ID: f.cred.ID}, {Kind: "project", ID: "project-1"}}}}}})
	must(e)
	viewer, e := accounts.Login(ctx, "k8sviewer", initial.Password)
	must(e)
	viewer, e = accounts.ChangePassword(ctx, viewer.Token, initial.Password, "kubernetes-http-viewer-password")
	must(e)
	router := New(testWebFSAuth(), tenancy.NewAuthenticator(accounts, mode.Epoch), WithTenancyAccounts(accounts), WithKubernetes(f.s, f.observer))
	req := func(session tenancy.Session, method, path string, in any) *httptest.ResponseRecorder {
		b, e := json.Marshal(in)
		must(e)
		r := httptest.NewRequest(method, path, bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Pipewright-Team", team)
		r.Header.Set(headerCsrf, session.CSRFToken)
		r.AddCookie(&http.Cookie{Name: cookieSession, Value: session.Token})
		r.AddCookie(&http.Cookie{Name: cookieCsrf, Value: session.CSRFToken})
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	assert := func(t *testing.T, w *httptest.ResponseRecorder, status int) {
		t.Helper()
		if w.Code != status {
			t.Fatalf("want %d, got %d: %s", status, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), f.input.Token) || strings.Contains(w.Body.String(), "certificate-authority-data") {
			t.Fatal("cluster credential was returned")
		}
	}
	cpath := "/api/kubernetes/connections"
	kpath := "/api/kubernetes/credentials"
	selected := cpath + "/" + f.conn.ID
	reads := []struct{ name, path, field, id string }{
		{"connections", cpath, "id", f.conn.ID},
		{"connection", selected, "id", f.conn.ID},
		{"credentials", kpath, "id", f.cred.ID},
		{"credential", kpath + "/" + f.cred.ID, "id", f.cred.ID},
		{"legacy-grants", selected + "/grants", "id", f.grant.ID},
		{"release-grants", selected + "/release-grants", "id", release1.ID},
		{"release-profile", selected + "/release-profile", "connectionId", f.conn.ID},
	}
	for _, tc := range reads {
		t.Run(tc.name, func(t *testing.T) {
			w := req(viewer, "GET", tc.path, nil)
			assert(t, w, 200)
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("private settings response is cacheable")
			}
			var rows []map[string]any
			if strings.HasPrefix(strings.TrimSpace(w.Body.String()), "[") {
				must(json.Unmarshal(w.Body.Bytes(), &rows))
			} else {
				var row map[string]any
				must(json.Unmarshal(w.Body.Bytes(), &row))
				rows = []map[string]any{row}
			}
			if len(rows) != 1 || rows[0][tc.field] != tc.id {
				t.Fatalf("wrong %s selection: %s", tc.name, w.Body.String())
			}
			if strings.Contains(w.Body.String(), siblingGrant.ID) || strings.Contains(w.Body.String(), release2.ID) {
				t.Fatal("sibling project's grant returned")
			}
			if tc.name == "connection" || strings.HasPrefix(tc.name, "release-") || tc.name == "legacy-grants" {
				for _, id := range []string{sibling.ID, foreign.ID, "unknown"} {
					denied := req(viewer, "GET", strings.Replace(tc.path, f.conn.ID, id, 1), nil)
					assert(t, denied, 403)
					if strings.Contains(denied.Body.String(), id) {
						t.Fatal("unselected resource disclosed")
					}
				}
			}
			if tc.name == "credential" {
				for _, id := range []string{siblingCred.ID, foreignCred.ID, "unknown"} {
					assert(t, req(viewer, "GET", kpath+"/"+id, nil), 403)
				}
			}
		})
	}
	t.Run("read-only-grants-and-runtime", func(t *testing.T) {
		legacyIn := siblingGrantIn
		legacyIn.ProjectID = "project-1"
		revision := map[string]any{"expectedRevision": 1}
		policy := map[string]any{"projectId": "project-1", "environment": "test", "executor": kubernetes.Executor{Kind: "local"}}
		for _, tc := range []struct {
			name, method, path string
			in                 any
			status             int
		}{
			{"create-connection", "POST", cpath, connIn, 403},
			{"edit-connection", "PATCH", selected, k8sConnectionWrite{k8sRevision: k8sRevision{ExpectedRevision: 1}, ConnectionInput: connIn}, 403},
			{"delete-connection", "DELETE", selected, revision, 403},
			{"edit-credential", "PATCH", kpath + "/" + f.cred.ID, k8sCredentialWrite{k8sRevision: k8sRevision{ExpectedRevision: 1}, CredentialInput: f.input}, 403},
			{"delete-credential", "DELETE", kpath + "/" + f.cred.ID, revision, 403},
			{"create-grant", "POST", selected + "/grants", legacyIn, 403},
			{"edit-grant", "PATCH", selected + "/grants/" + f.grant.ID, k8sGrantWrite{k8sRevision: k8sRevision{ExpectedRevision: 1}, GrantInput: legacyIn}, 403},
			{"edit-profile", "PUT", selected + "/release-profile", k8sReleaseProfileWrite{ExpectedRevision: 1, Enabled: false, Profile: profile}, 403},
			{"create-release-grant", "POST", selected + "/release-grants", releaseIn, 403},
			{"edit-release-grant", "PATCH", selected + "/release-grants/" + release1.ID, k8sReleaseGrantWrite{ExpectedRevision: 1, ReleaseGrantInput: releaseIn}, 403},
			{"delete-release-grant", "DELETE", selected + "/release-grants/" + release1.ID, revision, 403},
			{"release-options", "POST", selected + "/release-options", policy, 403},
			{"release-capabilities", "POST", selected + "/release-capabilities", policy, 403},
			{"probe", "POST", selected + "/probe", map[string]any{"grantId": f.grant.ID, "container": "app"}, 403},
		} {
			t.Run(tc.name, func(t *testing.T) { assert(t, req(viewer, tc.method, tc.path, tc.in), tc.status) })
		}
		for _, table := range []struct {
			name  string
			count int
		}{{"k8s_connections", 3}, {"k8s_credentials", 3}, {"k8s_deploy_grants", 2}, {"k8s_release_profiles", 3}, {"k8s_release_grants", 2}} {
			var count, sum int
			must(f.db.DB.QueryRow("SELECT COUNT(*), SUM(revision) FROM "+table.name).Scan(&count, &sum))
			if count != table.count || sum != table.count {
				t.Fatalf("denial changed %s: count %d revisions %d", table.name, count, sum)
			}
		}
		if f.observer.calls != 0 {
			t.Fatal("metadata reader reached observer")
		}
	})
	t.Run("revoke-session-during-probe", func(t *testing.T) {
		payload := map[string]any{"grantId": f.grant.ID, "container": "app"}
		assert(t, req(admin, "POST", selected+"/probe", payload), 200)
		f.observer.beforeReturn = func() { must(accounts.Logout(ctx, admin.Token)) }
		assert(t, req(admin, "POST", selected+"/probe", payload), 401)
		if f.observer.calls != 2 {
			t.Fatalf("unexpected observer calls: %d", f.observer.calls)
		}
	})
	t.Run("selected-grant-editor", func(t *testing.T) {
		admin, e := accounts.Login(ctx, "admin", "kubernetes-http-admin-password")
		if e != nil {
			t.Fatal(e)
		}
		grants = append(grants, authz.Grant{Action: "kubernetes.edit", Scope: authz.Scope{Kind: authz.ScopeTeam}})
		_, e = repo.SaveRole(ctx, admin.Actor, team, role.ID, tenancy.RoleInput{
			Name: "selected cluster reader", ExpectedRevision: role.Revision, Pages: []string{"page.projects", "page.kubernetes"},
			RequiresSelectedBinding: true, Grants: grants,
		})
		must(e)
		rev := map[string]any{"expectedRevision": 1}
		allowedLegacy := siblingGrantIn
		allowedLegacy.ProjectID = "project-1"
		allowedLegacy.Deployment = "backend-edited"
		allowedRelease := releaseIn
		allowedRelease.RequireApproval = true
		t.Run("legacy-grant-update", func(t *testing.T) {
			body := k8sGrantWrite{k8sRevision: k8sRevision{ExpectedRevision: 1}, GrantInput: allowedLegacy}
			route := selected + "/grants/"
			assert(t, req(viewer, "PATCH", route+siblingGrant.ID, body), 404)
			assert(t, req(viewer, "PATCH", route+f.grant.ID, body), 200)
			current, e := f.s.GetGrant(tenancy.WithActor(ctx, viewer.Actor, team), f.grant.ID)
			must(e)
			if current.Revision != 2 || current.ProjectID != "project-1" || current.Deployment != "backend-edited" {
				t.Fatalf("authorized grant edit missing: %#v", current)
			}
			var project string
			var revision int
			must(f.db.DB.QueryRow("SELECT project_id,revision FROM k8s_deploy_grants WHERE id=?", siblingGrant.ID).Scan(&project, &revision))
			if project != "project-sibling" || revision != 1 {
				t.Fatal("out-of-scope legacy grant changed", project, revision)
			}
		})
		t.Run("release-grant-update-and-delete", func(t *testing.T) {
			route := selected + "/release-grants/"
			body := k8sReleaseGrantWrite{ExpectedRevision: 1, ReleaseGrantInput: allowedRelease}
			assert(t, req(viewer, "PATCH", route+release2.ID, body), 404)
			assert(t, req(viewer, "DELETE", route+release2.ID, rev), 404)
			assert(t, req(viewer, "PATCH", route+release1.ID, body), 200)
			var revision int
			must(f.db.DB.QueryRow("SELECT revision FROM k8s_release_grants WHERE id=?", release1.ID).Scan(&revision))
			if revision != 2 {
				t.Fatal("allowed release grant did not change")
			}
			assert(t, req(viewer, "DELETE", route+release1.ID, map[string]any{"expectedRevision": 2}), 200)
			var count int
			must(f.db.DB.QueryRow("SELECT COUNT(*) FROM k8s_release_grants WHERE id=?", release1.ID).Scan(&count))
			if count != 0 {
				t.Fatal("selected grant remained after deletion")
			}
			var project string
			must(f.db.DB.QueryRow("SELECT project_id,revision FROM k8s_release_grants WHERE id=?", release2.ID).Scan(&project, &revision))
			if project != "project-sibling" || revision != 1 {
				t.Fatal("out-of-scope release grant changed", project, revision)
			}
		})
	})
	t.Run("revoke-read-session", func(t *testing.T) {
		must(accounts.Logout(ctx, viewer.Token))
		for _, tc := range reads {
			assert(t, req(viewer, "GET", tc.path, nil), 401)
		}
	})
}
