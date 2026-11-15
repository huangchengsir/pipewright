package tenancy

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"strings"
	"testing"
)

func TestMCPTokenOwnerScopeRevocationAndIsolation(t *testing.T) {
	accounts, r, admin := accountFixture(t)
	ctx := context.Background()
	team, err := r.CreateTeam(ctx, "MCP team")
	if err != nil {
		t.Fatal(err)
	}
	other, err := r.CreateTeam(ctx, "Other")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.db.Exec(`INSERT INTO credentials(id,name,type,scope,ciphertext,masked_value,created_at,updated_at) VALUES('c','C','git_token','',X'00','m','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"p1", "p2", "p3"} {
		if _, err = r.db.Exec(`INSERT INTO projects(id,name,repo_url,default_branch,credential_id,created_at,updated_at) VALUES(?,?,'https://example.invalid','main','c','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, id, id); err != nil {
			t.Fatal(err)
		}
		owner := team
		if id == "p3" {
			owner = other
		}
		if _, err = r.db.Exec(`INSERT INTO access_ownership(resource_kind,resource_id,tenant_id,project_id) VALUES('project',?,?,?)`, id, owner, id); err != nil {
			t.Fatal(err)
		}
	}
	input := RoleInput{Name: "MCP issuer", Pages: []string{"page.projects", "page.mcp"}, Grants: []authz.Grant{{Action: "project.read", Scope: authz.Scope{Kind: authz.ScopeTeam}}, {Action: "mcp.issue", Scope: authz.Scope{Kind: authz.ScopeTeam}}}}
	role, err := r.SaveRole(ctx, admin, team, "", input)
	if err != nil {
		t.Fatal(err)
	}
	employee, err := accounts.Create(ctx, admin, CreateUserInput{Username: "mcp-owner", DisplayName: "MCP Owner", Assignments: []MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
	if err != nil {
		t.Fatal(err)
	}
	login, err := accounts.Login(ctx, "mcp-owner", employee.Password)
	if err != nil {
		t.Fatal(err)
	}
	forced := WithActor(ctx, login.Actor, team)
	tokens := auth.NewMCPTokenStore(r.db, MCPAuthorization(r.db))
	if _, _, err = tokens.Create(forced, auth.MCPTokenInput{Name: "forced", ProjectIDs: []string{"p1"}}); err == nil {
		t.Fatal("forced password bypass")
	}
	login, err = accounts.ChangePassword(ctx, login.Token, employee.Password, "mcp-owner-new-password")
	if err != nil {
		t.Fatal(err)
	}
	userctx := WithActor(ctx, login.Actor, team)
	for _, id := range []string{"p3", "unknown"} {
		if _, _, err = tokens.Create(userctx, auth.MCPTokenInput{Name: "outside", ProjectIDs: []string{id}}); !errors.Is(err, ErrForbidden) {
			t.Fatal("outside project", id, err)
		}
	}
	if _, _, err = tokens.Create(userctx, auth.MCPTokenInput{Name: "trigger", ProjectIDs: []string{"p1"}, CanTrigger: true}); !errors.Is(err, ErrForbidden) {
		t.Fatal("undelegated trigger", err)
	}
	raw, token, err := tokens.Create(userctx, auth.MCPTokenInput{Name: "mine", ProjectIDs: []string{"p1", "p2"}})
	if err != nil {
		t.Fatal(err)
	}
	if token.OwnerUserID != employee.User.ID || token.OwnerTeamID != team {
		t.Fatal("owner not bound")
	}
	encoded, _ := json.Marshal(token)
	var dto map[string]any
	_ = json.Unmarshal(encoded, &dto)
	if _, exists := dto["ownerGeneration"]; exists {
		t.Fatal("internal principal leaked")
	}
	if _, err = auth.NewMCPTokenStore(r.db).Verify(ctx, raw); err == nil {
		t.Fatal("missing tenancy adapter allowed on mode")
	}
	fresh, err := tokens.Verify(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	tokenctx, err := tokens.Bind(ctx, fresh)
	if err != nil {
		t.Fatal(err)
	}
	checkIDs := func(want ...string) {
		t.Helper()
		pred, e := QueryPredicate(tokenctx, r.db, "project.read", "p.id")
		if e != nil {
			t.Fatal(e)
		}
		rows, e := r.db.Query(`SELECT p.id FROM projects p WHERE `+pred.SQL+` ORDER BY p.id`, pred.Args...)
		if e != nil {
			t.Fatal(e)
		}
		got := []string{}
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				t.Fatal(e)
			}
			got = append(got, id)
		}
		rows.Close()
		if len(got) != len(want) {
			t.Fatalf("IDs %v want %v", got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("IDs %v want %v", got, want)
			}
		}
	}
	checkIDs("p1", "p2")
	// A different actor cannot manage or enumerate the owner's token.
	adminctx := WithActor(ctx, admin, team)
	list, err := tokens.List(adminctx)
	if err != nil || len(list) != 0 {
		t.Fatal("other user's tokens visible", list, err)
	}
	if err = tokens.Revoke(adminctx, token.ID); !errors.Is(err, ErrForbidden) {
		t.Fatal("other user's revoke", err)
	}
	input.Grants[0].Scope = authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{"p2"}}
	input.Grants[1].Scope = input.Grants[0].Scope
	input.ExpectedRevision = role.Revision
	if _, err = r.SaveRole(ctx, admin, team, role.ID, input); err != nil {
		t.Fatal(err)
	}
	checkIDs("p2")
	if err = Check(tokenctx, r.db, "project.read", "project", "p1"); !errors.Is(err, ErrForbidden) {
		t.Fatal("revoked project still allowed", err)
	}
	if err = tokens.Revoke(userctx, token.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = tokens.Verify(ctx, raw); err == nil {
		t.Fatal("revoked token accepted")
	}
	if _, err = QueryPredicate(tokenctx, r.db, "project.read", "p.id"); err == nil {
		t.Fatal("old context after revoke accepted")
	}
	raw, token, err = tokens.Create(userctx, auth.MCPTokenInput{Name: "generation", ProjectIDs: []string{"p2"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.db.Exec(`DELETE FROM access_sessions WHERE token_hash=?`, login.Actor.SessionHash); err != nil {
		t.Fatal(err)
	}
	if _, err = tokens.Verify(ctx, raw); err != nil {
		t.Fatal("browser logout revoked independent token", err)
	}
	if _, err = r.db.Exec(`UPDATE access_users SET security_generation=security_generation+1 WHERE id=?`, employee.User.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = tokens.Verify(ctx, raw); err == nil {
		t.Fatal("password generation ignored")
	}
	if _, err = r.db.Exec(`UPDATE access_control SET enabled=0,mode_epoch=mode_epoch+1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err = tokens.Verify(ctx, raw); err == nil {
		t.Fatal("old token survived mode off")
	}
	if _, err = QueryPredicate(tokenctx, r.db, "project.read", "p.id"); err == nil {
		t.Fatal("old token context became unrestricted off")
	}
}

func TestMCPApprovedRunStopsWhenSourceTokenRevoked(t *testing.T) {
	_, r, admin := accountFixture(t)
	ctx := context.Background()
	team, err := r.CreateTeam(ctx, "MCP execution")
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{`INSERT INTO credentials(id,name,type,scope,ciphertext,masked_value,created_at,updated_at) VALUES('c','C','git_token','',X'00','m','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, `INSERT INTO projects(id,name,repo_url,default_branch,credential_id,created_at,updated_at) VALUES('p','P','https://example.invalid','main','c','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, `INSERT INTO pipeline_runs(id,project_id,status,created_at) VALUES('run','p','queued','2026-01-01T00:00:00Z')`} {
		if _, err = r.db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = r.db.Exec(`INSERT INTO access_ownership(resource_kind,resource_id,tenant_id,project_id) VALUES('project','p',?,'p')`, team); err != nil {
		t.Fatal(err)
	}
	adminctx := WithActor(ctx, admin, team)
	tokens := auth.NewMCPTokenStore(r.db, MCPAuthorization(r.db))
	raw, token, err := tokens.Create(adminctx, auth.MCPTokenInput{Name: "run", ProjectIDs: []string{"p"}, CanTrigger: true})
	if err != nil {
		t.Fatal(err)
	}
	verified, err := tokens.Verify(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	request, err := tokens.Bind(ctx, verified)
	if err != nil {
		t.Fatal(err)
	}
	digest := strings.Repeat("b", 64)
	err = configstate.WithWriteTxContext(request, r.db, func(txctx context.Context, q configstate.DBTX) error {
		if _, err := configstate.LockProject(txctx, q, r.db, "p"); err != nil {
			return err
		}
		if err := RecordOwnership(txctx, r.db, "run", "run", "p"); err != nil {
			return err
		}
		return ApproveRun(txctx, r.db, "run", "p", digest, []ExecutionPermission{{Action: "run.start", ResourceID: "run"}})
	})
	if err != nil {
		t.Fatal(err)
	}
	job := RunContext(ctx, r.db, "run", digest)
	if err = CheckRunBoundary(job, r.db, "run"); err != nil {
		t.Fatal(err)
	}
	if err = tokens.Revoke(adminctx, token.ID); err != nil {
		t.Fatal(err)
	}
	if err = CheckRunBoundary(job, r.db, "run"); err == nil {
		t.Fatal("source token revoke ignored by approved job")
	}
}
