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
	"github.com/huangchengsir/pipewright/internal/i18n"
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
	// EventApprovalRequired 需要人工审批(run 进入 waiting_approval 时发,携带签名审批链接)。
	EventApprovalRequired = "approval_required"
	// EventAnomalyDetected 服务器指标异常(异常检测命中阈值规则;非 run 维度,projectID 空=全局路由)。
	EventAnomalyDetected = "anomaly_detected"
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
		EventDeployFailed, EventRollback, EventHealthCheckFailed, EventApprovalRequired,
		EventAnomalyDetected:
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
		EventApprovalRequired, EventAnomalyDetected,
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
	// ListRoutes 返回所有全局路由(projectId 为空;按 event 再按 createdAt 升序;稳定排序)。
	// 兼容 5-2 调用方;5-4 加项目维度过滤经 ListRoutesForProject。
	ListRoutes(ctx context.Context) ([]Route, error)
	// ListRoutesForProject 返回指定作用域路由(Story 5.4):projectID 非空 → 仅该项目级路由;
	// projectID 空 → 仅全局路由(project_id IS NULL)。稳定排序同 ListRoutes。
	ListRoutesForProject(ctx context.Context, projectID string) ([]Route, error)
	// CreateRoute 校验事件枚举 + 渠道存在性后持久化一条路由。
	// 事件非法 → ErrInvalidEvent;渠道不存在 → ErrRouteChannelNotFound。
	// in.ProjectID 非空 = 项目级路由(5-4);空 = 全局默认。
	CreateRoute(ctx context.Context, in CreateRouteInput) (*Route, error)
	// DeleteRoute 删除一条路由。不存在 → ErrRouteNotFound。
	DeleteRoute(ctx context.Context, id string) error
	// RouteEvent 路由一次事件(全局):查该 event 的所有 enabled 全局 route → 对每个 channelID 调
	// SendVia 发送 payload。**未配置任何路由 → 不发送(返回 nil)**(FR-20)。
	// best-effort:单渠道发送失败仅记日志、续发其余;整体返回 nil(绝不阻断调用方)。
	// 兼容 5-2 全局路径;5-4 项目维度经 RouteEventForProject。
	RouteEvent(ctx context.Context, event string, payload Payload) error
	// RouteEventVars 路由一次事件并按渠道渲染模板(Story 5.3;FR-21;全局):查 enabled 全局 route →
	// 对每渠道经 RenderPayload(最具体匹配模板;无则平台默认)产 Payload → SendVia。
	// 未配置任何路由 → 不发送;best-effort,始终返回 nil。
	RouteEventVars(ctx context.Context, event string, vars TemplateVars) error
	// RouteEventForProject 按项目维度路由一次事件并渲染模板(Story 5.4;FR-20 细粒度,签名冻结)。
	//
	// **整体覆盖语义**:先查 projectID + event 的 enabled 项目级路由;非空 → **只**用项目级
	// (不与全局合并);空 → 回退全局(project_id IS NULL)路由。projectID 为空时等价 RouteEventVars
	// (全局路径)。未配置任何路由 → 不发送;best-effort,始终返回 nil。
	RouteEventForProject(ctx context.Context, projectID, event string, vars TemplateVars) error
}

// ListRoutes 实现 RouteService.ListRoutes(全局路由,兼容 5-2)。
func (s *service) ListRoutes(ctx context.Context) ([]Route, error) {
	return s.ListRoutesForProject(ctx, "")
}

// ListRoutesForProject 实现 RouteService.ListRoutesForProject(Story 5.4 作用域过滤)。
func (s *service) ListRoutesForProject(ctx context.Context, projectID string) ([]Route, error) {
	projectID = strings.TrimSpace(projectID)
	var (
		rows *sql.Rows
		err  error
	)
	if projectID == "" {
		// 全局路由:project_id IS NULL。
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, project_id, event, channel_id, enabled, created_at
			 FROM notification_routes WHERE project_id IS NULL
			 ORDER BY event, created_at, id`,
		)
	} else {
		// 项目级路由:project_id = ?。
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, project_id, event, channel_id, enabled, created_at
			 FROM notification_routes WHERE project_id = ?
			 ORDER BY event, created_at, id`, projectID,
		)
	}
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
	// project_id:非空 = 项目级覆盖(5-4);空 = 全局默认(存 NULL)。
	scopedProject := strings.TrimSpace(in.ProjectID)
	var projectID any
	if scopedProject != "" {
		projectID = scopedProject
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
		ProjectID: scopedProject,
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
	channelIDs := s.channelIDsForEvent(ctx, event)
	// 未配置任何路由(FR-20):不发送。
	for _, cid := range channelIDs {
		// SendVia 内部:渠道不存在 → ErrNotFound;未启用 → 优雅跳过(nil)。
		if derr := s.SendVia(ctx, cid, payload); derr != nil {
			// 单渠道失败:记日志、续发其余(best-effort)。错误体已人读、绝无敏感字段。
			log.Printf("[notify] route event %q: 渠道 %s 发送失败(已跳过续发):%v", event, cid, derr)
		}
	}
	return nil
}

// RouteEventVars 路由一次事件并按渠道渲染模板(Story 5.3;FR-21,供 NotifyHook 消费)。
//
// 查该 event 的所有 enabled route → 对每个 channelID 经 RenderPayload 按该渠道最具体匹配的模板
// 渲染 Payload(无模板 → 平台默认,5-2 行为不变)→ SendVia。**未配置任何路由 → 不发送**。
// best-effort:单渠道失败仅记日志、续发其余;整体始终返回 nil(绝不阻断调用方,NFR-10)。
func (s *service) RouteEventVars(ctx context.Context, event string, vars TemplateVars) error {
	channelIDs := s.channelIDsForEvent(ctx, event)
	for _, cid := range channelIDs {
		// 按渠道渲染:channelID 精确模板 > 该事件通用模板 > 平台默认(无 RCE 纯文本替换)。
		payload := s.RenderPayload(ctx, event, cid, vars)
		if derr := s.SendVia(ctx, cid, payload); derr != nil {
			log.Printf("[notify] route event %q: 渠道 %s 发送失败(已跳过续发):%v", event, cid, derr)
		}
	}
	return nil
}

// RouteEventForProject 实现 RouteService.RouteEventForProject(Story 5.4;签名冻结)。
//
// **整体覆盖语义**:先查 projectID + event 的 enabled 项目级路由;非空 → **只**用项目级渠道集
// (不与全局合并);空 → 回退全局(project_id IS NULL)路由。projectID 为空时直接走全局。
// 渠道集确定后按渠道渲染模板(RenderPayload)→ SendVia。**未配置任何路由 → 不发送**;
// best-effort:单渠道失败仅记日志、续发其余;整体始终返回 nil(绝不阻断调用方,NFR-10)。
func (s *service) RouteEventForProject(ctx context.Context, projectID, event string, vars TemplateVars) error {
	channelIDs := s.channelIDsForEventScoped(ctx, projectID, event)
	for _, cid := range channelIDs {
		payload := s.RenderPayload(ctx, event, cid, vars)
		if derr := s.SendVia(ctx, cid, payload); derr != nil {
			log.Printf("[notify] route event %q(project=%q): 渠道 %s 发送失败(已跳过续发):%v", event, projectID, cid, derr)
		}
	}
	return nil
}

// channelIDsForEvent 查该 event 的所有 enabled **全局** route 渠道 id(去重 + 稳定排序;兼容 5-2)。
// 未配置任何路由 / 查询失败 → 返回空(best-effort,不发不报错;FR-20)。
func (s *service) channelIDsForEvent(ctx context.Context, event string) []string {
	return s.channelIDsForEventScoped(ctx, "", event)
}

// channelIDsForEventScoped 按项目维度查 enabled route 渠道 id(Story 5.4;去重 + 稳定排序)。
//
// 整体覆盖:projectID 非空时**先**查该项目级路由,命中(非空)则只用项目级、不回退全局;
// 项目级为空 → 回退全局(project_id IS NULL)。projectID 空时直接查全局。
// 未配置任何路由 / 查询失败 → 返回空(best-effort,不发不报错;FR-20)。
func (s *service) channelIDsForEventScoped(ctx context.Context, projectID, event string) []string {
	if !validEvent(event) {
		// 未知事件:防御性忽略(不发、不报错)。
		log.Printf("[notify] route event 跳过:未知事件 %q", event)
		return nil
	}

	projectID = strings.TrimSpace(projectID)
	if projectID != "" {
		// 整体覆盖语义(code-review P8):项目级**存在任一路由**(无论 enabled)→ 该项目该事件
		// **只**用项目级(enabled 子集,可能为空=该项目此事件静音),**不回退全局**。
		// 此前用 enabled 渠道的存在性同时承担「是否覆盖」与「发哪些」→ 项目级全 disabled 会被
		// 误判为「无覆盖」而偷偷走全局,违反「继承 or 自定义二选一」。
		if s.projectHasRoute(ctx, projectID, event) {
			return s.queryChannelIDs(ctx, projectID, event)
		}
	}
	// 无项目级覆盖 → 继承全局(project_id IS NULL)。
	return s.queryChannelIDs(ctx, "", event)
}

// projectHasRoute 报告某项目+事件是否存在**任一**路由(无论 enabled);用于判定是否整体覆盖全局。
func (s *service) projectHasRoute(ctx context.Context, projectID, event string) bool {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM notification_routes WHERE project_id = ? AND event = ?`,
		projectID, event,
	).Scan(&n)
	if err != nil {
		log.Printf("[notify] projectHasRoute(project=%q,event=%q): %v", projectID, event, err)
		return false
	}
	return n > 0
}

// queryChannelIDs 查指定作用域(projectID 空 = 全局 IS NULL;非空 = 该项目)某 event 的 enabled
// route 渠道 id(去重 + 稳定排序)。查询/扫描失败 → 返回空(best-effort)。
func (s *service) queryChannelIDs(ctx context.Context, projectID, event string) []string {
	var (
		rows *sql.Rows
		err  error
	)
	if projectID == "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT channel_id FROM notification_routes
			 WHERE project_id IS NULL AND event = ? AND enabled = 1`, event,
		)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT channel_id FROM notification_routes
			 WHERE project_id = ? AND event = ? AND enabled = 1`, projectID, event,
		)
	}
	if err != nil {
		// 查询失败:best-effort,仅记日志(不阻断调用方)。
		log.Printf("[notify] route event %q(project=%q): 查询路由失败:%v", event, projectID, err)
		return nil
	}

	seen := make(map[string]bool)
	var channelIDs []string
	for rows.Next() {
		var cid string
		if err := rows.Scan(&cid); err != nil {
			log.Printf("[notify] route event %q(project=%q): 扫描路由失败:%v", event, projectID, err)
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

	// 稳定顺序(便于日志/测试)。
	sort.Strings(channelIDs)
	return channelIDs
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
func EventPayload(lang, event, projectName, branch, commit, status string, durationMs int64) Payload {
	if lang == "" {
		lang = i18n.Default
	}
	title := eventTitle(event, projectName, lang)

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

	// 正文:事件标签 + 「字段标签 值」列表(本地化标签),分隔/收尾随语言。
	parts := make([]string, 0, 5)
	if projectName != "" {
		parts = append(parts, i18n.T(lang, "项目")+" "+projectName)
	}
	if branch != "" {
		parts = append(parts, i18n.T(lang, "分支")+" "+branch)
	}
	if commit != "" {
		parts = append(parts, i18n.T(lang, "提交")+" "+shortCommit(commit))
	}
	if status != "" {
		parts = append(parts, i18n.T(lang, "状态")+" "+status)
	}
	if durationMs > 0 {
		parts = append(parts, i18n.T(lang, "耗时")+" "+humanDuration(durationMs))
	}
	var b strings.Builder
	b.WriteString(eventLabel(event, lang))
	if len(parts) > 0 {
		b.WriteString(bodySeparator(lang))
		b.WriteString(strings.Join(parts, bodyComma(lang)))
	}
	b.WriteString(bodyPeriod(lang))

	return Payload{Title: title, Body: b.String(), Fields: fields, Lang: lang}
}

// 正文标点随语言:中文/日文用全角,其余用半角。
func bodySeparator(lang string) string {
	if lang == "zh-CN" || lang == "zh-TW" || lang == "ja" {
		return ":"
	}
	return ": "
}
func bodyComma(lang string) string {
	if lang == "zh-CN" || lang == "zh-TW" || lang == "ja" {
		return ","
	}
	return ", "
}
func bodyPeriod(lang string) string {
	if lang == "zh-CN" || lang == "zh-TW" || lang == "ja" {
		return "。"
	}
	return "."
}

// eventLabel 返回事件标签(默认文案,按 lang 本地化)。
func eventLabel(event, lang string) string {
	switch event {
	case EventBuildSucceeded:
		return i18n.T(lang, "构建成功")
	case EventBuildFailed:
		return i18n.T(lang, "构建失败")
	case EventDeploySucceeded:
		return i18n.T(lang, "部署成功")
	case EventDeployFailed:
		return i18n.T(lang, "部署失败")
	case EventRollback:
		return i18n.T(lang, "已回滚")
	case EventHealthCheckFailed:
		return i18n.T(lang, "健康检查失败")
	case EventApprovalRequired:
		return i18n.T(lang, "需要审批")
	case EventAnomalyDetected:
		return i18n.T(lang, "异常检测")
	default:
		return event
	}
}

// eventTitle 组合通知标题。
func eventTitle(event, projectName, lang string) string {
	label := eventLabel(event, lang)
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
