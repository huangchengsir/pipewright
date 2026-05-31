/**
 * jobConfigSchema — declarative, per-type parameter forms for pipeline canvas nodes.
 *
 * The 2-2 pipeline contract freezes the node shape as `config: Record<string,string>`
 * (a flat string map). Rather than widen that contract, every known job *type* declares
 * a typed field schema here; JobDrawer renders those fields and reads/writes specific
 * keys in the same flat map. Anything not covered by a type's schema stays editable via
 * the drawer's "raw parameters" fallback, so custom/unknown types and power users lose
 * nothing. This is the Jenkins/云效-style "each step type has its own form" behaviour,
 * implemented without a backend DTO change.
 *
 * All values are strings (config is Record<string,string>). number/toggle fields are
 * serialized to/from strings at the field layer; multiline commands are stored as a
 * single newline-joined string.
 */

import type { CredentialType } from '../../api/credentials'

// ─── Field model ──────────────────────────────────────────────────────────────

export type FieldKind =
  | 'text'
  | 'textarea'
  | 'select'
  | 'number'
  | 'toggle'
  | 'credential'
  | 'server'

export interface SelectOption {
  value: string
  label: string
}

export interface JobField {
  /** Key written into job.config */
  key: string
  /** Human label (zh) */
  label: string
  kind: FieldKind
  placeholder?: string
  /** Helper text shown under the control */
  hint?: string
  /** Options for `select` */
  options?: SelectOption[]
  /** Restrict the credential picker to one credential type */
  credentialType?: CredentialType
  /** Render with monospace font (paths, commands, image refs) */
  monospace?: boolean
  /** Conditional visibility based on the current config values */
  when?: (config: Record<string, string>) => boolean
}

/** Accent palette keys — map to --color-{accent} / --color-{accent}-soft tokens. */
export type AccentName = 'cyan' | 'primary' | 'green' | 'amber' | 'red' | 'neutral'

/** Picker category id for grouping task types (Jenkins/云效-style gallery). */
export type CategoryId = 'source' | 'build' | 'deploy' | 'quality' | 'notify' | 'custom'

export interface JobTypeSpec {
  type: string
  /** Friendly zh label shown in the picker, drawer, and node card */
  label: string
  /** One-line description of what this node does (shown in the picker card) */
  description: string
  /** Accent colour for the type icon */
  accent: AccentName
  /** Category the type belongs to (picker grouping) */
  category: CategoryId
  fields: JobField[]
}

// ─── Shared option sets ─────────────────────────────────────────────────────────

const ARTIFACT_OPTIONS: SelectOption[] = [
  { value: 'image', label: '容器镜像 (image)' },
  { value: 'jar', label: 'JAR 包 (jar)' },
  { value: 'dist', label: '静态资源 (dist)' },
]

const BUILD_MODEL_OPTIONS: SelectOption[] = [
  { value: 'dockerfile', label: '自带 Dockerfile' },
  { value: 'toolchain', label: '平台工具链' },
]

const TOOLCHAIN_OPTIONS: SelectOption[] = [
  { value: 'node', label: 'Node.js' },
  { value: 'java', label: 'Java / Maven' },
  { value: 'go', label: 'Go' },
  { value: 'python', label: 'Python' },
  { value: 'custom', label: '自定义' },
]

const DEPLOY_STRATEGY_OPTIONS: SelectOption[] = [
  { value: 'rolling', label: '滚动更新(零停机)' },
  { value: 'recreate', label: '先停后起 (recreate)' },
  { value: 'blue-green', label: '蓝绿部署' },
]

const PROBE_MODE_OPTIONS: SelectOption[] = [
  { value: 'http', label: 'HTTP 探测' },
  { value: 'command', label: '命令探测' },
]

// `when` helpers
const modelIs = (v: string) => (c: Record<string, string>) =>
  (c.buildModel || 'dockerfile') === v
const probeIs = (v: string) => (c: Record<string, string>) =>
  (c.probeMode || 'http') === v

// ─── Per-type specs ──────────────────────────────────────────────────────────────

const SCRIPT_FIELDS: JobField[] = [
  {
    key: 'image',
    label: '运行镜像',
    kind: 'text',
    monospace: true,
    placeholder: 'node:20',
    hint: '在该隔离容器内执行命令(对标 Jenkins agent / 云效构建镜像)',
  },
  {
    key: 'commands',
    label: '执行命令',
    kind: 'textarea',
    monospace: true,
    placeholder: 'npm ci\nnpm run build',
    hint: '每行一条命令,按顺序执行',
  },
  {
    key: 'workDir',
    label: '工作目录',
    kind: 'text',
    monospace: true,
    placeholder: '.',
    hint: '相对克隆工作区根;留空为工作区根',
  },
]

export const JOB_TYPE_SPECS: Record<string, JobTypeSpec> = {
  git_source: {
    type: 'git_source',
    label: '拉取源码',
    description: '克隆 Git 仓库到构建工作区',
    accent: 'cyan',
    category: 'source',
    fields: [
      {
        key: 'repoUrl',
        label: '仓库地址',
        kind: 'text',
        monospace: true,
        placeholder: 'https://gitee.com/org/repo.git',
        hint: '留空则使用项目绑定的默认仓库',
      },
      {
        key: 'branch',
        label: '分支 / Ref',
        kind: 'text',
        monospace: true,
        placeholder: 'main',
        hint: '留空则使用触发时的分支或项目默认分支',
      },
      {
        key: 'credentialId',
        label: '访问凭据',
        kind: 'credential',
        credentialType: 'git_token',
        hint: '私有仓库需引用 Git 令牌凭据',
      },
      {
        key: 'depth',
        label: '克隆深度',
        kind: 'number',
        placeholder: '1',
        hint: '浅克隆深度;留空为完整历史',
      },
    ],
  },

  build_image: {
    type: 'build_image',
    label: '构建',
    description: '构建产物(镜像 / JAR / 静态资源)',
    accent: 'primary',
    category: 'build',
    fields: [
      { key: 'artifactType', label: '产物类型', kind: 'select', options: ARTIFACT_OPTIONS },
      {
        key: 'buildModel',
        label: '构建方式',
        kind: 'select',
        options: BUILD_MODEL_OPTIONS,
        hint: '自带 Dockerfile,或由平台按工具链构建',
      },
      {
        key: 'dockerfilePath',
        label: 'Dockerfile 路径',
        kind: 'text',
        monospace: true,
        placeholder: 'Dockerfile',
        when: modelIs('dockerfile'),
      },
      {
        key: 'context',
        label: '构建上下文',
        kind: 'text',
        monospace: true,
        placeholder: '.',
        hint: '相对仓库根的构建上下文目录',
        when: modelIs('dockerfile'),
      },
      {
        key: 'toolchainLanguage',
        label: '工具链语言',
        kind: 'select',
        options: TOOLCHAIN_OPTIONS,
        when: modelIs('toolchain'),
      },
      {
        key: 'toolchainVersion',
        label: '工具链版本',
        kind: 'text',
        monospace: true,
        placeholder: '20',
        when: modelIs('toolchain'),
      },
      {
        key: 'buildCommand',
        label: '构建命令',
        kind: 'text',
        monospace: true,
        placeholder: 'npm run build',
        hint: '工具链方式下的构建入口命令',
        when: modelIs('toolchain'),
      },
    ],
  },

  push_image: {
    type: 'push_image',
    label: '推送镜像',
    description: '推送镜像到容器仓库',
    accent: 'amber',
    category: 'build',
    fields: [
      {
        key: 'registry',
        label: '镜像仓库地址',
        kind: 'text',
        monospace: true,
        placeholder: 'registry.cn-hangzhou.aliyuncs.com',
      },
      {
        key: 'imageName',
        label: '镜像名',
        kind: 'text',
        monospace: true,
        placeholder: 'org/app',
      },
      {
        key: 'tag',
        label: '标签',
        kind: 'text',
        monospace: true,
        placeholder: '${COMMIT_SHA}',
        hint: '可用变量:${COMMIT_SHA} ${BRANCH} ${BUILD_NUMBER}',
      },
      {
        key: 'credentialId',
        label: '仓库凭据',
        kind: 'credential',
        credentialType: 'registry',
        hint: '引用镜像仓库登录凭据',
      },
    ],
  },

  deploy_ssh: {
    type: 'deploy_ssh',
    label: 'SSH 部署',
    description: '通过 SSH 把产物部署到目标服务器',
    accent: 'green',
    category: 'deploy',
    fields: [
      {
        key: 'serverId',
        label: '目标服务器',
        kind: 'server',
        hint: '选择已登记的服务器(凭据按引用绑定)',
      },
      {
        key: 'deployPath',
        label: '部署路径',
        kind: 'text',
        monospace: true,
        placeholder: '/opt/app',
      },
      {
        key: 'strategy',
        label: '部署策略',
        kind: 'select',
        options: DEPLOY_STRATEGY_OPTIONS,
      },
      {
        key: 'restartCommand',
        label: '重启 / 切换命令',
        kind: 'text',
        monospace: true,
        placeholder: 'systemctl restart app',
        hint: '部署后在目标机执行,用于重启或原子切换',
      },
    ],
  },

  health_check: {
    type: 'health_check',
    label: '健康门控',
    description: '部署后探测健康,失败则回滚',
    accent: 'green',
    category: 'quality',
    fields: [
      { key: 'probeMode', label: '探测方式', kind: 'select', options: PROBE_MODE_OPTIONS },
      {
        key: 'url',
        label: '探测 URL',
        kind: 'text',
        monospace: true,
        placeholder: 'http://localhost:8080/healthz',
        when: probeIs('http'),
      },
      {
        key: 'expectStatus',
        label: '期望状态码',
        kind: 'number',
        placeholder: '200',
        when: probeIs('http'),
      },
      {
        key: 'command',
        label: '探测命令',
        kind: 'text',
        monospace: true,
        placeholder: 'curl -fsS localhost:8080/healthz',
        when: probeIs('command'),
      },
      { key: 'retries', label: '重试次数', kind: 'number', placeholder: '3' },
      { key: 'intervalSeconds', label: '重试间隔(秒)', kind: 'number', placeholder: '5' },
    ],
  },

  notify: {
    type: 'notify',
    label: '通知',
    description: '运行结束时发送通知',
    accent: 'cyan',
    category: 'notify',
    fields: [
      {
        key: 'channel',
        label: '通知渠道',
        kind: 'text',
        placeholder: '渠道 ID 或名称',
        hint: '引用「通知」设置里已配置的渠道',
      },
      {
        key: 'events',
        label: '触发事件',
        kind: 'text',
        monospace: true,
        placeholder: 'success,failed',
        hint: '逗号分隔;留空为全部终态',
      },
    ],
  },

  script: {
    type: 'script',
    label: '自定义脚本',
    description: '在隔离容器内执行任意命令',
    accent: 'primary',
    category: 'custom',
    fields: SCRIPT_FIELDS,
  },

  custom: {
    type: 'custom',
    label: '自定义脚本',
    description: '在隔离容器内执行任意命令',
    accent: 'primary',
    category: 'custom',
    fields: SCRIPT_FIELDS,
  },
}

// ─── Lookups ──────────────────────────────────────────────────────────────────

/** Canonical, ordered list of pickable types (excludes the `custom` alias of `script`). */
export const PICKABLE_TYPES: readonly string[] = [
  'git_source',
  'build_image',
  'push_image',
  'deploy_ssh',
  'health_check',
  'notify',
  'script',
]

/** Dropdown options for the job type selector (friendly label + token). */
export const JOB_TYPE_OPTIONS: SelectOption[] = PICKABLE_TYPES.map((type) => ({
  value: type,
  label: `${JOB_TYPE_SPECS[type].label} · ${type}`,
}))

/** Picker categories in display order. */
export const JOB_CATEGORIES: ReadonlyArray<{ id: CategoryId; label: string }> = [
  { id: 'source', label: '源' },
  { id: 'build', label: '构建与制品' },
  { id: 'deploy', label: '部署' },
  { id: 'quality', label: '质量门禁' },
  { id: 'notify', label: '通知' },
  { id: 'custom', label: '自定义' },
]

/** Pickable specs grouped by category, in display order; empty groups omitted. */
export function groupedJobTypes(): Array<{ id: CategoryId; label: string; specs: JobTypeSpec[] }> {
  return JOB_CATEGORIES.map((cat) => ({
    id: cat.id,
    label: cat.label,
    specs: PICKABLE_TYPES.map((t) => JOB_TYPE_SPECS[t]).filter((s) => s.category === cat.id),
  })).filter((g) => g.specs.length > 0)
}

/** Accent colour for a type, falling back to neutral for unknown types. */
export function jobTypeAccent(type: string): AccentName {
  return JOB_TYPE_SPECS[type]?.accent ?? 'neutral'
}

/** Spec for a job type, or null when the type has no typed schema. */
export function getJobTypeSpec(type: string): JobTypeSpec | null {
  return JOB_TYPE_SPECS[type] ?? null
}

/** Friendly zh label for a type, falling back to the raw token. */
export function jobTypeLabel(type: string): string {
  return JOB_TYPE_SPECS[type]?.label ?? type
}

/** The set of config keys owned by a type's schema (used to split out raw extras). */
export function schemaKeys(type: string): Set<string> {
  const spec = JOB_TYPE_SPECS[type]
  return new Set(spec ? spec.fields.map((f) => f.key) : [])
}

/**
 * Split a config map into the keys owned by the type's schema vs. the rest
 * (rendered in the "raw parameters" advanced section). Order of extras preserved.
 */
export function splitConfig(
  type: string,
  config: Record<string, string>,
): { extras: Array<[string, string]> } {
  const owned = schemaKeys(type)
  const extras = Object.entries(config).filter(([k]) => !owned.has(k))
  return { extras }
}
