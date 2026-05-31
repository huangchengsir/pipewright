// Package dora 计算 DORA 四指标(FR-8-15),对既有运行数据做**只读聚合**(不新增事件采集)。
//
// 四指标(DevOps Research and Assessment):
//   - 部署频率(Deployment Frequency):单位时间内的成功部署次数 / 节奏。
//   - 变更前置时长(Lead Time for Changes):提交到投产的时长(从可用时间戳近似)。
//   - 变更失败率(Change Failure Rate):失败部署 / 总部署。
//   - 故障恢复时长(MTTR / Time to Restore):一次失败到下一次成功之间的时长。
//
// 在 CI 运行数据上算 DORA 必然是**近似**的。本包把所有派生口径显式写在常量/注释里,
// 并把数学下沉为可单测的纯函数(对合成运行集断言),边界(空集 / 除零 / 单条)优雅降级。
//
// 派生口径(冻结,前后端 / 文档一致):
//
//	一次「部署(deployment)」    = 一条进入**终态**的运行(success|failed|partial_failed|rolled_back)。
//	                              即把每条结束的运行视作一次投产尝试(平台无独立部署事件流,运行即投产单元)。
//	一次「成功部署」              = status == success 的终态运行。
//	一次「失败部署」              = status ∈ {failed, partial_failed, rolled_back} 的终态运行。
//	                              partial_failed / rolled_back 均计为失败(投产未达预期)。
//	部署频率(perDay/perWeek)    = 成功部署数 / 窗口天数(再换算到周)。
//	变更前置时长(LeadTime)      = 每条**成功**部署的 (finished_at − commitTime);
//	                              commitTime 不可得时回退用 created_at(入队时刻)作提交时间代理。
//	                              取所有成功部署的**中位数**(抗离群,DORA 惯例)。
//	变更失败率(CFR)             = 失败部署数 / 终态部署总数;总数为 0 → 0。
//	故障恢复时长(MTTR)          = 按时间排序的运行序列里,每段「失败 → 下一次成功」的
//	                              (恢复成功 finished_at − 失败 finished_at) 的**中位数**;
//	                              无任何「失败后恢复」配对 → 0(未定义,优雅降级)。
//
// 本包不依赖 internal/run(避免循环 / 抬高耦合):调用方把运行投影成本包的 Record 切片。
package dora

import (
	"sort"
	"time"
)

// 终态运行状态(与 internal/run 的枚举字面值一致;本包不 import run 以保持纯净)。
const (
	statusSuccess       = "success"
	statusFailed        = "failed"
	statusPartialFailed = "partial_failed"
	statusRolledBack    = "rolled_back"
)

// Record 是计算 DORA 所需的单条运行投影(由调用方从运行模型映射而来)。
//
// 只取计算必需的字段:终态判定靠 Status + FinishedAt;前置时长靠 CommitTime/CreatedAt + FinishedAt。
// 非终态运行(queued/running/...)由 FinishedAt==nil 自然排除,无需调用方预过滤。
type Record struct {
	// Status 是运行状态(success|failed|partial_failed|rolled_back|其它非终态)。
	Status string
	// CreatedAt 是入队时刻(必有);CommitTime 不可得时作提交时间代理。
	CreatedAt time.Time
	// CommitTime 是提交时刻(可空)。本平台运行模型当前不持久化提交时间,故通常为 nil;
	// 一旦上游补齐(如 webhook 带 commit 时间),前置时长口径自动更精确,无需改本包。
	CommitTime *time.Time
	// FinishedAt 是终态时刻(非终态为 nil — 据此判定「是否一次部署」)。
	FinishedAt *time.Time
}

// 部署绩效分档(DORA 四档:Elite / High / Medium / Low)。
// 字面值稳定(前端据此着色 / 文案),勿改。
const (
	// BandElite 表示精英级。
	BandElite = "elite"
	// BandHigh 表示高效级。
	BandHigh = "high"
	// BandMedium 表示中等级。
	BandMedium = "medium"
	// BandLow 表示低效级。
	BandLow = "low"
	// BandNone 表示样本不足、无法分档(空窗口)。
	BandNone = "none"
)

// Metrics 是一个窗口内聚合出的 DORA 四指标 + 派生分档 + 计数。
//
// 时长类指标以秒(float64)表达;无样本时为 0(调用方据 *Count==0 区分「真 0」与「无数据」)。
type Metrics struct {
	// WindowDays 是统计窗口天数(>0)。
	WindowDays float64
	// TotalDeployments 是窗口内终态运行(部署)总数。
	TotalDeployments int
	// SuccessfulDeployments 是成功部署数。
	SuccessfulDeployments int
	// FailedDeployments 是失败部署数(failed|partial_failed|rolled_back)。
	FailedDeployments int

	// DeploymentFrequencyPerDay 是日均成功部署数(SuccessfulDeployments / WindowDays)。
	DeploymentFrequencyPerDay float64
	// DeploymentFrequencyPerWeek 是周均成功部署数(便于「每周 N 次」直觉)。
	DeploymentFrequencyPerWeek float64
	// LeadTimeSeconds 是变更前置时长中位数(秒);无成功样本 → 0。
	LeadTimeSeconds float64
	// LeadTimeSampleCount 是参与前置时长统计的成功部署数。
	LeadTimeSampleCount int
	// ChangeFailureRate 是变更失败率 [0,1];无部署 → 0。
	ChangeFailureRate float64
	// MTTRSeconds 是故障恢复时长中位数(秒);无「失败→恢复」配对 → 0。
	MTTRSeconds float64
	// MTTRSampleCount 是参与 MTTR 统计的「失败→恢复」配对数。
	MTTRSampleCount int

	// DeploymentFrequencyBand / LeadTimeBand / ChangeFailureRateBand / MTTRBand
	// 是四指标各自的 DORA 绩效分档(Elite/High/Medium/Low / None)。
	DeploymentFrequencyBand string
	LeadTimeBand            string
	ChangeFailureRateBand   string
	MTTRBand                string
}

// isFailure 报告状态是否为「失败部署」(failed | partial_failed | rolled_back)。
func isFailure(status string) bool {
	switch status {
	case statusFailed, statusPartialFailed, statusRolledBack:
		return true
	default:
		return false
	}
}

// isTerminalDeployment 报告一条运行是否计作一次「部署」(已进入终态 = 有 FinishedAt 且状态为四终态之一)。
func isTerminalDeployment(r Record) bool {
	if r.FinishedAt == nil {
		return false
	}
	return r.Status == statusSuccess || isFailure(r.Status)
}

// Compute 对一组运行投影计算窗口内的 DORA 四指标。
//
// windowDays 是统计窗口天数(<=0 时钳为 1,避免除零并给出合理日频)。records 顺序无所谓
// (内部按 FinishedAt 排序算 MTTR)。非终态运行被自动忽略(FinishedAt==nil)。
//
// 纯函数:无 I/O、无副作用,可对合成集直接断言。
func Compute(records []Record, windowDays float64) Metrics {
	if windowDays <= 0 {
		windowDays = 1
	}
	m := Metrics{WindowDays: windowDays}

	// 仅保留终态部署。
	deploys := make([]Record, 0, len(records))
	for _, r := range records {
		if isTerminalDeployment(r) {
			deploys = append(deploys, r)
		}
	}

	leadTimes := make([]float64, 0, len(deploys))
	for _, r := range deploys {
		m.TotalDeployments++
		if r.Status == statusSuccess {
			m.SuccessfulDeployments++
			// 前置时长:finished − commit(commit 不可得 → created 代理)。负值丢弃(脏数据防御)。
			commit := r.CreatedAt
			if r.CommitTime != nil {
				commit = *r.CommitTime
			}
			if lt := r.FinishedAt.Sub(commit).Seconds(); lt >= 0 {
				leadTimes = append(leadTimes, lt)
			}
		} else {
			m.FailedDeployments++
		}
	}

	// 部署频率:成功部署 / 窗口。
	m.DeploymentFrequencyPerDay = float64(m.SuccessfulDeployments) / windowDays
	m.DeploymentFrequencyPerWeek = m.DeploymentFrequencyPerDay * 7

	// 前置时长:成功样本中位数。
	m.LeadTimeSampleCount = len(leadTimes)
	m.LeadTimeSeconds = median(leadTimes)

	// 变更失败率:失败 / 总部署(总数 0 → 0,避免 NaN)。
	if m.TotalDeployments > 0 {
		m.ChangeFailureRate = float64(m.FailedDeployments) / float64(m.TotalDeployments)
	}

	// MTTR:按完成时间排序,逐段「失败 → 下一次成功」配对。
	restoreTimes := mttrRestoreSeconds(deploys)
	m.MTTRSampleCount = len(restoreTimes)
	m.MTTRSeconds = median(restoreTimes)

	// 分档。
	m.DeploymentFrequencyBand = deployFrequencyBand(m.DeploymentFrequencyPerDay, m.SuccessfulDeployments)
	m.LeadTimeBand = leadTimeBand(m.LeadTimeSeconds, m.LeadTimeSampleCount)
	m.ChangeFailureRateBand = changeFailureRateBand(m.ChangeFailureRate, m.TotalDeployments)
	m.MTTRBand = mttrBand(m.MTTRSeconds, m.MTTRSampleCount)

	return m
}

// mttrRestoreSeconds 按完成时间升序扫描部署序列,对每个「失败段 → 下一次成功」产出恢复时长(秒)。
//
// 语义:进入失败态后,记录该段最早一次失败的完成时刻 failStart;遇到下一次成功时,
// 产出 (success.finished − failStart),并清空失败段(等待下一次失败开启新段)。
// 连续多次失败只按**该段最早**一次失败起算(故障从首次失败算起,到恢复为止)。
// 末尾悬空的失败段(尚未恢复)不产出配对(未恢复,不纳入恢复时长统计)。
func mttrRestoreSeconds(deploys []Record) []float64 {
	ordered := make([]Record, len(deploys))
	copy(ordered, deploys)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].FinishedAt.Before(*ordered[j].FinishedAt)
	})

	out := []float64{}
	var failStart *time.Time
	for i := range ordered {
		r := ordered[i]
		if isFailure(r.Status) {
			if failStart == nil {
				ft := *r.FinishedAt
				failStart = &ft
			}
			continue
		}
		// 成功:若处于失败段中,结算恢复时长。
		if r.Status == statusSuccess && failStart != nil {
			if d := r.FinishedAt.Sub(*failStart).Seconds(); d >= 0 {
				out = append(out, d)
			}
			failStart = nil
		}
	}
	return out
}

// median 返回切片中位数(升序后取中;偶数个取中间两者均值)。空切片 → 0。
func median(values []float64) float64 {
	n := len(values)
	if n == 0 {
		return 0
	}
	sorted := make([]float64, n)
	copy(sorted, values)
	sort.Float64s(sorted)
	mid := n / 2
	if n%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}
