package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/repocache"
	"github.com/huangchengsir/pipewright/internal/vault"
)

// refs.go 暴露「列项目仓库分支/tag」端点(代码管理区 · Story 8-18 / FR-8-18):
//
//	GET /api/projects/{id}/refs  → { branches:[{name,commit}], tags:[{name,commit}] }
//
// 数据取自中控机本地仓库镜像(repocache,先增量 fetch 再读本地,不每次全量触网),供前端触发流水线时
// 把「分支 / commit」从手敲升级为下拉选择。镜像不可用 → 503(优雅,前端回退手填)。

// RefsLister 抽象「列仓库分支/tag + 某 ref 的最近 commit」能力(*repocache.Cache 即满足)。
type RefsLister interface {
	ListRefs(ctx context.Context, repoURL, token string) (*repocache.Refs, error)
	ListCommits(ctx context.Context, repoURL, token, ref string, limit int) ([]repocache.Commit, error)
}

type refDTO struct {
	Name   string `json:"name"`
	Commit string `json:"commit"`
}

type refsResponse struct {
	Branches []refDTO `json:"branches"`
	Tags     []refDTO `json:"tags"`
}

// makeListRefsHandler 返回 GET /api/projects/{id}/refs handler(认证;只读)。
// 取项目仓库地址 + 凭据 → repocache.ListRefs(镜像增量更新后读)→ DTO。
// 项目不存在 → 404;代码管理区未启用 → 503;拉取/读取失败 → 502(人读,无凭据明文)。
func makeListRefsHandler(projects project.Service, v vault.Vault, lister RefsLister) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if lister == nil || projects == nil {
			writeError(w, http.StatusServiceUnavailable, "repocache_disabled", "代码管理区未启用,无法列分支")
			return
		}
		id := chi.URLParam(r, "id")
		proj, err := projects.Get(r.Context(), id)
		if err != nil {
			if errors.Is(err, project.ErrNotFound) {
				writeError(w, http.StatusNotFound, "project_not_found", "项目不存在")
				return
			}
			writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
			return
		}

		// 取仓库凭据(进程内即用即弃);取不到不致命 → 空 token(公开仓库可成,私有走拉取失败)。
		token := ""
		if v != nil && strings.TrimSpace(proj.CredentialID) != "" {
			if t, terr := v.Get(proj.CredentialID); terr == nil {
				token = t
			}
		}

		refs, lerr := lister.ListRefs(r.Context(), proj.RepoURL, token)
		token = "" //nolint:ineffassign // 尽早清明文引用
		_ = token
		if lerr != nil {
			// 错误体不含凭据明文(repocache 错误为干净领域错误)。
			writeError(w, http.StatusBadGateway, "refs_unavailable", "无法读取仓库分支(仓库不可达或凭据无效)")
			return
		}

		out := refsResponse{Branches: make([]refDTO, 0, len(refs.Branches)), Tags: make([]refDTO, 0, len(refs.Tags))}
		for _, b := range refs.Branches {
			out.Branches = append(out.Branches, refDTO{Name: b.Name, Commit: b.Commit})
		}
		for _, tg := range refs.Tags {
			out.Tags = append(out.Tags, refDTO{Name: tg.Name, Commit: tg.Commit})
		}
		writeJSON(w, http.StatusOK, out)
	}
}

type commitDTO struct {
	Sha     string `json:"sha"`
	Short   string `json:"short"`
	Subject string `json:"subject"`
	Author  string `json:"author"`
	When    string `json:"when"` // RFC3339
}

// makeListCommitsHandler 返回 GET /api/projects/{id}/commits?ref=&limit= handler(认证;只读)。
// 列某 ref(分支/tag/commit;空=默认分支)的最近提交,供前端选 commit 下拉。
func makeListCommitsHandler(projects project.Service, v vault.Vault, lister RefsLister) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if lister == nil || projects == nil {
			writeError(w, http.StatusServiceUnavailable, "repocache_disabled", "代码管理区未启用,无法列提交")
			return
		}
		id := chi.URLParam(r, "id")
		proj, err := projects.Get(r.Context(), id)
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
			ref = proj.DefaultBranch
		}
		limit := 30
		if n := strings.TrimSpace(r.URL.Query().Get("limit")); n != "" {
			if v, perr := strconv.Atoi(n); perr == nil {
				limit = v
			}
		}

		token := ""
		if v != nil && strings.TrimSpace(proj.CredentialID) != "" {
			if t, terr := v.Get(proj.CredentialID); terr == nil {
				token = t
			}
		}
		commits, lerr := lister.ListCommits(r.Context(), proj.RepoURL, token, ref, limit)
		token = ""
		_ = token
		if lerr != nil {
			writeError(w, http.StatusBadGateway, "commits_unavailable", "无法读取仓库提交(仓库不可达或凭据无效)")
			return
		}
		out := make([]commitDTO, 0, len(commits))
		for _, co := range commits {
			out = append(out, commitDTO{
				Sha: co.SHA, Short: co.Short, Subject: co.Subject, Author: co.Author,
				When: co.When.UTC().Format(time.RFC3339),
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"commits": out})
	}
}
