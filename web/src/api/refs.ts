/**
 * Repo refs API — 代码管理区(Story 8-18 / FR-8-18).
 *
 * GET /api/projects/{id}/refs → { branches:[{name,commit}], tags:[{name,commit}] }
 * 数据取自中控机本地仓库镜像(增量 fetch 后读),供触发流水线时把分支/commit 从手敲升级为下拉。
 * 代码管理区未启用 → 503;此时调用方应优雅回退到手填(不报错给用户)。
 */
import { http } from './http'

export interface GitRef {
  name: string
  commit: string
}

export interface RepoRefs {
  branches: GitRef[]
  tags: GitRef[]
}

export async function listRefs(projectId: string): Promise<RepoRefs> {
  return http.get<RepoRefs>(`/api/projects/${projectId}/refs`)
}
