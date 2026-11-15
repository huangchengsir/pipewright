package dbmigration

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/maintenance"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"os"
	"testing"
	"time"
)

func TestRevokedMigrationStartCannotIssueRecoveryOrChangeIntent(t *testing.T) {
	r, _ := probeRuntime(t)
	ctx := context.Background()
	db := r.Source.DB
	if err := auth.NewService(db, nil).Bootstrap("admin", "migration-permission-password"); err != nil {
		t.Fatal(err)
	}
	if _, err := tenancy.NewRepository(db).ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true); err != nil {
		t.Fatal(err)
	}
	accounts := tenancy.NewAccounts(db)
	login, err := accounts.Login(ctx, "admin", "migration-permission-password")
	if err != nil {
		t.Fatal(err)
	}
	var team string
	if err := db.QueryRow("SELECT default_team_id FROM access_control WHERE id=1").Scan(&team); err != nil {
		t.Fatal(err)
	}
	actorCtx := tenancy.WithActor(ctx, login.Actor, team)
	_, target := profiles()
	prepared, err := r.Control.Plan(r.Current, target)
	if err != nil {
		t.Fatal(err)
	}
	r.Restart = func() error { t.Error("denied request restarted"); return nil }
	if err := r.AuthorizeConfiguration(actorCtx); err != nil {
		t.Fatal("initial adapter authorization", err)
	}
	r.mu.Lock()
	result := make(chan error, 1)
	go func() {
		_, token, csrf, e := r.StartWithRecoverySession(actorCtx, prepared.TaskID, prepared.Revision)
		if token != "" || csrf != "" {
			result <- errors.New("denied request returned recovery credential")
			return
		}
		result <- e
	}()
	if err := accounts.Logout(ctx, login.Token); err != nil {
		r.mu.Unlock()
		t.Fatal(err)
	}
	r.mu.Unlock()
	if err := <-result; !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("revoked queued request", err)
	}
	after, err := r.Control.State()
	if err != nil || after.Phase != Prepared || after.Revision != prepared.Revision {
		t.Fatal("changed durable intent", after, err)
	}
	if _, err := r.Control.read("recovery"); !os.IsNotExist(err) {
		t.Fatal("created recovery record", err)
	}
	var fence string
	if err := db.QueryRow("SELECT task_id FROM selfupdate_control WHERE id=1").Scan(&fence); err != nil || fence != "" {
		t.Fatal("changed maintenance fence", fence, err)
	}
	// A fresh authorized operator can start, receive a valid restart bridge, and
	// abort during drain without ordinary new-work admission blocking cleanup.
	login, err = accounts.Login(ctx, "admin", "migration-permission-password")
	if err != nil {
		t.Fatal(err)
	}
	actorCtx = tenancy.WithActor(ctx, login.Actor, team)
	release, err := maintenance.Enter(ctx, db, "preexisting-work")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	started, token, csrf, err := r.StartWithRecoverySession(actorCtx, prepared.TaskID, prepared.Revision)
	if err != nil || started.Phase != Quiescing || token == "" || csrf == "" {
		t.Fatal("authorized maintenance", err)
	}
	if actual, err := r.Control.RecoverySession(token, time.Now()); err != nil || actual != csrf {
		t.Fatal("restart bridge", err)
	}
	aborted, err := r.Abort(actorCtx, started.TaskID, started.Revision)
	if err != nil || aborted.Phase != Aborted {
		t.Fatal("authorized abort while draining", err)
	}
	r.Close()

}
