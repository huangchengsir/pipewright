package dnsprovider

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"github.com/huangchengsir/pipewright/internal/vault"
	"testing"
)

func TestDNSRuntimeScopesRotationAndRevocation(t *testing.T) {
	ctx := context.Background()
	db := storetest.OpenDB(t)
	key := [32]byte{1, 2, 3}
	v := vault.New(db, &key)
	svc := New(db, v, nil).(*service)
	client := &countConfigurationDNS{}
	svc.dial = func(string, string) DNSClient { return client }
	if err := auth.NewService(db, nil).Bootstrap("admin", "dns-config-admin-password"); err != nil {
		t.Fatal(err)
	}
	repo := tenancy.NewRepository(db)
	if _, err := repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true); err != nil {
		t.Fatal(err)
	}
	accounts := tenancy.NewAccounts(db)
	admin, err := accounts.Login(ctx, "admin", "dns-config-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	var team string
	if err = db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); err != nil {
		t.Fatal(err)
	}
	adminCtx := tenancy.WithActor(ctx, admin.Actor, team)
	role, err := repo.SaveRole(ctx, admin.Actor, team, "", tenancy.RoleInput{Name: "Read projects", Pages: []string{"page.projects"}, Grants: []authz.Grant{{Action: "project.read", Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
	if err != nil {
		t.Fatal(err)
	}
	initial, err := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: "dnsuser", DisplayName: "DNS", Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
	if err != nil {
		t.Fatal(err)
	}
	login, err := accounts.Login(ctx, "dnsuser", initial.Password)
	if err != nil {
		t.Fatal(err)
	}
	login, err = accounts.ChangePassword(ctx, login.Token, initial.Password, "dns-user-new-password")
	if err != nil {
		t.Fatal(err)
	}
	user := tenancy.WithActor(ctx, login.Actor, team)

	p, err := svc.CreateWithToken(adminCtx, CreateInput{Type: TypeCloudflare, Name: "Allowed", BaseDomain: "example.invalid"}, "synthetic-dns-value")
	if err != nil {
		t.Fatal(err)
	}
	other, err := svc.CreateWithToken(adminCtx, CreateInput{Type: TypeCloudflare, Name: "Other", BaseDomain: "other.invalid"}, "other-synthetic-value")
	if err != nil {
		t.Fatal(err)
	}
	selected := func(kind, id string) authz.Scope {
		return authz.Scope{Kind: authz.ScopeSelected, ResourceRefs: []authz.ResourceRef{{Kind: kind, ID: id}}}
	}
	grants := []authz.Grant{{Action: "dns.use", Scope: selected("dns_provider", p.ID)}}
	save := func() {
		var e error
		role, e = repo.SaveRole(ctx, admin.Actor, team, role.ID, tenancy.RoleInput{ExpectedRevision: role.Revision, Name: "Scoped DNS", Pages: []string{"page.containers", "page.vault"}, Grants: grants})
		if e != nil {
			t.Fatal(e)
		}
	}
	save()
	if _, _, _, e := svc.ResolveToken(user, p.ID); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("provider use alone revealed credential", e)
	}
	if items, e := svc.ListUsable(user); e != nil || len(items) != 0 {
		t.Fatal("credential-denied picker", items, e)
	}
	grants = append(grants, authz.Grant{Action: "credential.metadata", Scope: selected("credential", p.CredentialID)}, authz.Grant{Action: "credential.use", Scope: selected("credential", p.CredentialID)})
	save()
	items, err := svc.ListUsable(user)
	if err != nil || len(items) != 1 || items[0].ID != p.ID {
		t.Fatal("SQL scope picker", items, err)
	}
	if _, e := svc.List(user); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("runtime privilege opened config", e)
	}
	if _, _, _, e := svc.ResolveToken(user, other.ID); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("outside provider", e)
	}
	if _, token, ok, e := svc.ResolveToken(user, p.ID); e != nil || !ok || token != "synthetic-dns-value" {
		t.Fatal("allowed runtime token failed", e)
	}
	held, err := svc.runtimeClient(user, p)
	if err != nil {
		t.Fatal(err)
	}
	if err = held.VerifyZone(user, p.BaseDomain); err != nil || client.calls != 1 {
		t.Fatal("allowed DNS", err, client.calls)
	}
	if _, err = vault.WithContext(adminCtx, v).Update(p.CredentialID, vault.UpdateInput{Secret: func() *string { v := "rotated-synthetic-value"; return &v }()}); err != nil {
		t.Fatal(err)
	}
	if err = held.VerifyZone(user, p.BaseDomain); !errors.Is(err, tenancy.ErrConflict) || client.calls != 1 {
		t.Fatal("rotated client used stale secret", err, client.calls)
	}
	held, err = svc.runtimeClient(user, p)
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.DeleteSubdomainRecord(user, p.ID, "outside.other.invalid"); err == nil || client.calls != 1 {
		t.Fatal("out-of-zone deletion", err)
	}
	client.hook = func() {
		if e := accounts.Logout(ctx, login.Token); e != nil {
			t.Fatal(e)
		}
	}
	if err = held.VerifyZone(user, p.BaseDomain); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("logout during DNS returned success", err)
	}
	calls := client.calls
	if err = held.EnsureARecord(user, p.BaseDomain, "x.example.invalid", "192.0.2.2"); !errors.Is(err, tenancy.ErrForbidden) || client.calls != calls {
		t.Fatal("revoked client side effect", err)
	}
}
