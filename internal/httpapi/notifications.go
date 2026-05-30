package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/huangjiawei/devopstool/internal/notify"
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
