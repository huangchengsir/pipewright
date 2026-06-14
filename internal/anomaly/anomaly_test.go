package anomaly

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/storetest"
	_ "modernc.org/sqlite"
)

// testDB 建一个内存库并应用 anomaly schema(独立于 store 包,免循环依赖)。
func testDB(t *testing.T) *sql.DB {
	return storetest.OpenDB(t)
}

// stubCollector 返回预置快照,不触网。
type stubCollector struct {
	snaps []ServerMetricsSnapshot
	err   error
}

func (c stubCollector) Collect(_ context.Context, _ []string) ([]ServerMetricsSnapshot, error) {
	return c.snaps, c.err
}

func ptr(s string) *string    { return &s }
func fptr(f float64) *float64 { return &f }

func TestCreateRuleValidation(t *testing.T) {
	svc := New(testDB(t), nil)
	ctx := context.Background()

	if _, err := svc.CreateRule(ctx, CreateRuleInput{Metric: "bogus", Operator: "gt", Threshold: 90, Enabled: true}); err != ErrInvalidMetric {
		t.Fatalf("want ErrInvalidMetric, got %v", err)
	}
	if _, err := svc.CreateRule(ctx, CreateRuleInput{Metric: "cpu", Operator: "ge", Threshold: 90, Enabled: true}); err != ErrInvalidOperator {
		t.Fatalf("want ErrInvalidOperator, got %v", err)
	}
	r, err := svc.CreateRule(ctx, CreateRuleInput{Metric: "cpu", Operator: "gt", Threshold: 90, Enabled: true})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if r.ID == "" || r.ServerID != nil || !r.Enabled {
		t.Fatalf("rule wrong: %+v", r)
	}
}

func TestCreateRuleScopedServer(t *testing.T) {
	svc := New(testDB(t), nil)
	ctx := context.Background()
	r, err := svc.CreateRule(ctx, CreateRuleInput{Metric: "disk", Operator: "gt", Threshold: 85, ServerID: ptr("srv-1"), Enabled: true})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if r.ServerID == nil || *r.ServerID != "srv-1" {
		t.Fatalf("scoped serverId wrong: %+v", r)
	}
	// 列表回读保持。
	rules, _ := svc.ListRules(ctx)
	if len(rules) != 1 || rules[0].ServerID == nil || *rules[0].ServerID != "srv-1" {
		t.Fatalf("list scoped wrong: %+v", rules)
	}
}

func TestDeleteRule(t *testing.T) {
	svc := New(testDB(t), nil)
	ctx := context.Background()
	r, _ := svc.CreateRule(ctx, CreateRuleInput{Metric: "cpu", Operator: "gt", Threshold: 90, Enabled: true})
	if err := svc.DeleteRule(ctx, r.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := svc.DeleteRule(ctx, r.ID); err != ErrNotFound {
		t.Fatalf("second delete want ErrNotFound, got %v", err)
	}
}

// TestUpdateRule:整体更新规则(改阈值/operator/scope/enabled)+ 不存在 → ErrNotFound。
func TestUpdateRule(t *testing.T) {
	svc := New(testDB(t), nil)
	ctx := context.Background()
	r, _ := svc.CreateRule(ctx, CreateRuleInput{Metric: "cpu", Operator: "gt", Threshold: 90, Enabled: true})

	up, err := svc.UpdateRule(ctx, r.ID, CreateRuleInput{Metric: "disk", Operator: "lt", Threshold: 10, ServerID: ptr("s1"), Enabled: false})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if up.Metric != "disk" || up.Operator != "lt" || up.Threshold != 10 || up.ServerID == nil || *up.ServerID != "s1" || up.Enabled {
		t.Fatalf("update wrong: %+v", up)
	}
	if up.CreatedAt != r.CreatedAt {
		t.Fatalf("created_at must be preserved: %v vs %v", up.CreatedAt, r.CreatedAt)
	}
	// 校验枚举。
	if _, err := svc.UpdateRule(ctx, r.ID, CreateRuleInput{Metric: "bogus", Operator: "gt", Threshold: 1}); err != ErrInvalidMetric {
		t.Fatalf("want ErrInvalidMetric, got %v", err)
	}
	// 不存在。
	if _, err := svc.UpdateRule(ctx, "nope", CreateRuleInput{Metric: "cpu", Operator: "gt", Threshold: 1}); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// TestCheckDiskHitsLowThreshold 对应本机真验:disk gt 1 必命中 → 产 disk 告警(value>threshold)。
func TestCheckDiskHitsLowThreshold(t *testing.T) {
	disk := 92.3
	col := stubCollector{snaps: []ServerMetricsSnapshot{
		{ServerID: "s1", ServerName: "web-1", Available: true, DiskPercent: &disk},
	}}
	svc := New(testDB(t), col)
	ctx := context.Background()
	if _, err := svc.CreateRule(ctx, CreateRuleInput{Metric: "disk", Operator: "gt", Threshold: 1, Enabled: true}); err != nil {
		t.Fatalf("create: %v", err)
	}
	alerts, err := svc.Check(ctx)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if len(alerts) != 1 {
		t.Fatalf("want 1 alert, got %d", len(alerts))
	}
	a := alerts[0]
	if a.Metric != "disk" || a.Value <= a.Threshold || a.ServerID != "s1" || a.ServerName != "web-1" {
		t.Fatalf("alert wrong: %+v", a)
	}
	// 入库可见。
	stored, _ := svc.ListAlerts(ctx, "", 0)
	if len(stored) != 1 || stored[0].Message == "" {
		t.Fatalf("alert not persisted: %+v", stored)
	}
}

// TestCheckCPUMissesHighThreshold 对应真验:cpu gt 9999 不命中 → 无告警。
func TestCheckCPUMissesHighThreshold(t *testing.T) {
	cpu := 12.5
	col := stubCollector{snaps: []ServerMetricsSnapshot{
		{ServerID: "s1", ServerName: "web-1", Available: true, CPUPercent: &cpu},
	}}
	svc := New(testDB(t), col)
	ctx := context.Background()
	_, _ = svc.CreateRule(ctx, CreateRuleInput{Metric: "cpu", Operator: "gt", Threshold: 9999, Enabled: true})
	alerts, err := svc.Check(ctx)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if len(alerts) != 0 {
		t.Fatalf("want 0 alerts, got %d: %+v", len(alerts), alerts)
	}
}

// TestCheckSkipsUnreachableAndNullMetric:不可达 / 指标 null 的服务器跳过(不误报)。
func TestCheckSkipsUnreachableAndNullMetric(t *testing.T) {
	col := stubCollector{snaps: []ServerMetricsSnapshot{
		// 不可达 → 整台跳过。
		{ServerID: "down", ServerName: "down", Available: false},
		// 可达但 memory 指标 null(如 macOS 无 free)→ memory 规则跳过。
		{ServerID: "mac", ServerName: "mac", Available: true, MemoryPercent: nil, DiskPercent: fptr(50)},
	}}
	svc := New(testDB(t), col)
	ctx := context.Background()
	// 全局 memory 规则(低阈值,若不跳过会误命中)。
	_, _ = svc.CreateRule(ctx, CreateRuleInput{Metric: "memory", Operator: "gt", Threshold: 1, Enabled: true})
	alerts, err := svc.Check(ctx)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if len(alerts) != 0 {
		t.Fatalf("unreachable/null metric must be skipped, got %d: %+v", len(alerts), alerts)
	}
}

// TestCheckNoRulesNoError:无规则 → 空检测不报错(且不调用采集)。
func TestCheckNoRulesNoError(t *testing.T) {
	col := stubCollector{err: context.Canceled} // 若被调用会暴露
	svc := New(testDB(t), col)
	alerts, err := svc.Check(context.Background())
	if err != nil {
		t.Fatalf("no rules should not error: %v", err)
	}
	if len(alerts) != 0 {
		t.Fatalf("want empty, got %d", len(alerts))
	}
}

// TestCheckScopedRuleOnlyMatchesServer:指定服务器规则不作用于其它服务器。
func TestCheckScopedRuleOnlyMatchesServer(t *testing.T) {
	d := 90.0
	col := stubCollector{snaps: []ServerMetricsSnapshot{
		{ServerID: "s1", ServerName: "s1", Available: true, DiskPercent: &d},
		{ServerID: "s2", ServerName: "s2", Available: true, DiskPercent: &d},
	}}
	svc := New(testDB(t), col)
	ctx := context.Background()
	_, _ = svc.CreateRule(ctx, CreateRuleInput{Metric: "disk", Operator: "gt", Threshold: 1, ServerID: ptr("s1"), Enabled: true})
	alerts, _ := svc.Check(ctx)
	if len(alerts) != 1 || alerts[0].ServerID != "s1" {
		t.Fatalf("scoped rule should only hit s1, got %+v", alerts)
	}
}

// TestCheckDisabledRuleSkipped:未启用规则不参与检测。
func TestCheckDisabledRuleSkipped(t *testing.T) {
	d := 90.0
	col := stubCollector{snaps: []ServerMetricsSnapshot{{ServerID: "s1", ServerName: "s1", Available: true, DiskPercent: &d}}}
	svc := New(testDB(t), col)
	ctx := context.Background()
	_, _ = svc.CreateRule(ctx, CreateRuleInput{Metric: "disk", Operator: "gt", Threshold: 1, Enabled: false})
	alerts, _ := svc.Check(ctx)
	if len(alerts) != 0 {
		t.Fatalf("disabled rule must not fire, got %+v", alerts)
	}
}

// TestListAlertsFilterAndLimit:按 serverId 过滤 + limit 上限。
func TestListAlertsFilterAndLimit(t *testing.T) {
	d := 90.0
	col := stubCollector{snaps: []ServerMetricsSnapshot{
		{ServerID: "s1", ServerName: "s1", Available: true, DiskPercent: &d},
		{ServerID: "s2", ServerName: "s2", Available: true, DiskPercent: &d},
	}}
	svc := New(testDB(t), col)
	ctx := context.Background()
	_, _ = svc.CreateRule(ctx, CreateRuleInput{Metric: "disk", Operator: "gt", Threshold: 1, Enabled: true})
	if _, err := svc.Check(ctx); err != nil {
		t.Fatalf("check: %v", err)
	}
	all, _ := svc.ListAlerts(ctx, "", 0)
	if len(all) != 2 {
		t.Fatalf("want 2 alerts, got %d", len(all))
	}
	s1, _ := svc.ListAlerts(ctx, "s1", 0)
	if len(s1) != 1 || s1[0].ServerID != "s1" {
		t.Fatalf("filter s1 wrong: %+v", s1)
	}
	lim, _ := svc.ListAlerts(ctx, "", 1)
	if len(lim) != 1 {
		t.Fatalf("limit 1 wrong: %d", len(lim))
	}
}

// stubNotifier 记录收到的告警(校验通知触发 + 去重)。
type stubNotifier struct{ got []*Alert }

func (n *stubNotifier) NotifyAlert(_ context.Context, a *Alert) { n.got = append(n.got, a) }

// TestCheckNotifierInvokedOnNewAlert:命中新告警 → 通知器收到一次(带正确字段)。
func TestCheckNotifierInvokedOnNewAlert(t *testing.T) {
	disk := 92.3
	col := stubCollector{snaps: []ServerMetricsSnapshot{
		{ServerID: "s1", ServerName: "web-1", Available: true, DiskPercent: &disk},
	}}
	nf := &stubNotifier{}
	svc := New(testDB(t), col, WithNotifier(nf), WithCooldown(time.Hour))
	ctx := context.Background()
	_, _ = svc.CreateRule(ctx, CreateRuleInput{Metric: "disk", Operator: "gt", Threshold: 1, Enabled: true})
	if _, err := svc.Check(ctx); err != nil {
		t.Fatalf("check: %v", err)
	}
	if len(nf.got) != 1 {
		t.Fatalf("want notifier called once, got %d", len(nf.got))
	}
	if nf.got[0].ServerID != "s1" || nf.got[0].Metric != "disk" {
		t.Fatalf("notified alert wrong: %+v", nf.got[0])
	}
}

// TestCheckCooldownDedup:cooldown 窗口内同「服务器×规则条件」重复命中 → 不再产告警/不再通知。
func TestCheckCooldownDedup(t *testing.T) {
	disk := 92.3
	col := stubCollector{snaps: []ServerMetricsSnapshot{
		{ServerID: "s1", ServerName: "web-1", Available: true, DiskPercent: &disk},
	}}
	nf := &stubNotifier{}
	svc := New(testDB(t), col, WithNotifier(nf), WithCooldown(time.Hour))
	ctx := context.Background()
	_, _ = svc.CreateRule(ctx, CreateRuleInput{Metric: "disk", Operator: "gt", Threshold: 1, Enabled: true})

	first, _ := svc.Check(ctx)
	second, _ := svc.Check(ctx) // 立即再检测:在 cooldown 内 → 跳过

	if len(first) != 1 {
		t.Fatalf("first check want 1, got %d", len(first))
	}
	if len(second) != 0 {
		t.Fatalf("second check within cooldown want 0, got %d", len(second))
	}
	stored, _ := svc.ListAlerts(ctx, "", 0)
	if len(stored) != 1 {
		t.Fatalf("dedup should keep a single alert, got %d", len(stored))
	}
	if len(nf.got) != 1 {
		t.Fatalf("notifier should fire once across cooldown, got %d", len(nf.got))
	}
}

// TestCheckRefiresAfterCooldown:窗口外(把已存告警时间改老)→ 再次命中重新产告警 + 通知。
func TestCheckRefiresAfterCooldown(t *testing.T) {
	disk := 92.3
	col := stubCollector{snaps: []ServerMetricsSnapshot{
		{ServerID: "s1", ServerName: "web-1", Available: true, DiskPercent: &disk},
	}}
	nf := &stubNotifier{}
	db := testDB(t)
	svc := New(db, col, WithNotifier(nf), WithCooldown(time.Hour))
	ctx := context.Background()
	_, _ = svc.CreateRule(ctx, CreateRuleInput{Metric: "disk", Operator: "gt", Threshold: 1, Enabled: true})

	if _, err := svc.Check(ctx); err != nil {
		t.Fatalf("check1: %v", err)
	}
	// 把已存告警的 created_at 改到 2 小时前(> cooldown 窗口),模拟冷却已过。
	old := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)
	if _, err := db.ExecContext(ctx, `UPDATE anomaly_alerts SET created_at = ?`, old); err != nil {
		t.Fatalf("age alert: %v", err)
	}
	again, err := svc.Check(ctx)
	if err != nil {
		t.Fatalf("check2: %v", err)
	}
	if len(again) != 1 {
		t.Fatalf("after cooldown should refire, got %d", len(again))
	}
	if len(nf.got) != 2 {
		t.Fatalf("notifier should fire twice (once per window), got %d", len(nf.got))
	}
}

func TestEvaluate(t *testing.T) {
	if !evaluate("gt", 92, 85) || evaluate("gt", 80, 85) {
		t.Fatalf("gt wrong")
	}
	if !evaluate("lt", 5, 10) || evaluate("lt", 20, 10) {
		t.Fatalf("lt wrong")
	}
	if evaluate("bogus", 1, 0) {
		t.Fatalf("unknown op must be false")
	}
}
