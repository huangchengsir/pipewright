package tenancy

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/authz"
	"testing"
)

func TestSQLPolicyMatchesWholeGrantAndVersion(t *testing.T) {
	r, s := testRepository(t)
	ctx := context.Background()
	team, err := r.CreateTeam(ctx, "Team")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`UPDATE access_control SET enabled=1,ever_enabled=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	state, err := r.Mode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p := authz.Principal{ID: "alice", TenantID: team, Active: true, ModeEpoch: state.Epoch, Revision: state.Revision}
	read := authz.Role{ID: "reader", TenantID: team, Pages: []string{"page.runs"}, Grants: []authz.Grant{{Action: "run.read", Scope: authz.Scope{Kind: authz.ScopeTeam}}}}
	narrow := authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{"p1"}, EnvironmentIDs: []string{"test"}}
	publisher := authz.Role{ID: "publisher", TenantID: team, Pages: []string{"page.runs"}, Grants: []authz.Grant{{Action: "run.read", Scope: narrow}, {Action: "run.retry", Scope: narrow}}}
	service, err := authz.New(authz.Snapshot{CatalogVersion: authz.CatalogVersion, ModeEpoch: p.ModeEpoch, Revision: p.Revision, Roles: []authz.Role{read, publisher}, Bindings: []authz.Binding{{SubjectID: p.ID, RoleID: "reader", TenantID: team, Limit: authz.Scope{Kind: authz.ScopeTeam}}, {SubjectID: p.ID, RoleID: "publisher", TenantID: team, Limit: narrow}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`INSERT INTO access_environments(id,tenant_id,project_id,name,purpose,revision) VALUES('test',?,'p1','Test','test',1)`, team); err != nil {
		t.Fatal(err)
	}
	resources := []authz.Resource{{TenantID: team, Kind: "run", ID: "r1", ProjectID: "p1", EnvironmentID: "test", EnvironmentPurpose: "test"}, {TenantID: team, Kind: "run", ID: "r2", ProjectID: "p2", EnvironmentID: "test"}, {TenantID: team, Kind: "run", ID: "r3", ProjectID: "p1", EnvironmentID: "prod"}}
	for _, v := range resources {
		if _, err = s.DB.Exec(`INSERT INTO access_ownership(resource_kind,resource_id,tenant_id,project_id,environment_id) VALUES('run',?,?,?,?)`, v.ID, team, v.ProjectID, v.EnvironmentID); err != nil {
			t.Fatal(err)
		}
	}
	for _, action := range []string{"run.read", "run.retry"} {
		predicate, e := service.RootPredicate(p, action, "o.resource_id")
		if e != nil {
			t.Fatal(e)
		}
		var count int
		if e = s.DB.QueryRow(`SELECT COUNT(*) FROM access_ownership o WHERE o.resource_kind='run' AND `+predicate.SQL, predicate.Args...).Scan(&count); e != nil {
			t.Fatal(e)
		}
		expected := 0
		for _, v := range resources {
			yes, e := service.Allowed(p, action, v)
			if e != nil {
				t.Fatal(e)
			}
			if yes {
				expected++
			}
		}
		if count != expected {
			t.Fatalf("%s SQL count %d != evaluator %d", action, count, expected)
		}
	}
	predicate, err := service.RootPredicate(p, "run.retry", "o.resource_id")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`UPDATE access_control SET authz_revision=authz_revision+1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = s.DB.QueryRow(`SELECT COUNT(*) FROM access_ownership o WHERE `+predicate.SQL, predicate.Args...).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("stale snapshot leaked rows")
	}
	injected, err := service.RootPredicate(p, "run.read", "o.resource_id) OR 1=1 --")
	if err != nil || injected.SQL != "1=0" {
		t.Fatal("untrusted identifier accepted")
	}
}

func TestTypedResourceQueryAndCheckKindIdentity(t *testing.T) {
	accounts, r, admin := accountFixture(t)
	ctx := context.Background()
	team, err := r.CreateTeam(ctx, "Typed resources")
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"server", "credential", "project"} {
		if _, err = r.db.Exec(`INSERT INTO access_ownership(resource_kind,resource_id,tenant_id) VALUES(?,?,?)`, kind, "shared", team); err != nil {
			t.Fatal(err)
		}
	}
	role, err := r.SaveRole(ctx, admin, team, "", RoleInput{Name: "Specific server", Pages: []string{"page.servers", "page.vault", "page.projects"}, RequiresSelectedBinding: true, Grants: []authz.Grant{{Action: "server.inspect", Scope: authz.Scope{Kind: authz.ScopeTeam}}, {Action: "credential.metadata", Scope: authz.Scope{Kind: authz.ScopeTeam}}, {Action: "project.read", Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
	if err != nil {
		t.Fatal(err)
	}
	initial, err := accounts.Create(ctx, admin, CreateUserInput{Username: "typeduser", DisplayName: "Typed", Assignments: []MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeSelected, ResourceRefs: []authz.ResourceRef{{Kind: "server", ID: "shared"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	login, err := accounts.Login(ctx, "typeduser", initial.Password)
	if err != nil {
		t.Fatal(err)
	}
	login, err = accounts.ChangePassword(ctx, login.Token, initial.Password, "typed-user-password")
	if err != nil {
		t.Fatal(err)
	}
	scoped := WithActor(ctx, login.Actor, team)
	if err = Check(scoped, r.db, "server.inspect", "server", "shared"); err != nil {
		t.Fatal(err)
	}
	if err = Check(scoped, r.db, "credential.metadata", "credential", "shared"); err == nil {
		t.Fatal("typed binding crossed resource kind")
	}
	if err = Check(WithActor(ctx, admin, team), r.db, "project.read", "server", "shared"); err == nil {
		t.Fatal("Check accepted mismatched action kind")
	}
	predicate, err := QueryPredicate(scoped, r.db, "server.inspect", "o.resource_id")
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err = r.db.QueryRow(`SELECT COUNT(*) FROM access_ownership o WHERE o.resource_kind='server' AND `+predicate.SQL, predicate.Args...).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
}

func TestSQLPolicyIntersectsTransitiveReadDependencies(t *testing.T) {
	r, st := testRepository(t)
	ctx := context.Background()
	team, e := r.CreateTeam(ctx, "Nested")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = st.DB.Exec(`UPDATE access_control SET enabled=1,ever_enabled=1 WHERE id=1`); e != nil {
		t.Fatal(e)
	}
	state, e := r.Mode(ctx)
	if e != nil {
		t.Fatal(e)
	}
	p := authz.Principal{ID: "alice", TenantID: team, Active: true, ModeEpoch: state.Epoch, Revision: state.Revision}
	whole := authz.Scope{Kind: authz.ScopeTeam}
	narrow := authz.Scope{Kind: authz.ScopeSelected, ResourceIDs: []string{"server-a"}}
	role := authz.Role{ID: "operator", TenantID: team, Pages: []string{"page.servers", "page.containers", "page.anomalies"}, Grants: []authz.Grant{{Action: "server.inspect", Scope: narrow}, {Action: "proxy.read", Scope: whole}, {Action: "proxy.manage", Scope: whole}, {Action: "anomaly.read", Scope: whole}, {Action: "anomaly.manage", Scope: whole}}}
	service, e := authz.New(authz.Snapshot{CatalogVersion: authz.CatalogVersion, ModeEpoch: p.ModeEpoch, Revision: p.Revision, Roles: []authz.Role{role}, Bindings: []authz.Binding{{SubjectID: p.ID, RoleID: role.ID, TenantID: team, Limit: whole}}})
	if e != nil {
		t.Fatal(e)
	}
	for _, id := range []string{"server-a", "server-b"} {
		if _, e = st.DB.Exec(`INSERT INTO access_ownership(resource_kind,resource_id,tenant_id) VALUES('server',?,?)`, id, team); e != nil {
			t.Fatal(e)
		}
	}
	for _, action := range []string{"proxy.read", "proxy.manage", "anomaly.read", "anomaly.manage"} {
		predicate, e := service.RootPredicate(p, action, "o.resource_id")
		if e != nil {
			t.Fatal(e)
		}
		var count int
		if e = st.DB.QueryRow(`SELECT COUNT(*) FROM access_ownership o WHERE o.resource_kind='server' AND `+predicate.SQL, predicate.Args...).Scan(&count); e != nil || count != 1 {
			t.Fatal(action, count, e)
		}
		var id string
		if e = st.DB.QueryRow(`SELECT o.resource_id FROM access_ownership o WHERE o.resource_kind='server' AND `+predicate.SQL, predicate.Args...).Scan(&id); e != nil || id != "server-a" {
			t.Fatal(action, id, e)
		}
	}
}
