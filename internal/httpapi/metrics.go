package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/huangchengsir/pipewright/internal/anomaly"
	"github.com/huangchengsir/pipewright/internal/metrics"
)

// 服务器指标时序历史(异常检测「看趋势」折线图)。后台采样器周期性把每台可达服务器的
// CPU/内存/磁盘使用率写入 metrics.Service;此处暴露查询端点供前端画折线。
//
// 路由(router.go append):
//   - GET /api/metrics/history?serverId=&hours=   (auth)  某服务器近 N 小时的指标序列

// metricPointDTO 是趋势序列上一个时间点(冻结契约;百分比 null=该时刻不可得,前端断线)。
type metricPointDTO struct {
	At     string   `json:"at"`
	CPU    *float64 `json:"cpu"`
	Memory *float64 `json:"memory"`
	Disk   *float64 `json:"disk"`
}

// SamplesFromSnapshots 把异常检测 collector 的快照转为 metrics 采样(供 main 采样器复用同一
// 6-1 采集源写时序历史;不可达的服务器整台跳过,不存空行)。
func SamplesFromSnapshots(snaps []anomaly.ServerMetricsSnapshot, at time.Time) []metrics.Sample {
	out := make([]metrics.Sample, 0, len(snaps))
	for _, s := range snaps {
		if !s.Available {
			continue
		}
		out = append(out, metrics.Sample{
			ServerID: s.ServerID,
			CPU:      s.CPUPercent,
			Memory:   s.MemoryPercent,
			Disk:     s.DiskPercent,
			At:       at,
		})
	}
	return out
}

// makeMetricsHistoryHandler 返回 GET /api/metrics/history?serverId=&hours=。
// serverId 必填;hours 默认 6,夹在 [1, 168](7 天)。返回按时间升序的指标点序列。
func makeMetricsHistoryHandler(svc metrics.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "指标历史服务未初始化")
			return
		}
		serverID := r.URL.Query().Get("serverId")
		if serverID == "" {
			writeError(w, http.StatusBadRequest, "bad_request", "缺少 serverId")
			return
		}
		hours := 6
		if hs := r.URL.Query().Get("hours"); hs != "" {
			if v, err := strconv.Atoi(hs); err == nil {
				hours = v
			}
		}
		if hours < 1 {
			hours = 1
		}
		if hours > 168 {
			hours = 168
		}
		since := time.Now().UTC().Add(-time.Duration(hours) * time.Hour)
		points, err := svc.QueryRange(r.Context(), serverID, since)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "查询指标历史失败")
			return
		}
		out := make([]metricPointDTO, 0, len(points))
		for _, p := range points {
			out = append(out, metricPointDTO{
				At:     p.At.UTC().Format(time.RFC3339),
				CPU:    p.CPU,
				Memory: p.Memory,
				Disk:   p.Disk,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"points": out, "hours": hours})
	}
}
