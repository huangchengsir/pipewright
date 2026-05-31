package httpapi

import (
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/artifactstore"
	"github.com/huangchengsir/pipewright/internal/run"
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

// makeArtifactDownloadHandler 返回 GET /api/runs/{id}/artifacts/{artifactId}/download。
// 仅对「已归档真字节」(metadata.stored=true,reference=制品库句柄)的产物可下载;占位产物 →
// 404(无真字节可取)。store 为 nil → 503。镜像类(reference=repo:tag)不在制品库 → 404。
// 文件名:dist(tar.gz)→ <name>.tar.gz;file 格式 → metadata.filename 或 <name>。
func makeArtifactDownloadHandler(svc run.Service, store *artifactstore.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "运行服务未初始化")
			return
		}
		if store == nil {
			writeError(w, http.StatusServiceUnavailable, "artifact_store_unavailable", "制品库未启用,无法下载")
			return
		}
		id := chi.URLParam(r, "id")
		artID := chi.URLParam(r, "artifactId")
		if _, err := svc.Get(r.Context(), id); err != nil {
			writeRunError(w, err)
			return
		}
		arts, err := svc.ListArtifacts(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
			return
		}
		var art *run.Artifact
		for i := range arts {
			if arts[i].ID == artID {
				art = &arts[i]
				break
			}
		}
		if art == nil {
			writeError(w, http.StatusNotFound, "artifact_not_found", "产物不存在")
			return
		}
		stored, _ := art.Metadata["stored"].(bool)
		if !stored || strings.TrimSpace(art.Reference) == "" {
			writeError(w, http.StatusNotFound, "artifact_not_downloadable", "该产物未归档真字节(占位/镜像类),无法下载")
			return
		}
		rc, oerr := store.Open(art.Reference)
		if oerr != nil {
			writeError(w, http.StatusNotFound, "artifact_bytes_missing", "制品库中找不到该产物字节")
			return
		}
		defer func() { _ = rc.Close() }()

		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Disposition", "attachment; filename=\""+downloadFilename(art)+"\"")
		if art.SizeBytes > 0 {
			w.Header().Set("Content-Length", itoa(art.SizeBytes))
		}
		_, _ = io.Copy(w, rc)
	}
}

// downloadFilename 据产物类型/元数据给一个安全的下载文件名。
func downloadFilename(a *run.Artifact) string {
	if fn, ok := a.Metadata["filename"].(string); ok && strings.TrimSpace(fn) != "" {
		return sanitizeFilename(fn)
	}
	base := sanitizeFilename(a.Name)
	if base == "" {
		base = "artifact"
	}
	if format, _ := a.Metadata["format"].(string); format == "tar.gz" {
		return base + ".tar.gz"
	}
	return base
}

// sanitizeFilename 去掉路径分隔/控制字符,防 header 注入与目录穿越(仅留文件名本体)。
func sanitizeFilename(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\\", "_")
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "\"", "_")
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", "")
	return s
}

// itoa 把 int64 转十进制字符串(避免引 strconv 仅为此;Content-Length 用)。
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
