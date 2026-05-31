package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/huangchengsir/pipewright/internal/dora"
	"github.com/huangchengsir/pipewright/internal/run"
)

// dora.go 挂载 DORA 四指标只读端点(FR-8-15):
//
//	GET /api/metrics/dora?projectId=&window=30d
//	  → { deploymentFrequency, leadTimeSeconds, changeFailureRate, mttrSeconds, window, trend, ... }
//
// 纯只读聚合:取窗口内运行投影(run.MetricsService)→ dora.Compute / dora.ComputeTrend → DTO。
// 无副作用、无新表。认证由路由组的 requireAuth 统一兜底(GET 豁免 CSRF)。
//
// 派生口径见 internal/dora 包顶部注释(部署=终态运行;成功部署=success;失败=failed/partial/rolled_back;
// 前置时长=finished−commit,commit 不可得回退 created;MTTR=失败→恢复成功配对中位数)。

const (
	// doraDefaultWindowDays 是默认统计窗口(天)。
	doraDefaultWindowDays = 30
	// doraMaxWindowDays 是窗口上界(防超长扫描;约两年)。
	doraMaxWindowDays = 730
	// doraTrendBuckets 是趋势序列的目标桶数(前端 sparkline 点数;实际按天对齐取整)。
	doraTrendBuckets = 12
)

// doraMetricDTO 是单个指标的统一表达:数值 + DORA 绩效分档 + 样本数(供前端判「无数据」)。
type doraMetricDTO struct {
	// Value 是指标数值(频率为日均成功部署数;时长为秒;失败率为 [0,1] 比例)。
	Value float64 `json:"value"`
	// Band 是 DORA 绩效分档(elite|high|medium|low|none)。
	Band string `json:"band"`
	// SampleCount 是参与该指标统计的样本数(0 = 无数据,前端显「—」而非「0」)。
	SampleCount int `json:"sampleCount"`
}

// doraTrendPointDTO 是趋势序列的一个时间桶(冻结形状;前端画迷你折线)。
type doraTrendPointDTO struct {
	BucketStart       string  `json:"bucketStart"`
	Deployments       int     `json:"deployments"`
	Successes         int     `json:"successes"`
	Failures          int     `json:"failures"`
	ChangeFailureRate float64 `json:"changeFailureRate"`
}

// doraResponseDTO 是 GET /api/metrics/dora 响应体。
//
// 四个核心指标既给「裸值」字段(deploymentFrequency/leadTimeSeconds/changeFailureRate/mttrSeconds,
// 便于直接消费),又在 metrics 块给「值+分档+样本数」的完整形态(前端卡片)。
type doraResponseDTO struct {
	// 窗口与计数。
	Window                string `json:"window"`
	WindowDays            int    `json:"windowDays"`
	ProjectID             string `json:"projectId"`
	TotalDeployments      int    `json:"totalDeployments"`
	SuccessfulDeployments int    `json:"successfulDeployments"`
	FailedDeployments     int    `json:"failedDeployments"`

	// 裸值(便捷消费)。
	DeploymentFrequency        float64 `json:"deploymentFrequency"`        // 日均成功部署数
	DeploymentFrequencyPerWeek float64 `json:"deploymentFrequencyPerWeek"` // 周均
	LeadTimeSeconds            float64 `json:"leadTimeSeconds"`
	ChangeFailureRate          float64 `json:"changeFailureRate"`
	MTTRSeconds                float64 `json:"mttrSeconds"`

	// 完整形态(值+分档+样本)。
	Metrics doraMetricsBlockDTO `json:"metrics"`

	// 趋势(按天对齐的时间桶;前端 sparkline)。
	Trend []doraTrendPointDTO `json:"trend"`

	// GeneratedAt 是聚合时刻(RFC3339;前端显「数据截至」)。
	GeneratedAt string `json:"generatedAt"`
}

type doraMetricsBlockDTO struct {
	DeploymentFrequency doraMetricDTO `json:"deploymentFrequency"`
	LeadTime            doraMetricDTO `json:"leadTime"`
	ChangeFailureRate   doraMetricDTO `json:"changeFailureRate"`
	MTTR                doraMetricDTO `json:"mttr"`
}

// makeDoraMetricsHandler 返回 GET /api/metrics/dora handler(只读 + 认证)。
// ms 为 nil → 503(服务未初始化)。projectId 可选(空 = 全部项目);window 形如 30d / 7d(默认 30d)。
func makeDoraMetricsHandler(ms run.MetricsService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if ms == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "运行服务未初始化")
			return
		}
		q := r.URL.Query()
		projectID := strings.TrimSpace(q.Get("projectId"))

		windowDays, windowLabel, ok := parseWindowDays(q.Get("window"))
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid_window", "window 必须形如 30d / 7d / 90d,且不超过 730 天")
			return
		}

		now := time.Now().UTC()
		since := now.Add(-time.Duration(windowDays) * 24 * time.Hour)

		records, err := ms.MetricsRecords(r.Context(), projectID, since)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
			return
		}

		doraRecords := make([]dora.Record, 0, len(records))
		for _, rec := range records {
			doraRecords = append(doraRecords, dora.Record{
				Status:     rec.Status,
				CreatedAt:  rec.CreatedAt,
				FinishedAt: rec.FinishedAt,
				// CommitTime 暂不可得(运行模型未持久化提交时间);dora 层回退用 CreatedAt 作代理。
			})
		}

		m := dora.Compute(doraRecords, float64(windowDays))
		trend := dora.ComputeTrend(doraRecords, since, now, doraTrendBuckets)

		writeJSON(w, http.StatusOK, toDoraResponse(m, trend, windowLabel, windowDays, projectID, now))
	}
}

// toDoraResponse 把 dora 计算结果映射为响应 DTO。
func toDoraResponse(m dora.Metrics, trend []dora.TrendPoint, windowLabel string, windowDays int, projectID string, now time.Time) doraResponseDTO {
	points := make([]doraTrendPointDTO, 0, len(trend))
	for _, p := range trend {
		points = append(points, doraTrendPointDTO{
			BucketStart:       p.BucketStart.UTC().Format(time.RFC3339),
			Deployments:       p.Deployments,
			Successes:         p.Successes,
			Failures:          p.Failures,
			ChangeFailureRate: p.ChangeFailureRate,
		})
	}

	return doraResponseDTO{
		Window:                     windowLabel,
		WindowDays:                 windowDays,
		ProjectID:                  projectID,
		TotalDeployments:           m.TotalDeployments,
		SuccessfulDeployments:      m.SuccessfulDeployments,
		FailedDeployments:          m.FailedDeployments,
		DeploymentFrequency:        m.DeploymentFrequencyPerDay,
		DeploymentFrequencyPerWeek: m.DeploymentFrequencyPerWeek,
		LeadTimeSeconds:            m.LeadTimeSeconds,
		ChangeFailureRate:          m.ChangeFailureRate,
		MTTRSeconds:                m.MTTRSeconds,
		Metrics: doraMetricsBlockDTO{
			DeploymentFrequency: doraMetricDTO{
				Value:       m.DeploymentFrequencyPerDay,
				Band:        m.DeploymentFrequencyBand,
				SampleCount: m.SuccessfulDeployments,
			},
			LeadTime: doraMetricDTO{
				Value:       m.LeadTimeSeconds,
				Band:        m.LeadTimeBand,
				SampleCount: m.LeadTimeSampleCount,
			},
			ChangeFailureRate: doraMetricDTO{
				Value:       m.ChangeFailureRate,
				Band:        m.ChangeFailureRateBand,
				SampleCount: m.TotalDeployments,
			},
			MTTR: doraMetricDTO{
				Value:       m.MTTRSeconds,
				Band:        m.MTTRBand,
				SampleCount: m.MTTRSampleCount,
			},
		},
		Trend:       points,
		GeneratedAt: now.UTC().Format(time.RFC3339),
	}
}

// parseWindowDays 解析 window 参数(形如 "30d" / "7d" / "90d";也接受纯数字当天数)。
// 空 → 默认 30d。返回 (天数, 规范化标签, 是否合法)。超界/非法 → ok=false。
func parseWindowDays(raw string) (days int, label string, ok bool) {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return doraDefaultWindowDays, strconv.Itoa(doraDefaultWindowDays) + "d", true
	}
	numStr := raw
	if strings.HasSuffix(raw, "d") {
		numStr = strings.TrimSuffix(raw, "d")
	}
	n, err := strconv.Atoi(strings.TrimSpace(numStr))
	if err != nil || n < 1 || n > doraMaxWindowDays {
		return 0, "", false
	}
	return n, strconv.Itoa(n) + "d", true
}
