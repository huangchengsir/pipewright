package qualitygate

import (
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/testreport"
)

func TestEvaluate_NoThresholds_AlwaysPass(t *testing.T) {
	th := Thresholds{MaxFailures: NoCheck, MinCoverage: float64(NoCheck)}
	if th.Enabled() {
		t.Error("Enabled() = true, want false for no-check thresholds")
	}
	v := Evaluate(testreport.Summary{Total: 3, Failed: 2}, th)
	if !v.Passed {
		t.Errorf("verdict = %+v, want passed even with failures (no gate)", v)
	}
}

func TestEvaluate_FailuresExceed(t *testing.T) {
	th := Thresholds{MaxFailures: 0, MinCoverage: float64(NoCheck)}
	v := Evaluate(testreport.Summary{Total: 5, Failed: 2, Passed: 3}, th)
	if v.Passed {
		t.Fatalf("verdict = %+v, want blocked", v)
	}
	if !strings.Contains(v.Reason(), "失败用例 2") {
		t.Errorf("reason = %q, want mention of 2 failures", v.Reason())
	}
}

func TestEvaluate_FailuresWithinLimit(t *testing.T) {
	th := Thresholds{MaxFailures: 3, MinCoverage: float64(NoCheck)}
	v := Evaluate(testreport.Summary{Total: 10, Failed: 2}, th)
	if !v.Passed {
		t.Errorf("verdict = %+v, want passed (2 <= 3)", v)
	}
}

func TestEvaluate_CoverageBelowMin(t *testing.T) {
	th := Thresholds{MaxFailures: NoCheck, MinCoverage: 80}
	v := Evaluate(testreport.Summary{Total: 4, Coverage: 72.5}, th)
	if v.Passed {
		t.Fatalf("verdict = %+v, want blocked for low coverage", v)
	}
	if !strings.Contains(v.Reason(), "72.5") {
		t.Errorf("reason = %q, want mention of 72.5%%", v.Reason())
	}
}

func TestEvaluate_CoverageMetExactly(t *testing.T) {
	th := Thresholds{MaxFailures: NoCheck, MinCoverage: 80}
	v := Evaluate(testreport.Summary{Total: 4, Coverage: 80}, th)
	if !v.Passed {
		t.Errorf("verdict = %+v, want passed (80 >= 80)", v)
	}
}

func TestEvaluate_CoverageRequiredButMissing(t *testing.T) {
	th := Thresholds{MaxFailures: NoCheck, MinCoverage: 80}
	v := Evaluate(testreport.Summary{Total: 4, Coverage: testreport.CoverageUnknown}, th)
	if v.Passed {
		t.Fatalf("verdict = %+v, want blocked when coverage required but absent", v)
	}
}

func TestEvaluate_BothViolated(t *testing.T) {
	th := Thresholds{MaxFailures: 0, MinCoverage: 90}
	v := Evaluate(testreport.Summary{Total: 5, Failed: 1, Coverage: 50}, th)
	if v.Passed || len(v.Violations) != 2 {
		t.Errorf("verdict = %+v, want blocked with 2 violations", v)
	}
}
