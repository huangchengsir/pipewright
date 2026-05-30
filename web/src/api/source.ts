/**
 * Source API — Story 7-4: 只读代码浏览(FR-4)。
 *
 * 消费 Story 3-6 冻结端点(本 story 不改后端):
 *   GET /api/projects/{id}/source/tree?ref=&path=  → SourceTree
 *   GET /api/projects/{id}/source/blob?ref=&path=  → SourceBlob
 *
 * 形状定死(camelCase,与后端 sourceTreeDTO / sourceBlobDTO 一一对应):
 *   tree {ref,path,entries:[{name,path,type,size?}],degraded?,degradedReason?}
 *   blob {ref,path,size,binary,truncated,content}
 *
 * 纯只读:无写端点;路径穿越/SSRF 由后端已拦,前端绝不构造绕过路径。
 * degraded:克隆失败时后端返 200 + 空 entries(tree.degraded=true);blob 克隆失败返
 * 200 + content 空。前端据此显「源码暂不可读」,不白屏、不 500。
 */

import { http } from './http'

/** 树节点类型:目录或文件。 */
export type SourceEntryType = 'dir' | 'file'

export interface SourceEntry {
  name: string
  /** ref 内相对路径(后端规范化;前端原样回传,不拼接构造)。 */
  path: string
  type: SourceEntryType
  /** 文件大小(字节);目录可能缺省。 */
  size?: number
}

export interface SourceTree {
  ref: string
  path: string
  entries: SourceEntry[]
  /** true ⇒ 克隆失败/源码暂不可读(entries 空)。 */
  degraded?: boolean
  /** degraded 人读原因(可选)。 */
  degradedReason?: string
}

export interface SourceBlob {
  ref: string
  path: string
  size: number
  /** true ⇒ 二进制文件,content 空,不可预览。 */
  binary: boolean
  /** true ⇒ 超过后端单文件上限被截断,content 为前缀。 */
  truncated: boolean
  /** 文本内容(binary 时为空)。 */
  content: string
}

export interface SourceQuery {
  /** ref(分支/tag/sha);空 ⇒ 项目默认分支(后端回填规范化值)。 */
  ref?: string
  /** 目录或文件路径;空 ⇒ 仓库根。 */
  path?: string
}

function buildQuery(q: SourceQuery): string {
  const params = new URLSearchParams()
  if (q.ref) params.set('ref', q.ref)
  if (q.path) params.set('path', q.path)
  const s = params.toString()
  return s ? `?${s}` : ''
}

/**
 * 列 ref 下某目录的直接子项(后端 name 升序、dir 在前)。
 * 克隆失败时返回 degraded=true 的空树(不抛),供前端友好降级。
 */
export async function getSourceTree(
  projectId: string,
  query: SourceQuery = {},
): Promise<SourceTree> {
  return http.get<SourceTree>(
    `/api/projects/${encodeURIComponent(projectId)}/source/tree${buildQuery(query)}`,
  )
}

/**
 * 读 ref 下某文件内容(binary 检测 + 后端截断)。
 * path 必填(后端对空 path 返 400)。克隆失败时返回 content 空的 blob。
 */
export async function getSourceBlob(
  projectId: string,
  query: SourceQuery & { path: string },
): Promise<SourceBlob> {
  return http.get<SourceBlob>(
    `/api/projects/${encodeURIComponent(projectId)}/source/blob${buildQuery(query)}`,
  )
}
