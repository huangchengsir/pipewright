package main

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"testing"
)

func TestMCPRuntimeAssemblyIssuesOwnedEmployeeToken(t *testing.T) {
	ctx := context.Background()
	db := storetest.OpenDB(t)
	legacy := auth.NewService(db, nil)
	if err := legacy.Bootstrap("admin", "testpass"); err != nil {
		t.Fatal(err)
	}
	access, err := assembleAccess(ctx, db, legacy, config.MultiTenantMode{Enabled: true, Explicit: true}, true)
	if err != nil {
		t.Fatal(err)
	}
	repo, accounts := access.repository, access.accounts
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
	tokens := assembleMCPTokens(db)
	raw, token, err := tokens.Create(userctx, auth.MCPTokenInput{Name: "sdk", ProjectIDs: ids})
	if err != nil {
		t.Fatal(err)
	}

	if token.OwnerUserID != initial.User.ID || token.OwnerTeamID != team {
		t.Fatal("missing owner binding")
	}
	if _, err := tokens.Verify(ctx, raw); err != nil {
		t.Fatal("verify issued token", err)
	}
	if err := tokens.Revoke(userctx, token.ID); err != nil {
		t.Fatal("revoke", err)
	}
	if _, err := tokens.Verify(ctx, raw); err == nil {
		t.Fatal("revoked token accepted")
	}
}

func TestMCPRuntimeAssemblyRetainsDisabledModeLifecycle(t *testing.T) {
	ctx := context.Background()
	db := storetest.OpenDB(t)
	legacy := auth.NewService(db, nil)
	if err := legacy.Bootstrap("admin", "testpass"); err != nil {
		t.Fatal(err)
	}
	if _, err := assembleAccess(ctx, db, legacy, config.MultiTenantMode{}, false); err != nil {
		t.Fatal(err)
	}
	const projectID = "00000000-0000-0000-0000-000000000009"
	if _, err := db.Exec(`INSERT INTO credentials(id,name,type,scope,ciphertext,masked_value,created_at,updated_at) VALUES('legacy-c','C','git_token','',X'00','m','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO projects(id,name,repo_url,default_branch,credential_id,created_at,updated_at) VALUES(?,'legacy','https://example.invalid','main','legacy-c','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, projectID); err != nil {
		t.Fatal(err)
	}
	tokens := assembleMCPTokens(db)
	raw, token, err := tokens.Create(ctx, auth.MCPTokenInput{Name: "legacy", ProjectIDs: []string{projectID}})
	if err != nil {
		t.Fatal(err)
	}
	if token.OwnerUserID != "" || token.OwnerTeamID != "" {
		t.Fatal("disabled mode unexpectedly assigns employee ownership")
	}
	verified, err := tokens.Verify(ctx, raw)
	if err != nil || !verified.Allows(projectID, false) || verified.Allows(projectID, true) {
		t.Fatal("legacy read-only token lost its scope", err)
	}
	if err := tokens.Revoke(ctx, token.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := tokens.Verify(ctx, raw); err == nil {
		t.Fatal("revoked legacy token accepted")
	}
}
