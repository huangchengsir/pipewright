package httpapi

import (
	"context"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/configdraft"
	"github.com/huangchengsir/pipewright/internal/configstate"
	pmcp "github.com/huangchengsir/pipewright/internal/mcp"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMCPOwnerScopeThroughSDKAndSQLPagination(t *testing.T) {
	ctx := context.Background()
	f := platformSetup(t)
	db := f.store.DB
	repo := tenancy.NewRepository(db)
	_, err := repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true)
	if err != nil {
		t.Fatal(err)
	}
	accounts := tenancy.NewAccounts(db)
	admin, err := accounts.Login(ctx, "admin", "testpass")
	if err != nil {
		t.Fatal(err)
	}
	team, err := repo.CreateTeam(ctx, "MCP SDK")
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{"00000000-0000-0000-0000-000000000001", "00000000-0000-0000-0000-000000000002", "00000000-0000-0000-0000-000000000003"}
	if _, err = db.Exec(`INSERT INTO credentials(id,name,type,scope,ciphertext,masked_value,created_at,updated_at) VALUES('c','C','git_token','',X'00','m','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if _, err = db.Exec(`INSERT INTO projects(id,name,repo_url,default_branch,credential_id,created_at,updated_at) VALUES(?,?,'https://example.invalid','main','c','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, id, id); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(`INSERT INTO access_ownership(resource_kind,resource_id,tenant_id,project_id) VALUES('project',?,?,?)`, id, team, id); err != nil {
			t.Fatal(err)
		}
	}
	input := tenancy.RoleInput{Name: "SDK reader", Pages: []string{"page.projects", "page.mcp"}, Grants: []authz.Grant{{Action: "project.read", Scope: authz.Scope{Kind: authz.ScopeTeam}}, {Action: "mcp.issue", Scope: authz.Scope{Kind: authz.ScopeTeam}}}}
	role, err := repo.SaveRole(ctx, admin.Actor, team, "", input)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: "sdk-user", DisplayName: "SDK User", Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
	if err != nil {
		t.Fatal(err)
	}
	login, err := accounts.Login(ctx, "sdk-user", initial.Password)
	if err != nil {
		t.Fatal(err)
	}
	login, err = accounts.ChangePassword(ctx, login.Token, initial.Password, "sdk-user-password")
	if err != nil {
		t.Fatal(err)
	}
	userctx := tenancy.WithActor(ctx, login.Actor, team)
	tokens := auth.NewMCPTokenStore(db, tenancy.MCPAuthorization(db))
	raw, token, err := tokens.Create(userctx, auth.MCPTokenInput{Name: "sdk", ProjectIDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewMCPHandler(tokens, pmcp.Dependencies{Projects: project.New(db, nil, nil)}, MCPHTTPOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sdkFixture := &mcpFixture{handler: handler}
	input.ExpectedRevision = role.Revision
	for i := range input.Grants {
		input.Grants[i].Scope = authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: ids[1:]}
	}
	if _, err = repo.SaveRole(ctx, admin.Actor, team, role.ID, input); err != nil {
		t.Fatal(err)
	}
	for page := 1; page <= 2; page++ {
		out := mcpOutput(t, sdkFixture.call(t, raw, "projects_list", map[string]any{"page": page, "pageSize": 1}))
		items := out["projects"].([]any)
		if len(items) != 1 || items[0].(map[string]any)["id"] != ids[page] || out["hasMore"] != (page == 1) {
			t.Fatalf("page %d: %v", page, out)
		}
	}
	// A persisted token remains a distinct principal, not a detached HTTP cookie.
	if _, err = db.Exec(`DELETE FROM access_sessions WHERE user_id=?`, initial.User.ID); err != nil {
		t.Fatal(err)
	}
	out := mcpOutput(t, sdkFixture.call(t, raw, "projects_list", map[string]any{}))
	if len(out["projects"].([]any)) != 2 {
		t.Fatal(out)
	}
	if _, err = db.Exec(`UPDATE mcp_tokens SET revoked_at='2026-01-01T00:00:00Z' WHERE id=?`, token.ID); err != nil {
		t.Fatal(err)
	}
	if w := sdkFixture.rpc(raw, "tools/call", map[string]any{"name": "projects_list", "arguments": map[string]any{}}); w.Code != 401 {
		t.Fatal("revoked HTTP token", w.Code)
	}
}

func TestMCPConfigurationIndependentApproverRevalidated(t *testing.T) {
	ctx := context.Background()
	f, svc, _, _, _ := configHTTPFixture(t)
	db := f.db
	hash, err := auth.HashPassword("config-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE admin_user SET username='admin', password_hash=? WHERE id=1`, hash); err != nil {
		t.Fatal(err)
	}
	repo := tenancy.NewRepository(db)
	mode, err := repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true)
	if err != nil {
		t.Fatal(err)
	}
	var team string
	if err = db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); err != nil {
		t.Fatal(err)
	}
	accounts := tenancy.NewAccounts(db)
	admin, err := accounts.Login(ctx, "admin", "config-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	makeRole := func(name string, actions ...string) tenancy.RoleView {
		t.Helper()
		pages := []string{}
		seen := map[string]bool{}
		grants := []authz.Grant{}
		for _, id := range actions {
			a, _ := authz.Lookup(id)
			if !seen[a.Page] {
				pages = append(pages, a.Page)
				seen[a.Page] = true
			}
			grants = append(grants, authz.Grant{Action: id, Scope: authz.Scope{Kind: authz.ScopeTeam}})
		}
		r, e := repo.SaveRole(ctx, admin.Actor, team, "", tenancy.RoleInput{Name: name, Pages: pages, Grants: grants})
		if e != nil {
			t.Fatal(e)
		}
		return r
	}
	editorRole := makeRole("Editor", "project.read", "project.edit", "pipeline.read", "pipeline.edit", "mcp.issue")
	reviewerRole := makeRole("Reviewer", "project.read", "project.edit", "pipeline.read", "mcp.approve")
	makeUser := func(name string, role tenancy.RoleView) tenancy.Session {
		t.Helper()
		initial, e := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: name, DisplayName: name, Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
		if e != nil {
			t.Fatal(e)
		}
		login, e := accounts.Login(ctx, name, initial.Password)
		if e != nil {
			t.Fatal(e)
		}
		login, e = accounts.ChangePassword(ctx, login.Token, initial.Password, name+"-permanent-password")
		if e != nil {
			t.Fatal(e)
		}
		return login
	}
	editor := makeUser("config-editor", editorRole)
	reviewer := makeUser("config-reviewer", reviewerRole)
	f.tokens = auth.NewMCPTokenStore(db, tenancy.MCPAuthorization(db))
	raw, _, err := f.tokens.Create(tenancy.WithActor(ctx, editor.Actor, team), auth.MCPTokenInput{Name: "change", ProjectIDs: []string{f.project}, ConfigGrants: []auth.MCPConfigGrant{{ProjectID: f.project, Read: true, Propose: true, Apply: true, Sections: []string{"project"}, Operations: []string{"project.set_name"}}}})
	if err != nil {
		t.Fatal(err)
	}
	f.handler, err = NewMCPHandler(f.tokens, pmcp.Dependencies{Configs: svc}, MCPHTTPOptions{})
	if err != nil {
		t.Fatal(err)
	}
	browser := New(testWebFSAuth(), tenancy.NewAuthenticator(accounts, mode.Epoch), WithTenancyAccounts(accounts), WithConfigManagement(db, svc, true))
	request := func(user tenancy.Session, path, method, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Pipewright-Team", team)
		r.Header.Set(headerCsrf, user.CSRFToken)
		r.AddCookie(&http.Cookie{Name: cookieSession, Value: user.Token})
		w := httptest.NewRecorder()
		browser.ServeHTTP(w, r)
		return w
	}
	snapshot := mcpOutput(t, f.call(t, raw, "project_config_get", map[string]any{"projectId": f.project, "section": "project", "beginSnapshot": true}))
	draft := mcpOutput(t, f.call(t, raw, "config_draft_create", map[string]any{"projectId": f.project, "baseRevision": snapshot["baseRevision"], "policyVersion": snapshot["policyVersion"], "requestId": "tenant-config-draft-1", "operations": []any{map[string]any{"type": "project.set_name", "value": "Approved new name"}}}))
	t.Run("maintenance-and-fencing", func(t *testing.T) {
		const operation = "tenant-review-operation"
		const activity = "tenant-review-legacy"
		if _, e := db.Exec(`INSERT INTO mcp_config_operations(id,token_id,project_id,request_id,input_digest,draft_id,approval_id,owner_epoch,state,reason,before_revision,result_json,created_at,updated_at)
			VALUES (?,?,?,?,?,?,?,'old-owner','pending','',1,'{}',0,0)`, operation, "fixture-token", f.project, "tenant-review-request", "fixture-fingerprint", draft["draftId"], "fixture-approval"); e != nil {
			t.Fatal(e)
		}
		if e := configstate.WithWriteTx(ctx, db, func(q configstate.DBTX) error {
			_, e := configstate.AcquireActivityTx(ctx, q, db, configstate.Activity{ID: activity, ProjectID: f.project, OwnerEpoch: "legacy-unreconciled", Kind: "run", Phase: "cleanup_unknown"})
			return e
		}); e != nil {
			t.Fatal(e)
		}
		const maintenance = "/api/mcp/config-maintenance"
		const operationPath = "/api/mcp/config-operations/" + operation
		w := request(admin, maintenance, "GET", "")
		var state configstate.MaintenanceSnapshot
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &state) != nil || len(state.Items) != 1 || state.Items[0].ActivityID != activity || state.Items[0].Recoverable {
			t.Fatalf("administrator should see the exact unrecoverable activity: %d %s", w.Code, w.Body.String())
		}
		recoverBody, _ := json.Marshal(configstate.MaintenanceRecoverRequest{ActivityID: activity, ExpectedDigest: state.Items[0].Digest, EvidenceID: "synthetic-unverified"})
		for _, tc := range []struct{ name, path, method, body string }{
			{"maintenance-read", maintenance, "GET", ""},
			{"maintenance-recover", maintenance + "/recover", "POST", string(recoverBody)},
			{"operation-read", operationPath, "GET", ""},
			{"operation-fence", operationPath + "/fence", "POST", "{}"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				if w := request(editor, tc.path, tc.method, tc.body); w.Code != 403 || strings.Contains(w.Body.String(), operation) || strings.Contains(w.Body.String(), activity) {
					t.Fatalf("unauthorized operation details or status: %d %s", w.Code, w.Body.String())
				}
			})
		}
		var revision, active int
		var opState string
		if e := db.QueryRow("SELECT state FROM mcp_config_operations WHERE id=?", operation).Scan(&opState); e != nil || opState != "pending" {
			t.Fatal("unprivileged user changed pending operation", e, opState)
		}
		w = request(reviewer, operationPath, "GET", "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"pending"`) {
			t.Fatalf("reviewer must see selected pending operation: %d %s", w.Code, w.Body.String())
		}
		w = request(reviewer, operationPath+"/fence", "POST", "{}")
		if w.Code != 200 || !strings.Contains(w.Body.String(), "interrupted_not_applied") {
			t.Fatalf("reviewer must fence selected operation: %d %s", w.Code, w.Body.String())
		}
		w = request(admin, maintenance+"/recover", "POST", string(recoverBody))
		if w.Code != 409 || !strings.Contains(w.Body.String(), "evidence_unavailable") {
			t.Fatalf("unverified recovery must not proceed: %d %s", w.Code, w.Body.String())
		}
		if e := db.QueryRow("SELECT COUNT(*) FROM project_activity WHERE activity_id=? AND released_at IS NULL", activity).Scan(&active); e != nil || active != 1 {
			t.Fatal("unverified recovery released activity", e, active)
		}
		if e := db.QueryRow("SELECT revision FROM project_config_versions WHERE project_id=?", f.project).Scan(&revision); e != nil || revision != 1 {
			t.Fatal("manual fence changed project settings", e, revision)
		}
		// Clear this synthetic activity before testing the independent apply path.
		if _, e := db.Exec("DELETE FROM project_activity WHERE activity_id=?", activity); e != nil {
			t.Fatal(e)
		}
	})

	path := "/api/mcp/config-drafts/" + draft["draftId"].(string)
	body, _ := json.Marshal(map[string]any{"baseRevision": draft["baseRevision"], "policyVersion": draft["policyVersion"], "reviewDigest": draft["reviewDigest"]})
	if w := request(editor, path+"/approve", "POST", string(body)); w.Code != 403 {
		t.Fatal("editor self-approved without action", w.Code, w.Body.String())
	}
	w := request(reviewer, path+"/approve", "POST", string(body))
	if w.Code != 200 {
		t.Fatal("authorized reviewer", w.Code, w.Body.String())
	}
	var approval configdraft.Approval
	if err = json.Unmarshal(w.Body.Bytes(), &approval); err != nil || approval.ID == "" {
		t.Fatal(err, w.Body.String())
	}
	// Revocation after a valid approval must be enforced at application time.
	reduced := tenancy.RoleInput{Name: reviewerRole.NameKey, Pages: reviewerRole.Pages, Grants: reviewerRole.Grants[:len(reviewerRole.Grants)-1], ExpectedRevision: reviewerRole.Revision}
	if _, err = repo.SaveRole(ctx, admin.Actor, team, reviewerRole.ID, reduced); err != nil {
		t.Fatal(err)
	}
	result := f.call(t, raw, "config_apply", map[string]any{"draftId": draft["draftId"], "approvalId": approval.ID, "requestId": "tenant-config-apply-1"})
	outcome := mcpOutput(t, result)
	if outcome["state"] != "rejected" || outcome["reason"] != "permission_denied" {
		t.Fatal("wrong permission rejection receipt", outcome)
	}
	var name string
	if err = db.QueryRow(`SELECT name FROM projects WHERE id=?`, f.project).Scan(&name); err != nil || name == "Approved new name" {
		t.Fatal("rejected apply mutated project", name, err)
	}
	restored := tenancy.RoleInput{Name: reviewerRole.NameKey, Pages: reviewerRole.Pages, Grants: reviewerRole.Grants, ExpectedRevision: reviewerRole.Revision + 1}
	if _, err = repo.SaveRole(ctx, admin.Actor, team, reviewerRole.ID, restored); err != nil {
		t.Fatal(err)
	}
	w = request(reviewer, path+"/approve", "POST", string(body))
	if w.Code != 200 {
		t.Fatal("reapproval after explicit restoration", w.Code, w.Body.String())
	}
	if err = json.Unmarshal(w.Body.Bytes(), &approval); err != nil {
		t.Fatal(err)
	}
	outcome = mcpOutput(t, f.call(t, raw, "config_apply", map[string]any{"draftId": draft["draftId"], "approvalId": approval.ID, "requestId": "tenant-config-apply-2"}))
	if outcome["state"] != "applied" {
		t.Fatal("authorized owner and approver could not apply", outcome)
	}
	if err = db.QueryRow(`SELECT name FROM projects WHERE id=?`, f.project).Scan(&name); err != nil || name != "Approved new name" {
		t.Fatal("approved configuration missing", name, err)
	}

}
