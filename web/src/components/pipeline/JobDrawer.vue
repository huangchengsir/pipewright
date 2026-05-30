<script setup lang="ts">
import { ref, watch } from 'vue'
import type { PipelineJob, PipelineStage, StageKind } from '../../api/pipeline'

const props = defineProps<{
  job: PipelineJob
  stage: PipelineStage
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
const localKV      = ref<KVRow[]>(objectToKV(props.job.config))

// Sync when the selected job changes
watch(
  () => props.job,
  (next) => {
    localName.value    = next.name
    localType.value    = next.type
    localSummary.value = next.summary
    localKV.value      = objectToKV(next.config)
  },
)

function objectToKV(obj: Record<string, string> | null | undefined): KVRow[] {
  return Object.entries(obj ?? {}).map(([k, v]) => ({ _key: ++_kvSeq, k, v }))
}

function kvToObject(rows: KVRow[]): Record<string, string> {
  const result: Record<string, string> = {}
  for (const row of rows) {
    const key = row.k.trim()
    if (key) result[key] = row.v
  }
  return result
}

function addKV(): void {
  localKV.value.push({ _key: ++_kvSeq, k: '', v: '' })
}

function removeKV(key: number): void {
  localKV.value = localKV.value.filter((r) => r._key !== key)
}

function flush(): void {
  emit('update', {
    name:    localName.value.trim() || props.job.name,
    type:    localType.value.trim() || props.job.type,
    summary: localSummary.value,
    config:  kvToObject(localKV.value),
  })
}

// ─── YAML preview ─────────────────────────────────────────────────────────────

const KIND_TYPE_OPTIONS: Array<{ value: string; label: string }> = [
  { value: 'git_source',   label: 'git_source' },
  { value: 'build_image',  label: 'build_image' },
  { value: 'push_image',   label: 'push_image' },
  { value: 'deploy_ssh',   label: 'deploy_ssh' },
  { value: 'health_check', label: 'health_check' },
  { value: 'notify',       label: 'notify' },
  { value: 'custom',       label: 'custom' },
]
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
        <div style="position:relative">
          <select
            v-model="localType"
            class="drawer-select"
            aria-label="任务类型"
            @change="flush"
          >
            <option
              v-for="opt in KIND_TYPE_OPTIONS"
              :key="opt.value"
              :value="opt.value"
            >{{ opt.label }}</option>
            <!-- Allow custom types not in the list -->
            <option
              v-if="!KIND_TYPE_OPTIONS.some(o => o.value === localType) && localType"
              :value="localType"
            >{{ localType }}</option>
          </select>
        </div>
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

    <!-- Config KV section -->
    <div class="drawer-section">
      <div class="drawer-section-label">配置参数</div>

      <div v-if="localKV.length === 0" class="kv-empty">
        暂无配置项
      </div>

      <div
        v-for="row in localKV"
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
          @click="removeKV(row._key); flush()"
        >
          <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
            <path d="M18 6 6 18M6 6l12 12"/>
          </svg>
        </button>
      </div>

      <button class="kv-add-btn" @click="addKV">
        <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" aria-hidden="true">
          <path d="M12 5v14M5 12h14"/>
        </svg>
        添加配置项
      </button>
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
</style>
