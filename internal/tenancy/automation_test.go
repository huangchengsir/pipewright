package tenancy

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"strings"
	"testing"
)

func TestAutomationFixedScopeLogoutReplacementAndRevocation(t *testing.T) {
	_, r, actor := accountFixture(t)
	team, err := r.CreateTeam(context.Background(), "Automation")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"s1", "s2"} {
		if _, err = r.db.Exec(`INSERT INTO access_ownership(resource_kind,resource_id,tenant_id,owner_id) VALUES('server',?,?,?)`, id, team, actor.UserID); err != nil {
			t.Fatal(err)
		}
	}
	ctx := WithActor(context.Background(), actor, team)
	digest := strings.Repeat("a", 64)
	approve := func(permissions []AutomationPermission) error {
		return configstate.WithWriteTxContext(ctx, r.db, func(txctx context.Context, _ configstate.DBTX) error {
			return ApproveAutomation(txctx, r.db, "anomaly_rule", "rule", "", digest, permissions)
		})
	}
	permissions := []AutomationPermission{{Action: "server.inspect", ResourceID: "s1"}}
	if err = approve(permissions); err != nil {
		t.Fatal(err)
	}
	current := digest
	load := func() (context.Context, error) {
		return AutomationContext(context.Background(), r.db, "anomaly_rule", "rule", digest, func(context.Context, configstate.DBTX) (string, error) { return current, nil })
	}
	job, err := load()
	if err != nil {
		t.Fatal(err)
	}
	if err = Check(job, r.db, "server.inspect", "server", "s1"); err != nil {
		t.Fatal(err)
	}
	if err = Check(job, r.db, "server.inspect", "server", "s2"); !errors.Is(err, ErrForbidden) {
		t.Fatal("ceiling", err)
	}
	if err = RequireInstanceAdministrator(job, r.db); !errors.Is(err, ErrForbidden) {
		t.Fatal("admin must not bypass ceiling", err)
	}
	if err = CheckNew(job, r.db, "server.create", "server", "new"); !errors.Is(err, ErrForbidden) {
		t.Fatal("create", err)
	}
	if err = configstate.WithWriteTxContext(job, r.db, func(tx context.Context, _ configstate.DBTX) error {
		return ApproveAutomation(tx, r.db, "anomaly_rule", "second", "", digest, permissions)
	}); !errors.Is(err, ErrForbidden) {
		t.Fatal("remint", err)
	}
	if err = approve(permissions); err != nil {
		t.Fatal(err)
	}
	if err = VerifyExecution(job); !errors.Is(err, ErrForbidden) {
		t.Fatal("replaced approval", err)
	}
	job, err = load()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.db.Exec(`DELETE FROM access_sessions WHERE token_hash=?`, actor.SessionHash); err != nil {
		t.Fatal(err)
	}
	if err = Check(job, r.db, "server.inspect", "server", "s1"); err != nil {
		t.Fatal("logout cancelled approved job", err)
	}
	current = strings.Repeat("b", 64)
	if err = VerifyExecution(job); !errors.Is(err, ErrForbidden) {
		t.Fatal("changed configuration", err)
	}
	current = digest
	if _, err = r.db.Exec(`UPDATE access_users SET security_generation=security_generation+1 WHERE id=?`, actor.UserID); err != nil {
		t.Fatal(err)
	}
	if err = VerifyExecution(job); !errors.Is(err, ErrForbidden) {
		t.Fatal("reset", err)
	}
}

func TestAutomationModeAndMissingApprovalFailClosed(t *testing.T) {
	_, r, actor := accountFixture(t)
	team, err := r.CreateTeam(context.Background(), "Automation")
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithActor(context.Background(), actor, team)
	digest := strings.Repeat("c", 64)
	if err = configstate.WithWriteTxContext(ctx, r.db, func(tx context.Context, _ configstate.DBTX) error {
		return ApproveAutomation(tx, r.db, "notification", "route", "", digest, []AutomationPermission{{Action: "system.manage", ResourceID: "instance"}})
	}); err != nil {
		t.Fatal(err)
	}
	load := func() (context.Context, error) {
		return AutomationContext(context.Background(), r.db, "notification", "route", digest, func(context.Context, configstate.DBTX) (string, error) { return digest, nil })
	}
	job, err := load()
	if err != nil {
		t.Fatal(err)
	}
	if err = RequireInstanceAdministrator(job, r.db); err != nil {
		t.Fatal(err)
	}
	if _, err = r.db.Exec(`UPDATE access_control SET enabled=0,mode_epoch=mode_epoch+1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err = QueryPredicate(job, r.db, "server.inspect", "s.id"); err == nil {
		t.Fatal("old principal became unrestricted")
	}
	if _, err = r.db.Exec(`UPDATE access_control SET enabled=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err = load(); err == nil {
		t.Fatal("epoch survived toggle")
	}
	if _, err = r.db.Exec(`DELETE FROM access_automation_authorizations`); err != nil {
		t.Fatal(err)
	}
	if _, err = load(); err == nil {
		t.Fatal("missing approval fallback")
	}
}

func TestAutomationRunRetainsExactParentApproval(t *testing.T) {
	_, r, actor := accountFixture(t)
	team, err := r.CreateTeam(context.Background(), "Automatic runs")
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{`INSERT INTO credentials(id,name,type,scope,ciphertext,masked_value,created_at,updated_at) VALUES('c','C','git_token','',X'00','m','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, `INSERT INTO projects(id,name,repo_url,default_branch,credential_id,created_at,updated_at) VALUES('p','P','https://example.invalid','main','c','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, `INSERT INTO pipeline_runs(id,project_id,status,created_at) VALUES('run','p','queued','2026-01-01T00:00:00Z')`} {
		if _, err = r.db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = r.db.Exec(`INSERT INTO access_ownership(resource_kind,resource_id,tenant_id,project_id) VALUES('project','p',?,'p')`, team); err != nil {
		t.Fatal(err)
	}
	ctx := WithActor(context.Background(), actor, team)
	digest := strings.Repeat("d", 64)
	approve := func() error {
		return configstate.WithWriteTxContext(ctx, r.db, func(c context.Context, q configstate.DBTX) error {
			if _, e := configstate.LockProject(c, q, r.db, "p"); e != nil {
				return e
			}
			return ApproveAutomation(c, r.db, "trigger", "p", "p", digest, []AutomationPermission{{Action: "run.start", ProjectID: "p"}})
		})
	}
	if err = approve(); err != nil {
		t.Fatal(err)
	}
	automatic, err := AutomationContext(context.Background(), r.db, "trigger", "p", digest, func(context.Context, configstate.DBTX) (string, error) { return digest, nil })
	if err != nil {
		t.Fatal(err)
	}
	err = configstate.WithWriteTxContext(automatic, r.db, func(c context.Context, _ configstate.DBTX) error {
		if _, e := configstate.LockProject(c, configstate.Queryer(c, r.db), r.db, "p"); e != nil {
			return e
		}
		if e := RecordOwnership(c, r.db, "run", "run", "p"); e != nil {
			return e
		}
		return ApproveRun(c, r.db, "run", "p", digest, []ExecutionPermission{{Action: "run.start", ResourceID: "run"}})
	})
	if err != nil {
		t.Fatal(err)
	}
	job := RunContext(context.Background(), r.db, "run", digest)
	if err = CheckRunPermission(job, r.db, "run.start", "run", ""); err != nil {
		t.Fatal(err)
	}
	if err = approve(); err != nil {
		t.Fatal(err)
	}
	if err = CheckRunPermission(job, r.db, "run.start", "run", ""); !errors.Is(err, ErrForbidden) {
		t.Fatal("replaced source resurrected admitted run", err)
	}
}
