package httpapi

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/ai"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/vault"
)

// ---- 冻结 run-diff 契约(Story 7.3;FR-25;camelCase) -------------------------
//
// 形状定死:available/reason/baselineRunId/baselineCommit/currentCommit/
// files[{path,status,additions,deletions}]/truncated/summary。
// available=false(无 baseline / 无 commit / 克隆失败)时 files 空、reason 人读。
// 7-2 诊断可消费此 diff 作上下文,7-4 代码浏览可复用,均不改形状。

type runDiffFileDTO struct {
	Path      string `json:"path"`
	Status    string `json:"status"` // added | modified | deleted | renamed
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

// runDiffDTO 是 GET /api/runs/{id}/diff 响应体(冻结)。
// available=false 时:files=[]、truncated=false、baseline/current commit 尽力回填(可空),reason 人读。
type runDiffDTO struct {
	Available      bool             `json:"available"`
	Reason         string           `json:"reason"`
	BaselineRunID  string           `json:"baselineRunId"`
	BaselineCommit string           `json:"baselineCommit"`
	CurrentCommit  string           `json:"currentCommit"`
	Files          []runDiffFileDTO `json:"files"`
	Truncated      bool             `json:"truncated"`
	Summary        string           `json:"summary"`
}

// runDiffDeps 聚合 diff 端点所需服务(复用已注入 runs + projects + vault + 注入的 RunDiffer)。
type runDiffDeps struct {
	runs     run.Service
	projects project.Service
	vault    vault.Vault
	differ   ai.RunDiffer
}

// toRunDiffDTO 把领域 ai.RunDiff 映射为冻结 DTO(注入 baselineRunId/commits 上下文)。
func toRunDiffDTO(d ai.RunDiff, baselineRunID, baselineCommit, currentCommit string) runDiffDTO {
	files := make([]runDiffFileDTO, 0, len(d.Files))
	for _, f := range d.Files {
		files = append(files, runDiffFileDTO{
			Path:      f.Path,
			Status:    f.Status,
			Additions: f.Additions,
			Deletions: f.Deletions,
		})
	}
	return runDiffDTO{
		Available:      d.Available,
		Reason:         d.Reason,
		BaselineRunID:  baselineRunID,
		BaselineCommit: baselineCommit,
		CurrentCommit:  currentCommit,
		Files:          files,
		Truncated:      d.Truncated,
		Summary:        d.Summary,
	}
}

// degradedDiffDTO 构造一条 available:false 的降级响应(files 空;reason 人读)。
func degradedDiffDTO(reason, baselineRunID, baselineCommit, currentCommit string) runDiffDTO {
	return runDiffDTO{
		Available:      false,
		Reason:         reason,
		BaselineRunID:  baselineRunID,
		BaselineCommit: baselineCommit,
		CurrentCommit:  currentCommit,
		Files:          []runDiffFileDTO{},
		Truncated:      false,
		Summary:        "",
	}
}

// makeRunDiffHandler 返回 GET /api/runs/{id}/diff handler(认证;只读)。
//
// 取本次 run(current commit)→ 取项目 repoURL/凭据 → RunDiffer.DiffCommit 算「该 commit
// 自身」文件级 diff(git show 语义:相对首个父提交;根提交=全新增)→ DTO(baselineCommit=父提交)。
// 不依赖 baseline 运行,故成功/失败运行都恒可展示「本次提交改了什么」。
//
// 优雅降级(绝不 500):本次 run 无 commit / 克隆失败 / commit 不可达 → 200 available:false +
// reason 人读。run 不存在 → 404 run_not_found。
func makeRunDiffHandler(d runDiffDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.runs == nil || d.projects == nil || d.differ == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "差异对比所需服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		ctx := r.Context()

		rn, err := d.runs.Get(ctx, id)
		if err != nil {
			writeRunError(w, err) // 不存在 → 404
			return
		}

		currentCommit := strings.TrimSpace(rn.Trigger.Commit)
		if currentCommit == "" {
			// 本次运行无 commit:无可对比,优雅降级(绝不 500)。
			writeJSON(w, http.StatusOK,
				degradedDiffDTO("本次运行无提交信息,无可对比的代码差异", "", "", ""))
			return
		}

		// 取项目 repoURL + 凭据(进程内取用即弃;取不到凭据不致命 → 空 token,私有仓库走克隆失败降级)。
		proj, perr := d.projects.Get(ctx, rn.ProjectID)
		if perr != nil {
			// 项目缺失/读失败:降级(绝不 500;diff 本属诊断辅助信号)。
			writeJSON(w, http.StatusOK,
				degradedDiffDTO("无法读取项目仓库信息,暂时无法计算差异", "", "", currentCommit))
			return
		}
		token := ""
		if d.vault != nil && strings.TrimSpace(proj.CredentialID) != "" {
			if t, terr := d.vault.Get(proj.CredentialID); terr == nil {
				token = t
			}
		}

		// 「本次提交自身」diff(git show 语义:相对首个父提交;根提交=全新增)。不依赖任何 baseline
		// 运行,故成功/失败运行都恒可展示「这次改了什么」。parentCommit 回填 baselineCommit 作上下文。
		diff, parentCommit := d.differ.DiffCommit(ctx, proj.RepoURL, token, currentCommit)
		writeJSON(w, http.StatusOK, toRunDiffDTO(diff, "", parentCommit, currentCommit))
	}
}
