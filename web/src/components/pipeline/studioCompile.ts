/**
 * studioCompile — 自定义节点工作室的「模型 ↔ templated 节点 config」纯函数转换层。
 *
 * 工作室把三样东西拼成一个可复用、可配置的自定义节点:
 *   ① 步骤组合(复用 stepCompile / StepBuilder,产出 commands + artifactPath)
 *   ② 提升参数(promoted params:key/label/type/default,步骤里以 {{key}} 引用)
 *   ③ 节点表面(名称/说明,由调用方持有)
 *
 * 编译产出落在**已有的 `templated` 节点类型**上 —— 后端零改(`internal/build/dag_stage_exec.go`
 * 的 templateContext/renderTemplate 已会读 `params` 当渲染上下文 + 渲染 `commandTemplate`/`artifactPath`
 * 里的 {{key}})。故工作室 = 纯前端编译,延续 Tier 3「StepBuilder 编译进 config 零后端改动」哲学。
 *
 * 关键:`commandTemplate` 与步骤构建器的 `commands` 是同一段脚本文本,只是 templated 节点
 * 存在 `commandTemplate` 键下并对其中 {{param}} 做渲染。故工作室在边界处把 commands ↔ commandTemplate
 * 互译,StepBuilder 始终以 `commands` 形态工作({{param}} 对它只是不透明文本)。
 *
 * `__studio` 是工作室私有结构(JSON 串),仅存提升参数的**额外元信息**(label/type/options)——
 * key 与 default 以 `params` 文本为唯一真源,避免双源漂移。后端忽略此键;删了它节点照常跑,
 * 只是回库重开时降级为「按 params 文本反解析(type 全 text)」。
 */

import { type StepBlock } from './stepCompile'

export type PromotedParamType = 'text' | 'select' | 'number' | 'toggle'

export interface PromotedParam {
  /** 模板占位名:步骤里 {{key}} 引用、实例编辑时可覆盖。 */
  key: string
  /** 实例编辑时的显示标签。 */
  label: string
  type: PromotedParamType
  /** 默认值(落入 params 文本,既是默认也是渲染上下文)。 */
  default: string
  /** select 类型的候选项。 */
  options?: string[]
}

/** 工作室模型:步骤部分以 StepBuilder 的 config 形状(commands + artifactPath)承载。 */
export interface StudioModel {
  image: string
  params: PromotedParam[]
  stepConfig: { commands: string; artifactPath: string }
}

/** config 键名:工作室私有元信息(后端忽略,仅前端无损回开用)。 */
export const STUDIO_META_KEY = '__studio'

const PARAM_TYPES: readonly PromotedParamType[] = ['text', 'select', 'number', 'toggle']

function configString(config: Record<string, unknown>, key: string): string {
  const v = config[key]
  if (typeof v === 'string') return v
  return v == null ? '' : String(v)
}

/**
 * 提升参数 → `params` 配置字段(每行 `key=default`)。
 * 后端 templateContext 读它:既作渲染上下文(`{{key}}` → default),也是实例未覆盖时的默认值。
 */
export function paramsToText(params: readonly PromotedParam[]): string {
  return params
    .map((p) => ({ k: p.key.trim(), v: p.default ?? '' }))
    .filter((x) => x.k !== '')
    .map((x) => `${x.k}=${x.v}`)
    .join('\n')
}

/** `params` 文本 → 基础提升参数(仅 key+default,type 兜底 text)。无 __studio 时的回退。 */
export function parseParamsText(text: string): PromotedParam[] {
  const out: PromotedParam[] = []
  for (const line of (text ?? '').replace(/\r/g, '').split('\n')) {
    const t = line.trim()
    if (t === '' || t.startsWith('#')) continue
    const i = t.indexOf('=')
    const key = (i >= 0 ? t.slice(0, i) : t).trim()
    if (key === '') continue
    out.push({ key, label: key, type: 'text', default: i >= 0 ? t.slice(i + 1) : '' })
  }
  return out
}

/** 把提升参数的额外元信息(label/type/options)编码进 __studio(不存 default,以 params 文本为准)。 */
export function encodeStudioMeta(params: readonly PromotedParam[]): string {
  const meta = params
    .filter((p) => p.key.trim() !== '')
    .map((p) => {
      const m: { key: string; label: string; type: PromotedParamType; options?: string[] } = {
        key: p.key.trim(),
        label: p.label,
        type: p.type,
      }
      if (p.type === 'select' && p.options && p.options.length > 0) m.options = p.options
      return m
    })
  return JSON.stringify({ v: 1, params: meta })
}

interface DecodedMeta {
  key: string
  label?: string
  type?: PromotedParamType
  options?: string[]
}

/** 解码 __studio;非法/缺失 → null(调用方回退 parseParamsText,绝不报错丢数据)。 */
function decodeStudioMeta(raw: unknown): DecodedMeta[] | null {
  if (typeof raw !== 'string' || raw.trim() === '') return null
  let obj: unknown
  try {
    obj = JSON.parse(raw)
  } catch {
    return null
  }
  const params = (obj as { params?: unknown })?.params
  if (!Array.isArray(params)) return null
  const out: DecodedMeta[] = []
  for (const p of params) {
    if (!p || typeof p !== 'object') continue
    const key = (p as { key?: unknown }).key
    if (typeof key !== 'string' || key.trim() === '') continue
    const type = (p as { type?: unknown }).type
    const label = (p as { label?: unknown }).label
    const options = (p as { options?: unknown }).options
    out.push({
      key: key.trim(),
      label: typeof label === 'string' ? label : undefined,
      type: PARAM_TYPES.includes(type as PromotedParamType) ? (type as PromotedParamType) : undefined,
      options: Array.isArray(options) ? options.filter((o): o is string => typeof o === 'string') : undefined,
    })
  }
  return out
}

/** 合并 params 文本(key+default 真源)与 __studio 元信息(label/type/options)→ 提升参数列表。 */
export function parsePromotedParams(config: Record<string, unknown>): PromotedParam[] {
  const base = parseParamsText(configString(config, 'params'))
  const meta = decodeStudioMeta(config[STUDIO_META_KEY])
  if (!meta) return base
  const byKey = new Map(meta.map((m) => [m.key, m]))
  return base.map((p) => {
    const m = byKey.get(p.key)
    if (!m) return p
    const merged: PromotedParam = {
      ...p,
      label: m.label ?? p.label,
      type: m.type ?? p.type,
    }
    if (m.options) merged.options = m.options
    return merged
  })
}

/** 反解析 templated 节点 config → 工作室模型(commandTemplate 优先,兼容旧 commands)。 */
export function parseStudioConfig(config: Record<string, unknown>): StudioModel {
  const commands = configString(config, 'commandTemplate') || configString(config, 'commands')
  return {
    image: configString(config, 'image'),
    params: parsePromotedParams(config),
    stepConfig: { commands, artifactPath: configString(config, 'artifactPath') },
  }
}

/** 工作室模型 → templated 节点 config(后端原样跑;空值不入 config;__studio 仅供回开)。 */
export function compileStudioConfig(model: StudioModel): Record<string, string> {
  const config: Record<string, string> = {}
  const image = model.image.trim()
  if (image) config.image = model.image
  if (model.stepConfig.commands.trim()) config.commandTemplate = model.stepConfig.commands
  if (model.stepConfig.artifactPath.trim()) config.artifactPath = model.stepConfig.artifactPath
  const params = paramsToText(model.params)
  if (params) config.params = params
  if (model.params.some((p) => p.key.trim() !== '')) {
    config[STUDIO_META_KEY] = encodeStudioMeta(model.params)
  }
  return config
}

/**
 * 判断一个自定义节点是否「工作室可编辑」:带 __studio 元信息,或本就是 templated 类型
 * (它的 config 形状=工作室产出)。其余类型仍走原始 KV 编辑器。
 */
export function isStudioNode(nodeType: string, config: Record<string, unknown>): boolean {
  if (typeof config[STUDIO_META_KEY] === 'string') return true
  return nodeType.trim() === 'templated'
}

/** 新建工作室节点的初始模型(一个空命令步骤起手 + 无提升参数)。 */
export function emptyStudioModel(): StudioModel {
  return { image: '', params: [], stepConfig: { commands: '', artifactPath: '' } }
}

// 供测试/调用方引用步骤类型(避免重复 import 路径)。
export type { StepBlock }
