/**
 * Remote build runner API — 远程构建 runner(FR-8-14 续).
 *
 * GET /api/projects/{id}/runner → { runnerServerId }
 * PUT /api/projects/{id}/runner → 同上(需 CSRF;空串 = 清,回本地构建)。
 *
 * 配了 runner 服务器 → 该项目构建下沉到该远程机执行;空 = 本地构建(默认)。
 */
import { http } from './http'

export interface RunnerConfig {
  runnerServerId: string
}

export async function getRunner(projectId: string): Promise<RunnerConfig> {
  return http.get<RunnerConfig>(`/api/projects/${projectId}/runner`)
}

export async function saveRunner(projectId: string, runnerServerId: string): Promise<RunnerConfig> {
  return http.put<RunnerConfig>(`/api/projects/${projectId}/runner`, { runnerServerId })
}
