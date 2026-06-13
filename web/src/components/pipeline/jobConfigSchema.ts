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
import { t } from '../../i18n'

// ─── Field model ──────────────────────────────────────────────────────────────

export type FieldKind =
  | 'text'
  | 'textarea'
  | 'select'
  | 'number'
  | 'toggle'
  | 'credential'
  | 'server'
  | 'channel'

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
  /** 模板节点的预填配置(选中即带上,用户只改参数);普通节点省略。 */
  defaultConfig?: Record<string, string>
}

// ─── Shared option sets ─────────────────────────────────────────────────────────

const ARTIFACT_OPTIONS: SelectOption[] = [
  { value: 'image', get label() { return t('pipelineJob.artifactImage') } },
  { value: 'jar', get label() { return t('pipelineJob.artifactJar') } },
  { value: 'dist', get label() { return t('pipelineJob.artifactDist') } },
]

const BUILD_MODEL_OPTIONS: SelectOption[] = [
  { value: 'dockerfile', get label() { return t('pipelineJob.buildModelDockerfile') } },
  { value: 'toolchain', get label() { return t('pipelineJob.buildModelToolchain') } },
]

const TOOLCHAIN_OPTIONS: SelectOption[] = [
  { value: 'node', label: 'Node.js' },
  { value: 'java', label: 'Java / Maven' },
  { value: 'go', label: 'Go' },
  { value: 'python', label: 'Python' },
  { value: 'custom', get label() { return t('pipelineJob.toolchainCustom') } },
]

const DEPLOY_STRATEGY_OPTIONS: SelectOption[] = [
  { value: 'rolling', get label() { return t('pipelineJob.deployStrategyRolling') } },
  { value: 'recreate', get label() { return t('pipelineJob.deployStrategyRecreate') } },
  { value: 'blue-green', get label() { return t('pipelineJob.deployStrategyBlueGreen') } },
]

const PROBE_MODE_OPTIONS: SelectOption[] = [
  { value: 'http', get label() { return t('pipelineJob.probeModeHttp') } },
  { value: 'command', get label() { return t('pipelineJob.probeModeCommand') } },
]

// 部署节点的产物类型偏好(本 run 同时产出镜像与文件产物时,挑哪件部署)。
// 留空 = 自动:优先文件产物(dist/jar/archive),没有文件产物才用镜像。
const DEPLOY_ARTIFACT_OPTIONS: SelectOption[] = [
  { value: '', get label() { return t('pipelineJob.deployArtifactAuto') } },
  { value: 'image', get label() { return t('pipelineJob.artifactImage') } },
  { value: 'dist', get label() { return t('pipelineJob.artifactDist') } },
  { value: 'jar', get label() { return t('pipelineJob.artifactJar') } },
  { value: 'archive', get label() { return t('pipelineJob.deployArtifactArchive') } },
]

// `when` helpers
const modelIs = (v: string) => (c: Record<string, string>) =>
  (c.buildModel || 'dockerfile') === v
const probeIs = (v: string) => (c: Record<string, string>) =>
  (c.probeMode || 'http') === v

// ─── Per-type specs ──────────────────────────────────────────────────────────────

// 任务级执行选项(P0 引擎能力):超时 / 重试 / 资源规格。脚本类节点共用。
// 全部可选;留空 = 旧行为(不限超时、不重试、不限资源)。timeout/retry 为非负整数。
const EXEC_OPTION_FIELDS: JobField[] = [
  {
    key: 'timeoutSeconds',
    get label() { return t('pipelineJob.fieldTimeoutLabel') },
    kind: 'number',
    placeholder: '0',
    get hint() { return t('pipelineJob.fieldTimeoutHint') },
  },
  {
    key: 'retries',
    get label() { return t('pipelineJob.fieldRetriesLabel') },
    kind: 'number',
    placeholder: '0',
    get hint() { return t('pipelineJob.fieldRetriesHint') },
  },
  {
    key: 'cpu',
    get label() { return t('pipelineJob.fieldCpuLabel') },
    kind: 'text',
    monospace: true,
    placeholder: '1',
    get hint() { return t('pipelineJob.fieldCpuHint') },
  },
  {
    key: 'memory',
    get label() { return t('pipelineJob.fieldMemoryLabel') },
    kind: 'text',
    monospace: true,
    placeholder: '512m',
    get hint() { return t('pipelineJob.fieldMemoryHint') },
  },
]

const SCRIPT_FIELDS: JobField[] = [
  {
    key: 'image',
    get label() { return t('pipelineJob.fieldImageLabel') },
    kind: 'text',
    monospace: true,
    placeholder: 'node:20',
    get hint() { return t('pipelineJob.fieldImageHint') },
  },
  {
    key: 'commands',
    get label() { return t('pipelineJob.fieldCommandsLabel') },
    kind: 'textarea',
    monospace: true,
    placeholder: 'npm ci\nnpm run build',
    get hint() { return t('pipelineJob.fieldCommandsHint') },
  },
  {
    key: 'workDir',
    get label() { return t('pipelineJob.fieldWorkDirLabel') },
    kind: 'text',
    monospace: true,
    placeholder: '.',
    get hint() { return t('pipelineJob.fieldWorkDirHint') },
  },
  {
    key: 'artifactPath',
    get label() { return t('pipelineJob.fieldArtifactPathLabel') },
    kind: 'textarea',
    monospace: true,
    placeholder: 'frontend/dist\nbackend/target/app.jar',
    get hint() { return t('pipelineJob.fieldArtifactPathHint') },
  },
  ...EXEC_OPTION_FIELDS,
  {
    key: 'cachePaths',
    get label() { return t('pipelineJob.fieldCachePathsLabel') },
    kind: 'textarea',
    monospace: true,
    placeholder: 'node_modules\n.m2/repository',
    get hint() { return t('pipelineJob.fieldCachePathsHint') },
  },
  {
    key: 'cacheKey',
    get label() { return t('pipelineJob.fieldCacheKeyLabel') },
    kind: 'text',
    monospace: true,
    get placeholder() { return t('pipelineJob.fieldCacheKeyPlaceholder') },
    get hint() { return t('pipelineJob.fieldCacheKeyHint') },
  },
]

// SSH 部署节点的字段(deploy_ssh 与 deploy_frontend 模板共用)。
const DEPLOY_SSH_FIELDS: JobField[] = [
  {
    key: 'serverId',
    get label() { return t('pipelineJob.fieldServerIdLabel') },
    kind: 'server',
    get hint() { return t('pipelineJob.fieldServerIdHint') },
  },
  {
    key: 'artifactType',
    get label() { return t('pipelineJob.fieldArtifactTypeLabel') },
    kind: 'select',
    options: DEPLOY_ARTIFACT_OPTIONS,
    get hint() { return t('pipelineJob.fieldArtifactTypeHint') },
  },
  {
    key: 'deployPath',
    get label() { return t('pipelineJob.fieldDeployPathLabel') },
    kind: 'text',
    monospace: true,
    placeholder: '/opt/app',
    get hint() { return t('pipelineJob.fieldDeployPathHint') },
    when: (c) => c.artifactType !== 'image',
  },
  {
    key: 'containerName',
    get label() { return t('pipelineJob.fieldContainerNameLabel') },
    kind: 'text',
    monospace: true,
    placeholder: 'app',
    get hint() { return t('pipelineJob.fieldContainerNameHint') },
    when: (c) => c.artifactType === 'image',
  },
  {
    key: 'ports',
    get label() { return t('pipelineJob.fieldPortsLabel') },
    kind: 'text',
    monospace: true,
    placeholder: '8080:80, 9000:9000',
    get hint() { return t('pipelineJob.fieldPortsHint') },
    when: (c) => c.artifactType === 'image',
  },
  {
    key: 'runArgs',
    get label() { return t('pipelineJob.fieldRunArgsLabel') },
    kind: 'text',
    monospace: true,
    placeholder: '-e KEY=value --restart always',
    get hint() { return t('pipelineJob.fieldRunArgsHint') },
    when: (c) => c.artifactType === 'image',
  },
  {
    key: 'strategy',
    get label() { return t('pipelineJob.fieldStrategyLabel') },
    kind: 'select',
    options: DEPLOY_STRATEGY_OPTIONS,
  },
  {
    key: 'restartCommand',
    get label() { return t('pipelineJob.fieldRestartCommandLabel') },
    kind: 'textarea',
    monospace: true,
    placeholder: 'systemctl restart app\nnginx -s reload',
    get hint() { return t('pipelineJob.fieldRestartCommandHint') },
    when: (c) => c.artifactType !== 'image',
  },
]

export const JOB_TYPE_SPECS: Record<string, JobTypeSpec> = {
  git_source: {
    type: 'git_source',
    get label() { return t('pipelineJob.typeGitSourceLabel') },
    get description() { return t('pipelineJob.typeGitSourceDesc') },
    accent: 'cyan',
    category: 'source',
    fields: [
      {
        key: 'repoUrl',
        get label() { return t('pipelineJob.fieldRepoUrlLabel') },
        kind: 'text',
        monospace: true,
        placeholder: 'https://gitee.com/org/repo.git',
        get hint() { return t('pipelineJob.fieldRepoUrlHint') },
      },
      {
        key: 'branch',
        get label() { return t('pipelineJob.fieldBranchLabel') },
        kind: 'text',
        monospace: true,
        placeholder: 'main',
        get hint() { return t('pipelineJob.fieldBranchHint') },
      },
      {
        key: 'credentialId',
        get label() { return t('pipelineJob.fieldCredentialIdLabel') },
        kind: 'credential',
        credentialType: 'git_token',
        get hint() { return t('pipelineJob.fieldCredentialIdHint') },
      },
      {
        key: 'depth',
        get label() { return t('pipelineJob.fieldDepthLabel') },
        kind: 'number',
        placeholder: '1',
        get hint() { return t('pipelineJob.fieldDepthHint') },
      },
    ],
  },

  build_image: {
    type: 'build_image',
    get label() { return t('pipelineJob.typeBuildImageLabel') },
    get description() { return t('pipelineJob.typeBuildImageDesc') },
    accent: 'primary',
    category: 'build',
    fields: [
      { key: 'artifactType', get label() { return t('pipelineJob.fieldArtifactTypeBuildLabel') }, kind: 'select', options: ARTIFACT_OPTIONS },
      {
        key: 'buildModel',
        get label() { return t('pipelineJob.fieldBuildModelLabel') },
        kind: 'select',
        options: BUILD_MODEL_OPTIONS,
        get hint() { return t('pipelineJob.fieldBuildModelHint') },
      },
      {
        key: 'dockerfilePath',
        get label() { return t('pipelineJob.fieldDockerfilePathLabel') },
        kind: 'text',
        monospace: true,
        placeholder: 'Dockerfile',
        when: modelIs('dockerfile'),
      },
      {
        key: 'context',
        get label() { return t('pipelineJob.fieldContextLabel') },
        kind: 'text',
        monospace: true,
        placeholder: '.',
        get hint() { return t('pipelineJob.fieldContextHint') },
        when: modelIs('dockerfile'),
      },
      {
        key: 'toolchainLanguage',
        get label() { return t('pipelineJob.fieldToolchainLanguageLabel') },
        kind: 'select',
        options: TOOLCHAIN_OPTIONS,
        when: modelIs('toolchain'),
      },
      {
        key: 'toolchainVersion',
        get label() { return t('pipelineJob.fieldToolchainVersionLabel') },
        kind: 'text',
        monospace: true,
        placeholder: '20',
        when: modelIs('toolchain'),
      },
      {
        key: 'buildCommand',
        get label() { return t('pipelineJob.fieldBuildCommandLabel') },
        kind: 'text',
        monospace: true,
        placeholder: 'npm run build',
        get hint() { return t('pipelineJob.fieldBuildCommandHint') },
        when: modelIs('toolchain'),
      },
    ],
  },

  push_image: {
    type: 'push_image',
    get label() { return t('pipelineJob.typePushImageLabel') },
    get description() { return t('pipelineJob.typePushImageDesc') },
    accent: 'amber',
    category: 'build',
    fields: [
      {
        key: 'registry',
        get label() { return t('pipelineJob.fieldRegistryLabel') },
        kind: 'text',
        monospace: true,
        placeholder: 'registry.cn-hangzhou.aliyuncs.com',
      },
      {
        key: 'imageName',
        get label() { return t('pipelineJob.fieldImageNameLabel') },
        kind: 'text',
        monospace: true,
        placeholder: 'org/app',
      },
      {
        key: 'tag',
        get label() { return t('pipelineJob.fieldTagLabel') },
        kind: 'text',
        monospace: true,
        placeholder: '${COMMIT_SHA}',
        get hint() { return t('pipelineJob.fieldTagHint') },
      },
      {
        key: 'credentialId',
        get label() { return t('pipelineJob.fieldRegistryCredentialLabel') },
        kind: 'credential',
        credentialType: 'registry',
        get hint() { return t('pipelineJob.fieldRegistryCredentialHint') },
      },
    ],
  },

  deploy_ssh: {
    type: 'deploy_ssh',
    get label() { return t('pipelineJob.typeDeploySshLabel') },
    get description() { return t('pipelineJob.typeDeploySshDesc') },
    accent: 'green',
    category: 'deploy',
    fields: DEPLOY_SSH_FIELDS,
  },

  health_check: {
    type: 'health_check',
    get label() { return t('pipelineJob.typeHealthCheckLabel') },
    get description() { return t('pipelineJob.typeHealthCheckDesc') },
    accent: 'green',
    category: 'quality',
    fields: [
      { key: 'probeMode', get label() { return t('pipelineJob.fieldProbeModeLabel') }, kind: 'select', options: PROBE_MODE_OPTIONS },
      {
        key: 'url',
        get label() { return t('pipelineJob.fieldUrlLabel') },
        kind: 'text',
        monospace: true,
        placeholder: 'http://localhost:8080/healthz',
        when: probeIs('http'),
      },
      {
        key: 'expectStatus',
        get label() { return t('pipelineJob.fieldExpectStatusLabel') },
        kind: 'number',
        placeholder: '200',
        when: probeIs('http'),
      },
      {
        key: 'command',
        get label() { return t('pipelineJob.fieldCommandLabel') },
        kind: 'text',
        monospace: true,
        placeholder: 'curl -fsS localhost:8080/healthz',
        when: probeIs('command'),
      },
      { key: 'retries', get label() { return t('pipelineJob.fieldHealthRetriesLabel') }, kind: 'number', placeholder: '3' },
      { key: 'intervalSeconds', get label() { return t('pipelineJob.fieldIntervalSecondsLabel') }, kind: 'number', placeholder: '5' },
    ],
  },

  notify: {
    type: 'notify',
    get label() { return t('pipelineJob.typeNotifyLabel') },
    get description() { return t('pipelineJob.typeNotifyDesc') },
    accent: 'cyan',
    category: 'notify',
    fields: [
      {
        key: 'channel',
        get label() { return t('pipelineJob.fieldChannelLabel') },
        kind: 'channel',
        get hint() { return t('pipelineJob.fieldChannelHint') },
      },
      {
        key: 'titleTemplate',
        get label() { return t('pipelineJob.fieldTitleTemplateLabel') },
        kind: 'text',
        get placeholder() { return t('pipelineJob.fieldTitleTemplatePlaceholder') },
        get hint() { return t('pipelineJob.fieldTitleTemplateHint') },
      },
      {
        key: 'bodyTemplate',
        get label() { return t('pipelineJob.fieldBodyTemplateLabel') },
        kind: 'textarea',
        get placeholder() { return t('pipelineJob.fieldBodyTemplatePlaceholder') },
        get hint() { return t('pipelineJob.fieldBodyTemplateHint') },
      },
    ],
  },

  // ── 模板节点:预填好常见步骤参数,改改就能用,不必都写自定义脚本 ──
  build_frontend: {
    type: 'build_frontend',
    get label() { return t('pipelineJob.typeBuildFrontendLabel') },
    get description() { return t('pipelineJob.typeBuildFrontendDesc') },
    accent: 'primary',
    category: 'build',
    fields: SCRIPT_FIELDS,
    defaultConfig: {
      image: 'node:20',
      commands: 'cd frontend\nnpm install --no-audit --no-fund\nnpm run build',
      artifactPath: 'frontend/dist',
    },
  },

  build_backend: {
    type: 'build_backend',
    get label() { return t('pipelineJob.typeBuildBackendLabel') },
    get description() { return t('pipelineJob.typeBuildBackendDesc') },
    accent: 'amber',
    category: 'build',
    fields: SCRIPT_FIELDS,
    defaultConfig: {
      image: 'maven:3.9-eclipse-temurin-21',
      commands: 'cd backend\nmvn -B -DskipTests package',
      artifactPath: 'backend/target/*.jar',
    },
  },

  deploy_frontend: {
    type: 'deploy_frontend',
    get label() { return t('pipelineJob.typeDeployFrontendLabel') },
    get description() { return t('pipelineJob.typeDeployFrontendDesc') },
    accent: 'green',
    category: 'deploy',
    fields: DEPLOY_SSH_FIELDS,
    defaultConfig: { strategy: 'rolling', restartCommand: 'nginx -s reload' },
  },

  templated: {
    type: 'templated',
    get label() { return t('pipelineJob.typeTemplatedLabel') },
    get description() { return t('pipelineJob.typeTemplatedDesc') },
    accent: 'cyan',
    category: 'custom',
    fields: [
      {
        key: 'image',
        get label() { return t('pipelineJob.fieldImageLabel') },
        kind: 'text',
        monospace: true,
        placeholder: 'node:20',
      },
      {
        key: 'params',
        get label() { return t('pipelineJob.fieldParamsLabel') },
        kind: 'textarea',
        monospace: true,
        placeholder: 'dir=frontend\nbuildCmd=npm run build',
        get hint() { return t('pipelineJob.fieldParamsHint') },
      },
      {
        key: 'commandTemplate',
        get label() { return t('pipelineJob.fieldCommandTemplateLabel') },
        kind: 'textarea',
        monospace: true,
        placeholder: 'cd {{dir}}\nnpm install\n{{buildCmd}}',
        get hint() { return t('pipelineJob.fieldCommandTemplateHint') },
      },
      {
        key: 'artifactPath',
        get label() { return t('pipelineJob.fieldArtifactPathLabel') },
        kind: 'textarea',
        monospace: true,
        placeholder: '{{dir}}/dist',
        get hint() { return t('pipelineJob.fieldTemplatedArtifactPathHint') },
      },
      {
        key: 'workDir',
        get label() { return t('pipelineJob.fieldWorkDirLabel') },
        kind: 'text',
        monospace: true,
        placeholder: '.',
        get hint() { return t('pipelineJob.fieldTemplatedWorkDirHint') },
      },
      ...EXEC_OPTION_FIELDS,
      {
        key: 'cachePaths',
        get label() { return t('pipelineJob.fieldCachePathsLabel') },
        kind: 'textarea',
        monospace: true,
        placeholder: '{{dir}}/node_modules',
        get hint() { return t('pipelineJob.fieldTemplatedCachePathsHint') },
      },
      {
        key: 'cacheKey',
        get label() { return t('pipelineJob.fieldCacheKeyLabel') },
        kind: 'text',
        monospace: true,
        get placeholder() { return t('pipelineJob.fieldCacheKeyPlaceholder') },
        get hint() { return t('pipelineJob.fieldTemplatedCacheKeyHint') },
      },
    ],
    defaultConfig: {
      image: 'node:20',
      params: 'dir=frontend\nbuildCmd=npm run build',
      commandTemplate: 'cd {{dir}}\nnpm install --no-audit --no-fund\n{{buildCmd}}',
      artifactPath: '{{dir}}/dist',
    },
  },

  script: {
    type: 'script',
    get label() { return t('pipelineJob.typeScriptLabel') },
    get description() { return t('pipelineJob.typeScriptDesc') },
    accent: 'primary',
    category: 'custom',
    fields: SCRIPT_FIELDS,
  },

  custom: {
    type: 'custom',
    get label() { return t('pipelineJob.typeScriptLabel') },
    get description() { return t('pipelineJob.typeScriptDesc') },
    accent: 'primary',
    category: 'custom',
    fields: SCRIPT_FIELDS,
  },
}

// ─── Lookups ──────────────────────────────────────────────────────────────────

/** Canonical, ordered list of pickable types (excludes the `custom` alias of `script`). */
export const PICKABLE_TYPES: readonly string[] = [
  'git_source',
  'build_frontend',
  'build_backend',
  'build_image',
  'push_image',
  'deploy_frontend',
  'deploy_ssh',
  'health_check',
  'notify',
  'script',
  'templated',
]

/** Dropdown options for the job type selector (friendly label + token). */
export const JOB_TYPE_OPTIONS: SelectOption[] = PICKABLE_TYPES.map((type) => ({
  value: type,
  get label() {
    return `${JOB_TYPE_SPECS[type].label} · ${type}`
  },
}))

/** Picker categories in display order. */
export const JOB_CATEGORIES: ReadonlyArray<{ id: CategoryId; label: string }> = [
  { id: 'source', get label() { return t('pipelineJob.categorySource') } },
  { id: 'build', get label() { return t('pipelineJob.categoryBuild') } },
  { id: 'deploy', get label() { return t('pipelineJob.categoryDeploy') } },
  { id: 'quality', get label() { return t('pipelineJob.categoryQuality') } },
  { id: 'notify', get label() { return t('pipelineJob.categoryNotify') } },
  { id: 'custom', get label() { return t('pipelineJob.categoryCustom') } },
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

/**
 * 「脚本类」节点:后端 `isScriptJob`(internal/build/dag_stage_exec.go)按 script 路径执行
 * (容器跑 + 收 artifactPath 产物)的那批 type。可视化步骤构建器只对这些 type 出现,
 * 因为它编译/反解析的就是这套 commands/artifactPath 键。与后端保持一致。
 */
const SCRIPT_CLASS_TYPES = new Set<string>([
  'script',
  'custom',
  'build_frontend',
  'build_backend',
  'templated',
])

export function isScriptClassType(type: string): boolean {
  return SCRIPT_CLASS_TYPES.has(type.trim())
}
