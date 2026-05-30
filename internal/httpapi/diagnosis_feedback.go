package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/run"
)

// diagnosis_feedback.go 是「诊断反馈闭环」HTTP 层(FR-26 / Story 7.5)。
//
// POST /api/runs/{id}/diagnosis/feedback(认证 + CSRF):对一条 ready 诊断 👍/👎(👎 可附正确根因)。
//   - run 不存在 → 404;run 无诊断 → 422 no_diagnosis;同 run 重复提交 = 覆盖(upsert by runID)。
//   - correctRootCause 脱敏(过 mask.Masker 尽力)+ 长度上限(领域层兜底截断)。
// GET /api/settings/diagnosis-stats(认证,只读):准确率/计数/最近趋势/最近 corrections。
//
// **冻结点**:feedback 请求(verdict/correctRootCause)+ stats DTO 形状定死;知识库检索(后续)
// 只消费这些数据,不改形状。

// ---- 冻结请求 / DTO(camelCase) -------------------------------------------------

// feedbackRequest 是 POST feedback 请求体(冻结:verdict + 可选 correctRootCause)。
type feedbackRequest struct {
	Verdict          string `json:"verdict"`          // up | down
	CorrectRootCause string `json:"correctRootCause"` // 可选,仅 down 有意义
}

// diagnosisStatsDTO 是 GET diagnosis-stats 响应体(冻结形状)。
// 无反馈 → 全 0 / 空数组 / accuracy:null(不报错)。
type diagnosisStatsDTO struct {
	TotalFeedback     int              `json:"totalFeedback"`
	ThumbsUp          int              `json:"thumbsUp"`
	ThumbsDown        int              `json:"thumbsDown"`
	Accuracy          *float64         `json:"accuracy"` // up/total;无反馈 → null
	RecentTrend       []trendBucketDTO `json:"recentTrend"`
	RecentCorrections []correctionDTO  `json:"recentCorrections"`
}

type trendBucketDTO struct {
	Period   string  `json:"period"`
	Accuracy float64 `json:"accuracy"`
	Count    int     `json:"count"`
}

type correctionDTO struct {
	RunID            string `json:"runId"`
	CorrectRootCause string `json:"correctRootCause"` // 已脱敏
	At               string `json:"at"`               // RFC3339
}

// makeSubmitDiagnosisFeedbackHandler 返回 POST /api/runs/{id}/diagnosis/feedback handler(认证 + CSRF)。
//
// 取 run:不存在 → 404。取诊断:无诊断(nil)→ 422 no_diagnosis(不创建空反馈)。
// 有诊断 → 脱敏 correctRootCause(尽力)+ 快照诊断 → upsert by runID(同 run 改判覆盖)→ 200 {ok:true}。
// verdict 非法(非 up|down)→ 422 invalid_verdict。
func makeSubmitDiagnosisFeedbackHandler(runs run.Service, fb run.FeedbackService, secretSrc *RunSecretSource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runs == nil || fb == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "反馈服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		ctx := r.Context()

		// run 存在性 + 取已持久化诊断(无诊断 → 422 no_diagnosis,不创建空反馈)。
		diag, err := runs.GetDiagnosis(ctx, id)
		if err != nil {
			writeRunError(w, err) // 不存在 → 404
			return
		}
		if diag == nil {
			writeError(w, http.StatusUnprocessableEntity, "no_diagnosis", "该运行尚无 AI 诊断,无法反馈")
			return
		}

		// 限请求体大小,防超大 body。
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		var req feedbackRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}

		// 脱敏 correctRootCause:登记该 run 真实用到的全部凭据(项目/registry/secret 变量)+ 桩 secret,
		// 再 Scrub 用户输入(绝无明文 secret 入库/回吐)。secretSrc 为 nil 时降级只登记桩。
		masker := maskerFor(ctx, secretSrc, id)
		correct := masker.Scrub(req.CorrectRootCause)

		// 诊断快照(脱敏后落 pipeline_runs.diagnosis_json 的诊断;此处经 DTO 再编码为快照)。
		snapshot := encodeDiagnosisSnapshot(diag)

		_, serr := fb.SaveFeedback(ctx, run.SaveFeedbackInput{
			RunID:             id,
			Verdict:           req.Verdict,
			CorrectRootCause:  correct,
			DiagnosisSnapshot: snapshot,
		})
		if serr != nil {
			switch {
			case errors.Is(serr, run.ErrInvalidVerdict):
				writeError(w, http.StatusUnprocessableEntity, "invalid_verdict", "verdict 只能为 up 或 down")
			case errors.Is(serr, run.ErrNotFound):
				writeError(w, http.StatusNotFound, "run_not_found", "运行不存在")
			default:
				writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
			}
			return
		}

		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

// makeDiagnosisStatsHandler 返回 GET /api/settings/diagnosis-stats handler(认证,只读)。
// 无反馈 → 全 0 / 空数组 / accuracy:null(绝不报错)。
func makeDiagnosisStatsHandler(fb run.FeedbackService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if fb == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "反馈服务未初始化")
			return
		}
		stats, err := fb.GetStats(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
			return
		}
		writeJSON(w, http.StatusOK, toDiagnosisStatsDTO(stats))
	}
}

// toDiagnosisStatsDTO 把领域统计映射为冻结 DTO(空切片而非 null;at 编为 RFC3339)。
func toDiagnosisStatsDTO(s *run.FeedbackStats) diagnosisStatsDTO {
	dto := diagnosisStatsDTO{
		TotalFeedback:     s.TotalFeedback,
		ThumbsUp:          s.ThumbsUp,
		ThumbsDown:        s.ThumbsDown,
		Accuracy:          s.Accuracy,
		RecentTrend:       make([]trendBucketDTO, 0, len(s.RecentTrend)),
		RecentCorrections: make([]correctionDTO, 0, len(s.RecentCorrections)),
	}
	for _, t := range s.RecentTrend {
		dto.RecentTrend = append(dto.RecentTrend, trendBucketDTO{
			Period:   t.Period,
			Accuracy: t.Accuracy,
			Count:    t.Count,
		})
	}
	for _, c := range s.RecentCorrections {
		dto.RecentCorrections = append(dto.RecentCorrections, correctionDTO{
			RunID:            c.RunID,
			CorrectRootCause: c.CorrectRootCause,
			At:               c.At.UTC().Format(time.RFC3339),
		})
	}
	return dto
}

// encodeDiagnosisSnapshot 把已脱敏的领域诊断编码为快照 JSON(经冻结 diagnosis 子 DTO,
// 与 run-detail diagnosis slot 同形)。诊断已脱敏(落库前过 mask),故快照绝无明文 secret。
// 编码失败 → 空串(快照非关键,不阻断反馈)。
func encodeDiagnosisSnapshot(d *run.Diagnosis) string {
	dto := toDiagnosisDTO(d)
	if dto == nil {
		return ""
	}
	b, err := json.Marshal(dto)
	if err != nil {
		return ""
	}
	return string(b)
}
