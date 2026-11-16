package httpapi

import (
	"context"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTenantAuditHTTPIdentityScopeAndRevocation(t *testing.T) {
	ctx := context.Background()
	db := storetest.OpenDB(t)
	if e := auth.NewService(db, nil).Bootstrap("admin", "audit-admin-password"); e != nil {
		t.Fatal(e)
	}
	repo := tenancy.NewRepository(db)
	mode, e := repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true)
	if e != nil {
		t.Fatal(e)
	}
	accounts := tenancy.NewAccounts(db)
	admin, e := accounts.Login(ctx, "admin", "audit-admin-password")
	if e != nil {
		t.Fatal(e)
	}
	var team string
	if e = db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); e != nil {
		t.Fatal(e)
	}
	foreign, e := repo.CreateTeamAs(ctx, admin.Actor, "Foreign")
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range []struct{ project, team string }{{"pa", team}, {"outside", team}, {"foreign", foreign.ID}} {
		if _, e = db.Exec(`INSERT INTO access_ownership(resource_kind,resource_id,tenant_id,owner_id,project_id) VALUES('project',?,?,?,?)`, v.project, v.team, admin.User.ID, v.project); e != nil {
			t.Fatal(e)
		}
	}
	role, e := repo.SaveRole(ctx, admin.Actor, team, "", tenancy.RoleInput{Name: "Audit reader", Pages: []string{"page.audit"}, Grants: []authz.Grant{{Action: "audit.read", Scope: authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{"pa"}}}}})
	if e != nil {
		t.Fatal(e)
	}
	initial, e := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: "auditor", DisplayName: "Auditor", Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
	if e != nil {
		t.Fatal(e)
	}
	login, e := accounts.Login(ctx, "auditor", initial.Password)
	if e != nil {
		t.Fatal(e)
	}
	login, e = accounts.ChangePassword(ctx, login.Token, initial.Password, "audit-reader-password")
	if e != nil {
		t.Fatal(e)
	}
	rec := audit.New(db, mask.NewMasker(), nil)
	for _, v := range []struct{ project, team string }{{"pa", team}, {"outside", team}, {"foreign", foreign.ID}} {
		if e = rec.Record(tenancy.WithActor(ctx, admin.Actor, v.team), audit.Entry{Actor: "forged", Action: "fixture", TargetType: "project", TargetID: v.project}); e != nil {
			t.Fatal(e)
		}
	}
	router := New(testWebFSAuth(), tenancy.NewAuthenticator(accounts, mode.Epoch), WithTenancyAccounts(accounts), WithAudit(rec))
	request := func(selected, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/api/audit?limit=1", nil)
		r.Header.Set("X-Pipewright-Team", selected)
		if token != "" {
			r.AddCookie(&http.Cookie{Name: cookieSession, Value: token})
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	w := request(team, login.Token)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var out auditListDTO
	if e = json.Unmarshal(w.Body.Bytes(), &out); e != nil {
		t.Fatal(e)
	}
	if len(out.Entries) != 1 || out.Entries[0].TargetID != "pa" || out.NextBefore != nil {
		t.Fatal("selected project filtering/paging", out)
	}
	w = request(foreign.ID, login.Token)
	if w.Code != 403 || strings.Contains(w.Body.String(), "fixture") {
		t.Fatal("forged team header", w.Code, w.Body.String())
	}
	w = request(team, "")
	if w.Code != 401 {
		t.Fatal("missing session", w.Code)
	}
	if _, e = repo.SaveRole(ctx, admin.Actor, team, role.ID, tenancy.RoleInput{ExpectedRevision: role.Revision, Name: "Audit reader", Pages: []string{"page.audit"}, Grants: []authz.Grant{}}); e != nil {
		t.Fatal(e)
	}
	w = request(team, login.Token)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if e = json.Unmarshal(w.Body.Bytes(), &out); e != nil || len(out.Entries) != 0 {
		t.Fatal("live role revocation", out, e)
	}
	if _, e = db.Exec(`UPDATE access_users SET enabled=0 WHERE id=?`, login.User.ID); e != nil {
		t.Fatal(e)
	}
	w = request(team, login.Token)
	if w.Code != 401 {
		t.Fatal("disabled cookie still authenticated", w.Code, w.Body.String())
	}
}
