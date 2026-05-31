<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import type { PipelineJob, PipelineStage } from '../../api/pipeline'
import type { Credential } from '../../api/credentials'
import type { Server } from '../../api/servers'
import {
  getJobTypeSpec,
  splitConfig,
  JOB_TYPE_OPTIONS,
  type JobField,
} from './jobConfigSchema'

const props = defineProps<{
  job: PipelineJob
  stage: PipelineStage
  credentials?: Credential[]
  servers?: Server[]
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'update', patch: Partial<PipelineJob>): void
}>()

// ─── Local editable copy ──────────────────────────────────────────────────────

interface KVRow {
  _key: number
  k: string
  v: string
}

let _kvSeq = 0

const localName    = ref(props.job.name)
const localType    = ref(props.job.type)
const localSummary = ref(props.job.summary)
/** Schema-owned keys for the current type (the typed form binds here) */
const typedConfig  = ref<Record<string, string>>({})
/** Keys NOT covered by the type schema — editable in the advanced section */
const extraRows    = ref<KVRow[]>([])

const showAdvanced = ref(false)

function hydrate(job: PipelineJob): void {
  localName.value    = job.name
  localType.value    = job.type
  localSummary.value = job.summary
  splitOnType(job.type, job.config ?? {})
}

/** Recompute typed config + raw extras for a given type, preserving all values. */
function splitOnType(type: string, config: Record<string, string>): void {
  const { extras } = splitConfig(type, config)
  const typed: Record<string, string> = {}
  for (const [k, v] of Object.entries(config)) {
    if (!extras.some(([ek]) => ek === k)) typed[k] = v
  }
  typedConfig.value = typed
  extraRows.value = extras.map(([k, v]) => ({ _key: ++_kvSeq, k, v }))
  showAdvanced.value = extras.length > 0
}

// Resync when the selected job changes
watch(() => props.job, (next) => hydrate(next))
hydrate(props.job)

// ─── Field schema for the current type ────────────────────────────────────────

const spec = computed(() => getJobTypeSpec(localType.value))

const visibleFields = computed<JobField[]>(() => {
  if (!spec.value) return []
  return spec.value.fields.filter((f) => !f.when || f.when(typedConfig.value))
})

function credentialOptions(field: JobField): Credential[] {
  const all = props.credentials ?? []
  if (!field.credentialType) return all
  return all.filter((c) => c.type === field.credentialType)
}

// ─── Value get/set ────────────────────────────────────────────────────────────

function fieldValue(key: string): string {
  return typedConfig.value[key] ?? ''
}

/** Default-display value for selects so the first option shows before any edit. */
function selectValue(field: JobField): string {
  const v = typedConfig.value[field.key]
  if (v) return v
  return field.options?.[0]?.value ?? ''
}

function updateLocal(key: string, value: string): void {
  typedConfig.value = { ...typedConfig.value, [key]: value }
}

function setField(key: string, value: string): void {
  updateLocal(key, value)
  flush()
}

// ─── Raw extras (advanced) ─────────────────────────────────────────────────────

function addExtra(): void {
  extraRows.value.push({ _key: ++_kvSeq, k: '', v: '' })
}

function removeExtra(key: number): void {
  extraRows.value = extraRows.value.filter((r) => r._key !== key)
  flush()
}

function extrasToObject(rows: KVRow[]): Record<string, string> {
  const out: Record<string, string> = {}
  for (const row of rows) {
    const key = row.k.trim()
    if (key) out[key] = row.v
  }
  return out
}

// ─── Type change ───────────────────────────────────────────────────────────────

function onTypeChange(type: string): void {
  // Preserve all current values, just re-split owned vs. extra for the new type.
  const merged = { ...extrasToObject(extraRows.value), ...typedConfig.value }
  localType.value = type
  splitOnType(type, merged)
  flush()
}

// ─── Flush ─────────────────────────────────────────────────────────────────────

function flush(): void {
  // typed wins over extras on key conflict (they should never overlap).
  const config = { ...extrasToObject(extraRows.value), ...typedConfig.value }
  emit('update', {
    name:    localName.value.trim() || props.job.name,
    type:    localType.value.trim() || props.job.type,
    summary: localSummary.value,
    config,
  })
}
</script>

<template>
  <aside class="job-drawer" aria-label="任务配置">
    <!-- Head -->
    <div class="drawer-head">
      <span class="drawer-icon" aria-hidden="true">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9">
          <rect x="3" y="4" width="18" height="6" rx="1.6"/>
          <rect x="3" y="14" width="18" height="6" rx="1.6"/>
        </svg>
      </span>
      <div class="drawer-title">
        {{ localName || '(未命名任务)' }}
        <small class="drawer-subtitle">{{ stage.name }} · 本流水线</small>
      </div>
      <button
        class="drawer-close"
        aria-label="关闭抽屉"
        @click="emit('close')"
      >
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <path d="M18 6 6 18M6 6l12 12"/>
        </svg>
      </button>
    </div>

    <!-- Basic info section -->
    <div class="drawer-section">
      <div class="drawer-section-label">基本信息</div>

      <div class="drawer-field">
        <div class="drawer-field-label">任务名称</div>
        <input
          v-model="localName"
          class="drawer-input"
          type="text"
          placeholder="例:隔离构建"
          aria-label="任务名称"
          @blur="flush"
        />
      </div>

      <div class="drawer-field">
        <div class="drawer-field-label">任务类型</div>
        <select
          :value="localType"
          class="drawer-select"
          aria-label="任务类型"
          @change="onTypeChange(($event.target as HTMLSelectElement).value)"
        >
          <option
            v-for="opt in JOB_TYPE_OPTIONS"
            :key="opt.value"
            :value="opt.value"
          >{{ opt.label }}</option>
          <!-- Allow custom types not in the list -->
          <option
            v-if="!JOB_TYPE_OPTIONS.some(o => o.value === localType) && localType"
            :value="localType"
          >{{ localType }}</option>
        </select>
        <p v-if="spec" class="field-desc">{{ spec.description }}</p>
      </div>

      <div class="drawer-field">
        <div class="drawer-field-label">摘要描述</div>
        <input
          v-model="localSummary"
          class="drawer-input"
          type="text"
          placeholder="卡片副标题(可选)"
          aria-label="摘要描述"
          @blur="flush"
        />
      </div>
    </div>

    <!-- Typed config section (per-type form) -->
    <div v-if="spec" class="drawer-section">
      <div class="drawer-section-label">{{ spec.label }}配置</div>

      <div
        v-for="field in visibleFields"
        :key="field.key"
        class="drawer-field"
      >
        <div class="drawer-field-label">{{ field.label }}</div>

        <!-- textarea -->
        <textarea
          v-if="field.kind === 'textarea'"
          :value="fieldValue(field.key)"
          class="drawer-input drawer-textarea"
          :class="{ 'is-mono': field.monospace }"
          rows="4"
          :placeholder="field.placeholder"
          :aria-label="field.label"
          @input="updateLocal(field.key, ($event.target as HTMLTextAreaElement).value)"
          @blur="flush"
        ></textarea>

        <!-- select -->
        <select
          v-else-if="field.kind === 'select'"
          :value="selectValue(field)"
          class="drawer-select"
          :aria-label="field.label"
          @change="setField(field.key, ($event.target as HTMLSelectElement).value)"
        >
          <option v-for="opt in field.options" :key="opt.value" :value="opt.value">
            {{ opt.label }}
          </option>
        </select>

        <!-- credential picker -->
        <select
          v-else-if="field.kind === 'credential'"
          :value="fieldValue(field.key)"
          class="drawer-select"
          :aria-label="field.label"
          @change="setField(field.key, ($event.target as HTMLSelectElement).value)"
        >
          <option value="">— 未选择 —</option>
          <option v-for="c in credentialOptions(field)" :key="c.id" :value="c.id">
            {{ c.name }} ({{ c.maskedValue }})
          </option>
        </select>

        <!-- server picker -->
        <select
          v-else-if="field.kind === 'server'"
          :value="fieldValue(field.key)"
          class="drawer-select"
          :aria-label="field.label"
          @change="setField(field.key, ($event.target as HTMLSelectElement).value)"
        >
          <option value="">— 未选择 —</option>
          <option v-for="srv in (servers ?? [])" :key="srv.id" :value="srv.id">
            {{ srv.name }} · {{ srv.host }}
          </option>
        </select>

        <!-- toggle -->
        <label v-else-if="field.kind === 'toggle'" class="drawer-toggle">
          <input
            type="checkbox"
            :checked="fieldValue(field.key) === 'true'"
            :aria-label="field.label"
            @change="setField(field.key, ($event.target as HTMLInputElement).checked ? 'true' : 'false')"
          />
          <span>{{ field.hint || '启用' }}</span>
        </label>

        <!-- number / text -->
        <input
          v-else
          :value="fieldValue(field.key)"
          class="drawer-input"
          :class="{ 'is-mono': field.monospace }"
          :type="field.kind === 'number' ? 'number' : 'text'"
          :placeholder="field.placeholder"
          :aria-label="field.label"
          @input="updateLocal(field.key, ($event.target as HTMLInputElement).value)"
          @blur="flush"
        />

        <p v-if="field.hint && field.kind !== 'toggle'" class="field-hint">{{ field.hint }}</p>
      </div>
    </div>

    <!-- Advanced raw KV (extras not covered by the schema) -->
    <div class="drawer-section">
      <button
        class="advanced-toggle"
        :class="{ 'advanced-toggle--open': showAdvanced }"
        :aria-expanded="showAdvanced"
        @click="showAdvanced = !showAdvanced"
      >
        <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true">
          <path d="M9 18l6-6-6-6"/>
        </svg>
        高级 · 原始参数<span v-if="extraRows.length" class="advanced-count">{{ extraRows.length }}</span>
      </button>

      <div v-if="showAdvanced" class="advanced-body">
        <div v-if="extraRows.length === 0" class="kv-empty">
          {{ spec ? '该类型的参数已在上方表单;此处可加自定义键值' : '为该类型添加自定义键值参数' }}
        </div>

        <div
          v-for="row in extraRows"
          :key="row._key"
          class="kv-row"
        >
          <input
            v-model="row.k"
            class="kv-input"
            type="text"
            placeholder="key"
            :aria-label="`配置键 ${row._key}`"
            @blur="flush"
          />
          <input
            v-model="row.v"
            class="kv-input"
            type="text"
            placeholder="value"
            :aria-label="`配置值 ${row._key}`"
            @blur="flush"
          />
          <button
            class="kv-del"
            :aria-label="`删除配置项 ${row.k || row._key}`"
            @click="removeExtra(row._key)"
          >
            <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
              <path d="M18 6 6 18M6 6l12 12"/>
            </svg>
          </button>
        </div>

        <button class="kv-add-btn" @click="addExtra">
          <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" aria-hidden="true">
            <path d="M12 5v14M5 12h14"/>
          </svg>
          添加参数
        </button>
      </div>
    </div>
  </aside>
</template>

<style scoped>
.kv-empty {
  font-size: 0.78rem;
  color: var(--color-faint);
  font-style: italic;
  margin-bottom: 8px;
}

.field-desc {
  margin: 6px 0 0;
  font-size: 0.74rem;
  color: var(--color-faint);
}

.field-hint {
  margin: 5px 0 0;
  font-size: 0.72rem;
  color: var(--color-faint);
  line-height: 1.4;
}

.drawer-textarea {
  height: auto;
  min-height: 84px;
  padding: 9px 11px;
  line-height: 1.5;
  resize: vertical;
}

.is-mono {
  font-family: var(--font-mono);
  font-size: 0.8rem;
}

.drawer-toggle {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  font-size: 0.8rem;
  color: var(--color-dim);
  cursor: pointer;
}

.drawer-toggle input {
  width: 15px;
  height: 15px;
  accent-color: var(--color-primary);
  cursor: pointer;
}

.advanced-toggle {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  background: none;
  border: none;
  padding: 0;
  color: var(--color-dim);
  font: inherit;
  font-size: 0.76rem;
  font-weight: 600;
  cursor: pointer;
  text-transform: uppercase;
  letter-spacing: 0.02em;
}

.advanced-toggle svg {
  transition: transform var(--duration-fast);
}

.advanced-toggle--open svg {
  transform: rotate(90deg);
}

.advanced-toggle:hover {
  color: var(--color-text);
}

.advanced-count {
  display: inline-grid;
  place-items: center;
  min-width: 16px;
  height: 16px;
  padding: 0 4px;
  border-radius: 8px;
  background: var(--color-primary-soft);
  color: var(--color-primary);
  font-size: 0.66rem;
  font-weight: 700;
}

.advanced-body {
  margin-top: 11px;
}
</style>
