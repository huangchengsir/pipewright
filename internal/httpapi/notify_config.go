package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/huangchengsir/pipewright/internal/notify"
)

// notifyConfigDTO 是通知全局配置(本期仅 language:外发通知默认文案语言)。
type notifyConfigDTO struct {
	Language string `json:"language"`
}

// GET /api/notifications/config — 读取通知语言。
func makeGetNotifyConfigHandler(svc notify.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "通知服务未初始化")
			return
		}
		writeJSON(w, http.StatusOK, notifyConfigDTO{Language: svc.NotifyLanguage(r.Context())})
	}
}

// PUT /api/notifications/config — 设置通知语言(受支持 locale 码)。
func makeSetNotifyConfigHandler(svc notify.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "通知服务未初始化")
			return
		}
		var in notifyConfigDTO
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}
		if err := svc.SetNotifyLanguage(r.Context(), in.Language); err != nil {
			if errors.Is(err, notify.ErrInvalidLanguage) {
				writeError(w, http.StatusUnprocessableEntity, "invalid_language", "通知语言非法:须为受支持的语言代码")
				return
			}
			writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
			return
		}
		writeJSON(w, http.StatusOK, notifyConfigDTO{Language: in.Language})
	}
}
