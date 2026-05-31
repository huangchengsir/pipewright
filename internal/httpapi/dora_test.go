package httpapi

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/vault"
)

// setupDoraServer 构造带 auth + DORA 指标端点的测试 server。返回 server / client / csrf / DB。
func setupDoraServer(t *testing.T) (*httptest.Server, *http.Client, string, *sql.DB) {
	t.Helper()
	st := testStoreAuth(t)
	svc := auth.NewService(st.DB, nil)
	if err := svc.Bootstrap("admin", "testpass"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	v := vault.New(st.DB, testMasterKey())
	rsvc := run.New(st.DB)
	ms := run.NewMetricsService(rsvc)

	srv := httptest.NewServer(New(testWebFSAuth(), svc, WithVault(v), WithDoraMetrics(ms)))
	t.Cleanup(srv.Close)

	client := newTestClient(t)
	csrf := loginWithClient(t, client, srv.URL)
	return srv, client, csrf, st.DB
}

func TestDoraMetrics_RequiresAuth(t *testing.T) {
	st := testStoreAuth(t)
	svc := auth.NewService(st.DB, nil)
	if err := svc.Bootstrap("admin", "testpass"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	ms := run.NewMetricsService(run.New(st.DB))
	srv := httptest.NewServer(New(testWebFSAuth(), svc, WithDoraMetrics(ms)))
	t.Cleanup(srv.Close)

	// 无会话 cookie → 401。
	resp, err := http.Get(srv.URL + "/api/metrics/dora")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestDoraMetrics_Unconfigured503(t *testing.T) {
	st := testStoreAuth(t)
	svc := auth.NewService(st.DB, nil)
	if err := svc.Bootstrap("admin", "testpass"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	// 不注入 WithDoraMetrics → 503。
	srv := httptest.NewServer(New(testWebFSAuth(), svc))
	t.Cleanup(srv.Close)
	client := newTestClient(t)
	csrf := loginWithClient(t, client, srv.URL)

	resp := doJSON(t, client, http.MethodGet, srv.URL+"/api/metrics/dora", csrf, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

func TestDoraMetrics_EmptyOk(t *testing.T) {
	srv, client, csrf, _ := setupDoraServer(t)
	resp := doJSON(t, client, http.MethodGet, srv.URL+"/api/metrics/dora", csrf, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body doraResponseDTO
	raw, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, raw)
	}
	if body.Window != "30d" || body.WindowDays != 30 {
		t.Fatalf("default window = %q/%d, want 30d/30", body.Window, body.WindowDays)
	}
	if body.TotalDeployments != 0 {
		t.Fatalf("empty totalDeployments = %d, want 0", body.TotalDeployments)
	}
	if body.Metrics.DeploymentFrequency.Band != "none" {
		t.Fatalf("empty band = %q, want none", body.Metrics.DeploymentFrequency.Band)
	}
}

func TestDoraMetrics_AggregatesAndFilters(t *testing.T) {
	srv, client, csrf, db := setupDoraServer(t)

	projA := seedDoraProject(t, db, "acme")
	projB := seedDoraProject(t, db, "globex")

	base := time.Now().UTC().Add(-5 * 24 * time.Hour)
	fin := func(d time.Duration) *time.Time { tt := base.Add(d); return &tt }

	// projA: 2 成功 + 1 失败(终态)+ 1 running(忽略)。
	seedDoraRun(t, db, projA, run.StatusSuccess, base, fin(time.Hour))
	seedDoraRun(t, db, projA, run.StatusSuccess, base.Add(time.Hour), fin(2*time.Hour))
	seedDoraRun(t, db, projA, run.StatusFailed, base.Add(2*time.Hour), fin(3*time.Hour))
	seedDoraRun(t, db, projA, run.StatusRunning, base.Add(3*time.Hour), nil)
	// projB: 1 成功。
	seedDoraRun(t, db, projB, run.StatusSuccess, base, fin(time.Hour))

	// 全项目:3 成功 + 1 失败 = 4 部署。
	resp := doJSON(t, client, http.MethodGet, srv.URL+"/api/metrics/dora?window=30d", csrf, "")
	var all doraResponseDTO
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err := json.Unmarshal(raw, &all); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, raw)
	}
	if all.TotalDeployments != 4 || all.SuccessfulDeployments != 3 || all.FailedDeployments != 1 {
		t.Fatalf("all counts = %+v", all)
	}
	if all.ChangeFailureRate <= 0.24 || all.ChangeFailureRate >= 0.26 {
		t.Fatalf("CFR = %v, want ~0.25", all.ChangeFailureRate)
	}

	// 仅 projA:2 成功 + 1 失败 = 3 部署。
	resp2 := doJSON(t, client, http.MethodGet, srv.URL+"/api/metrics/dora?projectId="+projA, csrf, "")
	var a doraResponseDTO
	raw2, _ := io.ReadAll(resp2.Body)
	_ = resp2.Body.Close()
	if err := json.Unmarshal(raw2, &a); err != nil {
		t.Fatalf("unmarshal A: %v (%s)", err, raw2)
	}
	if a.TotalDeployments != 3 || a.SuccessfulDeployments != 2 || a.FailedDeployments != 1 {
		t.Fatalf("projA counts = %+v", a)
	}
	if a.ProjectID != projA {
		t.Fatalf("projectId echo = %q, want %q", a.ProjectID, projA)
	}
	if len(a.Trend) == 0 {
		t.Fatalf("expected non-empty trend")
	}
}

func TestDoraMetrics_InvalidWindow(t *testing.T) {
	srv, client, csrf, _ := setupDoraServer(t)
	for _, bad := range []string{"abc", "0d", "-5d", "9999d", "10x"} {
		resp := doJSON(t, client, http.MethodGet, srv.URL+"/api/metrics/dora?window="+bad, csrf, "")
		got := resp.StatusCode
		_ = resp.Body.Close()
		if got != http.StatusBadRequest {
			t.Fatalf("window=%q status = %d, want 400", bad, got)
		}
	}
}

// seedDoraProject 直接插一个项目(满足运行外键),返回 project id。
func seedDoraProject(t *testing.T, db *sql.DB, name string) string {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	credID := uuid.NewString()
	if _, err := db.Exec(
		`INSERT INTO credentials (id, name, type, scope, ciphertext, masked_value, created_at, updated_at)
		 VALUES (?, ?, 'git_token', '', X'00', 'm', ?, ?)`,
		credID, name+"-cred", now, now,
	); err != nil {
		t.Fatalf("seed credential: %v", err)
	}
	projID := uuid.NewString()
	if _, err := db.Exec(
		`INSERT INTO projects (id, name, repo_url, default_branch, credential_id, created_at, updated_at)
		 VALUES (?, ?, 'https://example.com/p.git', 'main', ?, ?, ?)`,
		projID, name, credID, now, now,
	); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return projID
}

// seedDoraRun 直接插一条 pipeline_runs(绕过状态机),便于构造 DORA 测试数据。
func seedDoraRun(t *testing.T, db *sql.DB, projID, status string, created time.Time, finished *time.Time) {
	t.Helper()
	var finishedVal any
	if finished != nil {
		finishedVal = finished.UTC().Format(time.RFC3339)
	}
	if _, err := db.Exec(
		`INSERT INTO pipeline_runs
		   (id, project_id, status, trigger_type, trigger_branch, trigger_commit, trigger_actor,
		    created_at, started_at, finished_at)
		 VALUES (?, ?, ?, 'manual', 'main', '', 'admin', ?, NULL, ?)`,
		uuid.NewString(), projID, status, created.UTC().Format(time.RFC3339), finishedVal,
	); err != nil {
		t.Fatalf("seed run: %v", err)
	}
}
