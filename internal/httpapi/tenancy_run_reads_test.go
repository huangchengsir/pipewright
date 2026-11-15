package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/ai"
	"github.com/huangchengsir/pipewright/internal/approval"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/kubernetes"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/promotion"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/runretry"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"github.com/huangchengsir/pipewright/internal/vault"
)

type tenantRunDiffer struct {
	ai.RunDiffer
	beforeReturn func()
	calls        int
}

func (d *tenantRunDiffer) DiffCommit(_ context.Context, _, _, _, _ string) (ai.RunDiff, string) {
	d.calls++
	if d.beforeReturn != nil {
		d.beforeReturn()
	}
	return ai.RunDiff{Available: true, Summary: "synthetic-allowed-run-diff"}, "synthetic-parent"
}

func TestTenantRunHTTPReadSurfaces(t *testing.T) {
	f := newTenantAuxFixture(t)
	ctx := context.Background()
	accounts := tenancy.NewAccounts(f.db)
	admin, err := accounts.Login(ctx, "admin", "auxiliary-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	member, err := accounts.Login(ctx, "auxmember", "auxiliary-member-password")
	if err != nil {
		t.Fatal(err)
	}
	var team, roleID string
	var revision uint64
	if err = f.db.QueryRow("SELECT default_team_id FROM access_control WHERE id=1").Scan(&team); err != nil {
		t.Fatal(err)
	}
	if err = f.db.QueryRow("SELECT id,revision FROM access_roles WHERE name='Auxiliary' AND tenant_id=?", team).Scan(&roleID, &revision); err != nil {
		t.Fatal(err)
	}
	scope := authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{"p"}}
	role := tenancy.RoleInput{Name: "Auxiliary", ExpectedRevision: revision, Pages: []string{"page.runs", "page.environments", "page.projects", "page.vault"},
		Grants: []authz.Grant{{Action: "run.read", Scope: scope}, {Action: "environment.read", Scope: authz.Scope{Kind: authz.ScopeSelected, ResourceIDs: []string{"test-run-env"}}}}}
	repo := tenancy.NewRepository(f.db)

	service := run.New(f.db)
	pool := run.NewWorkerPool(service)
	v := vault.New(f.db, testMasterKey())
	differ := &tenantRunDiffer{}
	retry := runretry.New(f.db, v)
	role.Grants = append(role.Grants, authz.Grant{Action: "project.read", Scope: scope},
		authz.Grant{Action: "credential.metadata", Scope: authz.Scope{Kind: authz.ScopeTeam}},
		authz.Grant{Action: "credential.use", Scope: authz.Scope{Kind: authz.ScopeTeam}})
	router := New(testWebFSAuth(), tenancy.NewAuthenticator(accounts, member.Actor.ModeEpoch),
		WithTenancyAccounts(accounts), WithRuns(service, pool), WithApprovals(approval.New(), approval.NewStore(f.db)),
		WithProjects(project.New(f.db, v, nil)), WithVault(v), WithRunDiff(differ), WithRunRetry(retry),
		WithPromotion(promotion.NewStore(f.db)), WithKubernetes(kubernetes.New(f.db, vault.New(f.db, testMasterKey())), nil))
	for _, item := range []struct{ id, project, createdAt string }{
		{"read-old", "p", "2026-11-01T00:00:00Z"}, {"read-new", "p", "2026-11-02T00:00:00Z"},
		{"read-outside", "outside", "2026-11-03T00:00:00Z"}, {"read-foreign", "foreign", "2026-11-04T00:00:00Z"},
	} {
		if _, err = f.db.Exec("INSERT INTO pipeline_runs(id,project_id,status,trigger_branch,trigger_commit,created_at) VALUES(?,?,'success',?,'synthetic-commit',?)", item.id, item.project, item.id, item.createdAt); err != nil {
			t.Fatal(err)
		}
		var owner any
		if item.id == "read-old" {
			owner = member.Actor.UserID
		}
		if _, err = f.db.Exec("INSERT INTO access_ownership(resource_kind,resource_id,tenant_id,project_id,owner_id) SELECT 'run',?,tenant_id,?,? FROM access_ownership WHERE resource_kind='project' AND resource_id=?", item.id, item.project, owner, item.project); err != nil {
			t.Fatal(err)
		}
		if _, err = service.AppendLog(ctx, item.id, "stdout", 0, "log-"+item.id); err != nil {
			t.Fatal(err)
		}
		if _, err = service.AddArtifact(ctx, run.Artifact{RunID: item.id, Type: "archive", Name: "artifact-" + item.id, Reference: "test-archive"}); err != nil {
			t.Fatal(err)
		}
		if _, err = service.SaveTestReport(ctx, run.TestReport{RunID: item.id, StageID: "test", StageName: "report-" + item.id, Total: 1, Passed: 1}); err != nil {
			t.Fatal(err)
		}
		if _, err = f.db.Exec("INSERT INTO run_approvals(id,run_id,stage_id,stage_name,created_at) VALUES(?,?,'gate',?,?)", "approval-"+item.id, item.id, "approval-"+item.id, item.createdAt); err != nil {
			t.Fatal(err)
		}
		if _, err = f.db.Exec("INSERT INTO run_promotions(id,project_id,source_run_id,target_environment,created_at) VALUES(?,?,?,'test',?)", "promotion-"+item.id, item.project, item.id, item.createdAt); err != nil {
			t.Fatal(err)
		}
	}
	for _, env := range []struct{ id, name string }{{"test-run-env", "test"}, {"production-run-env", "production"}} {
		if _, err = f.db.Exec("INSERT INTO access_environments(id,tenant_id,project_id,name,purpose,revision) VALUES(?,?,'p',?,?,1)", env.id, team, env.name, env.name); err != nil {
			t.Fatal(err)
		}
		if _, err = f.db.Exec("INSERT INTO access_ownership(resource_kind,resource_id,tenant_id,project_id,environment_id) VALUES('environment',?,?,'p',?)", env.id, team, env.id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = f.db.Exec("INSERT INTO run_promotions(id,project_id,source_run_id,target_environment,created_at) VALUES('promotion-private-production','p','read-new','production','2026-11-02T00:00:00Z')"); err != nil {
		t.Fatal(err)
	}
	updated, err := repo.SaveRole(ctx, admin.Actor, team, roleID, role)
	if err != nil {
		t.Fatal(err)
	}
	request := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("X-Pipewright-Team", team)
		req.AddCookie(&http.Cookie{Name: cookieSession, Value: member.Token})
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	checkList := func(t *testing.T, query, wantID string, wantCount int) {
		t.Helper()
		w := request("/api/runs?" + query)
		var body runListDTO
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil || body.Total != wantCount {
			t.Fatalf("run list: status=%d, total=%d want=%d", w.Code, body.Total, wantCount)
		}
		if wantID == "" && len(body.Items) != 0 || wantID != "" && (len(body.Items) != 1 || body.Items[0].ID != wantID) {
			t.Fatalf("run list returned unexpected IDs for %s", query)
		}
	}
	for _, item := range []struct {
		name, query, id string
		count           int
	}{
		{"first-page", "pageSize=1&page=1", "read-new", 2},
		{"second-page", "pageSize=1&page=2", "read-old", 2},
		{"past-last-page", "pageSize=1&page=3", "", 2},
		{"allowed-search", "q=read-old", "read-old", 1},
		{"denied-search", "q=read-foreign", "", 0},
		{"denied-project-filter", "projectId=outside", "", 0},
	} {
		t.Run("list/"+item.name, func(t *testing.T) { checkList(t, item.query, item.id, item.count) })
	}
	endpoints := []struct{ name, suffix, expected string }{
		{"detail", "", "read-new"},
		{"logs", "/logs", "log-read-new"},
		{"stream", "/events", "log-read-new"},
		{"artifact-metadata", "/artifacts", "artifact-read-new"},
		{"test-report", "/test-report", "report-read-new"},
		{"approval-history", "/approvals", "approval-read-new"},
		{"promotion-history", "/promotions", "promotion-read-new"},
		{"kubernetes-operations", "/kubernetes/operations", ""},
		{"kubernetes-releases", "/kubernetes/releases", ""},
	}
	checkRead := func(t *testing.T, id, suffix, expected string, allowed bool) {
		t.Helper()
		w := request("/api/runs/" + id + suffix)
		if allowed {
			if w.Code != 200 || !strings.Contains(w.Body.String(), expected) || strings.Contains(w.Body.String(), "promotion-private-production") {
				t.Fatalf("visible run endpoint: status=%d body=%q", w.Code, w.Body.String())
			}
			return
		}
		if (w.Code != 403 && w.Code != 404) || strings.Contains(w.Body.String(), id) || strings.Contains(w.Header().Get("Content-Type"), "text/event-stream") {
			t.Fatalf("unreadable run endpoint: status=%d content-type=%s", w.Code, w.Header().Get("Content-Type"))
		}
	}
	for _, item := range endpoints {
		t.Run(item.name, func(t *testing.T) {
			t.Run("selected", func(t *testing.T) { checkRead(t, "read-new", item.suffix, item.expected, true) })
			for _, id := range []string{"read-outside", "read-foreign", "missing"} {
				t.Run(id, func(t *testing.T) { checkRead(t, id, item.suffix, "", false) })
			}
		})
	}
	t.Run("commit-diff-return-rechecks-current-scope", func(t *testing.T) {
		w := request("/api/runs/read-new/diff")
		if w.Code != 200 || !strings.Contains(w.Body.String(), "synthetic-allowed-run-diff") || differ.calls != 1 {
			t.Fatalf("allowed commit diff: %d %s %d", w.Code, w.Body.String(), differ.calls)
		}
		for _, id := range []string{"read-outside", "read-foreign", "missing"} {
			checkRead(t, id, "/diff", "", false)
		}
		if differ.calls != 1 {
			t.Fatal("out-of-scope request accessed repository")
		}
		stable := append([]authz.Grant{}, role.Grants...)
		differ.beforeReturn = func() {
			role.ExpectedRevision = updated.Revision
			role.Grants = slices.DeleteFunc(append([]authz.Grant{}, stable...), func(g authz.Grant) bool { return g.Action == "credential.use" })
			var e error
			updated, e = repo.SaveRole(ctx, admin.Actor, team, roleID, role)
			if e != nil {
				t.Fatal(e)
			}
		}
		w = request("/api/runs/read-new/diff")
		if w.Code != 403 || strings.Contains(w.Body.String(), "synthetic-allowed-run-diff") || differ.calls != 2 {
			t.Fatalf("result retained live credential scope: %d %s %d", w.Code, w.Body.String(), differ.calls)
		}
	})

	t.Run("retry-context-attempts-and-request-owner", func(t *testing.T) {
		const runID = "read-new"
		snap := runretry.Snapshot{
			SchemaVersion: runretry.SchemaVersion, ProjectID: "p", RequestedRef: "main", CommitSHA: strings.Repeat("b", 40),
			Settings: map[string]any{"private": "synthetic-manifest-secret"},
			Spec:     pipeline.Spec{Stages: []pipeline.Stage{{ID: "build", Name: "Build", Jobs: []pipeline.Job{{ID: "step", Name: "Build", Type: "script"}}}}},
		}
		if e := retry.SaveManifest(ctx, runID, snap); e != nil {
			t.Fatal(e)
		}
		if _, e := retry.Prepare(ctx, runID); e != nil {
			t.Fatal(e)
		}
		s, _, e := tenancy.RetryCaller(tenancy.WithActor(ctx, member.Actor, team), f.db, "", "")
		if e != nil {
			t.Fatal(e)
		}
		for _, row := range []struct{ scope, id, initiator string }{{s, "request-own", member.Actor.UserID}, {"principal:anotherscope", "request-other", "fixture-someone-else"}} {
			if _, e = f.db.Exec(`INSERT INTO run_retry_requests(root_id,caller_scope,request_id,input_digest,source_attempt_id,attempt_id,state,created_at,initiator)
					VALUES (?,?,?,'synthetic',?,?,'accepted',0,?)`, runID, row.scope, row.id, runID, runID, row.initiator); e != nil {
				t.Fatal(e)
			}
		}
		base := "/api/runs/" + runID
		for _, ep := range []struct{ suffix, has string }{
			{"/retry-context", runID}, {"/attempts?page=1&pageSize=1", `"total":1`}, {"/retry-requests/request-own", member.Actor.UserID},
		} {
			w := request(base + ep.suffix)
			if w.Code != 200 || !strings.Contains(w.Body.String(), ep.has) || strings.Contains(w.Body.String(), "synthetic-manifest-secret") {
				t.Fatalf("selected endpoint %s: %d %s", ep.suffix, w.Code, w.Body.String())
			}
			if strings.HasPrefix(ep.suffix, "/retry-requests") {
				continue
			}
			for _, wrong := range []string{"read-outside", "read-foreign", "missing"} {
				checkRead(t, wrong, ep.suffix, "", false)
			}
		}
		for _, wrong := range []string{"request-other", "missing"} {
			w := request(base + "/retry-requests/" + wrong)
			if w.Code != 404 || strings.Contains(w.Body.String(), "fixture-someone-else") {
				t.Fatalf("other caller request visible: %d %s", w.Code, w.Body.String())
			}
		}
	})
	t.Run("project-promotion-history", func(t *testing.T) {
		for pid, count := range map[string]int{"p": 2, "outside": 0, "foreign": 0, "missing": 0} {
			w := request("/api/projects/" + pid + "/promotions")
			var out struct {
				Items []promotionDTO `json:"items"`
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || len(out.Items) != count || strings.Contains(w.Body.String(), "promotion-private-production") {
				t.Fatalf("scoped project promotion history for %s: %d %s", pid, w.Code, w.Body.String())
			}
			for _, item := range out.Items {
				if item.ProjectID != "p" || item.TargetEnvironment != "test" {
					t.Fatal("unexpected promotion item", item.ID)
				}
			}
		}
	})

	updateReadScope := func(scope authz.Scope) {
		t.Helper()
		role.ExpectedRevision = updated.Revision
		role.Grants = []authz.Grant{{Action: "run.read", Scope: scope}}
		var err error
		updated, err = repo.SaveRole(ctx, admin.Actor, team, roleID, role)
		if err != nil {
			t.Fatal(err)
		}
	}
	updateReadScope(authz.Scope{Kind: authz.ScopeTeam})
	t.Run("list/team-excludes-foreign", func(t *testing.T) {
		checkList(t, "pageSize=1&page=1", "read-outside", 3)
	})
	updateReadScope(authz.Scope{Kind: authz.ScopeOwn})
	t.Run("list/owner-only", func(t *testing.T) {
		checkList(t, "pageSize=1", "read-old", 1)
		checkRead(t, "read-old", "", "read-old", true)
		checkRead(t, "read-new", "", "", false)
	})
	updateReadScope(authz.Scope{Kind: authz.ScopeNone})
	t.Run("list/revoked-same-session", func(t *testing.T) { checkList(t, "pageSize=1", "", 0) })
	for _, item := range endpoints {
		t.Run(item.name+"/revoked-same-session", func(t *testing.T) { checkRead(t, "read-new", item.suffix, "", false) })
	}
}

func TestTenantRunCancelRequiresSelectedAction(t *testing.T) {
	f := newTenantAuxFixture(t)
	ctx := context.Background()
	accounts := tenancy.NewAccounts(f.db)
	admin, err := accounts.Login(ctx, "admin", "auxiliary-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	member, err := accounts.Login(ctx, "auxmember", "auxiliary-member-password")
	if err != nil {
		t.Fatal(err)
	}
	var team, roleID string
	var revision uint64
	if err = f.db.QueryRow("SELECT default_team_id FROM access_control WHERE id=1").Scan(&team); err != nil {
		t.Fatal(err)
	}
	if err = f.db.QueryRow("SELECT id,revision FROM access_roles WHERE tenant_id=? AND name='Auxiliary'", team).Scan(&roleID, &revision); err != nil {
		t.Fatal(err)
	}
	for id, project := range map[string]string{"cancel-selected": "p", "cancel-after-revocation": "p", "cancel-outside": "outside", "cancel-foreign": "foreign"} {
		if _, err = f.db.Exec("INSERT INTO pipeline_runs(id,project_id,status,created_at) VALUES(?,?,'queued','2026-11-15T00:00:00Z')", id, project); err != nil {
			t.Fatal(err)
		}
		if _, err = f.db.Exec("INSERT INTO access_ownership(resource_kind,resource_id,tenant_id,project_id) SELECT 'run',?,tenant_id,? FROM access_ownership WHERE resource_kind='project' AND resource_id=?", id, project, project); err != nil {
			t.Fatal(err)
		}
	}
	repo := tenancy.NewRepository(f.db)
	save := func(action string) {
		t.Helper()
		read := authz.Scope{Kind: authz.ScopeTeam}
		grants := []authz.Grant{{Action: "run.read", Scope: read}}
		if action != "" {
			grants = append(grants, authz.Grant{Action: action, Scope: authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{"p"}}})
		}
		role, err := repo.SaveRole(ctx, admin.Actor, team, roleID, tenancy.RoleInput{
			Name: "Auxiliary", ExpectedRevision: revision, Pages: []string{"page.runs"}, Grants: grants,
		})
		if err != nil {
			t.Fatal(err)
		}
		revision = role.Revision
	}
	save("")
	runs := run.New(f.db)
	router := New(testWebFSAuth(), tenancy.NewAuthenticator(accounts, member.Actor.ModeEpoch),
		WithTenancyAccounts(accounts), WithRuns(runs, run.NewWorkerPool(runs)), WithRunRetry(runretry.New(f.db, nil)))
	request := func(method, path, payload string, csrf bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(payload))
		req.Header.Set("X-Pipewright-Team", team)
		req.AddCookie(&http.Cookie{Name: cookieSession, Value: member.Token})
		if csrf {
			req.Header.Set(headerCsrf, member.CSRFToken)
			req.AddCookie(&http.Cookie{Name: cookieCsrf, Value: member.CSRFToken})
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	assertCancel := func(t *testing.T, id string, csrf bool, wantCode int, wantStatus string) {
		t.Helper()
		w := request(http.MethodPost, "/api/runs/"+id+"/cancel", "", csrf)
		if w.Code != wantCode {
			t.Fatalf("cancel %s: status=%d want=%d body=%s", id, w.Code, wantCode, w.Body.String())
		}
		if wantStatus == "" {
			return
		}
		var actual string
		if err := f.db.QueryRow("SELECT status FROM pipeline_runs WHERE id=?", id).Scan(&actual); err != nil || actual != wantStatus {
			t.Fatalf("cancel %s persisted %q, want %q: %v", id, actual, wantStatus, err)
		}
	}
	t.Run("reader-cannot-cancel", func(t *testing.T) { assertCancel(t, "cancel-selected", true, http.StatusForbidden, run.StatusQueued) })
	save("run.cancel")
	t.Run("selected-missing-csrf", func(t *testing.T) { assertCancel(t, "cancel-selected", false, http.StatusForbidden, run.StatusQueued) })
	t.Run("selected-cancel", func(t *testing.T) { assertCancel(t, "cancel-selected", true, http.StatusOK, run.StatusFailed) })
	t.Run("selected-terminal", func(t *testing.T) { assertCancel(t, "cancel-selected", true, http.StatusConflict, run.StatusFailed) })
	t.Run("same-team-outside-selection", func(t *testing.T) { assertCancel(t, "cancel-outside", true, http.StatusForbidden, run.StatusQueued) })
	t.Run("other-team", func(t *testing.T) { assertCancel(t, "cancel-foreign", true, http.StatusForbidden, run.StatusQueued) })
	t.Run("unknown-run", func(t *testing.T) { assertCancel(t, "unknown", true, http.StatusForbidden, "") })
	save("")
	t.Run("revoked-same-session", func(t *testing.T) {
		assertCancel(t, "cancel-after-revocation", true, http.StatusForbidden, run.StatusQueued)
	})
	node := strings.Repeat("a", 64)
	retryEndpoints := []struct {
		name, method, suffix, payload, marker string
		status                                int
	}{
		{"plan-get", http.MethodGet, "/retry-plan?nodeId=" + node, "", "manifest_missing", http.StatusOK},
		{"plan-post", http.MethodPost, "/retry-plan", "{\"targetNodeIds\":[\"" + node + "\"]}", "manifest_missing", http.StatusOK},
		{"submit", http.MethodPost, "/retry", "{\"requestId\":\"fixture-retry\",\"baseAttemptId\":\"cancel-selected\",\"baseRevision\":1,\"planFingerprint\":\"" + node + "\",\"targetNodeIds\":[\"" + node + "\"]}", "retry_evidence_unavailable", http.StatusUnprocessableEntity},
		{"reconcile", http.MethodPost, "/retry-reconcile", "{}", "retry_evidence_unavailable", http.StatusUnprocessableEntity},
	}
	for _, scenario := range []struct{ name, action string }{
		{"read-only", ""}, {"granted", "run.retry"}, {"revoked", ""},
	} {
		save(scenario.action)
		for _, endpoint := range retryEndpoints {
			t.Run("retry/"+scenario.name+"/"+endpoint.name, func(t *testing.T) {
				try := func(id string, status int, marker string) {
					t.Helper()
					w := request(endpoint.method, "/api/runs/"+id+endpoint.suffix, endpoint.payload, true)
					if w.Code != status || !strings.Contains(w.Body.String(), marker) {
						t.Fatalf("retry %s: got %d want %d, body=%q", id, w.Code, status, w.Body.String())
					}
				}
				if scenario.action == "" {
					try("cancel-selected", http.StatusForbidden, "")
					return
				}
				try("cancel-selected", endpoint.status, endpoint.marker)
				try("cancel-outside", http.StatusForbidden, "")
				try("cancel-foreign", http.StatusNotFound, "run_not_found")
			})
		}
	}
	var attempts int
	if err := f.db.QueryRow("SELECT COUNT(*) FROM run_retry_requests").Scan(&attempts); err != nil || attempts != 0 {
		t.Fatalf("unexpected recovery requests persisted: %d %v", attempts, err)
	}
}

type manualAdmissionFixture struct{ preparations int }

func (p *manualAdmissionFixture) Prepare(context.Context, string, run.Trigger) (*run.PreparedExecution, error) {
	p.preparations++
	return &run.PreparedExecution{}, nil
}
func (*manualAdmissionFixture) Approve(context.Context, string, string, run.Trigger, *run.PreparedExecution) (string, []tenancy.ExecutionPermission, error) {
	return strings.Repeat("b", 64), nil, nil
}
func (*manualAdmissionFixture) Digest(context.Context, string) (string, error) {
	return strings.Repeat("b", 64), nil
}
func (*manualAdmissionFixture) Verify(context.Context, string) error { return nil }

type manualParametersFixture struct {
	run.ParameterService
	resolutions int
}

func (p *manualParametersFixture) Resolve(_ context.Context, _ string, in map[string]string) (map[string]string, error) {
	p.resolutions++
	return in, nil
}

func TestTenantManualRunAdmissionPrecedesParameters(t *testing.T) {
	f := newTenantAuxFixture(t)
	ctx := context.Background()
	accounts := tenancy.NewAccounts(f.db)
	admin, e := accounts.Login(ctx, "admin", "auxiliary-admin-password")
	if e != nil {
		t.Fatal(e)
	}
	employee, e := accounts.Login(ctx, "auxmember", "auxiliary-member-password")
	if e != nil {
		t.Fatal(e)
	}
	var team, roleID string
	var revision uint64
	if e = f.db.QueryRow("SELECT default_team_id FROM access_control WHERE id=1").Scan(&team); e != nil {
		t.Fatal(e)
	}
	if e = f.db.QueryRow("SELECT id,revision FROM access_roles WHERE tenant_id=? AND name='Auxiliary'", team).Scan(&roleID, &revision); e != nil {
		t.Fatal(e)
	}
	grant := func(start bool) {
		t.Helper()
		scope := authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{"p"}}
		grants := []authz.Grant{{Action: "run.read", Scope: scope}}
		if start {
			grants = append(grants, authz.Grant{Action: "run.start", Scope: scope})
		}
		role, e := tenancy.NewRepository(f.db).SaveRole(ctx, admin.Actor, team, roleID, tenancy.RoleInput{
			Name: "Auxiliary", ExpectedRevision: revision, Pages: []string{"page.runs"}, Grants: grants,
		})
		if e != nil {
			t.Fatal(e)
		}
		revision = role.Revision
	}
	params := &manualParametersFixture{}
	plan := &manualAdmissionFixture{}
	runs := run.New(f.db, run.WithExecutionAuthorizer(plan))
	router := New(testWebFSAuth(), tenancy.NewAuthenticator(accounts, employee.Actor.ModeEpoch), WithTenancyAccounts(accounts), WithRuns(runs, nil), WithParameters(params))
	request := func(projectID string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/projects/"+projectID+"/runs", strings.NewReader(`{"branch":"main","params":{"FIXTURE":"small"}}`))
		req.Header.Set("X-Pipewright-Team", team)
		req.Header.Set(headerCsrf, employee.CSRFToken)
		req.AddCookie(&http.Cookie{Name: cookieSession, Value: employee.Token})
		req.AddCookie(&http.Cookie{Name: cookieCsrf, Value: employee.CSRFToken})
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	grant(false)
	if w := request("p"); w.Code != 403 {
		t.Fatalf("read-only actor started run: %d %s", w.Code, w.Body.String())
	}
	grant(true)
	for _, id := range []string{"outside", "foreign", "missing"} {
		if w := request(id); w.Code != 403 {
			t.Fatalf("outside scope start: %d %s", w.Code, w.Body.String())
		}
	}
	if params.resolutions != 0 || plan.preparations != 0 {
		t.Fatal("unselected request queried parameters or prepared execution")
	}
	w := request("p")
	if w.Code != 201 {
		t.Fatalf("authorized starter: %d %s", w.Code, w.Body.String())
	}
	var runID, owner string
	if e = f.db.QueryRow("SELECT r.id,o.owner_id FROM pipeline_runs r JOIN access_ownership o ON o.resource_id=r.id AND o.resource_kind='run' WHERE r.project_id='p'").Scan(&runID, &owner); e != nil || owner != employee.Actor.UserID {
		t.Fatal("new run wrong ownership", e, owner)
	}
	grant(false)
	if w := request("p"); w.Code != 403 {
		t.Fatalf("revoked startup: %d %s", w.Code, w.Body.String())
	}
	var n int
	if e = f.db.QueryRow("SELECT COUNT(*) FROM pipeline_runs").Scan(&n); e != nil || n != 1 || params.resolutions != 1 || plan.preparations != 1 {
		t.Fatal("incorrect startup effects", e, n, params.resolutions, plan.preparations)
	}
}
