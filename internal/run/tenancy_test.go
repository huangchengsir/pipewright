package run

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"strings"
	"testing"
)

func TestRunTeamQueriesFilterCountDetailsAndChildEvidence(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	svc := New(db)
	first, second := seedProject(t, db), seedProject(t, db)
	allowed, err := svc.Create(ctx, first, Trigger{Type: TriggerManual, Branch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	hidden, err := svc.Create(ctx, second, Trigger{Type: TriggerManual, Branch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []*Run{allowed, hidden} {
		if _, err = svc.Cancel(ctx, row.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = svc.AppendLog(ctx, row.ID, "stdout", 1, "project-private-log"); err != nil {
			t.Fatal(err)
		}
	}
	legacy := auth.NewService(db, nil)
	if err = legacy.Bootstrap("admin", "admin-run-password"); err != nil {
		t.Fatal(err)
	}
	repo := tenancy.NewRepository(db)
	if _, err = repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true); err != nil {
		t.Fatal(err)
	}
	var team string
	db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team)
	accounts := tenancy.NewAccounts(db)
	admin, err := accounts.Login(ctx, "admin", "admin-run-password")
	if err != nil {
		t.Fatal(err)
	}
	roles, err := repo.Roles(ctx, team)
	if err != nil {
		t.Fatal(err)
	}
	var viewer string
	for _, r := range roles {
		if r.PresetKey == "viewer" {
			viewer = r.ID
		}
	}
	initial, err := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: "reader", DisplayName: "Reader", Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: viewer, Scope: authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{first}}}}})
	if err != nil {
		t.Fatal(err)
	}
	login, err := accounts.Login(ctx, "reader", initial.Password)
	if err != nil {
		t.Fatal(err)
	}
	login, err = accounts.ChangePassword(ctx, login.Token, initial.Password, "reader-run-password")
	if err != nil {
		t.Fatal(err)
	}
	userctx := tenancy.WithActor(ctx, login.Actor, team)
	if _, err = db.Exec(`INSERT INTO access_environments(id,tenant_id,project_id,name,purpose,revision) VALUES('run-saved-env',?,?,'test','test',1)`, team, first); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE pipeline_runs SET resolved_environment='test' WHERE id=?`, allowed.ID); err != nil {
		t.Fatal(err)
	}
	if got, e := svc.Get(userctx, allowed.ID); e != nil || got.AccessEnvironmentID != "run-saved-env" {
		t.Fatal("saved run environment projection", got, e)
	}
	if _, err = db.Exec(`UPDATE pipeline_runs SET resolved_environment='missing' WHERE id=?`, allowed.ID); err != nil {
		t.Fatal(err)
	}
	if got, e := svc.Get(userctx, allowed.ID); e != nil || got.AccessEnvironmentID != "" {
		t.Fatal("missing environment must not use default", got, e)
	}
	page, err := svc.List(userctx, ListFilter{PageSize: 1})
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != allowed.ID {
		t.Fatal(page, err)
	}
	page, err = svc.List(userctx, ListFilter{Page: 2, PageSize: 1})
	if err != nil || page.Total != 1 || len(page.Items) != 0 {
		t.Fatal(page, err)
	}
	if _, err = svc.Get(userctx, hidden.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("hidden run", err)
	}
	logs, err := svc.GetLogs(userctx, allowed.ID, 0)
	if err != nil || len(logs) != 1 {
		t.Fatal(logs, err)
	}
	logs, err = svc.GetLogs(userctx, hidden.ID, 0)
	if err != nil || len(logs) != 0 {
		t.Fatal("hidden logs", logs, err)
	}
	if _, err = svc.GetFailureLog(userctx, hidden.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("hidden failure log", err)
	}
	if _, err = svc.GetDiagnosis(userctx, hidden.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("hidden diagnosis", err)
	}

	// Viewing a run never implicitly grants its archived bytes.
	if err = AuthorizeArtifactDownload(userctx, svc, allowed.ID); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("viewer download", err)
	}
	scope := authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{first}}
	role, err := repo.SaveRole(ctx, admin.Actor, team, "", tenancy.RoleInput{Name: "Artifact reader", Pages: []string{"page.runs"}, Grants: []authz.Grant{{Action: "run.read", Scope: scope}, {Action: "artifact.download", Scope: scope}}})
	if err != nil {
		t.Fatal(err)
	}
	var memberID string
	var revision uint64
	if err = db.QueryRow(`SELECT id,revision FROM access_members WHERE tenant_id=? AND user_id=?`, team, login.Actor.UserID).Scan(&memberID, &revision); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.SaveMember(ctx, admin.Actor, team, memberID, tenancy.MemberInput{ExpectedRevision: revision, UserID: login.Actor.UserID, Enabled: true, Bindings: []tenancy.MemberRoleInput{{RoleID: role.ID, Scope: scope}}}); err != nil {
		t.Fatal(err)
	}
	if err = AuthorizeArtifactDownload(userctx, svc, allowed.ID); err != nil {
		t.Fatal("authorized download", err)
	}
	if err = AuthorizeArtifactDownload(userctx, svc, hidden.ID); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("out-of-scope download", err)
	}
	inherited := ArtifactDownloadReader(userctx, svc, allowed.ID, strings.NewReader("private artifact"), hidden.ID)
	if n, e := inherited.Read(make([]byte, 4)); n != 0 || !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("inherited artifact bypass", n, e)
	}
	stream := ArtifactDownloadReader(userctx, svc, allowed.ID, strings.NewReader("private artifact"))
	buf := make([]byte, 4)
	if n, e := stream.Read(buf); n != 4 || e != nil {
		t.Fatal("first chunk", n, e)
	}
	if err = accounts.Logout(ctx, login.Token); err != nil {
		t.Fatal(err)
	}
	if n, e := stream.Read(buf); n != 0 || !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("revoked stream leaked bytes", n, e)
	}
	if _, err = svc.GetLogs(userctx, allowed.ID, 0); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("logged-out stream", err)
	}
}
