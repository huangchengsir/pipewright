package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/ai"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/vault"
)

// diagStubAI 是可配置的 ai.Service 桩(只实现诊断路径所需;其余返回零值)。
type diagStubAI struct {
	diag       *ai.Diagnosis  // Diagnose 返回值
	gotLog     string         // 记录收到的失败日志(脱敏前)
	gotMaskLog bool           // Diagnose 内是否已脱敏(经 Masker.Scrub 验证)
	risk       *ai.RiskReport // AnnotateRisks 返回值(nil → 空报告)
	gotSteps   []ai.ScriptStep
	cmd        *ai.CommandSuggestion // CommandSuggest 返回值(nil → ErrAINotConfigured)
	cmdErr     error                 // CommandSuggest/ExplainCommand/CompleteCommand 强制错误
	explain    *ai.CommandExplanation
	completion *ai.CompletionResult // CompleteCommand 返回值(nil → ErrAINotConfigured)
	gotNL      string
}

func (s *diagStubAI) Get(context.Context) (*ai.Config, error) { return &ai.Config{}, nil }
func (s *diagStubAI) Save(context.Context, ai.SaveInput) (*ai.Config, error) {
	return &ai.Config{}, nil
}
func (s *diagStubAI) Test(context.Context, ai.TestInput) (*ai.TestResult, error) {
	return &ai.TestResult{}, nil
}
func (s *diagStubAI) Generate(context.Context, ai.GenerateInput) (*ai.Proposal, error) {
	return &ai.Proposal{}, nil
}
func (s *diagStubAI) Diagnose(_ context.Context, in ai.DiagnoseInput) (*ai.Diagnosis, error) {
	s.gotLog = in.FailureLog
	if in.Masker != nil && in.Masker.Scrub(in.FailureLog) != in.FailureLog {
		s.gotMaskLog = true // Masker 确实会改写日志(已登记 secret)
	}
	if s.diag != nil {
		return s.diag, nil
	}
	return &ai.Diagnosis{
		Status:          "unavailable",
		Reason:          "stub 未配置诊断",
		AlternateCauses: []string{},
		FixSuggestions:  []string{},
		Evidence:        []ai.DiagnosisEvidence{},
		GeneratedAt:     time.Now().UTC(),
	}, nil
}
func (s *diagStubAI) AnnotateRisks(_ context.Context, in ai.AnnotateRisksInput) (*ai.RiskReport, error) {
	s.gotSteps = in.Steps
	if s.risk != nil {
		return s.risk, nil
	}
	return &ai.RiskReport{Findings: []ai.RiskFinding{}, GeneratedAt: time.Now().UTC()}, nil
}
func (s *diagStubAI) CommandSuggest(_ context.Context, in ai.CommandSuggestInput) (*ai.CommandSuggestion, error) {
	s.gotNL = in.NL
	if s.cmdErr != nil {
		return nil, s.cmdErr
	}
	if s.cmd != nil {
		return s.cmd, nil
	}
	return nil, ai.ErrAINotConfigured
}
func (s *diagStubAI) ExplainCommand(_ context.Context, _ ai.ExplainCommandInput) (*ai.CommandExplanation, error) {
	if s.cmdErr != nil {
		return nil, s.cmdErr
	}
	if s.explain != nil {
		return s.explain, nil
	}
	return nil, ai.ErrAINotConfigured
}
func (s *diagStubAI) CompleteCommand(_ context.Context, in ai.CompleteCommandInput) (*ai.CompletionResult, error) {
	if s.cmdErr != nil {
		return nil, s.cmdErr
	}
	if s.completion != nil {
		return s.completion, nil
	}
	return nil, ai.ErrAINotConfigured
}

// setupDiagnoseServer 构造带 auth + project + run(失败 runner,FailAt=0)+ stub AI 的 server。
// 返回 server / client / csrf / projID / run.Service。
func setupDiagnoseServer(t *testing.T, stubAI ai.Service) (*httptest.Server, *http.Client, string, string, run.Service) {
	t.Helper()
	st := testStoreAuth(t)
	asvc := auth.NewService(st.DB, nil)
	if err := asvc.Bootstrap("admin", "testpass"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	v := vault.New(st.DB, testMasterKey())
	psvc := project.New(st.DB, v, stubProber{branch: "main"})
	rsvc := run.New(st.DB)
	// FailAt=0:首步即失败,桩 runner 合成含假 secret 的失败日志。
	pool := run.NewWorkerPool(rsvc, run.WithRunner(&run.StubRunner{Steps: []string{"构建镜像"}, FailAt: 0}))
	pool.Start()
	t.Cleanup(func() { pool.Stop(context.Background()) })

	srv := httptest.NewServer(New(testWebFSAuth(), asvc,
		WithVault(v), WithProjects(psvc), WithRuns(rsvc, pool), WithAISettings(stubAI)))
	t.Cleanup(srv.Close)

	client := newTestClient(t)
	csrf := loginWithClient(t, client, srv.URL)
	credID := newGitCred(t, client, srv.URL, csrf, "ghp_token_for_run")
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
	return srv, client, csrf, projID, rsvc
}

// createFailedRun 经 manual 触发创建一个 run,等待其落 failed(桩 runner FailAt=0)。
func createFailedRun(t *testing.T, client *http.Client, srv *httptest.Server, csrf, projID string) string {
	t.Helper()
	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/projects/"+projID+"/runs", csrf, `{"branch":"main"}`)
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	var r map[string]any
	_ = json.Unmarshal(raw, &r)
	id, _ := r["id"].(string)
	if id == "" {
		t.Fatalf("create run failed: %s", raw)
	}
	waitRunStatus(t, client, srv, csrf, id, "failed")
	return id
}

// TestDiagnoseReadyEndpoint 验证 POST /api/runs/{id}/diagnose:失败 run → status=ready,
// 持久化后 GET run-detail 的 diagnosis slot 填值(冻结子 DTO 形状)。
func TestDiagnoseReadyEndpoint(t *testing.T) {
	stub := &diagStubAI{diag: &ai.Diagnosis{
		Status:          "ready",
		Hypothesis:      "最可能的根因是缺少依赖 leftpad(假说,非结论)",
		Confidence:      "high",
		AlternateCauses: []string{"lockfile 未提交"},
		FixSuggestions:  []string{"声明依赖"},
		Evidence:        []ai.DiagnosisEvidence{{Line: 2, Text: "npm ERR! [MASKED]", Highlight: true}},
		GeneratedAt:     time.Now().UTC(),
	}}
	srv, client, csrf, projID, _ := setupDiagnoseServer(t, stub)
	runID := createFailedRun(t, client, srv, csrf, projID)

	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/runs/"+runID+"/diagnose", csrf, "")
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("diagnose status = %d, body=%s", resp.StatusCode, raw)
	}
	var d diagnosisDTO
	_ = json.NewDecoder(resp.Body).Decode(&d)
	_ = resp.Body.Close()
	if d.Status != "ready" || !strings.Contains(d.Hypothesis, "leftpad") {
		t.Fatalf("diagnosis DTO 异常: %+v", d)
	}
	if d.Confidence != "high" || len(d.FixSuggestions) == 0 || len(d.Evidence) == 0 {
		t.Fatalf("diagnosis 子 DTO 字段不全: %+v", d)
	}
	if !d.Evidence[0].Highlight || d.Evidence[0].Line != 2 {
		t.Fatalf("evidence 命中行/高亮异常: %+v", d.Evidence[0])
	}

	// stub 收到的失败日志含假 secret(脱敏前),且 Masker 会脱敏它(出网前)。
	if !strings.Contains(stub.gotLog, run.StubFailureSecret) {
		t.Fatalf("Diagnose 应收到含假 secret 的原始日志: %q", stub.gotLog)
	}
	if !stub.gotMaskLog {
		t.Fatalf("Diagnose 入参 Masker 应已登记假 secret(出网前脱敏)")
	}

	// GET run-detail diagnosis slot 填值。
	got := waitRunStatus(t, client, srv, csrf, runID, "failed")
	diag, ok := got["diagnosis"].(map[string]any)
	if !ok {
		t.Fatalf("run-detail diagnosis 应填值: %v", got["diagnosis"])
	}
	if diag["status"] != "ready" {
		t.Fatalf("run-detail diagnosis.status 应 ready: %v", diag)
	}
}

// TestDiagnoseNoSecretLeak 验证整条响应(含 evidence)绝无明文假 secret。
// 这里让 stub 直接走真实 ai.Service？不:用 mask 验证 DTO 层。改为断言响应体不含明文。
func TestDiagnoseNoSecretLeak(t *testing.T) {
	// stub 返回的诊断字段已是 [MASKED](模拟 ai 层脱敏后),验证 DTO/DB 不引入明文。
	stub := &diagStubAI{diag: &ai.Diagnosis{
		Status:         "ready",
		Hypothesis:     "根因:registry token 配置(" + mask.Placeholder + ")",
		Confidence:     "medium",
		FixSuggestions: []string{"轮换凭据"},
		Evidence:       []ai.DiagnosisEvidence{{Line: 3, Text: "npm config set token=" + mask.Placeholder, Highlight: true}},
		GeneratedAt:    time.Now().UTC(),
	}}
	srv, client, csrf, projID, _ := setupDiagnoseServer(t, stub)
	runID := createFailedRun(t, client, srv, csrf, projID)

	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/runs/"+runID+"/diagnose", csrf, "")
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if strings.Contains(string(raw), run.StubFailureSecret) {
		t.Fatalf("诊断响应泄漏明文 secret: %s", raw)
	}
	if !strings.Contains(string(raw), mask.Placeholder) {
		t.Fatalf("诊断响应应含 [MASKED] 占位: %s", raw)
	}

	// DB 中的 diagnosis_json 也不应含明文(经 GET 间接验证响应,再直接验 GET run-detail)。
	got := waitRunStatus(t, client, srv, csrf, runID, "failed")
	b, _ := json.Marshal(got)
	if strings.Contains(string(b), run.StubFailureSecret) {
		t.Fatalf("run-detail diagnosis 泄漏明文 secret: %s", b)
	}
}

// TestDiagnoseUnavailableNotConfigured 验证 AI 未配(stub 返 unavailable)→ 200 + status=unavailable,
// 绝不 500、绝不阻断。
func TestDiagnoseUnavailableNotConfigured(t *testing.T) {
	stub := &diagStubAI{} // 默认返回 unavailable
	srv, client, csrf, projID, _ := setupDiagnoseServer(t, stub)
	runID := createFailedRun(t, client, srv, csrf, projID)

	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/runs/"+runID+"/diagnose", csrf, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("未配应 200, got %d", resp.StatusCode)
	}
	var d diagnosisDTO
	_ = json.NewDecoder(resp.Body).Decode(&d)
	_ = resp.Body.Close()
	if d.Status != "unavailable" || d.Reason == "" {
		t.Fatalf("应 unavailable + reason: %+v", d)
	}
}

// TestDiagnoseNilAIService 验证未注入 aiSvc → 200 + status=unavailable(绝不 500)。
func TestDiagnoseNilAIService(t *testing.T) {
	srv, client, csrf, projID, _ := setupDiagnoseServer(t, nil)
	runID := createFailedRun(t, client, srv, csrf, projID)

	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/runs/"+runID+"/diagnose", csrf, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("无 aiSvc 应 200, got %d", resp.StatusCode)
	}
	var d diagnosisDTO
	_ = json.NewDecoder(resp.Body).Decode(&d)
	_ = resp.Body.Close()
	if d.Status != "unavailable" {
		t.Fatalf("无 aiSvc 应 unavailable: %+v", d)
	}
}

// TestDiagnoseRunNotFailed 验证非失败 run → 422 run_not_failed。
func TestDiagnoseRunNotFailed(t *testing.T) {
	// 用成功 runner 的标准 server(setupRunServer 默认全成功)。
	srv, client, csrf, projID, rsvc := setupRunServer(t)
	r, err := rsvc.Create(context.Background(), projID, run.Trigger{Type: run.TriggerManual, Branch: "main"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	waitRunStatus(t, client, srv, csrf, r.ID, "success")

	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/runs/"+r.ID+"/diagnose", csrf, "")
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("成功 run 诊断应 422, got %d", resp.StatusCode)
	}
	var body errBody
	_ = json.NewDecoder(resp.Body).Decode(&body)
	_ = resp.Body.Close()
	if body.Error.Code != "run_not_failed" {
		t.Fatalf("错误码应 run_not_failed: %+v", body)
	}
}

// TestDiagnoseRunNotFound 验证不存在 run → 404。
func TestDiagnoseRunNotFound(t *testing.T) {
	srv, client, csrf, _, _ := setupRunServer(t)
	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/runs/nope/diagnose", csrf, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("不存在 run 应 404, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()
}

// TestDiagnoseRequiresCSRF 验证写方法缺 CSRF → 403。
func TestDiagnoseRequiresCSRF(t *testing.T) {
	srv, client, csrf, _, _ := setupRunServer(t)
	_ = csrf
	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/runs/whatever/diagnose", "", "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("缺 CSRF 应 403, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()
}

// TestDiagnoseRunPersistUnavailableFlag 验证 code-review P1:
//   - persistUnavailable=false(自动钩子语义):unavailable 诊断**不落库**,GetDiagnosis 仍 nil
//     (保持「diagnosis=null 即未诊断」干净态;AI 未配/取消的 run 不被污染为「诊断不可用」)。
//   - persistUnavailable=true(显式端点语义):unavailable **落库**,刷新仍能回显 reason。
func TestDiagnoseRunPersistUnavailableFlag(t *testing.T) {
	stub := &diagStubAI{} // 默认返回 unavailable
	srv, client, csrf, projID, rsvc := setupDiagnoseServer(t, stub)
	runID := createFailedRun(t, client, srv, csrf, projID)
	ctx := context.Background()

	// 自动钩子语义:不持久化 unavailable。
	d, err := diagnoseRun(ctx, rsvc, stub, nil, runID, false)
	if err != nil || d == nil || d.Status != "unavailable" {
		t.Fatalf("diagnoseRun(false) 应返回 unavailable: d=%+v err=%v", d, err)
	}
	if got, gerr := rsvc.GetDiagnosis(ctx, runID); gerr != nil || got != nil {
		t.Fatalf("persistUnavailable=false 不应落库,GetDiagnosis 应 nil,得 %+v err=%v", got, gerr)
	}

	// 显式端点语义:持久化 unavailable。
	if _, err := diagnoseRun(ctx, rsvc, stub, nil, runID, true); err != nil {
		t.Fatalf("diagnoseRun(true): %v", err)
	}
	got, gerr := rsvc.GetDiagnosis(ctx, runID)
	if gerr != nil || got == nil || got.Status != "unavailable" {
		t.Fatalf("persistUnavailable=true 应落库 unavailable,得 %+v err=%v", got, gerr)
	}
}
