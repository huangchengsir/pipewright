package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/huangchengsir/pipewright/internal/run"
)

// TestRunTestReportEndpoint_Empty 验证无报告 run → { reports: [], gate:{enabled:false,passed:true} }。
func TestRunTestReportEndpoint_Empty(t *testing.T) {
	srv, client, csrf, projID, rsvc := setupRunServer(t)
	r, err := rsvc.Create(context.Background(), projID,
		run.Trigger{Type: run.TriggerManual, Branch: "main", Commit: "deadbeef", Actor: "admin"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	waitRunStatus(t, client, srv, csrf, r.ID, run.StatusSuccess)

	resp := doJSON(t, client, http.MethodGet, srv.URL+"/api/runs/"+r.ID+"/test-report", csrf, "")
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, raw)
	}
	var body struct {
		Reports []map[string]any `json:"reports"`
		Gate    struct {
			Enabled bool     `json:"enabled"`
			Passed  bool     `json:"passed"`
			Reasons []string `json:"reasons"`
		} `json:"gate"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, raw)
	}
	if len(body.Reports) != 0 {
		t.Errorf("reports = %v, want empty", body.Reports)
	}
	if body.Gate.Enabled || !body.Gate.Passed {
		t.Errorf("gate = %+v, want enabled=false passed=true", body.Gate)
	}
}

// TestRunTestReportEndpoint_WithReports 验证持久化的阶段报告 + 门禁聚合裁决正确出口。
func TestRunTestReportEndpoint_WithReports(t *testing.T) {
	srv, client, csrf, projID, rsvc := setupRunServer(t)
	r, err := rsvc.Create(context.Background(), projID,
		run.Trigger{Type: run.TriggerManual, Branch: "main", Commit: "deadbeef", Actor: "admin"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	waitRunStatus(t, client, srv, csrf, r.ID, run.StatusSuccess)

	// 一条通过门禁,一条阻断门禁。
	if _, err := rsvc.SaveTestReport(context.Background(), run.TestReport{
		RunID: r.ID, StageID: "s1", StageName: "单元测试", Format: "junit",
		Total: 10, Passed: 10, Failed: 0, Skipped: 0, Coverage: 85,
		GateEnabled: true, GatePassed: true,
	}); err != nil {
		t.Fatalf("SaveTestReport pass: %v", err)
	}
	if _, err := rsvc.SaveTestReport(context.Background(), run.TestReport{
		RunID: r.ID, StageID: "s2", StageName: "集成测试", Format: "junit",
		Total: 5, Passed: 3, Failed: 2, Coverage: -1,
		GateEnabled: true, GatePassed: false, GateReason: "失败用例 2 个,超过门禁上限 0",
	}); err != nil {
		t.Fatalf("SaveTestReport block: %v", err)
	}

	resp := doJSON(t, client, http.MethodGet, srv.URL+"/api/runs/"+r.ID+"/test-report", csrf, "")
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, raw)
	}
	var body struct {
		Reports []struct {
			StageName  string  `json:"stageName"`
			Total      int     `json:"total"`
			Failed     int     `json:"failed"`
			Coverage   float64 `json:"coverage"`
			GatePassed bool    `json:"gatePassed"`
		} `json:"reports"`
		Gate struct {
			Enabled bool     `json:"enabled"`
			Passed  bool     `json:"passed"`
			Reasons []string `json:"reasons"`
		} `json:"gate"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, raw)
	}
	if len(body.Reports) != 2 {
		t.Fatalf("reports = %d, want 2", len(body.Reports))
	}
	// 整体门禁:有阶段阻断 → enabled=true, passed=false, reasons 非空。
	if !body.Gate.Enabled || body.Gate.Passed || len(body.Gate.Reasons) != 1 {
		t.Errorf("gate = %+v, want enabled=true passed=false 1 reason", body.Gate)
	}
}

// TestRunTestReportEndpoint_NotFound 验证不存在 run → 404。
func TestRunTestReportEndpoint_NotFound(t *testing.T) {
	srv, client, csrf, _, _ := setupRunServer(t)
	resp := doJSON(t, client, http.MethodGet, srv.URL+"/api/runs/no-such-run/test-report", csrf, "")
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}
