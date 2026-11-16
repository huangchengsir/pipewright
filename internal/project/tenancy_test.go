package project

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"github.com/huangchengsir/pipewright/internal/vault"
	"testing"
)

func TestTeamProjectQueriesCountPageDetailAndWriteBoundary(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	v := vault.New(db, testMasterKey())
	probe := &stubProber{branch: "main"}
	service := New(db, v, probe)
	credential := newCred(t, v, "test-only-placeholder")
	first, err := service.Create(ctx, CreateInput{Name: "Allowed", RepoURL: "https://example.invalid/one", CredentialID: credential})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Create(ctx, CreateInput{Name: "Hidden", RepoURL: "https://example.invalid/two", CredentialID: credential})
	if err != nil {
		t.Fatal(err)
	}
	old := auth.NewService(db, nil)
	if err = old.Bootstrap("admin", "original-admin-password"); err != nil {
		t.Fatal(err)
	}
	repo := tenancy.NewRepository(db)
	if _, err = repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true); err != nil {
		t.Fatal(err)
	}
	var team string
	db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team)
	accounts := tenancy.NewAccounts(db)
	admin, err := accounts.Login(ctx, "admin", "original-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	roles, err := repo.Roles(ctx, team)
	if err != nil {
		t.Fatal(err)
	}
	var viewer string
	for _, role := range roles {
		if role.PresetKey == "viewer" {
			viewer = role.ID
		}
	}
	initial, err := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: "alice", DisplayName: "Alice", Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: viewer, Scope: authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{first.ID}}}}})
	if err != nil {
		t.Fatal(err)
	}
	login, err := accounts.Login(ctx, "alice", initial.Password)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.ListPaged(tenancy.WithActor(ctx, login.Actor, team), 1, 20); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("restricted credential accessed business", err)
	}
	user, err := accounts.ChangePassword(ctx, login.Token, initial.Password, "alice-new-password")
	if err != nil {
		t.Fatal(err)
	}
	userctx := tenancy.WithActor(ctx, user.Actor, team)
	page, err := service.ListPaged(userctx, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != first.ID {
		t.Fatal("list/count exposed unauthorized project", page)
	}
	page, err = service.ListPaged(userctx, 2, 1)
	if err != nil || page.Total != 1 || len(page.Items) != 0 {
		t.Fatal("pagination exposed unauthorized project", page, err)
	}
	if _, err = service.Get(userctx, second.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("hidden detail distinguishable", err)
	}
	before := probe.calls
	name := "Denied"
	if _, err = service.Update(userctx, first.ID, UpdateInput{Name: &name}); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal(err)
	}
	if err = service.Delete(userctx, first.ID); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal(err)
	}
	if _, err = service.TestClone(userctx, "https://example.invalid/deny", credential); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal(err)
	}
	if probe.calls != before {
		t.Fatal("unauthorized external credential use")
	}
	if _, err = service.List(ctx); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("missing actor allowed in enabled mode", err)
	}
	other, err := repo.CreateTeam(ctx, "Other")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.List(tenancy.WithActor(ctx, user.Actor, other)); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("team header switched membership", err)
	}
	adminctx := tenancy.WithActor(ctx, admin.Actor, team)
	created, err := service.Create(adminctx, CreateInput{Name: "New", RepoURL: "https://example.invalid/new", CredentialID: credential})
	if err != nil {
		t.Fatal(err)
	}
	resource, err := repo.Resource(ctx, team, "project", created.ID)
	if err != nil || resource.OwnerID != admin.User.ID {
		t.Fatal("new owner not recorded", resource, err)
	}
	if err = accounts.Logout(ctx, user.Token); err != nil {
		t.Fatal(err)
	}
	if _, err = service.List(userctx); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("logged out actor continued reading", err)
	}
}
