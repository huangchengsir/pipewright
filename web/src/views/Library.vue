<script setup lang="ts">
/**
 * 复用库(Library)— FR-8-13:流水线模板 + 变量组。
 *
 * 两类跨项目可复用资产,合并在一个带分段切换的管理界面:
 *   - 模板(Templates):命名、可复用的流水线定义;在流水线编辑器内「应用到项目」。
 *   - 变量组(Variable Groups):命名、可复用的变量集合(key=value + secret 引用)。
 *
 * secret 变量绝无明文:仅存/回 credentialId + maskedValue。HttpError 形状 err.apiError?.code/.message/.status。
 */
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { useMessage } from 'naive-ui'
import { HttpError } from '../api/http'
import {
  listTemplates,
  deleteTemplate,
  type TemplateSummary,
} from '../api/templates'
import {
  listVariableGroups,
  createVariableGroup,
  updateVariableGroup,
  deleteVariableGroup,
  type VariableGroup,
  type VariableGroupVarInput,
} from '../api/variableGroups'
import { listCredentials, type Credential } from '../api/credentials'
import {
  listCustomNodes,
  updateCustomNode,
  deleteCustomNode,
  type CustomNode,
} from '../api/customNodes'
import { jobTypeLabel } from '../components/pipeline/jobConfigSchema'
import { isStudioNode } from '../components/pipeline/studioCompile'

type Tab = 'templates' | 'variableGroups' | 'customNodes'
type LoadState = 'idle' | 'loading' | 'error'

const { t } = useI18n()
const message = useMessage()
const route = useRoute()
const router = useRouter()
const tab = ref<Tab>(route.query.tab === 'customNodes' ? 'customNodes' : route.query.tab === 'variableGroups' ? 'variableGroups' : 'templates')

// ─── templates ────────────────────────────────────────────────────────────────
const tplState = ref<LoadState>('idle')
const tplError = ref('')
const templates = ref<TemplateSummary[]>([])

async function loadTemplates(): Promise<void> {
  tplState.value = 'loading'
  tplError.value = ''
  try {
    templates.value = await listTemplates()
    tplState.value = 'idle'
  } catch (err: unknown) {
    tplState.value = 'error'
    tplError.value = err instanceof HttpError
      ? err.apiError?.message ?? t('library.loadTemplatesFailedStatus', { status: err.status })
      : t('library.loadTemplatesFailed')
  }
}

async function removeTemplate(ts: TemplateSummary): Promise<void> {
  if (!window.confirm(t('library.confirmDeleteTemplate', { name: ts.name }))) return
  try {
    await deleteTemplate(ts.id)
    message.success(t('library.deletedTemplate', { name: ts.name }))
    await loadTemplates()
  } catch (err: unknown) {
    message.error(err instanceof HttpError ? err.apiError?.message ?? t('library.deleteFailed') : t('library.deleteFailed'))
  }
}

// ─── variable groups ───────────────────────────────────────────────────────────
const vgState = ref<LoadState>('idle')
const vgError = ref('')
const variableGroups = ref<VariableGroup[]>([])
const credentials = ref<Credential[]>([])

async function loadVariableGroups(): Promise<void> {
  vgState.value = 'loading'
  vgError.value = ''
  try {
    const [groups, creds] = await Promise.all([listVariableGroups(), loadCredentialsSafe()])
    variableGroups.value = groups
    credentials.value = creds
    vgState.value = 'idle'
  } catch (err: unknown) {
    vgState.value = 'error'
    vgError.value = err instanceof HttpError
      ? err.apiError?.message ?? t('library.loadGroupsFailedStatus', { status: err.status })
      : t('library.loadGroupsFailed')
  }
}

/** 凭据列表用于 secret 变量挑选;保险库未配置不阻断变量组加载。 */
async function loadCredentialsSafe(): Promise<Credential[]> {
  try {
    return await listCredentials()
  } catch {
    return []
  }
}

// ─── variable-group editor modal ────────────────────────────────────────────────
interface EditorVar {
  id?: string
  key: string
  secret: boolean
  value: string
  credentialId: string
  maskedValue: string
}

const editorOpen = ref(false)
const editorMode = ref<'create' | 'edit'>('create')
const editingId = ref<string | null>(null)
const editorName = ref('')
const editorDescription = ref('')
const editorVars = ref<EditorVar[]>([])
const editorBanner = ref('')
const editorSaving = ref(false)

function openCreateGroup(): void {
  editorMode.value = 'create'
  editingId.value = null
  editorName.value = ''
  editorDescription.value = ''
  editorVars.value = [{ key: '', secret: false, value: '', credentialId: '', maskedValue: '' }]
  editorBanner.value = ''
  editorOpen.value = true
}

function openEditGroup(g: VariableGroup): void {
  editorMode.value = 'edit'
  editingId.value = g.id
  editorName.value = g.name
  editorDescription.value = g.description
  editorVars.value = g.vars.map((v) => ({
    id: v.id,
    key: v.key,
    secret: v.secret,
    value: v.value ?? '',
    credentialId: v.credentialId ?? '',
    maskedValue: v.maskedValue ?? '',
  }))
  if (editorVars.value.length === 0) {
    editorVars.value = [{ key: '', secret: false, value: '', credentialId: '', maskedValue: '' }]
  }
  editorBanner.value = ''
  editorOpen.value = true
}

function addEditorVar(): void {
  editorVars.value.push({ key: '', secret: false, value: '', credentialId: '', maskedValue: '' })
}

function removeEditorVar(idx: number): void {
  editorVars.value.splice(idx, 1)
}

function toggleSecret(v: EditorVar): void {
  v.secret = !v.secret
  // 切到 secret 时清明文;切回明文时清引用(避免泄漏/悬挂)。
  if (v.secret) v.value = ''
  else {
    v.credentialId = ''
    v.maskedValue = ''
  }
}

const editorTitle = computed(() => (editorMode.value === 'create' ? t('library.createGroup') : t('library.editGroup')))

async function saveGroup(): Promise<void> {
  editorBanner.value = ''
  const name = editorName.value.trim()
  if (!name) {
    editorBanner.value = t('library.groupNameRequired')
    return
  }
  // 仅提交 key 非空的变量;secret 项只传 credentialId(不传明文)。
  const vars: VariableGroupVarInput[] = editorVars.value
    .filter((v) => v.key.trim() !== '')
    .map((v) =>
      v.secret
        ? { id: v.id, key: v.key.trim(), secret: true, credentialId: v.credentialId }
        : { id: v.id, key: v.key.trim(), secret: false, value: v.value },
    )

  editorSaving.value = true
  try {
    const payload = { name, description: editorDescription.value.trim(), vars }
    if (editorMode.value === 'create') {
      await createVariableGroup(payload)
      message.success(t('library.createdGroup', { name }))
    } else if (editingId.value) {
      await updateVariableGroup(editingId.value, payload)
      message.success(t('library.updatedGroup', { name }))
    }
    editorOpen.value = false
    await loadVariableGroups()
  } catch (err: unknown) {
    editorBanner.value = err instanceof HttpError
      ? err.apiError?.message ?? t('library.saveFailedStatus', { status: err.status })
      : t('library.saveFailed')
  } finally {
    editorSaving.value = false
  }
}

async function removeGroup(g: VariableGroup): Promise<void> {
  if (!window.confirm(t('library.confirmDeleteGroup', { name: g.name }))) return
  try {
    await deleteVariableGroup(g.id)
    message.success(t('library.deletedGroup', { name: g.name }))
    await loadVariableGroups()
  } catch (err: unknown) {
    message.error(err instanceof HttpError ? err.apiError?.message ?? t('library.deleteFailed') : t('library.deleteFailed'))
  }
}

// ─── custom nodes(复用库 Tier 2) ──────────────────────────────────────────────
const cnState = ref<LoadState>('idle')
const cnError = ref('')
const customNodes = ref<CustomNode[]>([])

async function loadCustomNodes(): Promise<void> {
  cnState.value = 'loading'
  cnError.value = ''
  try {
    customNodes.value = await listCustomNodes()
    cnState.value = 'idle'
  } catch (err: unknown) {
    cnState.value = 'error'
    cnError.value = err instanceof HttpError
      ? err.apiError?.message ?? t('library.loadNodesFailedStatus', { status: err.status })
      : t('library.loadNodesFailed')
  }
}

async function removeCustomNode(n: CustomNode): Promise<void> {
  if (!window.confirm(t('library.confirmDeleteNode', { name: n.name }))) return
  try {
    await deleteCustomNode(n.id)
    message.success(t('library.deletedNode', { name: n.name }))
    await loadCustomNodes()
  } catch (err: unknown) {
    message.error(err instanceof HttpError ? err.apiError?.message ?? t('library.deleteFailed') : t('library.deleteFailed'))
  }
}

/** config 快照前若干键,供卡片预览(纯展示,不含 secret 明文)。 */
function configPreview(n: CustomNode): Array<{ k: string; v: string }> {
  return Object.entries(n.config ?? {})
    .slice(0, 4)
    .map(([k, v]) => ({ k, v: typeof v === 'string' ? v : JSON.stringify(v) }))
}

// ─── custom-node editor modal ────────────────────────────────────────────────
// config 是自由 KV(用户铁律:参数不限死);用普通 KV 行编辑,无 secret 概念。
// 值原样为字符串则原样回填/回存;原为非字符串(数字/对象等)则以 JSON 文本编辑,
// 保存时尝试 JSON 还原,无法解析则当字符串存。
interface CnEditorRow {
  key: string
  value: string
}

const cnEditorOpen = ref(false)
const cnEditingId = ref<string | null>(null)
const cnEditorName = ref('')
const cnEditorDescription = ref('')
const cnEditorSummary = ref('')
const cnEditorNodeType = ref('')
const cnEditorRows = ref<CnEditorRow[]>([])
const cnEditorBanner = ref('')
const cnEditorSaving = ref(false)

const cnEditorTypeLabel = computed(() => jobTypeLabel(cnEditorNodeType.value))

// ─── 自定义节点工作室(独立低代码页 → templated 节点)──────────────────────────────
function openCreateStudio(): void {
  router.push({ name: 'studio-create' })
}

/** 工作室节点(templated 或带 __studio)走独立工作室页;其余类型走原始 KV 编辑器。 */
function openEditCustomNode(n: CustomNode): void {
  if (isStudioNode(n.nodeType, n.config ?? {})) {
    router.push({ name: 'studio-edit', params: { id: n.id } })
    return
  }
  cnEditingId.value = n.id
  cnEditorName.value = n.name
  cnEditorDescription.value = n.description
  cnEditorSummary.value = n.summary
  cnEditorNodeType.value = n.nodeType
  cnEditorRows.value = Object.entries(n.config ?? {}).map(([k, v]) => ({
    key: k,
    value: typeof v === 'string' ? v : JSON.stringify(v),
  }))
  cnEditorBanner.value = ''
  cnEditorOpen.value = true
}

function addCnRow(): void {
  cnEditorRows.value.push({ key: '', value: '' })
}

function removeCnRow(idx: number): void {
  cnEditorRows.value.splice(idx, 1)
}

/** 把一行的字符串值尽量还原为原始 JSON 类型(数字/布尔/对象/数组),失败则按字符串存。 */
function parseCnValue(raw: string): unknown {
  const trimmed = raw.trim()
  if (trimmed === '') return raw
  if (
    trimmed === 'true' ||
    trimmed === 'false' ||
    trimmed === 'null' ||
    /^-?\d+(\.\d+)?$/.test(trimmed) ||
    trimmed.startsWith('{') ||
    trimmed.startsWith('[')
  ) {
    try {
      return JSON.parse(trimmed)
    } catch {
      return raw
    }
  }
  return raw
}

async function saveCustomNode(): Promise<void> {
  cnEditorBanner.value = ''
  const name = cnEditorName.value.trim()
  if (!name) {
    cnEditorBanner.value = t('library.nodeNameRequired')
    return
  }
  if (!cnEditingId.value) return

  // 仅提交 key 非空的参数;后写覆盖同名,保留用户填写顺序。
  const config: Record<string, unknown> = {}
  for (const row of cnEditorRows.value) {
    const k = row.key.trim()
    if (k === '') continue
    config[k] = parseCnValue(row.value)
  }

  cnEditorSaving.value = true
  try {
    await updateCustomNode(cnEditingId.value, {
      name,
      description: cnEditorDescription.value.trim(),
      nodeType: cnEditorNodeType.value,
      summary: cnEditorSummary.value.trim(),
      config,
    })
    message.success(t('library.updatedNode', { name }))
    cnEditorOpen.value = false
    await loadCustomNodes()
  } catch (err: unknown) {
    cnEditorBanner.value = err instanceof HttpError
      ? err.apiError?.message ?? t('library.saveFailedStatus', { status: err.status })
      : t('library.saveFailed')
  } finally {
    cnEditorSaving.value = false
  }
}

function switchTab(next: Tab): void {
  tab.value = next
  if (next === 'templates' && templates.value.length === 0 && tplState.value !== 'error') loadTemplates()
  if (next === 'variableGroups' && variableGroups.value.length === 0 && vgState.value !== 'error') loadVariableGroups()
  if (next === 'customNodes' && customNodes.value.length === 0 && cnState.value !== 'error') loadCustomNodes()
}

onMounted(() => {
  loadTemplates()
  loadVariableGroups()
  loadCustomNodes()
})
</script>

<template>
  <section class="library">
    <header class="page-header">
      <div>
        <h1 class="page-title">{{ t('library.title') }}</h1>
        <p class="page-sub">{{ t('library.subtitle') }}</p>
      </div>
      <button
        v-if="tab === 'variableGroups'"
        class="btn-primary"
        @click="openCreateGroup"
      >
        {{ t('library.newGroup') }}
      </button>
      <button
        v-if="tab === 'customNodes'"
        class="btn-primary"
        @click="openCreateStudio"
      >
        {{ t('library.newStudioNode') }}
      </button>
    </header>

    <!-- Segmented tab switch -->
    <div class="seg" role="tablist" :aria-label="t('library.segAria')">
      <button
        class="seg-item"
        role="tab"
        :aria-selected="tab === 'templates'"
        :class="{ 'seg-item--active': tab === 'templates' }"
        @click="switchTab('templates')"
      >
        {{ t('library.tabTemplates') }}
        <span class="seg-count">{{ templates.length }}</span>
      </button>
      <button
        class="seg-item"
        role="tab"
        :aria-selected="tab === 'variableGroups'"
        :class="{ 'seg-item--active': tab === 'variableGroups' }"
        @click="switchTab('variableGroups')"
      >
        {{ t('library.tabVariableGroups') }}
        <span class="seg-count">{{ variableGroups.length }}</span>
      </button>
      <button
        class="seg-item"
        role="tab"
        :aria-selected="tab === 'customNodes'"
        :class="{ 'seg-item--active': tab === 'customNodes' }"
        @click="switchTab('customNodes')"
      >
        {{ t('library.tabCustomNodes') }}
        <span class="seg-count">{{ customNodes.length }}</span>
      </button>
    </div>

    <!-- ─── Templates ─── -->
    <div v-show="tab === 'templates'" class="panel">
      <div v-if="tplState === 'error'" class="banner banner--error">
        <span>{{ tplError }}</span>
        <button class="banner-retry" @click="loadTemplates">↻ {{ t('library.retry') }}</button>
      </div>

      <div v-if="tplState === 'loading'" class="skeleton-grid">
        <div v-for="i in 3" :key="i" class="skeleton-card" />
      </div>

      <div
        v-else-if="tplState === 'idle' && templates.length === 0"
        class="empty-state"
      >
        <div class="empty-icon">▦</div>
        <p class="empty-label">{{ t('library.emptyTemplatesTitle') }}</p>
        <p class="empty-hint">{{ t('library.emptyTemplatesHint') }}</p>
      </div>

      <ul v-else class="card-grid" role="list">
        <li v-for="tpl in templates" :key="tpl.id" class="tpl-card">
          <div class="tpl-card-head">
            <h3 class="tpl-name">{{ tpl.name }}</h3>
            <span class="tpl-stage-pill">{{ t('library.stageCount', { n: tpl.stageCount }) }}</span>
          </div>
          <p class="tpl-desc">{{ tpl.description || t('library.noDescription') }}</p>
          <div class="tpl-card-foot">
            <span class="tpl-time">{{ t('library.updatedAt', { time: new Date(tpl.updatedAt).toLocaleDateString() }) }}</span>
            <button class="link-danger" @click="removeTemplate(tpl)">{{ t('library.delete') }}</button>
          </div>
        </li>
      </ul>
    </div>

    <!-- ─── Variable Groups ─── -->
    <div v-show="tab === 'variableGroups'" class="panel">
      <div v-if="vgState === 'error'" class="banner banner--error">
        <span>{{ vgError }}</span>
        <button class="banner-retry" @click="loadVariableGroups">↻ {{ t('library.retry') }}</button>
      </div>

      <div v-if="vgState === 'loading'" class="skeleton-grid">
        <div v-for="i in 3" :key="i" class="skeleton-card" />
      </div>

      <div
        v-else-if="vgState === 'idle' && variableGroups.length === 0"
        class="empty-state"
      >
        <div class="empty-icon">{ }</div>
        <p class="empty-label">{{ t('library.emptyGroupsTitle') }}</p>
        <p class="empty-hint">{{ t('library.emptyGroupsHint') }}</p>
        <button class="btn-primary" @click="openCreateGroup">{{ t('library.newGroup') }}</button>
      </div>

      <ul v-else class="card-grid" role="list">
        <li v-for="g in variableGroups" :key="g.id" class="vg-card">
          <div class="vg-card-head">
            <h3 class="vg-name">{{ g.name }}</h3>
            <span class="vg-count-pill">{{ t('library.varCount', { n: g.vars.length }) }}</span>
          </div>
          <p class="vg-desc">{{ g.description || t('library.noDescription') }}</p>
          <ul class="vg-var-list" role="list">
            <li v-for="v in g.vars.slice(0, 5)" :key="v.id" class="vg-var">
              <code class="vg-var-key">{{ v.key }}</code>
              <span v-if="v.secret" class="vg-var-secret" :title="t('library.secretRefTitle')">
                🔒 {{ v.maskedValue || '••••' }}
              </span>
              <span v-else class="vg-var-value">{{ v.value || t('library.emptyValue') }}</span>
            </li>
            <li v-if="g.vars.length > 5" class="vg-var vg-var--more">
              {{ t('library.moreVars', { n: g.vars.length - 5 }) }}
            </li>
            <li v-if="g.vars.length === 0" class="vg-var vg-var--more">{{ t('library.noVars') }}</li>
          </ul>
          <div class="vg-card-foot">
            <button class="btn-secondary btn-sm" @click="openEditGroup(g)">{{ t('library.edit') }}</button>
            <button class="link-danger" @click="removeGroup(g)">{{ t('library.delete') }}</button>
          </div>
        </li>
      </ul>
    </div>

    <!-- ─── Custom Nodes(复用库 Tier 2) ─── -->
    <div v-show="tab === 'customNodes'" class="panel">
      <div v-if="cnState === 'error'" class="banner banner--error">
        <span>{{ cnError }}</span>
        <button class="banner-retry" @click="loadCustomNodes">↻ {{ t('library.retry') }}</button>
      </div>

      <div v-if="cnState === 'loading'" class="skeleton-grid">
        <div v-for="i in 3" :key="i" class="skeleton-card" />
      </div>

      <div
        v-else-if="cnState === 'idle' && customNodes.length === 0"
        class="empty-state"
      >
        <div class="empty-icon">◇</div>
        <p class="empty-label">{{ t('library.emptyNodesTitle') }}</p>
        <p class="empty-hint">{{ t('library.emptyNodesHint') }}</p>
        <button class="btn-primary" @click="openCreateStudio">{{ t('library.newStudioNode') }}</button>
      </div>

      <ul v-else class="card-grid" role="list">
        <li v-for="n in customNodes" :key="n.id" class="cn-card">
          <div class="cn-card-head">
            <h3 class="cn-name">{{ n.name }}</h3>
            <span class="cn-type-pill">{{ jobTypeLabel(n.nodeType) }}</span>
          </div>
          <p class="cn-desc">{{ n.description || n.summary || t('library.noDescription') }}</p>
          <ul class="cn-cfg-list" role="list">
            <li v-for="item in configPreview(n)" :key="item.k" class="cn-cfg">
              <code class="cn-cfg-key">{{ item.k }}</code>
              <span class="cn-cfg-val">{{ item.v || t('library.emptyValue') }}</span>
            </li>
            <li v-if="Object.keys(n.config ?? {}).length > 4" class="cn-cfg cn-cfg--more">
              {{ t('library.moreParams', { n: Object.keys(n.config).length - 4 }) }}
            </li>
            <li v-if="Object.keys(n.config ?? {}).length === 0" class="cn-cfg cn-cfg--more">{{ t('library.noParams') }}</li>
          </ul>
          <div class="cn-card-foot">
            <span class="cn-time">{{ t('library.updatedAt', { time: new Date(n.updatedAt).toLocaleDateString() }) }}</span>
            <div class="cn-card-actions">
              <button class="btn-secondary btn-sm" @click="openEditCustomNode(n)">{{ t('library.edit') }}</button>
              <button class="link-danger" @click="removeCustomNode(n)">{{ t('library.delete') }}</button>
            </div>
          </div>
        </li>
      </ul>
    </div>

    <!-- ─── Variable-group editor modal ─── -->
    <div v-if="editorOpen" class="modal-scrim" @click.self="editorOpen = false">
      <div class="modal" role="dialog" aria-modal="true" :aria-label="editorTitle">
        <header class="modal-head">
          <h2 class="modal-title">{{ editorTitle }}</h2>
          <button class="modal-close" :aria-label="t('library.close')" @click="editorOpen = false">✕</button>
        </header>

        <div v-if="editorBanner" class="banner banner--error">{{ editorBanner }}</div>

        <div class="field">
          <label class="field-label" for="vg-name">{{ t('library.fieldName') }}</label>
          <input
            id="vg-name"
            v-model="editorName"
            class="input"
            :placeholder="t('library.groupNamePlaceholder')"
            autocomplete="off"
          />
        </div>
        <div class="field">
          <label class="field-label" for="vg-desc">{{ t('library.fieldDescriptionOptional') }}</label>
          <input
            id="vg-desc"
            v-model="editorDescription"
            class="input"
            :placeholder="t('library.groupDescPlaceholder')"
            autocomplete="off"
          />
        </div>

        <div class="field">
          <div class="field-label-row">
            <span class="field-label">{{ t('library.fieldVariables') }}</span>
            <button class="link-add" @click="addEditorVar">{{ t('library.addVariable') }}</button>
          </div>
          <div class="var-rows">
            <div v-for="(v, idx) in editorVars" :key="idx" class="var-row">
              <input
                v-model="v.key"
                class="input input--key"
                placeholder="KEY"
                autocomplete="off"
              />
              <template v-if="v.secret">
                <select v-model="v.credentialId" class="input input--cred">
                  <option value="">{{ t('library.selectCredential') }}</option>
                  <option v-for="c in credentials" :key="c.id" :value="c.id">
                    {{ c.name }} ({{ c.maskedValue }})
                  </option>
                </select>
              </template>
              <template v-else>
                <input
                  v-model="v.value"
                  class="input input--val"
                  placeholder="value"
                  autocomplete="off"
                />
              </template>
              <button
                class="var-secret-toggle"
                :class="{ 'var-secret-toggle--on': v.secret }"
                :title="v.secret ? t('library.secretToggleOn') : t('library.secretToggleOff')"
                @click="toggleSecret(v)"
              >
                {{ v.secret ? '🔒' : '🔓' }}
              </button>
              <button class="var-remove" :title="t('library.remove')" @click="removeEditorVar(idx)">✕</button>
            </div>
          </div>
        </div>

        <footer class="modal-foot">
          <button class="btn-secondary" :disabled="editorSaving" @click="editorOpen = false">
            {{ t('library.cancel') }}
          </button>
          <button class="btn-primary" :disabled="editorSaving" @click="saveGroup">
            {{ editorSaving ? t('library.saving') : t('library.save') }}
          </button>
        </footer>
      </div>
    </div>

    <!-- ─── Custom-node editor modal ─── -->
    <div v-if="cnEditorOpen" class="modal-scrim" @click.self="cnEditorOpen = false">
      <div class="modal" role="dialog" aria-modal="true" :aria-label="t('library.editNode')">
        <header class="modal-head">
          <h2 class="modal-title">{{ t('library.editNode') }}</h2>
          <button class="modal-close" :aria-label="t('library.close')" @click="cnEditorOpen = false">✕</button>
        </header>

        <div v-if="cnEditorBanner" class="banner banner--error">{{ cnEditorBanner }}</div>

        <div class="field">
          <label class="field-label" for="cn-name">{{ t('library.fieldName') }}</label>
          <input
            id="cn-name"
            v-model="cnEditorName"
            class="input"
            :placeholder="t('library.nodeNamePlaceholder')"
            autocomplete="off"
          />
        </div>
        <div class="field">
          <label class="field-label" for="cn-desc">{{ t('library.fieldDescriptionOptional') }}</label>
          <input
            id="cn-desc"
            v-model="cnEditorDescription"
            class="input"
            :placeholder="t('library.nodeDescPlaceholder')"
            autocomplete="off"
          />
        </div>
        <div class="field">
          <label class="field-label" for="cn-summary">{{ t('library.fieldSummaryOptional') }}</label>
          <input
            id="cn-summary"
            v-model="cnEditorSummary"
            class="input"
            :placeholder="t('library.nodeSummaryPlaceholder')"
            autocomplete="off"
          />
        </div>
        <div class="field">
          <span class="field-label">{{ t('library.fieldUnderlyingType') }}</span>
          <div class="cn-type-readonly">
            <span class="cn-type-pill">{{ cnEditorTypeLabel }}</span>
            <span class="cn-type-hint">{{ t('library.underlyingTypeHint') }}</span>
          </div>
        </div>

        <div class="field">
          <div class="field-label-row">
            <span class="field-label">{{ t('library.fieldParams') }}</span>
            <button class="link-add" @click="addCnRow">{{ t('library.addParam') }}</button>
          </div>
          <div class="var-rows">
            <div v-for="(row, idx) in cnEditorRows" :key="idx" class="var-row">
              <input
                v-model="row.key"
                class="input input--key"
                placeholder="KEY"
                autocomplete="off"
              />
              <input
                v-model="row.value"
                class="input input--val"
                placeholder="value"
                autocomplete="off"
              />
              <button class="var-remove" :title="t('library.remove')" @click="removeCnRow(idx)">✕</button>
            </div>
            <p v-if="cnEditorRows.length === 0" class="cn-rows-empty">
              {{ t('library.noParamsHint') }}
            </p>
          </div>
        </div>

        <footer class="modal-foot">
          <button class="btn-secondary" :disabled="cnEditorSaving" @click="cnEditorOpen = false">
            {{ t('library.cancel') }}
          </button>
          <button class="btn-primary" :disabled="cnEditorSaving" @click="saveCustomNode">
            {{ cnEditorSaving ? t('library.saving') : t('library.save') }}
          </button>
        </footer>
      </div>
    </div>
  </section>
</template>

<style scoped>
.library {
  max-width: 1120px;
  margin: 0 auto;
  padding: clamp(1.5rem, 1rem + 2vw, 3rem) clamp(1rem, 0.5rem + 2vw, 2.5rem);
}

.page-header {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: 1rem;
  margin-bottom: 1.5rem;
}
.page-title {
  font-size: clamp(1.5rem, 1.2rem + 1.4vw, 2.1rem);
  font-weight: 680;
  letter-spacing: -0.02em;
  margin: 0;
  color: var(--color-text);
}
.page-sub {
  margin: 0.35rem 0 0;
  color: var(--color-dim);
  font-size: 0.92rem;
}

/* Segmented control — intentional active state with sliding underline feel */
.seg {
  display: inline-flex;
  gap: 0.25rem;
  padding: 0.25rem;
  background: var(--color-inset);
  border: 1px solid var(--color-border);
  border-radius: 12px;
  margin-bottom: 1.75rem;
}
.seg-item {
  display: inline-flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.5rem 1rem;
  border: none;
  background: transparent;
  color: var(--color-dim);
  font-size: 0.9rem;
  font-weight: 560;
  border-radius: 9px;
  cursor: pointer;
  transition: color var(--duration-fast, 150ms), background var(--duration-fast, 150ms);
}
.seg-item:hover {
  color: var(--color-text);
}
.seg-item--active {
  background: var(--color-card);
  color: var(--color-text);
  box-shadow: var(--shadow);
}
.seg-count {
  display: inline-grid;
  place-items: center;
  min-width: 1.4rem;
  height: 1.4rem;
  padding: 0 0.4rem;
  font-size: 0.72rem;
  font-weight: 640;
  color: var(--color-faint);
  background: var(--color-border);
  border-radius: 999px;
}
.seg-item--active .seg-count {
  color: var(--color-primary);
  background: var(--color-primary-soft);
}

.panel { min-height: 240px; }

/* Card grid — bento-ish, depth via surface + shadow + hover lift */
.card-grid {
  list-style: none;
  margin: 0;
  padding: 0;
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(290px, 1fr));
  gap: 1rem;
}

.tpl-card,
.vg-card,
.cn-card {
  display: flex;
  flex-direction: column;
  gap: 0.7rem;
  padding: 1.15rem 1.25rem;
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: 16px;
  box-shadow: var(--shadow);
  transition: transform var(--duration-fast, 150ms), border-color var(--duration-fast, 150ms);
}
.tpl-card:hover,
.vg-card:hover,
.cn-card:hover {
  transform: translateY(-2px);
  border-color: var(--color-border-strong);
}

.tpl-card-head,
.vg-card-head,
.cn-card-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
}
.tpl-name,
.vg-name,
.cn-name {
  margin: 0;
  font-size: 1.05rem;
  font-weight: 640;
  letter-spacing: -0.01em;
  color: var(--color-text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.tpl-stage-pill,
.vg-count-pill,
.cn-type-pill {
  flex-shrink: 0;
  font-size: 0.72rem;
  font-weight: 600;
  padding: 0.18rem 0.55rem;
  border-radius: 999px;
  color: var(--color-primary);
  background: var(--color-primary-soft);
}
.tpl-desc,
.vg-desc,
.cn-desc {
  margin: 0;
  font-size: 0.86rem;
  color: var(--color-dim);
  line-height: 1.45;
  min-height: 1.2em;
}

/* Custom-node config preview — mirrors the variable-group var list */
.cn-cfg-list {
  list-style: none;
  margin: 0;
  padding: 0.6rem 0 0;
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
  border-top: 1px solid var(--color-border);
}
.cn-cfg {
  display: flex;
  align-items: center;
  gap: 0.6rem;
  font-size: 0.82rem;
  min-width: 0;
}
.cn-cfg-key {
  flex-shrink: 0;
  font-family: var(--font-mono, ui-monospace, monospace);
  color: var(--color-text);
  background: var(--color-inset);
  padding: 0.1rem 0.4rem;
  border-radius: 6px;
}
.cn-cfg-val {
  color: var(--color-dim);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.cn-cfg--more {
  color: var(--color-faint);
  font-style: italic;
}
.cn-card-foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
  margin-top: auto;
  padding-top: 0.4rem;
}
.cn-time {
  font-size: 0.76rem;
  color: var(--color-faint);
}
.cn-card-actions {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}

/* Custom-node editor — read-only type display + empty-params hint */
.cn-type-readonly {
  display: flex;
  align-items: center;
  gap: 0.6rem;
}
.cn-type-hint {
  font-size: 0.78rem;
  color: var(--color-faint);
}
.cn-rows-empty {
  margin: 0;
  font-size: 0.82rem;
  color: var(--color-faint);
  font-style: italic;
}

.vg-var-list {
  list-style: none;
  margin: 0;
  padding: 0.6rem 0 0;
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
  border-top: 1px solid var(--color-border);
}
.vg-var {
  display: flex;
  align-items: center;
  gap: 0.6rem;
  font-size: 0.82rem;
}
.vg-var-key {
  font-family: var(--font-mono, ui-monospace, monospace);
  color: var(--color-text);
  background: var(--color-inset);
  padding: 0.1rem 0.4rem;
  border-radius: 6px;
}
.vg-var-value {
  color: var(--color-dim);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.vg-var-secret {
  color: var(--color-amber);
  font-family: var(--font-mono, ui-monospace, monospace);
  font-size: 0.78rem;
}
.vg-var--more {
  color: var(--color-faint);
  font-style: italic;
}

.tpl-card-foot,
.vg-card-foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
  margin-top: auto;
  padding-top: 0.4rem;
}
.tpl-time {
  font-size: 0.76rem;
  color: var(--color-faint);
}

/* Buttons */
.btn-primary {
  padding: 0.55rem 1.1rem;
  background: var(--color-primary);
  color: oklch(100% 0 0);
  border: none;
  border-radius: 10px;
  font-weight: 600;
  font-size: 0.9rem;
  cursor: pointer;
  transition: background var(--duration-fast, 150ms), transform var(--duration-fast, 150ms);
}
.btn-primary:hover:not(:disabled) { background: var(--color-primary-press); transform: translateY(-1px); }
.btn-primary:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 2px; }
.btn-primary:disabled { opacity: 0.55; cursor: not-allowed; }

.btn-secondary {
  padding: 0.55rem 1.1rem;
  background: var(--color-card-2);
  color: var(--color-text);
  border: 1px solid var(--color-border-strong);
  border-radius: 10px;
  font-weight: 560;
  font-size: 0.9rem;
  cursor: pointer;
  transition: border-color var(--duration-fast, 150ms), background var(--duration-fast, 150ms);
}
.btn-secondary:hover:not(:disabled) { border-color: var(--color-primary); }
.btn-secondary:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 2px; }
.btn-secondary:disabled { opacity: 0.55; cursor: not-allowed; }
.btn-sm { padding: 0.4rem 0.85rem; font-size: 0.84rem; }

.link-danger {
  border: none;
  background: none;
  color: var(--color-red);
  font-size: 0.82rem;
  font-weight: 560;
  cursor: pointer;
  padding: 0.2rem 0.3rem;
  border-radius: 6px;
}
.link-danger:hover { background: var(--color-red-soft); }
.link-danger:focus-visible { outline: 2px solid var(--color-red); outline-offset: 1px; }

.link-add {
  border: none;
  background: none;
  color: var(--color-primary);
  font-size: 0.82rem;
  font-weight: 580;
  cursor: pointer;
}
.link-add:hover { text-decoration: underline; }

/* Banner */
.banner {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  padding: 0.75rem 1rem;
  border-radius: 10px;
  font-size: 0.88rem;
  margin-bottom: 1rem;
}
.banner--error {
  color: var(--color-red);
  background: var(--color-red-soft);
  border: 1px solid var(--color-red-line);
}
.banner-retry {
  border: none;
  background: none;
  color: var(--color-red);
  font-weight: 600;
  cursor: pointer;
}

/* Empty state */
.empty-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  text-align: center;
  gap: 0.5rem;
  padding: 3.5rem 1rem;
}
.empty-icon {
  font-size: 2rem;
  color: var(--color-faint);
  opacity: 0.6;
}
.empty-label { margin: 0.3rem 0 0; font-size: 1.1rem; font-weight: 600; color: var(--color-text); }
.empty-hint { margin: 0; max-width: 30rem; color: var(--color-dim); font-size: 0.88rem; line-height: 1.55; }
.empty-state .btn-primary { margin-top: 0.8rem; }

/* Skeleton */
.skeleton-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(290px, 1fr));
  gap: 1rem;
}
.skeleton-card {
  height: 150px;
  border-radius: 16px;
  background: linear-gradient(100deg, var(--color-card) 30%, var(--color-card-2) 50%, var(--color-card) 70%);
  background-size: 200% 100%;
  animation: shimmer 1.4s ease-in-out infinite;
}
@keyframes shimmer {
  0% { background-position: 200% 0; }
  100% { background-position: -200% 0; }
}

/* Modal */
.modal-scrim {
  position: fixed;
  inset: 0;
  background: oklch(0% 0 0 / 0.55);
  display: grid;
  place-items: center;
  padding: 1rem;
  z-index: 50;
}
.modal {
  width: min(620px, 100%);
  max-height: 88vh;
  overflow-y: auto;
  background: var(--color-card);
  border: 1px solid var(--color-border-strong);
  border-radius: 18px;
  box-shadow: var(--shadow-modal);
  padding: 1.5rem;
}
.modal-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 1rem;
}
.modal-title { margin: 0; font-size: 1.2rem; font-weight: 640; color: var(--color-text); }
.modal-close {
  border: none;
  background: none;
  color: var(--color-dim);
  font-size: 1rem;
  cursor: pointer;
  width: 2rem;
  height: 2rem;
  border-radius: 8px;
}
.modal-close:hover { background: var(--color-inset); color: var(--color-text); }
.modal-foot {
  display: flex;
  justify-content: flex-end;
  gap: 0.6rem;
  margin-top: 1.25rem;
}

/* Fields */
.field { margin-bottom: 1rem; }
.field-label-row { display: flex; align-items: center; justify-content: space-between; margin-bottom: 0.4rem; }
.field-label { display: block; margin-bottom: 0.4rem; font-size: 0.84rem; font-weight: 560; color: var(--color-dim); }
.input {
  width: 100%;
  padding: 0.55rem 0.75rem;
  background: var(--color-inset);
  border: 1px solid var(--color-border);
  border-radius: 9px;
  color: var(--color-text);
  font-size: 0.9rem;
  transition: border-color var(--duration-fast, 150ms);
}
.input:focus { outline: none; border-color: var(--color-primary); }

.var-rows { display: flex; flex-direction: column; gap: 0.5rem; }
.var-row { display: flex; align-items: center; gap: 0.5rem; }
.input--key { flex: 0 0 32%; font-family: var(--font-mono, ui-monospace, monospace); }
.input--val,
.input--cred { flex: 1 1 auto; }
.var-secret-toggle {
  flex-shrink: 0;
  width: 2.2rem;
  height: 2.2rem;
  border: 1px solid var(--color-border);
  background: var(--color-inset);
  border-radius: 9px;
  cursor: pointer;
  font-size: 0.95rem;
}
.var-secret-toggle--on { border-color: var(--color-amber-line); background: var(--color-amber-soft); }
.var-remove {
  flex-shrink: 0;
  width: 2.2rem;
  height: 2.2rem;
  border: 1px solid var(--color-border);
  background: transparent;
  border-radius: 9px;
  color: var(--color-faint);
  cursor: pointer;
}
.var-remove:hover { color: var(--color-red); border-color: var(--color-red-line); }
</style>
