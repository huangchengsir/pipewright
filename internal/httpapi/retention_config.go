package httpapi

import (
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"net/http"

	"github.com/huangchengsir/pipewright/internal/retention"
)

// retentionConfigDTO 是运行数据保留策略(全局)。
//   - enabled:总开关(关时不清理)。
//   - keepPerProject:每项目保留最近 N 条终态运行(0=不限)。
//   - maxAgeDays:删除创建早于 N 天的终态运行(0=不限)。
type retentionConfigDTO struct {
	Enabled        bool `json:"enabled"`
	KeepPerProject int  `json:"keepPerProject"`
	MaxAgeDays     int  `json:"maxAgeDays"`
}

// GET /api/retention/config — 读取保留策略。
func makeGetRetentionConfigHandler(svc *retention.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "保留服务未初始化")
			return
		}
		cfg, err := svc.ReadConfiguration(r.Context())
		if err != nil {
			writeRetentionConfigError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, retentionConfigDTO{
			Enabled:        cfg.Enabled,
			KeepPerProject: cfg.KeepPerProject,
			MaxAgeDays:     cfg.MaxAgeDays,
		})
	}
}

// PUT /api/retention/config — 设置保留策略(负值归一为 0)。
func makeSetRetentionConfigHandler(svc *retention.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "保留服务未初始化")
			return
		}
		var in retentionConfigDTO
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}
		cfg := retention.Config{Enabled: in.Enabled, KeepPerProject: in.KeepPerProject, MaxAgeDays: in.MaxAgeDays}
		if err := svc.SetConfig(r.Context(), cfg); err != nil {
			writeRetentionConfigError(w, err)
			return
		}
		// 回读归一后的值返回(负值已被归零)。
		saved, err := svc.ReadConfiguration(r.Context())
		if err != nil {
			writeRetentionConfigError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, retentionConfigDTO{
			Enabled:        saved.Enabled,
			KeepPerProject: saved.KeepPerProject,
			MaxAgeDays:     saved.MaxAgeDays,
		})
	}
}

func writeRetentionConfigError(w http.ResponseWriter, err error) {
	if errors.Is(err, tenancy.ErrForbidden) {
		writeError(w, http.StatusForbidden, "forbidden", "无权执行此操作")
		return
	}
	if errors.Is(err, tenancy.ErrConflict) {
		writeError(w, http.StatusConflict, "conflict", "权限状态已变化")
		return
	}
	writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
}
