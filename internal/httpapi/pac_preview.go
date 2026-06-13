package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/pipelineyaml"
	"github.com/huangchengsir/pipewright/internal/project"
)

// pacPreviewFile 是「流水线即代码」预览/校验的固定目标文件名(与 pacloader.DefaultFile 同语义;
// 此处内联常量避免 httpapi 反向 import pacloader)。
const pacPreviewFile = ".pipewright.yml"

// ---- PAC 预览/校验契约 DTO(冻结;camelCase) ----
//
// {found, valid, ref, file, error, stageCount, stages:[{name,kind,jobCount}]}
// 绝不回显仓库 URL 凭据 / 任何 secret;解析失败 found=true valid=false error=<message> stages=[]。

type pacStageSummaryDTO struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	JobCount int    `json:"jobCount"`
}

type pacPreviewDTO struct {
	Found      bool                 `json:"found"`
	Valid      bool                 `json:"valid"`
	Ref        string               `json:"ref"`
	File       string               `json:"file"`
	Error      string               `json:"error"`
	StageCount int                  `json:"stageCount"`
	Stages     []pacStageSummaryDTO `json:"stages"`
}

// makePacPreviewHandler 返回 GET /api/projects/{id}/pac/preview?ref=<ref> handler(认证;只读)。
//
// 在「依赖」仓库 .pipewright.yml 驱动运行之前,让用户主动按 ref 拉取并校验它——看清运行时会用到的
// 配置摘要,并把 YAML 错误显式呈现(运行时是静默回退,无反馈)。镜像 pacloader 的拉取语义:
//   - ref 省略 → 项目默认分支;
//   - token 经保险库尽力解(取不到则空 token 试公开仓库,与 pacloader 一致);
//   - 文件不存在 → 200 found=false;克隆降级 → 200 found=false(无法判定);
//   - 解析/校验失败 → 200 found=true valid=false error=<message> stages=[];
//   - 成功 → found=true valid=true + 阶段摘要(name/kind/jobCount)。
//
// 绝不把仓库 URL 凭据 / 任何 secret 写入响应或 error。项目不存在 → 404;所需服务未初始化 → 503。
func makePacPreviewHandler(d sourceDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.projects == nil || d.reader == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "源码读取所需服务未初始化")
			return
		}

		id := chi.URLParam(r, "id")
		proj, err := d.projects.Get(r.Context(), id)
		if err != nil {
			if errors.Is(err, project.ErrNotFound) {
				writeError(w, http.StatusNotFound, "project_not_found", "项目不存在")
				return
			}
			writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
			return
		}

		ref := strings.TrimSpace(r.URL.Query().Get("ref"))
		if ref == "" {
			ref = strings.TrimSpace(proj.DefaultBranch)
		}

		out := pacPreviewDTO{Ref: ref, File: pacPreviewFile, Stages: []pacStageSummaryDTO{}}

		if strings.TrimSpace(proj.RepoURL) == "" {
			// 未配置仓库:无从拉取,按「文件不存在」呈现(found=false),不报错。
			writeJSON(w, http.StatusOK, out)
			return
		}

		// 取仓库凭据(进程内取用即弃;取不到不致命 → 空 token 试公开仓库,与 pacloader 一致)。
		token := ""
		if d.vault != nil && strings.TrimSpace(proj.CredentialID) != "" {
			if t, terr := d.vault.Get(proj.CredentialID); terr == nil {
				token = t
			}
		}

		blob, berr := d.reader.Blob(r.Context(), proj.RepoURL, token, ref, pacPreviewFile)
		if berr != nil {
			// 文件不存在 / 克隆失败 → found=false(运行时此路径会回退库内配置)。
			// 绝不外泄底层错误(可能含 URL 凭据);valid=false、error 留空(非 YAML 问题)。
			writeJSON(w, http.StatusOK, out)
			return
		}
		if blob.Degraded {
			// 克隆降级:无法判定文件是否存在 → found=false(不误报)。
			writeJSON(w, http.StatusOK, out)
			return
		}
		if blob.Binary || strings.TrimSpace(blob.Content) == "" {
			// 二进制 / 空文件:视作未提供有效配置(found=false)。
			writeJSON(w, http.StatusOK, out)
			return
		}

		// 文件存在。
		out.Found = true

		cfg, perr := pipelineyaml.Parse([]byte(blob.Content))
		if perr != nil {
			// YAML 非法 → found=true valid=false + 解析错误消息(pipelineyaml/pipeline 的错误不含密钥)。
			out.Valid = false
			out.Error = perr.Error()
			writeJSON(w, http.StatusOK, out)
			return
		}

		out.Valid = true
		stages := make([]pacStageSummaryDTO, 0, len(cfg.Spec.Stages))
		for _, st := range cfg.Spec.Stages {
			stages = append(stages, pacStageSummaryDTO{
				Name:     st.Name,
				Kind:     st.Kind,
				JobCount: len(st.Jobs),
			})
		}
		out.Stages = stages
		out.StageCount = len(stages)
		writeJSON(w, http.StatusOK, out)
	}
}
