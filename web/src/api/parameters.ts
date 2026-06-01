/**
 * Parameters API — 项目级「类型化运行参数定义」(P0 typed params).
 *
 * GET /api/projects/{id}/parameters → { parameters: ParamDef[] }
 * PUT /api/projects/{id}/parameters → 同上 (needs CSRF)
 *
 * 手动触发弹窗据此渲染类型化控件(枚举→下拉、布尔→开关、数字→数字框)并校验;
 * 无定义则回退自由 KV(向后兼容)。执行期仍以 key=value 注入容器(同 Story 8-11)。
 * secret 绝不作明文参数 —— 仍走 vault 引用。
 */

import { http } from './http'

export type ParamType = 'string' | 'choice' | 'boolean' | 'number'

export interface ParamDef {
  key: string
  label: string
  type: ParamType
  default: string
  options?: string[]
  required: boolean
}

interface ParametersResponse {
  parameters: ParamDef[]
}

export async function getParameters(projectId: string): Promise<ParamDef[]> {
  const res = await http.get<ParametersResponse>(`/api/projects/${projectId}/parameters`)
  return res.parameters ?? []
}

export async function saveParameters(projectId: string, parameters: ParamDef[]): Promise<ParamDef[]> {
  const res = await http.put<ParametersResponse>(`/api/projects/${projectId}/parameters`, { parameters })
  return res.parameters ?? []
}

export const PARAM_TYPE_OPTIONS: ReadonlyArray<{ value: ParamType; label: string }> = [
  { value: 'string', label: '文本' },
  { value: 'choice', label: '枚举' },
  { value: 'boolean', label: '布尔' },
  { value: 'number', label: '数字' },
]

/**
 * 客户端校验「触发时填的值」是否满足定义(与后端 ResolveParams 同规则),返回首个错误信息或 ''。
 * 用于触发弹窗提交前的即时反馈;后端仍会再校验一次(422)。
 */
export function validateParamValues(defs: readonly ParamDef[], values: Record<string, string>): string {
  for (const d of defs) {
    const raw = values[d.key]
    const v = (raw ?? '').trim() !== '' ? raw : d.default
    if ((v ?? '').trim() === '') {
      if (d.required) return `参数「${d.label}」为必填`
      continue
    }
    if (d.type === 'number' && Number.isNaN(Number(v))) return `参数「${d.label}」须为数字`
    if (d.type === 'boolean' && v !== 'true' && v !== 'false') return `参数「${d.label}」须为 true/false`
    if (d.type === 'choice' && !(d.options ?? []).includes(v)) return `参数「${d.label}」不在可选项中`
  }
  return ''
}
