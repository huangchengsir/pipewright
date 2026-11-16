package anomaly

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"testing"
)

func TestRuleActionsRespectServerScopeAndRevocation(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	svc := New(db, nil).(*service)
	defer svc.Close()
	input := func(id string) CreateRuleInput {
		in := CreateRuleInput{Metric: MetricCPU, Operator: OperatorGreaterThan, Threshold: 80, Enabled: true}
		if id != "" {
			in.ServerID = &id
		}
		return in
	}
	seeded := map[string]*Rule{}
	for _, id := range []string{"", "s1", "s2", "s3"} {
		r, e := svc.CreateRule(ctx, input(id))
		if e != nil {
			t.Fatal(e)
		}
		seeded[id] = r
	}
	if e := auth.NewService(db, nil).Bootstrap("admin", "rule-admin-password"); e != nil {
		t.Fatal(e)
	}
	repo := tenancy.NewRepository(db)
	if _, e := repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true); e != nil {
		t.Fatal(e)
	}
	accounts := tenancy.NewAccounts(db)
	admin, e := accounts.Login(ctx, "admin", "rule-admin-password")
	if e != nil {
		t.Fatal(e)
	}
	var team string
	if e = db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); e != nil {
		t.Fatal(e)
	}
	other, e := repo.CreateTeamAs(ctx, admin.Actor, "Other")
	if e != nil {
		t.Fatal(e)
	}
	for _, s := range []struct{ id, team string }{{"s1", team}, {"s2", team}, {"s3", other.ID}} {
		if _, e = db.Exec(`INSERT INTO access_ownership(resource_kind,resource_id,tenant_id,owner_id) VALUES('server',?,?,?)`, s.id, s.team, admin.User.ID); e != nil {
			t.Fatal(e)
		}
	}
	grants := []authz.Grant{{Action: "server.inspect", Scope: authz.Scope{Kind: authz.ScopeTeam}}, {Action: "anomaly.read", Scope: authz.Scope{Kind: authz.ScopeSelected, ResourceIDs: []string{"s1", "s2"}}}, {Action: "anomaly.manage", Scope: authz.Scope{Kind: authz.ScopeSelected, ResourceIDs: []string{"s1"}}}}
	role, e := repo.SaveRole(ctx, admin.Actor, team, "", tenancy.RoleInput{Name: "Rule operator", Pages: []string{"page.anomalies", "page.servers"}, Grants: grants})
	if e != nil {
		t.Fatal(e)
	}
	initial, e := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: "rules", DisplayName: "Rules", Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
	if e != nil {
		t.Fatal(e)
	}
	login, e := accounts.Login(ctx, "rules", initial.Password)
	if e != nil {
		t.Fatal(e)
	}
	login, e = accounts.ChangePassword(ctx, login.Token, initial.Password, "rule-member-password")
	if e != nil {
		t.Fatal(e)
	}
	user := tenancy.WithActor(ctx, login.Actor, team)
	rows, e := svc.ListRules(user)
	if e != nil || len(rows) != 2 {
		t.Fatalf("scoped list: %d %v", len(rows), e)
	}
	for _, r := range rows {
		if r.ServerID == nil || (*r.ServerID != "s1" && *r.ServerID != "s2") {
			t.Fatal("leaked rule")
		}
	}
	count := func() int {
		var n int
		if e := db.QueryRow(`SELECT COUNT(*) FROM anomaly_rules`).Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	before := count()
	for _, id := range []string{"", "s2", "s3", "missing"} {
		if _, e = svc.CreateRule(user, input(id)); !errors.Is(e, tenancy.ErrForbidden) {
			t.Fatalf("create %s: %v", id, e)
		}
	}
	ai := input("s1")
	ai.AIAnalysisEnabled = boolPtr(true)
	if _, e = svc.CreateRule(user, ai); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("AI create without grant", e)
	}
	if count() != before {
		t.Fatal("denied create wrote data")
	}
	if _, e = svc.UpdateRule(user, seeded["s1"].ID, input("s2")); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("retarget beyond action scope", e)
	}
	if _, e = svc.UpdateRule(user, seeded["s2"].ID, input("s1")); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("source requires manage too", e)
	}
	if _, e = svc.UpdateRule(user, seeded["s1"].ID, ai); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("AI enable", e)
	}
	for _, id := range []string{"", "s3"} {
		if e = svc.DeleteRule(user, seeded[id].ID); !errors.Is(e, ErrNotFound) {
			t.Fatal("hidden rule deletion", id, e)
		}
	}
	if e = svc.DeleteRule(user, seeded["s2"].ID); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("read is not delete", e)
	}
	fresh, e := svc.CreateRule(user, input(" s1 "))
	if e != nil {
		t.Fatal(e)
	}
	if fresh.ServerID == nil || *fresh.ServerID != "s1" {
		t.Fatal("stored identity differs from authorized identity")
	}
	changed := input("s1")
	changed.Threshold = 42
	if _, e = svc.UpdateRule(user, fresh.ID, changed); e != nil {
		t.Fatal(e)
	}
	// Revoking AI does not prevent an operator from stopping an existing analysis.
	adminCtx := tenancy.WithActor(ctx, admin.Actor, team)
	if _, e = svc.UpdateRule(adminCtx, fresh.ID, ai); e != nil {
		t.Fatal(e)
	}
	ai.Enabled = false
	if _, e = svc.UpdateRule(user, fresh.ID, ai); e != nil {
		t.Fatal("disable must remain possible", e)
	}
	if e = svc.DeleteRule(user, fresh.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = repo.SaveRole(ctx, admin.Actor, team, role.ID, tenancy.RoleInput{ExpectedRevision: role.Revision, Name: "Rule operator", Pages: []string{"page.anomalies", "page.servers"}, Grants: grants[:2]}); e != nil {
		t.Fatal(e)
	}
	if _, e = svc.CreateRule(user, input("s1")); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("revoked manage", e)
	}
	if e = accounts.Logout(ctx, login.Token); e != nil {
		t.Fatal(e)
	}
	if _, e = svc.ListRules(user); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("logged out read", e)
	}
}

func TestRuleCancellationWaitsForOuterCommit(t *testing.T) {
	for _, operation := range []string{"disable", "delete"} {
		for _, commit := range []bool{false, true} {
			t.Run(operation+map[bool]string{false: "Rollback", true: "Commit"}[commit], func(t *testing.T) {
				db := testDB(t)
				ctx := context.Background()
				s := New(db, nil).(*service)
				defer s.Close()
				in := CreateRuleInput{Metric: MetricCPU, Operator: OperatorGreaterThan, Threshold: 80, Enabled: true, AIAnalysisEnabled: boolPtr(true)}
				rule, e := s.CreateRule(ctx, in)
				if e != nil {
					t.Fatal(e)
				}
				canceled := false
				s.analysisJobs["test-job"] = analysisJob{ruleID: rule.ID, cancel: func() { canceled = true }}
				rollback := errors.New("rollback requested")
				e = configstate.WithWriteTxContext(ctx, db, func(c context.Context, _ configstate.DBTX) error {
					if operation == "delete" {
						if e := s.DeleteRule(c, rule.ID); e != nil {
							return e
						}
					} else {
						in.Enabled = false
						if _, e := s.UpdateRule(c, rule.ID, in); e != nil {
							return e
						}
					}
					if canceled {
						t.Fatal("analysis canceled before outer commit")
					}
					if !commit {
						return rollback
					}
					return nil
				})
				if commit && e != nil || !commit && !errors.Is(e, rollback) {
					t.Fatal(e)
				}
				if canceled != commit {
					t.Fatalf("canceled=%v commit=%v", canceled, commit)
				}
				saved, e := s.getRule(ctx, rule.ID)
				if !commit && (e != nil || !saved.Enabled) {
					t.Fatal("rollback did not preserve rule", e)
				}
				if commit && operation == "delete" && !errors.Is(e, ErrNotFound) {
					t.Fatal("delete not committed", e)
				}
				if commit && operation == "disable" && (e != nil || saved.Enabled) {
					t.Fatal("disable not committed", e)
				}
			})
		}
	}
}
