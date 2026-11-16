package httpapi

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/approval"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestTenantApprovalHTTPAndPublicLinkBoundaries(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	for _, stmt := range []string{
		`INSERT INTO credentials(id,name,type,scope,ciphertext,masked_value,created_at,updated_at) VALUES('c','C','git_token','',X'00','m','now','now')`,
		`INSERT INTO projects(id,name,repo_url,default_branch,credential_id,created_at,updated_at) VALUES('p','P','https://example.invalid','main','c','now','now')`,
		`INSERT INTO pipeline_runs(id,project_id,status,created_at) VALUES('run','p','success','now')`,
	} {
		if _, e := st.DB.Exec(stmt); e != nil {
			t.Fatal(e)
		}
	}
	if e := auth.NewService(st.DB, nil).Bootstrap("admin", "administrator-approval-password"); e != nil {
		t.Fatal(e)
	}
	repo := tenancy.NewRepository(st.DB)
	if _, e := repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true); e != nil {
		t.Fatal(e)
	}
	var team string
	if e := st.DB.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); e != nil {
		t.Fatal(e)
	}
	accounts := tenancy.NewAccounts(st.DB)
	admin, e := accounts.Login(ctx, "admin", "administrator-approval-password")
	if e != nil {
		t.Fatal(e)
	}
	adminctx := tenancy.WithActor(ctx, admin.Actor, team)
	grants := []authz.Grant{{Action: "run.read", Scope: authz.Scope{Kind: authz.ScopeTeam}}, {Action: "run.approve", Scope: authz.Scope{Kind: authz.ScopeTeam}}}
	role, e := repo.SaveRole(ctx, admin.Actor, team, "", tenancy.RoleInput{Name: "Approver", Pages: []string{"page.runs"}, Grants: grants, RequiresSelectedBinding: true})
	if e != nil {
		t.Fatal(e)
	}
	create := func(name, project string) tenancy.Session {
		t.Helper()
		initial, e := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: name, DisplayName: name, Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{project}}}}})
		if e != nil {
			t.Fatal(e)
		}
		s, e := accounts.Login(ctx, name, initial.Password)
		if e != nil {
			t.Fatal(e)
		}
		s, e = accounts.ChangePassword(ctx, s.Token, initial.Password, name+"-approval-password")
		if e != nil {
			t.Fatal(e)
		}
		return s
	}
	first := create("first-approver", "p")
	_ = create("second-approver", "p")
	digest := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	e = configstate.WithWriteTxContext(adminctx, st.DB, func(tx context.Context, q configstate.DBTX) error {
		if _, e := configstate.LockProject(tx, q, st.DB, "p"); e != nil {
			return e
		}
		return tenancy.ApproveRun(tx, st.DB, "run", "p", digest, []tenancy.ExecutionPermission{{Action: "run.start", ResourceID: "run"}})
	})
	if e != nil {
		t.Fatal(e)
	}
	job := tenancy.RunContext(ctx, st.DB, "run", digest)
	job = tenancy.WithExecutionVerifier(job, func(c context.Context) error { return tenancy.CheckRunBoundary(c, st.DB, "run") })
	store := approval.NewStore(st.DB)
	coord := approval.New()
	if e = store.CreatePending(job, "run", "stage", "CI gate"); e != nil {
		t.Fatal(e)
	}
	ch := coord.Wait(approval.Key("run", "stage"))
	signer := approval.NewSigner([]byte("approval-test-key-is-not-a-real-secret"))
	router := New(testWebFSAuth(), tenancy.NewAuthenticator(accounts, admin.Actor.ModeEpoch), WithTenancyAccounts(accounts), WithApprovals(coord, store), WithApprovalLinks(signer))
	request := func(method, path, body string, login tenancy.Session, selectedTeam string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Pipewright-Team", selectedTeam)
		r.Header.Set(headerCsrf, login.CSRFToken)
		r.AddCookie(&http.Cookie{Name: cookieSession, Value: login.Token})
		r.AddCookie(&http.Cookie{Name: cookieCsrf, Value: login.CSRFToken})
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	r := request("GET", "/api/runs/run/approvals", "", first, team)
	if r.Code != 200 || !strings.Contains(r.Body.String(), "run.approve") {
		t.Fatal(r.Code, r.Body.String())
	}
	r = request("POST", "/api/runs/run/approve", `{"stageId":"stage","environmentId":"forged","actor":"admin"}`, first, team)
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	select {
	case d := <-ch:
		if d.Actor != first.Actor.UserID || !d.Approved {
			t.Fatal(d)
		}
	default:
		t.Fatal("not delivered")
	}
	if e = store.CreatePending(job, "run", "stage2", "CI gate"); e != nil {
		t.Fatal(e)
	}
	rejected := coord.Wait(approval.Key("run", "stage2"))
	token := signer.Sign("run", "stage2", time.Now().Add(time.Hour))
	req := httptest.NewRequest("GET", "/approvals?token="+url.QueryEscape(token), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != 303 || strings.Contains(w.Header().Get("Location"), token) || !coord.IsWaiting(approval.Key("run", "stage2")) {
		t.Fatal("link did not become safe locator", w.Code, w.Header().Get("Location"))
	}
	req = httptest.NewRequest("POST", "/approvals/act", strings.NewReader(url.Values{"token": {token}, "decision": {"approve"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != 403 || !coord.IsWaiting(approval.Key("run", "stage2")) {
		t.Fatal("public token approved", w.Code)
	}
	role, e = repo.SaveRole(ctx, admin.Actor, team, role.ID, tenancy.RoleInput{Name: "Approver", ExpectedRevision: role.Revision, Pages: role.Pages, Grants: grants[:1], RequiresSelectedBinding: true})
	if e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{"approve", "reject"} {
		r = request("POST", "/api/runs/run/"+path, `{"stageId":"stage2"}`, first, team)
		if r.Code != 403 {
			t.Fatal(path, r.Code, r.Body.String())
		}
	}
	other, e := repo.CreateTeamAs(ctx, admin.Actor, "other")
	if e != nil {
		t.Fatal(e)
	}
	r = request("GET", "/api/runs/run/approvals", "", first, other.ID)
	if r.Code != 403 {
		t.Fatal("other team", r.Code, r.Body.String())
	}
	var status string
	if e = st.DB.QueryRow(`SELECT status FROM run_approvals WHERE stage_id='stage2'`).Scan(&status); e != nil || status != "pending" {
		t.Fatal(status, e)
	}
	t.Run("restored-scope-rejects-and-persists", func(t *testing.T) {
		if _, err := repo.SaveRole(ctx, admin.Actor, team, role.ID, tenancy.RoleInput{
			Name: "Approver", ExpectedRevision: role.Revision, Pages: role.Pages,
			Grants: grants, RequiresSelectedBinding: true,
		}); err != nil {
			t.Fatal(err)
		}
		w := request("POST", "/api/runs/run/reject", "{\"stageId\":\"stage2\"}", first, team)
		if w.Code != http.StatusOK {
			t.Fatalf("reject response = %d %s", w.Code, w.Body.String())
		}
		select {
		case decision := <-rejected:
			if decision.Approved || decision.Actor != first.Actor.UserID {
				t.Fatalf("reject decision = %#v", decision)
			}
		default:
			t.Fatal("reject decision did not reach the waiting job")
		}
		var status, actor string
		if err := st.DB.QueryRow("SELECT status, decided_by FROM run_approvals WHERE stage_id='stage2'").Scan(&status, &actor); err != nil {
			t.Fatal(err)
		}
		if status != "rejected" || actor != first.Actor.UserID {
			t.Fatalf("persisted rejection = %q by %q", status, actor)
		}
	})
}
