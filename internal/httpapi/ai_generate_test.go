package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/huangjiawei/devopstool/internal/ai"
	"github.com/huangjiawei/devopstool/internal/auth"
	"github.com/huangjiawei/devopstool/internal/pipeline"
	"github.com/huangjiawei/devopstool/internal/project"
	"github.com/huangjiawei/devopstool/internal/trigger"
	"github.com/huangjiawei/devopstool/internal/vault"
)

// ---- stub ai.Service(只实现 Generate;其余满足接口) ----

type stubAIService struct {
	proposal *ai.Proposal
	err      error
	lastNL   string
	gotClone bool // 记录最近一次 Analysis.Cloned
}

func (s *stubAIService) Get(context.Context) (*ai.Config, error) { return &ai.Config{}, nil }
func (s *stubAIService) Save(context.Context, ai.SaveInput) (*ai.Config, error) {
	return &ai.Config{}, nil
}
func (s *stubAIService) Test(context.Context, ai.TestInput) (*ai.TestResult, error) {
	return &ai.TestResult{}, nil
}
func (s *stubAIService) Generate(_ context.Context, in ai.GenerateInput) (*ai.Proposal, error) {
	s.lastNL = in.NL
	s.gotClone = in.Analysis.Cloned
	if s.err != nil {
		return nil, s.err
	}
	return s.proposal, nil
}
func (s *stubAIService) Diagnose(context.Context, ai.DiagnoseInput) (*ai.Diagnosis, error) {
	return &ai.Diagnosis{Status: "unavailable", Reason: "stub"}, nil
}

// ---- stub analyzer ----

type stubAnalyzer struct {
	res ai.RepoAnalysis
}

func (a stubAnalyzer) Analyze(context.Context, string, string) ai.RepoAnalysis { return a.res }

// sampleProposal 是一份合法提案(node:22 + main→生产)。
func sampleProposal() *ai.Proposal {
	return &ai.Proposal{
		Stages: []ai.ProposalStage{
			{ID: "p_s1", Name: "流水线源", Kind: "source", Jobs: []ai.ProposalJob{{ID: "p_j1", Name: "源", Type: "git_source"}}},
			{ID: "p_s2", Name: "构建", Kind: "build", Jobs: []ai.ProposalJob{{ID: "p_j2", Name: "隔离构建", Type: "build_image", Summary: "node:22"}}},
			{ID: "p_s3", Name: "部署", Kind: "deploy", Jobs: []ai.ProposalJob{{ID: "p_j3", Name: "部署", Type: "deploy"}}},
		},
		Build: ai.ProposalBuild{
			Model:        "toolchain",
			Toolchain:    ai.ProposalToolchain{Language: "node", Version: "22"},
			ArtifactType: "image",
		},
		BranchMappings: []ai.ProposalBranchMapping{{ID: "p_m1", BranchPattern: "main", Environment: "生产"}},
		Rationale:      "检测到 Node 22",
	}
}

// setupAIGenServer 装配带 auth + vault + 全部相关服务 + stub AI/analyzer 的 server,返回一个项目 id。
func setupAIGenServer(t *testing.T, aiSvc ai.Service, analyzer ai.RepoAnalyzer) (*httptest.Server, *http.Client, string, string) {
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
		WithPipelineSettings(setsvc), WithTriggers(trsvc),
		WithAISettings(aiSvc), WithAIGenerate(analyzer)))
	t.Cleanup(srv.Close)

	client := newTestClient(t)
	csrf := loginWithClient(t, client, srv.URL)

	credID := newGitCred(t, client, srv.URL, csrf, "ghp_aigen_token")
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

func decode(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	raw, _ := io.ReadAll(resp.Body)
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode: %v, body=%s", err, raw)
	}
	return m
}

func TestAIGenerateHappyPath(t *testing.T) {
	aiSvc := &stubAIService{proposal: sampleProposal()}
	analyzer := stubAnalyzer{res: ai.RepoAnalysis{
		Cloned: true, Language: "node", LanguageVersion: "22", BuildTool: "npm",
		ArtifactHint: "image", Signals: []string{"package.json", "engines.node=22"},
	}}
	srv, client, csrf, projID := setupAIGenServer(t, aiSvc, analyzer)

	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/projects/"+projID+"/pipeline/ai-generate", csrf,
		`{"nlSupplement":"push main 部署到生产"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, raw)
	}
	dto := decode(t, resp)
	if dto["available"] != true {
		t.Fatalf("available should be true: %v", dto)
	}
	an, _ := dto["analysis"].(map[string]any)
	if an["language"] != "node" || an["languageVersion"] != "22" {
		t.Fatalf("analysis 错误: %v", an)
	}
	prop, _ := dto["proposal"].(map[string]any)
	if prop == nil {
		t.Fatalf("proposal 缺失: %v", dto)
	}
	stages, _ := prop["stages"].([]any)
	if len(stages) != 3 {
		t.Fatalf("stages = %d, want 3", len(stages))
	}
	if aiSvc.lastNL != "push main 部署到生产" {
		t.Fatalf("NL 未透传: %q", aiSvc.lastNL)
	}
	if !aiSvc.gotClone {
		t.Fatalf("analysis.Cloned 未透传给 Generate")
	}
}

func TestAIGenerateAINotConfigured(t *testing.T) {
	aiSvc := &stubAIService{err: ai.ErrAINotConfigured}
	srv, client, csrf, projID := setupAIGenServer(t, aiSvc, stubAnalyzer{res: ai.RepoAnalysis{Cloned: true}})

	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/projects/"+projID+"/pipeline/ai-generate", csrf, `{}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("AI 未配应 200(不阻断), got %d", resp.StatusCode)
	}
	dto := decode(t, resp)
	if dto["available"] != false {
		t.Fatalf("available should be false: %v", dto)
	}
	if r, _ := dto["reason"].(string); r == "" {
		t.Fatalf("reason 应有友好引导")
	}
}

func TestAIGenerateCloneFailDegrade(t *testing.T) {
	aiSvc := &stubAIService{proposal: sampleProposal()}
	// analyzer 返回 cloned=false 降级。
	analyzer := stubAnalyzer{res: ai.RepoAnalysis{Cloned: false, Signals: []string{}}}
	srv, client, csrf, projID := setupAIGenServer(t, aiSvc, analyzer)

	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/projects/"+projID+"/pipeline/ai-generate", csrf, `{}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("clone 失败应降级 200, got %d", resp.StatusCode)
	}
	dto := decode(t, resp)
	an, _ := dto["analysis"].(map[string]any)
	if an["cloned"] != false {
		t.Fatalf("cloned 应为 false: %v", an)
	}
	if dto["available"] != true {
		t.Fatalf("clone 降级但 AI 可用,available 应 true: %v", dto)
	}
}

func TestAIGenerateLLMFailDegradeNoKey(t *testing.T) {
	aiSvc := &stubAIService{err: ai.ErrGenerateFailed}
	srv, client, csrf, projID := setupAIGenServer(t, aiSvc, stubAnalyzer{res: ai.RepoAnalysis{Cloned: true}})

	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/projects/"+projID+"/pipeline/ai-generate", csrf, `{}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("LLM 失败应 200(不 500), got %d", resp.StatusCode)
	}
	dto := decode(t, resp)
	if dto["available"] != true {
		t.Fatalf("LLM 失败 available 仍应 true: %v", dto)
	}
	if r, _ := dto["reason"].(string); !strings.Contains(r, "AI 生成失败") {
		t.Fatalf("reason 应说明生成失败: %q", r)
	}
}

func TestAIGenerateProjectNotFound(t *testing.T) {
	aiSvc := &stubAIService{proposal: sampleProposal()}
	srv, client, csrf, _ := setupAIGenServer(t, aiSvc, stubAnalyzer{res: ai.RepoAnalysis{Cloned: true}})

	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/projects/nonexistent/pipeline/ai-generate", csrf, `{}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("不存在项目应 404, got %d", resp.StatusCode)
	}
}

func TestAIGenerateRequiresAuth(t *testing.T) {
	aiSvc := &stubAIService{proposal: sampleProposal()}
	srv, _, _, projID := setupAIGenServer(t, aiSvc, stubAnalyzer{res: ai.RepoAnalysis{Cloned: true}})

	// 无 cookie 的裸客户端。
	resp, err := http.Post(srv.URL+"/api/projects/"+projID+"/pipeline/ai-generate", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("无认证应 401, got %d", resp.StatusCode)
	}
}

func TestAIGenerateRequiresCSRF(t *testing.T) {
	aiSvc := &stubAIService{proposal: sampleProposal()}
	srv, client, _, projID := setupAIGenServer(t, aiSvc, stubAnalyzer{res: ai.RepoAnalysis{Cloned: true}})

	// 有会话但不带 CSRF header。
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/projects/"+projID+"/pipeline/ai-generate", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("缺 CSRF 应 403, got %d", resp.StatusCode)
	}
}

// ---- apply ----

func proposalJSON() string {
	p := sampleProposal()
	raw, _ := json.Marshal(map[string]any{
		"stages":         p.Stages,
		"build":          p.Build,
		"branchMappings": p.BranchMappings,
		"rationale":      p.Rationale,
	})
	return string(raw)
}

func TestAIApplyAllSelected(t *testing.T) {
	srv, client, csrf, projID := setupAIGenServer(t, &stubAIService{proposal: sampleProposal()}, stubAnalyzer{res: ai.RepoAnalysis{Cloned: true}})

	body := `{"proposal":` + proposalJSON() + `,"selections":{"stageIds":["p_s2","p_s3"],"build":true,"branchMappingIds":["p_m1"]}}`
	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/projects/"+projID+"/pipeline/ai-apply", csrf, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("apply status = %d, body=%s", resp.StatusCode, raw)
	}
	dto := decode(t, resp)
	applied, _ := dto["applied"].(map[string]any)
	if applied["spec"] != true || applied["settings"] != true || applied["triggers"] != true {
		t.Fatalf("全选应三项都写: %v", applied)
	}

	// 验证 spec 写入了构建/部署阶段(且仍恰一个源阶段)。
	pResp := doJSON(t, client, http.MethodGet, srv.URL+"/api/projects/"+projID+"/pipeline", csrf, "")
	defer pResp.Body.Close()
	pdto := decode(t, pResp)
	stages, _ := pdto["stages"].([]any)
	sourceCount := 0
	hasBuildJob := false
	for _, s := range stages {
		sm, _ := s.(map[string]any)
		if sm["kind"] == "source" {
			sourceCount++
		}
		if sm["kind"] == "build" {
			if jobs, _ := sm["jobs"].([]any); len(jobs) > 0 {
				hasBuildJob = true
			}
		}
	}
	if sourceCount != 1 {
		t.Fatalf("应恰一个源阶段, got %d", sourceCount)
	}
	if !hasBuildJob {
		t.Fatalf("构建阶段应含 AI 提案任务")
	}

	// 验证 settings 写入了 toolchain/node/22。
	sResp := doJSON(t, client, http.MethodGet, srv.URL+"/api/projects/"+projID+"/pipeline/settings", csrf, "")
	defer sResp.Body.Close()
	sdto := decode(t, sResp)
	build, _ := sdto["build"].(map[string]any)
	if build["model"] != "toolchain" {
		t.Fatalf("settings 应写 toolchain: %v", build)
	}
	tc, _ := build["toolchain"].(map[string]any)
	if tc["language"] != "node" || tc["version"] != "22" {
		t.Fatalf("toolchain 应为 node/22: %v", tc)
	}

	// 验证 trigger 写入了 main→生产 映射。
	tResp := doJSON(t, client, http.MethodGet, srv.URL+"/api/projects/"+projID+"/trigger", csrf, "")
	defer tResp.Body.Close()
	tdto := decode(t, tResp)
	mappings, _ := tdto["branchMappings"].([]any)
	found := false
	for _, m := range mappings {
		mm, _ := m.(map[string]any)
		if mm["branchPattern"] == "main" && mm["environment"] == "生产" {
			found = true
		}
	}
	if !found {
		t.Fatalf("trigger 应含 main→生产 映射: %v", mappings)
	}
}

func TestAIApplyPartialSelected(t *testing.T) {
	srv, client, csrf, projID := setupAIGenServer(t, &stubAIService{proposal: sampleProposal()}, stubAnalyzer{res: ai.RepoAnalysis{Cloned: true}})

	// 仅勾选 build。
	body := `{"proposal":` + proposalJSON() + `,"selections":{"build":true}}`
	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/projects/"+projID+"/pipeline/ai-apply", csrf, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("apply status = %d, body=%s", resp.StatusCode, raw)
	}
	dto := decode(t, resp)
	applied, _ := dto["applied"].(map[string]any)
	if applied["settings"] != true {
		t.Fatalf("应写 settings: %v", applied)
	}
	if applied["spec"] != false || applied["triggers"] != false {
		t.Fatalf("未勾选项不应写: %v", applied)
	}

	// trigger 不应有 main→生产 映射(未勾选)。
	tResp := doJSON(t, client, http.MethodGet, srv.URL+"/api/projects/"+projID+"/trigger", csrf, "")
	defer tResp.Body.Close()
	tdto := decode(t, tResp)
	mappings, _ := tdto["branchMappings"].([]any)
	if len(mappings) != 0 {
		t.Fatalf("未勾选 branch 映射,trigger 应为空: %v", mappings)
	}
}

func TestAIApplyProjectNotFound(t *testing.T) {
	srv, client, csrf, _ := setupAIGenServer(t, &stubAIService{proposal: sampleProposal()}, stubAnalyzer{res: ai.RepoAnalysis{Cloned: true}})

	body := `{"proposal":` + proposalJSON() + `,"selections":{"build":true}}`
	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/projects/nonexistent/pipeline/ai-apply", csrf, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("不存在项目应 404, got %d, body=%s", resp.StatusCode, raw)
	}
}

// TestAIApplyNoSecretCredentialIDFabricated 验证 apply settings 时绝不编造 credentialId。
func TestAIApplyNoSecretCredentialIDFabricated(t *testing.T) {
	srv, client, csrf, projID := setupAIGenServer(t, &stubAIService{proposal: sampleProposal()}, stubAnalyzer{res: ai.RepoAnalysis{Cloned: true}})

	body := `{"proposal":` + proposalJSON() + `,"selections":{"build":true}}`
	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/projects/"+projID+"/pipeline/ai-apply", csrf, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("apply status = %d", resp.StatusCode)
	}

	sResp := doJSON(t, client, http.MethodGet, srv.URL+"/api/projects/"+projID+"/pipeline/settings", csrf, "")
	defer sResp.Body.Close()
	sdto := decode(t, sResp)
	build, _ := sdto["build"].(map[string]any)
	vars, _ := build["vars"].([]any)
	if len(vars) != 0 {
		t.Fatalf("AI 应用 build 不应编造任何 secret 变量: %v", vars)
	}
}
