package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/anomaly"
	"github.com/huangchengsir/pipewright/internal/notify"
	"github.com/huangchengsir/pipewright/internal/target"
)

// Story 6.5(FR-23):可配置运行时异常检测与告警。
//
// 管理员配规则(metric ∈ cpu/memory/disk;operator ∈ gt/lt;threshold;作用域全局/指定服务器);
// POST /api/anomaly/check 经 6-1 采集逐服务器逐 enabled 规则求值,命中产告警入库。
//
// 指标采集**复用 6-1**(collectServerMetrics):metricsCollector 适配器把 serverMetricsDTO
// 归一为「百分比」快照交 anomaly.Service。不可达 / 该指标 null 的服务器跳过(不误报)。
//
// 路由(router.go 仅 append):
//   - GET    /api/anomaly/rules           (auth)          列规则
//   - POST   /api/anomaly/rules           (auth + CSRF)   建规则(metric/operator 枚举校验)
//   - DELETE /api/anomaly/rules/{id}       (auth + CSRF)   删规则
//   - POST   /api/anomaly/check            (auth + CSRF)   执行检测,返回本次新增告警
//   - GET    /api/anomaly/alerts?serverId=&limit=  (auth)  最近告警列表

// NewAnomalyCollector 构造复用 6-1 指标采集的 anomaly.MetricsCollector(main.go 装配 anomaly 服务时注入)。
// servers 为已装配的 target.Service(4-1);检测时经 collectServerMetrics 逐台采集再归一为百分比快照。
func NewAnomalyCollector(servers target.Service) anomaly.MetricsCollector {
	return metricsCollector{servers: servers}
}

// NewAnomalyNotifier 构造 anomaly.Notifier:每条新告警 → 通知事件 anomaly_detected,经 notify
// **全局路由**(projectID 空,异常非 run/项目维度)发到各配置渠道。让 anomaly 包不 import notify
// (仿 NewAnomalyCollector 解耦);未配置任何路由 → RouteEventForProject 内部 best-effort 不发。
func NewAnomalyNotifier(notifySvc notify.Service) anomaly.Notifier {
	return anomalyNotifier{notify: notifySvc}
}

// anomalyNotifier 把 anomaly.Alert 映射为通知事件并经渠道发送。
type anomalyNotifier struct{ notify notify.Service }

// NotifyAlert 异步发一次告警通知(fire-and-forget):不阻塞检测主流程(手动检测的 HTTP 响应 /
// 定时检测的 goroutine 都不等慢渠道)。用独立 ctx + 超时,失败仅记日志(best-effort,NFR-10)。
func (n anomalyNotifier) NotifyAlert(ctx context.Context, a *anomaly.Alert) {
	if n.notify == nil || a == nil {
		return
	}
	// TemplateVars 复用既有占位:Project=服务器名(标题渲染为「[<服务器>] 异常检测」),
	// Status=人读告警文案(如「磁盘使用率 92.3% > 1%」,正文/字段透出)。
	vars := notify.TemplateVars{
		Project: a.ServerName,
		Status:  a.Message,
		Event:   notify.EventAnomalyDetected,
	}
	go func() {
		sendCtx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		if err := n.notify.RouteEventForProject(sendCtx, "", notify.EventAnomalyDetected, vars); err != nil {
			log.Printf("[anomaly] 告警 %s 通知 best-effort 失败:%v", a.ID, err)
		}
	}()
}

// metricsCollector 是 anomaly.MetricsCollector 的 httpapi 适配:复用 6-1 的 collectServerMetrics
// 逐台并行采集(有界并发),再把字节级指标归一为百分比快照(指标 null → 对应 *float64 为 nil)。
type metricsCollector struct {
	servers target.Service
}

// Collect 实现 anomaly.MetricsCollector。serverIDs 为空 → 取全部已登记服务器。
// 逐台独立:不可达 / 定位失败仅令该台 Available=false,不连累其它台、不返回错误。
func (c metricsCollector) Collect(ctx context.Context, serverIDs []string) ([]anomaly.ServerMetricsSnapshot, error) {
	if c.servers == nil {
		return nil, nil
	}

	// 解析目标服务器集合(取展示名供告警快照)。serverIDs 为空 → 全部。
	type srvRef struct {
		id   string
		name string
	}
	var refs []srvRef
	if len(serverIDs) == 0 {
		all, err := c.servers.List(ctx)
		if err != nil {
			return nil, err
		}
		for _, s := range all {
			refs = append(refs, srvRef{id: s.ID, name: s.Name})
		}
	} else {
		for _, id := range serverIDs {
			name := id
			if s, err := c.servers.Get(ctx, id); err == nil {
				name = s.Name
			}
			refs = append(refs, srvRef{id: id, name: name})
		}
	}

	out := make([]anomaly.ServerMetricsSnapshot, len(refs))
	sem := make(chan struct{}, metricsConcurrency)
	var wg sync.WaitGroup
	for i, ref := range refs {
		wg.Add(1)
		go func(i int, ref srvRef) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			// 逐台独立:定位类错误对某台亦只表现为 reachable:false(忽略 locErr)。
			dto, _ := collectServerMetrics(ctx, c.servers, ref.id)
			out[i] = snapshotFromMetricsDTO(ref.id, ref.name, dto)
		}(i, ref)
	}
	wg.Wait()
	return out, nil
}

// snapshotFromMetricsDTO 把 6-1 的字节级 serverMetricsDTO 归一为百分比快照(供异常检测求值)。
//   - 不可达 → Available=false(整台跳过)。
//   - cpu:loadavg1/cores×100(任一缺失 → nil)。
//   - memory/disk:used/total×100(缺失或 total<=0 → nil)。
func snapshotFromMetricsDTO(id, name string, dto serverMetricsDTO) anomaly.ServerMetricsSnapshot {
	snap := anomaly.ServerMetricsSnapshot{
		ServerID:   id,
		ServerName: name,
		Available:  dto.Reachable,
	}
	if !dto.Reachable {
		return snap
	}
	if dto.CPU != nil && dto.CPU.Loadavg1 != nil && dto.CPU.Cores != nil && *dto.CPU.Cores > 0 {
		v := (*dto.CPU.Loadavg1 / float64(*dto.CPU.Cores)) * 100
		snap.CPUPercent = &v
	}
	if dto.Memory != nil && dto.Memory.TotalBytes > 0 {
		v := (float64(dto.Memory.UsedBytes) / float64(dto.Memory.TotalBytes)) * 100
		snap.MemoryPercent = &v
	}
	if dto.Disk != nil && dto.Disk.TotalBytes > 0 {
		v := (float64(dto.Disk.UsedBytes) / float64(dto.Disk.TotalBytes)) * 100
		snap.DiskPercent = &v
	}
	return snap
}

// --- DTO ---

// anomalyRuleDTO 是规则对外响应体(冻结契约;camelCase)。serverId 为 null 表示全局。
type anomalyRuleDTO struct {
	ID        string  `json:"id"`
	Metric    string  `json:"metric"`
	Operator  string  `json:"operator"`
	Threshold float64 `json:"threshold"`
	ServerID  *string `json:"serverId"`
	Enabled   bool    `json:"enabled"`
	CreatedAt string  `json:"createdAt"`
}

func toRuleDTO(r *anomaly.Rule) anomalyRuleDTO {
	return anomalyRuleDTO{
		ID:        r.ID,
		Metric:    r.Metric,
		Operator:  r.Operator,
		Threshold: r.Threshold,
		ServerID:  r.ServerID,
		Enabled:   r.Enabled,
		CreatedAt: r.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// anomalyAlertDTO 是告警对外响应体(冻结契约)。
type anomalyAlertDTO struct {
	ID         string  `json:"id"`
	ServerID   string  `json:"serverId"`
	ServerName string  `json:"serverName"`
	Metric     string  `json:"metric"`
	Operator   string  `json:"operator"`
	Threshold  float64 `json:"threshold"`
	Value      float64 `json:"value"`
	Message    string  `json:"message"`
	At         string  `json:"at"`
}

func toAlertDTO(a *anomaly.Alert) anomalyAlertDTO {
	return anomalyAlertDTO{
		ID:         a.ID,
		ServerID:   a.ServerID,
		ServerName: a.ServerName,
		Metric:     a.Metric,
		Operator:   a.Operator,
		Threshold:  a.Threshold,
		Value:      a.Value,
		Message:    a.Message,
		At:         a.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// writeAnomalyError 把领域错误映射为契约错误码/状态码。
func writeAnomalyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, anomaly.ErrNotFound):
		writeError(w, http.StatusNotFound, "rule_not_found", "规则不存在")
	case errors.Is(err, anomaly.ErrInvalidMetric):
		writeError(w, http.StatusBadRequest, "invalid_rule", "metric 必须是 cpu / memory / disk 之一")
	case errors.Is(err, anomaly.ErrInvalidOperator):
		writeError(w, http.StatusBadRequest, "invalid_rule", "operator 必须是 gt / lt 之一")
	default:
		writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
	}
}

// --- Handlers ---

// makeListAnomalyRulesHandler 返回 GET /api/anomaly/rules → { items: [...] }。
func makeListAnomalyRulesHandler(svc anomaly.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "异常检测服务未初始化")
			return
		}
		rules, err := svc.ListRules(r.Context())
		if err != nil {
			writeAnomalyError(w, err)
			return
		}
		out := make([]anomalyRuleDTO, 0, len(rules))
		for _, rule := range rules {
			out = append(out, toRuleDTO(rule))
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": out})
	}
}

// makeCreateAnomalyRuleHandler 返回 POST /api/anomaly/rules(auth + CSRF)。
// metric/operator 枚举非法 → 400 invalid_rule。serverId 省略/空/null → 全局规则。
func makeCreateAnomalyRuleHandler(svc anomaly.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "异常检测服务未初始化")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		var req struct {
			Metric    string  `json:"metric"`
			Operator  string  `json:"operator"`
			Threshold float64 `json:"threshold"`
			ServerID  *string `json:"serverId"`
			Enabled   *bool   `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}
		// enabled 省略时默认启用。
		enabled := true
		if req.Enabled != nil {
			enabled = *req.Enabled
		}
		rule, err := svc.CreateRule(r.Context(), anomaly.CreateRuleInput{
			Metric:    req.Metric,
			Operator:  req.Operator,
			Threshold: req.Threshold,
			ServerID:  req.ServerID,
			Enabled:   enabled,
		})
		if err != nil {
			writeAnomalyError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, toRuleDTO(rule))
	}
}

// makeDeleteAnomalyRuleHandler 返回 DELETE /api/anomaly/rules/{id}(auth + CSRF)。
func makeDeleteAnomalyRuleHandler(svc anomaly.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "异常检测服务未初始化")
			return
		}
		if err := svc.DeleteRule(r.Context(), chi.URLParam(r, "id")); err != nil {
			writeAnomalyError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// makeAnomalyCheckHandler 返回 POST /api/anomaly/check(auth + CSRF)。
// 经 6-1 采集逐服务器逐 enabled 规则求值,命中产告警入库 → 返回本次新增告警列表。
// 无规则 → 空列表不报错;不可达 / 指标不可得的服务器跳过(不误报)。
func makeAnomalyCheckHandler(svc anomaly.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "异常检测服务未初始化")
			return
		}
		alerts, err := svc.Check(r.Context())
		if err != nil {
			writeAnomalyError(w, err)
			return
		}
		out := make([]anomalyAlertDTO, 0, len(alerts))
		for _, a := range alerts {
			out = append(out, toAlertDTO(a))
		}
		writeJSON(w, http.StatusOK, map[string]any{"alerts": out})
	}
}

// makeListAnomalyAlertsHandler 返回 GET /api/anomaly/alerts?serverId=&limit=(auth,只读)。
func makeListAnomalyAlertsHandler(svc anomaly.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "异常检测服务未初始化")
			return
		}
		serverID := r.URL.Query().Get("serverId")
		limit := 0
		if ls := r.URL.Query().Get("limit"); ls != "" {
			if v, err := strconv.Atoi(ls); err == nil {
				limit = v
			}
		}
		alerts, err := svc.ListAlerts(r.Context(), serverID, limit)
		if err != nil {
			writeAnomalyError(w, err)
			return
		}
		out := make([]anomalyAlertDTO, 0, len(alerts))
		for _, a := range alerts {
			out = append(out, toAlertDTO(a))
		}
		writeJSON(w, http.StatusOK, map[string]any{"alerts": out})
	}
}
