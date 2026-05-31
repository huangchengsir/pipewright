package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/vault"
)

// setupPipelineServer 构造带 auth + vault + project + pipeline 的测试 server,返回一个项目 id。
func setupPipelineServer(t *testing.T) (*httptest.Server, *http.Client, string, string) {
	t.Helper()
	st := testStoreAuth(t)
	svc := auth.NewService(st.DB, nil)
	if err := svc.Bootstrap("admin", "testpass"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	v := vault.New(st.DB, testMasterKey())
	psvc := project.New(st.DB, v, stubProber{branch: "main"})
	plsvc := pipeline.New(st.DB)
	srv := httptest.NewServer(New(testWebFSAuth(), svc,
		WithVault(v), WithProjects(psvc), WithPipelines(plsvc)))
	t.Cleanup(srv.Close)

	client := newTestClient(t)
	csrf := loginWithClient(t, client, srv.URL)

	credID := newGitCred(t, client, srv.URL, csrf, "ghp_token_for_proj")
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

func TestPipelineGetLazyDefault(t *testing.T) {
	srv, client, csrf, projID := setupPipelineServer(t)
	resp := doJSON(t, client, http.MethodGet, srv.URL+"/api/projects/"+projID+"/pipeline", csrf, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	var dto map[string]any
	_ = json.Unmarshal(raw, &dto)

	stages, ok := dto["stages"].([]any)
	if !ok || len(stages) != 2 {
		t.Fatalf("默认应 2 阶段(源+构建), got %v", dto["stages"])
	}
	if dto["status"] != "draft" {
		t.Fatalf("status = %v, want draft", dto["status"])
	}
	yamlStr, _ := dto["yaml"].(string)
	if strings.TrimSpace(yamlStr) == "" {
		t.Fatal("yaml 不应为空")
	}
	if _, ok := dto["updatedAt"].(string); !ok {
		t.Fatalf("应有 updatedAt, got %v", dto["updatedAt"])
	}
	// 源阶段第一个任务 type=git_source。
	src, _ := stages[0].(map[string]any)
	jobs, _ := src["jobs"].([]any)
	if len(jobs) != 1 {
		t.Fatalf("源阶段应 1 任务, got %v", src["jobs"])
	}
	j0, _ := jobs[0].(map[string]any)
	if j0["type"] != "git_source" {
		t.Fatalf("源任务 type = %v, want git_source", j0["type"])
	}
}

func TestPipelineSaveRoundTrip(t *testing.T) {
	srv, client, csrf, projID := setupPipelineServer(t)
	base := srv.URL + "/api/projects/" + projID + "/pipeline"

	// 含源阶段(前端 save 始终带源阶段;API 要求恰一个 source 阶段不变式)。
	body := `{"stages":[
	  {"name":"流水线源","kind":"source","jobs":[
	    {"name":"Gitee 源","type":"git_source","summary":"main","config":{}}
	  ]},
	  {"name":"构建","kind":"build","jobs":[
	    {"name":"打镜像","type":"build_image","summary":"docker build","config":{"dockerfile":"Dockerfile"}}
	  ]},
	  {"name":"部署","kind":"deploy","jobs":[]}
	]}`
	resp := doJSON(t, client, http.MethodPut, base, csrf, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("PUT status = %d, body=%s", resp.StatusCode, raw)
	}
	raw, _ := io.ReadAll(resp.Body)
	var dto map[string]any
	_ = json.Unmarshal(raw, &dto)
	stages, _ := dto["stages"].([]any)
	if len(stages) != 3 {
		t.Fatalf("阶段数 = %d, want 3", len(stages))
	}
	// 服务端补全 id。
	s0, _ := stages[0].(map[string]any)
	if id, _ := s0["id"].(string); id == "" {
		t.Fatal("服务端应补全阶段 id")
	}
	if dto["status"] != "draft" {
		t.Fatalf("status = %v, want draft", dto["status"])
	}
	if !strings.Contains(dto["yaml"].(string), "build_image") {
		t.Fatalf("yaml 应含任务 type, got %v", dto["yaml"])
	}

	// 刷新 GET 仍是 2 阶段(持久化)。
	gresp := doJSON(t, client, http.MethodGet, base, csrf, "")
	defer gresp.Body.Close()
	graw, _ := io.ReadAll(gresp.Body)
	var gdto map[string]any
	_ = json.Unmarshal(graw, &gdto)
	if gs, _ := gdto["stages"].([]any); len(gs) != 3 {
		t.Fatalf("GET 后阶段数 = %d, want 3", len(gs))
	}
}

func TestPipelineSaveInvalidStage422(t *testing.T) {
	srv, client, csrf, projID := setupPipelineServer(t)
	base := srv.URL + "/api/projects/" + projID + "/pipeline"
	resp := doJSON(t, client, http.MethodPut, base, csrf, `{"stages":[{"name":"  ","kind":"build","jobs":[]}]}`)
	defer resp.Body.Close()
	assertErrorCode(t, resp, http.StatusUnprocessableEntity, "invalid_stage")
}

func TestPipelineSaveInvalidKind422(t *testing.T) {
	srv, client, csrf, projID := setupPipelineServer(t)
	base := srv.URL + "/api/projects/" + projID + "/pipeline"
	resp := doJSON(t, client, http.MethodPut, base, csrf, `{"stages":[{"name":"x","kind":"bogus","jobs":[]}]}`)
	defer resp.Body.Close()
	assertErrorCode(t, resp, http.StatusUnprocessableEntity, "invalid_stage")
}

func TestPipelineSaveInvalidJob422(t *testing.T) {
	srv, client, csrf, projID := setupPipelineServer(t)
	base := srv.URL + "/api/projects/" + projID + "/pipeline"
	resp := doJSON(t, client, http.MethodPut, base, csrf,
		`{"stages":[{"name":"构建","kind":"build","jobs":[{"name":"","type":"t"}]}]}`)
	defer resp.Body.Close()
	assertErrorCode(t, resp, http.StatusUnprocessableEntity, "invalid_job")
}

func TestPipelineSaveDuplicateID422(t *testing.T) {
	srv, client, csrf, projID := setupPipelineServer(t)
	base := srv.URL + "/api/projects/" + projID + "/pipeline"
	resp := doJSON(t, client, http.MethodPut, base, csrf,
		`{"stages":[{"id":"dup","name":"a","kind":"build","jobs":[]},{"id":"dup","name":"b","kind":"deploy","jobs":[]}]}`)
	defer resp.Body.Close()
	assertErrorCode(t, resp, http.StatusUnprocessableEntity, "duplicate_id")
}

func TestPipelineProjectNotFound404(t *testing.T) {
	srv, client, csrf, _ := setupPipelineServer(t)
	resp := doJSON(t, client, http.MethodGet, srv.URL+"/api/projects/nope/pipeline", csrf, "")
	defer resp.Body.Close()
	assertErrorCode(t, resp, http.StatusNotFound, "project_not_found")
}

func TestPipelineUnauthenticated401(t *testing.T) {
	srv, _, _, projID := setupPipelineServer(t)
	// 无 cookie 的裸客户端。
	bare := newTestClient(t)
	resp, err := bare.Get(srv.URL + "/api/projects/" + projID + "/pipeline")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestPipelineNoCSRF403(t *testing.T) {
	srv, client, _, projID := setupPipelineServer(t)
	base := srv.URL + "/api/projects/" + projID + "/pipeline"
	// PUT 不带 X-CSRF-Token(传空 csrf)。
	resp := doJSON(t, client, http.MethodPut, base, "", `{"stages":[]}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

// assertErrorCode 断言响应状态码与错误体 code。
func assertErrorCode(t *testing.T, resp *http.Response, wantStatus int, wantCode string) {
	t.Helper()
	if resp.StatusCode != wantStatus {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want %d, body=%s", resp.StatusCode, wantStatus, raw)
	}
	raw, _ := io.ReadAll(resp.Body)
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &body)
	if body.Error.Code != wantCode {
		t.Fatalf("error code = %q, want %q, body=%s", body.Error.Code, wantCode, raw)
	}
}

// ─── 流水线即代码导入/预览(FR-8-12) ────────────────────────────────────────

const importYAMLDoc = `version: 1
stages:
  - id: stg_src
    name: 流水线源
    kind: source
    jobs:
      - name: Gitee 源
        type: git_source
  - id: stg_build
    name: 构建
    kind: build
    needs: [stg_src]
    jobs:
      - name: 运行测试
        type: script
        script:
          image: golang:1.23
          commands:
            - go vet ./...
            - go test ./...
          workdir: .
  - id: stg_deploy
    name: 部署
    kind: deploy
    needs: [stg_build]
    gate: true
    when:
      branches: [main]
    jobs:
      - name: SSH 部署
        type: deploy_ssh
`

func TestPipelineImportPreviewNoSave(t *testing.T) {
	srv, client, csrf, projID := setupPipelineServer(t)
	body, _ := json.Marshal(map[string]any{"yaml": importYAMLDoc})
	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/projects/"+projID+"/pipeline/import", csrf, string(body))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, raw)
	}
	raw, _ := io.ReadAll(resp.Body)
	var dto map[string]any
	_ = json.Unmarshal(raw, &dto)
	stages, ok := dto["stages"].([]any)
	if !ok || len(stages) != 3 {
		t.Fatalf("预览应 3 阶段, got %v", dto["stages"])
	}

	// 预览不落库:GET 仍是默认 4 阶段种子。
	g := doJSON(t, client, http.MethodGet, srv.URL+"/api/projects/"+projID+"/pipeline", csrf, "")
	defer g.Body.Close()
	graw, _ := io.ReadAll(g.Body)
	var gd map[string]any
	_ = json.Unmarshal(graw, &gd)
	if gs, _ := gd["stages"].([]any); len(gs) != 2 {
		t.Fatalf("预览不应落库,GET 应仍为默认 2 阶段(源+构建), got %d", len(gs))
	}
}

func TestPipelineImportSavePersists(t *testing.T) {
	srv, client, csrf, projID := setupPipelineServer(t)
	body, _ := json.Marshal(map[string]any{"yaml": importYAMLDoc, "save": true})
	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/projects/"+projID+"/pipeline/import", csrf, string(body))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, raw)
	}

	// save=true 应落库:GET 返回导入的 3 阶段。
	g := doJSON(t, client, http.MethodGet, srv.URL+"/api/projects/"+projID+"/pipeline", csrf, "")
	defer g.Body.Close()
	graw, _ := io.ReadAll(g.Body)
	var gd map[string]any
	_ = json.Unmarshal(graw, &gd)
	gs, _ := gd["stages"].([]any)
	if len(gs) != 3 {
		t.Fatalf("save=true 应落库 3 阶段, got %d", len(gs))
	}
}

func TestPipelineImportInvalidYAML(t *testing.T) {
	srv, client, csrf, projID := setupPipelineServer(t)
	body, _ := json.Marshal(map[string]any{"yaml": "not: [valid"})
	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/projects/"+projID+"/pipeline/import", csrf, string(body))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("非法 YAML status = %d, want 422", resp.StatusCode)
	}
}

func TestPipelineImportInvalidStage(t *testing.T) {
	srv, client, csrf, projID := setupPipelineServer(t)
	doc := "stages:\n  - name: x\n    kind: bogus\n    jobs: [{name: j, type: git_source}]\n"
	body, _ := json.Marshal(map[string]any{"yaml": doc})
	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/projects/"+projID+"/pipeline/import", csrf, string(body))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("非法 kind status = %d, want 422", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	var body2 struct{ Error struct{ Code string } }
	_ = json.Unmarshal(raw, &body2)
	if body2.Error.Code != "invalid_stage" {
		t.Fatalf("error code = %q, want invalid_stage, body=%s", body2.Error.Code, raw)
	}
}
