package audit

import (
	"context"
	"database/sql"
	"errors"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"testing"
)

func tenancyAuditFixture(t *testing.T) (*sql.DB, Recorder, map[string]context.Context) {
	t.Helper()
	db := testDB(t)
	rec := New(db, mask.NewMasker(), nil)
	ctx := context.Background()
	if e := rec.Record(ctx, Entry{Actor: "legacy", Action: "legacy", TargetType: "instance"}); e != nil {
		t.Fatal(e)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := db.Exec(q, args...); e != nil {
			t.Fatal(e)
		}
	}
	for _, team := range []string{"ta", "tb"} {
		exec(`INSERT INTO access_teams(id,name,enabled,revision,created_at) VALUES(?,?,1,1,1)`, team, team)
	}
	contexts := map[string]context.Context{}
	for _, v := range []struct {
		id, team string
		admin    int
	}{{"alice", "ta", 0}, {"bob", "tb", 0}, {"other", "ta", 0}, {"root", "ta", 1}} {
		exec(`INSERT INTO access_users(id,username,username_key,display_name,enabled,instance_admin,must_change_password,security_generation,created_at,updated_at) VALUES(?,?,?,?,1,?,0,1,1,1)`, v.id, v.id, v.id, v.id, v.admin)
		exec(`INSERT INTO access_members(id,tenant_id,user_id,enabled,revision) VALUES(?,?,?,1,1)`, "m"+v.id, v.team, v.id)
		exec(`INSERT INTO access_sessions(token_hash,user_id,csrf_token,mode_epoch,security_generation,auth_source,created_at,expires_at,last_seen_at) VALUES(?,?,?,2,1,'password',1,4102444800,1)`, "session-"+v.id, v.id, "csrf")
		contexts[v.id] = tenancy.WithActor(ctx, tenancy.Actor{UserID: v.id, SessionHash: "session-" + v.id, ModeEpoch: 2, SecurityGeneration: 1}, v.team)
	}
	exec(`INSERT INTO access_control(id,enabled,ever_enabled,mode_epoch,authz_revision,catalog_version,bootstrap_user_id,default_team_id) VALUES(1,1,1,2,1,?,'root','ta')`, authz.CatalogVersion)
	for _, team := range []string{"ta", "tb"} {
		exec(`INSERT INTO access_roles(id,tenant_id,name,preset_key,seed_version,requires_selected_binding,deleted,revision,pages_json,grants_json) VALUES(?,?,?,'custom',1,0,0,1,?,?)`, "r"+team, team, team, `["page.audit"]`, `[{"action":"audit.read","scope":{"kind":"team"}}]`)
	}
	for _, v := range []struct{ id, team string }{{"alice", "ta"}, {"bob", "tb"}, {"other", "ta"}} {
		exec(`INSERT INTO access_bindings(id,tenant_id,member_id,role_id,scope_json) VALUES(?,?,?,?,?)`, "b"+v.id, v.team, "m"+v.id, "r"+v.team, `{"kind":"team"}`)
	}
	for _, v := range []struct{ p, team, user string }{{"pa", "ta", "alice"}, {"pb", "tb", "bob"}} {
		exec(`INSERT INTO access_ownership(resource_kind,resource_id,tenant_id,owner_id,project_id) VALUES('project',?,?,?,?)`, v.p, v.team, v.user, v.p)
	}
	return db, rec, contexts
}
func TestAuditTenancyRowsCursorOwnershipAndRevocation(t *testing.T) {
	db, rec, contexts := tenancyAuditFixture(t)
	record := func(user, project string) {
		t.Helper()
		if e := rec.Record(contexts[user], Entry{Actor: "caller-cannot-choose", Action: "changed", TargetType: "project", TargetID: project}); e != nil {
			t.Fatal(e)
		}
	}
	record("alice", "pa")
	record("bob", "pb")
	record("alice", "pa")
	record("other", "pa")
	list := func(user string, f ListFilter) *ListResult {
		t.Helper()
		r, e := rec.List(contexts[user], f)
		if e != nil {
			t.Fatal(e)
		}
		return r
	}
	a := list("alice", ListFilter{Limit: 1})
	if len(a.Entries) != 1 || a.Entries[0].TargetID != "pa" || a.NextBefore == "" {
		t.Fatal(a)
	}
	b := list("bob", ListFilter{})
	if len(b.Entries) != 1 || b.Entries[0].Actor != "user:bob" {
		t.Fatal(b)
	}
	forged := list("alice", ListFilter{Limit: 1, Before: b.Entries[0].ID})
	if forged.Entries[0].ID != a.Entries[0].ID {
		t.Fatal("foreign cursor affected visible page")
	}
	next := list("alice", ListFilter{Limit: 1, Before: a.NextBefore})
	if len(next.Entries) != 1 || next.Entries[0].ID == a.Entries[0].ID {
		t.Fatal("cursor failed")
	}
	if len(list("alice", ListFilter{Action: "legacy"}).Entries) != 0 {
		t.Fatal("legacy global audit leaked")
	}
	if len(list("root", ListFilter{Action: "legacy"}).Entries) != 1 {
		t.Fatal("instance admin lost legacy audit")
	}
	// History is attributed when written, not recomputed from a transferred target.
	if _, e := db.Exec(`UPDATE access_ownership SET tenant_id='tb',owner_id='bob' WHERE resource_kind='project' AND resource_id='pa'`); e != nil {
		t.Fatal(e)
	}
	if len(list("alice", ListFilter{}).Entries) != 3 || len(list("bob", ListFilter{}).Entries) != 1 {
		t.Fatal("historical ownership changed with target")
	}
	if _, e := db.Exec(`UPDATE access_roles SET grants_json=? WHERE id='rta'`, `[{"action":"audit.read","scope":{"kind":"own"}}]`); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Exec(`UPDATE access_control SET authz_revision=authz_revision+1 WHERE id=1`); e != nil {
		t.Fatal(e)
	}
	if len(list("alice", ListFilter{}).Entries) != 2 {
		t.Fatal("own filter included another employee")
	}
	if _, e := db.Exec(`UPDATE access_roles SET grants_json='[]' WHERE id='rta'`); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Exec(`UPDATE access_control SET authz_revision=authz_revision+1 WHERE id=1`); e != nil {
		t.Fatal(e)
	}
	if len(list("alice", ListFilter{}).Entries) != 0 {
		t.Fatal("revoked action still reads")
	}
	if _, e := db.Exec(`UPDATE access_users SET enabled=0 WHERE id='alice'`); e != nil {
		t.Fatal(e)
	}
	if _, e := rec.List(contexts["alice"], ListFilter{}); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("disabled identity", e)
	}
	if _, e := rec.List(context.Background(), ListFilter{}); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("missing identity", e)
	}
}
func TestAuditTenancyAtomicAppendAndPostRevocationAttribution(t *testing.T) {
	db, rec, contexts := tenancyAuditFixture(t)
	tx, e := db.BeginTx(context.Background(), nil)
	if e != nil {
		t.Fatal(e)
	}
	ctx := configstate.WithTxContext(contexts["alice"], db, tx)
	if e = AppendLocalTx(ctx, tx, mask.NewMasker(), Entry{Actor: "forged", Action: "rollback", TargetType: "project", TargetID: "pa"}); e != nil {
		tx.Rollback()
		t.Fatal(e)
	}
	if e = tx.Rollback(); e != nil {
		t.Fatal(e)
	}
	var count int
	if e = db.QueryRow(`SELECT COUNT(*) FROM access_ownership WHERE resource_kind='audit'`).Scan(&count); e != nil || count != 0 {
		t.Fatal("ownership escaped rollback", count, e)
	}
	if e = db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action='rollback'`).Scan(&count); e != nil || count != 0 {
		t.Fatal("audit escaped rollback", count, e)
	}
	if _, e = db.Exec(`UPDATE access_users SET enabled=0 WHERE id='alice'`); e != nil {
		t.Fatal(e)
	}
	if e = rec.Record(contexts["alice"], Entry{Actor: "forged", Action: "accepted-before-revocation", TargetType: "project", TargetID: "pa"}); e != nil {
		t.Fatal(e)
	}
	rows, e := rec.List(contexts["root"], ListFilter{Action: "accepted-before-revocation"})
	if e != nil || len(rows.Entries) != 1 || rows.Entries[0].Actor != "user:alice" {
		t.Fatal("revocation lost completed-operation evidence", rows, e)
	}
}

func TestAuditGlobalAndForeignTargetsStayInstanceOnly(t *testing.T) {
	_, rec, contexts := tenancyAuditFixture(t)
	for _, e := range []Entry{{Action: "global", TargetType: "account", TargetID: "root"}, {Action: "foreign", TargetType: "project", TargetID: "pb"}} {
		if err := rec.Record(contexts["alice"], e); err != nil {
			t.Fatal(err)
		}
	}
	got, e := rec.List(contexts["alice"], ListFilter{})
	if e != nil || len(got.Entries) != 0 {
		t.Fatal("global/foreign metadata leaked to selected team", got, e)
	}
	got, e = rec.List(contexts["root"], ListFilter{})
	if e != nil || len(got.Entries) != 3 {
		t.Fatal("global evidence unavailable to instance administrator", got, e)
	}
}

func TestProjectDeletionRetainsAuditOwnership(t *testing.T) {
	db, rec, contexts := tenancyAuditFixture(t)
	if _, e := db.Exec(`INSERT INTO credentials(id,name,type,scope,ciphertext,masked_value,created_at,updated_at) VALUES('fixture-credential','fixture','git_token','',`+store.BinaryPlaceholder(store.DialectOf(db))+`,'***','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, []byte("synthetic-unused-cipher")); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Exec(`INSERT INTO projects(id,name,repo_url,default_branch,credential_id,created_at,updated_at) VALUES('pa','fixture','https://invalid.example/repo','main','fixture-credential','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); e != nil {
		t.Fatal(e)
	}
	if e := rec.Record(contexts["root"], Entry{Action: "before-delete", TargetType: "project", TargetID: "pa"}); e != nil {
		t.Fatal(e)
	}
	if e := project.New(db, nil, nil).Delete(contexts["root"], "pa"); e != nil {
		t.Fatal("real project service delete", e)
	}
	if e := rec.Record(contexts["root"], Entry{Action: ActionProjectDelete, TargetType: "project", TargetID: "pa"}); e != nil {
		t.Fatal(e)
	}
	got, e := rec.List(contexts["alice"], ListFilter{})
	if e != nil || len(got.Entries) != 2 {
		t.Fatal("history disappeared after service deletion", got, e)
	}
	var n int
	if e = db.QueryRow(`SELECT COUNT(*) FROM access_ownership WHERE resource_kind='audit' AND project_id='pa'`).Scan(&n); e != nil || n != 2 {
		t.Fatal("audit project binding lost", n, e)
	}
	if _, e = db.Exec(`UPDATE audit_log SET actor='tampered' WHERE action='before-delete'`); e == nil {
		t.Fatal("history is not append-only")
	}
}
