package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/library"
)

// custom_nodes.go 暴露自定义节点端点(复用库 Tier 2 · 对标 Jenkins 自定义步骤 / 云效自建任务模板)。
//
//	GET    /api/custom-nodes       → 列自定义节点(选择器/复用库画廊)
//	POST   /api/custom-nodes       → 建自定义节点(name + nodeType + config 快照)
//	GET    /api/custom-nodes/{id}  → 取单自定义节点(含完整 config)
//	PUT    /api/custom-nodes/{id}  → 覆盖自定义节点
//	DELETE /api/custom-nodes/{id}  → 删自定义节点
//
// config 为 Job.Config 自由 KV 快照,不含明文 secret(与流水线 spec 同形状,secret 走保险库引用)。
// 写操作过 auth + CSRF + 审计(detail 仅元数据:名称 + 类型,绝无 config 明文)。

// customNodeDTO 是自定义节点对外 DTO(冻结契约;camelCase;含完整 config 供回填)。
type customNodeDTO struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	NodeType    string         `json:"nodeType"`
	Summary     string         `json:"summary"`
	Config      map[string]any `json:"config"`
	CreatedAt   string         `json:"createdAt"`
	UpdatedAt   string         `json:"updatedAt"`
}

func toCustomNodeDTO(n *library.CustomNode) customNodeDTO {
	cfg := n.Config
	if cfg == nil {
		cfg = map[string]any{}
	}
	return customNodeDTO{
		ID:          n.ID,
		Name:        n.Name,
		Description: n.Description,
		NodeType:    n.NodeType,
		Summary:     n.Summary,
		Config:      cfg,
		CreatedAt:   n.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:   n.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// writeCustomNodeError 把自定义节点领域错误映射为契约错误码/状态码。
func writeCustomNodeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, library.ErrCustomNodeNotFound):
		writeError(w, http.StatusNotFound, "custom_node_not_found", "自定义节点不存在")
	case errors.Is(err, library.ErrCustomNodeNameTaken):
		writeError(w, http.StatusConflict, "custom_node_name_taken", "自定义节点名已被占用")
	case errors.Is(err, library.ErrInvalidCustomNode):
		writeError(w, http.StatusUnprocessableEntity, "invalid_custom_node", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
	}
}

// customNodeReqBody 是建/改自定义节点的请求体。Config 为自由 KV(不含明文 secret)。
type customNodeReqBody struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	NodeType    string         `json:"nodeType"`
	Summary     string         `json:"summary"`
	Config      map[string]any `json:"config"`
}

func (b customNodeReqBody) toInput() library.CustomNodeInput {
	return library.CustomNodeInput{
		Name:        b.Name,
		Description: b.Description,
		NodeType:    b.NodeType,
		Summary:     b.Summary,
		Config:      b.Config,
	}
}

func makeListCustomNodesHandler(svc library.CustomNodeService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "自定义节点服务未初始化")
			return
		}
		list, err := svc.List(r.Context())
		if err != nil {
			writeCustomNodeError(w, err)
			return
		}
		out := make([]customNodeDTO, 0, len(list))
		for i := range list {
			out = append(out, toCustomNodeDTO(&list[i]))
		}
		writeJSON(w, http.StatusOK, map[string]any{"customNodes": out})
	}
}

func makeGetCustomNodeHandler(svc library.CustomNodeService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "自定义节点服务未初始化")
			return
		}
		n, err := svc.Get(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeCustomNodeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toCustomNodeDTO(n))
	}
}

func makeCreateCustomNodeHandler(svc library.CustomNodeService, aud audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "自定义节点服务未初始化")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<18) // 256KB
		var req customNodeReqBody
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}
		n, err := svc.Create(r.Context(), req.toInput())
		if err != nil {
			writeCustomNodeError(w, err)
			return
		}
		recordAudit(r.Context(), aud, audit.Entry{
			Actor:      auditActor,
			Action:     audit.ActionCustomNodeCreate,
			TargetType: audit.TargetCustomNode,
			TargetID:   n.ID,
			Detail:     map[string]any{"name": n.Name, "nodeType": n.NodeType},
			IP:         clientIP(r),
		})
		writeJSON(w, http.StatusCreated, toCustomNodeDTO(n))
	}
}

func makeUpdateCustomNodeHandler(svc library.CustomNodeService, aud audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "自定义节点服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		r.Body = http.MaxBytesReader(w, r.Body, 1<<18)
		var req customNodeReqBody
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}
		n, err := svc.Update(r.Context(), id, req.toInput())
		if err != nil {
			writeCustomNodeError(w, err)
			return
		}
		recordAudit(r.Context(), aud, audit.Entry{
			Actor:      auditActor,
			Action:     audit.ActionCustomNodeUpdate,
			TargetType: audit.TargetCustomNode,
			TargetID:   n.ID,
			Detail:     map[string]any{"name": n.Name, "nodeType": n.NodeType},
			IP:         clientIP(r),
		})
		writeJSON(w, http.StatusOK, toCustomNodeDTO(n))
	}
}

func makeDeleteCustomNodeHandler(svc library.CustomNodeService, aud audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "自定义节点服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		if err := svc.Delete(r.Context(), id); err != nil {
			writeCustomNodeError(w, err)
			return
		}
		recordAudit(r.Context(), aud, audit.Entry{
			Actor:      auditActor,
			Action:     audit.ActionCustomNodeDelete,
			TargetType: audit.TargetCustomNode,
			TargetID:   id,
			IP:         clientIP(r),
		})
		w.WriteHeader(http.StatusNoContent)
	}
}
