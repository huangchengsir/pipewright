package previewenv

import (
	"context"
	"database/sql"
	"errors"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"testing"
	"time"
)

type tenancyRecordDeleter func(context.Context, string, string) error

func (f tenancyRecordDeleter) DeleteRecord(c context.Context, provider, fqdn string) error {
	return f(c, provider, fqdn)
}

type tenancyRouteDeleter func(context.Context, string) error

func (f tenancyRouteDeleter) DeleteRoute(ctx context.Context, id string) error { return f(ctx, id) }

func TestPreviewScopesAndExternalWriteDenials(t *testing.T) {
	svc, allocator, deleter := newTestSvc(t)
	svc.SetRecordDeleter(tenancyRecordDeleter(func(context.Context, string, string) error { return nil }))
	db := svc.store.db
	seedTenantPreviewResources(t, db)
	ctx := context.Background()
	stamp := time.Now().UTC().Format(time.RFC3339)
	for _, id := range []string{"p2", "p3"} {
		if _, err := db.Exec(`INSERT INTO projects(id,name,repo_url,default_branch,credential_id,created_at,updated_at) VALUES (?,?,'https://example.invalid/repo','main','preview-fixture',?,?)`, id, id, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	ids := map[string]string{}
	for _, id := range []string{"p1", "p2", "p3"} {
		enablePreview(t, svc, id)
		env, err := svc.Provision(ctx, ProvisionInput{ProjectID: id, PRNumber: 1, ServerID: "server", UpstreamContainer: "app", UpstreamPort: 80, HostIP: "192.0.2.1"})
		if err != nil {
			t.Fatal(err)
		}
		ids[id] = env.ID
	}
	if err := auth.NewService(db, nil).Bootstrap("admin", "preview-admin-password"); err != nil {
		t.Fatal(err)
	}
	repo := tenancy.NewRepository(db)
	if _, err := repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true); err != nil {
		t.Fatal(err)
	}
	accounts := tenancy.NewAccounts(db)
	admin, err := accounts.Login(ctx, "admin", "preview-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	var team string
	if err = db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); err != nil {
		t.Fatal(err)
	}
	other, err := repo.CreateTeamAs(ctx, admin.Actor, "Foreign")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []struct{ kind, id string }{{"project", "p3"}, {"preview", ids["p3"]}} {
		if _, err = db.Exec(`UPDATE access_ownership SET tenant_id=? WHERE resource_kind=? AND resource_id=?`, other.ID, v.kind, v.id); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err = db.QueryRow(`SELECT COUNT(*) FROM access_ownership WHERE resource_kind='preview'`).Scan(&n); err != nil || n != 3 {
		t.Fatal("legacy preview ownership", n, err)
	}
	scope := authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{"p1"}}
	role, err := repo.SaveRole(ctx, admin.Actor, team, "", tenancy.RoleInput{Name: "Preview", Pages: []string{"page.previews", "page.pipeline"}, Grants: []authz.Grant{{Action: "preview.read", Scope: scope}, {Action: "preview.reclaim", Scope: scope}, {Action: "pipeline.read", Scope: scope}, {Action: "pipeline.edit", Scope: scope}}})
	if err != nil {
		t.Fatal(err)
	}
	initial, err := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: "previewreader", DisplayName: "Preview", Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
	if err != nil {
		t.Fatal(err)
	}
	login, err := accounts.Login(ctx, "previewreader", initial.Password)
	if err != nil {
		t.Fatal(err)
	}
	login, err = accounts.ChangePassword(ctx, login.Token, initial.Password, "preview-reader-password")
	if err != nil {
		t.Fatal(err)
	}
	user := tenancy.WithActor(ctx, login.Actor, team)
	rows, err := svc.List(user, "")
	if err != nil || len(rows) != 1 || rows[0].ID != ids["p1"] {
		t.Fatal("scoped list", rows, err)
	}
	if _, err = svc.Get(user, ids["p1"]); err != nil {
		t.Fatal("allowed detail", err)
	}
	if _, err = svc.GetConfig(user, "p1"); err != nil {
		t.Fatal("allowed config", err)
	}
	for _, id := range []string{"p2", "p3"} {
		rows, err = svc.List(user, id)
		if err != nil || len(rows) != 0 {
			t.Fatal("hidden filter", id, rows, err)
		}
		if _, err = svc.Get(user, ids[id]); !errors.Is(err, ErrNotFound) {
			t.Fatal("hidden detail", err)
		}
		if _, err = svc.GetConfig(user, id); !errors.Is(err, tenancy.ErrForbidden) {
			t.Fatal("hidden config", err)
		}
	}
	beforeAlloc := len(allocator.calls)
	if err = svc.Reclaim(user, "p1", 1); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("delegated DNS reclaim", err)
	}
	if _, err = svc.Provision(user, ProvisionInput{ProjectID: "p1", PRNumber: 2, UpstreamContainer: "app", UpstreamPort: 80, HostIP: "192.0.2.1"}); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("delegated DNS provision", err)
	}
	if _, err = svc.SetConfig(user, Config{ProjectID: "p1", Enabled: false}); err != nil {
		t.Fatal("configuration editor can disable without consuming DNS", err)
	}
	if _, err = svc.SetConfig(user, Config{ProjectID: "p1", Enabled: true, DNSProviderID: "prov-1", BaseDomain: "preview.example.com"}); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("editor cannot enable external execution implicitly", err)
	}
	if len(allocator.calls) != beforeAlloc || len(deleter.deleted) != 0 {
		t.Fatal("denial had external effects")
	}
	old, err := svc.store.getConfig(ctx, "p1")
	if err != nil || old.Enabled {
		t.Fatal("denial changed config", err)
	}
	existing, err := svc.store.get(ctx, ids["p1"])
	if err != nil || existing.Status != StatusActive {
		t.Fatal("denial changed environment", err)
	}
	adminctx := tenancy.WithActor(ctx, admin.Actor, team)
	if _, err = svc.SetConfig(adminctx, Config{ProjectID: "p1", Enabled: true, DNSProviderID: "prov-1", BaseDomain: "preview.example.com"}); err != nil {
		t.Fatal(err)
	}
	fresh, err := svc.Provision(adminctx, ProvisionInput{ProjectID: "p1", PRNumber: 2, ServerID: "server", UpstreamContainer: "app", UpstreamPort: 80, HostIP: "192.0.2.1"})
	if err != nil {
		t.Fatal("admin provision", err)
	}
	if _, err = svc.Get(user, fresh.ID); err != nil {
		t.Fatal("new row missing atomic ownership", err)
	}
	if err = svc.Reclaim(adminctx, "p1", 2); err != nil {
		t.Fatal("admin reclaim", err)
	}
	if _, err = repo.SaveRole(ctx, admin.Actor, team, role.ID, tenancy.RoleInput{Name: "Preview", ExpectedRevision: role.Revision, Pages: []string{"page.previews"}}); err != nil {
		t.Fatal(err)
	}
	rows, err = svc.List(user, "")
	if err != nil || len(rows) != 0 {
		t.Fatal("revoked read", rows, err)
	}
	if err = accounts.Logout(ctx, login.Token); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.List(user, ""); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("logout", err)
	}
	// Revocation during a completed route deletion must prevent later effects and
	// the final success marker. The already completed external operation stays real.
	svc.SetRouteDeleter(tenancyRouteDeleter(func(context.Context, string) error { return accounts.Logout(ctx, admin.Token) }))
	if err = svc.Reclaim(adminctx, "p1", 1); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("revoke during effect", err)
	}
	existing, err = svc.store.get(ctx, ids["p1"])
	if err != nil || existing.Status != StatusActive {
		t.Fatal("revoked reclaim marked success", err)
	}
	// Mode toggles preserve imported team assignments and genuine creator IDs.
	for _, on := range []bool{false, true} {
		if _, err = repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: on, Explicit: true}, true); err != nil {
			t.Fatal("mode transition", on, err)
		}
	}
	var actualTeam, actualOwner string
	if err = db.QueryRow(`SELECT tenant_id FROM access_ownership WHERE resource_kind='preview' AND resource_id=?`, ids["p3"]).Scan(&actualTeam); err != nil || actualTeam != other.ID {
		t.Fatal("mode reassigned foreign preview", actualTeam, err)
	}
	if err = db.QueryRow(`SELECT owner_id FROM access_ownership WHERE resource_kind='preview' AND resource_id=?`, fresh.ID).Scan(&actualOwner); err != nil || actualOwner != admin.User.ID {
		t.Fatal("mode replaced preview creator", actualOwner, err)
	}
	choices, err := svc.Projects(tenancy.WithActor(ctx, login.Actor, team))
	if !errors.Is(err, tenancy.ErrForbidden) || len(choices) != 0 {
		t.Fatal("old session reused after toggle", choices, err)
	}

}

func seedTenantPreviewResources(t *testing.T, db *sql.DB) {
	t.Helper()
	stamp := time.Now().UTC().Format(time.RFC3339)
	for _, id := range []string{"prov-1", "prov-2"} {
		if _, e := db.Exec(`INSERT INTO dns_providers(id,type,name,credential_id,base_domain,created_at,updated_at) VALUES(?,'cloudflare',?,'preview-fixture','preview.example.com',?,?)`, id, id, stamp, stamp); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := db.Exec(`INSERT INTO servers(id,name,host,port,user,credential_id,created_at,updated_at) VALUES('server','preview-node','192.0.2.1',22,'preview','preview-fixture',?,?)`, stamp, stamp); e != nil {
		t.Fatal(e)
	}
}
