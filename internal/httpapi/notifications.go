package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/notify"
)

// ---- 通知渠道 DTO(冻结契约;camelCase;敏感字段绝无明文,仅 hasPassword:bool) ----

// channelConfigDTO 是 per-type 非敏感配置(webhook={url};email={smtpHost,...,hasPassword})。
// 敏感字段(SMTP 密码)绝不在响应:仅以 hasPassword 标识是否已配。
type channelConfigDTO struct {
	// webhook
	URL string `json:"url,omitempty"`
	// email
	SMTPHost    string `json:"smtpHost,omitempty"`
	SMTPPort    int    `json:"smtpPort,omitempty"`
	From        string `json:"from,omitempty"`
	To          string `json:"to,omitempty"`
	Username    string `json:"username,omitempty"`
	HasPassword bool   `json:"hasPassword,omitempty"`
}

// channelDTO 是渠道对外响应体(冻结契约)。
type channelDTO struct {
	ID        string           `json:"id"`
	Name      string           `json:"name"`
	Type      string           `json:"type"`
	Enabled   bool             `json:"enabled"`
	Config    channelConfigDTO `json:"config"`
	CreatedAt string           `json:"createdAt"`
	UpdatedAt string           `json:"updatedAt"`
}

// channelTestResultDTO 是 POST test 响应体(冻结契约:ok/latencyMs/detail/error)。
type channelTestResultDTO struct {
	OK        bool    `json:"ok"`
	LatencyMs int64   `json:"latencyMs"`
	Detail    string  `json:"detail"`
	Error     *string `json:"error"`
}

// toChannelDTO 把领域 Channel 转契约 DTO(敏感字段仅 hasPassword,绝无明文)。
func toChannelDTO(c *notify.Channel) channelDTO {
	cfg := channelConfigDTO{}
	switch c.Type {
	case notify.TypeWebhook:
		cfg.URL = c.Config.URL
	case notify.TypeEmail:
		cfg.SMTPHost = c.Config.SMTPHost
		cfg.SMTPPort = c.Config.SMTPPort
		cfg.From = c.Config.From
		cfg.To = c.Config.To
		cfg.Username = c.Config.Username
		cfg.HasPassword = c.HasSecret
	}
	return channelDTO{
		ID:        c.ID,
		Name:      c.Name,
		Type:      c.Type,
		Enabled:   c.Enabled,
		Config:    cfg,
		CreatedAt: c.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt: c.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// writeNotifyError 把领域错误映射为契约错误码/状态码;绝不回显敏感字段明文/密文。
func writeNotifyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, notify.ErrRouteNotFound):
		writeError(w, http.StatusNotFound, "route_not_found", "通知路由不存在")
	case errors.Is(err, notify.ErrInvalidEvent):
		writeError(w, http.StatusUnprocessableEntity, "invalid_event", "事件类型非法:只能为 build_succeeded、build_failed、deploy_succeeded、deploy_failed、rollback 或 health_check_failed")
	case errors.Is(err, notify.ErrRouteChannelNotFound):
		writeError(w, http.StatusUnprocessableEntity, "route_channel_not_found", "路由引用的通知渠道不存在")
	case errors.Is(err, notify.ErrTemplateNotFound):
		writeError(w, http.StatusNotFound, "template_not_found", "通知模板不存在")
	case errors.Is(err, notify.ErrTemplateChannelNotFound):
		writeError(w, http.StatusUnprocessableEntity, "template_channel_not_found", "模板引用的通知渠道不存在")
	case errors.Is(err, notify.ErrNotFound):
		writeError(w, http.StatusNotFound, "channel_not_found", "通知渠道不存在")
	case errors.Is(err, notify.ErrInvalidType):
		writeError(w, http.StatusUnprocessableEntity, "invalid_type", "渠道类型只能为 webhook、email、wecom、dingtalk 或 feishu")
	case errors.Is(err, notify.ErrEmptyName):
		writeError(w, http.StatusUnprocessableEntity, "invalid_channel", "渠道名称不能为空")
	case errors.Is(err, notify.ErrInvalidConfig):
		writeError(w, http.StatusUnprocessableEntity, "invalid_config", "渠道配置不完整或非法")
	case errors.Is(err, notify.ErrSSRFBlocked):
		writeError(w, http.StatusUnprocessableEntity, "url_not_allowed", "webhook 地址不被允许:不可指向云元数据或链路本地地址")
	case errors.Is(err, notify.ErrVaultUnconfigured):
		writeError(w, http.StatusServiceUnavailable, "vault_unconfigured", "保险库未配置 master key,无法加密或读取敏感字段")
	default:
		writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
	}
}

// channelRequest 是创建/更新渠道的请求体(camelCase)。
// 更新时各字段经指针区分「省略(保留)」与「显式赋值」;password 写入后仅 hasPassword 回显。
type channelRequest struct {
	Name    *string `json:"name"`
	Type    *string `json:"type"`
	Enabled *bool   `json:"enabled"`
	Config  *struct {
		URL      *string `json:"url"`
		SMTPHost *string `json:"smtpHost"`
		SMTPPort *int    `json:"smtpPort"`
		From     *string `json:"from"`
		To       *string `json:"to"`
		Username *string `json:"username"`
		// Password 为只写:省略/nil=保留既有,空串=清除,非空=轮换。绝不回显。
		Password *string `json:"password"`
	} `json:"config"`
}

// makeListChannelsHandler 返回 GET /api/notifications/channels handler。
// 响应 { items: [...] }(冻结契约)。
func makeListChannelsHandler(svc notify.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "通知服务未初始化")
			return
		}
		chans, err := svc.List(r.Context())
		if err != nil {
			writeNotifyError(w, err)
			return
		}
		items := make([]channelDTO, 0, len(chans))
		for i := range chans {
			items = append(items, toChannelDTO(&chans[i]))
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

// makeGetChannelHandler 返回 GET /api/notifications/channels/{id} handler。
func makeGetChannelHandler(svc notify.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "通知服务未初始化")
			return
		}
		ch, err := svc.Get(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeNotifyError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toChannelDTO(ch))
	}
}

// makeCreateChannelHandler 返回 POST /api/notifications/channels handler。
// 敏感字段(email password)写入后仅 hasPassword 回显,绝不在响应回明文。校验失败 → 422 定位。
func makeCreateChannelHandler(svc notify.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "通知服务未初始化")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		var req channelRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}

		in := notify.CreateInput{}
		if req.Name != nil {
			in.Name = *req.Name
		}
		if req.Type != nil {
			in.Type = *req.Type
		}
		if req.Enabled != nil {
			in.Enabled = *req.Enabled
		} else {
			in.Enabled = true // 默认启用
		}
		in.Config, in.Secret = configFromRequest(req)

		ch, err := svc.Create(r.Context(), in)
		if err != nil {
			writeNotifyError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, toChannelDTO(ch))
	}
}

// makeUpdateChannelHandler 返回 PUT /api/notifications/channels/{id} handler。
// password 只写:省略/nil 保留既有,空串清除,非空轮换。绝不在响应回明文。
func makeUpdateChannelHandler(svc notify.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "通知服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		var req channelRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}

		in := notify.UpdateInput{
			Name:    req.Name,
			Enabled: req.Enabled,
		}
		if req.Config != nil {
			cfg, secret := configFromRequest(req)
			in.Config = &cfg
			// password 指针区分省略/赋值:仅当显式给出(nil 表示省略=保留)。
			if req.Config.Password != nil {
				in.Secret = &secret
			}
		}

		ch, err := svc.Update(r.Context(), id, in)
		if err != nil {
			writeNotifyError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toChannelDTO(ch))
	}
}

// makeDeleteChannelHandler 返回 DELETE /api/notifications/channels/{id} handler。
func makeDeleteChannelHandler(svc notify.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "通知服务未初始化")
			return
		}
		if err := svc.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
			writeNotifyError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// makeTestChannelHandler 返回 POST /api/notifications/channels/{id}/test handler。
// 向已保存渠道发一条测试通知;200 回 {ok,latencyMs,detail,error};error 人读(绝无敏感字段)。
func makeTestChannelHandler(svc notify.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "通知服务未初始化")
			return
		}
		res, err := svc.Test(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeNotifyError(w, err)
			return
		}
		dto := channelTestResultDTO{OK: res.OK, LatencyMs: res.LatencyMs, Detail: res.Detail}
		if res.Error != "" {
			dto.Error = &res.Error
		}
		writeJSON(w, http.StatusOK, dto)
	}
}

// configFromRequest 从请求体抽取 per-type 非敏感配置 + 敏感字段(password)明文。
func configFromRequest(req channelRequest) (notify.Config, string) {
	cfg := notify.Config{}
	secret := ""
	if req.Config == nil {
		return cfg, secret
	}
	c := req.Config
	if c.URL != nil {
		cfg.URL = *c.URL
	}
	if c.SMTPHost != nil {
		cfg.SMTPHost = *c.SMTPHost
	}
	if c.SMTPPort != nil {
		cfg.SMTPPort = *c.SMTPPort
	}
	if c.From != nil {
		cfg.From = *c.From
	}
	if c.To != nil {
		cfg.To = *c.To
	}
	if c.Username != nil {
		cfg.Username = *c.Username
	}
	if c.Password != nil {
		secret = *c.Password
	}
	return cfg, secret
}

// ---- 通知事件路由 DTO + handler(Story 5.2;FR-20;camelCase) ----

// routeDTO 是事件路由对外响应体(冻结契约)。projectId 本期恒空(全局默认),5-4 填项目维度。
type routeDTO struct {
	ID        string `json:"id"`
	ProjectID string `json:"projectId,omitempty"`
	Event     string `json:"event"`
	ChannelID string `json:"channelId"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"createdAt"`
}

// toRouteDTO 把领域 Route 转契约 DTO。
func toRouteDTO(r *notify.Route) routeDTO {
	return routeDTO{
		ID:        r.ID,
		ProjectID: r.ProjectID,
		Event:     r.Event,
		ChannelID: r.ChannelID,
		Enabled:   r.Enabled,
		CreatedAt: r.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// routeRequest 是创建路由的请求体(camelCase)。enabled 省略默认启用。
// projectId 可选:非空 = 项目级覆盖路由(Story 5.4);空/省略 = 全局默认。
type routeRequest struct {
	ProjectID *string `json:"projectId"`
	Event     *string `json:"event"`
	ChannelID *string `json:"channelId"`
	Enabled   *bool   `json:"enabled"`
}

// makeListRoutesHandler 返回 GET /api/notifications/routes handler。响应 { items: [...] }。
// 可选 query ?projectId=<id>:非空 → 仅该项目级路由(Story 5.4);省略/空 → 全局路由。
func makeListRoutesHandler(svc notify.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "通知服务未初始化")
			return
		}
		projectID := strings.TrimSpace(r.URL.Query().Get("projectId"))
		routes, err := svc.ListRoutesForProject(r.Context(), projectID)
		if err != nil {
			writeNotifyError(w, err)
			return
		}
		items := make([]routeDTO, 0, len(routes))
		for i := range routes {
			items = append(items, toRouteDTO(&routes[i]))
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

// makeCreateRouteHandler 返回 POST /api/notifications/routes handler(认证 + CSRF)。
// 事件枚举非法 → 422 invalid_event;渠道不存在 → 422 route_channel_not_found。
func makeCreateRouteHandler(svc notify.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "通知服务未初始化")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		var req routeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}
		in := notify.CreateRouteInput{Enabled: true} // 默认启用
		if req.ProjectID != nil {
			in.ProjectID = *req.ProjectID // 非空 = 项目级覆盖(Story 5.4);空 = 全局
		}
		if req.Event != nil {
			in.Event = *req.Event
		}
		if req.ChannelID != nil {
			in.ChannelID = *req.ChannelID
		}
		if req.Enabled != nil {
			in.Enabled = *req.Enabled
		}
		route, err := svc.CreateRoute(r.Context(), in)
		if err != nil {
			writeNotifyError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, toRouteDTO(route))
	}
}

// makeDeleteRouteHandler 返回 DELETE /api/notifications/routes/{id} handler(认证 + CSRF)。
func makeDeleteRouteHandler(svc notify.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "通知服务未初始化")
			return
		}
		if err := svc.DeleteRoute(r.Context(), chi.URLParam(r, "id")); err != nil {
			writeNotifyError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// ---- 通知模板 DTO + handler(Story 5.3;FR-21;camelCase) ----

// templateDTO 是通知模板对外响应体(冻结契约)。channelId 空 = 该事件通用;projectId 本期恒空。
type templateDTO struct {
	ID            string `json:"id"`
	ProjectID     string `json:"projectId,omitempty"`
	Event         string `json:"event"`
	ChannelID     string `json:"channelId,omitempty"`
	TitleTemplate string `json:"titleTemplate"`
	BodyTemplate  string `json:"bodyTemplate"`
	CreatedAt     string `json:"createdAt"`
}

// toTemplateDTO 把领域 Template 转契约 DTO。
func toTemplateDTO(t *notify.Template) templateDTO {
	return templateDTO{
		ID:            t.ID,
		ProjectID:     t.ProjectID,
		Event:         t.Event,
		ChannelID:     t.ChannelID,
		TitleTemplate: t.TitleTemplate,
		BodyTemplate:  t.BodyTemplate,
		CreatedAt:     t.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// templateRequest 是创建/更新模板的请求体(camelCase)。
// 更新时各字段经指针区分「省略(保留)」与「显式赋值」。channelId 空串 = 改为该事件通用。
type templateRequest struct {
	Event         *string `json:"event"`
	ChannelID     *string `json:"channelId"`
	TitleTemplate *string `json:"titleTemplate"`
	BodyTemplate  *string `json:"bodyTemplate"`
}

// makeListTemplatesHandler 返回 GET /api/notifications/templates handler。响应 { items: [...] }。
func makeListTemplatesHandler(svc notify.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "通知服务未初始化")
			return
		}
		tpls, err := svc.ListTemplates(r.Context())
		if err != nil {
			writeNotifyError(w, err)
			return
		}
		items := make([]templateDTO, 0, len(tpls))
		for i := range tpls {
			items = append(items, toTemplateDTO(&tpls[i]))
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

// makeCreateTemplateHandler 返回 POST /api/notifications/templates handler(认证 + CSRF)。
// 事件枚举非法 → 422 invalid_event;渠道指定但不存在 → 422 template_channel_not_found。
func makeCreateTemplateHandler(svc notify.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "通知服务未初始化")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		var req templateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}
		in := notify.CreateTemplateInput{}
		if req.Event != nil {
			in.Event = *req.Event
		}
		if req.ChannelID != nil {
			in.ChannelID = *req.ChannelID
		}
		if req.TitleTemplate != nil {
			in.TitleTemplate = *req.TitleTemplate
		}
		if req.BodyTemplate != nil {
			in.BodyTemplate = *req.BodyTemplate
		}
		tpl, err := svc.CreateTemplate(r.Context(), in)
		if err != nil {
			writeNotifyError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, toTemplateDTO(tpl))
	}
}

// makeUpdateTemplateHandler 返回 PUT /api/notifications/templates/{id} handler(认证 + CSRF)。
func makeUpdateTemplateHandler(svc notify.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "通知服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		var req templateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}
		in := notify.UpdateTemplateInput{
			Event:         req.Event,
			ChannelID:     req.ChannelID,
			TitleTemplate: req.TitleTemplate,
			BodyTemplate:  req.BodyTemplate,
		}
		tpl, err := svc.UpdateTemplate(r.Context(), id, in)
		if err != nil {
			writeNotifyError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toTemplateDTO(tpl))
	}
}

// makeDeleteTemplateHandler 返回 DELETE /api/notifications/templates/{id} handler(认证 + CSRF)。
func makeDeleteTemplateHandler(svc notify.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "通知服务未初始化")
			return
		}
		if err := svc.DeleteTemplate(r.Context(), chi.URLParam(r, "id")); err != nil {
			writeNotifyError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
