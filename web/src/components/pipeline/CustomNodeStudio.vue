<script setup lang="ts">
/**
 * CustomNodeStudio — 复用库「自定义节点工作室」(低代码组合 · Tier 3 收口)。
 *
 * 把可视化步骤(复用 StepBuilder)+ 提升参数(promoted params)+ 节点表面(名称/说明)
 * 拼成一个可复用、可配置的自定义节点,编译成 `templated` 节点 config 存库 —— 后端零改。
 * 见 studioCompile.ts 的转换契约。
 *
 * 受控组件:父级持有 open / saving / banner;本组件持有表单态,保存时把
 * { name, description, summary, config } emit 出去,由父级调 createCustomNode / updateCustomNode。
 */
import { ref, computed, watch } from 'vue'
import StepBuilder from './StepBuilder.vue'
import {
  type PromotedParam,
  type PromotedParamType,
  type StudioModel,
  compileStudioConfig,
  parseStudioConfig,
  emptyStudioModel,
} from './studioCompile'

interface StudioNode {
  id: string
  name: string
  description: string
  summary: string
  nodeType: string
  config: Record<string, unknown>
}

const props = defineProps<{
  open: boolean
  /** 编辑既有节点;null = 新建。 */
  node: StudioNode | null
  saving: boolean
  banner: string
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'save', payload: { id: string | null; name: string; description: string; summary: string; config: Record<string, string> }): void
}>()

const name = ref('')
const description = ref('')
const summary = ref('')
const image = ref('')
const params = ref<PromotedParam[]>([])
const stepConfig = ref<{ commands: string; artifactPath: string }>({ commands: '', artifactPath: '' })
const localError = ref('')

/** 每次打开 +1,作 StepBuilder 的 key 强制按当前 config 干净反解析。 */
const instanceKey = ref(0)

const isEdit = computed(() => props.node !== null)
const title = computed(() => (isEdit.value ? '编辑工作室节点' : '新建工作室节点'))

const PARAM_TYPE_OPTIONS: ReadonlyArray<{ value: PromotedParamType; label: string }> = [
  { value: 'text', label: '文本' },
  { value: 'select', label: '枚举' },
  { value: 'number', label: '数字' },
  { value: 'toggle', label: '布尔' },
]

// 含字面 {{ }} 的示例文案放常量里(避免模板内 {{ '{{x}}' }} 触发 vue 解析错误)。
const ex = {
  imgLabelHint: '可用 {{param}}',
  imgPlaceholder: 'node:20 或 node:{{ver}}',
  summaryPlaceholder: '用 {{node}} 镜像构建 {{dir}} 并产出 dist',
  keyRef: '{{key}}',
  braceL: '{{',
  braceR: '}}',
}

function hydrate(): void {
  localError.value = ''
  if (props.node) {
    name.value = props.node.name
    description.value = props.node.description
    summary.value = props.node.summary
    const model = parseStudioConfig(props.node.config)
    image.value = model.image
    params.value = model.params.map((p) => ({ ...p }))
    stepConfig.value = { ...model.stepConfig }
  } else {
    name.value = ''
    description.value = ''
    summary.value = ''
    const m = emptyStudioModel()
    image.value = m.image
    params.value = []
    stepConfig.value = { ...m.stepConfig }
  }
  instanceKey.value += 1
}

watch(
  () => [props.open, props.node] as const,
  ([open]) => {
    if (open) hydrate()
  },
  { immediate: true },
)

/** StepBuilder 回传编译片段(commands + artifactPath)。 */
function onStepsUpdate(patch: { commands: string; artifactPath: string }): void {
  stepConfig.value = { commands: patch.commands, artifactPath: patch.artifactPath }
}

// ─── 提升参数编辑 ──────────────────────────────────────────────────────────────
function addParam(): void {
  params.value = [...params.value, { key: `param${params.value.length + 1}`, label: '新参数', type: 'text', default: '' }]
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
  const options = raw
    .split(',')
    .map((s) => s.trim())
    .filter((s) => s !== '')
  patchParam(idx, { options })
}

// ─── 实时编译预览 ──────────────────────────────────────────────────────────────
const model = computed<StudioModel>(() => ({
  image: image.value,
  params: params.value,
  stepConfig: stepConfig.value,
}))
const compiled = computed(() => compileStudioConfig(model.value))
const previewLines = computed<Array<{ k: string; v: string }>>(() => {
  const c = compiled.value
  const order = ['image', 'commandTemplate', 'artifactPath', 'params']
  return order.filter((k) => c[k] != null && c[k] !== '').map((k) => ({ k, v: c[k] }))
})

/** 步骤里引用了但未声明的 {{param}}(轻量校验提示)。 */
const undeclaredRefs = computed<string[]>(() => {
  const declared = new Set(params.value.map((p) => p.key.trim()).filter(Boolean))
  const text = `${image.value}\n${stepConfig.value.commands}\n${stepConfig.value.artifactPath}`
  const refs = new Set<string>()
  for (const m of text.matchAll(/\{\{\s*([a-zA-Z_]\w*)\s*\}\}/g)) {
    if (!declared.has(m[1])) refs.add(m[1])
  }
  return [...refs]
})
const undeclaredRefsText = computed(() => undeclaredRefs.value.map((r) => `${ex.braceL}${r}${ex.braceR}`).join(' '))

function highlight(value: string): string {
  const esc = value.replace(/[&<>]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;' })[c] ?? c)
  return esc.replace(/\{\{\s*([a-zA-Z_]\w*)\s*\}\}/g, '<span class="tok">{{$1}}</span>')
}

const stepBuilderConfig = computed(() => ({
  commands: stepConfig.value.commands,
  artifactPath: stepConfig.value.artifactPath,
}))

function onSave(): void {
  localError.value = ''
  const trimmed = name.value.trim()
  if (!trimmed) {
    localError.value = '节点名称不能为空'
    return
  }
  if (!stepConfig.value.commands.trim()) {
    localError.value = '至少要有一个会产生命令的步骤'
    return
  }
  emit('save', {
    id: props.node?.id ?? null,
    name: trimmed,
    description: description.value.trim(),
    summary: summary.value.trim(),
    config: compiled.value,
  })
}
</script>

<template>
  <Teleport to="body">
    <div v-if="open" class="studio-scrim" @click.self="emit('close')">
      <div class="studio" role="dialog" aria-modal="true" :aria-label="title">
        <header class="studio-head">
          <div>
            <h2 class="studio-title">{{ title }}</h2>
            <p class="studio-sub">低代码组合步骤 + 提升参数 → 可复用 <code>templated</code> 节点(后端零改)</p>
          </div>
          <button class="studio-close" aria-label="关闭" @click="emit('close')">✕</button>
        </header>

        <div v-if="banner || localError" class="studio-banner">{{ banner || localError }}</div>

        <div class="studio-meta">
          <label class="fld fld--grow">
            <span class="fld-label">节点名称</span>
            <input v-model="name" class="fld-input" placeholder="如 构建并打包前端" autocomplete="off" />
          </label>
          <label class="fld fld--img">
            <span class="fld-label">运行镜像({{ ex.imgLabelHint }})</span>
            <input v-model="image" class="fld-input is-mono" :placeholder="ex.imgPlaceholder" autocomplete="off" />
          </label>
        </div>
        <label class="fld">
          <span class="fld-label">一句话说明(可选,实例卡片展示)</span>
          <input v-model="summary" class="fld-input" :placeholder="ex.summaryPlaceholder" autocomplete="off" />
        </label>

        <div class="studio-body">
          <section class="studio-col">
            <h3 class="col-head">步骤组合</h3>
            <StepBuilder :key="instanceKey" :config="stepBuilderConfig" @update="onStepsUpdate" />
          </section>

          <section class="studio-col">
            <div class="col-head-row">
              <h3 class="col-head">提升参数</h3>
              <button class="link-add" @click="addParam">＋ 提升参数</button>
            </div>
            <p class="col-hint">步骤里以 <span class="tok">{{ ex.keyRef }}</span> 引用;实例复用时只配这几项。</p>

            <div v-if="params.length === 0" class="param-empty">还没有提升参数。整段脚本写死也行,提升后才可在实例里改。</div>

            <div v-for="(p, idx) in params" :key="idx" class="param-row">
              <button class="param-del" aria-label="移除参数" @click="removeParam(idx)">✕</button>
              <div class="param-key-line">
                <span class="param-brace">{{ ex.braceL }}</span>
                <input
                  :value="p.key"
                  class="fld-input is-mono param-key"
                  placeholder="key"
                  @input="patchParam(idx, { key: ($event.target as HTMLInputElement).value })"
                />
                <span class="param-brace">{{ ex.braceR }}</span>
              </div>
              <input
                :value="p.label"
                class="fld-input param-label"
                placeholder="显示标签"
                @input="patchParam(idx, { label: ($event.target as HTMLInputElement).value })"
              />
              <div class="param-row2">
                <input
                  :value="p.default"
                  class="fld-input is-mono"
                  placeholder="默认值"
                  @input="patchParam(idx, { default: ($event.target as HTMLInputElement).value })"
                />
                <select
                  :value="p.type"
                  class="fld-input param-type"
                  @change="patchParam(idx, { type: ($event.target as HTMLSelectElement).value as PromotedParamType })"
                >
                  <option v-for="opt in PARAM_TYPE_OPTIONS" :key="opt.value" :value="opt.value">{{ opt.label }}</option>
                </select>
              </div>
              <input
                v-if="p.type === 'select'"
                :value="optionsText(p)"
                class="fld-input is-mono param-options"
                placeholder="逗号分隔选项,如 20, 18, 22"
                @input="setOptions(idx, ($event.target as HTMLInputElement).value)"
              />
            </div>
          </section>
        </div>

        <details class="studio-preview" open>
          <summary>编译产出 · templated 节点 config(后端原样跑)</summary>
          <div v-if="undeclaredRefs.length" class="preview-warn">
            ⚠ 步骤引用了未提升的参数:{{ undeclaredRefsText }}(将原样保留,实例改不动)
          </div>
          <pre class="preview-code"><template v-for="line in previewLines" :key="line.k"><span class="pk">{{ line.k }}</span>: <span v-html="highlight(line.v)"></span>
</template></pre>
        </details>

        <footer class="studio-foot">
          <button class="btn-ghost" :disabled="saving" @click="emit('close')">取消</button>
          <button class="btn-primary" :disabled="saving" @click="onSave">{{ saving ? '保存中…' : '保存到复用库' }}</button>
        </footer>
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
.studio-scrim {
  position: fixed;
  inset: 0;
  z-index: 60;
  background: color-mix(in oklab, var(--color-text) 38%, transparent);
  backdrop-filter: blur(2px);
  display: grid;
  place-items: center;
  padding: 24px;
}

.studio {
  width: min(960px, 96vw);
  max-height: 92vh;
  overflow: auto;
  background: var(--color-bg, #fff);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-lg, 16px);
  box-shadow: 0 24px 60px rgba(20, 26, 34, 0.22);
  padding: 20px 22px 18px;
  display: flex;
  flex-direction: column;
  gap: 13px;
}

.studio-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}
.studio-title {
  margin: 0;
  font-size: 1.12rem;
  font-weight: 700;
}
.studio-sub {
  margin: 3px 0 0;
  font-size: 0.76rem;
  color: var(--color-faint);
}
.studio-sub code {
  font-family: var(--font-mono);
  font-size: 0.72rem;
  padding: 0 3px;
  border-radius: 3px;
  background: var(--color-inset);
}
.studio-close {
  flex: none;
  width: 30px;
  height: 30px;
  border: 1px solid var(--color-border);
  border-radius: 8px;
  background: none;
  color: var(--color-faint);
  cursor: pointer;
}
.studio-close:hover {
  color: var(--color-text);
  border-color: var(--color-border-strong);
}

.studio-banner {
  background: var(--color-red-soft);
  color: var(--color-red);
  border: 1px solid color-mix(in oklab, var(--color-red) 30%, transparent);
  border-radius: var(--rounded-md);
  padding: 8px 11px;
  font-size: 0.8rem;
}

.studio-meta {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 11px;
}
.fld {
  display: flex;
  flex-direction: column;
  gap: 5px;
}
.fld-label {
  font-size: 0.72rem;
  font-weight: 600;
  color: var(--color-faint);
}
.fld-input {
  width: 100%;
  height: 32px;
  padding: 0 10px;
  background: var(--color-inset);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded-md);
  color: var(--color-text);
  font: inherit;
  font-size: 0.82rem;
}
.fld-input:focus {
  outline: none;
  border-color: var(--color-primary);
  box-shadow: 0 0 0 2px var(--color-primary-soft);
}
.is-mono {
  font-family: var(--font-mono);
  font-size: 0.78rem;
}

.studio-body {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 16px;
  align-items: start;
}
.studio-col {
  display: flex;
  flex-direction: column;
  gap: 8px;
  min-width: 0;
}
.col-head {
  margin: 0;
  font-size: 0.74rem;
  font-weight: 700;
  text-transform: uppercase;
  letter-spacing: 0.03em;
  color: var(--color-faint);
}
.col-head-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.col-hint {
  margin: -2px 0 2px;
  font-size: 0.7rem;
  color: var(--color-faint);
}
.link-add {
  background: none;
  border: none;
  color: var(--color-primary);
  font: inherit;
  font-size: 0.76rem;
  font-weight: 600;
  cursor: pointer;
  padding: 0;
}
.link-add:hover {
  text-decoration: underline;
}

.param-empty {
  font-size: 0.74rem;
  color: var(--color-faint);
  font-style: italic;
  padding: 6px 0;
}
.param-row {
  position: relative;
  border: 1px solid var(--color-border);
  border-left: 3px solid var(--color-amber);
  border-radius: var(--rounded-md);
  background: var(--color-amber-soft, var(--color-inset));
  padding: 9px 10px;
  margin-bottom: 9px;
  display: flex;
  flex-direction: column;
  gap: 7px;
}
.param-del {
  position: absolute;
  top: 7px;
  right: 7px;
  width: 20px;
  height: 20px;
  border: none;
  background: none;
  color: var(--color-faint);
  cursor: pointer;
  border-radius: 4px;
}
.param-del:hover {
  color: var(--color-red);
  background: var(--color-red-soft);
}
.param-key-line {
  display: flex;
  align-items: center;
  gap: 4px;
}
.param-brace {
  font-family: var(--font-mono);
  font-weight: 700;
  color: var(--color-amber);
}
.param-key {
  height: 28px;
  max-width: 180px;
}
.param-label {
  height: 28px;
}
.param-row2 {
  display: grid;
  grid-template-columns: 1fr 92px;
  gap: 7px;
}
.param-row2 .fld-input,
.param-options {
  height: 28px;
}
.param-type {
  cursor: pointer;
}

.tok {
  background: var(--color-amber-soft, #fbf3df);
  color: var(--color-amber);
  border-radius: 3px;
  padding: 0 3px;
  font-weight: 700;
}

.studio-preview {
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-md);
  background: var(--color-inset);
  padding: 4px 12px 10px;
}
.studio-preview > summary {
  cursor: pointer;
  font-size: 0.74rem;
  font-weight: 600;
  color: var(--color-dim);
  padding: 8px 0;
}
.preview-warn {
  font-size: 0.72rem;
  color: var(--color-amber);
  margin-bottom: 6px;
}
.preview-code {
  margin: 0;
  font-family: var(--font-mono);
  font-size: 0.74rem;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-word;
  color: var(--color-text);
}
.preview-code .pk {
  color: var(--color-primary);
  font-weight: 700;
}

.studio-foot {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  padding-top: 4px;
  border-top: 1px solid var(--color-border);
  margin-top: 2px;
  padding-top: 13px;
}
.btn-ghost,
.btn-primary {
  height: 34px;
  padding: 0 16px;
  border-radius: var(--rounded-md);
  font: inherit;
  font-size: 0.82rem;
  font-weight: 600;
  cursor: pointer;
}
.btn-ghost {
  background: none;
  border: 1px solid var(--color-border-strong);
  color: var(--color-text);
}
.btn-ghost:hover {
  border-color: var(--color-primary);
}
.btn-primary {
  background: var(--color-primary);
  border: 1px solid var(--color-primary);
  color: #fff;
}
.btn-primary:hover {
  background: var(--color-primary-strong, var(--color-primary));
  filter: brightness(0.96);
}
.btn-primary:disabled,
.btn-ghost:disabled {
  opacity: 0.55;
  cursor: not-allowed;
}

@media (max-width: 760px) {
  .studio-meta,
  .studio-body {
    grid-template-columns: 1fr;
  }
}
</style>
