<script setup lang="ts">
/**
 * CronPanel — scheduled (cron) trigger config for a project (Epic 8 · Story 8-6).
 *
 * Self-contained card with its own load/save against /api/projects/{id}/cron
 * (separate endpoint from the webhook trigger config). 5-field Vixie cron string;
 * quick-pick presets fill common schedules. nextRun is server-computed and shown
 * read-only after a successful save / load.
 *
 * Embedded inside TriggersPanel so it appears in both the standalone triggers page
 * and the pipeline "触发设置" tab.
 */
import { ref, computed, onMounted, watch } from 'vue'
import { getCron, saveCron, type CronConfig } from '../api/cron'
import { HttpError } from '../api/http'

const props = defineProps<{
  projectId: string
}>()

type LoadState = 'idle' | 'loading' | 'error'

const loadState = ref<LoadState>('idle')
const loadError = ref('')

const enabled = ref(false)
const expression = ref('')
const branch = ref('')
const nextRun = ref('')

const saveSubmitting = ref(false)
const saveBanner = ref('')
const saveSuccess = ref(false)

// ─── Quick-pick presets (5-field 分 时 日 月 周) ───────────────────────────────
const presets: ReadonlyArray<{ label: string; expr: string }> = [
  { label: '每 5 分钟', expr: '*/5 * * * *' },
  { label: '每小时', expr: '0 * * * *' },
  { label: '每天 02:00', expr: '0 2 * * *' },
  { label: '每周一 09:00', expr: '0 9 * * 1' },
  { label: '每月 1 号 00:00', expr: '0 0 1 * *' },
]

/** Format an RFC3339 nextRun into a local, readable string (empty stays empty). */
const nextRunLabel = computed<string>(() => {
  if (!nextRun.value) return ''
  const d = new Date(nextRun.value)
  if (Number.isNaN(d.getTime())) return nextRun.value
  return d.toLocaleString()
})

function applyConfig(c: CronConfig): void {
  enabled.value = c.enabled
  expression.value = c.expression
  branch.value = c.branch
  nextRun.value = c.nextRun
}

async function load(): Promise<void> {
  loadState.value = 'loading'
  loadError.value = ''
  try {
    applyConfig(await getCron(props.projectId))
    loadState.value = 'idle'
  } catch (err) {
    loadState.value = 'error'
    loadError.value = err instanceof HttpError ? err.message : '加载定时配置失败'
  }
}

function pickPreset(expr: string): void {
  expression.value = expr
  if (!enabled.value) enabled.value = true
  saveSuccess.value = false
  saveBanner.value = ''
}

async function handleSave(): Promise<void> {
  saveBanner.value = ''
  saveSuccess.value = false
  // Client guard mirrors backend: enabling requires a non-empty expression.
  if (enabled.value && !expression.value.trim()) {
    saveBanner.value = '启用定时触发须填写 cron 表达式'
    return
  }
  saveSubmitting.value = true
  try {
    applyConfig(
      await saveCron(props.projectId, {
        expression: expression.value.trim(),
        branch: branch.value.trim(),
        enabled: enabled.value,
      }),
    )
    saveSuccess.value = true
    saveBanner.value = '定时配置已保存'
  } catch (err) {
    saveSuccess.value = false
    if (err instanceof HttpError) {
      saveBanner.value =
        err.apiError?.code === 'invalid_cron'
          ? 'cron 表达式非法(须为 5 字段:分 时 日 月 周)'
          : err.message
    } else {
      saveBanner.value = '保存失败,请重试'
    }
  } finally {
    saveSubmitting.value = false
  }
}

onMounted(load)
watch(() => props.projectId, load)
</script>

<template>
  <section class="config-card" aria-labelledby="cron-heading">
    <div class="card-head">
      <span class="card-icon" aria-hidden="true">
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9">
          <circle cx="12" cy="12" r="9" /><polyline points="12 7 12 12 15.5 14" />
        </svg>
      </span>
      <h2 id="cron-heading" class="card-title">定时触发</h2>
      <span class="card-sub">按 cron 表达式周期性自动触发流水线</span>
      <label class="cron-switch" :title="enabled ? '已启用' : '已停用'">
        <input type="checkbox" v-model="enabled" :disabled="loadState !== 'idle'" />
        <span class="cron-switch-track" aria-hidden="true"><span class="cron-switch-thumb" /></span>
        <span class="cron-switch-label">{{ enabled ? '启用' : '停用' }}</span>
      </label>
    </div>

    <div class="card-body card-body--pad">
      <p v-if="loadState === 'loading'" class="cron-loading">加载中…</p>
      <p v-else-if="loadState === 'error'" class="cron-error" role="alert">{{ loadError }}</p>

      <template v-else>
        <!-- Expression -->
        <div class="cron-field">
          <label class="cron-label" for="cron-expr">
            cron 表达式
            <span class="cron-hint-inline">分 时 日 月 周</span>
          </label>
          <input
            id="cron-expr"
            v-model="expression"
            class="cron-input cron-input--mono"
            type="text"
            placeholder="0 2 * * *"
            autocomplete="off"
            spellcheck="false"
            :disabled="!enabled"
            @input="saveSuccess = false"
          />
          <div class="cron-presets">
            <button
              v-for="p in presets"
              :key="p.expr"
              type="button"
              class="cron-preset"
              :class="{ 'cron-preset--active': expression.trim() === p.expr }"
              :disabled="!enabled"
              @click="pickPreset(p.expr)"
            >{{ p.label }}</button>
          </div>
        </div>

        <!-- Branch -->
        <div class="cron-field">
          <label class="cron-label" for="cron-branch">
            分支
            <span class="cron-hint-inline">留空用项目默认分支</span>
          </label>
          <input
            id="cron-branch"
            v-model="branch"
            class="cron-input cron-input--mono"
            type="text"
            placeholder="main"
            autocomplete="off"
            :disabled="!enabled"
          />
        </div>

        <!-- Next run preview -->
        <p class="cron-next" :class="{ 'cron-next--muted': !nextRunLabel }">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
            <circle cx="12" cy="12" r="9" /><polyline points="12 7 12 12 15.5 14" />
          </svg>
          <template v-if="nextRunLabel">下次触发:<strong>{{ nextRunLabel }}</strong></template>
          <template v-else-if="enabled">保存后显示下次触发时间</template>
          <template v-else>定时触发已停用</template>
        </p>

        <!-- Save banner -->
        <p
          v-if="saveBanner"
          class="cron-banner"
          :class="saveSuccess ? 'cron-banner--ok' : 'cron-banner--err'"
          role="status"
        >{{ saveBanner }}</p>

        <div class="cron-save">
          <button class="btn-primary" :disabled="saveSubmitting" :aria-busy="saveSubmitting" @click="handleSave">
            <span v-if="saveSubmitting" class="spinner" aria-hidden="true" />
            {{ saveSubmitting ? '保存中…' : '保存定时配置' }}
          </button>
        </div>
      </template>
    </div>
  </section>
</template>

<style scoped>
.config-card {
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-lg, 12px);
  background: var(--color-card);
  overflow: hidden;
}
.card-head {
  display: flex;
  align-items: center;
  gap: 9px;
  padding: 14px 16px;
  border-bottom: 1px solid var(--color-border);
}
.card-icon {
  display: grid;
  place-items: center;
  width: 26px;
  height: 26px;
  border-radius: var(--rounded-md);
  background: var(--color-primary-soft);
  color: var(--color-primary);
  flex: none;
}
.card-title { font-size: 0.92rem; font-weight: 650; color: var(--color-text); }
.card-sub { font-size: 0.76rem; color: var(--color-faint); flex: 1; min-width: 0; }

/* ─── Enable switch ───────────────────────────────────────────────────────── */
.cron-switch { display: inline-flex; align-items: center; gap: 7px; cursor: pointer; flex: none; }
.cron-switch input { position: absolute; opacity: 0; width: 0; height: 0; }
.cron-switch-track {
  position: relative;
  width: 34px;
  height: 19px;
  border-radius: 10px;
  background: var(--color-border-strong);
  transition: background-color var(--duration-fast);
}
.cron-switch-thumb {
  position: absolute;
  top: 2px;
  left: 2px;
  width: 15px;
  height: 15px;
  border-radius: 50%;
  background: #fff;
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.3);
  transition: transform var(--duration-fast);
}
.cron-switch input:checked + .cron-switch-track { background: var(--color-primary); }
.cron-switch input:checked + .cron-switch-track .cron-switch-thumb { transform: translateX(15px); }
.cron-switch input:focus-visible + .cron-switch-track { outline: 2px solid var(--color-primary); outline-offset: 2px; }
.cron-switch-label { font-size: 0.78rem; font-weight: 600; color: var(--color-dim); }

.card-body--pad { padding: 16px; display: flex; flex-direction: column; gap: 14px; }
.cron-loading, .cron-error { margin: 0; font-size: 0.82rem; }
.cron-error { color: var(--color-danger, #dc2626); }
.cron-loading { color: var(--color-faint); }

.cron-field { display: flex; flex-direction: column; gap: 7px; }
.cron-label { font-size: 0.8rem; font-weight: 600; color: var(--color-text); }
.cron-hint-inline { font-weight: 400; color: var(--color-faint); margin-left: 6px; }
.cron-input {
  width: 100%;
  height: 36px;
  padding: 0 11px;
  font: inherit;
  font-size: 0.85rem;
  color: var(--color-text);
  background: var(--color-bg-subtle, var(--color-card));
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded-md);
  box-sizing: border-box;
  transition: border-color var(--duration-fast);
}
.cron-input:focus { outline: none; border-color: var(--color-primary); }
.cron-input:disabled { opacity: 0.55; cursor: not-allowed; }
.cron-input--mono { font-family: var(--font-mono, ui-monospace, monospace); letter-spacing: 0.02em; }

.cron-presets { display: flex; flex-wrap: wrap; gap: 6px; }
.cron-preset {
  height: 26px;
  padding: 0 10px;
  font: inherit;
  font-size: 0.74rem;
  font-weight: 500;
  color: var(--color-dim);
  background: none;
  border: 1px solid var(--color-border-strong);
  border-radius: 13px;
  cursor: pointer;
  transition: border-color var(--duration-fast), color var(--duration-fast), background-color var(--duration-fast);
}
.cron-preset:hover:not(:disabled) { border-color: var(--color-primary); color: var(--color-primary); }
.cron-preset--active { border-color: var(--color-primary); color: var(--color-primary); background: var(--color-primary-soft); }
.cron-preset:disabled { opacity: 0.5; cursor: not-allowed; }

.cron-next {
  display: flex;
  align-items: center;
  gap: 6px;
  margin: 0;
  font-size: 0.8rem;
  color: var(--color-text);
}
.cron-next svg { flex: none; color: var(--color-primary); }
.cron-next strong { font-weight: 650; }
.cron-next--muted { color: var(--color-faint); }
.cron-next--muted svg { color: var(--color-faint); }

.cron-banner { margin: 0; font-size: 0.8rem; font-weight: 500; }
.cron-banner--ok { color: var(--color-success, #16a34a); }
.cron-banner--err { color: var(--color-danger, #dc2626); }

.cron-save { display: flex; justify-content: flex-end; }
</style>
