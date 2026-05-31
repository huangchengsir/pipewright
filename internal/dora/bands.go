package dora

import "time"

// 绩效分档阈值(对齐 DORA / Accelerate State of DevOps 报告的经典四档口径)。
//
// 这些阈值是行业惯例的近似刻度,用于把原始数值翻译成「Elite/High/Medium/Low」直觉标签;
// 在 CI 运行数据上是参考性的(口径已在 dora.go 顶部声明),不作 SLA 依据。
//
// 常量集中于此,便于审阅 / 调参,且让分档函数保持纯粹的「数值 → 标签」映射。
const (
	// 部署频率(按日均成功部署数,折算「按需 / 每周 / 每月」直觉)。
	deployFreqElitePerDay  = 1.0      // ≥ 每天一次(按需多次)→ Elite
	deployFreqHighPerDay   = 1.0 / 7  // ≥ 每周一次 → High
	deployFreqMediumPerDay = 1.0 / 30 // ≥ 每月一次 → Medium;低于则 Low

	// 变更前置时长(秒)。
	leadTimeEliteSeconds  = float64(24 * time.Hour / time.Second)      // < 1 天 → Elite
	leadTimeHighSeconds   = float64(7 * 24 * time.Hour / time.Second)  // < 1 周 → High
	leadTimeMediumSeconds = float64(30 * 24 * time.Hour / time.Second) // < 1 月 → Medium;否则 Low

	// 变更失败率(比例 [0,1])。
	cfrEliteMax  = 0.15 // ≤ 15% → Elite
	cfrHighMax   = 0.30 // ≤ 30% → High
	cfrMediumMax = 0.45 // ≤ 45% → Medium;高于则 Low

	// 故障恢复时长(秒)。
	mttrEliteSeconds  = float64(1 * time.Hour / time.Second)      // < 1 小时 → Elite
	mttrHighSeconds   = float64(24 * time.Hour / time.Second)     // < 1 天 → High
	mttrMediumSeconds = float64(7 * 24 * time.Hour / time.Second) // < 1 周 → Medium;否则 Low
)

// deployFrequencyBand 据日均成功部署数分档。无成功样本 → None(样本不足,不强行分档)。
func deployFrequencyBand(perDay float64, successCount int) string {
	if successCount == 0 {
		return BandNone
	}
	switch {
	case perDay >= deployFreqElitePerDay:
		return BandElite
	case perDay >= deployFreqHighPerDay:
		return BandHigh
	case perDay >= deployFreqMediumPerDay:
		return BandMedium
	default:
		return BandLow
	}
}

// leadTimeBand 据前置时长中位数(秒)分档。无样本 → None。
func leadTimeBand(seconds float64, sampleCount int) string {
	if sampleCount == 0 {
		return BandNone
	}
	switch {
	case seconds < leadTimeEliteSeconds:
		return BandElite
	case seconds < leadTimeHighSeconds:
		return BandHigh
	case seconds < leadTimeMediumSeconds:
		return BandMedium
	default:
		return BandLow
	}
}

// changeFailureRateBand 据变更失败率分档。无部署 → None。
func changeFailureRateBand(rate float64, totalDeployments int) string {
	if totalDeployments == 0 {
		return BandNone
	}
	switch {
	case rate <= cfrEliteMax:
		return BandElite
	case rate <= cfrHighMax:
		return BandHigh
	case rate <= cfrMediumMax:
		return BandMedium
	default:
		return BandLow
	}
}

// mttrBand 据恢复时长中位数(秒)分档。无「失败→恢复」配对 → None。
func mttrBand(seconds float64, sampleCount int) string {
	if sampleCount == 0 {
		return BandNone
	}
	switch {
	case seconds < mttrEliteSeconds:
		return BandElite
	case seconds < mttrHighSeconds:
		return BandHigh
	case seconds < mttrMediumSeconds:
		return BandMedium
	default:
		return BandLow
	}
}
