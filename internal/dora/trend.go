package dora

import (
	"sort"
	"time"
)

// TrendPoint 是趋势序列里的一个时间桶(默认按天)的聚合,供前端画 sparkline / 迷你折线。
//
// 每个桶独立按「该桶内的终态部署」算频率 / 失败率(轻量,不含 lead/MTTR 跨桶配对,
// 避免桶边界把一段故障切碎)。BucketStart 是桶起始时刻(UTC,RFC3339 由 HTTP 层格式化)。
type TrendPoint struct {
	// BucketStart 是该桶起始时刻(UTC,按天对齐到 00:00:00)。
	BucketStart time.Time
	// Deployments 是该桶内终态部署总数。
	Deployments int
	// Successes 是该桶内成功部署数。
	Successes int
	// Failures 是该桶内失败部署数。
	Failures int
	// ChangeFailureRate 是该桶变更失败率 [0,1];桶内无部署 → 0。
	ChangeFailureRate float64
}

// ComputeTrend 把 [windowStart, now] 切成 bucketCount 个等宽桶(按天对齐),逐桶聚合部署计数。
//
// windowStart 是窗口起点(UTC),now 是窗口终点;bucketCount<=0 时退化为不产出趋势(返回空切片)。
// 每条 record 按 FinishedAt 落桶;落在窗口外或非终态的运行被忽略。
// 桶按天对齐:bucketDays = ceil(windowDays / bucketCount),保证整天边界,前端 x 轴可读。
//
// 纯函数:可对合成集断言桶计数。
func ComputeTrend(records []Record, windowStart, now time.Time, bucketCount int) []TrendPoint {
	if bucketCount <= 0 || !now.After(windowStart) {
		return []TrendPoint{}
	}

	windowStart = windowStart.UTC()
	now = now.UTC()

	// 桶宽(整天):至少 1 天。
	totalDays := int(now.Sub(windowStart).Hours()/24) + 1
	bucketDays := (totalDays + bucketCount - 1) / bucketCount
	if bucketDays < 1 {
		bucketDays = 1
	}

	// 把窗口起点对齐到当天 00:00 UTC,使桶边界落在整天。
	origin := time.Date(windowStart.Year(), windowStart.Month(), windowStart.Day(), 0, 0, 0, 0, time.UTC)
	bucketDur := time.Duration(bucketDays) * 24 * time.Hour

	// 桶数:覆盖到 now。
	span := now.Sub(origin)
	nBuckets := int(span/bucketDur) + 1
	if nBuckets < 1 {
		nBuckets = 1
	}

	points := make([]TrendPoint, nBuckets)
	for i := range points {
		points[i] = TrendPoint{BucketStart: origin.Add(time.Duration(i) * bucketDur)}
	}

	for _, r := range records {
		if !isTerminalDeployment(r) {
			continue
		}
		ft := r.FinishedAt.UTC()
		if ft.Before(origin) || ft.After(now) {
			continue
		}
		idx := int(ft.Sub(origin) / bucketDur)
		if idx < 0 || idx >= nBuckets {
			continue
		}
		p := &points[idx]
		p.Deployments++
		if r.Status == statusSuccess {
			p.Successes++
		} else {
			p.Failures++
		}
	}

	for i := range points {
		if points[i].Deployments > 0 {
			points[i].ChangeFailureRate = float64(points[i].Failures) / float64(points[i].Deployments)
		}
	}

	// 已天然按 BucketStart 升序(origin + i*dur),但稳妥起见显式排序。
	sort.SliceStable(points, func(i, j int) bool {
		return points[i].BucketStart.Before(points[j].BucketStart)
	})
	return points
}
