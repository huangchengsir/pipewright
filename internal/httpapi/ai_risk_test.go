package httpapi

import (
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/ai"
)

// TestAnalyzeRisksHappyPath 验证风险标注端点回 200 + findings + aiEnhanced 透传。
func TestAnalyzeRisksHappyPath(t *testing.T) {
	stub := &diagStubAI{risk: &ai.RiskReport{
		Findings: []ai.RiskFinding{
			{Level: ai.RiskHigh, StepName: "构建", Line: 1, Title: "递归强制删除根/通配路径", Why: "rm -rf /", Suggestion: "固定子目录", Source: "rule"},
			{Level: ai.RiskLow, StepName: "部署", Line: 0, Title: "部署后未回滚", Why: "无回滚", Suggestion: "加 rollout undo", Source: "ai"},
		},
		AIEnhanced:  true,
		GeneratedAt: time.Now().UTC(),
	}}
	srv, client, csrf, projID := setupAIGenServer(t, stub, stubAnalyzer{res: ai.RepoAnalysis{Cloned: true}})

	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/projects/"+projID+"/pipeline/analyze-risks", csrf, ``)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, raw)
	}
	dto := decode(t, resp)
	if dto["aiEnhanced"] != true {
		t.Fatalf("aiEnhanced 应透传 true: %v", dto)
	}
	findings, _ := dto["findings"].([]any)
	if len(findings) != 2 {
		t.Fatalf("findings = %d, want 2: %v", len(findings), dto)
	}
	first, _ := findings[0].(map[string]any)
	if first["level"] != "high" || first["source"] != "rule" {
		t.Fatalf("finding[0] 映射错误: %v", first)
	}
}

// TestAnalyzeRisksProjectNotFound 验证项目不存在 → 404。
func TestAnalyzeRisksProjectNotFound(t *testing.T) {
	stub := &diagStubAI{}
	srv, client, csrf, _ := setupAIGenServer(t, stub, stubAnalyzer{res: ai.RepoAnalysis{Cloned: true}})

	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/projects/nope-id/pipeline/analyze-risks", csrf, ``)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("不存在项目应 404, got %d", resp.StatusCode)
	}
}

// TestAnalyzeRisksDegradesNoAI 验证 AI 未配(stub 返回 aiEnhanced=false + reason)仍 200。
func TestAnalyzeRisksDegradesNoAI(t *testing.T) {
	stub := &diagStubAI{risk: &ai.RiskReport{
		Findings:    []ai.RiskFinding{},
		AIEnhanced:  false,
		AIReason:    "AI 未配置或未启用,仅展示确定性规则结果",
		GeneratedAt: time.Now().UTC(),
	}}
	srv, client, csrf, projID := setupAIGenServer(t, stub, stubAnalyzer{res: ai.RepoAnalysis{Cloned: true}})

	resp := doJSON(t, client, http.MethodPost, srv.URL+"/api/projects/"+projID+"/pipeline/analyze-risks", csrf, ``)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("AI 未配应 200(不阻断), got %d", resp.StatusCode)
	}
	dto := decode(t, resp)
	if dto["aiEnhanced"] != false {
		t.Fatalf("aiEnhanced 应为 false: %v", dto)
	}
	if r, _ := dto["aiReason"].(string); r == "" {
		t.Fatalf("aiReason 应有人读原因")
	}
	if _, ok := dto["findings"].([]any); !ok {
		t.Fatalf("findings 应为数组(非 null): %v", dto)
	}
}
