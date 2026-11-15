package chain

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"testing"
)

func TestChainFixedApprovalSeparatesSourceAndDownstream(t *testing.T) {
	db := testDB(t)
	up := seedProject(t, db)
	down := seedProject(t, db)
	base := context.Background()
	if _, err := db.Exec(`INSERT INTO pipeline_runs(id,project_id,status,created_at) VALUES('source-run',?,'success','2026-01-01T00:00:00Z')`, up); err != nil {
		t.Fatal(err)
	}
	if err := auth.NewService(db, nil).Bootstrap("admin", "chain-admin-password"); err != nil {
		t.Fatal(err)
	}
	if _, err := tenancy.NewRepository(db).ReconcileMode(base, config.MultiTenantMode{Enabled: true, Explicit: true}, true); err != nil {
		t.Fatal(err)
	}
	accounts := tenancy.NewAccounts(db)
	login, err := accounts.Login(base, "admin", "chain-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	var team string
	if err = db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); err != nil {
		t.Fatal(err)
	}
	ctx := tenancy.WithActor(base, login.Actor, team)
	svc := NewService(db).(*service)
	target := Target{DownstreamProjectID: down, Branch: "main", Enabled: true}
	if _, err = svc.Save(ctx, up, []Target{target}); err != nil {
		t.Fatal(err)
	}
	child, err := svc.ChainContext(ctx, "source-run", up, target)
	if err != nil {
		t.Fatal(err)
	}
	if err = tenancy.CheckRunAdmission(child, db, down, "next"); err != nil {
		t.Fatal(err)
	}
	if err = tenancy.CheckRunAdmission(child, db, up, "wrong"); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("child allowed unrelated project", err)
	}
	if _, err = db.Exec(`UPDATE project_config_versions SET revision=revision+1 WHERE project_id=?`, up); err != nil {
		t.Fatal(err)
	}
	if err = tenancy.VerifyExecution(child); err != nil {
		t.Fatal("other scheduling metadata killed child", err)
	}
	if _, err = db.Exec(`UPDATE projects SET default_branch='changed' WHERE id=?`, up); err != nil {
		t.Fatal(err)
	}
	if err = tenancy.VerifyExecution(child); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("changed source retained chain", err)
	}
	if _, err = svc.Save(ctx, up, []Target{target}); err != nil {
		t.Fatal(err)
	}
	child, err = svc.ChainContext(ctx, "source-run", up, target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Save(ctx, up, nil); err != nil {
		t.Fatal(err)
	}
	if err = tenancy.VerifyExecution(child); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("deleted chain retained child", err)
	}
	if _, err = svc.ChainContext(base, "source-run", up, target); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("unowned source call", err)
	}
}
