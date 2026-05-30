// Package anomaly 是「可配置运行时异常检测与告警」的领域层(FR-23 · Story 6.5)。
//
// 管理员配置阈值规则(metric ∈ cpu/memory/disk,operator ∈ gt/lt,threshold,作用域:
// 全局或指定服务器);系统按规则对服务器指标(经 6-1 SSH 轮询采集)求值,命中即产告警。
//
// 设计纪律:
//   - 指标采集**复用 6-1**:本包不含任何 SSH/采集逻辑,仅经注入的 MetricsCollector 取
//     已归一为「百分比」的指标快照(httpapi 适配 collectServerMetrics)。
//   - 指标 null / 不可达的服务器**跳过不误报**:Collector 返回 Available=false 或某 metric
//     的 *float64 为 nil 时,该 (server, rule) 不求值、不产告警。
//   - 无规则 → 空检测、不报错;无 init 副作用(检测是显式触发,不在 init 起轮询)。
//   - 参数化 SQL;错误体不含任何敏感信息(指标本就无敏感)。
package anomaly

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// metric / operator 枚举(DB 存小写字串;JSON 同名)。
const (
	// MetricCPU 是 CPU 使用率(百分比;loadavg1/cores×100)。
	MetricCPU = "cpu"
	// MetricMemory 是内存使用率(百分比;used/total×100)。
	MetricMemory = "memory"
	// MetricDisk 是磁盘使用率(百分比;used/total×100)。
	MetricDisk = "disk"

	// OperatorGreaterThan 是「大于阈值」命中。
	OperatorGreaterThan = "gt"
	// OperatorLessThan 是「小于阈值」命中。
	OperatorLessThan = "lt"
)

// 领域错误。
var (
	// ErrNotFound 表示规则不存在。
	ErrNotFound = errors.New("anomaly: rule not found")
	// ErrInvalidMetric 表示 metric 枚举非法。
	ErrInvalidMetric = errors.New("anomaly: invalid metric")
	// ErrInvalidOperator 表示 operator 枚举非法。
	ErrInvalidOperator = errors.New("anomaly: invalid operator")
)

// validMetric 校验 metric 枚举。
func validMetric(m string) bool {
	switch m {
	case MetricCPU, MetricMemory, MetricDisk:
		return true
	default:
		return false
	}
}

// validOperator 校验 operator 枚举。
func validOperator(op string) bool {
	switch op {
	case OperatorGreaterThan, OperatorLessThan:
		return true
	default:
		return false
	}
}

// Rule 是异常检测规则(冻结契约)。
//   - ServerID 为 nil 表示全局(对所有服务器);非空表示仅作用于该服务器。
//   - Threshold 是百分比阈值。
type Rule struct {
	ID        string
	Metric    string
	Operator  string
	Threshold float64
	ServerID  *string
	Enabled   bool
	CreatedAt time.Time
}

// CreateRuleInput 是创建规则的入参。
type CreateRuleInput struct {
	Metric    string
	Operator  string
	Threshold float64
	ServerID  *string // nil = 全局
	Enabled   bool
}

// Alert 是命中规则产生的告警(冻结契约;持久化 + 列表可见)。
type Alert struct {
	ID         string
	ServerID   string
	ServerName string
	Metric     string
	Operator   string
	Threshold  float64
	Value      float64
	Message    string
	CreatedAt  time.Time
}

// ServerMetricsSnapshot 是单台服务器的「已归一为百分比」指标快照(由 Collector 提供)。
//   - Available=false(不可达 / 定位失败)→ 整台跳过,不求值任何规则。
//   - 各 *float64 为 nil 表示该 metric 不可得(跨平台 best-effort / 解析失败)→ 该规则跳过。
type ServerMetricsSnapshot struct {
	ServerID   string
	ServerName string
	Available  bool
	// 百分比(0..100+);nil 表示该指标不可得 → 跳过对应 metric 的规则,不误报。
	CPUPercent    *float64
	MemoryPercent *float64
	DiskPercent   *float64
}

// percentFor 取指定 metric 的百分比指针(nil = 不可得)。
func (s ServerMetricsSnapshot) percentFor(metric string) *float64 {
	switch metric {
	case MetricCPU:
		return s.CPUPercent
	case MetricMemory:
		return s.MemoryPercent
	case MetricDisk:
		return s.DiskPercent
	default:
		return nil
	}
}

// MetricsCollector 抽象「对一组服务器采集已归一指标快照」的能力(复用 6-1;注入便于测试)。
// serverIDs 为空表示「全部已登记服务器」;实现逐台独立,不可达/失败仅令该台 Available=false。
type MetricsCollector interface {
	Collect(ctx context.Context, serverIDs []string) ([]ServerMetricsSnapshot, error)
}

// Service 定义异常检测领域对外接口(冻结契约;httpapi 消费)。
type Service interface {
	// ListRules 返回所有规则(创建时间倒序)。
	ListRules(ctx context.Context) ([]*Rule, error)
	// CreateRule 校验枚举后入库;返回规则视图。
	CreateRule(ctx context.Context, in CreateRuleInput) (*Rule, error)
	// DeleteRule 删除规则;不存在 → ErrNotFound。
	DeleteRule(ctx context.Context, id string) error
	// Check 对所有 enabled 规则求值:逐服务器逐规则,命中产告警入库,返回**本次新增**告警列表。
	// 指标不可达 / 该 metric 不可得的服务器跳过(不误报);无规则 → 空结果不报错。
	Check(ctx context.Context) ([]*Alert, error)
	// ListAlerts 返回最近告警(可按 serverID 过滤;limit 上限)。
	ListAlerts(ctx context.Context, serverID string, limit int) ([]*Alert, error)
}

// service 是 store + collector 支撑的 Service 实现。
type service struct {
	db        *sql.DB
	collector MetricsCollector
}

// New 构造 Service。collector 复用 6-1 采集(httpapi 适配注入);为 nil 时 Check 返回空结果
// (无采集源 → 不误报)。不在此做任何重活(无 init 副作用)。
func New(db *sql.DB, collector MetricsCollector) Service {
	return &service{db: db, collector: collector}
}

const defaultAlertLimit = 100
const maxAlertLimit = 500

func (s *service) ListRules(ctx context.Context) ([]*Rule, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, metric, operator, threshold, server_id, enabled, created_at
		 FROM anomaly_rules
		 ORDER BY created_at DESC, id`,
	)
	if err != nil {
		return nil, fmt.Errorf("anomaly: list rules: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]*Rule, 0)
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("anomaly: iterate rules: %w", err)
	}
	return out, nil
}

func (s *service) CreateRule(ctx context.Context, in CreateRuleInput) (*Rule, error) {
	if !validMetric(in.Metric) {
		return nil, ErrInvalidMetric
	}
	if !validOperator(in.Operator) {
		return nil, ErrInvalidOperator
	}
	id := uuid.NewString()
	nowStr := time.Now().UTC().Format(time.RFC3339)

	var serverID any
	if in.ServerID != nil && *in.ServerID != "" {
		serverID = *in.ServerID
	}
	enabled := 0
	if in.Enabled {
		enabled = 1
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO anomaly_rules (id, metric, operator, threshold, server_id, enabled, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, in.Metric, in.Operator, in.Threshold, serverID, enabled, nowStr,
	)
	if err != nil {
		return nil, fmt.Errorf("anomaly: insert rule: %w", err)
	}
	return s.getRule(ctx, id)
}

func (s *service) getRule(ctx context.Context, id string) (*Rule, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, metric, operator, threshold, server_id, enabled, created_at
		 FROM anomaly_rules WHERE id = ?`, id,
	)
	r, err := scanRule(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return r, nil
}

func (s *service) DeleteRule(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM anomaly_rules WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("anomaly: delete rule: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Check 逐服务器逐 enabled 规则求值。流程:
//  1. 取所有 enabled 规则;无规则 → 直接返回空(不采集、不报错)。
//  2. 经 collector 采集相关服务器指标快照(复用 6-1)。
//  3. 逐快照 × 逐规则(规则全局或匹配该服务器)求值:指标不可得 / 不可达 → 跳过。
//  4. 命中 → 产告警入库,收集进返回列表。
func (s *service) Check(ctx context.Context) ([]*Alert, error) {
	rules, err := s.ListRules(ctx)
	if err != nil {
		return nil, err
	}
	enabled := make([]*Rule, 0, len(rules))
	for _, r := range rules {
		if r.Enabled {
			enabled = append(enabled, r)
		}
	}
	if len(enabled) == 0 {
		return []*Alert{}, nil
	}
	if s.collector == nil {
		// 无采集源:不误报,返回空。
		return []*Alert{}, nil
	}

	snapshots, err := s.collector.Collect(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("anomaly: collect metrics: %w", err)
	}

	out := make([]*Alert, 0)
	for _, snap := range snapshots {
		if !snap.Available {
			// 不可达 / 定位失败:整台跳过,不误报。
			continue
		}
		for _, rule := range enabled {
			// 作用域:全局规则(ServerID==nil)对所有服务器;指定规则仅匹配该服务器。
			if rule.ServerID != nil && *rule.ServerID != snap.ServerID {
				continue
			}
			pv := snap.percentFor(rule.Metric)
			if pv == nil {
				// 该指标不可得(跨平台 best-effort / 解析失败):跳过,不误报。
				continue
			}
			if !evaluate(rule.Operator, *pv, rule.Threshold) {
				continue
			}
			alert, err := s.recordAlert(ctx, rule, snap, *pv)
			if err != nil {
				return nil, err
			}
			out = append(out, alert)
		}
	}
	return out, nil
}

// evaluate 求值 value <op> threshold。
func evaluate(operator string, value, threshold float64) bool {
	switch operator {
	case OperatorGreaterThan:
		return value > threshold
	case OperatorLessThan:
		return value < threshold
	default:
		return false
	}
}

// recordAlert 把一次命中持久化为告警并返回视图。
func (s *service) recordAlert(ctx context.Context, rule *Rule, snap ServerMetricsSnapshot, value float64) (*Alert, error) {
	id := uuid.NewString()
	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339)
	msg := formatMessage(rule.Metric, rule.Operator, value, rule.Threshold)

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO anomaly_alerts
		   (id, server_id, server_name, metric, operator, threshold, value, message, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, snap.ServerID, snap.ServerName, rule.Metric, rule.Operator,
		rule.Threshold, value, msg, nowStr,
	)
	if err != nil {
		return nil, fmt.Errorf("anomaly: insert alert: %w", err)
	}
	return &Alert{
		ID:         id,
		ServerID:   snap.ServerID,
		ServerName: snap.ServerName,
		Metric:     rule.Metric,
		Operator:   rule.Operator,
		Threshold:  rule.Threshold,
		Value:      value,
		Message:    msg,
		CreatedAt:  now,
	}, nil
}

// metricLabel 是 metric 的人读名(用于告警文案)。
func metricLabel(metric string) string {
	switch metric {
	case MetricCPU:
		return "CPU 使用率"
	case MetricMemory:
		return "内存使用率"
	case MetricDisk:
		return "磁盘使用率"
	default:
		return metric
	}
}

// operatorSymbol 是 operator 的人读符号。
func operatorSymbol(operator string) string {
	switch operator {
	case OperatorGreaterThan:
		return ">"
	case OperatorLessThan:
		return "<"
	default:
		return operator
	}
}

// formatMessage 拼人读告警文案,如「磁盘使用率 92.3% > 85%」。
func formatMessage(metric, operator string, value, threshold float64) string {
	return fmt.Sprintf("%s %.1f%% %s %g%%",
		metricLabel(metric), value, operatorSymbol(operator), threshold)
}

func (s *service) ListAlerts(ctx context.Context, serverID string, limit int) ([]*Alert, error) {
	if limit <= 0 {
		limit = defaultAlertLimit
	}
	if limit > maxAlertLimit {
		limit = maxAlertLimit
	}

	var (
		rows *sql.Rows
		err  error
	)
	if serverID != "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, server_id, server_name, metric, operator, threshold, value, message, created_at
			 FROM anomaly_alerts
			 WHERE server_id = ?
			 ORDER BY created_at DESC, id
			 LIMIT ?`, serverID, limit,
		)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, server_id, server_name, metric, operator, threshold, value, message, created_at
			 FROM anomaly_alerts
			 ORDER BY created_at DESC, id
			 LIMIT ?`, limit,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("anomaly: list alerts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]*Alert, 0)
	for rows.Next() {
		a, err := scanAlert(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("anomaly: iterate alerts: %w", err)
	}
	return out, nil
}

// scanner 抽象 *sql.Row 与 *sql.Rows 的 Scan。
type scanner interface {
	Scan(dest ...any) error
}

// scanRule 把一行扫描为 Rule(server_id 可空 → *string)。
func scanRule(sc scanner) (*Rule, error) {
	var r Rule
	var serverID sql.NullString
	var enabled int
	var createdStr string
	if err := sc.Scan(
		&r.ID, &r.Metric, &r.Operator, &r.Threshold, &serverID, &enabled, &createdStr,
	); err != nil {
		return nil, err
	}
	if serverID.Valid && serverID.String != "" {
		v := serverID.String
		r.ServerID = &v
	}
	r.Enabled = enabled != 0
	created, err := time.Parse(time.RFC3339, createdStr)
	if err != nil {
		return nil, fmt.Errorf("anomaly: parse rule created_at: %w", err)
	}
	r.CreatedAt = created
	return &r, nil
}

// scanAlert 把一行扫描为 Alert。
func scanAlert(sc scanner) (*Alert, error) {
	var a Alert
	var createdStr string
	if err := sc.Scan(
		&a.ID, &a.ServerID, &a.ServerName, &a.Metric, &a.Operator,
		&a.Threshold, &a.Value, &a.Message, &createdStr,
	); err != nil {
		return nil, err
	}
	created, err := time.Parse(time.RFC3339, createdStr)
	if err != nil {
		return nil, fmt.Errorf("anomaly: parse alert created_at: %w", err)
	}
	a.CreatedAt = created
	return &a, nil
}
