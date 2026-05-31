package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/ai"
	"github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/project"
)

// ai_risk.go 是「AI 脚本风险标注」HTTP 层(护城河 · AI moat)。
//
// POST /api/projects/{id}/pipeline/analyze-risks(认证 + CSRF):对项目流水线 settings 里的
// 脚本步骤(Epic 8 script step)做风险标注。编排:取项目(404)→ 取 settings 脚本步骤 →
// 据项目凭据建 Masker(出网前脱敏)→ ai.AnnotateRisks(确定性规则 + 可选 LLM 增强)→ 回 DTO。
//
// **优雅降级铁律**:AI 未配/失败 → 仍回 200 + 确定性规则结果 + aiEnhanced=false + reason;
// 绝不 500、绝无明文密钥。AnnotateRisks 本身永不 error(护城河:无 AI 也有价值)。

// ---- 风险标注 DTO(契约形状;camelCase;绝无明文密钥) ----

type riskFindingDTO struct {
	Level      string `json:"level"`      // high | medium | low
	StepName   string `json:"stepName"`   //
	Line       int    `json:"line"`       // 1-based;0=步骤/镜像级
	Title      string `json:"title"`      //
	Why        string `json:"why"`        //
	Suggestion string `json:"suggestion"` //
	Source     string `json:"source"`     // rule | ai
}

// analyzeRisksDTO 是 analyze-risks 端点响应。findings 永不为 null。
type analyzeRisksDTO struct {
	Findings    []riskFindingDTO `json:"findings"`
	AIEnhanced  bool             `json:"aiEnhanced"`
	AIReason    string           `json:"aiReason"`
	GeneratedAt string           `json:"generatedAt"`
}

func toAnalyzeRisksDTO(r *ai.RiskReport) analyzeRisksDTO {
	findings := make([]riskFindingDTO, 0, len(r.Findings))
	for _, f := range r.Findings {
		findings = append(findings, riskFindingDTO{
			Level:      f.Level,
			StepName:   f.StepName,
			Line:       f.Line,
			Title:      f.Title,
			Why:        f.Why,
			Suggestion: f.Suggestion,
			Source:     f.Source,
		})
	}
	return analyzeRisksDTO{
		Findings:    findings,
		AIEnhanced:  r.AIEnhanced,
		AIReason:    r.AIReason,
		GeneratedAt: r.GeneratedAt.UTC().Format(time.RFC3339),
	}
}

// aiRiskDeps 聚合 analyze-risks handler 所需服务(复用已注入服务,无新顶层依赖)。
type aiRiskDeps struct {
	aiSvc        ai.Service
	projects     project.Service
	settings     pipeline.SettingsService
	secretSource *RunSecretSource
}

// makeAnalyzeRisksHandler 返回 POST /api/projects/{id}/pipeline/analyze-risks handler。
//
// 项目不存在 → 404;服务未初始化 → 503。其余一律 200(含 AI 未配/失败的优雅降级)。
func makeAnalyzeRisksHandler(d aiRiskDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.aiSvc == nil || d.projects == nil || d.settings == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "风险标注所需服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		ctx := r.Context()

		// 项目存在性(404 与既有 ai-generate 一致)。
		if _, err := d.projects.Get(ctx, id); err != nil {
			if err == project.ErrNotFound {
				writeError(w, http.StatusNotFound, "project_not_found", "项目不存在")
				return
			}
			writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
			return
		}

		// 取流水线 settings 的脚本步骤(Epic 8);取不到则降级为空步骤(仍返回空报告,不 500)。
		steps := []ai.ScriptStep{}
		if st, err := d.settings.Get(ctx, id); err == nil && st != nil {
			steps = toAIScriptSteps(st.Steps)
		}

		// 出网前脱敏:据项目凭据建 Masker(把 secret env 明文登记后,命令进 prompt 前一律脱敏)。
		masker := maskerForProject(ctx, d.secretSource, id)

		report, _ := d.aiSvc.AnnotateRisks(ctx, ai.AnnotateRisksInput{
			Steps:  steps,
			Masker: masker,
		})
		if report == nil {
			// 极端兜底:AnnotateRisks 约定永不返 nil,但仍防御性给空报告(绝不 500)。
			report = &ai.RiskReport{Findings: []ai.RiskFinding{}, GeneratedAt: time.Now().UTC()}
		}
		writeJSON(w, http.StatusOK, toAnalyzeRisksDTO(report))
	}
}

// toAIScriptSteps 把 pipeline 脚本步骤映射为 ai 中性入参(ai 包不 import pipeline)。
func toAIScriptSteps(steps []pipeline.PipelineStep) []ai.ScriptStep {
	out := make([]ai.ScriptStep, 0, len(steps))
	for _, st := range steps {
		cmds := make([]string, len(st.Commands))
		copy(cmds, st.Commands)
		out = append(out, ai.ScriptStep{
			Name:     st.Name,
			Image:    st.Image,
			Commands: cmds,
		})
	}
	return out
}

// maskerForProject 返回登记了该项目真实凭据(+ 桩假 secret)的 Masker;secretSrc 为 nil 时
// 降级为只登记桩 secret(纯单测/未装配场景)。供脚本风险标注出网前脱敏。
func maskerForProject(ctx context.Context, src *RunSecretSource, projectID string) *mask.Masker {
	if src == nil {
		m := mask.NewMasker()
		return m
	}
	return src.MaskerForProject(ctx, projectID)
}
