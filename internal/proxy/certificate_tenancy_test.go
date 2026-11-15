package proxy

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/target"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"testing"
)

type countCertProbe struct {
	calls int
	hook  func()
}

func (p *countCertProbe) Probe(context.Context, string) ProbeResult {
	p.calls++
	if p.hook != nil {
		p.hook()
	}
	return ProbeResult{Status: CertStatusIssued, Detail: "synthetic issued"}
}

type deniedCertTarget struct{ fakeTarget }

func (t *deniedCertTarget) Exec(context.Context, string, []string) (*target.ExecResult, error) {
	return nil, tenancy.ErrForbidden
}
func TestCertificateScopesAndRefreshRevocation(t *testing.T) {
	ctx := context.Background()
	db := storetest.OpenDB(t)
	st := NewStore(db)
	routes := []*Route{}
	for _, name := range []string{"selected", "same-team-hidden", "foreign"} {
		r := newRoute(CreateInput{ServerID: "fixture-host", Domain: name + ".example.invalid", UpstreamContainer: "web", UpstreamPort: 80})
		r.Config = RouteConfig{Aliases: []string{"alias.example.invalid"}, BasicAuthUser: "private-user", BasicAuthHash: "private-hash", IPAllow: []string{"10.0.0.0/8"}}
		if err := st.insert(ctx, r); err != nil {
			t.Fatal(err)
		}
		routes = append(routes, r)
	}
	if err := auth.NewService(db, nil).Bootstrap("admin", "certificate-admin-password"); err != nil {
		t.Fatal(err)
	}
	repo := tenancy.NewRepository(db)
	if _, err := repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true); err != nil {
		t.Fatal(err)
	}
	accounts := tenancy.NewAccounts(db)
	admin, err := accounts.Login(ctx, "admin", "certificate-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	var team string
	if err = db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); err != nil {
		t.Fatal(err)
	}
	other, err := repo.CreateTeamAs(ctx, admin.Actor, "Other")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE access_ownership SET tenant_id=? WHERE resource_kind='proxy_route' AND resource_id=?`, other.ID, routes[2].ID); err != nil {
		t.Fatal(err)
	}
	scope := authz.Scope{Kind: authz.ScopeSelected, ResourceRefs: []authz.ResourceRef{{Kind: "proxy_route", ID: routes[0].ID}}}
	roleInput := tenancy.RoleInput{Name: "Certificate reader", Pages: []string{"page.certificates"}, Grants: []authz.Grant{{Action: "certificate.read", Scope: scope}}}
	role, err := repo.SaveRole(ctx, admin.Actor, team, "", roleInput)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: "certreader", DisplayName: "Cert", Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
	if err != nil {
		t.Fatal(err)
	}
	login, err := accounts.Login(ctx, "certreader", initial.Password)
	if err != nil {
		t.Fatal(err)
	}
	login, err = accounts.ChangePassword(ctx, login.Token, initial.Password, "certificate-reader-password")
	if err != nil {
		t.Fatal(err)
	}
	user := tenancy.WithActor(ctx, login.Actor, team)
	probe := &countCertProbe{}
	svc := &service{store: st, prober: probe}
	list, err := svc.Overview(user)
	if err != nil || len(list) != 1 || list[0].ID != routes[0].ID {
		t.Fatal("scoped overview", list, err)
	}
	if list[0].Config.BasicAuthUser != "" || list[0].Config.BasicAuthHash != "" || len(list[0].Config.IPAllow) != 0 || len(list[0].Config.Aliases) != 1 {
		t.Fatal("unnecessary config exposed")
	}
	if _, err = svc.RefreshStatus(user, routes[0].ID); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("read grants refresh", err)
	}
	for _, id := range []string{routes[1].ID, routes[2].ID, "unknown"} {
		if _, err = svc.RefreshStatus(user, id); !errors.Is(err, ErrNotFound) {
			t.Fatal("hidden resource", id, err)
		}
	}
	if probe.calls != 0 {
		t.Fatal("denied effects", probe.calls)
	}
	roleInput.ExpectedRevision = role.Revision
	roleInput.Grants = append(roleInput.Grants, authz.Grant{Action: "certificate.refresh", Scope: scope})
	role, err = repo.SaveRole(ctx, admin.Actor, team, role.ID, roleInput)
	if err != nil {
		t.Fatal(err)
	}
	svc.tg = &deniedCertTarget{}
	if _, err = svc.RefreshStatus(user, routes[0].ID); !errors.Is(err, tenancy.ErrForbidden) || probe.calls != 0 {
		t.Fatal("SSH denial fell back to public probe", err, probe.calls)
	}
	svc.tg = nil
	if _, err = svc.RefreshStatus(user, routes[0].ID); err != nil || probe.calls != 1 {
		t.Fatal("allowed refresh", err, probe.calls)
	}
	probe.hook = func() {
		if e := accounts.Logout(ctx, login.Token); e != nil {
			t.Fatal(e)
		}
	}
	if _, err = db.Exec(`UPDATE proxy_routes SET cert_status='pending',cert_detail='unchanged' WHERE id=?`, routes[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.RefreshStatus(user, routes[0].ID); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("revocation after probe", err)
	}
	current, err := st.get(ctx, routes[0].ID)
	if err != nil || current.CertStatus != "pending" || current.CertDetail != "unchanged" {
		t.Fatal("unauthorized final write", current, err)
	}
}
