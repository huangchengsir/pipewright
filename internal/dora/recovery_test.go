package dora

import (
	"testing"
	"time"
)

func TestRecoveryDoesNotInflateDeploymentCountsAndClosesOnlyItsFailure(t *testing.T) {
	root := failed(statusFailed, 0, time.Hour)
	root.RunID = "root"
	root.LogicalRunID = "root"
	root.ProjectID = "project"
	retry1 := failed(statusFailed, time.Hour, 2*time.Hour)
	retry1.RunID = "child1"
	retry1.ProjectID = "project"
	retry1.LogicalRunID = "root"
	retry1.ExecutionKind = "recovery"
	retry2 := success(2*time.Hour, 3*time.Hour)
	retry2.RunID = "child2"
	retry2.ProjectID = "project"
	retry2.LogicalRunID = "root"
	retry2.ExecutionKind = "recovery"
	m := Compute([]Record{root, retry1, retry2}, 7)
	if m.TotalDeployments != 1 || m.FailedDeployments != 1 || m.SuccessfulDeployments != 0 || m.LeadTimeSampleCount != 0 || m.RecoveryAttempts != 2 || m.SuccessfulRecoveries != 1 || m.MTTRSampleCount != 1 || m.MTTRSeconds != (2*time.Hour).Seconds() {
		t.Fatalf("recovery counted as new deployment: %+v", m)
	}
	trend := ComputeTrend([]Record{root, retry1, retry2}, base, base.Add(24*time.Hour), 7)
	total := 0
	for _, p := range trend {
		total += p.Deployments
	}
	if total != 1 {
		t.Fatal(total)
	}
	retry2.LogicalRunID = "unrelated"
	if got := Compute([]Record{root, retry2}, 7); got.MTTRSampleCount != 0 {
		t.Fatal("unrelated recovery closed incident")
	}
	retry2.LogicalRunID = "root"
	retry2.ProjectID = "another"
	if got := Compute([]Record{root, retry2}, 7); got.MTTRSampleCount != 0 {
		t.Fatal("another project's recovery closed incident")
	}
}
func TestMultipleFailedLogicalRunsMustAllRecover(t *testing.T) {
	a := failed(statusFailed, 0, time.Hour)
	a.RunID = "a"
	a.ProjectID = "p"
	b := failed(statusFailed, time.Hour, 2*time.Hour)
	b.RunID = "b"
	b.ProjectID = "p"
	ra := success(2*time.Hour, 3*time.Hour)
	ra.ExecutionKind = "recovery"
	ra.LogicalRunID = "a"
	ra.ProjectID = "p"
	rb := success(3*time.Hour, 4*time.Hour)
	rb.ExecutionKind = "recovery"
	rb.LogicalRunID = "b"
	rb.ProjectID = "p"
	if got := Compute([]Record{a, b, ra}, 7); got.MTTRSampleCount != 0 {
		t.Fatal("one recovered branch hid outstanding failure")
	}
	if got := Compute([]Record{a, b, ra, rb}, 7); got.MTTRSampleCount != 1 || got.MTTRSeconds != (3*time.Hour).Seconds() {
		t.Fatal(got)
	}
}
