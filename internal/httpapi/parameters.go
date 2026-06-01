package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/run"
)

// parameters.go 暴露项目级「类型化运行参数定义」端点(P0 typed params)。
//
// GET /api/projects/{id}/parameters → { parameters: ParamDef[] }
// PUT /api/projects/{id}/parameters → 同上(需 CSRF)。校验在 run.ParameterService。
//
// 手动触发弹窗据此渲染类型化控件并校验;无定义则前端回退自由 KV。执行期仍以 key=value 注入。

type paramDefDTO struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Type     string   `json:"type"`
	Default  string   `json:"default"`
	Options  []string `json:"options,omitempty"`
	Required bool     `json:"required"`
}

type parametersDTO struct {
	Parameters []paramDefDTO `json:"parameters"`
}

func toParametersDTO(c *run.ParametersConfig) parametersDTO {
	defs := make([]paramDefDTO, 0, len(c.Defs))
	for _, d := range c.Defs {
		defs = append(defs, paramDefDTO{
			Key: d.Key, Label: d.Label, Type: d.Type, Default: d.Default,
			Options: d.Options, Required: d.Required,
		})
	}
	return parametersDTO{Parameters: defs}
}

func writeParametersError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, run.ErrParamProjectNotFound):
		writeError(w, http.StatusNotFound, "project_not_found", "项目不存在")
	case errors.Is(err, run.ErrInvalidParamDef):
		// 校验错误体已含人读原因(键/类型/枚举/默认值)。
		writeError(w, http.StatusUnprocessableEntity, "invalid_parameter", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
	}
}

func makeGetParametersHandler(svc run.ParameterService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "参数配置服务未初始化")
			return
		}
		cfg, err := svc.Get(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeParametersError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toParametersDTO(cfg))
	}
}

func makeSaveParametersHandler(svc run.ParameterService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "参数配置服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		var req struct {
			Parameters []run.ParamDef `json:"parameters"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}
		cfg, err := svc.Save(r.Context(), id, req.Parameters)
		if err != nil {
			writeParametersError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toParametersDTO(cfg))
	}
}
