package selfupdate

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/releasesource"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestTenancyUpdateAdmissionAndRecovery(t *testing.T) {
	var calls atomic.Int32
	h := fixture(t, func(_ *harness, w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(503) })
	ctx := context.Background()
	accounts := tenancy.NewAccounts(h.db)
	if err := auth.NewService(h.db, nil).Bootstrap("admin", "update-admin-password"); err != nil {
		t.Fatal(err)
	}
	if _, err := tenancy.NewRepository(h.db).ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true); err != nil {
		t.Fatal(err)
	}
	admin, err := accounts.Login(ctx, "admin", "update-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	var team string
	if err := h.db.QueryRow("SELECT default_team_id FROM access_control WHERE id=1").Scan(&team); err != nil {
		t.Fatal(err)
	}
	adminCtx := tenancy.WithActor(ctx, admin.Actor, team)
	// Missing authenticated identity cannot read or start global maintenance.
	for _, fn := range []func() error{
		func() error { _, e := h.s.Get(ctx, "missing"); return e },
		func() error { _, e := h.s.Current(ctx); return e },
		func() error { _, e := h.s.ForTarget(ctx, "2.0.0"); return e },
		func() error { _, e := h.s.Submit(ctx, Request{RequestID: "denied", Target: "2.0.0"}); return e },
		func() error { _, e := h.s.Resume(ctx, "missing", "denied"); return e },
	} {
		if e := fn(); !errors.Is(e, tenancy.ErrForbidden) {
			t.Fatal("unauthenticated domain", e)
		}
	}
	if calls.Load() != 0 || h.installs.Load() != 0 {
		t.Fatal("denied side effect")
	}
	// An enclosing transaction cannot start filesystem/process work before commit.
	err = configstate.WithWriteTxContext(adminCtx, h.db, func(c context.Context, _ configstate.DBTX) error {
		_, e := h.s.Submit(c, Request{RequestID: "nested", Target: "2.0.0"})
		return e
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatal("nested admission", err)
	}
	// Revoke after entry preflight, before the authoritative acceptance transaction.
	h.s.opts.PreferredSource = func(context.Context) (releasesource.ID, error) {
		return releasesource.GitHub, accounts.Logout(ctx, admin.Token)
	}
	if _, err = h.s.Submit(adminCtx, Request{RequestID: "revoked-in-flight", Target: "2.0.0"}); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("revoked admission", err)
	}
	var n int
	if err = h.db.QueryRow("SELECT COUNT(*) FROM selfupdate_tasks").Scan(&n); err != nil || n != 0 || calls.Load() != 0 {
		t.Fatal("denied task or network", n, calls.Load(), err)
	}
	admin, err = accounts.Login(ctx, "admin", "update-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	adminCtx = tenancy.WithActor(ctx, admin.Actor, team)
	h.s.opts.PreferredSource = nil
	task, err := h.s.Submit(adminCtx, Request{RequestID: "authorized", Target: "2.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(8 * time.Second)
	for {
		task, err = h.s.Get(adminCtx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if task.State == "interrupted" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("worker did not settle", task.State)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if calls.Load() == 0 {
		t.Fatal("authorized request never ran")
	}
	if err = accounts.Logout(ctx, admin.Token); err != nil {
		t.Fatal(err)
	}
	before := calls.Load()
	if _, err = h.s.Resume(adminCtx, task.ID, "revoked-recovery"); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("revoked resume", err)
	}
	if _, err = h.s.Get(adminCtx, task.ID); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("revoked read", err)
	}
	if calls.Load() != before || h.installs.Load() != 0 {
		t.Fatal("revoked recovery side effects")
	}
}
