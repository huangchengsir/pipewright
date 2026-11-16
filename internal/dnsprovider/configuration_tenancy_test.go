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

type countConfigurationDNS struct {
	calls int
	hook  func()
}

func (d *countConfigurationDNS) EnsureARecord(context.Context, string, string, string) error {
	d.calls++
	return nil
}
func (d *countConfigurationDNS) DeleteARecord(context.Context, string, string) error {
	d.calls++
	return nil
}
func (d *countConfigurationDNS) VerifyZone(context.Context, string) error {
	d.calls++
	if d.hook != nil {
		d.hook()
	}
	return nil
}
func TestDNSConfigurationAuthorizationAndAtomicSecret(t *testing.T) {
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
	valid := CreateInput{Type: TypeCloudflare, Name: "Scoped DNS", BaseDomain: "example.invalid"}
	for name, fn := range map[string]func() error{"list": func() error { _, e := svc.List(user); return e }, "get": func() error { _, e := svc.Get(user, "unknown"); return e }, "create": func() error { _, e := svc.Create(user, valid); return e }, "token create": func() error { _, e := svc.CreateWithToken(user, valid, "synthetic-secret"); return e }, "delete": func() error { return svc.Delete(user, "unknown") }, "verify": func() error { return svc.Verify(user, "unknown") }} {
		if e := fn(); !errors.Is(e, tenancy.ErrForbidden) {
			t.Fatal(name, e)
		}
	}
	count := func(table string) int {
		var n int
		if e := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	if count("credentials") != 0 || count("dns_providers") != 0 || client.calls != 0 {
		t.Fatal("denied side effects")
	}
	invalid := valid
	invalid.BaseDomain = "not-a-domain"
	if _, err = svc.CreateWithToken(adminCtx, invalid, "synthetic-secret"); err == nil {
		t.Fatal("invalid domain accepted")
	}
	if count("credentials") != 0 || count("dns_providers") != 0 {
		t.Fatal("failed provider left secret")
	}
	provider, err := svc.CreateWithToken(adminCtx, valid, "synthetic-secret")
	if err != nil {
		t.Fatal(err)
	}
	if count("credentials") != 1 || count("dns_providers") != 1 {
		t.Fatal("atomic create")
	}
	var ownerTeam string
	if err = db.QueryRow(`SELECT tenant_id FROM access_ownership WHERE resource_kind='dns_provider' AND resource_id=?`, provider.ID).Scan(&ownerTeam); err != nil || ownerTeam != team {
		t.Fatal("owner", ownerTeam, err)
	}
	if err = svc.Verify(user, provider.ID); !errors.Is(err, tenancy.ErrForbidden) || client.calls != 0 {
		t.Fatal("denied verify", err, client.calls)
	}
	if err = svc.Verify(adminCtx, provider.ID); err != nil || client.calls != 1 {
		t.Fatal("allowed verify", err, client.calls)
	}
	client.hook = func() {
		if e := accounts.Logout(ctx, admin.Token); e != nil {
			t.Fatal(e)
		}
	}
	if err = svc.Verify(adminCtx, provider.ID); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("stale verification success", err)
	}
	if err = svc.Delete(adminCtx, provider.ID); !errors.Is(err, tenancy.ErrForbidden) || count("dns_providers") != 1 {
		t.Fatal("revoked delete", err)
	}
}
