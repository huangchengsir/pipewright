package target

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"github.com/huangchengsir/pipewright/internal/vault"
	"strings"
	"testing"
)

func TestTenantServerScopeAndDeniedOperationsHaveNoSideEffects(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	v := vault.New(db, testMasterKey())
	cred := newSSHCred(t, v, "server-test-password")
	dialer := &capturingDialer{res: &ExecResult{ExitCode: 0, Stdout: "fixture"}}
	svc := New(db, v, dialer)
	create := func(name string) *Server {
		s, e := svc.Create(ctx, CreateInput{Name: name, Host: "127.0.0.1", User: "fixture", CredentialID: cred})
		if e != nil {
			t.Fatal(e)
		}
		return s
	}
	a, b, foreign := create("Same name"), create("Same name"), create("Same name")
	if e := auth.NewService(db, nil).Bootstrap("admin", "admin-target-password"); e != nil {
		t.Fatal(e)
	}
	repo := tenancy.NewRepository(db)
	if _, e := repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true); e != nil {
		t.Fatal(e)
	}
	var team string
	db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team)
	accounts := tenancy.NewAccounts(db)
	admin, e := accounts.Login(ctx, "admin", "admin-target-password")
	if e != nil {
		t.Fatal(e)
	}
	other, e := repo.CreateTeamAs(ctx, admin.Actor, "Other")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE access_ownership SET tenant_id=? WHERE resource_kind='server' AND resource_id=?`, other.ID, foreign.ID); e != nil {
		t.Fatal(e)
	}
	role, e := repo.SaveRole(ctx, admin.Actor, team, "", tenancy.RoleInput{Name: "Use selected server", Pages: []string{"page.servers", "page.vault"}, RequiresSelectedBinding: true, Grants: []authz.Grant{{Action: "server.inspect", Scope: authz.Scope{Kind: authz.ScopeTeam}}, {Action: "server.use", Scope: authz.Scope{Kind: authz.ScopeTeam}}, {Action: "credential.metadata", Scope: authz.Scope{Kind: authz.ScopeTeam}}, {Action: "credential.use", Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
	if e != nil {
		t.Fatal(e)
	}
	initial, e := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: "operator", DisplayName: "Operator", Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeSelected, ResourceRefs: []authz.ResourceRef{{Kind: "server", ID: a.ID}, {Kind: "credential", ID: cred}}}}}})
	if e != nil {
		t.Fatal(e)
	}
	login, e := accounts.Login(ctx, "operator", initial.Password)
	if e != nil {
		t.Fatal(e)
	}
	login, e = accounts.ChangePassword(ctx, login.Token, initial.Password, "operator-target-password")
	if e != nil {
		t.Fatal(e)
	}
	actor := tenancy.WithActor(ctx, login.Actor, team)
	dialer.gotAddr = ""
	if _, e = svc.Exec(actor, a.ID, []string{"uname"}); !errors.Is(e, tenancy.ErrForbidden) || dialer.gotAddr != "" {
		t.Fatal("unassigned team execution", e)
	}
	assigner := svc.(ExecutionAssignmentService)
	if _, e = assigner.AssignExecution(actor, a.ID, "SHA256:"+strings.Repeat("A", 43)); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("member self assigned executor", e)
	}
	if _, e = assigner.AssignExecution(tenancy.WithActor(ctx, admin.Actor, team), a.ID, "SHA256:"+strings.Repeat("A", 43)); e != nil {
		t.Fatal(e)
	}

	rows, e := svc.List(actor)
	if e != nil || len(rows) != 1 || rows[0].ID != a.ID {
		t.Fatal("exact list IDs", rows, e)
	}
	for _, id := range []string{b.ID, foreign.ID, "unknown"} {
		if _, e = svc.Get(actor, id); !errors.Is(e, ErrNotFound) {
			t.Fatal("out-of-range detail not hidden", id, e)
		}
		dialer.gotAddr = ""
		if _, e = svc.Exec(actor, id, []string{"touch", "should-not-execute"}); !errors.Is(e, tenancy.ErrForbidden) {
			t.Fatal("out-of-range execute", id, e)
		}
		if dialer.gotAddr != "" {
			t.Fatal("denied operation dialed server")
		}
	}
	newHost := "192.0.2.10"
	if _, e = svc.Update(actor, a.ID, UpdateInput{Host: &newHost}); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("missing edit action", e)
	}
	if e = svc.Delete(actor, a.ID); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("missing delete action", e)
	}
	var host string
	var count int
	db.QueryRow(`SELECT host FROM servers WHERE id=?`, a.ID).Scan(&host)
	db.QueryRow(`SELECT COUNT(*) FROM servers`).Scan(&count)
	if host != "127.0.0.1" || count != 3 {
		t.Fatal("rejected writes changed business rows", host, count)
	}
	dialer.gotAddr = ""
	if _, e = svc.ExecInteractive(actor, a.ID, []string{"sh"}); !errors.Is(e, tenancy.ErrForbidden) || dialer.gotAddr != "" {
		t.Fatal("use implied terminal", e)
	}
	if _, e = svc.Exec(actor, a.ID, []string{"uname", "-a"}); e != nil || dialer.gotAddr == "" {
		t.Fatal("authorized execution", e)
	}
	// A terminal grant is independent from ordinary command execution.
	terminalGrants := append(append([]authz.Grant{}, role.Grants...), authz.Grant{Action: "server.terminal", Scope: authz.Scope{Kind: authz.ScopeTeam}})
	role, e = repo.SaveRole(ctx, admin.Actor, team, role.ID, tenancy.RoleInput{Name: "Use selected server", ExpectedRevision: role.Revision, Pages: role.Pages, RequiresSelectedBinding: true, Grants: terminalGrants})
	if e != nil {
		t.Fatal(e)
	}
	terminal, e := svc.ExecInteractive(actor, a.ID, []string{"sh"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = terminal.Write([]byte("accepted")); e != nil {
		t.Fatal(e)
	}
	role, e = repo.SaveRole(ctx, admin.Actor, team, role.ID, tenancy.RoleInput{Name: "Use selected server", ExpectedRevision: role.Revision, Pages: role.Pages, RequiresSelectedBinding: true, Grants: terminalGrants[:len(terminalGrants)-1]})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = terminal.Write([]byte("denied")); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("revoked terminal write", e)
	}
	if e = terminal.Resize(80, 24); e == nil {
		t.Fatal("revoked resize accepted")
	}
	if n, e := terminal.Read(make([]byte, 8)); e == nil || n != 0 {
		t.Fatal("revoked terminal output", n, e)
	}
	if e = terminal.Close(); e != nil {
		t.Fatal("cleanup after revocation", e)
	}
	containers := ForContainerAction(svc, "container.inspect")
	if _, e = containers.Get(actor, a.ID); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("server use implied container inspect", e)
	}
	containerGrants := append(append([]authz.Grant{}, role.Grants...), authz.Grant{Action: "container.inspect", Scope: authz.Scope{Kind: authz.ScopeTeam}}, authz.Grant{Action: "container.terminal", Scope: authz.Scope{Kind: authz.ScopeTeam}})
	role, e = repo.SaveRole(ctx, admin.Actor, team, role.ID, tenancy.RoleInput{Name: "Use selected server", ExpectedRevision: role.Revision, Pages: append(role.Pages, "page.containers"), RequiresSelectedBinding: true, Grants: containerGrants})
	if e != nil {
		t.Fatal(e)
	}
	containerRows, e := containers.List(actor)
	if e != nil || len(containerRows) != 1 || containerRows[0].ID != a.ID {
		t.Fatal("container parent SQL filter", containerRows, e)
	}
	dialer.gotAddr = ""
	if _, e = ForContainerAction(svc, "container.operate").Exec(actor, a.ID, []string{"docker", "rm", "app"}); !errors.Is(e, tenancy.ErrForbidden) || dialer.gotAddr != "" {
		t.Fatal("inspect implied mutation", e)
	}
	if CanRevealContainerConfig(actor, containers, a.ID) {
		t.Fatal("inspect implied sensitive config")
	}
	containerTerminal := ForContainerAction(svc, "container.terminal")
	if _, e = containerTerminal.ExecInteractive(actor, a.ID, []string{"sh"}); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("container grant became host shell", e)
	}
	shell, e := containerTerminal.ExecInteractive(actor, a.ID, []string{"docker", "exec", "-it", "app", "/bin/zsh"})
	if e != nil {
		t.Fatal("independent container shell", e)
	}
	if _, e = shell.Write([]byte("allowed container input")); e != nil {
		t.Fatal(e)
	}
	_ = shell.Close()
	if _, e = svc.ExecInteractive(actor, a.ID, []string{"sh"}); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("container grant implied host terminal", e)
	}
	owned, e := svc.(WorkspaceCreator).CreateWorkspace(actor, a.ID)
	if e != nil {
		t.Fatal("authorized workspace creation", e)
	}
	if e = accounts.Logout(ctx, login.Token); e != nil {
		t.Fatal(e)
	}
	dialer.gotAddr = ""
	if _, e = svc.Exec(actor, a.ID, []string{"uname", "-a"}); !errors.Is(e, tenancy.ErrForbidden) || dialer.gotAddr != "" {
		t.Fatal("revoked session executed", e)
	}
	if _, e = svc.(WorkspaceCreator).CreateWorkspace(actor, a.ID); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("revoked session created workspace", e)
	}
	if e = owned.Cleanup(context.WithoutCancel(actor)); e != nil {
		t.Fatal("fixed cleanup after revocation", e)
	}
	if len(dialer.gotCmd) != 6 || dialer.gotCmd[2] != cleanupWorkspaceScript || dialer.gotCmd[4]+"/work" != owned.Directory() {
		t.Fatal("cleanup was not confined to the owned directory")
	}
	dialer.gotAddr = ""
	if e = owned.Cleanup(ctx); e != nil || dialer.gotAddr != "" {
		t.Fatal("cleanup must be one-shot", e)
	}
}
