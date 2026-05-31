package dora

import (
	"math"
	"testing"
	"time"
)

// base 是测试用的固定参照时刻(UTC)。
var base = time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

// at 返回 base + d 的指针(便于构造 FinishedAt/CommitTime)。
func at(d time.Duration) *time.Time {
	t := base.Add(d)
	return &t
}

// success 构造一条成功终态运行:created 在 commitOffset,finished 在 finishOffset。
func success(createdOffset, finishOffset time.Duration) Record {
	return Record{Status: statusSuccess, CreatedAt: base.Add(createdOffset), FinishedAt: at(finishOffset)}
}

// failed 构造一条失败终态运行。
func failed(status string, createdOffset, finishOffset time.Duration) Record {
	return Record{Status: status, CreatedAt: base.Add(createdOffset), FinishedAt: at(finishOffset)}
}

const eps = 1e-9

func almostEqual(a, b float64) bool { return math.Abs(a-b) < eps }

func TestCompute_Empty(t *testing.T) {
	m := Compute(nil, 30)
	if m.TotalDeployments != 0 || m.SuccessfulDeployments != 0 || m.FailedDeployments != 0 {
		t.Fatalf("empty: counts not zero: %+v", m)
	}
	if m.DeploymentFrequencyPerDay != 0 || m.LeadTimeSeconds != 0 || m.ChangeFailureRate != 0 || m.MTTRSeconds != 0 {
		t.Fatalf("empty: metrics not zero: %+v", m)
	}
	// 空窗口各指标分档应为 None(样本不足)。
	if m.DeploymentFrequencyBand != BandNone || m.LeadTimeBand != BandNone ||
		m.ChangeFailureRateBand != BandNone || m.MTTRBand != BandNone {
		t.Fatalf("empty: bands not None: %+v", m)
	}
}

func TestCompute_IgnoresNonTerminal(t *testing.T) {
	recs := []Record{
		{Status: "queued", CreatedAt: base},           // FinishedAt nil → 忽略
		{Status: "running", CreatedAt: base},          // 忽略
		{Status: "waiting_approval", CreatedAt: base}, // 忽略
		{Status: statusSuccess, CreatedAt: base},      // FinishedAt nil → 即便 success 也忽略(未结束)
		success(0, time.Hour),                         // 唯一计入的部署
	}
	m := Compute(recs, 30)
	if m.TotalDeployments != 1 || m.SuccessfulDeployments != 1 {
		t.Fatalf("expected 1 terminal deployment, got %+v", m)
	}
}

func TestCompute_DeploymentFrequency(t *testing.T) {
	// 30 天窗口内 15 次成功部署 → 0.5/天 → 3.5/周。
	recs := make([]Record, 0, 15)
	for i := 0; i < 15; i++ {
		recs = append(recs, success(time.Duration(i)*time.Hour, time.Duration(i)*time.Hour+time.Hour))
	}
	m := Compute(recs, 30)
	if !almostEqual(m.DeploymentFrequencyPerDay, 0.5) {
		t.Fatalf("perDay = %v, want 0.5", m.DeploymentFrequencyPerDay)
	}
	if !almostEqual(m.DeploymentFrequencyPerWeek, 3.5) {
		t.Fatalf("perWeek = %v, want 3.5", m.DeploymentFrequencyPerWeek)
	}
}

func TestCompute_WindowDaysClampedToOne(t *testing.T) {
	m := Compute([]Record{success(0, time.Hour)}, 0)
	if m.WindowDays != 1 {
		t.Fatalf("windowDays clamp = %v, want 1", m.WindowDays)
	}
	if !almostEqual(m.DeploymentFrequencyPerDay, 1) {
		t.Fatalf("perDay = %v, want 1", m.DeploymentFrequencyPerDay)
	}
}

func TestCompute_LeadTimeMedian_UsesCommitWhenPresent(t *testing.T) {
	// 三条成功:lead = 1h, 2h, 4h → 中位数 2h。
	r1 := Record{Status: statusSuccess, CreatedAt: base, CommitTime: at(0), FinishedAt: at(time.Hour)}
	r2 := Record{Status: statusSuccess, CreatedAt: base, CommitTime: at(0), FinishedAt: at(2 * time.Hour)}
	r3 := Record{Status: statusSuccess, CreatedAt: base, CommitTime: at(0), FinishedAt: at(4 * time.Hour)}
	m := Compute([]Record{r1, r2, r3}, 30)
	if m.LeadTimeSampleCount != 3 {
		t.Fatalf("leadTime sample = %d, want 3", m.LeadTimeSampleCount)
	}
	if !almostEqual(m.LeadTimeSeconds, (2 * time.Hour).Seconds()) {
		t.Fatalf("leadTime = %v, want %v", m.LeadTimeSeconds, (2 * time.Hour).Seconds())
	}
}

func TestCompute_LeadTimeFallsBackToCreatedAt(t *testing.T) {
	// 无 CommitTime → 用 CreatedAt 当提交代理:created=base, finished=base+3h → lead 3h。
	r := success(0, 3*time.Hour) // createdOffset 0 → CreatedAt = base
	m := Compute([]Record{r}, 30)
	if !almostEqual(m.LeadTimeSeconds, (3 * time.Hour).Seconds()) {
		t.Fatalf("leadTime fallback = %v, want %v", m.LeadTimeSeconds, (3 * time.Hour).Seconds())
	}
}

func TestCompute_LeadTimeEvenMedian(t *testing.T) {
	// 偶数个样本 lead = 1h, 3h → 中位数 (1+3)/2 = 2h。
	r1 := success(0, time.Hour)
	r2 := success(0, 3*time.Hour)
	m := Compute([]Record{r1, r2}, 30)
	if !almostEqual(m.LeadTimeSeconds, (2 * time.Hour).Seconds()) {
		t.Fatalf("even median = %v, want %v", m.LeadTimeSeconds, (2 * time.Hour).Seconds())
	}
}

func TestCompute_LeadTimeNegativeDiscarded(t *testing.T) {
	// finished 早于 commit(脏数据)→ 该样本丢弃;只剩一条有效 lead=2h。
	dirty := Record{Status: statusSuccess, CreatedAt: base, CommitTime: at(5 * time.Hour), FinishedAt: at(time.Hour)}
	good := Record{Status: statusSuccess, CreatedAt: base, CommitTime: at(0), FinishedAt: at(2 * time.Hour)}
	m := Compute([]Record{dirty, good}, 30)
	if m.LeadTimeSampleCount != 1 {
		t.Fatalf("expected 1 valid lead sample, got %d", m.LeadTimeSampleCount)
	}
	if !almostEqual(m.LeadTimeSeconds, (2 * time.Hour).Seconds()) {
		t.Fatalf("leadTime = %v, want 2h", m.LeadTimeSeconds)
	}
	// 但 dirty 仍计入成功部署数(它确实成功了,只是 lead 不可信)。
	if m.SuccessfulDeployments != 2 {
		t.Fatalf("successful deployments = %d, want 2", m.SuccessfulDeployments)
	}
}

func TestCompute_ChangeFailureRate(t *testing.T) {
	// 2 成功 + 1 failed + 1 partial_failed + 1 rolled_back → 失败 3 / 总 5 = 0.6。
	recs := []Record{
		success(0, time.Hour),
		success(0, time.Hour),
		failed(statusFailed, 0, time.Hour),
		failed(statusPartialFailed, 0, time.Hour),
		failed(statusRolledBack, 0, time.Hour),
	}
	m := Compute(recs, 30)
	if m.TotalDeployments != 5 || m.FailedDeployments != 3 || m.SuccessfulDeployments != 2 {
		t.Fatalf("counts = %+v", m)
	}
	if !almostEqual(m.ChangeFailureRate, 0.6) {
		t.Fatalf("CFR = %v, want 0.6", m.ChangeFailureRate)
	}
}

func TestCompute_ChangeFailureRate_DivisionByZeroSafe(t *testing.T) {
	// 只有非终态运行 → 0 部署 → CFR 0(非 NaN)。
	m := Compute([]Record{{Status: "running", CreatedAt: base}}, 30)
	if m.ChangeFailureRate != 0 || math.IsNaN(m.ChangeFailureRate) {
		t.Fatalf("CFR = %v, want 0", m.ChangeFailureRate)
	}
}

func TestCompute_MTTR_SinglePair(t *testing.T) {
	// 失败@1h → 成功@4h → 恢复 3h。
	recs := []Record{
		failed(statusFailed, 0, time.Hour),
		success(0, 4*time.Hour),
	}
	m := Compute(recs, 30)
	if m.MTTRSampleCount != 1 {
		t.Fatalf("MTTR sample = %d, want 1", m.MTTRSampleCount)
	}
	if !almostEqual(m.MTTRSeconds, (3 * time.Hour).Seconds()) {
		t.Fatalf("MTTR = %v, want 3h", m.MTTRSeconds)
	}
}

func TestCompute_MTTR_ConsecutiveFailuresUseEarliest(t *testing.T) {
	// 失败@1h, 失败@2h, 成功@5h → 故障从首次失败(1h)算起 → 恢复 4h(只一对)。
	recs := []Record{
		failed(statusFailed, 0, time.Hour),
		failed(statusFailed, 0, 2*time.Hour),
		success(0, 5*time.Hour),
	}
	m := Compute(recs, 30)
	if m.MTTRSampleCount != 1 {
		t.Fatalf("MTTR sample = %d, want 1 (consecutive failures collapse)", m.MTTRSampleCount)
	}
	if !almostEqual(m.MTTRSeconds, (4 * time.Hour).Seconds()) {
		t.Fatalf("MTTR = %v, want 4h", m.MTTRSeconds)
	}
}

func TestCompute_MTTR_MultiplePairsMedian(t *testing.T) {
	// 段1: 失败@1h → 成功@3h(2h);段2: 失败@5h → 成功@9h(4h)。中位数(2 样本)=(2+4)/2=3h。
	recs := []Record{
		failed(statusFailed, 0, time.Hour),
		success(0, 3*time.Hour),
		failed(statusRolledBack, 0, 5*time.Hour),
		success(0, 9*time.Hour),
	}
	m := Compute(recs, 30)
	if m.MTTRSampleCount != 2 {
		t.Fatalf("MTTR sample = %d, want 2", m.MTTRSampleCount)
	}
	if !almostEqual(m.MTTRSeconds, (3 * time.Hour).Seconds()) {
		t.Fatalf("MTTR median = %v, want 3h", m.MTTRSeconds)
	}
}

func TestCompute_MTTR_DanglingFailureNoPair(t *testing.T) {
	// 成功@1h → 失败@3h(末尾未恢复)→ 无配对。
	recs := []Record{
		success(0, time.Hour),
		failed(statusFailed, 0, 3*time.Hour),
	}
	m := Compute(recs, 30)
	if m.MTTRSampleCount != 0 || m.MTTRSeconds != 0 {
		t.Fatalf("dangling failure should yield no MTTR pair: %+v", m)
	}
	if m.MTTRBand != BandNone {
		t.Fatalf("MTTR band = %q, want none", m.MTTRBand)
	}
}

func TestCompute_MTTR_UnorderedInputSorted(t *testing.T) {
	// 乱序输入仍按完成时间排序:失败@1h → 成功@2h → 恢复 1h。
	recs := []Record{
		success(0, 2*time.Hour),
		failed(statusFailed, 0, time.Hour),
	}
	m := Compute(recs, 30)
	if !almostEqual(m.MTTRSeconds, time.Hour.Seconds()) {
		t.Fatalf("MTTR (unordered) = %v, want 1h", m.MTTRSeconds)
	}
}

func TestCompute_LeadingSuccessDoesNotCountAsRecovery(t *testing.T) {
	// 成功@1h(无前序失败)→ 失败@2h → 成功@4h:只有第二段配对(2h)。
	recs := []Record{
		success(0, time.Hour),
		failed(statusFailed, 0, 2*time.Hour),
		success(0, 4*time.Hour),
	}
	m := Compute(recs, 30)
	if m.MTTRSampleCount != 1 {
		t.Fatalf("MTTR sample = %d, want 1 (leading success ignored)", m.MTTRSampleCount)
	}
	if !almostEqual(m.MTTRSeconds, (2 * time.Hour).Seconds()) {
		t.Fatalf("MTTR = %v, want 2h", m.MTTRSeconds)
	}
}
