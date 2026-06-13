/**
 * studioCompile — 自定义节点工作室的「模型 ↔ templated 节点 config」纯函数转换层。
 *
 * 工作室把四样东西拼成一个可复用、可配置的自定义节点:
 *   ① 步骤组合(结构化积木 StudioStep[],编译成 commandTemplate + artifactPath + 质量门禁键)
 *   ② 提升参数(promoted params:key/label/type/default,步骤里以 {{key}} 引用)
 *   ③ 运行镜像(image,可含 {{param}})
 *   ④ 节点表面(icon/category/summary,复用者第一眼看到的卡片)
 *
 * 编译产出落在**已有的 `templated` 节点类型**上 —— 后端零改(`internal/build/dag_stage_exec.go`
 * 的 templateContext/renderTemplate 已会读 `params` 当渲染上下文 + 渲染 `commandTemplate`/`artifactPath`
 * 里的 {{key}})。故工作室 = 纯前端编译,延续 Tier 3「编译进 config 零后端改动」哲学。
 *
 * `__studio` 是工作室私有结构(JSON 串),存提升参数额外元信息(label/type/options)+ 结构化步骤
 * + 节点表面(icon/category),供回库重开时无损反解析。后端忽略此键;删了它节点照常跑,只是回开时
 * 降级为「按 commandTemplate 文本兜成一个命令步骤 + 按 params 文本反解析参数(type 全 text)」。
 * 提升参数的 key/default 仍以 `params` 文本为唯一真源,避免双源漂移。
 */

import { t } from '../../i18n'

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

// ─── 结构化步骤(积木)──────────────────────────────────────────────────────────

export type StudioStepKind =
  | 'command'
  | 'install'
  | 'echo'
  | 'env'
  | 'workDir'
  | 'path'
  | 'artifact'
  | 'download'
  | 'extract'
  | 'condition'
  | 'retry'
  | 'timeout'
  | 'sleep'
  | 'healthcheck'
  | 'note'
  | 'test'

/** 一个步骤:种类 + 该种类的字段袋(字段名见 stepDefaults / bodyFields)。`id` 仅前端 UI 用,不落库。 */
export interface StudioStep {
  id: number
  kind: StudioStepKind
  fields: Record<string, string>
}

/** 节点表面:复用者第一眼看到的卡片元信息。summary 可含 {{param}}。 */
export interface NodeMeta {
  icon: string
  category: string
  summary: string
}

/** 积木目录项。 */
export interface CatalogItem {
  kind: StudioStepKind
  label: string
  color: string
}

/** 积木目录(按分组),决定左侧调色板的排布与配色。 */
export const STEP_CATALOG: ReadonlyArray<{ group: string; items: ReadonlyArray<CatalogItem> }> = [
  {
    get group() { return t('pipelineJob.studioGroupBasic') },
    items: [
      { kind: 'command', get label() { return t('pipelineJob.studioStepCommand') }, color: '#2f9e44' },
      { kind: 'install', get label() { return t('pipelineJob.studioStepInstall') }, color: '#2f9e44' },
      { kind: 'echo', get label() { return t('pipelineJob.studioStepEcho') }, color: '#2f9e44' },
    ],
  },
  {
    get group() { return t('pipelineJob.studioGroupEnvDir') },
    items: [
      { kind: 'env', get label() { return t('pipelineJob.studioStepEnv') }, color: '#b8860b' },
      { kind: 'workDir', get label() { return t('pipelineJob.studioStepWorkDir') }, color: '#1098ad' },
      { kind: 'path', get label() { return t('pipelineJob.studioStepPath') }, color: '#1098ad' },
    ],
  },
  {
    get group() { return t('pipelineJob.studioGroupQuality') },
    items: [
      { kind: 'test', get label() { return t('pipelineJob.studioStepTest') }, color: '#e8590c' },
      { kind: 'healthcheck', get label() { return t('pipelineJob.studioStepHealthcheck') }, color: '#e8590c' },
    ],
  },
  {
    get group() { return t('pipelineJob.studioGroupArtifact') },
    items: [
      { kind: 'artifact', get label() { return t('pipelineJob.studioStepArtifact') }, color: '#7048e8' },
      { kind: 'download', get label() { return t('pipelineJob.studioStepDownload') }, color: '#7048e8' },
      { kind: 'extract', get label() { return t('pipelineJob.studioStepExtract') }, color: '#7048e8' },
    ],
  },
  {
    get group() { return t('pipelineJob.studioGroupControl') },
    items: [
      { kind: 'condition', get label() { return t('pipelineJob.studioStepCondition') }, color: '#e03131' },
      { kind: 'retry', get label() { return t('pipelineJob.studioStepRetry') }, color: '#e03131' },
      { kind: 'timeout', get label() { return t('pipelineJob.studioStepTimeout') }, color: '#e03131' },
      { kind: 'sleep', get label() { return t('pipelineJob.studioStepSleep') }, color: '#e03131' },
    ],
  },
  {
    get group() { return t('pipelineJob.studioGroupDoc') },
    items: [{ kind: 'note', get label() { return t('pipelineJob.studioStepNote') }, color: '#868e96' }],
  },
]

const CATALOG_BY_KIND: Record<string, CatalogItem> = (() => {
  const m: Record<string, CatalogItem> = {}
  for (const g of STEP_CATALOG) for (const it of g.items) m[it.kind] = it
  return m
})()

/** 查目录项(未知种类兜底成灰色)。 */
export function catalogItem(kind: StudioStepKind): CatalogItem {
  return CATALOG_BY_KIND[kind] ?? { kind, label: kind, color: '#868e96' }
}

/** 新建某种步骤的默认字段(对齐 demo seed)。 */
export function stepDefaults(kind: StudioStepKind): Record<string, string> {
  switch (kind) {
    case 'command':
      return { command: '' }
    case 'install':
      return { command: 'npm ci' }
    case 'echo':
      return { command: '构建开始' }
    case 'env':
      return { envKey: 'KEY', envValue: 'value' }
    case 'workDir':
      return { dir: '.' }
    case 'path':
      return { dir: 'node_modules/.bin' }
    case 'artifact':
      return { artifact: 'dist' }
    case 'download':
      return { url: 'https://example.com/x', out: 'x' }
    case 'extract':
      return { file: 'x.tar.gz', dir: '.' }
    case 'condition':
      return { condition: 'test -f package.json' }
    case 'retry':
      return { command: 'flaky-cmd', count: '3', delay: '2' }
    case 'timeout':
      return { command: 'long-cmd', secs: '60' }
    case 'sleep':
      return { secs: '5' }
    case 'healthcheck':
      return { url: 'http://127.0.0.1:8080/health', delay: '2' }
    case 'note':
      return { text: '说明…' }
    case 'test':
      return { command: 'npm test', reportPath: 'junit.xml', minCov: '80' }
    default:
      return {}
  }
}

let stepUid = 0
/** 造一个带新 id 的步骤(默认字段)。 */
export function makeStep(kind: StudioStepKind): StudioStep {
  return { id: ++stepUid, kind, fields: { ...stepDefaults(kind) } }
}

/** config 键名:工作室私有元信息(后端忽略,仅前端无损回开用)。 */
export const STUDIO_META_KEY = '__studio'

const PARAM_TYPES: readonly PromotedParamType[] = ['text', 'select', 'number', 'toggle']
const STEP_KINDS = new Set<string>(STEP_CATALOG.flatMap((g) => g.items.map((i) => i.kind)))

function configString(config: Record<string, unknown>, key: string): string {
  const v = config[key]
  if (typeof v === 'string') return v
  return v == null ? '' : String(v)
}

/** 单引号包裹含特殊字符的值(对齐 demo shq)。 */
function shq(v: string): string {
  return /[^\w@%+=:,./{}-]/.test(v) ? `'${v.replace(/'/g, `'\\''`)}'` : v
}

export interface CompiledSteps {
  commandTemplate: string
  artifactPath: string
  /** 质量门禁等额外 config 键(testReport / reportPath / gateMinCoverage)。 */
  extraKeys: Record<string, string>
}

/** 结构化步骤 → 命令脚本 + 产物 + 额外键(对齐 demo compile 的 switch)。 */
export function compileSteps(steps: readonly StudioStep[]): CompiledSteps {
  const cmds: string[] = []
  const artifacts: string[] = []
  const extraKeys: Record<string, string> = {}
  const f = (s: StudioStep, k: string): string => (s.fields[k] ?? '').trim()
  for (const s of steps) {
    switch (s.kind) {
      case 'command':
      case 'install':
        if (f(s, 'command')) cmds.push(...s.fields.command.split('\n'))
        break
      case 'echo':
        if (f(s, 'command')) cmds.push(`echo ${shq(s.fields.command)}`)
        break
      case 'env':
        if (f(s, 'envKey')) cmds.push(`export ${s.fields.envKey}=${shq(s.fields.envValue ?? '')}`)
        break
      case 'path':
        if (f(s, 'dir')) cmds.push(`export PATH=${shq(s.fields.dir)}:$PATH`)
        break
      case 'workDir':
        if (f(s, 'dir')) cmds.push(`cd ${shq(s.fields.dir)}`)
        break
      case 'artifact':
        if (f(s, 'artifact')) artifacts.push(s.fields.artifact)
        break
      case 'download':
        if (f(s, 'url')) cmds.push(`curl -fsSL ${shq(s.fields.url)} -o ${shq(f(s, 'out') || 'download.bin')}`)
        break
      case 'extract':
        if (f(s, 'file')) cmds.push(`tar -xzf ${shq(s.fields.file)} -C ${shq(f(s, 'dir') || '.')}`)
        break
      case 'condition':
        if (f(s, 'condition')) cmds.push(`if ! ( ${s.fields.condition} ); then echo "条件不满足,跳过"; exit 0; fi`)
        break
      case 'retry':
        if (f(s, 'command'))
          cmds.push(`for i in $(seq ${f(s, 'count') || '3'}); do ${s.fields.command} && break || sleep ${f(s, 'delay') || '2'}; done`)
        break
      case 'timeout':
        if (f(s, 'command')) cmds.push(`timeout ${f(s, 'secs') || '60'} ${s.fields.command}`)
        break
      case 'sleep':
        cmds.push(`sleep ${f(s, 'secs') || '5'}`)
        break
      case 'healthcheck':
        if (f(s, 'url')) cmds.push(`until curl -fsS ${shq(s.fields.url)} >/dev/null; do sleep ${f(s, 'delay') || '2'}; done`)
        break
      case 'note':
        if (f(s, 'text')) cmds.push(`# ${s.fields.text}`)
        break
      case 'test':
        if (f(s, 'command')) cmds.push(...s.fields.command.split('\n'))
        if (f(s, 'reportPath')) {
          extraKeys.testReport = 'junit'
          extraKeys.reportPath = s.fields.reportPath
        }
        if (f(s, 'minCov')) extraKeys.gateMinCoverage = s.fields.minCov
        break
    }
  }
  return { commandTemplate: cmds.join('\n'), artifactPath: artifacts.join('\n'), extraKeys }
}

/** 工作室完整模型。 */
export interface StudioModel {
  image: string
  params: PromotedParam[]
  steps: StudioStep[]
  meta: NodeMeta
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

interface EncodedParamMeta {
  key: string
  label: string
  type: PromotedParamType
  options?: string[]
}

/** 提升参数的额外元信息(label/type/options)编码数组(不含 default,以 params 文本为准)。 */
function encodeParamMeta(params: readonly PromotedParam[]): EncodedParamMeta[] {
  return params
    .filter((p) => p.key.trim() !== '')
    .map((p) => {
      const m: EncodedParamMeta = { key: p.key.trim(), label: p.label, type: p.type }
      if (p.type === 'select' && p.options && p.options.length > 0) m.options = p.options
      return m
    })
}

/**
 * 把提升参数编码成一个 `__studio` JSON 串(仅 params 元信息,v2 兼容)。
 * 供调用方/测试构造只关心参数定义的 __studio 值;完整(含 steps/meta)的串由 compileStudioConfig 产出。
 */
export function encodeStudioMeta(params: readonly PromotedParam[]): string {
  return JSON.stringify({ v: 2, params: encodeParamMeta(params) })
}

interface DecodedMeta {
  key: string
  label?: string
  type?: PromotedParamType
  options?: string[]
}

interface DecodedStudio {
  params: DecodedMeta[] | null
  steps: StudioStep[] | null
  meta: Partial<NodeMeta> | null
}

/** 解码 __studio(v1 仅 params;v2 含 steps + meta)。非法/缺失各字段降级为 null,绝不抛错丢数据。 */
function decodeStudio(raw: unknown): DecodedStudio {
  const empty: DecodedStudio = { params: null, steps: null, meta: null }
  if (typeof raw !== 'string' || raw.trim() === '') return empty
  let obj: unknown
  try {
    obj = JSON.parse(raw)
  } catch {
    return empty
  }
  if (!obj || typeof obj !== 'object') return empty
  const o = obj as Record<string, unknown>

  // params 元信息
  let params: DecodedMeta[] | null = null
  if (Array.isArray(o.params)) {
    params = []
    for (const p of o.params) {
      if (!p || typeof p !== 'object') continue
      const key = (p as { key?: unknown }).key
      if (typeof key !== 'string' || key.trim() === '') continue
      const type = (p as { type?: unknown }).type
      const label = (p as { label?: unknown }).label
      const options = (p as { options?: unknown }).options
      params.push({
        key: key.trim(),
        label: typeof label === 'string' ? label : undefined,
        type: PARAM_TYPES.includes(type as PromotedParamType) ? (type as PromotedParamType) : undefined,
        options: Array.isArray(options) ? options.filter((x): x is string => typeof x === 'string') : undefined,
      })
    }
  }

  // 结构化步骤
  let steps: StudioStep[] | null = null
  if (Array.isArray(o.steps)) {
    steps = []
    for (const s of o.steps) {
      if (!s || typeof s !== 'object') continue
      const kind = (s as { kind?: unknown }).kind
      if (typeof kind !== 'string' || !STEP_KINDS.has(kind)) continue
      const rawFields = (s as { fields?: unknown }).fields
      const fields: Record<string, string> = {}
      if (rawFields && typeof rawFields === 'object') {
        for (const [k, v] of Object.entries(rawFields as Record<string, unknown>)) {
          fields[k] = typeof v === 'string' ? v : v == null ? '' : String(v)
        }
      }
      steps.push({ id: ++stepUid, kind: kind as StudioStepKind, fields })
    }
  }

  // 节点表面
  let meta: Partial<NodeMeta> | null = null
  if (o.meta && typeof o.meta === 'object') {
    const m = o.meta as Record<string, unknown>
    meta = {}
    if (typeof m.icon === 'string') meta.icon = m.icon
    if (typeof m.category === 'string') meta.category = m.category
    if (typeof m.summary === 'string') meta.summary = m.summary
  }

  return { params, steps, meta }
}

/** 合并 params 文本(key+default 真源)与 __studio 元信息(label/type/options)→ 提升参数列表。 */
export function parsePromotedParams(config: Record<string, unknown>): PromotedParam[] {
  const base = parseParamsText(configString(config, 'params'))
  const meta = decodeStudio(config[STUDIO_META_KEY]).params
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

/**
 * 实例编辑专用:读出每个提升参数的「当前值」(key → value)。
 *
 * 当前值以 `params` 文本里 `key=value` 的 value 为唯一真源 —— `parsePromotedParams`
 * 把它解析进 `default` 字段;在实例语境下它就是「实例已填的值 / 未填则模板默认」。
 * 供节点抽屉的短清单控件初始绑定。
 */
export function promotedValues(config: Record<string, unknown>): Record<string, string> {
  const out: Record<string, string> = {}
  for (const p of parsePromotedParams(config)) out[p.key] = p.default
  return out
}

/**
 * 实例编辑专用:把短清单控件改后的值写回 `params` 文本。
 *
 * 以提升参数定义(key/顺序)为骨架,仅替换各参数的 value;`values` 缺失的 key
 * 沿用其原 default。**只动 value,不碰 key/label/type/options**,故 `__studio`
 * 元信息不被实例编辑破坏;产出直接覆盖 config.params,回开短清单回显新值。
 */
export function applyPromotedValues(
  params: readonly PromotedParam[],
  values: Record<string, string>,
): string {
  return paramsToText(
    params.map((p) => {
      const next = values[p.key]
      return next === undefined ? p : { ...p, default: next }
    }),
  )
}

const DEFAULT_META: NodeMeta = {
  icon: '🔧',
  get category() {
    return t('pipelineJob.studioDefaultCategory')
  },
  summary: '',
}

/** 反解析 templated 节点 config → 工作室模型。优先用 __studio 里的结构化步骤;缺失则把脚本兜成单命令步骤。 */
export function parseStudioConfig(config: Record<string, unknown>): StudioModel {
  const decoded = decodeStudio(config[STUDIO_META_KEY])
  let steps: StudioStep[]
  if (decoded.steps && decoded.steps.length > 0) {
    steps = decoded.steps
  } else {
    // 兜底:把现有脚本/产物还原成可编辑的命令步骤 + 产物步骤。
    steps = []
    const script = configString(config, 'commandTemplate') || configString(config, 'commands')
    if (script.trim()) steps.push({ id: ++stepUid, kind: 'command', fields: { command: script } })
    const artifact = configString(config, 'artifactPath')
    if (artifact.trim()) steps.push({ id: ++stepUid, kind: 'artifact', fields: { artifact } })
  }
  return {
    image: configString(config, 'image'),
    params: parsePromotedParams(config),
    steps,
    meta: {
      icon: decoded.meta?.icon ?? DEFAULT_META.icon,
      category: decoded.meta?.category ?? DEFAULT_META.category,
      summary: decoded.meta?.summary ?? '',
    },
  }
}

/** 工作室模型 → templated 节点 config(后端原样跑;空值不入 config;__studio 仅供回开)。 */
export function compileStudioConfig(model: StudioModel): Record<string, string> {
  const config: Record<string, string> = {}
  const image = model.image.trim()
  if (image) config.image = model.image
  const { commandTemplate, artifactPath, extraKeys } = compileSteps(model.steps)
  if (commandTemplate.trim()) config.commandTemplate = commandTemplate
  if (artifactPath.trim()) config.artifactPath = artifactPath
  for (const [k, v] of Object.entries(extraKeys)) config[k] = v
  const params = paramsToText(model.params)
  if (params) config.params = params
  // __studio:有提升参数 / 步骤 / 非默认表面 时写入,确保回开无损。
  const hasParams = model.params.some((p) => p.key.trim() !== '')
  const hasSteps = model.steps.length > 0
  const metaChanged =
    model.meta.icon !== DEFAULT_META.icon || model.meta.category !== DEFAULT_META.category || model.meta.summary !== ''
  if (hasParams || hasSteps || metaChanged) {
    config[STUDIO_META_KEY] = JSON.stringify({
      v: 2,
      params: encodeParamMeta(model.params),
      steps: model.steps.map((s) => ({ kind: s.kind, fields: s.fields })),
      meta: { icon: model.meta.icon, category: model.meta.category, summary: model.meta.summary },
    })
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

/** 新建工作室节点的初始模型(空画布 + 无提升参数 + 默认表面)。 */
export function emptyStudioModel(): StudioModel {
  return { image: '', params: [], steps: [], meta: { ...DEFAULT_META } }
}
