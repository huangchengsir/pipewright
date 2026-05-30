package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/huangjiawei/devopstool/internal/auth"
	"github.com/huangjiawei/devopstool/internal/pipeline"
	"github.com/huangjiawei/devopstool/internal/project"
	"github.com/huangjiawei/devopstool/internal/vault"
)

// setupSettingsServer 构造带 auth + vault + project + pipelineSettings 的测试 server,返回一个项目 id。
func setupSettingsServer(t *testing.T) (*httptest.Server, *http.Client, string, string) {
	t.Helper()
	st := testStoreAuth(t)
	svc := auth.NewService(st.DB, nil)
	if err := svc.Bootstrap("admin", "testpass"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	v := vault.New(st.DB, testMasterKey())
	psvc := project.New(st.DB, v, stubProber{branch: "main"})
	setsvc := pipeline.NewSettingsService(st.DB, v)
	srv := httptest.NewServer(New(testWebFSAuth(), svc,
		WithVault(v), WithProjects(psvc), WithPipelineSettings(setsvc)))
	t.Cleanup(srv.Close)

	client := newTestClient(t)
	csrf := loginWithClient(t, client, srv.URL)

	credID := newGitCred(t, client, srv.URL, csrf, "ghp_token_for_settings_proj")
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
	return srv, client, csrf, projID
}

func TestSettingsGetLazyDefaultHTTP(t *testing.T) {
	srv, client, csrf, projID := setupSettingsServer(t)
	resp := doJSON(t, client, http.MethodGet, srv.URL+"/api/projects/"+projID+"/pipeline/settings", csrf, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, raw)
	}
	raw, _ := io.ReadAll(resp.Body)
	var dto map[string]any
	_ = json.Unmarshal(raw, &dto)

	build, _ := dto["build"].(map[string]any)
	if build["model"] != "dockerfile" {
		t.Fatalf("默认构建模型应为 dockerfile, got %v", build["model"])
	}
	if build["artifactType"] != "image" {
		t.Fatalf("默认产物应为 image, got %v", build["artifactType"])
	}
	if vars, ok := build["vars"].([]any); !ok || len(vars) != 0 {
		t.Fatalf("默认构建变量应为 []: %v", build["vars"])
	}
	if envs, ok := dto["environments"].([]any); !ok || len(envs) != 0 {
		t.Fatalf("默认环境应为 []: %v", dto["environments"])
	}
}

func TestSettingsBuildModelToggleRoundTripHTTP(t *testing.T) {
	srv, client, csrf, projID := setupSettingsServer(t)
	base := srv.URL + "/api/projects/" + projID + "/pipeline/settings"

	// 模型 B 工具链。
	body := `{"build":{"model":"toolchain","toolchain":{"language":"node","version":"22"},
	          "artifactType":"jar","vars":[{"key":"NODE_ENV","secret":false,"value":"production"}],
	          "cache":{"enabled":true,"paths":["node_modules"]}},"environments":[]}`
	resp := doJSON(t, client, http.MethodPut, base, csrf, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("PUT B status = %d, body=%s", resp.StatusCode, raw)
	}
	raw, _ := io.ReadAll(resp.Body)
	var dto map[string]any
	_ = json.Unmarshal(raw, &dto)
	build, _ := dto["build"].(map[string]any)
	if build["model"] != "toolchain" || build["artifactType"] != "jar" {
		t.Fatalf("模型 B 往返失败: %v", build)
	}

	// 切回模型 A。
	resp2 := doJSON(t, client, http.MethodPut, base, csrf,
		`{"build":{"model":"dockerfile","artifactType":"image"},"environments":[]}`)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("PUT A status = %d", resp2.StatusCode)
	}
	raw2, _ := io.ReadAll(resp2.Body)
	var dto2 map[string]any
	_ = json.Unmarshal(raw2, &dto2)
	build2, _ := dto2["build"].(map[string]any)
	if build2["model"] != "dockerfile" || build2["dockerfilePath"] != "Dockerfile" {
		t.Fatalf("模型 A 切换失败(应补默认 Dockerfile 路径): %v", build2)
	}
}

func TestSettingsSecretVarRoundTripMaskedHTTP(t *testing.T) {
	srv, client, csrf, projID := setupSettingsServer(t)
	base := srv.URL + "/api/projects/" + projID + "/pipeline/settings"

	// 先建一个含明文标记的凭据。
	credID := newGitCred(t, client, srv.URL, csrf, "ghp_LEAKMARKER_in_settings_zzz")

	body := `{"build":{"model":"dockerfile","artifactType":"image",
	          "vars":[{"key":"NPM_TOKEN","secret":true,"credentialId":"` + credID + `"}]},
	          "environments":[]}`
	resp := doJSON(t, client, http.MethodPut, base, csrf, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("PUT status = %d, body=%s", resp.StatusCode, raw)
	}
	raw, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(raw), "LEAKMARKER") {
		t.Fatalf("响应泄漏明文 secret: %s", raw)
	}
	var dto map[string]any
	_ = json.Unmarshal(raw, &dto)
	build, _ := dto["build"].(map[string]any)
	vars, _ := build["vars"].([]any)
	if len(vars) != 1 {
		t.Fatalf("变量数 = %d", len(vars))
	}
	v0, _ := vars[0].(map[string]any)
	if v0["secret"] != true || v0["credentialId"] != credID {
		t.Fatalf("secret 引用未保留: %v", v0)
	}
	if _, hasValue := v0["value"]; hasValue {
		t.Fatalf("secret 项不应含明文 value 字段: %v", v0)
	}
	masked, _ := v0["maskedValue"].(string)
	if masked == "" || strings.Contains(masked, "LEAKMARKER") {
		t.Fatalf("应回掩码且不含明文: %q", masked)
	}
}

func TestSettingsCredentialNotFound422(t *testing.T) {
	srv, client, csrf, projID := setupSettingsServer(t)
	base := srv.URL + "/api/projects/" + projID + "/pipeline/settings"
	resp := doJSON(t, client, http.MethodPut, base, csrf,
		`{"build":{"vars":[{"key":"X","secret":true,"credentialId":"00000000-0000-0000-0000-000000000000"}]},"environments":[]}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	var e map[string]map[string]string
	_ = json.Unmarshal(raw, &e)
	if e["error"]["code"] != "credential_not_found" {
		t.Fatalf("code = %q, want credential_not_found: %s", e["error"]["code"], raw)
	}
}

func TestSettingsInvalidModel422(t *testing.T) {
	srv, client, csrf, projID := setupSettingsServer(t)
	base := srv.URL + "/api/projects/" + projID + "/pipeline/settings"
	resp := doJSON(t, client, http.MethodPut, base, csrf,
		`{"build":{"model":"bogus","artifactType":"image"},"environments":[]}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	var e map[string]map[string]string
	_ = json.Unmarshal(raw, &e)
	if e["error"]["code"] != "invalid_build" {
		t.Fatalf("code = %q, want invalid_build: %s", e["error"]["code"], raw)
	}
}

func TestSettingsInvalidArtifact422(t *testing.T) {
	srv, client, csrf, projID := setupSettingsServer(t)
	base := srv.URL + "/api/projects/" + projID + "/pipeline/settings"
	resp := doJSON(t, client, http.MethodPut, base, csrf,
		`{"build":{"model":"dockerfile","artifactType":"bogus"},"environments":[]}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
}

func TestSettingsRequiresAuth(t *testing.T) {
	srv, _, _, projID := setupSettingsServer(t)
	resp, err := http.Get(srv.URL + "/api/projects/" + projID + "/pipeline/settings")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("无会话 GET status = %d, want 401", resp.StatusCode)
	}
}

func TestSettingsWriteRequiresCSRF(t *testing.T) {
	srv, client, _, projID := setupSettingsServer(t)
	base := srv.URL + "/api/projects/" + projID + "/pipeline/settings"
	req, _ := http.NewRequest(http.MethodPut, base, strings.NewReader(`{"build":{},"environments":[]}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("无 CSRF PUT status = %d, want 403", resp.StatusCode)
	}
}

// TestSettingsPipelineSpecUntouched 确保 settings 端点不影响 2-2 的 /pipeline 路由共存(注册顺序不被吞)。
func TestSettingsRouteDoesNotShadowPipeline(t *testing.T) {
	srv, client, csrf, projID := setupSettingsServer(t)
	// /pipeline 未注入服务 → 503(而非被 settings 吞成 200/404),证明两路由独立解析。
	resp := doJSON(t, client, http.MethodGet, srv.URL+"/api/projects/"+projID+"/pipeline", csrf, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("/pipeline 应 503(未注入), got %d", resp.StatusCode)
	}
}
