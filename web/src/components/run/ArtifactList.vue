<!--
  ArtifactList.vue — Story 3-4: 构建产物列表(FR-6)

  渲染一次成功运行产出的构建产物:type 徽标(image/jar/dist/archive 各色)+ name +
  reference(可复制)+ 人读大小。产物契约是 Epic 3 一等交付物:type 枚举 + reference
  类型寻址定死,Epic 4 部署按 (type, reference) 消费。桩产物(metadata.stub=true)诚实标注。

  纯展示组件(container/presentational split):仅接收 artifacts prop,不自取数据。
  RunDetail 成功态据 run.artifacts 渲染;不扰终端(3-6)/诊断(7-2)slot。
-->
<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ArtifactDTO, ArtifactType } from '../../api/runs'

const { t } = useI18n()

const props = defineProps<{
  artifacts: ArtifactDTO[]
  runId: string
}>()

// 已归档真字节(metadata.stored=true 且有 reference 句柄)才可下载;占位/镜像类不可。
function downloadable(a: ArtifactDTO): boolean {
  return a.metadata?.stored === true && !!a.reference
}

function downloadUrl(a: ArtifactDTO): string {
  return `/api/runs/${props.runId}/artifacts/${a.id}/download`
}

// 来源节点(阶段 · 节点名),由后端写进 metadata.sourceStage/sourceJob;标注「哪个节点产的」。
function sourceLabel(a: ArtifactDTO): string {
  const stage = typeof a.metadata?.sourceStage === 'string' ? a.metadata.sourceStage : ''
  const job = typeof a.metadata?.sourceJob === 'string' ? a.metadata.sourceJob : ''
  if (stage && job) return `${stage} · ${job}`
  return job || stage || ''
}

// ─── Type badge config (each type its own color — semantic, not decorative) ──

interface TypeConfig {
  label: string
  fg: string
  bg: string
  line: string
  icon: 'image' | 'jar' | 'dist' | 'archive'
}

const TYPE_CONFIG: Record<ArtifactType, TypeConfig> = {
  image:   { label: t('run.artifactTypeImage'), fg: 'var(--color-primary)', bg: 'var(--color-primary-soft)', line: 'var(--color-primary)', icon: 'image'   },
  jar:     { label: 'JAR',     fg: 'var(--color-amber)',   bg: 'var(--color-amber-soft)',   line: 'var(--color-amber-line)',   icon: 'jar'     },
  dist:    { label: 'dist',    fg: 'var(--color-cyan)',    bg: 'var(--color-cyan-soft)',    line: 'var(--color-cyan-line)',    icon: 'dist'    },
  archive: { label: 'archive', fg: 'var(--color-green)',   bg: 'var(--color-green-soft)',   line: 'var(--color-green-line)',   icon: 'archive' },
}

function typeConfig(type: string): TypeConfig {
  return TYPE_CONFIG[type as ArtifactType] ?? {
    label: type, fg: 'var(--color-dim)', bg: 'var(--color-card-2)', line: 'var(--color-border-strong)', icon: 'archive',
  }
}

// ─── Human-readable size ──────────────────────────────────────────────────────

function humanSize(bytes: number): string {
  if (!bytes || bytes <= 0) return t('run.sizeUnknown')
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let v = bytes
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  const fixed = i === 0 ? String(Math.round(v)) : v.toFixed(v < 10 ? 1 : 0)
  return `${fixed} ${units[i]}`
}

// ─── stub flag (honest labelling of synthesized artifacts) ───────────────────

function isStub(a: ArtifactDTO): boolean {
  return a.metadata?.stub === true
}

// ─── copy reference ───────────────────────────────────────────────────────────

const copiedId = ref<string | null>(null)
let copyTimer: ReturnType<typeof setTimeout> | null = null

async function copyReference(a: ArtifactDTO): Promise<void> {
  try {
    await navigator.clipboard.writeText(a.reference)
    copiedId.value = a.id
    if (copyTimer) clearTimeout(copyTimer)
    copyTimer = setTimeout(() => { copiedId.value = null }, 1600)
  } catch {
    // Clipboard unavailable (insecure context / denied) — silent; ref still visible to select.
  }
}
</script>

<template>
  <section class="artifact-list" role="region" :aria-label="t('run.artifacts')">
    <header class="al-head">
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
        <path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z"/>
        <path d="M3.27 6.96 12 12.01l8.73-5.05M12 22.08V12"/>
      </svg>
      <h3 class="al-title">{{ t('run.artifacts') }}</h3>
      <span class="al-count">{{ artifacts.length }}</span>
    </header>

    <!-- Empty (success but no artifacts emitted) -->
    <p v-if="artifacts.length === 0" class="al-empty">{{ t('run.artifactsEmpty') }}</p>

    <ul v-else class="al-items" role="list">
      <li
        v-for="a in artifacts"
        :key="a.id"
        class="artifact-card"
        :style="{ '--type-line': typeConfig(a.type).line }"
      >
        <!-- Type badge -->
        <span
          class="type-badge"
          :style="{ color: typeConfig(a.type).fg, background: typeConfig(a.type).bg }"
          :aria-label="t('run.artifactTypeAria', { label: typeConfig(a.type).label })"
        >
          <!-- image -->
          <svg v-if="typeConfig(a.type).icon === 'image'" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" aria-hidden="true">
            <rect x="3" y="3" width="18" height="18" rx="2"/><circle cx="9" cy="9" r="2"/><path d="m21 15-3.5-3.5L7 22"/>
          </svg>
          <!-- jar (coffee) -->
          <svg v-else-if="typeConfig(a.type).icon === 'jar'" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" aria-hidden="true">
            <path d="M17 8h1a4 4 0 1 1 0 8h-1M3 8h14v9a4 4 0 0 1-4 4H7a4 4 0 0 1-4-4z"/><path d="M6 2v2M10 2v2M14 2v2"/>
          </svg>
          <!-- dist (layers) -->
          <svg v-else-if="typeConfig(a.type).icon === 'dist'" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" aria-hidden="true">
            <path d="m12 2 9 5-9 5-9-5 9-5z"/><path d="m3 12 9 5 9-5M3 17l9 5 9-5"/>
          </svg>
          <!-- archive -->
          <svg v-else width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" aria-hidden="true">
            <rect x="2" y="4" width="20" height="5" rx="1"/><path d="M4 9v10a1 1 0 0 0 1 1h14a1 1 0 0 0 1-1V9M10 13h4"/>
          </svg>
          {{ typeConfig(a.type).label }}
        </span>

        <!-- Name + reference -->
        <div class="artifact-main">
          <div class="artifact-name-row">
            <span class="artifact-name">{{ a.name }}</span>
            <span v-if="sourceLabel(a)" class="source-tag" :title="t('run.sourceNodeTitle', { label: sourceLabel(a) })">
              <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                <circle cx="6" cy="6" r="2.5"/><circle cx="6" cy="18" r="2.5"/><path d="M6 8.5v7M18 8a4 4 0 0 1-4 4H8.5"/><circle cx="18" cy="6" r="2.5"/>
              </svg>
              {{ sourceLabel(a) }}
            </span>
            <span v-if="isStub(a)" class="stub-tag" :title="t('run.stubTitle')">{{ t('run.stubTag') }}</span>
          </div>
          <button
            type="button"
            class="reference-row"
            :title="t('run.copyReferenceTitle', { ref: a.reference })"
            :aria-label="t('run.copyReferenceAria', { ref: a.reference })"
            @click="copyReference(a)"
          >
            <code class="reference mono">{{ a.reference }}</code>
            <span class="copy-icon" aria-hidden="true">
              <svg v-if="copiedId === a.id" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2">
                <path d="M20 6 9 17l-5-5"/>
              </svg>
              <svg v-else width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8">
                <rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/>
              </svg>
            </span>
            <span v-if="copiedId === a.id" class="copied-hint" role="status">{{ t('run.copied') }}</span>
          </button>
        </div>

        <!-- Size -->
        <span class="artifact-size mono" :aria-label="t('run.sizeAria', { size: humanSize(a.sizeBytes) })">{{ humanSize(a.sizeBytes) }}</span>

        <!-- Download (仅已归档真字节可下载) -->
        <a
          v-if="downloadable(a)"
          class="download-btn"
          :href="downloadUrl(a)"
          download
          :title="t('run.downloadTitle', { name: a.name })"
          :aria-label="t('run.downloadTitle', { name: a.name })"
        >
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" aria-hidden="true">
            <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4M7 10l5 5 5-5M12 15V3"/>
          </svg>
          {{ t('run.download') }}
        </a>
      </li>
    </ul>
  </section>
</template>

<style scoped>
.artifact-list {
  border: 1px solid var(--color-border);
  border-radius: var(--rounded);
  background: var(--color-card);
  overflow: hidden;
}

.al-head {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 11px 16px;
  background: var(--color-inset);
  border-bottom: 1px solid var(--color-border);
  color: var(--color-dim);
}

.al-title {
  font-size: 0.82rem;
  font-weight: 600;
  color: var(--color-text);
  letter-spacing: -0.01em;
  flex: 1;
}

.al-count {
  font-size: 0.72rem;
  font-weight: 600;
  min-width: 20px;
  height: 20px;
  padding: 0 6px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: var(--rounded-full);
  background: var(--color-card-2);
  color: var(--color-faint);
}

.al-empty {
  padding: 22px 16px;
  font-size: 0.82rem;
  color: var(--color-faint);
  text-align: center;
}

.al-items {
  list-style: none;
  display: flex;
  flex-direction: column;
}

/* ─── artifact card ──────────────────────────────────────────────────────── */
.artifact-card {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 13px 16px;
  border-bottom: 1px solid var(--color-border);
  border-left: 3px solid var(--type-line);
  transition: background-color var(--duration-fast);
}

.artifact-card:last-child { border-bottom: none; }

.artifact-card:hover {
  background: var(--color-inset);
}

/* ─── type badge ─────────────────────────────────────────────────────────── */
.type-badge {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 4px 9px;
  border-radius: var(--rounded-md);
  font-size: 0.74rem;
  font-weight: 600;
  white-space: nowrap;
  flex-shrink: 0;
  min-width: 74px;
}

/* ─── main: name + reference ─────────────────────────────────────────────── */
.artifact-main {
  display: flex;
  flex-direction: column;
  gap: 5px;
  flex: 1;
  min-width: 0;
}

.artifact-name-row {
  display: flex;
  align-items: center;
  gap: 7px;
  min-width: 0;
}

.artifact-name {
  font-size: 0.86rem;
  font-weight: 600;
  color: var(--color-text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.stub-tag {
  flex-shrink: 0;
  font-size: 0.64rem;
  font-weight: 600;
  line-height: 1;
  padding: 2px 5px;
  border-radius: var(--rounded-sm);
  background: var(--color-amber-soft);
  color: var(--color-amber);
  border: 1px solid var(--color-amber-line);
}

/* 来源节点标签(哪个节点产的) */
.source-tag {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  flex-shrink: 0;
  font-size: 0.7rem;
  font-weight: 500;
  line-height: 1;
  padding: 3px 7px;
  border-radius: var(--rounded-sm);
  background: var(--color-inset);
  color: var(--color-faint);
  border: 1px solid var(--color-border);
  white-space: nowrap;
}

.source-tag svg { opacity: 0.8; }

/* ─── reference (copyable) ───────────────────────────────────────────────── */
.reference-row {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  max-width: 100%;
  background: var(--color-inset);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-md);
  padding: 4px 8px;
  cursor: pointer;
  font-family: var(--font-sans);
  transition: border-color var(--duration-fast), background-color var(--duration-fast);
  text-align: left;
  align-self: flex-start;
  min-width: 0;
}

.reference-row:hover {
  border-color: var(--color-border-strong);
  background: var(--color-card-2);
}

.reference-row:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}

.reference {
  font-size: 0.76rem;
  color: var(--color-dim);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  min-width: 0;
}

.copy-icon {
  display: inline-flex;
  flex-shrink: 0;
  color: var(--color-faint);
}

.reference-row:hover .copy-icon { color: var(--color-primary); }

.copied-hint {
  font-size: 0.7rem;
  color: var(--color-green);
  font-weight: 600;
  flex-shrink: 0;
}

/* ─── size ───────────────────────────────────────────────────────────────── */
.artifact-size {
  font-size: 0.78rem;
  color: var(--color-faint);
  flex-shrink: 0;
  min-width: 60px;
  text-align: right;
}

.mono { font-family: var(--font-mono); }

/* ─── download button ────────────────────────────────────────────────────── */
.download-btn {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  flex-shrink: 0;
  padding: 5px 11px;
  border-radius: var(--rounded-md);
  border: 1px solid var(--color-border-strong);
  background: var(--color-card-2);
  color: var(--color-text);
  font-size: 0.78rem;
  font-weight: 600;
  text-decoration: none;
  transition: border-color var(--duration-fast), background-color var(--duration-fast), color var(--duration-fast);
}

.download-btn:hover {
  border-color: var(--color-primary);
  background: var(--color-primary-soft);
  color: var(--color-primary);
}

.download-btn:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}

@media (max-width: 640px) {
  .artifact-card {
    flex-wrap: wrap;
    gap: 8px 12px;
  }
  .artifact-size {
    text-align: left;
    min-width: 0;
  }
}
</style>
