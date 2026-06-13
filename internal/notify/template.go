package notify

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// 模板领域错误。
var (
	// ErrTemplateNotFound 表示模板不存在。
	ErrTemplateNotFound = errors.New("notify: template not found")
	// ErrTemplateChannelNotFound 表示模板引用的渠道不存在(创建/更新时校验)。
	ErrTemplateChannelNotFound = errors.New("notify: template channel not found")
)

// Template 是「事件(可选按渠道)→ 标题/正文模板」的对外视图(冻结契约;FR-21)。
//
// 渲染时占位 {{name}} 纯文本替换;变量集冻结(见 TemplateVars)。匹配优先级:
// channelID 精确 > 该事件通用(ChannelID 空)> 平台默认(无匹配回 EventPayload)。
// ProjectID 本期恒空(全局默认);5-4 加项目维度覆盖,不改形状。
type Template struct {
	ID            string
	ProjectID     string // 空 = 全局默认
	Event         string
	ChannelID     string // 空 = 该事件所有渠道通用;非空 = 仅该渠道
	TitleTemplate string
	BodyTemplate  string
	CreatedAt     time.Time
}

// CreateTemplateInput 是创建模板的入参。
type CreateTemplateInput struct {
	Event         string
	ChannelID     string // 空 = 该事件通用
	TitleTemplate string
	BodyTemplate  string
	// ProjectID 本期忽略(恒为全局);契约预留,5-4 消费。
	ProjectID string
}

// UpdateTemplateInput 是更新模板的入参;指针字段为 nil 表示不修改。
type UpdateTemplateInput struct {
	Event         *string
	ChannelID     *string // 非 nil:空串=改为通用,非空=改为指定渠道
	TitleTemplate *string
	BodyTemplate  *string
}

// TemplateVars 是渲染上下文(冻结变量集;FR-21)。
//   - Project / Branch / Commit(短)/ Status / Event:运行展示元数据。
//   - DurationMs:耗时毫秒(字符串化);RunID:运行 ID。
//   - ErrorSummary:失败摘要(已尽力脱敏;绝无明文 secret)。
type TemplateVars struct {
	Project      string
	Branch       string
	Commit       string
	Status       string
	Event        string
	DurationMs   string
	RunID        string
	ErrorSummary string
}

// asMap 把 TemplateVars 展开为占位名 → 值的映射(冻结占位名)。
func (v TemplateVars) asMap() map[string]string {
	return map[string]string{
		"project":      v.Project,
		"branch":       v.Branch,
		"commit":       v.Commit,
		"status":       v.Status,
		"event":        v.Event,
		"durationMs":   v.DurationMs,
		"runId":        v.RunID,
		"errorSummary": v.ErrorSummary,
	}
}

// TemplateService 定义通知模板 CRUD + 渲染(冻结契约,供 HTTP 层与 NotifyHook 消费)。
type TemplateService interface {
	// ListTemplates 返回所有模板(按 event 再按 createdAt 升序;稳定排序)。
	ListTemplates(ctx context.Context) ([]Template, error)
	// CreateTemplate 校验事件枚举 + 渠道存在性(若指定)后持久化一条模板。
	// 事件非法 → ErrInvalidEvent;渠道指定但不存在 → ErrTemplateChannelNotFound。
	CreateTemplate(ctx context.Context, in CreateTemplateInput) (*Template, error)
	// UpdateTemplate 修改模板;不存在 → ErrTemplateNotFound。
	UpdateTemplate(ctx context.Context, id string, in UpdateTemplateInput) (*Template, error)
	// DeleteTemplate 删除一条模板。不存在 → ErrTemplateNotFound。
	DeleteTemplate(ctx context.Context, id string) error
	// RenderPayload 渲染一次事件的 Payload(冻结契约,供 5-2 NotifyHook 消费)。
	//
	// 查最具体匹配模板(channelID 精确 > 该事件通用 > 无)→ 纯文本替换占位 → Payload。
	// **无匹配 → 平台默认**(= EventPayload,5-2 行为不变)。占位渲染为纯文本替换
	// (未知占位 → 空串),**绝不执行用户模板**(无 RCE)。
	RenderPayload(ctx context.Context, event, channelID string, vars TemplateVars) Payload
}

// ListTemplates 实现 TemplateService.ListTemplates。
func (s *service) ListTemplates(ctx context.Context) ([]Template, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, event, channel_id, title_template, body_template, created_at
		 FROM notification_templates ORDER BY event, created_at, id`,
	)
	if err != nil {
		return nil, fmt.Errorf("notify: list templates: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]Template, 0)
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("notify: iterate templates: %w", err)
	}
	return out, nil
}

// CreateTemplate 实现 TemplateService.CreateTemplate。
func (s *service) CreateTemplate(ctx context.Context, in CreateTemplateInput) (*Template, error) {
	event := strings.TrimSpace(in.Event)
	if !validEvent(event) {
		return nil, ErrInvalidEvent
	}
	channelID := strings.TrimSpace(in.ChannelID)
	if channelID != "" {
		// 渠道存在性校验:不存在 → 人读错误(避免悬挂模板)。
		if _, _, err := s.load(ctx, channelID); err != nil {
			if errors.Is(err, ErrNotFound) {
				return nil, ErrTemplateChannelNotFound
			}
			return nil, err
		}
	}

	id := uuid.NewString()
	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339)
	var projectID any
	if strings.TrimSpace(in.ProjectID) != "" {
		projectID = strings.TrimSpace(in.ProjectID)
	}
	var chanID any
	if channelID != "" {
		chanID = channelID
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO notification_templates (id, project_id, event, channel_id, title_template, body_template, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, projectID, event, chanID, in.TitleTemplate, in.BodyTemplate, nowStr,
	)
	if err != nil {
		return nil, fmt.Errorf("notify: insert template: %w", err)
	}

	return &Template{
		ID:            id,
		Event:         event,
		ChannelID:     channelID,
		TitleTemplate: in.TitleTemplate,
		BodyTemplate:  in.BodyTemplate,
		CreatedAt:     now,
	}, nil
}

// UpdateTemplate 实现 TemplateService.UpdateTemplate。
func (s *service) UpdateTemplate(ctx context.Context, id string, in UpdateTemplateInput) (*Template, error) {
	cur, err := s.loadTemplate(ctx, id)
	if err != nil {
		return nil, err
	}

	event := cur.Event
	if in.Event != nil {
		e := strings.TrimSpace(*in.Event)
		if !validEvent(e) {
			return nil, ErrInvalidEvent
		}
		event = e
	}
	channelID := cur.ChannelID
	if in.ChannelID != nil {
		c := strings.TrimSpace(*in.ChannelID)
		if c != "" {
			if _, _, lerr := s.load(ctx, c); lerr != nil {
				if errors.Is(lerr, ErrNotFound) {
					return nil, ErrTemplateChannelNotFound
				}
				return nil, lerr
			}
		}
		channelID = c
	}
	title := cur.TitleTemplate
	if in.TitleTemplate != nil {
		title = *in.TitleTemplate
	}
	body := cur.BodyTemplate
	if in.BodyTemplate != nil {
		body = *in.BodyTemplate
	}

	var chanID any
	if channelID != "" {
		chanID = channelID
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE notification_templates SET event = ?, channel_id = ?, title_template = ?, body_template = ?
		 WHERE id = ?`,
		event, chanID, title, body, id,
	)
	if err != nil {
		return nil, fmt.Errorf("notify: update template: %w", err)
	}

	return &Template{
		ID:            id,
		ProjectID:     cur.ProjectID,
		Event:         event,
		ChannelID:     channelID,
		TitleTemplate: title,
		BodyTemplate:  body,
		CreatedAt:     cur.CreatedAt,
	}, nil
}

// DeleteTemplate 实现 TemplateService.DeleteTemplate。
func (s *service) DeleteTemplate(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM notification_templates WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("notify: delete template: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrTemplateNotFound
	}
	return nil
}

// RenderPayload 实现 TemplateService.RenderPayload(冻结契约)。
//
// 查最具体匹配模板(channelID 精确 > 该事件通用 > 无)→ 纯文本替换 → Payload。
// 无匹配 → 平台默认(EventPayload,5-2 行为不变)。绝不执行用户模板(无 RCE)。
func (s *service) RenderPayload(ctx context.Context, event, channelID string, vars TemplateVars) Payload {
	tpl := s.matchTemplate(ctx, event, channelID)
	if tpl == nil {
		// 无匹配模板:回平台默认(行为向后兼容 5-2)。
		return s.defaultPayload(ctx, event, vars)
	}

	m := vars.asMap()
	title := renderTemplate(tpl.TitleTemplate, m)
	body := renderTemplate(tpl.BodyTemplate, m)

	// 标题为空时回退到默认标题(避免空主题);保持 Fields 一致供 webhook/email 透出。
	if strings.TrimSpace(title) == "" {
		title = s.defaultPayload(ctx, event, vars).Title
	}
	// 自定义模板正文是用户文本(不翻译),但 Lang 仍要带上,供飞书/邮件本地化字段标签与标题。
	return Payload{Title: title, Body: body, Fields: varsFields(vars), Lang: s.notifyLanguage(ctx)}
}

// defaultPayload 用 TemplateVars 还原 EventPayload 入参,产出平台默认 Payload(按通知语言本地化)。
func (s *service) defaultPayload(ctx context.Context, event string, vars TemplateVars) Payload {
	var durationMs int64
	if vars.DurationMs != "" {
		if d, err := strconv.ParseInt(vars.DurationMs, 10, 64); err == nil {
			durationMs = d
		}
	}
	return EventPayload(s.notifyLanguage(ctx), event, vars.Project, vars.Branch, vars.Commit, vars.Status, durationMs)
}

// varsFields 把 TemplateVars 展开为 Payload.Fields(仅非空项),供 webhook/email 透出结构化键值。
func varsFields(vars TemplateVars) map[string]string {
	f := map[string]string{}
	for k, v := range vars.asMap() {
		if v != "" {
			f[k] = v
		}
	}
	return f
}

// matchTemplate 查最具体匹配模板:同事件下,channelID 精确匹配优先于通用(ChannelID 空)。
// 同优先级多条取最早创建(稳定)。无匹配 → nil。
func (s *service) matchTemplate(ctx context.Context, event, channelID string) *Template {
	if !validEvent(event) {
		return nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, event, channel_id, title_template, body_template, created_at
		 FROM notification_templates WHERE event = ? AND (channel_id IS NULL OR channel_id = '' OR channel_id = ?)
		 ORDER BY created_at, id`, event, channelID,
	)
	if err != nil {
		// best-effort:查询失败按无模板处理(回默认),不阻断通知。
		return nil
	}
	defer func() { _ = rows.Close() }()

	var exact, generic *Template
	for rows.Next() {
		t, serr := scanTemplate(rows)
		if serr != nil {
			return nil
		}
		if t.ChannelID == channelID && channelID != "" {
			if exact == nil {
				exact = t
			}
		} else if t.ChannelID == "" {
			if generic == nil {
				generic = t
			}
		}
	}
	if exact != nil {
		return exact
	}
	return generic
}

// placeholderRE 匹配 {{name}} 占位(name 为字母数字与下划线;两侧可有空格)。
var placeholderRE = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_]+)\s*\}\}`)

// RenderText 用 TemplateVars 对一段含 {{占位}} 的文本做纯文本替换(导出供 build 包的
// notify 节点内联模板渲染复用;占位名同 TemplateVars.asMap,未知占位 → 空串)。
// 与 RenderPayload 共用同一套占位语义,**绝不执行用户模板**(无 RCE)。
func RenderText(tpl string, vars TemplateVars) string {
	return renderTemplate(tpl, vars.asMap())
}

// renderTemplate 纯文本替换占位:{{name}} → vars[name];未知占位 → 空串。
// **不执行任何用户模板**(无 text/template、无函数、无 RCE)——仅正则替换字符串。
func renderTemplate(tpl string, vars map[string]string) string {
	if tpl == "" {
		return ""
	}
	return placeholderRE.ReplaceAllStringFunc(tpl, func(match string) string {
		sub := placeholderRE.FindStringSubmatch(match)
		if len(sub) < 2 {
			return ""
		}
		// 已知占位 → 值;未知占位 → 空串(不报错、不保留原文)。
		return vars[sub[1]]
	})
}

// loadTemplate 读取单条模板行。不存在 → ErrTemplateNotFound。
func (s *service) loadTemplate(ctx context.Context, id string) (*Template, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, project_id, event, channel_id, title_template, body_template, created_at
		 FROM notification_templates WHERE id = ?`, id,
	)
	t, err := scanTemplate(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrTemplateNotFound
		}
		return nil, err
	}
	return t, nil
}

// scanTemplate 把一行扫描为 Template 视图。
func scanTemplate(sc scanner) (*Template, error) {
	var (
		t          Template
		projectID  sql.NullString
		channelID  sql.NullString
		createdStr string
	)
	if err := sc.Scan(&t.ID, &projectID, &t.Event, &channelID, &t.TitleTemplate, &t.BodyTemplate, &createdStr); err != nil {
		return nil, fmt.Errorf("notify: scan template: %w", err)
	}
	if projectID.Valid {
		t.ProjectID = projectID.String
	}
	if channelID.Valid {
		t.ChannelID = channelID.String
	}
	created, err := time.Parse(time.RFC3339, createdStr)
	if err != nil {
		return nil, fmt.Errorf("notify: parse template created_at: %w", err)
	}
	t.CreatedAt = created
	return &t, nil
}

// ---- errorSummary 尽力脱敏(无明文 secret) ----

// 常见敏感串形状(尽力脱敏):key=value 形式的 token/password/secret、Bearer、Basic、
// AWS/GitHub 风格 token、PEM 私钥头。非穷尽,但确保通知正文不直出明文密钥。
var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(pass(word)?|secret|token|api[_-]?key|access[_-]?key|private[_-]?key|pwd)\b\s*[:=]\s*\S+`),
	// code-review P9:复合标识符里的 token/secret/auth(如 `_authToken=`、`registry_token=`、
	// `X-Auth-Token:`)——`\btoken\b` 因前置词字符无边界而漏掉(如 npm `:_authToken=...`),此条兜住。
	regexp.MustCompile(`(?i)[A-Za-z0-9_.\-]*(token|secret|passwd|password|apikey|auth)[A-Za-z0-9_.\-]*\s*[:=]\s*\S+`),
	regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._\-]+`),
	regexp.MustCompile(`(?i)\bBasic\s+[A-Za-z0-9+/=]+`),
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`\bAKIA[0-9A-Z]{12,}`),
	regexp.MustCompile(`(?i)-----BEGIN[^-]*PRIVATE KEY-----`),
}

// maxErrorSummaryLen 限制错误摘要长度(避免超长正文 / 信息溢出)。
const maxErrorSummaryLen = 512

// MaskErrorSummary 尽力脱敏失败摘要:命中已知敏感形状替换为 [MASKED],并截断到上限。
// 这是「尽力」而非穷尽脱敏;调用方应优先传入已经过 mask.Masker 处理的文本。
func MaskErrorSummary(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	for _, re := range secretPatterns {
		s = re.ReplaceAllString(s, "[MASKED]")
	}
	if len(s) > maxErrorSummaryLen {
		s = s[:maxErrorSummaryLen] + "…"
	}
	return s
}

// ShortCommit 截短 commit 至前 8 位(空则原样);供 NotifyHook 构 TemplateVars 用。
func ShortCommit(commit string) string {
	return shortCommit(commit)
}

// SummarizeFailure 把失败日志原文压缩为脱敏后的简短摘要(取前若干非空行 + 尽力脱敏 + 截断)。
// 供 NotifyHook 构 TemplateVars.ErrorSummary 用;**绝无明文 secret**。
func SummarizeFailure(raw string) string {
	return firstLineMasked(raw)
}

// firstLineMasked 取首段非空内容并脱敏(失败日志常多行,摘要取首要信息即可)。
func firstLineMasked(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	// 取前若干行拼为摘要(脱敏 + 截断)。
	lines := strings.Split(s, "\n")
	keep := make([]string, 0, len(lines))
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		keep = append(keep, ln)
		if len(keep) >= 3 {
			break
		}
	}
	return MaskErrorSummary(strings.Join(keep, " | "))
}
