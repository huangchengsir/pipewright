package cron

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"testing"
)

func TestScheduledPrincipalSurvivesLogoutButNotSourceChange(t *testing.T) {
	db := testDB(t)
	project := seedProject(t, db)
	base := context.Background()
	if err := auth.NewService(db, nil).Bootstrap("admin", "cron-admin-password"); err != nil {
		t.Fatal(err)
	}
	repo := tenancy.NewRepository(db)
	if _, err := repo.ReconcileMode(base, config.MultiTenantMode{Enabled: true, Explicit: true}, true); err != nil {
		t.Fatal(err)
	}
	accounts := tenancy.NewAccounts(db)
	login, err := accounts.Login(base, "admin", "cron-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	var team string
	if err = db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); err != nil {
		t.Fatal(err)
	}
	ctx := tenancy.WithActor(base, login.Actor, team)
	svc := NewService(db).(*service)
	input := SaveInput{Expression: "* * * * *", Branch: "main", Enabled: true}
	if _, err = svc.Save(ctx, project, input); err != nil {
		t.Fatal(err)
	}
	entry := Entry{ProjectID: project, Expression: input.Expression, Branch: input.Branch}
	job, err := svc.ScheduledContext(base, entry)
	if err != nil {
		t.Fatal(err)
	}
	if err = tenancy.CheckRunAdmission(job, db, project, "new-run"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE project_config_versions SET revision=revision+1 WHERE project_id=?`, project); err != nil {
		t.Fatal(err)
	}
	if err = tenancy.VerifyExecution(job); err != nil {
		t.Fatal("unrelated source metadata revision invalidated schedule", err)
	}
	if err = accounts.Logout(base, login.Token); err != nil {
		t.Fatal(err)
	}
	if err = tenancy.VerifyExecution(job); err != nil {
		t.Fatal("logout revoked approved schedule", err)
	}
	if _, err = db.Exec(`UPDATE projects SET default_branch='changed' WHERE id=?`, project); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.ScheduledContext(base, entry); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("changed plan retained schedule approval", err)
	}
	login, err = accounts.Login(base, "admin", "cron-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	ctx = tenancy.WithActor(base, login.Actor, team)
	if _, err = svc.Save(ctx, project, input); err != nil {
		t.Fatal(err)
	}
	job, err = svc.ScheduledContext(base, entry)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE credentials SET ciphertext=? WHERE id=(SELECT credential_id FROM projects WHERE id=?)`, []byte{9}, project); err != nil {
		t.Fatal(err)
	}
	if err = tenancy.VerifyExecution(job); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("changed credential retained approval", err)
	}
	if _, err = svc.Save(ctx, project, input); err != nil {
		t.Fatal(err)
	}
	job, err = svc.ScheduledContext(base, entry)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Save(ctx, project, SaveInput{Expression: input.Expression, Branch: input.Branch}); err != nil {
		t.Fatal(err)
	}
	if err = tenancy.VerifyExecution(job); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("disabled source retained approval", err)
	}
	// Ordinary controller context cannot mutate configuration or manufacture approval.
	if _, err = svc.Save(base, project, input); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("anonymous config write", err)
	}
	if err = configstate.WithWriteTxContext(ctx, db, func(c context.Context, _ configstate.DBTX) error {
		return tenancy.RevokeAutomation(c, db, "cron", project)
	}); err != nil {
		t.Fatal(err)
	}
}
