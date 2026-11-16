package pipeline

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"testing"
)

func TestPipelineTeamReadWriteAndReadonlyDefaults(t *testing.T) {
	db := testDB(t)
	first, second := seedProject(t, db), seedProject(t, db)
	ctx := context.Background()
	legacy := auth.NewService(db, nil)
	if err := legacy.Bootstrap("admin", "admin-pipeline-password"); err != nil {
		t.Fatal(err)
	}
	repo := tenancy.NewRepository(db)
	if _, err := repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true); err != nil {
		t.Fatal(err)
	}
	var team string
	if err := db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); err != nil {
		t.Fatal(err)
	}
	accounts := tenancy.NewAccounts(db)
	admin, err := accounts.Login(ctx, "admin", "admin-pipeline-password")
	if err != nil {
		t.Fatal(err)
	}
	role, err := repo.SaveRole(ctx, admin.Actor, team, "", tenancy.RoleInput{Name: "Pipeline reader", Pages: []string{"page.pipeline"}, Grants: []authz.Grant{{Action: "pipeline.read", Scope: authz.Scope{Kind: authz.ScopeTeam}}}, RequiresSelectedBinding: true})
	if err != nil {
		t.Fatal(err)
	}
	initial, err := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: "reader", DisplayName: "Reader", Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{first}}}}})
	if err != nil {
		t.Fatal(err)
	}
	login, err := accounts.Login(ctx, "reader", initial.Password)
	if err != nil {
		t.Fatal(err)
	}
	login, err = accounts.ChangePassword(ctx, login.Token, initial.Password, "reader-pipeline-password")
	if err != nil {
		t.Fatal(err)
	}
	userctx := tenancy.WithActor(ctx, login.Actor, team)
	svc := New(db)
	cfg, err := svc.Get(userctx, first)
	if err != nil || cfg == nil {
		t.Fatal(err)
	}
	var count int
	db.QueryRow(`SELECT COUNT(*) FROM pipeline_configs WHERE project_id=?`, first).Scan(&count)
	if count != 0 {
		t.Fatal("read-only request created configuration")
	}
	if _, err = svc.Get(userctx, second); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("hidden project", err)
	}
	if _, err = svc.Save(userctx, first, cfg.Spec); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("read-only write", err)
	}
	if _, err = svc.Save(tenancy.WithActor(ctx, admin.Actor, team), first, cfg.Spec); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Get(userctx, first); err != nil {
		t.Fatal(err)
	}
	settings := NewSettingsService(db, nil)
	if _, err = settings.Get(userctx, first); err != nil {
		t.Fatal("readonly settings", err)
	}
	db.QueryRow(`SELECT COUNT(*) FROM pipeline_settings WHERE project_id=?`, first).Scan(&count)
	if count != 0 {
		t.Fatal("readonly settings wrote defaults")
	}
	adminctx := tenancy.WithActor(ctx, admin.Actor, team)
	input := SettingsInput{Build: DefaultSettings().Build, Environments: []Environment{{ID: "external-env", Name: "Production", TargetServerIDs: []string{}, EnvVars: []BuildVar{}}}}
	if _, err = settings.Save(adminctx, first, input); err != nil {
		t.Fatal("settings save", err)
	}
	var environmentID, purpose string
	if err = db.QueryRow(`SELECT id,purpose FROM access_environments WHERE project_id=? AND name='Production'`, first).Scan(&environmentID, &purpose); err != nil || purpose != "other" {
		t.Fatal(purpose, err)
	}
	if err = repo.SetEnvironmentPurpose(ctx, admin.Actor, team, environmentID, "production"); err != nil {
		t.Fatal(err)
	}
	if _, err = settings.Save(adminctx, first, input); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT purpose FROM access_environments WHERE id=?`, environmentID).Scan(&purpose); err != nil || purpose != "production" {
		t.Fatal("configuration downgraded purpose", purpose, err)
	}
	var credential string
	if err = db.QueryRow(`SELECT credential_id FROM projects WHERE id=?`, first).Scan(&credential); err != nil {
		t.Fatal(err)
	}
	for _, input := range []any{map[string]any{"credentialId": credential}, map[string]any{"executor": `{"kind":"ssh","serverId":"hidden"}`}, map[string]any{"targetServerIds": []string{"hidden"}}} {
		if err = authorizeConfigReferences(userctx, db, input); !errors.Is(err, tenancy.ErrForbidden) {
			t.Fatal("resource reference bypass", err)
		}
	}
	if err = authorizeConfigReferences(tenancy.WithActor(ctx, admin.Actor, team), db, map[string]any{"credentialId": credential}); err != nil {
		t.Fatal("admin resource reference", err)
	}
	if err = accounts.Logout(ctx, login.Token); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Get(userctx, first); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("revoked session", err)
	}
}
