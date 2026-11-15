package tenancy

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"strings"
	"testing"
)

func TestRunPrincipalSurvivesLogoutButNotRevocationOrPlanChange(t *testing.T) {
	for _, diagnoseAtAdmission := range []bool{false, true} {
		t.Run(map[bool]string{false: "without-diagnosis", true: "with-diagnosis"}[diagnoseAtAdmission], func(t *testing.T) {
			accounts, r, admin := accountFixture(t)
			ctx := context.Background()
			team, err := r.CreateTeam(ctx, "Execution")
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
			role, err := r.SaveRole(ctx, admin, team, "", RoleInput{Name: "Runner", Pages: []string{"page.runs"}, Grants: []authz.Grant{{Action: "run.read", Scope: authz.Scope{Kind: authz.ScopeTeam}}, {Action: "run.start", Scope: authz.Scope{Kind: authz.ScopeTeam}}}, RequiresSelectedBinding: true})
			if err != nil {
				t.Fatal(err)
			}
			if diagnoseAtAdmission {
				role, err = r.SaveRole(ctx, admin, team, role.ID, RoleInput{Name: "Runner", ExpectedRevision: role.Revision, Pages: role.Pages, Grants: append(role.Grants, authz.Grant{Action: "run.diagnose", Scope: authz.Scope{Kind: authz.ScopeTeam}}), RequiresSelectedBinding: true})
				if err != nil {
					t.Fatal(err)
				}
			}
			initial, err := accounts.Create(ctx, admin, CreateUserInput{Username: "runner", DisplayName: "Runner", Assignments: []MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{"p"}}}}})
			if err != nil {
				t.Fatal(err)
			}
			login, err := accounts.Login(ctx, "runner", initial.Password)
			if err != nil {
				t.Fatal(err)
			}
			login, err = accounts.ChangePassword(ctx, login.Token, initial.Password, "runner-user-password")
			if err != nil {
				t.Fatal(err)
			}
			userctx := WithActor(ctx, login.Actor, team)
			preparation, err := PrepareRunContext(userctx, r.db, "p")
			if err != nil {
				t.Fatal(err)
			}
			if err = Check(preparation, r.db, "pipeline.read", "project", "p"); err != nil {
				t.Fatal("bounded preparation", err)
			}
			if err = Check(preparation, r.db, "pipeline.edit", "project", "p"); !errors.Is(err, ErrForbidden) {
				t.Fatal("preparation acquired edit", err)
			}
			digest := strings.Repeat("a", 64)
			err = configstate.WithWriteTxContext(userctx, r.db, func(txctx context.Context, q configstate.DBTX) error {
				if _, err := configstate.LockProject(txctx, q, r.db, "p"); err != nil {
					return err
				}
				if err := RecordOwnership(txctx, r.db, "run", "run", "p"); err != nil {
					return err
				}
				return ApproveRun(txctx, r.db, "run", "p", digest, []ExecutionPermission{{Action: "run.start", ResourceID: "run"}})
			})
			if err != nil {
				t.Fatal(err)
			}
			if err = Check(userctx, r.db, "pipeline.read", "project", "p"); !errors.Is(err, ErrForbidden) {
				t.Fatal("runner acquired config inspection", err)
			}
			job := RunContext(ctx, r.db, "run", digest)
			if err = Check(job, r.db, "pipeline.read", "project", "p"); err != nil {
				t.Fatal("approved worker cannot consume fixed inputs", err)
			}
			if err = Check(job, r.db, "pipeline.edit", "project", "p"); !errors.Is(err, ErrForbidden) {
				t.Fatal("execution gained config writes", err)
			}

			diagnoseErr := Check(job, r.db, "run.diagnose", "run", "run")
			if diagnoseAtAdmission && diagnoseErr != nil {
				t.Fatal("approved diagnostic ceiling missing", diagnoseErr)
			}
			if !diagnoseAtAdmission {
				if !errors.Is(diagnoseErr, ErrForbidden) {
					t.Fatal("implicit diagnosis", diagnoseErr)
				}
				role, err = r.SaveRole(ctx, admin, team, role.ID, RoleInput{Name: "Runner", ExpectedRevision: role.Revision, Pages: role.Pages, Grants: append(role.Grants, authz.Grant{Action: "run.diagnose", Scope: authz.Scope{Kind: authz.ScopeTeam}}), RequiresSelectedBinding: true})
				if err != nil {
					t.Fatal(err)
				}
				if err = Check(job, r.db, "run.diagnose", "run", "run"); !errors.Is(err, ErrForbidden) {
					t.Fatal("later role grant expanded existing job", err)
				}
			}
			if err = accounts.Logout(ctx, login.Token); err != nil {
				t.Fatal(err)
			}
			if err = CheckRunPermission(job, r.db, "run.start", "run", ""); err != nil {
				t.Fatal("logout killed approved execution", err)
			}
			if diagnoseAtAdmission {
				if err = Check(job, r.db, "run.diagnose", "run", "run"); err != nil {
					t.Fatal("logout killed approved auto diagnosis", err)
				}
			}
			if _, err = r.CreateTeamAs(ctx, admin, "Unrelated"); err != nil {
				t.Fatal(err)
			}
			if err = CheckRunPermission(job, r.db, "run.start", "run", ""); err != nil {
				t.Fatal("unrelated revision killed execution", err)
			}
			predicate, err := QueryPredicate(job, r.db, "run.read", "pr.id")
			if err != nil {
				t.Fatal(err)
			}
			var count int
			if err = r.db.QueryRow(`SELECT COUNT(*) FROM pipeline_runs pr WHERE `+predicate.SQL, predicate.Args...).Scan(&count); err != nil || count != 1 {
				t.Fatal(count, err)
			}
			if err = CheckRunPermission(job, r.db, "run.start", "other", ""); !errors.Is(err, ErrForbidden) {
				t.Fatal("expanded plan", err)
			}
			if err = CheckRunPermission(RunContext(ctx, r.db, "run", strings.Repeat("b", 64)), r.db, "run.start", "run", ""); !errors.Is(err, ErrForbidden) {
				t.Fatal("changed executable plan", err)
			}
			if _, err = r.SaveRole(ctx, admin, team, role.ID, RoleInput{Name: "Runner", ExpectedRevision: role.Revision, RequiresSelectedBinding: true, Pages: role.Pages, Grants: role.Grants[:1]}); err != nil {
				t.Fatal(err)
			}
			if err = CheckRunPermission(job, r.db, "run.start", "run", ""); !errors.Is(err, ErrForbidden) {
				t.Fatal("revocation ignored", err)
			}
			if err = Check(job, r.db, "run.diagnose", "run", "run"); !errors.Is(err, ErrForbidden) {
				t.Fatal("diagnostic revocation ignored", err)
			}
			if _, err = r.db.Exec(`UPDATE access_control SET enabled=0,mode_epoch=mode_epoch+1 WHERE id=1`); err != nil {
				t.Fatal(err)
			}
			if _, err = QueryPredicate(job, r.db, "run.read", "pr.id"); !errors.Is(err, ErrForbidden) {
				t.Fatal("mode disabled became unrestricted", err)
			}
		})
	}
}
