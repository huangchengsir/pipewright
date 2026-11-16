package tenancy

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/authz"
	"testing"
	"time"
)

func TestObservationPrincipalFixedCeilingAndRevocation(t *testing.T) {
	accounts, r, admin := accountFixture(t)
	ctx := context.Background()
	team, err := r.CreateTeam(ctx, "Observation")
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`INSERT INTO credentials(id,name,type,scope,ciphertext,masked_value,created_at,updated_at) VALUES('c','C','ssh_password','',X'00','m','now','now')`,
		`INSERT INTO projects(id,name,repo_url,default_branch,credential_id,created_at,updated_at) VALUES('p','P','https://example.invalid','main','c','now','now')`,
		`INSERT INTO project_config_versions(project_id,revision) VALUES('p',1)`,
		`INSERT INTO pipeline_runs(id,project_id,status,created_at) VALUES('run','p','success','now')`,
		`INSERT INTO servers(id,name,host,port,user,credential_id,created_at,updated_at) VALUES('s','S','127.0.0.1',22,'worker','c','now','now')`,
	} {
		if _, err = r.db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	for _, ref := range []authz.ResourceRef{{Kind: "project", ID: "p"}, {Kind: "run", ID: "run"}, {Kind: "server", ID: "s"}, {Kind: "server", ID: "other"}, {Kind: "credential", ID: "c"}} {
		project := ""
		if ref.Kind == "run" || ref.Kind == "project" {
			project = "p"
		}
		if _, err = r.db.Exec(`INSERT INTO access_ownership(resource_kind,resource_id,tenant_id,project_id) VALUES(?,?,?,?)`, ref.Kind, ref.ID, team, project); err != nil {
			t.Fatal(err)
		}
	}
	grants := []authz.Grant{}
	for _, action := range []string{"guard.read", "guard.observe", "guard.delete", "server.inspect", "server.use", "credential.metadata", "credential.use"} {
		grants = append(grants, authz.Grant{Action: action, Scope: authz.Scope{Kind: authz.ScopeTeam}})
	}
	input := RoleInput{Name: "Observer", Pages: []string{"page.guard", "page.servers", "page.vault"}, Grants: grants}
	role, err := r.SaveRole(ctx, admin, team, "", input)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := accounts.Create(ctx, admin, CreateUserInput{Username: "observer", DisplayName: "Observer", Assignments: []MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
	if err != nil {
		t.Fatal(err)
	}
	login, err := accounts.Login(ctx, "observer", initial.Password)
	if err != nil {
		t.Fatal(err)
	}
	login, err = accounts.ChangePassword(ctx, login.Token, initial.Password, "observer-normal-password")
	if err != nil {
		t.Fatal(err)
	}
	observation, err := ObservationContext(WithActor(ctx, login.Actor, team), r.db, "p", "run", []string{"s"})
	if err != nil {
		t.Fatal(err)
	}
	if err = VerifyExecution(observation); err != nil {
		t.Fatal(err)
	}
	if err = accounts.Logout(ctx, login.Token); err != nil {
		t.Fatal(err)
	}
	if err = Check(observation, r.db, "server.use", "server", "s"); err != nil {
		t.Fatal("logout killed approved observation", err)
	}
	if err = Check(observation, r.db, "server.use", "server", "other"); !errors.Is(err, ErrForbidden) {
		t.Fatal("expanded server", err)
	}
	if err = Check(observation, r.db, "guard.delete", "run", "run"); !errors.Is(err, ErrForbidden) {
		t.Fatal("expanded action", err)
	}
	if err = CheckNew(observation, r.db, "server.create", "server", "new"); !errors.Is(err, ErrForbidden) {
		t.Fatal("creation", err)
	}
	if _, err = ObservationContext(observation, r.db, "p", "run", []string{"s"}); !errors.Is(err, ErrForbidden) {
		t.Fatal("delegation", err)
	}
	if _, err = r.CreateTeamAs(ctx, admin, "Unrelated"); err != nil {
		t.Fatal(err)
	}
	if err = VerifyExecution(observation); err != nil {
		t.Fatal("unrelated revision", err)
	}
	cap := observation.Value(observationKey{}).(*observationPrincipal)
	if remaining := time.Until(cap.expires); remaining > 35*time.Minute || remaining < 34*time.Minute {
		t.Fatal("fixed expiry outside authorized limit", remaining)
	}
	originalExpiry := cap.expires
	if err = VerifyExecution(observation); err != nil {
		t.Fatal(err)
	}
	if cap.expires != originalExpiry {
		t.Fatal("checking authority extended its expiry")
	}
	saved := cap.expires
	cap.expires = time.Now().Add(-time.Second)
	if err = VerifyExecution(observation); !errors.Is(err, ErrForbidden) {
		t.Fatal("expired", err)
	}
	cap.expires = saved
	for _, tc := range []struct{ change, restore string }{
		{`UPDATE project_config_versions SET revision=2 WHERE project_id='p'`, `UPDATE project_config_versions SET revision=1 WHERE project_id='p'`},
		{`UPDATE access_users SET enabled=0 WHERE username='observer'`, `UPDATE access_users SET enabled=1 WHERE username='observer'`},
		{`UPDATE access_control SET enabled=0`, `UPDATE access_control SET enabled=1`},
	} {
		if _, err = r.db.Exec(tc.change); err != nil {
			t.Fatal(err)
		}
		if err = VerifyExecution(observation); !errors.Is(err, ErrForbidden) {
			t.Fatal("revocation", tc.change, err)
		}
		if _, err = r.db.Exec(tc.restore); err != nil {
			t.Fatal(err)
		}
	}
	input.ExpectedRevision = role.Revision
	input.Grants = grants[:1]
	if _, err = r.SaveRole(ctx, admin, team, role.ID, input); err != nil {
		t.Fatal(err)
	}
	if err = VerifyExecution(observation); !errors.Is(err, ErrForbidden) {
		t.Fatal("revoked role", err)
	}
}
