package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/huangjiawei/devopstool/internal/auth"
	"github.com/huangjiawei/devopstool/internal/pipeline"
	"github.com/huangjiawei/devopstool/internal/project"
	"github.com/huangjiawei/devopstool/internal/trigger"
	"github.com/huangjiawei/devopstool/internal/vault"
)

// validationTestEnv 持有校验测试用的 server + 客户端 + csrf + 项目 id。
type validationTestEnv struct {
	srv    *httptest.Server
	client *http.Client
	csrf   string
	projID string
}

// setupValidationServer 构造带全部五个协作服务的测试 server,返回一个绑定有效凭据的项目。
func setupValidationServer(t *testing.T) validationTestEnv {
	t.Helper()
	st := testStoreAuth(t)
	svc := auth.NewService(st.DB, nil)
	if err := svc.Bootstrap("admin", "testpass"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	v := vault.New(st.DB, testMasterKey())
	psvc := project.New(st.DB, v, stubProber{branch: "main"})
	plsvc := pipeline.New(st.DB)
	setsvc := pipeline.NewSettingsService(st.DB, v)
	trsvc := trigger.New(st.DB, v)

	srv := httptest.NewServer(New(testWebFSAuth(), svc,
		WithVault(v), WithProjects(psvc), WithPipelines(plsvc),
		WithPipelineSettings(setsvc), WithTriggers(trsvc)))
	t.Cleanup(srv.Close)

	client := newTestClient(t)
	csrf := loginWithClient(t, client, srv.URL)

	credID := newGitCred(t, client, srv.URL, csrf, "ghp_token_for_validation_proj")
	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/projects", csrf,
		`{"name":"shop","repoUrl":"https://gitee.com/acme/shop.git","credentialId":"`+credID+`"}`)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var p map[string]any
	_ = json.Unmarshal(raw, &p)
	projID, _ := p["id"].(string)
	if projID == "" {
		t.Fatalf("create project failed: %s", raw)
	}
	return validationTestEnv{srv: srv, client: client, csrf: csrf, projID: projID}
}

// getValidation 拉取校验结果并解析为 {ready, issues}。
func getValidation(t *testing.T, env validationTestEnv) (bool, []map[string]any) {
	t.Helper()
	resp := doJSON(t, env.client, http.MethodGet,
		env.srv.URL+"/api/projects/"+env.projID+"/pipeline/validation", env.csrf, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("validation status = %d, want 200, body=%s", resp.StatusCode, raw)
	}
	raw, _ := io.ReadAll(resp.Body)
	var dto struct {
		Ready  bool             `json:"ready"`
		Issues []map[string]any `json:"issues"`
	}
	if err := json.Unmarshal(raw, &dto); err != nil {
		t.Fatalf("decode validation: %v, body=%s", err, raw)
	}
	return dto.Ready, dto.Issues
}

func hasIssueCode(issues []map[string]any, code string) (map[string]any, bool) {
	for _, is := range issues {
		if is["code"] == code {
			return is, true
		}
	}
	return nil, false
}

func putSettings(t *testing.T, env validationTestEnv, body string) {
	t.Helper()
	resp := doJSON(t, env.client, http.MethodPut,
		env.srv.URL+"/api/projects/"+env.projID+"/pipeline/settings", env.csrf, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("PUT settings status = %d, body=%s", resp.StatusCode, raw)
	}
}

func putTrigger(t *testing.T, env validationTestEnv, body string) {
	t.Helper()
	resp := doJSON(t, env.client, http.MethodPut,
		env.srv.URL+"/api/projects/"+env.projID+"/trigger", env.csrf, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("PUT trigger status = %d, body=%s", resp.StatusCode, raw)
	}
}

// TestValidationRequiresAuth 未认证 → 401。
func TestValidationRequiresAuth(t *testing.T) {
	env := setupValidationServer(t)
	resp, err := http.Get(env.srv.URL + "/api/projects/" + env.projID + "/pipeline/validation")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("无会话 GET status = %d, want 401", resp.StatusCode)
	}
}

// TestValidationProjectNotFound 不存在项目 → 404 project_not_found。
func TestValidationProjectNotFound(t *testing.T) {
	env := setupValidationServer(t)
	resp := doJSON(t, env.client, http.MethodGet,
		env.srv.URL+"/api/projects/00000000-0000-0000-0000-000000000000/pipeline/validation", env.csrf, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 404, body=%s", resp.StatusCode, raw)
	}
	raw, _ := io.ReadAll(resp.Body)
	var e map[string]map[string]string
	_ = json.Unmarshal(raw, &e)
	if e["error"]["code"] != "project_not_found" {
		t.Fatalf("code = %q, want project_not_found: %s", e["error"]["code"], raw)
	}
}

// TestValidationDefaultManualOnly 默认种子(空构建/部署、events 全 false、无映射):
// ready=true 且含 manual_only(info) + no_build_or_deploy(warning),无 error。
func TestValidationDefaultReadyManualOnly(t *testing.T) {
	env := setupValidationServer(t)
	ready, issues := getValidation(t, env)
	if !ready {
		t.Fatalf("默认配置应 ready, issues=%+v", issues)
	}
	if _, ok := hasIssueCode(issues, "manual_only"); !ok {
		t.Fatalf("应含 manual_only: %+v", issues)
	}
	// 默认无 build/deploy 任务 → 应有 no_build_or_deploy warning。
	if _, ok := hasIssueCode(issues, "no_build_or_deploy"); !ok {
		t.Fatalf("应含 no_build_or_deploy: %+v", issues)
	}
}

// TestValidationEnvironmentUndefined 分支映射引用未定义环境 → error environment_undefined,ready=false。
func TestValidationEnvironmentUndefined(t *testing.T) {
	env := setupValidationServer(t)
	// settings 仍无任何环境定义;trigger 加一条映射引用「生产」。
	putTrigger(t, env, `{"events":{"push":true,"tag":false,"pullRequest":false},
	  "branchMappings":[{"branchPattern":"main","environment":"生产","targetServerIds":[]}],
	  "unmatchedPolicy":"record"}`)

	ready, issues := getValidation(t, env)
	is, ok := hasIssueCode(issues, "environment_undefined")
	if !ok {
		t.Fatalf("应报 environment_undefined: %+v", issues)
	}
	if is["severity"] != "error" || is["scope"] != "triggers" {
		t.Fatalf("environment_undefined 应 error/triggers: %+v", is)
	}
	if ready {
		t.Fatalf("含 error 不应 ready")
	}

	// 在 settings 定义同名环境后,重拉应不再报 environment_undefined。
	putSettings(t, env, `{"build":{"model":"dockerfile","artifactType":"image"},
	  "environments":[{"name":"生产","targetServerIds":[],"envVars":[],"imageRegistry":{"type":"","url":"","credentialId":""}}]}`)
	_, issues2 := getValidation(t, env)
	if _, ok := hasIssueCode(issues2, "environment_undefined"); ok {
		t.Fatalf("定义环境后不应再报 environment_undefined: %+v", issues2)
	}
}

// TestValidationToolchainIncomplete model=toolchain 缺版本 → error toolchain_incomplete。
func TestValidationToolchainIncomplete(t *testing.T) {
	env := setupValidationServer(t)
	putSettings(t, env, `{"build":{"model":"toolchain","toolchain":{"language":"node","version":""},"artifactType":"jar"},"environments":[]}`)
	ready, issues := getValidation(t, env)
	is, ok := hasIssueCode(issues, "toolchain_incomplete")
	if !ok {
		t.Fatalf("应报 toolchain_incomplete: %+v", issues)
	}
	if is["severity"] != "error" {
		t.Fatalf("应为 error: %+v", is)
	}
	if ready {
		t.Fatalf("toolchain 不全不应 ready")
	}
}

// TestValidationRegistryUrlMissing 镜像仓库选型但无 url → error registry_url_missing。
func TestValidationRegistryUrlMissing(t *testing.T) {
	env := setupValidationServer(t)
	putSettings(t, env, `{"build":{"model":"dockerfile","artifactType":"image"},
	  "environments":[{"name":"生产","targetServerIds":[],"envVars":[],
	    "imageRegistry":{"type":"harbor","url":"","credentialId":""}}]}`)
	ready, issues := getValidation(t, env)
	is, ok := hasIssueCode(issues, "registry_url_missing")
	if !ok {
		t.Fatalf("应报 registry_url_missing: %+v", issues)
	}
	if is["severity"] != "error" {
		t.Fatalf("应为 error: %+v", is)
	}
	if ready {
		t.Fatalf("registry url 缺失不应 ready")
	}
}

// TestValidationTargetServersPending targetServerIds 非空 → warning target_servers_pending,ready 不受影响。
func TestValidationTargetServersPending(t *testing.T) {
	env := setupValidationServer(t)
	// 先定义环境「生产」,再加引用它且带目标服务器的映射(避免 environment_undefined error)。
	putSettings(t, env, `{"build":{"model":"dockerfile","artifactType":"image"},
	  "environments":[{"name":"生产","targetServerIds":[],"envVars":[],"imageRegistry":{"type":"","url":"","credentialId":""}}]}`)
	putTrigger(t, env, `{"events":{"push":true,"tag":false,"pullRequest":false},
	  "branchMappings":[{"branchPattern":"main","environment":"生产","targetServerIds":["srv-1"]}],
	  "unmatchedPolicy":"record"}`)

	ready, issues := getValidation(t, env)
	is, ok := hasIssueCode(issues, "target_servers_pending")
	if !ok {
		t.Fatalf("应报 target_servers_pending: %+v", issues)
	}
	if is["severity"] != "warning" {
		t.Fatalf("应为 warning: %+v", is)
	}
	if !ready {
		t.Fatalf("仅 warning(无 error)应 ready, issues=%+v", issues)
	}
}
