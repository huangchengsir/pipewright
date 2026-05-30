package httpapi

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/huangjiawei/devopstool/internal/run"
)

// ---- 冻结 artifacts 契约(Story 3.4 / FR-6;camelCase) ----------------------
//
// 产物契约是 Epic 3 一等交付物:type 枚举(image|jar|dist|archive)+ reference 类型寻址
// 定死。Epic 4 部署按 (type, reference) 消费,无需了解构建内部。3-3 真实构建经同一
// EmitArtifact 接口喂产物、不改此形状。

// artifactDTO 是单条构建产物(run-detail artifacts slot 元素 + artifacts 端点元素共用形状)。
type artifactDTO struct {
	ID        string         `json:"id"`
	Type      string         `json:"type"`      // image | jar | dist | archive(冻结枚举)
	Name      string         `json:"name"`      // 产物逻辑名
	Reference string         `json:"reference"` // 类型寻址引用(Epic 4 据此消费)
	SizeBytes int64          `json:"sizeBytes"` // 字节数(未知 = 0)
	Metadata  map[string]any `json:"metadata"`  // 自由 KV(digest/path/stub 等)
	CreatedAt string         `json:"createdAt"` // RFC3339
}

// runArtifactsDTO 是 GET /api/runs/{id}/artifacts 响应体(冻结)。
type runArtifactsDTO struct {
	Artifacts []artifactDTO `json:"artifacts"`
}

func toArtifactDTO(a run.Artifact) artifactDTO {
	meta := a.Metadata
	if meta == nil {
		meta = map[string]any{}
	}
	return artifactDTO{
		ID:        a.ID,
		Type:      a.Type,
		Name:      a.Name,
		Reference: a.Reference,
		SizeBytes: a.SizeBytes,
		Metadata:  meta,
		CreatedAt: a.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// toArtifactDTOs 把领域产物切片映射为 DTO 切片(始终非 nil;空 → [] 以保契约形状)。
func toArtifactDTOs(arts []run.Artifact) []artifactDTO {
	out := make([]artifactDTO, 0, len(arts))
	for i := range arts {
		out = append(out, toArtifactDTO(arts[i]))
	}
	return out
}

// makeRunArtifactsHandler 返回 GET /api/runs/{id}/artifacts handler(认证;只读)。
// → { "artifacts": [ …同 run-detail slot… ] };run 不存在 → 404 run_not_found。
// 产物契约(FR-6)端点:Epic 4 部署按 (type, reference) 消费。
func makeRunArtifactsHandler(svc run.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "运行服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")

		// run 存在性:不存在 → 404(ListArtifacts 对不存在 run 返回空,不报错;仿 logs 端点)。
		if _, err := svc.Get(r.Context(), id); err != nil {
			writeRunError(w, err)
			return
		}

		arts, err := svc.ListArtifacts(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
			return
		}
		writeJSON(w, http.StatusOK, runArtifactsDTO{Artifacts: toArtifactDTOs(arts)})
	}
}
