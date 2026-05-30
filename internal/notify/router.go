package notify

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// 通知事件枚举(冻结契约;DB 存小写串;JSON 同名)。run 终态 → 事件由 main 装配的 NotifyHook
// 映射;event 枚举本身在 5-2~5-4 不变(部署/健康事件随 Epic 4 细化时复用同枚举)。
const (
	// EventBuildSucceeded 构建成功(run 终态 success)。
	EventBuildSucceeded = "build_succeeded"
	// EventBuildFailed 构建失败(run 终态 failed)。
	EventBuildFailed = "build_failed"
	// EventDeploySucceeded 部署成功(Epic 4 细化;本期由 run 终态映射占位)。
	EventDeploySucceeded = "deploy_succeeded"
	// EventDeployFailed 部署失败(run 终态 partial_failed)。
	EventDeployFailed = "deploy_failed"
	// EventRollback 回滚(run 终态 rolled_back)。
	EventRollback = "rollback"
	// EventHealthCheckFailed 健康检查失败(Epic 4 细化)。
	EventHealthCheckFailed = "health_check_failed"
)

// 路由领域错误。
var (
	// ErrRouteNotFound 表示路由不存在。
	ErrRouteNotFound = errors.New("notify: route not found")
	// ErrInvalidEvent 表示事件枚举非法。
	ErrInvalidEvent = errors.New("notify: invalid event")
	// ErrRouteChannelNotFound 表示路由引用的渠道不存在(创建 route 时校验)。
	ErrRouteChannelNotFound = errors.New("notify: route channel not found")
)

// validEvent 报告事件是否为冻结枚举之一。
func validEvent(e string) bool {
	switch e {
	case EventBuildSucceeded, EventBuildFailed, EventDeploySucceeded,
		EventDeployFailed, EventRollback, EventHealthCheckFailed:
		return true
	default:
		return false
	}
}

// Events 返回冻结的事件枚举顺序列表(供 HTTP / 前端枚举展示;顺序稳定)。
func Events() []string {
	return []string{
		EventBuildSucceeded, EventBuildFailed,
		EventDeploySucceeded, EventDeployFailed,
		EventRollback, EventHealthCheckFailed,
	}
}

// Route 是「事件 → 渠道」映射的对外视图(冻结契约)。
// ProjectID 本期恒空(全局默认);5-4 加项目维度覆盖。
type Route struct {
	ID        string
	ProjectID string // 空 = 全局默认
	Event     string
	ChannelID string
	Enabled   bool
	CreatedAt time.Time
}

// CreateRouteInput 是创建路由的入参。
type CreateRouteInput struct {
	Event     string
	ChannelID string
	Enabled   bool
	// ProjectID 本期忽略(恒为全局);契约预留,5-4 消费。
	ProjectID string
}

// RouteService 定义事件路由 CRUD + 引擎(冻结契约,供 HTTP 层与 NotifyHook 消费)。
type RouteService interface {
	// ListRoutes 返回所有路由(按 event 再按 createdAt 升序;稳定排序)。
	ListRoutes(ctx context.Context) ([]Route, error)
	// CreateRoute 校验事件枚举 + 渠道存在性后持久化一条路由。
	// 事件非法 → ErrInvalidEvent;渠道不存在 → ErrRouteChannelNotFound。
	CreateRoute(ctx context.Context, in CreateRouteInput) (*Route, error)
	// DeleteRoute 删除一条路由。不存在 → ErrRouteNotFound。
	DeleteRoute(ctx context.Context, id string) error
	// RouteEvent 路由一次事件:查该 event 的所有 enabled route → 对每个 channelID 调
	// SendVia 发送 payload。**未配置任何路由 → 不发送(返回 nil)**(FR-20)。
	// best-effort:单渠道发送失败仅记日志、续发其余;整体返回 nil(绝不阻断调用方)。
	RouteEvent(ctx context.Context, event string, payload Payload) error
}

// ListRoutes 实现 RouteService.ListRoutes。
func (s *service) ListRoutes(ctx context.Context) ([]Route, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, event, channel_id, enabled, created_at
		 FROM notification_routes ORDER BY event, created_at, id`,
	)
	if err != nil {
		return nil, fmt.Errorf("notify: list routes: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]Route, 0)
	for rows.Next() {
		r, err := scanRoute(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("notify: iterate routes: %w", err)
	}
	return out, nil
}

// CreateRoute 实现 RouteService.CreateRoute。
func (s *service) CreateRoute(ctx context.Context, in CreateRouteInput) (*Route, error) {
	event := strings.TrimSpace(in.Event)
	if !validEvent(event) {
		return nil, ErrInvalidEvent
	}
	channelID := strings.TrimSpace(in.ChannelID)
	if channelID == "" {
		return nil, ErrRouteChannelNotFound
	}
	// 渠道存在性校验:不存在 → 人读错误(避免悬挂路由)。
	if _, _, err := s.load(ctx, channelID); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrRouteChannelNotFound
		}
		return nil, err
	}

	id := uuid.NewString()
	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339)
	// 本期 project_id 恒空(全局默认);5-4 填充项目维度。
	var projectID any
	if strings.TrimSpace(in.ProjectID) != "" {
		projectID = strings.TrimSpace(in.ProjectID)
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO notification_routes (id, project_id, event, channel_id, enabled, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		id, projectID, event, channelID, boolToInt(in.Enabled), nowStr,
	)
	if err != nil {
		return nil, fmt.Errorf("notify: insert route: %w", err)
	}

	return &Route{
		ID:        id,
		Event:     event,
		ChannelID: channelID,
		Enabled:   in.Enabled,
		CreatedAt: now,
	}, nil
}

// DeleteRoute 实现 RouteService.DeleteRoute。
func (s *service) DeleteRoute(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM notification_routes WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("notify: delete route: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrRouteNotFound
	}
	return nil
}

// RouteEvent 实现 RouteService.RouteEvent(冻结契约,供 NotifyHook 调用)。
//
// 查该 event 的所有 enabled route → 收集渠道 id(去重)→ 对每个 channelID 调 SendVia。
// **未配置任何路由 → 不发送(返回 nil)**(FR-20)。best-effort:单渠道失败仅记日志、续发
// 其余;整体始终返回 nil(绝不把发送失败冒泡为阻断 run 终态/500 的错误,NFR-10)。
func (s *service) RouteEvent(ctx context.Context, event string, payload Payload) error {
	if !validEvent(event) {
		// 未知事件:防御性忽略(不发、不报错)。
		log.Printf("[notify] route event 跳过:未知事件 %q", event)
		return nil
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT channel_id FROM notification_routes
		 WHERE event = ? AND enabled = 1`, event,
	)
	if err != nil {
		// 查询失败:best-effort,仅记日志(不阻断调用方)。
		log.Printf("[notify] route event %q: 查询路由失败:%v", event, err)
		return nil
	}

	seen := make(map[string]bool)
	var channelIDs []string
	for rows.Next() {
		var cid string
		if err := rows.Scan(&cid); err != nil {
			log.Printf("[notify] route event %q: 扫描路由失败:%v", event, err)
			_ = rows.Close()
			return nil
		}
		if seen[cid] {
			continue // 同事件多条 route 指向同渠道:只发一次。
		}
		seen[cid] = true
		channelIDs = append(channelIDs, cid)
	}
	_ = rows.Close()

	// 未配置任何路由(FR-20):不发送。
	if len(channelIDs) == 0 {
		return nil
	}
	// 稳定顺序(便于日志/测试)。
	sort.Strings(channelIDs)

	for _, cid := range channelIDs {
		// SendVia 内部:渠道不存在 → ErrNotFound;未启用 → 优雅跳过(nil)。
		if derr := s.SendVia(ctx, cid, payload); derr != nil {
			// 单渠道失败:记日志、续发其余(best-effort)。错误体已人读、绝无敏感字段。
			log.Printf("[notify] route event %q: 渠道 %s 发送失败(已跳过续发):%v", event, cid, derr)
		}
	}
	return nil
}

// scanRoute 把一行扫描为 Route 视图。
func scanRoute(sc scanner) (*Route, error) {
	var (
		r          Route
		projectID  sql.NullString
		enabledInt int
		createdStr string
	)
	if err := sc.Scan(&r.ID, &projectID, &r.Event, &r.ChannelID, &enabledInt, &createdStr); err != nil {
		return nil, fmt.Errorf("notify: scan route: %w", err)
	}
	if projectID.Valid {
		r.ProjectID = projectID.String
	}
	r.Enabled = enabledInt != 0
	created, err := time.Parse(time.RFC3339, createdStr)
	if err != nil {
		return nil, fmt.Errorf("notify: parse route created_at: %w", err)
	}
	r.CreatedAt = created
	return &r, nil
}

// EventPayload 构造一个事件的默认通知 Payload(本期内置默认文案;5-3 模板渲染后接管)。
//
// 入参为运行的展示元数据(项目名 / 分支 / commit / 状态 / 耗时);任一为空则在文案中省略。
// 形状对齐冻结 Payload(Title/Body/Fields),5-3 仅替换文案生成方式、不改形状。
func EventPayload(event, projectName, branch, commit, status string, durationMs int64) Payload {
	title := eventTitle(event, projectName)

	fields := map[string]string{
		"event": event,
	}
	if projectName != "" {
		fields["project"] = projectName
	}
	if branch != "" {
		fields["branch"] = branch
	}
	if commit != "" {
		fields["commit"] = shortCommit(commit)
	}
	if status != "" {
		fields["status"] = status
	}
	if durationMs > 0 {
		fields["duration"] = humanDuration(durationMs)
	}

	var b strings.Builder
	b.WriteString(eventLabel(event))
	if projectName != "" {
		b.WriteString(":项目 ")
		b.WriteString(projectName)
	}
	if branch != "" {
		b.WriteString(",分支 ")
		b.WriteString(branch)
	}
	if commit != "" {
		b.WriteString(",commit ")
		b.WriteString(shortCommit(commit))
	}
	if status != "" {
		b.WriteString(",状态 ")
		b.WriteString(status)
	}
	if durationMs > 0 {
		b.WriteString(",耗时 ")
		b.WriteString(humanDuration(durationMs))
	}
	b.WriteString("。")

	return Payload{Title: title, Body: b.String(), Fields: fields}
}

// eventLabel 返回事件的中文标签(默认文案)。
func eventLabel(event string) string {
	switch event {
	case EventBuildSucceeded:
		return "构建成功"
	case EventBuildFailed:
		return "构建失败"
	case EventDeploySucceeded:
		return "部署成功"
	case EventDeployFailed:
		return "部署失败"
	case EventRollback:
		return "已回滚"
	case EventHealthCheckFailed:
		return "健康检查失败"
	default:
		return event
	}
}

// eventTitle 组合通知标题。
func eventTitle(event, projectName string) string {
	label := eventLabel(event)
	if projectName != "" {
		return fmt.Sprintf("[%s] %s", projectName, label)
	}
	return label
}

// shortCommit 截短 commit 至前 8 位(空则原样)。
func shortCommit(commit string) string {
	c := strings.TrimSpace(commit)
	if len(c) > 8 {
		return c[:8]
	}
	return c
}

// humanDuration 把毫秒格式化为人读耗时(如 1.2s / 3m05s)。
func humanDuration(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	if d < time.Second {
		return fmt.Sprintf("%dms", ms)
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	m := int(d / time.Minute)
	sec := int((d % time.Minute) / time.Second)
	return fmt.Sprintf("%dm%02ds", m, sec)
}
