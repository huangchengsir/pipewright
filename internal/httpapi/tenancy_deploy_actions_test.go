package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/deploy"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/tenancy"
)

func TestTenantDeployHTTPBatchReadsAndWriteAdmission(t *testing.T) {
	f := newTenantAuxFixture(t)
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := f.db.Exec(q, args...); e != nil {
			t.Fatal(e)
		}
	}
	accounts := tenancy.NewAccounts(f.db)
	employee, e := accounts.Login(ctx, "auxmember", "auxiliary-member-password")
	if e != nil {
		t.Fatal(e)
	}
	admin, e := accounts.Login(ctx, "admin", "auxiliary-admin-password")
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
	scope := authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{"p"}}
	role := tenancy.RoleInput{Name: "Auxiliary", ExpectedRevision: revision, Pages: []string{"page.runs", "page.environments"}, Grants: []authz.Grant{{Action: "run.read", Scope: scope}}}
	repo := tenancy.NewRepository(f.db)
	grant, e := repo.SaveRole(ctx, admin.Actor, team, roleID, role)
	if e != nil {
		t.Fatal(e)
	}
	for _, caseData := range []struct{ runID, project, batch string }{
		{"deploy-a", "p", "batch-a"}, {"deploy-other", "outside", "batch-other"}, {"deploy-foreign", "foreign", "batch-foreign"},
	} {
		exec(`INSERT INTO pipeline_runs(id,project_id,status,resolved_environment,created_at) VALUES(?,?,'success','test','2026-11-15T00:00:00Z')`, caseData.runID, caseData.project)
		exec(`INSERT INTO access_ownership(resource_kind,resource_id,tenant_id,project_id) SELECT 'run',?,tenant_id,? FROM access_ownership WHERE resource_kind='project' AND resource_id=?`, caseData.runID, caseData.project, caseData.project)
		exec(`INSERT INTO access_environments(id,tenant_id,project_id,name,purpose,revision) SELECT ?,tenant_id,?,'test','test',1 FROM access_ownership WHERE resource_kind='project' AND resource_id=?`, "environment-"+caseData.project, caseData.project, caseData.project)
		exec(`INSERT INTO deploy_batches(id,run_id,idempotency_key,request_hash,status,created_at,updated_at) VALUES(?,?,'key','hash','partial','2026-11-15T00:00:00Z','2026-11-15T00:00:00Z')`, caseData.batch, caseData.runID)
		exec(`INSERT INTO deploy_batch_items(id,batch_id,item_index,artifact_id,artifact_name,server_id,server_name,config_json,status,attempt)
     VALUES(?,?,1,'fixture-artifact','fixture-artifact','s1','fixture-server','{}','pending',0)`, "item-"+caseData.batch, caseData.batch)
	}
	runs := run.New(f.db)
	pool := run.NewWorkerPool(runs)
	service := deploy.New(nil, runs, deploy.WithBatchDB(f.db))
	router := New(testWebFSAuth(), tenancy.NewAuthenticator(accounts, employee.Actor.ModeEpoch), WithTenancyAccounts(accounts), WithRuns(runs, pool), WithDeploy(service))
	request := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Pipewright-Team", team)
		r.Header.Set(headerCsrf, employee.CSRFToken)
		r.AddCookie(&http.Cookie{Name: cookieSession, Value: employee.Token})
		r.AddCookie(&http.Cookie{Name: cookieCsrf, Value: employee.CSRFToken})
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	base := "/api/runs/deploy-a/deploy"
	t.Run("list-and-detail", func(t *testing.T) {
		w := request("GET", base+"/batches", "")
		var list struct {
			Batches []deploy.BatchResult `json:"batches"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &list) != nil || len(list.Batches) != 1 || list.Batches[0].ID != "batch-a" {
			t.Fatalf("selected list: %d %s", w.Code, w.Body.String())
		}
		w = request("GET", base+"/batches/batch-a", "")
		var batch deploy.BatchResult
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &batch) != nil || batch.RunID != "deploy-a" || len(batch.Items) != 1 || batch.Items[0].ID != "item-batch-a" {
			t.Fatalf("selected detail: %d %s", w.Code, w.Body.String())
		}
		for _, side := range []string{"deploy-other", "deploy-foreign", "missing"} {
			for _, ext := range []string{"/batches", "/batches/batch-a"} {
				w = request("GET", "/api/runs/"+side+"/deploy"+ext, "")
				if w.Code != 403 || strings.Contains(w.Body.String(), "fixture-server") {
					t.Fatalf("out-of-scope %s%s: %d %s", side, ext, w.Code, w.Body.String())
				}
			}
		}
		w = request("GET", base+"/batches/batch-other", "")
		if w.Code != 404 {
			t.Fatalf("wrong parent batch: %d %s", w.Code, w.Body.String())
		}
	})
	t.Run("read-only-operations-do-not-start-activity", func(t *testing.T) {
		for _, caseData := range []struct{ name, suffix string }{
			{"deploy", ""}, {"retry", "/retry"}, {"continue", "/continue"}, {"abort", "/abort"}, {"batch-create", "/batches"}, {"batch-retry", "/batches/batch-a/retry"}, {"batch-continue", "/batches/batch-a/continue"},
		} {
			t.Run(caseData.name, func(t *testing.T) {
				w := request("POST", base+caseData.suffix, "{}")
				if w.Code != 403 {
					t.Fatalf("expected scope denial for %s: %d %s", caseData.name, w.Code, w.Body.String())
				}
			})
		}
		var n, unchanged int
		if e := f.db.QueryRow("SELECT COUNT(*) FROM project_activity WHERE kind='deploy'").Scan(&n); e != nil || n != 0 {
			t.Fatal("read-only request created activity", e, n)
		}
		if e := f.db.QueryRow("SELECT COUNT(*) FROM deploy_batch_items WHERE status='pending' AND attempt=0").Scan(&unchanged); e != nil || unchanged != 3 {
			t.Fatal("read-only request changed batch", e, unchanged)
		}
	})
	role.ExpectedRevision, role.Grants = grant.Revision, nil
	if _, e = repo.SaveRole(ctx, admin.Actor, team, roleID, role); e != nil {
		t.Fatal(e)
	}
	t.Run("same-session-read-revoked", func(t *testing.T) {
		for _, ext := range []string{"/batches", "/batches/batch-a"} {
			if w := request("GET", base+ext, ""); w.Code != 403 || strings.Contains(w.Body.String(), "fixture-server") {
				t.Fatalf("revoked batch read: %d %s", w.Code, w.Body.String())
			}
		}
	})
}
