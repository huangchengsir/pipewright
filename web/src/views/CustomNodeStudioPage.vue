<script setup lang="ts">
/**
 * CustomNodeStudioPage — 复用库「自定义节点工作室」独立低代码页(对标 n8n / Node-RED Subflow)。
 *
 * 三栏 + 底部:左积木库(分组、可拖拽)→ 中步骤画布(拖拽组合 / 排序、按种类的字段)→
 * 右栏(提升参数 / 节点表面双 tab)→ 底部(实时编译 templated config + 实例预览卡)。
 *
 * 全程纯前端编译进**已有 templated 节点**(后端零改),__studio 私有键无损存步骤/参数元/表面。
 * 见 studioCompile.ts 的转换契约。本页是路由级聚焦编辑器(shell 外),进出经路由。
 */
import { ref, computed, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useMessage } from 'naive-ui'
import { HttpError } from '../api/http'
import {
  getCustomNode,
  createCustomNode,
  updateCustomNode,
} from '../api/customNodes'
import {
  type PromotedParam,
  type PromotedParamType,
  type StudioStep,
  type StudioStepKind,
  type NodeMeta,
  STEP_CATALOG,
  catalogItem,
  makeStep,
  compileStudioConfig,
  compileSteps,
  parseStudioConfig,
  emptyStudioModel,
} from '../components/pipeline/studioCompile'

const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const message = useMessage()

// ─── 表单态 ─────────────────────────────────────────────────────────────────────
const editId = computed<string | null>(() => (typeof route.params.id === 'string' ? route.params.id : null))

const name = ref('')
const description = ref('')
const image = ref('')
const params = ref<PromotedParam[]>([])
const steps = ref<StudioStep[]>([])
const meta = ref<NodeMeta>({ icon: '🔧', category: t('studio.defaultCategory'), summary: '' })

const tab = ref<'params' | 'meta'>('params')
const loading = ref(false)
const saving = ref(false)
const banner = ref('')

// 含字面 {{ }} 的示例文案放常量(避免模板里 {{ '{{x}}' }} 触发 vue 解析)。
const ex = computed(() => ({
  imgPlaceholder: t('studio.imgPlaceholder'),
  summaryPlaceholder: t('studio.summaryPlaceholder'),
  braceL: '{{',
  braceR: '}}',
}))

const PARAM_TYPE_OPTIONS = computed<ReadonlyArray<{ value: PromotedParamType; label: string }>>(() => [
  { value: 'text', label: t('studio.paramTypeText') },
  { value: 'select', label: t('studio.paramTypeSelect') },
  { value: 'number', label: t('studio.paramTypeNumber') },
  { value: 'toggle', label: t('studio.paramTypeToggle') },
])

// ─── 步骤字段布局(按种类;每行是并排字段组)──────────────────────────────────────
interface FieldDef {
  field: string
  label: string
  placeholder?: string
  multiline?: boolean
}
const STEP_FIELDS = computed<Record<StudioStepKind, FieldDef[][]>>(() => ({
  command: [[{ field: 'command', label: t('studio.fieldCommandMultiline'), multiline: true }]],
  install: [[{ field: 'command', label: t('studio.fieldInstallCommand'), placeholder: 'npm ci / pip install -r ...' }]],
  echo: [[{ field: 'command', label: t('studio.fieldEchoText'), placeholder: t('studio.phEchoText') }]],
  env: [[{ field: 'envKey', label: t('studio.fieldEnvKey') }, { field: 'envValue', label: t('studio.fieldEnvValue') }]],
  workDir: [[{ field: 'dir', label: t('studio.fieldTargetDir') }]],
  path: [[{ field: 'dir', label: t('studio.fieldPathDir'), placeholder: 'node_modules/.bin' }]],
  artifact: [[{ field: 'artifact', label: t('studio.fieldArtifactPath') }]],
  download: [[{ field: 'url', label: 'URL', placeholder: 'https://…' }, { field: 'out', label: t('studio.fieldSaveAs') }]],
  extract: [[{ field: 'file', label: t('studio.fieldArchiveFile'), placeholder: 'x.tar.gz' }, { field: 'dir', label: t('studio.fieldExtractTo') }]],
  condition: [[{ field: 'condition', label: t('studio.fieldCondition'), placeholder: 'test -f package.json' }]],
  retry: [[{ field: 'command', label: t('studio.fieldCommand'), placeholder: 'flaky-cmd' }], [{ field: 'count', label: t('studio.fieldRetryCount') }, { field: 'delay', label: t('studio.fieldDelaySecs') }]],
  timeout: [[{ field: 'command', label: t('studio.fieldCommand') }, { field: 'secs', label: t('studio.fieldTimeoutSecs') }]],
  sleep: [[{ field: 'secs', label: t('studio.fieldSleepSecs') }]],
  healthcheck: [[{ field: 'url', label: t('studio.fieldProbeUrl'), placeholder: 'http://127.0.0.1:8080/health' }, { field: 'delay', label: t('studio.fieldDelaySecs') }]],
  note: [[{ field: 'text', label: t('studio.fieldNote') }]],
  test: [[{ field: 'command', label: t('studio.fieldTestCommand') }], [{ field: 'reportPath', label: t('studio.fieldReportPath') }, { field: 'minCov', label: t('studio.fieldMinCoverage') }]],
}))

// ─── hydrate ───────────────────────────────────────────────────────────────────
async function hydrate(): Promise<void> {
  if (!editId.value) {
    const m = emptyStudioModel()
    name.value = ''
    description.value = ''
    image.value = m.image
    params.value = m.params
    steps.value = m.steps
    meta.value = m.meta
    return
  }
  loading.value = true
  banner.value = ''
  try {
    const node = await getCustomNode(editId.value)
    const m = parseStudioConfig(node.config ?? {})
    name.value = node.name
    description.value = node.description
    image.value = m.image
    params.value = m.params.map((p) => ({ ...p }))
    steps.value = m.steps
    meta.value = { ...m.meta }
    if (m.meta.summary === '' && node.summary) meta.value.summary = node.summary
  } catch (err: unknown) {
    banner.value = err instanceof HttpError ? err.apiError?.message ?? t('studio.loadFailedCode', { code: err.status }) : t('studio.loadFailed')
  } finally {
    loading.value = false
  }
}
onMounted(hydrate)

// ─── 步骤增删改 / 排序 ────────────────────────────────────────────────────────────
function addStep(kind: StudioStepKind, at?: number): void {
  const s = makeStep(kind)
  const next = [...steps.value]
  next.splice(at ?? next.length, 0, s)
  steps.value = next
}
function removeStep(id: number): void {
  steps.value = steps.value.filter((s) => s.id !== id)
}
function moveStep(id: number, dir: -1 | 1): void {
  const i = steps.value.findIndex((s) => s.id === id)
  const j = i + dir
  if (i < 0 || j < 0 || j >= steps.value.length) return
  const next = [...steps.value]
  ;[next[i], next[j]] = [next[j], next[i]]
  steps.value = next
}
function patchField(id: number, field: string, value: string): void {
  steps.value = steps.value.map((s) => (s.id === id ? { ...s, fields: { ...s.fields, [field]: value } } : s))
}

// ─── 拖拽(原生 HTML5 DnD)────────────────────────────────────────────────────────
type Drag = { type: 'new'; kind: StudioStepKind } | { type: 'move'; id: number } | null
const drag = ref<Drag>(null)
const dropIndex = ref<number | null>(null)

function onPaletteDragStart(kind: StudioStepKind): void {
  drag.value = { type: 'new', kind }
}
function onStepDragStart(id: number): void {
  drag.value = { type: 'move', id }
}
function onDragEnd(): void {
  drag.value = null
  dropIndex.value = null
}
function onDropLineOver(idx: number): void {
  if (drag.value) dropIndex.value = idx
}
function onComposeDrop(): void {
  if (!drag.value) return
  let idx = dropIndex.value ?? steps.value.length
  if (drag.value.type === 'new') {
    addStep(drag.value.kind, idx)
  } else {
    const from = steps.value.findIndex((s) => s.id === (drag.value as { id: number }).id)
    if (from >= 0) {
      const next = [...steps.value]
      const [m] = next.splice(from, 1)
      if (from < idx) idx--
      next.splice(idx, 0, m)
      steps.value = next
    }
  }
  onDragEnd()
}

// ─── 提升参数 ─────────────────────────────────────────────────────────────────────
function addParam(): void {
  params.value = [...params.value, { key: `param${params.value.length + 1}`, label: t('studio.newParamLabel'), type: 'text', default: '' }]
}
function removeParam(idx: number): void {
  params.value = params.value.filter((_, i) => i !== idx)
}
function patchParam(idx: number, patch: Partial<PromotedParam>): void {
  params.value = params.value.map((p, i) => (i === idx ? { ...p, ...patch } : p))
}
function optionsText(p: PromotedParam): string {
  return (p.options ?? []).join(', ')
}
function setOptions(idx: number, raw: string): void {
  patchParam(idx, { options: raw.split(',').map((s) => s.trim()).filter((s) => s !== '') })
}

// ─── 实时编译 / 预览 ───────────────────────────────────────────────────────────────
const model = computed(() => ({ image: image.value, params: params.value, steps: steps.value, meta: meta.value }))
const compiled = computed(() => compileStudioConfig(model.value))
const compiledSteps = computed(() => compileSteps(steps.value))

/** 步骤里引用了但未声明的 {{param}}(轻量校验提示)。 */
const undeclaredRefs = computed<string[]>(() => {
  const declared = new Set(params.value.map((p) => p.key.trim()).filter(Boolean))
  const c = compiledSteps.value
  const text = `${image.value}\n${c.commandTemplate}\n${c.artifactPath}`
  const refs = new Set<string>()
  for (const m of text.matchAll(/\{\{\s*([a-zA-Z_]\w*)\s*\}\}/g)) if (!declared.has(m[1])) refs.add(m[1])
  return [...refs]
})
const undeclaredRefsText = computed(() => undeclaredRefs.value.map((r) => `${ex.value.braceL}${r}${ex.value.braceR}`).join(' '))

function esc(value: string): string {
  return value.replace(/[&<>]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;' })[c] ?? c)
}
function highlight(value: string): string {
  return esc(value).replace(/\{\{\s*([a-zA-Z_]\w*)\s*\}\}/g, '<span class="tok">{{$1}}</span>')
}

/** 底部编译产出按行渲染(key: value;commandTemplate 多行缩进)。 */
const compiledLines = computed<Array<{ key: string; value: string; block?: boolean }>>(() => {
  const c = compiled.value
  const out: Array<{ key: string; value: string; block?: boolean }> = []
  const order = ['image', 'commandTemplate', 'artifactPath', 'testReport', 'reportPath', 'gateMinCoverage', 'params']
  for (const k of order) {
    const v = c[k]
    if (v == null || v === '') continue
    out.push({ key: k, value: v, block: v.includes('\n') })
  }
  return out
})

// ─── 保存 / 取消 ───────────────────────────────────────────────────────────────────
function backToLibrary(): void {
  router.push({ name: 'library', query: { tab: 'customNodes' } })
}

async function onSave(): Promise<void> {
  banner.value = ''
  const trimmed = name.value.trim()
  if (!trimmed) {
    banner.value = t('studio.errNameRequired')
    return
  }
  if (!compiledSteps.value.commandTemplate.trim()) {
    banner.value = t('studio.errNeedCommandStep')
    return
  }
  saving.value = true
  try {
    const input = {
      name: trimmed,
      description: description.value.trim(),
      nodeType: 'templated',
      summary: meta.value.summary.trim(),
      config: compiled.value,
    }
    if (editId.value) {
      await updateCustomNode(editId.value, input)
      message.success(t('studio.updatedToast', { name: trimmed }))
    } else {
      await createCustomNode(input)
      message.success(t('studio.createdToast', { name: trimmed }))
    }
    backToLibrary()
  } catch (err: unknown) {
    banner.value = err instanceof HttpError ? err.apiError?.message ?? t('studio.saveFailedCode', { code: err.status }) : t('studio.saveFailed')
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="studio-page">
    <header class="bar">
      <div class="brand">
        <div class="wrench">🔧</div>
        <div>
          <h1>{{ t('studio.brandTitle') }}</h1>
          <p>{{ t('studio.brandSubtitle') }}</p>
        </div>
      </div>
      <div class="grow" />
      <input v-model="name" class="node-name" :placeholder="t('studio.namePlaceholder')" :aria-label="t('studio.nameAria')" autocomplete="off" />
      <button class="btn" :disabled="saving" @click="backToLibrary">{{ t('studio.cancel') }}</button>
      <button class="btn primary" :disabled="saving" @click="onSave">{{ saving ? t('studio.saving') : t('studio.saveToLibrary') }}</button>
    </header>

    <div class="hero">
      <div class="eyebrow">{{ t('studio.heroEyebrow') }}</div>
      <h2>{{ t('studio.heroTitlePre') }}<em>{{ t('studio.heroTitleEm') }}</em>{{ t('studio.heroTitlePost') }}</h2>
      <p>
        {{ t('studio.heroDescPre') }}
        <code>templated</code> {{ t('studio.heroDescPost') }}
      </p>
    </div>

    <div v-if="banner" class="banner">{{ banner }}</div>
    <div v-if="loading" class="banner banner--info">{{ t('studio.loadingBanner') }}</div>

    <div class="studio">
      <!-- 左:积木库 -->
      <aside class="panel palette">
        <p class="hint">{{ t('studio.paletteHintPre') }}<strong>{{ t('studio.paletteHintStrong') }}</strong>{{ t('studio.paletteHintPost') }}</p>
        <template v-for="g in STEP_CATALOG" :key="g.group">
          <div class="pal-group">{{ g.group }}</div>
          <button
            v-for="it in g.items"
            :key="it.kind"
            class="pal-btn"
            draggable="true"
            :class="{ dragging: drag?.type === 'new' && drag.kind === it.kind }"
            @dragstart="onPaletteDragStart(it.kind)"
            @dragend="onDragEnd"
            @click="addStep(it.kind)"
          >
            <span class="pal-dot" :style="{ background: it.color }" />
            {{ it.label }}
            <span class="pal-grip">⠿</span>
          </button>
        </template>
      </aside>

      <!-- 中:步骤画布 -->
      <section class="panel">
        <h3 class="panel-title">{{ t('studio.composeTitle') }}</h3>
        <div
          class="compose"
          :class="{ 'drop-active': drag && steps.length === 0 }"
          @dragover.prevent
          @drop.prevent="onComposeDrop"
        >
          <div v-if="steps.length === 0" class="empty">{{ t('studio.composeEmpty') }}</div>
          <template v-for="(s, i) in steps" :key="s.id">
            <div
              class="dropline"
              :class="{ on: dropIndex === i }"
              @dragover.prevent="onDropLineOver(i)"
            />
            <article
              class="step"
              :class="{ dragging: drag?.type === 'move' && drag.id === s.id }"
              :style="{ borderLeftColor: catalogItem(s.kind).color }"
            >
              <header class="step-head" draggable="true" @dragstart="onStepDragStart(s.id)" @dragend="onDragEnd">
                <span class="step-grip">⠿</span>
                <span class="step-idx">{{ i + 1 }}</span>
                <span class="step-kind">{{ catalogItem(s.kind).label }}</span>
                <div class="grow" />
                <div class="step-tools">
                  <button class="icon-btn" :disabled="i === 0" :title="t('studio.moveUp')" @click="moveStep(s.id, -1)">↑</button>
                  <button class="icon-btn" :disabled="i === steps.length - 1" :title="t('studio.moveDown')" @click="moveStep(s.id, 1)">↓</button>
                  <button class="icon-btn" :title="t('studio.deleteStep')" @click="removeStep(s.id)">✕</button>
                </div>
              </header>
              <div class="step-body">
                <div v-for="(row, ri) in STEP_FIELDS[s.kind]" :key="ri" class="field-row" :style="{ gridTemplateColumns: `repeat(${row.length}, 1fr)` }">
                  <label v-for="fd in row" :key="fd.field" class="field">
                    <span class="field-label">{{ fd.label }}</span>
                    <textarea
                      v-if="fd.multiline"
                      rows="2"
                      class="field-input is-mono"
                      :value="s.fields[fd.field] ?? ''"
                      :placeholder="fd.placeholder"
                      @input="patchField(s.id, fd.field, ($event.target as HTMLTextAreaElement).value)"
                    />
                    <input
                      v-else
                      class="field-input is-mono"
                      :value="s.fields[fd.field] ?? ''"
                      :placeholder="fd.placeholder"
                      @input="patchField(s.id, fd.field, ($event.target as HTMLInputElement).value)"
                    />
                  </label>
                </div>
              </div>
            </article>
          </template>
          <div
            v-if="steps.length"
            class="dropline"
            :class="{ on: dropIndex === steps.length }"
            @dragover.prevent="onDropLineOver(steps.length)"
          />
        </div>
      </section>

      <!-- 右:提升参数 / 节点表面 -->
      <aside class="panel">
        <div class="tabs">
          <button class="tab" :class="{ active: tab === 'params' }" @click="tab = 'params'">{{ t('studio.tabParams') }}</button>
          <button class="tab" :class="{ active: tab === 'meta' }" @click="tab = 'meta'">{{ t('studio.tabMeta') }}</button>
        </div>

        <div v-if="tab === 'params'" class="rail-body">
          <p class="col-hint">{{ t('studio.paramsHintPre') }} <span class="tok">{{ ex.braceL }}key{{ ex.braceR }}</span> {{ t('studio.paramsHintPost') }}</p>
          <div v-if="params.length === 0" class="param-empty">{{ t('studio.paramsEmpty') }}</div>
          <div v-for="(p, idx) in params" :key="idx" class="param-row">
            <button class="param-del" :aria-label="t('studio.removeParamAria')" @click="removeParam(idx)">✕</button>
            <div class="pk">{{ ex.braceL }}{{ p.key }}{{ ex.braceR }}</div>
            <input
              :value="p.key"
              class="param-input is-mono"
              placeholder="key"
              @input="patchParam(idx, { key: ($event.target as HTMLInputElement).value })"
            />
            <input
              :value="p.label"
              class="param-input"
              :placeholder="t('studio.phDisplayLabel')"
              @input="patchParam(idx, { label: ($event.target as HTMLInputElement).value })"
            />
            <div class="param-two">
              <input
                :value="p.default"
                class="param-input is-mono"
                :placeholder="t('studio.phDefaultValue')"
                @input="patchParam(idx, { default: ($event.target as HTMLInputElement).value })"
              />
              <select
                :value="p.type"
                class="param-input"
                @change="patchParam(idx, { type: ($event.target as HTMLSelectElement).value as PromotedParamType })"
              >
                <option v-for="opt in PARAM_TYPE_OPTIONS" :key="opt.value" :value="opt.value">{{ opt.label }}</option>
              </select>
            </div>
            <input
              v-if="p.type === 'select'"
              :value="optionsText(p)"
              class="param-input is-mono"
              :placeholder="t('studio.phOptions')"
              @input="setOptions(idx, ($event.target as HTMLInputElement).value)"
            />
          </div>
          <button class="add-link" @click="addParam">{{ t('studio.addParam') }}</button>
        </div>

        <div v-else class="rail-body">
          <div class="meta-field">
            <label class="field-label">{{ t('studio.metaImagePre') }}{{ ex.braceL }}param{{ ex.braceR }}{{ t('studio.metaImagePost') }}</label>
            <input v-model="image" class="meta-input is-mono" :placeholder="ex.imgPlaceholder" autocomplete="off" />
          </div>
          <div class="meta-field meta-icon-cat">
            <div>
              <label class="field-label">{{ t('studio.metaIcon') }}</label>
              <input v-model="meta.icon" class="meta-input meta-icon" />
            </div>
            <div>
              <label class="field-label">{{ t('studio.metaCategory') }}</label>
              <input v-model="meta.category" class="meta-input" :placeholder="t('studio.phCategory')" />
            </div>
          </div>
          <div class="meta-field">
            <label class="field-label">{{ t('studio.metaSummaryPre') }}{{ ex.braceL }}param{{ ex.braceR }}{{ t('studio.metaSummaryPost') }}</label>
            <textarea v-model="meta.summary" rows="3" class="meta-input" :placeholder="ex.summaryPlaceholder" />
          </div>
          <p class="col-hint">
            {{ t('studio.metaHint') }}
          </p>
        </div>
      </aside>
    </div>

    <!-- 底部:编译产出 + 实例预览 -->
    <div class="bottom">
      <div>
        <div class="panel panel--code-head"><h3 class="panel-title">{{ t('studio.compiledTitle') }}</h3></div>
        <div v-if="undeclaredRefs.length" class="preview-warn">
          {{ t('studio.undeclaredWarn', { refs: undeclaredRefsText }) }}
        </div>
        <pre class="code"><span class="c">{{ t('studio.compiledComment', { open: ex.braceL + 'param' + ex.braceR }) }}</span>
<template v-for="line in compiledLines" :key="line.key"><span class="k">{{ line.key }}</span><template v-if="line.block">: |
<span v-html="line.value.split('\n').map((l) => '  ' + highlight(l)).join('\n')" />
</template><template v-else>: <span v-html="highlight(line.value)" />
</template></template><span v-if="compiledLines.length === 0" class="c">{{ t('studio.compiledEmpty') }}</span></pre>
      </div>

      <div class="panel preview-card">
        <h3 class="panel-title">{{ t('studio.previewTitle') }}</h3>
        <div class="pv-nm">
          <span class="pv-ic">{{ meta.icon || '🔧' }}</span>
          <b>{{ name || t('studio.unnamedNode') }}</b>
        </div>
        <div class="pv-cat">{{ meta.category || t('studio.defaultCategory') }} · {{ t('studio.customLabel') }}</div>
        <div class="pv-sub" v-html="highlight(meta.summary || '')" />
        <div class="inst-params">
          <div v-for="p in params.filter((x) => x.key.trim())" :key="p.key" class="inst-param">
            <label>
              {{ p.label || p.key }}
              <span class="pk2">{{ ex.braceL }}{{ p.key }}{{ ex.braceR }}</span>
            </label>
            <select v-if="p.type === 'select'" :value="p.default">
              <option v-for="o in (p.options ?? [])" :key="o" :value="o">{{ o }}</option>
            </select>
            <select v-else-if="p.type === 'toggle'" :value="p.default">
              <option value="true">true</option>
              <option value="false">false</option>
            </select>
            <input v-else :value="p.default" :type="p.type === 'number' ? 'number' : 'text'" />
          </div>
        </div>
        <p class="note">
          {{ t('studio.previewNote') }}
        </p>
      </div>
    </div>
  </div>
</template>

<style scoped>
.studio-page {
  --param: #b8860b;
  --param-soft: #fbf3df;
  max-width: 1320px;
  margin: 0 auto;
  padding: 22px 26px 60px;
  min-height: 100vh;
  background:
    radial-gradient(1200px 600px at 85% -10%, color-mix(in oklab, var(--color-primary) 12%, transparent) 0%, transparent 55%),
    radial-gradient(900px 500px at -5% 10%, color-mix(in oklab, var(--param) 12%, transparent) 0%, transparent 50%);
  color: var(--color-text);
}

/* Header bar */
.bar {
  display: flex;
  align-items: center;
  gap: 16px;
  margin-bottom: 18px;
}
.brand {
  display: flex;
  align-items: center;
  gap: 11px;
}
.wrench {
  width: 38px;
  height: 38px;
  border-radius: 11px;
  display: grid;
  place-items: center;
  background: linear-gradient(145deg, #2b3f9e, #4a6bff);
  color: #fff;
  font-size: 19px;
  box-shadow: var(--shadow-sm, 0 1px 2px rgba(20, 26, 34, 0.06));
}
.brand h1 {
  font-size: 15px;
  margin: 0;
  letter-spacing: 0.2px;
}
.brand p {
  font-size: 11.5px;
  margin: 0;
  color: var(--color-faint);
}
.grow {
  flex: 1;
}
.node-name {
  font-weight: 600;
  font-size: 15px;
  border: 1px solid var(--color-border-strong);
  border-radius: 10px;
  padding: 8px 12px;
  min-width: 240px;
  background: var(--color-bg, #fff);
  color: var(--color-text);
  outline: none;
}
.node-name:focus {
  border-color: var(--color-primary);
  box-shadow: 0 0 0 3px var(--color-primary-soft);
}
.btn {
  border: 1px solid var(--color-border-strong);
  background: var(--color-bg, #fff);
  color: var(--color-text);
  border-radius: 10px;
  padding: 9px 15px;
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
  transition: 0.14s;
}
.btn:hover:not(:disabled) {
  border-color: var(--color-primary);
  transform: translateY(-1px);
}
.btn.primary {
  background: var(--color-primary);
  border-color: var(--color-primary);
  color: #fff;
}
.btn.primary:hover:not(:disabled) {
  filter: brightness(0.96);
}
.btn:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

/* Hero */
.hero {
  margin: 6px 0 20px;
}
.hero .eyebrow {
  font-size: 11px;
  letter-spacing: 2.5px;
  text-transform: uppercase;
  color: var(--color-primary);
  font-weight: 700;
}
.hero h2 {
  font-size: clamp(24px, 3vw, 38px);
  margin: 4px 0 2px;
  letter-spacing: -1px;
  line-height: 1.05;
}
.hero h2 em {
  font-style: normal;
  color: var(--color-primary);
}
.hero p {
  color: var(--color-faint);
  margin: 0;
  max-width: 660px;
}
.hero code,
.preview-card code {
  font-family: var(--font-mono);
  font-size: 0.85em;
  padding: 0 4px;
  border-radius: 4px;
  background: var(--color-inset);
}

.banner {
  background: var(--color-red-soft);
  color: var(--color-red);
  border: 1px solid color-mix(in oklab, var(--color-red) 30%, transparent);
  border-radius: var(--rounded-md);
  padding: 9px 12px;
  font-size: 0.82rem;
  margin-bottom: 14px;
}
.banner--info {
  background: var(--color-primary-soft);
  color: var(--color-primary);
  border-color: color-mix(in oklab, var(--color-primary) 30%, transparent);
}

/* Studio grid */
.studio {
  display: grid;
  grid-template-columns: 224px 1fr 322px;
  gap: 16px;
  align-items: start;
}
.panel {
  background: var(--color-bg, #fff);
  border: 1px solid var(--color-border);
  border-radius: 14px;
  box-shadow: var(--shadow-sm, 0 1px 2px rgba(20, 26, 34, 0.06));
}
.panel-title {
  font-size: 12px;
  letter-spacing: 0.4px;
  text-transform: uppercase;
  color: var(--color-faint);
  margin: 0;
  padding: 14px 16px 8px;
}

/* Left palette */
.palette {
  max-height: 78vh;
  overflow: auto;
  padding-bottom: 8px;
}
.hint {
  font-size: 11px;
  color: var(--color-faint);
  padding: 12px 16px 6px;
  line-height: 1.55;
  margin: 0;
}
.pal-group {
  font-size: 10.5px;
  letter-spacing: 1.2px;
  text-transform: uppercase;
  color: var(--color-faint);
  font-weight: 700;
  padding: 12px 16px 4px;
}
.pal-btn {
  display: flex;
  align-items: center;
  gap: 9px;
  width: calc(100% - 20px);
  margin: 0 10px 6px;
  border: 1px solid var(--color-border);
  background: var(--color-inset);
  border-radius: 9px;
  padding: 8px 10px;
  cursor: grab;
  transition: 0.12s;
  text-align: left;
  font: inherit;
  font-size: 12.5px;
  color: var(--color-text);
  user-select: none;
}
.pal-btn:hover {
  transform: translateX(2px);
  border-color: var(--color-border-strong);
}
.pal-btn:active {
  cursor: grabbing;
}
.pal-btn.dragging {
  opacity: 0.4;
}
.pal-dot {
  width: 9px;
  height: 9px;
  border-radius: 3px;
  flex: none;
}
.pal-grip {
  color: var(--color-faint);
  font-size: 12px;
  margin-left: auto;
}

/* Center compose */
.compose {
  padding: 6px 16px 22px;
  min-height: 380px;
}
.empty {
  color: var(--color-faint);
  text-align: center;
  padding: 60px 10px;
  border: 1.5px dashed var(--color-border);
  border-radius: 12px;
  margin-top: 8px;
}
.compose.drop-active .empty {
  border-color: var(--color-primary);
  color: var(--color-primary);
  background: var(--color-primary-soft);
}
.dropline {
  height: 0;
  border-top: 2px dashed var(--color-primary);
  margin: -4px 0 7px;
  border-radius: 2px;
  opacity: 0;
  transition: opacity 0.1s;
}
.dropline.on {
  opacity: 1;
}
.step {
  border: 1px solid var(--color-border);
  border-left: 4px solid var(--color-primary);
  border-radius: 12px;
  background: var(--color-bg, #fff);
  margin-bottom: 11px;
  box-shadow: var(--shadow-sm, 0 1px 2px rgba(20, 26, 34, 0.06));
  overflow: hidden;
}
.step.dragging {
  opacity: 0.45;
}
.step-head {
  display: flex;
  align-items: center;
  gap: 9px;
  padding: 9px 12px;
  background: var(--color-inset);
  cursor: grab;
}
.step-head:active {
  cursor: grabbing;
}
.step-kind {
  font-size: 11px;
  font-weight: 700;
  letter-spacing: 0.3px;
  text-transform: uppercase;
  color: var(--color-faint);
}
.step-idx {
  width: 20px;
  height: 20px;
  border-radius: 6px;
  background: var(--color-bg, #fff);
  border: 1px solid var(--color-border);
  display: grid;
  place-items: center;
  font-size: 11px;
  font-family: var(--font-mono);
  color: var(--color-faint);
}
.step-grip {
  color: var(--color-faint);
  font-size: 12px;
}
.step-tools {
  display: flex;
  gap: 4px;
}
.icon-btn {
  width: 24px;
  height: 24px;
  border: 1px solid var(--color-border);
  background: var(--color-bg, #fff);
  border-radius: 7px;
  cursor: pointer;
  color: var(--color-faint);
  font-size: 12px;
  display: grid;
  place-items: center;
}
.icon-btn:hover:not(:disabled) {
  color: var(--color-text);
  border-color: var(--color-border-strong);
}
.icon-btn:disabled {
  opacity: 0.3;
  cursor: not-allowed;
}
.step-body {
  padding: 11px 12px;
  display: grid;
  gap: 8px;
}
.field-row {
  display: grid;
  gap: 8px;
}
.field {
  display: block;
  min-width: 0;
}
.field-label {
  font-size: 11px;
  color: var(--color-faint);
  display: block;
  margin-bottom: 3px;
}
.field-input {
  width: 100%;
  border: 1px solid var(--color-border);
  border-radius: 8px;
  padding: 8px 10px;
  font-size: 12.5px;
  color: var(--color-text);
  background: var(--color-inset);
  outline: none;
  resize: vertical;
}
.field-input:focus {
  border-color: var(--color-primary);
  background: var(--color-bg, #fff);
  box-shadow: 0 0 0 3px var(--color-primary-soft);
}
.is-mono {
  font-family: var(--font-mono);
}

/* Right rail */
.tabs {
  display: flex;
  gap: 4px;
  padding: 12px 14px 0;
}
.tab {
  flex: 1;
  text-align: center;
  padding: 8px;
  font: inherit;
  font-size: 12.5px;
  font-weight: 600;
  color: var(--color-faint);
  border: 1px solid transparent;
  border-radius: 9px;
  cursor: pointer;
  background: none;
}
.tab.active {
  color: var(--color-primary);
  background: var(--color-primary-soft);
}
.rail-body {
  padding: 12px 14px 16px;
}
.col-hint {
  font-size: 11px;
  color: var(--color-faint);
  margin: 0 0 10px;
  line-height: 1.5;
}
.param-empty {
  font-size: 12px;
  color: var(--color-faint);
  font-style: italic;
  padding: 6px 0;
}
.param-row {
  border: 1px solid var(--color-border);
  border-left: 3px solid var(--param);
  border-radius: 11px;
  padding: 10px;
  margin-bottom: 10px;
  background: var(--param-soft);
  position: relative;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.param-row .pk {
  font-family: var(--font-mono);
  font-size: 12px;
  color: var(--param);
  font-weight: 700;
}
.param-input {
  width: 100%;
  border: 1px solid #ecdcb0;
  border-radius: 7px;
  padding: 6px 8px;
  font-size: 12px;
  background: #fff;
  color: #161a22;
  outline: none;
}
.param-input:focus {
  border-color: var(--param);
}
.param-two {
  display: grid;
  grid-template-columns: 1fr 92px;
  gap: 7px;
}
.param-del {
  position: absolute;
  top: 8px;
  right: 8px;
  width: 22px;
  height: 22px;
  border: 1px solid #ecdcb0;
  background: #fff;
  border-radius: 7px;
  cursor: pointer;
  color: var(--param);
  font-size: 11px;
}
.param-del:hover {
  background: var(--param-soft);
}
.add-link {
  color: var(--color-primary);
  font: inherit;
  font-size: 12.5px;
  font-weight: 600;
  cursor: pointer;
  background: none;
  border: none;
  padding: 0;
}
.add-link:hover {
  text-decoration: underline;
}
.meta-field {
  margin-bottom: 12px;
}
.meta-icon-cat {
  display: grid;
  grid-template-columns: 64px 1fr;
  gap: 8px;
}
.meta-input {
  width: 100%;
  border: 1px solid var(--color-border-strong);
  border-radius: 8px;
  padding: 8px 10px;
  font: inherit;
  font-size: 13px;
  background: var(--color-inset);
  color: var(--color-text);
  outline: none;
  resize: vertical;
}
.meta-input:focus {
  border-color: var(--color-primary);
  box-shadow: 0 0 0 3px var(--color-primary-soft);
}
.meta-icon {
  text-align: center;
}

.tok {
  background: var(--param-soft);
  color: var(--param);
  border-radius: 4px;
  padding: 0 3px;
  font-weight: 700;
}

/* Bottom */
.bottom {
  display: grid;
  grid-template-columns: 1fr 322px;
  gap: 16px;
  margin-top: 18px;
  align-items: start;
}
.panel--code-head {
  margin-bottom: 10px;
}
.panel--code-head .panel-title {
  padding: 14px 16px;
}
.preview-warn {
  font-size: 11.5px;
  color: var(--param);
  margin-bottom: 8px;
}
.code {
  font-family: var(--font-mono);
  font-size: 12.5px;
  line-height: 1.65;
  white-space: pre-wrap;
  word-break: break-word;
  background: #0f1320;
  color: #d6def0;
  border-radius: 14px;
  padding: 16px 18px;
  overflow: auto;
  box-shadow: var(--shadow-md, 0 6px 24px rgba(20, 26, 34, 0.1));
  margin: 0;
}
.code :deep(.k) {
  color: #79c0ff;
}
.code :deep(.c) {
  color: #6b7689;
}
.code :deep(.tok) {
  color: #f0b84d;
  background: none;
  font-weight: 700;
}

/* Instance preview card */
.preview-card {
  padding: 16px;
}
.pv-nm {
  display: flex;
  align-items: center;
  gap: 9px;
  margin: 6px 0 4px;
}
.pv-ic {
  width: 30px;
  height: 30px;
  border-radius: 9px;
  display: grid;
  place-items: center;
  background: var(--color-primary-soft);
  color: var(--color-primary);
  font-size: 15px;
}
.pv-nm b {
  font-size: 14px;
}
.pv-cat {
  font-size: 11px;
  color: var(--color-faint);
}
.pv-sub {
  font-size: 12px;
  color: var(--color-faint);
  margin: 6px 0 14px;
  min-height: 1em;
}
.inst-param {
  margin-bottom: 11px;
}
.inst-param label {
  font-size: 11.5px;
  color: var(--color-faint);
  display: flex;
  gap: 6px;
  align-items: center;
  margin-bottom: 4px;
}
.inst-param .pk2 {
  font-family: var(--font-mono);
  font-size: 10.5px;
  color: var(--param);
  background: var(--param-soft);
  padding: 0 4px;
  border-radius: 4px;
}
.inst-param input,
.inst-param select {
  width: 100%;
  border: 1px solid var(--color-border);
  border-radius: 8px;
  padding: 7px 9px;
  font: inherit;
  font-size: 12.5px;
  background: var(--color-inset);
  color: var(--color-text);
  outline: none;
}
.note {
  font-size: 11px;
  color: var(--color-faint);
  margin-top: 12px;
  line-height: 1.5;
  border-top: 1px dashed var(--color-border);
  padding-top: 10px;
}

@media (max-width: 1080px) {
  .studio {
    grid-template-columns: 1fr;
  }
  .bottom {
    grid-template-columns: 1fr;
  }
  .palette {
    max-height: none;
  }
}
</style>
