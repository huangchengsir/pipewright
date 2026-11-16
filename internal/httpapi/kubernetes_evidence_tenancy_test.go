package httpapi

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/kubernetes"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"github.com/huangchengsir/pipewright/internal/vault"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTenantKubernetesEvidenceHTTPRejectsUnscopedRequests(t *testing.T) {
	f := newTenantAuxFixture(t)
	ctx := context.Background()
	accounts := tenancy.NewAccounts(f.db)
	login, err := accounts.Login(ctx, "auxmember", "auxiliary-member-password")
	if err != nil {
		t.Fatal(err)
	}
	var team string
	if err = f.db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); err != nil {
		t.Fatal(err)
	}
	service := kubernetes.New(f.db, vault.New(f.db, testMasterKey()))
	observer := &k8sTestObserver{}
	router := New(testWebFSAuth(), tenancy.NewAuthenticator(accounts, login.Actor.ModeEpoch), WithTenancyAccounts(accounts), WithKubernetes(service, observer))
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/runs/foreign/kubernetes/operations"},
		{"GET", "/api/runs/foreign/kubernetes/releases"},
		{"GET", "/api/runs/foreign/kubernetes/releases/krelease"},
		{"GET", "/api/runs/foreign/kubernetes/releases/krelease/rollback-requests/request"},
		{"POST", "/api/runs/foreign/kubernetes/operations/kop/reconcile"},
		{"POST", "/api/runs/foreign/kubernetes/operations/kop/retire"},
		{"POST", "/api/runs/foreign/kubernetes/releases/krelease/continue"},
		{"POST", "/api/runs/foreign/kubernetes/releases/krelease/reconcile"},
		{"POST", "/api/runs/foreign/kubernetes/releases/krelease/cleanup"},
		{"POST", "/api/runs/foreign/kubernetes/releases/krelease/rollback"},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{"expectedRevision":1,"planId":"plan"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Pipewright-Team", team)
			req.Header.Set(headerCsrf, login.CSRFToken)
			req.AddCookie(&http.Cookie{Name: cookieSession, Value: login.Token})
			req.AddCookie(&http.Cookie{Name: cookieCsrf, Value: login.CSRFToken})
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)
			if response.Code != 403 {
				t.Fatalf("want forbidden, got %d %s", response.Code, response.Body.String())
			}
		})
	}
	if observer.calls != 0 {
		t.Fatal("unauthorized request reached remote observer", observer.calls)
	}
}

func TestTenantKubernetesRecoveryRechecksRevocationAfterObservation(t *testing.T) {
	f := newK8sHTTP(t)
	ctx := context.Background()
	hash, err := auth.HashPassword("evidence-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.DB.Exec(`UPDATE admin_user SET password_hash=? WHERE id=1`, hash); err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.DB.Exec(`INSERT INTO pipeline_runs(id,project_id,status,trigger_type,trigger_branch,trigger_commit,trigger_actor,created_at) VALUES('run-1','project-1','success','manual','main','','admin','2026-11-13T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.DB.Exec(`UPDATE k8s_operations SET owner_fenced=1,business_state='failed' WHERE id=?`, f.op.ID); err != nil {
		t.Fatal(err)
	}
	repo := tenancy.NewRepository(f.db.DB)
	mode, err := repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true)
	if err != nil {
		t.Fatal(err)
	}
	accounts := tenancy.NewAccounts(f.db.DB)
	login, err := accounts.Login(ctx, "admin", "evidence-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	var team string
	if err = f.db.DB.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.DB.Exec(`INSERT INTO access_environments(id,tenant_id,project_id,name,purpose,revision) VALUES('env-test',?,'project-1','test','test',1)`, team); err != nil {
		t.Fatal(err)
	}
	router := New(testWebFSAuth(), tenancy.NewAuthenticator(accounts, mode.Epoch), WithTenancyAccounts(accounts), WithKubernetes(f.s, f.observer))
	request := func(method, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(`{"expectedRevision":1}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Pipewright-Team", team)
		req.Header.Set(headerCsrf, login.CSRFToken)
		req.AddCookie(&http.Cookie{Name: cookieSession, Value: login.Token})
		req.AddCookie(&http.Cookie{Name: cookieCsrf, Value: login.CSRFToken})
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	w := request("GET", "/api/runs/run-1/kubernetes/operations")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"canManage":true`) {
		t.Fatal("authorized management hint", w.Code, w.Body.String())
	}
	f.observer.beforeReturn = func() {
		if err := accounts.Logout(ctx, login.Token); err != nil {
			t.Error(err)
		}
	}
	w = request("POST", "/api/runs/run-1/kubernetes/operations/"+f.op.ID+"/reconcile")
	if w.Code != 401 || f.observer.calls != 1 {
		t.Fatal("revoked observation", w.Code, w.Body.String(), f.observer.calls)
	}
	current, err := f.s.GetOperation(ctx, f.op.ID)
	if err != nil || current.Revision != 1 || current.Business != kubernetes.Failed {
		t.Fatal("revoked observer changed evidence", current.Revision, current.Business, err)
	}
}
