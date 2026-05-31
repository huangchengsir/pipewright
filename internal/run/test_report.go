package run

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// test_report.go 是「测试报告 + 质量门禁」的持久层(Epic 8 · Story 8-6 / FR-8-6)。
//
// script 步骤产出测试报告(JUnit XML / 可选 Cobertura 覆盖率)后,阶段执行器解析出
// 通过/失败/跳过计数 + 覆盖率% + 门禁裁决,经本层存一条汇总(每阶段一条)。
// httpapi run-detail 子端点据此展示;门禁阻断语义由阶段失败本身承载,本层只搬运形状。

// TestReport 是一次运行(某阶段)的测试报告汇总领域模型。
//
//   - Format       : 报告格式(目前固定 "junit")。
//   - Total/Passed/Failed/Skipped : 用例计数。
//   - DurationSec  : 套件总耗时(秒)。
//   - Coverage     : 行覆盖率百分比(0~100;-1 = 未提供)。
//   - GateEnabled  : 是否声明了质量门禁阈值。
//   - GatePassed   : 门禁是否通过(无门禁视为通过)。
//   - GateReason   : 门禁阻断原因(人读;不含 secret;仅计数/阈值数字)。
type TestReport struct {
	ID          string
	RunID       string
	StageID     string
	StageName   string
	Format      string
	Total       int
	Passed      int
	Failed      int
	Skipped     int
	DurationSec float64
	Coverage    float64
	GateEnabled bool
	GatePassed  bool
	GateReason  string
	CreatedAt   time.Time
}

// SaveTestReport 持久化一条测试报告汇总(参数化 SQL)。
func (s *service) SaveTestReport(ctx context.Context, tr TestReport) (*TestReport, error) {
	if tr.ID == "" {
		tr.ID = uuid.NewString()
	}
	if tr.CreatedAt.IsZero() {
		tr.CreatedAt = time.Now().UTC()
	}
	if strings.TrimSpace(tr.Format) == "" {
		tr.Format = "junit"
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO run_test_reports
		   (id, run_id, stage_id, stage_name, format, total, passed, failed, skipped,
		    duration_sec, coverage_pct, gate_enabled, gate_passed, gate_reason, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		tr.ID, tr.RunID, tr.StageID, tr.StageName, tr.Format,
		tr.Total, tr.Passed, tr.Failed, tr.Skipped, tr.DurationSec, tr.Coverage,
		boolToInt(tr.GateEnabled), boolToInt(tr.GatePassed), tr.GateReason,
		tr.CreatedAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		if isForeignKeyErr(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("run: insert test report: %w", err)
	}
	out := tr
	out.CreatedAt = tr.CreatedAt.UTC()
	return &out, nil
}

// ListTestReports 取某次运行的全部测试报告汇总(按 created_at 升序,rowid tiebreaker)。
func (s *service) ListTestReports(ctx context.Context, runID string) ([]TestReport, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, run_id, stage_id, stage_name, format, total, passed, failed, skipped,
		        duration_sec, coverage_pct, gate_enabled, gate_passed, gate_reason, created_at
		 FROM run_test_reports WHERE run_id = ? ORDER BY created_at ASC, rowid ASC`, runID)
	if err != nil {
		return nil, fmt.Errorf("run: load test reports: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []TestReport{}
	for rows.Next() {
		var (
			tr          TestReport
			gateEnabled int
			gatePassed  int
			createdStr  string
		)
		if err := rows.Scan(&tr.ID, &tr.RunID, &tr.StageID, &tr.StageName, &tr.Format,
			&tr.Total, &tr.Passed, &tr.Failed, &tr.Skipped, &tr.DurationSec, &tr.Coverage,
			&gateEnabled, &gatePassed, &tr.GateReason, &createdStr); err != nil {
			return nil, fmt.Errorf("run: scan test report: %w", err)
		}
		tr.GateEnabled = gateEnabled != 0
		tr.GatePassed = gatePassed != 0
		if t, perr := time.Parse(time.RFC3339, createdStr); perr == nil {
			tr.CreatedAt = t.UTC()
		}
		out = append(out, tr)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("run: iterate test reports: %w", err)
	}
	return out, nil
}

// boolToInt 把布尔映射为 0/1(sqlite 无原生布尔)。
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
