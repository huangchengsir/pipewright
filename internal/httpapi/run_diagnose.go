package httpapi

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/ai"
	"github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/run"
)

// ---- 冻结 diagnosis 子 DTO(Story 7.2 定义并冻结;camelCase) ------------------
//
// 形状定死:status/reason/hypothesis/confidence/alternateCauses/fixSuggestions/
// evidence[{line,text,highlight}]/generatedAt。run-detail 外层不变(diagnosis null→填值)。

type diagnosisEvidenceDTO struct {
	Line      int    `json:"line"`
	Text      string `json:"text"`
	Highlight bool   `json:"highlight"`
}

// diagnosisDTO 是冻结的 diagnosis 子 DTO。status≠ready 时 hypothesis 等为空、reason 人读。
type diagnosisDTO struct {
	Status          string                 `json:"status"` // ready | unavailable | pending
	Reason          string                 `json:"reason"`
	Hypothesis      string                 `json:"hypothesis"`
	Confidence      string                 `json:"confidence"` // high | medium | low
	AlternateCauses []string               `json:"alternateCauses"`
	FixSuggestions  []string               `json:"fixSuggestions"`
	Evidence        []diagnosisEvidenceDTO `json:"evidence"`
	GeneratedAt     string                 `json:"generatedAt"`
}

// toDiagnosisDTO 把领域 run.Diagnosis 映射为冻结 DTO(nil → nil,run-detail diagnosis=null)。
func toDiagnosisDTO(d *run.Diagnosis) *diagnosisDTO {
	if d == nil {
		return nil
	}
	alt := d.AlternateCauses
	if alt == nil {
		alt = []string{}
	}
	fixes := d.FixSuggestions
	if fixes == nil {
		fixes = []string{}
	}
	ev := make([]diagnosisEvidenceDTO, 0, len(d.Evidence))
	for _, e := range d.Evidence {
		ev = append(ev, diagnosisEvidenceDTO{Line: e.Line, Text: e.Text, Highlight: e.Highlight})
	}
	return &diagnosisDTO{
		Status:          d.Status,
		Reason:          d.Reason,
		Hypothesis:      d.Hypothesis,
		Confidence:      d.Confidence,
		AlternateCauses: alt,
		FixSuggestions:  fixes,
		Evidence:        ev,
		GeneratedAt:     d.GeneratedAt.UTC().Format(time.RFC3339),
	}
}

// aiToRunDiagnosis 把 ai.Diagnosis 映射为 run.Diagnosis(领域搬运形状;run 包不 import ai)。
func aiToRunDiagnosis(d *ai.Diagnosis) *run.Diagnosis {
	if d == nil {
		return nil
	}
	ev := make([]run.DiagnosisEvidence, 0, len(d.Evidence))
	for _, e := range d.Evidence {
		ev = append(ev, run.DiagnosisEvidence{Line: e.Line, Text: e.Text, Highlight: e.Highlight})
	}
	return &run.Diagnosis{
		Status:          d.Status,
		Reason:          d.Reason,
		Hypothesis:      d.Hypothesis,
		Confidence:      d.Confidence,
		AlternateCauses: d.AlternateCauses,
		FixSuggestions:  d.FixSuggestions,
		Evidence:        ev,
		GeneratedAt:     d.GeneratedAt,
	}
}

// diagnoseRun 是「取失败日志 → 脱敏登记 → ai.Diagnose → 保存」的共享编排,
// 供 POST /api/runs/{id}/diagnose handler 与 worker best-effort 自动诊断钩子复用。
//
// **出网前脱敏铁律**:为本次 run 新建一个 Masker,把失败日志中已知的敏感串登记进去后
// 再交给 ai.Diagnose(后者在出网前 Scrub)。本期无凭据级 secret 注入点,故由 ai 层据
// Masker 脱敏;桩日志中的假 secret 在此登记(下方 registerRunSecrets),以验脱敏链路。
//
// 优雅降级:ai.Diagnose 对所有 AI 不可用路径返回 status=unavailable 的诊断(非 error);
// 故本函数几乎总返回一个非 nil 诊断。保存失败仅返回 error 供 handler 决策(钩子忽略)。
//
// persistUnavailable 控制是否把**非 ready**(unavailable)诊断落库:
//   - 显式端点 POST /diagnose 传 true:用户主动请求,落库以便刷新仍能回显「诊断不可用 + reason」。
//   - 自动钩子传 false:仅落 ready 诊断,避免污染「diagnosis=null 即未诊断」干净态、并杜绝
//     「AI 未配 / 用户取消(取消复用 StatusFailed)的每个失败 run 都无谓写一条 unavailable」。
func diagnoseRun(ctx context.Context, runs run.Service, aiSvc ai.Service, id string, persistUnavailable bool) (*run.Diagnosis, error) {
	failureLog, err := runs.GetFailureLog(ctx, id)
	if err != nil {
		return nil, err // ErrNotFound 等,由调用方映射
	}

	masker := mask.NewMasker()
	registerRunSecrets(masker, failureLog)

	var diag *ai.Diagnosis
	if aiSvc != nil {
		diag, _ = aiSvc.Diagnose(ctx, ai.DiagnoseInput{
			FailureLog: failureLog,
			Masker:     masker,
		})
	}
	if diag == nil {
		// aiSvc 未注入:同样降级为 unavailable(绝不 500 / 绝无密钥)。
		diag = &ai.Diagnosis{
			Status:          "unavailable",
			Reason:          "AI 未配置或未启用,无法生成诊断",
			AlternateCauses: []string{},
			FixSuggestions:  []string{},
			Evidence:        []ai.DiagnosisEvidence{},
			GeneratedAt:     time.Now().UTC(),
		}
	}

	domain := aiToRunDiagnosis(diag)
	// 自动钩子(persistUnavailable=false)只持久化 ready 诊断:非 ready 直接返回不落库,
	// 保持 run-detail diagnosis=null 干净态(前端显「触发诊断」而非恒「诊断不可用」),
	// 并消除 AI 未配/取消场景下每个失败 run 的无谓写。显式端点(true)仍落库供刷新回显。
	if domain.Status != "ready" && !persistUnavailable {
		return domain, nil
	}
	if serr := runs.SaveDiagnosis(ctx, id, domain); serr != nil {
		return domain, serr
	}
	return domain, nil
}

// registerRunSecrets 把失败日志中**已知的敏感串**登记进 Masker(出网前脱敏)。
//
// 真实日志的凭据登记点在 3-3/3-6 落地后接入(届时从 vault 取该 run 用到的凭据明文登记)。
// 本期日志为桩合成,内嵌一个固定假 secret(run.StubFailureSecret)以验脱敏链路端到端有效;
// 此处据该已知串登记,使 prompt/响应/evidence/DB 中它一律 [MASKED]。
func registerRunSecrets(m *mask.Masker, failureLog string) {
	_ = failureLog
	m.RegisterSecret(run.StubFailureSecret)
}

// NewDiagnoseHook 把 run.Service + ai.Service 适配为 run.WorkerPool 的 best-effort 自动诊断钩子
// (供 main 装配,使 run 包不 import ai;仿 NewRunCreator 解耦)。
//
// 钩子在 run→failed 后被 worker 在独立 goroutine 调用:它「取失败日志 → 脱敏 → ai.Diagnose →
// 保存」,自带超时(防黑洞 LLM 把 goroutine 挂死);失败仅记日志,**绝不影响 run 终态**。
func NewDiagnoseHook(runs run.Service, aiSvc ai.Service) func(ctx context.Context, runID string) {
	return func(ctx context.Context, runID string) {
		if runs == nil {
			return
		}
		// best-effort:自带超时;父 ctx 取消(停机)时一并取消。
		hookCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		// 自动诊断:仅持久化 ready 结果(persistUnavailable=false),不污染未诊断干净态。
		if _, err := diagnoseRun(hookCtx, runs, aiSvc, runID, false); err != nil {
			log.Printf("[diagnose] run %s: auto-diagnose best-effort failed: %v", runID, err)
		}
	}
}

// makeDiagnoseRunHandler 返回 POST /api/runs/{id}/diagnose handler(认证 + CSRF)。
//
// 取 run:非失败态 → 422 run_not_failed;不存在 → 404。失败态 → 编排诊断(取日志→脱敏→
// ai.Diagnose→保存)→ 返回 diagnosis 子 DTO。**任何 LLM 失败均 200 + status=unavailable,
// 绝不 500、绝无密钥。**
func makeDiagnoseRunHandler(runs run.Service, aiSvc ai.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runs == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "运行服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		ctx := r.Context()

		rn, err := runs.Get(ctx, id)
		if err != nil {
			writeRunError(w, err) // 不存在 → 404
			return
		}
		// 仅失败态可诊断(本期诊断针对失败运行)。
		if rn.Status != run.StatusFailed {
			writeError(w, http.StatusUnprocessableEntity, "run_not_failed", "仅失败的运行可进行诊断")
			return
		}

		// 显式诊断:用户主动请求,落库 unavailable 也无妨(刷新仍能回显 reason)。
		domain, derr := diagnoseRun(ctx, runs, aiSvc, id, true)
		if derr != nil {
			if errors.Is(derr, run.ErrNotFound) {
				writeError(w, http.StatusNotFound, "run_not_found", "运行不存在")
				return
			}
			// 保存等内部错误:仍不暴露细节;但诊断本身已生成,尽力回显(无则 500 兜底)。
			if domain != nil {
				writeJSON(w, http.StatusOK, toDiagnosisDTO(domain))
				return
			}
			writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
			return
		}
		writeJSON(w, http.StatusOK, toDiagnosisDTO(domain))
	}
}
