/**
 * Version API — 构建版本元数据 + 一键检查更新。
 *
 * GET /version            → VersionInfo(公开,构建期注入的版本/commit/日期)
 * GET /api/version/check  → UpdateInfo(鉴权,查 GitHub 最新发布并与当前版本比对)
 *
 * 检查更新永不抛业务错:后端在 CheckError 字段里带失败原因(网络/限流),始终附当前版本,
 * 由 UI 优雅降级渲染。
 */

import { http } from './http'

export interface VersionInfo {
  version: string
  commit: string
  date: string
  goVersion: string
  platform: string
}

export interface UpdateInfo {
  current: string
  latest: string
  updateAvailable: boolean
  releaseUrl: string
  publishedAt: string
  notes: string
  /** 非空 ⇒ 本次检查失败(网络/限流);此时 updateAvailable 恒 false。 */
  checkError?: string
}

/** 一键自动更新结果。binary 模式成功后进程会自重启;docker 模式返回升级命令。 */
export interface UpdateResult {
  mode: 'binary' | 'docker' | ''
  status: 'restarting' | 'manual' | 'uptodate' | 'error'
  from: string
  to: string
  message: string
  command?: string
}

export function getVersion(): Promise<VersionInfo> {
  return http.get<VersionInfo>('/version')
}

export function checkUpdate(): Promise<UpdateInfo> {
  return http.get<UpdateInfo>('/api/version/check')
}

/** 触发一键自动更新(写操作,带 CSRF)。 */
export function applyUpdate(): Promise<UpdateResult> {
  return http.post<UpdateResult>('/api/version/update')
}
