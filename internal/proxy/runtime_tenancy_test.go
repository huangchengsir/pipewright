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
	"github.com/huangchengsir/pipewright/internal/vault"
	"io"
	"strings"
	"testing"
)

type scopedProxyTarget struct {
	fakeTarget
	snapshot target.ConnectionSnapshot
	hook     func()
}

func (f *scopedProxyTarget) Capture(context.Context, string) (target.ConnectionSnapshot, error) {
	return f.snapshot, nil
}
func (f *scopedProxyTarget) AuthorizePinned(ctx context.Context, snap target.ConnectionSnapshot) error {
	if snap.Fingerprint() != f.snapshot.Fingerprint() {
		return target.ErrTargetChanged
	}
	return tenancy.VerifyExecution(ctx)
}
func (f *scopedProxyTarget) ExecLimited(c context.Context, s target.ConnectionSnapshot, a []string, l target.ExecutionLimits) (*target.LimitedResult, error) {
	return f.ExecPinned(c, s, a, l)
}
func (f *scopedProxyTarget) ExecPinned(c context.Context, s target.ConnectionSnapshot, a []string, _ target.ExecutionLimits) (*target.LimitedResult, error) {
	if f.hook != nil {
		h := f.hook
		f.hook = nil
		h()
	}
	if s.Fingerprint() != f.snapshot.Fingerprint() {
		return nil, target.ErrTargetChanged
	}
	if e := tenancy.VerifyExecution(c); e != nil {
		return nil, e
	}
	r, e := f.fakeTarget.Exec(c, s.Server.ID, a)
	if e != nil {
		return nil, e
	}
	return &target.LimitedResult{Stdout: r.Stdout, Stderr: r.Stderr, ExitCode: r.ExitCode}, nil
}
func (f *scopedProxyTarget) UploadPinned(c context.Context, s target.ConnectionSnapshot, r io.Reader, path string) error {
	if s.Fingerprint() != f.snapshot.Fingerprint() {
		return target.ErrTargetChanged
	}
	if e := tenancy.VerifyExecution(c); e != nil {
		return e
	}
	return f.fakeTarget.Upload(c, s.Server.ID, r, path)
}
func TestProxyRuntimeScopedWriteAndPinnedRevocation(t *testing.T) {
	ctx := context.Background()
	db := storetest.OpenDB(t)
	v := vault.New(db, &[32]byte{1})
	credential, e := v.Create(vault.CreateInput{Name: "SSH", Type: vault.TypeSSHPassword, Secret: "fixture-only"})
	if e != nil {
		t.Fatal(e)
	}
	servers := target.New(db, v, nil)
	a, e := servers.Create(ctx, target.CreateInput{Name: "A", Host: "192.0.2.1", User: "builder", CredentialID: credential.ID})
	if e != nil {
		t.Fatal(e)
	}
	b, e := servers.Create(ctx, target.CreateInput{Name: "B", Host: "192.0.2.2", User: "builder", CredentialID: credential.ID})
	if e != nil {
		t.Fatal(e)
	}
	st := NewStore(db)
	ra := newRoute(CreateInput{ServerID: a.ID, Domain: "a.example.invalid", UpstreamContainer: "web", UpstreamPort: 80})
	rb := newRoute(CreateInput{ServerID: b.ID, Domain: "b.example.invalid", UpstreamContainer: "web", UpstreamPort: 80})
	if e = st.insert(ctx, ra); e != nil {
		t.Fatal(e)
	}
	if e = st.insert(ctx, rb); e != nil {
		t.Fatal(e)
	}
	if e = auth.NewService(db, nil).Bootstrap("admin", "administrator-proxy-password"); e != nil {
		t.Fatal(e)
	}
	repo := tenancy.NewRepository(db)
	if _, e = repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true); e != nil {
		t.Fatal(e)
	}
	accounts := tenancy.NewAccounts(db)
	admin, e := accounts.Login(ctx, "admin", "administrator-proxy-password")
	if e != nil {
		t.Fatal(e)
	}
	var team string
	if e = db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); e != nil {
		t.Fatal(e)
	}
	scope := authz.Scope{Kind: authz.ScopeSelected, ResourceRefs: []authz.ResourceRef{{Kind: "server", ID: a.ID}}}
	input := tenancy.RoleInput{Name: "Proxy", Pages: []string{"page.servers", "page.containers", "page.vault"}, Grants: []authz.Grant{{Action: "server.inspect", Scope: scope}, {Action: "proxy.read", Scope: scope}}}
	role, e := repo.SaveRole(ctx, admin.Actor, team, "", input)
	if e != nil {
		t.Fatal(e)
	}
	initial, e := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: "proxyuser", DisplayName: "Proxy", Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
	if e != nil {
		t.Fatal(e)
	}
	user, e := accounts.Login(ctx, "proxyuser", initial.Password)
	if e != nil {
		t.Fatal(e)
	}
	user, e = accounts.ChangePassword(ctx, user.Token, initial.Password, "proxy-user-password")
	if e != nil {
		t.Fatal(e)
	}
	actor := tenancy.WithActor(ctx, user.Actor, team)
	remote := &scopedProxyTarget{snapshot: target.ConnectionSnapshot{Server: *a, CredentialVersion: "fixture"}}
	svc := New(db, remote).(*service)
	rows, e := svc.List(actor, "")
	if e != nil || len(rows) != 1 || rows[0].ID != ra.ID {
		t.Fatal("scoped list", rows, e)
	}
	if e = svc.Delete(actor, ra.ID); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("read granted delete", e)
	}
	if _, e = svc.Create(actor, CreateInput{ServerID: b.ID, Domain: "outside.example.invalid", UpstreamContainer: "web", UpstreamPort: 80}); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("out of scope create", e)
	}
	if e = svc.SetEnabled(actor, rb.ID, false); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("outside toggle", e)
	}
	if len(remote.execCalls) != 0 || len(remote.uploads) != 0 {
		t.Fatal("denied SSH effect")
	}
	for _, action := range []string{"proxy.manage", "server.use", "credential.metadata", "credential.use"} {
		resourceScope := scope
		if action == "credential.metadata" || action == "credential.use" {
			resourceScope = authz.Scope{Kind: authz.ScopeSelected, ResourceRefs: []authz.ResourceRef{{Kind: "credential", ID: credential.ID}}}
		}
		input.Grants = append(input.Grants, authz.Grant{Action: action, Scope: resourceScope})
	}
	input.ExpectedRevision = role.Revision
	role, e = repo.SaveRole(ctx, admin.Actor, team, role.ID, input)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = svc.PrepareCaddy(actor, a.ID); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("manage granted installation", e)
	}
	if e = svc.SetEnabled(actor, ra.ID, false); e != nil {
		t.Fatal("allowed toggle", e)
	}
	bound, verify, err := svc.PrepareDNSRoute(actor, a.ID)
	if err != nil || bound == nil {
		t.Fatal("prepare DNS route", err)
	}
	originalHost := remote.snapshot.Server.Host
	remote.snapshot.Server.Host = "192.0.2.98"
	if e = verify(actor); !errors.Is(e, target.ErrTargetChanged) {
		t.Fatal("DNS preflight did not pin endpoint", e)
	}
	remote.snapshot.Server.Host = originalHost
	before := len(remote.execCalls)
	remote.hook = func() { remote.snapshot.Server.Host = "192.0.2.99" }
	if _, e = svc.Update(actor, rb.ID, UpdateInput{}); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("foreign update", e)
	}
	if e = svc.SetEnabled(actor, ra.ID, true); e == nil {
		t.Fatal("changed endpoint used")
	}
	if len(remote.execCalls) != before {
		t.Fatal("redirected SSH executed")
	}
	remote.snapshot.Server = *a
	remote.hook = func() {
		input.ExpectedRevision = role.Revision
		input.Grants = input.Grants[:2]
		var err error
		role, err = repo.SaveRole(ctx, admin.Actor, team, role.ID, input)
		if err != nil {
			t.Fatal(err)
		}
	}
	if e = svc.SetEnabled(actor, ra.ID, true); e == nil {
		t.Fatal("revoke during dispatch accepted")
	}
	if len(remote.execCalls) != before {
		t.Fatal("revoked action executed")
	}
}

func TestImplicitCaddyInstallationRequiresIndependentGrant(t *testing.T) {
	for _, scenario := range []string{"network", "container", "ports"} {
		t.Run(scenario, func(t *testing.T) {
			remote := &fakeTarget{resultFor: func(cmd []string) *target.ExecResult {
				joined := strings.Join(cmd, " ")
				if scenario == "network" && strings.HasPrefix(joined, "docker network inspect") {
					return &target.ExecResult{ExitCode: 1}
				}
				if scenario == "container" && joined == "docker inspect "+caddyContainer {
					return &target.ExecResult{ExitCode: 1}
				}
				if strings.Contains(joined, "--format") {
					return &target.ExecResult{Stdout: `{"80/tcp":[{"HostPort":"80"}],"443/tcp":[{"HostPort":"443"}]}`}
				}
				return &target.ExecResult{}
			}}
			err := ensureCaddy(context.Background(), remote, "server", []int{9000}, func(context.Context) error { return tenancy.ErrForbidden })
			if !errors.Is(err, tenancy.ErrForbidden) {
				t.Fatal(err)
			}
			for _, cmd := range remote.execCalls {
				joined := strings.Join(cmd, " ")
				if strings.Contains(joined, "network create") || strings.Contains(joined, "docker rm") || strings.Contains(joined, "docker pull") || strings.Contains(joined, "docker run") {
					t.Fatal("denied install performed mutation", joined)
				}
			}
		})
	}
}
