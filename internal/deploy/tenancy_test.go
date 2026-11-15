package deploy

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"testing"
	"time"
)

func TestTenantDeployAdmissionBeforeActivityAndEffects(t *testing.T) {
	ctx := context.Background()
	db := storetest.OpenDB(t)
	for _, stmt := range []string{
		`INSERT INTO credentials(id,name,type,scope,ciphertext,masked_value,created_at,updated_at) VALUES('c','c','git_token','',X'00','x','now','now')`,
		`INSERT INTO projects(id,name,repo_url,default_branch,credential_id,created_at,updated_at) VALUES('p','p','https://example.invalid/repo','main','c','now','now')`,
		`INSERT INTO pipeline_runs(id,project_id,status,trigger_type,resolved_environment,created_at) VALUES('test-run','p','success','manual','test','now')`,
		`INSERT INTO pipeline_runs(id,project_id,status,trigger_type,resolved_environment,created_at) VALUES('prod-run','p','success','manual','prod','now')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := auth.NewService(db, nil).Bootstrap("admin", "admin-deploy-password"); err != nil {
		t.Fatal(err)
	}
	repo := tenancy.NewRepository(db)
	if _, err := repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true); err != nil {
		t.Fatal(err)
	}
	var team, env string
	if err := db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT id FROM access_environments WHERE project_id='p' AND name='test'`).Scan(&env); err != nil {
		t.Fatal(err)
	}
	accounts := tenancy.NewAccounts(db)
	admin, err := accounts.Login(ctx, "admin", "admin-deploy-password")
	if err != nil {
		t.Fatal(err)
	}
	scope := authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{"p"}, EnvironmentIDs: []string{env}}
	input := tenancy.RoleInput{Name: "Publisher", Pages: []string{"page.runs", "page.environments"}, Grants: []authz.Grant{{Action: "environment.read", Scope: scope}, {Action: "run.read", Scope: authz.Scope{Kind: authz.ScopeSelected, ResourceIDs: []string{"test-run"}}}}}
	role, err := repo.SaveRole(ctx, admin.Actor, team, "", input)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: "publisher", DisplayName: "Publisher", Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeTeam, EnvironmentIDs: []string{env}}}}})
	if err != nil {
		t.Fatal(err)
	}
	login, err := accounts.Login(ctx, "publisher", initial.Password)
	if err != nil {
		t.Fatal(err)
	}
	login, err = accounts.ChangePassword(ctx, login.Token, initial.Password, "publisher-deploy-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE pipeline_runs SET created_at=?`, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	actor := tenancy.WithActor(ctx, login.Actor, team)
	targets := &stubTarget{}
	s := New(targets, run.New(db), WithBatchDB(db)).(*service)
	calls := []struct {
		name string
		call func() error
	}{
		{"deploy", func() error {
			_, e := s.Deploy(actor, DeployInput{RunID: "test-run", ServerIDs: []string{"server"}})
			return e
		}},
		{"rollback", func() error { _, e := s.DeployRollback(actor, DeployInput{RunID: "test-run"}); return e }},
		{"retry", func() error { _, e := s.RetryFailed(actor, RetryInput{RunID: "test-run"}); return e }},
		{"continue", func() error { _, e := s.ContinueDeploy(actor, ContinueInput{RunID: "test-run"}); return e }},
		{"abort", func() error { _, e := s.AbortDeploy(actor, AbortInput{RunID: "test-run"}); return e }},
		{"batch", func() error { _, e := s.DeployBatch(actor, BatchInput{RunID: "test-run"}); return e }},
		{"batch-retry", func() error { _, e := s.RetryBatch(actor, "test-run", "batch"); return e }},
		{"batch-continue", func() error { _, e := s.ContinueBatch(actor, "test-run", "batch"); return e }},
		{"stage", func() error { _, e := s.DeployForStage(actor, "test-run", []string{"server"}, nil, ""); return e }},
	}
	for _, tc := range calls {
		t.Run(tc.name, func(t *testing.T) {
			if e := tc.call(); !errors.Is(e, tenancy.ErrForbidden) {
				t.Fatal("missing deployment action", e)
			}
		})
	}
	var n int
	if err = db.QueryRow(`SELECT COUNT(*) FROM project_activity WHERE kind='deploy'`).Scan(&n); err != nil || n != 0 || len(targets.calls) != 0 {
		t.Fatal("denied caused effects", n, targets.calls, err)
	}
	if _, err = s.ListBatches(actor, "prod-run"); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("batch read scope", err)
	}
	if _, err = s.GetBatch(actor, "prod-run", "fake"); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("batch detail scope", err)
	}
	input.ExpectedRevision = role.Revision
	input.Grants = append(input.Grants, authz.Grant{Action: "deployment.execute", Scope: scope})
	role, err = repo.SaveRole(ctx, admin.Actor, team, role.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.beginActivity(actor, "prod-run"); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("production escaped", err)
	}
	if _, err = s.Deploy(actor, DeployInput{RunID: "test-run", Config: map[string]string{"restartCommand": "arbitrary"}}); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("publisher overrode command", err)
	}
	admitted, done, err := s.beginActivity(actor, "test-run")
	if err != nil {
		t.Fatal("explicit test admission", err)
	}
	input.ExpectedRevision = role.Revision
	input.Grants = input.Grants[:2]
	if _, err = repo.SaveRole(ctx, admin.Actor, team, role.ID, input); err != nil {
		t.Fatal(err)
	}
	if err = tenancy.VerifyExecution(admitted); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("revoke before next effect", err)
	}
	done()
	if err = db.QueryRow(`SELECT COUNT(*) FROM project_activity WHERE kind='deploy' AND released_at IS NULL`).Scan(&n); err != nil || n != 0 {
		t.Fatal("trusted finalization could not release", n, err)
	}
	// A rollback-only role can read exactly its internal source summary/artifact,
	// not arbitrary runs, source logs or the download API.
	input.ExpectedRevision = role.Revision + 1
	input.Grants = []authz.Grant{{Action: "environment.read", Scope: scope}, {Action: "release.rollback", Scope: scope}}
	role, err = repo.SaveRole(ctx, admin.Actor, team, role.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO run_artifacts(id,run_id,type,name,reference,size_bytes,metadata_json,created_at) VALUES('art','test-run','archive','archive','payload',0,'',?)`, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO run_logs(id,run_id,seq,step_ordinal,stream,text,ts) VALUES('log','test-run',1,0,'stdout','private-log',?)`, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	source, finish, err := s.beginActivity(actor, "test-run", "release.rollback")
	if err != nil {
		t.Fatal(err)
	}
	realRuns := run.New(db)
	if _, err = realRuns.Get(actor, "test-run"); !errors.Is(err, run.ErrNotFound) {
		t.Fatal("ordinary run menu unexpectedly allowed", err)
	}
	if summary, err := realRuns.Get(source, "test-run"); err != nil || summary.ProjectID != "p" {
		t.Fatal("bounded summary", summary, err)
	}
	if _, err = realRuns.Get(source, "prod-run"); !errors.Is(err, run.ErrNotFound) {
		t.Fatal("other run readable", err)
	}
	if artifacts, err := realRuns.ListArtifacts(source, "test-run"); err != nil || len(artifacts) != 1 {
		t.Fatal("bounded artifacts", artifacts, err)
	}
	if logs, err := realRuns.GetLogs(source, "test-run", 0); err != nil || len(logs) != 0 {
		t.Fatal("metadata capability leaked logs", logs, err)
	}
	if err = tenancy.Check(source, db, "artifact.download", "run", "test-run"); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("metadata granted download", err)
	}
	finish()
	server := seedServer(t, targets, "rollback")
	results, err := s.DeployRollback(actor, DeployInput{RunID: "test-run", ArtifactID: "art", ServerIDs: []string{server.ID}})
	if err != nil || len(results) != 1 || results[0].Status != run.TargetSuccess {
		t.Fatal("real run service rollback integration", results, err)
	}

}
