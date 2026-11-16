package vault_test

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/httpapi"
	"github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"github.com/huangchengsir/pipewright/internal/vault"
	"testing"
)

func TestTenantVaultScopesUseRevealAndLegacyEntryPoints(t *testing.T) {
	ctx := context.Background()
	db := storetest.OpenDB(t)
	key := [32]byte{7}
	v := vault.New(db, &key)
	first, err := v.Create(vault.CreateInput{Name: "A", Type: vault.TypeGitToken, Secret: "first-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if _, release, err := vault.AcquireGitAuth(ctx, v, ""); err != nil {
		t.Fatal("off anonymous Git", err)
	} else {
		release()
	}
	if err = auth.NewService(db, nil).Bootstrap("admin", "admin-vault-password"); err != nil {
		t.Fatal(err)
	}
	repo := tenancy.NewRepository(db)
	if _, err = repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true); err != nil {
		t.Fatal(err)
	}
	var team string
	db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team)
	accounts := tenancy.NewAccounts(db)
	admin, err := accounts.Login(ctx, "admin", "admin-vault-password")
	if err != nil {
		t.Fatal(err)
	}
	other, err := repo.CreateTeamAs(ctx, admin.Actor, "Other")
	if err != nil {
		t.Fatal(err)
	}
	otherVault := vault.WithContext(tenancy.WithActor(ctx, admin.Actor, other.ID), v)
	second, err := otherVault.Create(vault.CreateInput{Name: "B", Type: vault.TypeGitToken, Secret: "second-secret"})
	if err != nil {
		t.Fatal(err)
	}
	role, err := repo.SaveRole(ctx, admin.Actor, team, "", tenancy.RoleInput{Name: "Use one", Pages: []string{"page.vault"}, Grants: []authz.Grant{{Action: "credential.metadata", Scope: authz.Scope{Kind: authz.ScopeTeam}}, {Action: "credential.use", Scope: authz.Scope{Kind: authz.ScopeTeam}}}, RequiresSelectedBinding: true})
	if err != nil {
		t.Fatal(err)
	}
	initial, err := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: "consumer", DisplayName: "Consumer", Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeSelected, ResourceRefs: []authz.ResourceRef{{Kind: "credential", ID: first.ID}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	login, err := accounts.Login(ctx, "consumer", initial.Password)
	if err != nil {
		t.Fatal(err)
	}
	login, err = accounts.ChangePassword(ctx, login.Token, initial.Password, "consumer-vault-password")
	if err != nil {
		t.Fatal(err)
	}
	userctx := tenancy.WithActor(ctx, login.Actor, team)
	scoped := vault.WithContext(userctx, v)
	list, err := scoped.List()
	if err != nil || len(list) != 1 || list[0].ID != first.ID {
		t.Fatal("SQL listing scope", list, err)
	}
	if plain, err := vault.Use(userctx, v, first.ID); err != nil || plain != "first-secret" {
		t.Fatal("explicit use", err)
	}
	if _, err = scoped.Reveal(first.ID); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("use implied reveal", err)
	}
	if _, err = scoped.Get(second.ID); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("other team secret", err)
	}
	if _, err = scoped.Update(first.ID, vault.UpdateInput{}); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("use implied edit", err)
	}
	for _, call := range []func() error{func() error { _, e := v.Get(first.ID); return e }, func() error { _, e := v.List(); return e }, func() error { _, e := v.Reveal(first.ID); return e }, func() error { _, e := v.GetGitAuth(first.ID); return e }} {
		if e := call(); !errors.Is(e, tenancy.ErrForbidden) {
			t.Fatal("context-free bypass", e)
		}
	}
	if auth, release, err := vault.AcquireGitAuth(userctx, v, ""); err != nil || auth.Token != "" {
		t.Fatal("on anonymous Git", err)
	} else {
		release()
	}

	// A registry metadata view is not a plaintext-reveal operation.
	registry, err := vault.WithContext(tenancy.WithActor(ctx, admin.Actor, team), v).Create(vault.CreateInput{Name: "Registry", Type: vault.TypeRegistry, Secret: "registry-user:registry-secret"})
	if err != nil {
		t.Fatal(err)
	}
	metadataRole, err := repo.SaveRole(ctx, admin.Actor, team, "", tenancy.RoleInput{Name: "Registry metadata", Pages: []string{"page.vault"}, Grants: []authz.Grant{{Action: "credential.metadata", Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
	if err != nil {
		t.Fatal(err)
	}
	makeViewer := func(name string, role string, scope authz.Scope) context.Context {
		initial, e := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: name, DisplayName: name, Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role, Scope: scope}}})
		if e != nil {
			t.Fatal(e)
		}
		login, e := accounts.Login(ctx, name, initial.Password)
		if e != nil {
			t.Fatal(e)
		}
		login, e = accounts.ChangePassword(ctx, login.Token, initial.Password, name+"-long-password")
		if e != nil {
			t.Fatal(e)
		}
		return tenancy.WithActor(ctx, login.Actor, team)
	}
	metadatactx := makeViewer("metadata", metadataRole.ID, authz.Scope{Kind: authz.ScopeSelected, ResourceRefs: []authz.ResourceRef{{Kind: "credential", ID: registry.ID}}})
	views, e := vault.WithContext(metadatactx, v).List()
	if e != nil || len(views) != 1 || views[0].Username != "registry-user" || views[0].MaskedValue == "registry-secret" {
		t.Fatal("registry metadata required reveal", views, e)
	}
	if _, e = vault.WithContext(metadatactx, v).Reveal(registry.ID); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("registry metadata revealed secret", e)
	}

	// Read-only run access must still receive fully scrubbed logs without secret grants.
	if _, err = db.Exec(`INSERT INTO projects(id,name,repo_url,default_branch,credential_id,created_at,updated_at) VALUES('p','P','https://example.invalid','main',?,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO pipeline_runs(id,project_id,status,created_at) VALUES('r','p','success','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	for _, root := range []struct{ kind, id string }{{"project", "p"}, {"run", "r"}} {
		if _, err = db.Exec(`INSERT INTO access_ownership(resource_kind,resource_id,tenant_id,project_id) VALUES(?,?,?,'p')`, root.kind, root.id, team); err != nil {
			t.Fatal(err)
		}
	}
	viewerRole, err := repo.SaveRole(ctx, admin.Actor, team, "", tenancy.RoleInput{Name: "Run viewer", Pages: []string{"page.runs"}, Grants: []authz.Grant{{Action: "run.read", Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
	if err != nil {
		t.Fatal(err)
	}
	viewer := makeViewer("viewer", viewerRole.ID, authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{"p"}})
	source := httpapi.NewRunSecretSource(run.New(db), nil, nil, v)
	if _, e = vault.Use(viewer, v, first.ID); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("viewer acquired use", e)
	}
	scrubber := source.MaskerForRun(viewer, "r")
	if got := scrubber.Scrub("prefix first-secret suffix"); got != "prefix "+mask.Placeholder+" suffix" {
		t.Fatal("viewer log secret leaked", got)
	}
	if got := source.MaskerForRun(viewer, "missing").Scrub("unverified content"); got != mask.Placeholder {
		t.Fatal("missing run registration failed open", got)
	}
	if _, err = db.Exec(`UPDATE credentials SET ciphertext=X'00' WHERE id=?`, first.ID); err != nil {
		t.Fatal(err)
	}
	if got := source.MaskerForRun(viewer, "r").Scrub("first-secret"); got != mask.Placeholder {
		t.Fatal("corrupt secret registration failed open", got)
	}
	if err = accounts.Logout(ctx, login.Token); err != nil {
		t.Fatal(err)
	}
	if _, err = scoped.Get(first.ID); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("scoped vault cached permission after logout", err)
	}
	if err = otherVault.Delete(second.ID); err != nil {
		t.Fatal("admin delete", err)
	}
	var n int
	if err = db.QueryRow(`SELECT COUNT(*) FROM access_ownership WHERE resource_kind='credential' AND resource_id=?`, second.ID).Scan(&n); err != nil || n != 0 {
		t.Fatal("orphan ownership", n, err)
	}
}
