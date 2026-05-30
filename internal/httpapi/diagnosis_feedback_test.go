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
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/vault"
)

// feedbackEnv 聚合反馈 server 测试夹具(便于按需取 store 做裸 DB 断言)。
type feedbackEnv struct {
	srv    *httptest.Server
	client *http.Client
	csrf   string
	projID string
	runs   run.Service
	fb     run.FeedbackService
	store  *store.Store
}

// setupFeedbackServer 构造带 auth + project + 失败 runner + stub AI + feedback 服务的 server。
func setupFeedbackServer(t *testing.T, stubAI ai.Service) feedbackEnv {
	t.Helper()
	st := testStoreAuth(t)
	asvc := auth.NewService(st.DB, nil)
	if err := asvc.Bootstrap("admin", "testpass"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	v := vault.New(st.DB, testMasterKey())
	psvc := project.New(st.DB, v, stubProber{branch: "main"})
	rsvc := run.New(st.DB)
	fbsvc := run.NewFeedbackService(st.DB)
	pool := run.NewWorkerPool(rsvc, run.WithRunner(&run.StubRunner{Steps: []string{"构建镜像"}, FailAt: 0}))
	pool.Start()
	t.Cleanup(func() { pool.Stop(context.Background()) })

	srv := httptest.NewServer(New(testWebFSAuth(), asvc,
		WithVault(v), WithProjects(psvc), WithRuns(rsvc, pool),
		WithAISettings(stubAI), WithDiagnosisFeedback(fbsvc)))
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
	return feedbackEnv{srv: srv, client: client, csrf: csrf, projID: projID, runs: rsvc, fb: fbsvc, store: st}
}

// readyStub 返回固定 ready 诊断的 stub。
func readyStub() *diagStubAI {
	return &diagStubAI{diag: &ai.Diagnosis{
		Status:          "ready",
		Hypothesis:      "缺少依赖 leftpad(假说,非结论)",
		Confidence:      "high",
		AlternateCauses: []string{},
		FixSuggestions:  []string{"声明依赖"},
		Evidence:        []ai.DiagnosisEvidence{{Line: 2, Text: "npm ERR! [MASKED]", Highlight: true}},
		GeneratedAt:     time.Now().UTC(),
	}}
}

// diagnoseRunHTTP 经 POST /diagnose 给 run 生成并持久化诊断(供反馈前置)。
func diagnoseRunHTTP(t *testing.T, client *http.Client, srv *httptest.Server, csrf, runID string) {
	t.Helper()
	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/runs/"+runID+"/diagnose", csrf, "")
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		t.Fatalf("diagnose 失败 %d: %s", resp.StatusCode, raw)
	}
	_ = resp.Body.Close()
}

func statsViaHTTP(t *testing.T, client *http.Client, srv *httptest.Server, csrf string) diagnosisStatsDTO {
	t.Helper()
	resp := doJSON(t, client, http.MethodGet, srv.URL+"/api/settings/diagnosis-stats", csrf, "")
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		t.Fatalf("stats 状态 %d: %s", resp.StatusCode, raw)
	}
	var s diagnosisStatsDTO
	_ = json.NewDecoder(resp.Body).Decode(&s)
	_ = resp.Body.Close()
	return s
}

// TestFeedbackThumbsUpUpdatesAccuracy 验证 👍 → 200 ok;GET stats 准确率/计数更新。
func TestFeedbackThumbsUpUpdatesAccuracy(t *testing.T) {
	env := setupFeedbackServer(t, readyStub())
	srv, client, csrf, projID := env.srv, env.client, env.csrf, env.projID
	runID := createFailedRun(t, client, srv, csrf, projID)
	diagnoseRunHTTP(t, client, srv, csrf, runID)

	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/runs/"+runID+"/diagnosis/feedback", csrf, `{"verdict":"up"}`)
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("👍 应 200, got %d: %s", resp.StatusCode, raw)
	}
	var ok map[string]bool
	_ = json.NewDecoder(resp.Body).Decode(&ok)
	_ = resp.Body.Close()
	if !ok["ok"] {
		t.Fatalf("响应应 {ok:true}: %+v", ok)
	}

	stats := statsViaHTTP(t, client, srv, csrf)
	if stats.TotalFeedback != 1 || stats.ThumbsUp != 1 || stats.ThumbsDown != 0 {
		t.Fatalf("👍 后计数异常: %+v", stats)
	}
	if stats.Accuracy == nil || *stats.Accuracy != 1.0 {
		t.Fatalf("👍 后 accuracy 应 1.0: %v", stats.Accuracy)
	}
}

// TestFeedbackThumbsDownCorrection 验证 👎 + 根因 → recentCorrections 出现(脱敏)。
func TestFeedbackThumbsDownCorrection(t *testing.T) {
	env := setupFeedbackServer(t, readyStub())
	srv, client, csrf, projID := env.srv, env.client, env.csrf, env.projID
	runID := createFailedRun(t, client, srv, csrf, projID)
	diagnoseRunHTTP(t, client, srv, csrf, runID)

	// 根因里夹带桩假 secret,验证脱敏:落库/响应均应 [MASKED]。
	body := `{"verdict":"down","correctRootCause":"真正根因是 registry token ` + run.StubFailureSecret + ` 失效"}`
	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/runs/"+runID+"/diagnosis/feedback", csrf, body)
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("👎 应 200, got %d: %s", resp.StatusCode, raw)
	}
	_ = resp.Body.Close()

	stats := statsViaHTTP(t, client, srv, csrf)
	if stats.TotalFeedback != 1 || stats.ThumbsDown != 1 {
		t.Fatalf("👎 后计数异常: %+v", stats)
	}
	if len(stats.RecentCorrections) != 1 {
		t.Fatalf("recentCorrections 应有 1 条: %+v", stats.RecentCorrections)
	}
	corr := stats.RecentCorrections[0]
	if corr.RunID != runID {
		t.Fatalf("correction runId 应为该 run: %+v", corr)
	}
	if strings.Contains(corr.CorrectRootCause, run.StubFailureSecret) {
		t.Fatalf("correction 泄漏明文 secret: %q", corr.CorrectRootCause)
	}
	if !strings.Contains(corr.CorrectRootCause, "[MASKED]") {
		t.Fatalf("correction 应脱敏为 [MASKED]: %q", corr.CorrectRootCause)
	}
}

// TestFeedbackUpsertOverwrite 验证同 run 再 POST → 覆盖不新增。
func TestFeedbackUpsertOverwrite(t *testing.T) {
	env := setupFeedbackServer(t, readyStub())
	srv, client, csrf, projID := env.srv, env.client, env.csrf, env.projID
	runID := createFailedRun(t, client, srv, csrf, projID)
	diagnoseRunHTTP(t, client, srv, csrf, runID)

	// 先 👍。
	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/runs/"+runID+"/diagnosis/feedback", csrf, `{"verdict":"up"}`)
	_ = resp.Body.Close()
	// 再改判 👎。
	resp = doJSON(t, client, http.MethodPost, srv.URL+"/api/runs/"+runID+"/diagnosis/feedback", csrf, `{"verdict":"down","correctRootCause":"实际是磁盘满"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("改判应 200, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	stats := statsViaHTTP(t, client, srv, csrf)
	if stats.TotalFeedback != 1 {
		t.Fatalf("upsert 后总数应仍 1: %+v", stats)
	}
	if stats.ThumbsUp != 0 || stats.ThumbsDown != 1 {
		t.Fatalf("改判后应 0 up / 1 down: %+v", stats)
	}
}

// TestFeedbackNoDiagnosis422 验证 run 无诊断 → 422 no_diagnosis。
func TestFeedbackNoDiagnosis422(t *testing.T) {
	env := setupFeedbackServer(t, readyStub())
	srv, client, csrf, projID := env.srv, env.client, env.csrf, env.projID
	runID := createFailedRun(t, client, srv, csrf, projID)
	// 不调用 diagnose:run 失败但无诊断。

	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/runs/"+runID+"/diagnosis/feedback", csrf, `{"verdict":"up"}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("无诊断应 422, got %d", resp.StatusCode)
	}
	var body errBody
	_ = json.NewDecoder(resp.Body).Decode(&body)
	_ = resp.Body.Close()
	if body.Error.Code != "no_diagnosis" {
		t.Fatalf("错误码应 no_diagnosis: %+v", body)
	}
}

// TestFeedbackRunNotFound 验证不存在 run → 404。
func TestFeedbackRunNotFound(t *testing.T) {
	env := setupFeedbackServer(t, readyStub())
	srv, client, csrf := env.srv, env.client, env.csrf
	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/runs/nope/diagnosis/feedback", csrf, `{"verdict":"up"}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("不存在 run 应 404, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()
}

// TestFeedbackInvalidVerdict 验证非法 verdict → 422 invalid_verdict。
func TestFeedbackInvalidVerdict(t *testing.T) {
	env := setupFeedbackServer(t, readyStub())
	srv, client, csrf, projID := env.srv, env.client, env.csrf, env.projID
	runID := createFailedRun(t, client, srv, csrf, projID)
	diagnoseRunHTTP(t, client, srv, csrf, runID)

	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/runs/"+runID+"/diagnosis/feedback", csrf, `{"verdict":"maybe"}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("非法 verdict 应 422, got %d", resp.StatusCode)
	}
	var body errBody
	_ = json.NewDecoder(resp.Body).Decode(&body)
	_ = resp.Body.Close()
	if body.Error.Code != "invalid_verdict" {
		t.Fatalf("错误码应 invalid_verdict: %+v", body)
	}
}

// TestFeedbackRequiresCSRF 验证写方法缺 CSRF → 403。
func TestFeedbackRequiresCSRF(t *testing.T) {
	env := setupFeedbackServer(t, readyStub())
	srv, client := env.srv, env.client
	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/runs/whatever/diagnosis/feedback", "", `{"verdict":"up"}`)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("缺 CSRF 应 403, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()
}

// TestFeedbackRequiresAuth 验证未认证 → 401。
func TestFeedbackRequiresAuth(t *testing.T) {
	env := setupFeedbackServer(t, readyStub())
	srv := env.srv
	noAuth := &http.Client{}
	resp, err := noAuth.Get(srv.URL + "/api/settings/diagnosis-stats")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("未认证 stats 应 401, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()
}

// TestStatsEmptyNoFeedback 验证无反馈 → 全 0 / accuracy null / 空数组。
func TestStatsEmptyNoFeedback(t *testing.T) {
	env := setupFeedbackServer(t, readyStub())
	srv, client, csrf := env.srv, env.client, env.csrf
	resp := doJSON(t, client, http.MethodGet, srv.URL+"/api/settings/diagnosis-stats", csrf, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("空 stats 应 200, got %d", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	// accuracy 字段必须为 null(显式存在)。
	if !strings.Contains(string(raw), `"accuracy":null`) {
		t.Fatalf("空库 accuracy 应为 null: %s", raw)
	}
	var s diagnosisStatsDTO
	_ = json.Unmarshal(raw, &s)
	if s.TotalFeedback != 0 || s.RecentTrend == nil || s.RecentCorrections == nil {
		t.Fatalf("空库 stats 形状异常: %+v", s)
	}
}

// TestFeedbackRawDBNoPlaintextSecret 验证裸 DB:correct_root_cause / diagnosis_snapshot
// 列均无明文假 secret(脱敏在 handler 层落库前完成)。
func TestFeedbackRawDBNoPlaintextSecret(t *testing.T) {
	env := setupFeedbackServer(t, readyStub())
	runID := createFailedRun(t, env.client, env.srv, env.csrf, env.projID)
	diagnoseRunHTTP(t, env.client, env.srv, env.csrf, runID)

	body := `{"verdict":"down","correctRootCause":"根因是 token ` + run.StubFailureSecret + ` 失效"}`
	resp := doJSON(t, env.client, http.MethodPost, env.srv.URL+"/api/runs/"+runID+"/diagnosis/feedback", env.csrf, body)
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("👎 应 200, got %d: %s", resp.StatusCode, raw)
	}
	_ = resp.Body.Close()

	var correct, snapshot string
	if err := env.store.DB.QueryRow(
		`SELECT correct_root_cause, diagnosis_snapshot FROM diagnosis_feedback WHERE run_id = ?`, runID,
	).Scan(&correct, &snapshot); err != nil {
		t.Fatalf("query raw feedback: %v", err)
	}
	if strings.Contains(correct, run.StubFailureSecret) {
		t.Fatalf("裸 DB correct_root_cause 泄漏明文 secret: %q", correct)
	}
	if !strings.Contains(correct, "[MASKED]") {
		t.Fatalf("裸 DB correct_root_cause 应脱敏为 [MASKED]: %q", correct)
	}
	if strings.Contains(snapshot, run.StubFailureSecret) {
		t.Fatalf("裸 DB diagnosis_snapshot 泄漏明文 secret: %q", snapshot)
	}
}
