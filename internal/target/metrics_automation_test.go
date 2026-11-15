package target

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"github.com/huangchengsir/pipewright/internal/vault"
	"testing"
)

func TestSavedMetricsApprovalCannotExecuteArbitraryCommands(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	v := vault.New(db, testMasterKey())
	cred := newSSHCred(t, v, "fixture-monitor-password")
	dialer := &limitedCapture{}
	svc := New(db, v, dialer)
	server, e := svc.Create(ctx, CreateInput{Name: "metrics", Host: "192.0.2.1", User: "monitor", CredentialID: cred})
	if e != nil {
		t.Fatal(e)
	}
	if e = auth.NewService(db, nil).Bootstrap("admin", "metrics-admin-password"); e != nil {
		t.Fatal(e)
	}
	repo := tenancy.NewRepository(db)
	if _, e = repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true); e != nil {
		t.Fatal(e)
	}
	accounts := tenancy.NewAccounts(db)
	login, e := accounts.Login(ctx, "admin", "metrics-admin-password")
	if e != nil {
		t.Fatal(e)
	}
	var team string
	if e = db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); e != nil {
		t.Fatal(e)
	}
	admin := tenancy.WithActor(ctx, login.Actor, team)
	monitor := svc.(MetricsAutomationService)
	state, e := monitor.MetricsAuthorizationState(admin, server.ID)
	if e != nil || state != "required" {
		t.Fatal("legacy not silently approved", state, e)
	}
	visited := 0
	if e = monitor.CollectApproved(ctx, func(context.Context, string, Service) error { visited++; return nil }); e != nil || visited != 0 {
		t.Fatal(e, visited)
	}
	if _, e = svc.Update(admin, server.ID, UpdateInput{}); e != nil {
		t.Fatal(e)
	}
	state, e = monitor.MetricsAuthorizationState(admin, server.ID)
	if e != nil || state != "ready" {
		t.Fatal(state, e)
	}
	if e = accounts.Logout(ctx, login.Token); e != nil {
		t.Fatal(e)
	}
	var captured context.Context
	var fixed Service
	e = monitor.CollectApproved(ctx, func(current context.Context, id string, bounded Service) error {
		captured = current
		fixed = bounded
		visited++
		if id != server.ID {
			t.Fatal("different server")
		}
		if _, e = bounded.Exec(current, id, []string{"sh", "-c", "id"}); !errors.Is(e, tenancy.ErrForbidden) {
			t.Fatal("shell capability", e)
		}
		if _, e = svc.Exec(current, id, []string{"nproc"}); !errors.Is(e, tenancy.ErrForbidden) {
			t.Fatal("generic exec capability", e)
		}
		if _, e = bounded.Exec(current, "different", []string{"nproc"}); !errors.Is(e, tenancy.ErrForbidden) {
			t.Fatal("wrong resource", e)
		}
		_, e = bounded.Exec(current, id, []string{"nproc"})
		return e
	})
	if e != nil || visited != 1 || dialer.calls != 1 {
		t.Fatal(e, visited, dialer.calls)
	}
	if e = monitor.CollectApproved(ctx, func(context.Context, string, Service) error { return errors.New("synthetic storage failure") }); e == nil {
		t.Fatal("sample consumer failure silently successful")
	}
	if _, e = db.Exec(`UPDATE servers SET host='192.0.2.2' WHERE id=?`, server.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = fixed.Exec(captured, server.ID, []string{"nproc"}); e == nil || dialer.calls != 1 {
		t.Fatal("changed target executed", e, dialer.calls)
	}
	if e = monitor.CollectApproved(ctx, func(context.Context, string, Service) error { visited++; return nil }); e != nil || visited != 1 {
		t.Fatal("changed approval visited", e, visited)
	}
}
