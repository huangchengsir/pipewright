package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/deploy"
	"github.com/huangchengsir/pipewright/internal/environments"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/vault"
)

// stubDeploy 是一个记录入参的部署服务替身(回滚端点只需 Deploy 被以正确参数调用)。
type stubDeploy struct {
	lastInput deploy.DeployInput
	result    []deploy.TargetResult
}

func (s *stubDeploy) Deploy(_ context.Context, in deploy.DeployInput) ([]deploy.TargetResult, error) {
	s.lastInput = in
	now := time.Now().UTC()
	if s.result != nil {
		return s.result, nil
	}
	out := make([]deploy.TargetResult, 0, len(in.ServerIDs))
	for _, sid := range in.ServerIDs {
		out = append(out, deploy.TargetResult{ServerID: sid, ServerName: sid, Status: run.TargetSuccess, StartedAt: now, FinishedAt: &now})
	}
	return out, nil
}
func (s *stubDeploy) RetryFailed(context.Context, deploy.RetryInput) ([]deploy.TargetResult, error) {
	return nil, nil
}
func (s *stubDeploy) ContinueDeploy(context.Context, deploy.ContinueInput) ([]deploy.TargetResult, error) {
	return nil, nil
}
func (s *stubDeploy) AbortDeploy(context.Context, deploy.AbortInput) ([]deploy.TargetResult, error) {
	return nil, nil
}
func (s *stubDeploy) DeployForStage(context.Context, string, []string, map[string]string, string) ([]deploy.TargetResult, error) {
	return nil, nil
}

// setupEnvironmentsServer 构造带 auth + project + run + environments + (stub)deploy 的测试 server。
func setupEnvironmentsServer(t *testing.T) (*httptest.Server, *http.Client, string, string, run.Service, *stubDeploy) {
	t.Helper()
	st := testStoreAuth(t)
	svc := auth.NewService(st.DB, nil)
	if err := svc.Bootstrap("admin", "testpass"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	v := vault.New(st.DB, testMasterKey())
	psvc := project.New(st.DB, v, stubProber{branch: "main"})
	rsvc := run.New(st.DB)
	envSvc := environments.NewService(st.DB)
	dep := &stubDeploy{}
	pool := run.NewWorkerPool(rsvc)
	pool.Start()
	t.Cleanup(func() { pool.Stop(context.Background()) })

	srv := httptest.NewServer(New(testWebFSAuth(), svc,
		WithVault(v), WithProjects(psvc), WithRuns(rsvc, pool),
		WithEnvironments(envSvc), WithDeploy(dep)))
	t.Cleanup(srv.Close)

	client := newTestClient(t)
	csrf := loginWithClient(t, client, srv.URL)

	credID := newGitCred(t, client, srv.URL, csrf, "ghp_env")
	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/projects", csrf,
		`{"name":"acme","repoUrl":"https://gitee.com/acme/shop.git","credentialId":"`+credID+`"}`)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var p map[string]any
	_ = json.Unmarshal(raw, &p)
	projID, _ := p["id"].(string)
	if projID == "" {
		t.Fatalf("create project failed: %s", raw)
	}
	return srv, client, csrf, projID, rsvc, dep
}

// seedDeployedRun 直接落一个「向某环境部署过」的成功 run(run + deploy_targets + artifact)。
func seedDeployedRun(t *testing.T, rsvc run.Service, projID, env string, at time.Time) (runID, artID, serverID string) {
	t.Helper()
	ctx := context.Background()
	r, err := rsvc.Create(ctx, projID, run.Trigger{Type: run.TriggerWebhook, ResolvedEnvironment: env, Branch: "main"})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := rsvc.SetDeployTerminal(ctx, r.ID, run.StatusSuccess); err != nil {
		t.Fatalf("force success: %v", err)
	}
	art, err := rsvc.AddArtifact(ctx, run.Artifact{RunID: r.ID, Type: "dist", Name: "web", Reference: "ref-" + env})
	if err != nil {
		t.Fatalf("add artifact: %v", err)
	}
	serverID = "srv-" + uuid.NewString()[:8]
	fin := at
	if err := rsvc.SaveDeployTargets(ctx, r.ID, []run.DeployTarget{{
		RunID: r.ID, ServerID: serverID, ServerName: serverID, Status: run.TargetSuccess,
		StartedAt: at, FinishedAt: &fin,
	}}); err != nil {
		t.Fatalf("save deploy targets: %v", err)
	}
	return r.ID, art.ID, serverID
}

func TestEnvironmentDeploymentsEndpoint(t *testing.T) {
	srv, client, _, projID, rsvc, _ := setupEnvironmentsServer(t)
	base := time.Now().UTC().Add(-time.Hour)
	seedDeployedRun(t, rsvc, projID, "staging", base)
	prodRun, _, _ := seedDeployedRun(t, rsvc, projID, "prod", base.Add(30*time.Minute))

	resp, err := client.Get(srv.URL + "/api/projects/" + projID + "/environments/deployments")
	if err != nil {
		t.Fatalf("GET deployments: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d: %s", resp.StatusCode, raw)
	}
	var got struct {
		Environments []struct {
			Environment string `json:"environment"`
			Active      *struct {
				RunID string `json:"runId"`
			} `json:"active"`
			Deployments []map[string]any `json:"deployments"`
		} `json:"environments"`
	}
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &got)
	if len(got.Environments) != 2 {
		t.Fatalf("want 2 environments, got %d: %s", len(got.Environments), raw)
	}
	if got.Environments[0].Environment != "prod" {
		t.Fatalf("prod (most recent) should be first, got %q", got.Environments[0].Environment)
	}
	if got.Environments[0].Active == nil || got.Environments[0].Active.RunID != prodRun {
		t.Fatalf("prod active should be prodRun, got %+v", got.Environments[0].Active)
	}
}

func TestEnvironmentRollbackEndpoint(t *testing.T) {
	srv, client, csrf, projID, rsvc, dep := setupEnvironmentsServer(t)
	base := time.Now().UTC().Add(-2 * time.Hour)

	// 旧成功(回滚目标)+ 当前活跃成功。
	oldRun, oldArt, oldServer := seedDeployedRun(t, rsvc, projID, "prod", base)
	curRun, _, _ := seedDeployedRun(t, rsvc, projID, "prod", base.Add(time.Hour))

	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/projects/"+projID+"/environments/prod/rollback", csrf, ``)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rollback = %d: %s", resp.StatusCode, raw)
	}
	var out struct {
		Environment string `json:"environment"`
		FromRunID   string `json:"fromRunId"`
		ToRunID     string `json:"toRunId"`
		ArtifactID  string `json:"artifactId"`
	}
	_ = json.Unmarshal(raw, &out)
	if out.ToRunID != oldRun {
		t.Fatalf("rollback target should be oldRun %q, got %q", oldRun, out.ToRunID)
	}
	if out.FromRunID != curRun {
		t.Fatalf("from run should be curRun %q, got %q", curRun, out.FromRunID)
	}
	if out.ArtifactID != oldArt {
		t.Fatalf("artifact should be oldArt %q, got %q", oldArt, out.ArtifactID)
	}
	// 验证 deploy.Service 被以正确入参调用(重发旧 run 的旧产物到原目标机)。
	if dep.lastInput.RunID != oldRun || dep.lastInput.ArtifactID != oldArt {
		t.Fatalf("deploy called with wrong run/artifact: %+v", dep.lastInput)
	}
	if len(dep.lastInput.ServerIDs) != 1 || dep.lastInput.ServerIDs[0] != oldServer {
		t.Fatalf("deploy should target original server %q, got %v", oldServer, dep.lastInput.ServerIDs)
	}
}

func TestEnvironmentRollbackNoTarget(t *testing.T) {
	srv, client, csrf, projID, rsvc, _ := setupEnvironmentsServer(t)
	// 仅一次成功 → 无可回滚目标 → 422。
	seedDeployedRun(t, rsvc, projID, "prod", time.Now().UTC().Add(-time.Hour))
	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/projects/"+projID+"/environments/prod/rollback", csrf, ``)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("want 422, got %d: %s", resp.StatusCode, raw)
	}
}

func TestEnvironmentHistoryNotFound(t *testing.T) {
	srv, client, _, projID, _, _ := setupEnvironmentsServer(t)
	resp, err := client.Get(srv.URL + "/api/projects/" + projID + "/environments/ghost/history")
	if err != nil {
		t.Fatalf("GET history: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
}
