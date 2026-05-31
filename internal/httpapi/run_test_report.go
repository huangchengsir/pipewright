package httpapi

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/run"
)

// run_test_report.go 暴露测试报告 + 质量门禁汇总只读端点(Epic 8 · Story 8-6 / FR-8-6)。
//
// GET /api/runs/{id}/test-report → { reports: [...], gate: {...} }
// reports 为各阶段产出的测试报告汇总(通过/失败/跳过/覆盖率);gate 为整次运行的门禁裁决聚合
// (任一阶段门禁不过 → 整体 blocked,reason 汇总)。run 不存在 → 404。

// testReportDTO 是单条阶段测试报告(camelCase)。
type testReportDTO struct {
	ID          string  `json:"id"`
	StageID     string  `json:"stageId"`
	StageName   string  `json:"stageName"`
	Format      string  `json:"format"`      // junit
	Total       int     `json:"total"`       // 用例总数
	Passed      int     `json:"passed"`      //
	Failed      int     `json:"failed"`      //
	Skipped     int     `json:"skipped"`     //
	DurationSec float64 `json:"durationSec"` // 套件总耗时(秒)
	Coverage    float64 `json:"coverage"`    // 行覆盖率%(-1 = 未提供)
	GateEnabled bool    `json:"gateEnabled"`
	GatePassed  bool    `json:"gatePassed"`
	GateReason  string  `json:"gateReason"` // 门禁阻断原因(无 secret;仅数字)
	CreatedAt   string  `json:"createdAt"`  // RFC3339
}

// gateVerdictDTO 是整次运行的门禁聚合裁决。
type gateVerdictDTO struct {
	Enabled bool     `json:"enabled"` // 是否有任一阶段声明了门禁
	Passed  bool     `json:"passed"`  // 全部门禁是否通过(无门禁 → true)
	Reasons []string `json:"reasons"` // 各阶段阻断原因(通过 → [])
}

// runTestReportDTO 是 GET /api/runs/{id}/test-report 响应体。
type runTestReportDTO struct {
	Reports []testReportDTO `json:"reports"`
	Gate    gateVerdictDTO  `json:"gate"`
}

func toTestReportDTO(tr run.TestReport) testReportDTO {
	return testReportDTO{
		ID:          tr.ID,
		StageID:     tr.StageID,
		StageName:   tr.StageName,
		Format:      tr.Format,
		Total:       tr.Total,
		Passed:      tr.Passed,
		Failed:      tr.Failed,
		Skipped:     tr.Skipped,
		DurationSec: tr.DurationSec,
		Coverage:    tr.Coverage,
		GateEnabled: tr.GateEnabled,
		GatePassed:  tr.GatePassed,
		GateReason:  tr.GateReason,
		CreatedAt:   tr.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// makeRunTestReportHandler 返回 GET /api/runs/{id}/test-report handler(认证;只读)。
// run 不存在 → 404 run_not_found。无报告 → { reports: [], gate: {enabled:false, passed:true} }。
func makeRunTestReportHandler(svc run.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "运行服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")

		// run 存在性:不存在 → 404(ListTestReports 对不存在 run 返回空,不报错)。
		if _, err := svc.Get(r.Context(), id); err != nil {
			writeRunError(w, err)
			return
		}

		reports, err := svc.ListTestReports(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
			return
		}

		dtos := make([]testReportDTO, 0, len(reports))
		gate := gateVerdictDTO{Passed: true, Reasons: []string{}}
		for i := range reports {
			dtos = append(dtos, toTestReportDTO(reports[i]))
			if reports[i].GateEnabled {
				gate.Enabled = true
				if !reports[i].GatePassed {
					gate.Passed = false
					if reports[i].GateReason != "" {
						gate.Reasons = append(gate.Reasons, reports[i].GateReason)
					}
				}
			}
		}
		writeJSON(w, http.StatusOK, runTestReportDTO{Reports: dtos, Gate: gate})
	}
}
