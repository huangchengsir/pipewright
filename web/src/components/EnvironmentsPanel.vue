<script setup lang="ts">
/**
 * EnvironmentsPanel — environment chain configuration (Epic 8 · Story 8-7 / FR-8-7).
 *
 * Lets the user define an ordered promotion chain (e.g. dev → staging → prod)
 * with per-stage gated toggle and per-environment plain variables.
 *
 * Secret variables are intentionally read-only here (they reference vault
 * credentials by ID; editing them is out of scope for this panel).
 *
 * Used by:
 *   - TriggersPanel.vue (embedded as a card after cron config)
 *   - Potentially the pipeline settings tab
 */
import { ref, onMounted, watch } from 'vue'
import {
  getEnvironments,
  saveEnvironments,
  type EnvStage,
  type EnvVariable,
} from '../api/promotion'
import { validateEnvName, findDuplicateEnvName } from '../api/promotion.helpers'
import { HttpError } from '../api/http'

// ─── Props ────────────────────────────────────────────────────────────────────

const props = defineProps<{
  projectId: string
}>()

// ─── Load state ───────────────────────────────────────────────────────────────

type LoadState = 'idle' | 'loading' | 'error'
const loadState = ref<LoadState>('idle')
const loadError = ref('')

// ─── Environment chain state ──────────────────────────────────────────────────

interface EnvRow {
  _key: number
  name: string
  gated: boolean
  nameError: string
}

let _keySeq = 0
const envRows = ref<EnvRow[]>([])

// Per-env variables (keyed by env name)
const variablesByEnv = ref<Record<string, EnvVariable[]>>({})
// Which env has its variables expanded
const expandedEnv = ref<string | null>(null)

// ─── Variable editing ─────────────────────────────────────────────────────────

interface VarDraft {
  key: string
  value: string
}

// Draft vars for currently expanded env
const varDrafts = ref<VarDraft[]>([])
let _varKeySeq = 0

function expandVars(envName: string): void {
  if (expandedEnv.value === envName) {
    expandedEnv.value = null
    varDrafts.value = []
    return
  }
  expandedEnv.value = envName
  // Populate drafts from existing vars (non-secret only)
  const existing = (variablesByEnv.value[envName] ?? []).filter((v) => !v.secret)
  varDrafts.value = existing.map((v) => ({ key: v.key, value: v.value }))
  if (varDrafts.value.length === 0) {
    addVarRow()
  }
}

function addVarRow(): void {
  _varKeySeq++
  varDrafts.value.push({ key: '', value: '' })
}

function removeVarRow(idx: number): void {
  varDrafts.value.splice(idx, 1)
}

// ─── Chain editing ────────────────────────────────────────────────────────────

function addEnvRow(): void {
  envRows.value.push({ _key: ++_keySeq, name: '', gated: false, nameError: '' })
}

function removeEnvRow(key: number): void {
  const row = envRows.value.find((r) => r._key === key)
  if (row && expandedEnv.value === row.name) {
    expandedEnv.value = null
    varDrafts.value = []
  }
  envRows.value = envRows.value.filter((r) => r._key !== key)
}

function moveUp(idx: number): void {
  if (idx <= 0) return
  const arr = [...envRows.value]
  ;[arr[idx - 1], arr[idx]] = [arr[idx], arr[idx - 1]]
  envRows.value = arr
}

function moveDown(idx: number): void {
  if (idx >= envRows.value.length - 1) return
  const arr = [...envRows.value]
  ;[arr[idx], arr[idx + 1]] = [arr[idx + 1], arr[idx]]
  envRows.value = arr
}

function onNameInput(row: EnvRow): void {
  if (row.nameError) row.nameError = validateEnvName(row.name)
}

// ─── Save ─────────────────────────────────────────────────────────────────────

const saveSubmitting = ref(false)
const saveBanner = ref('')
const saveSuccess = ref(false)
let saveSuccessTimer: ReturnType<typeof setTimeout> | null = null

function showSaveSuccess(): void {
  saveSuccess.value = true
  if (saveSuccessTimer) clearTimeout(saveSuccessTimer)
  saveSuccessTimer = setTimeout(() => {
    saveSuccess.value = false
  }, 3200)
}

// Flush current varDrafts back into variablesByEnv before saving
function flushVarDrafts(): void {
  if (!expandedEnv.value) return
  const env = expandedEnv.value
  const existing = (variablesByEnv.value[env] ?? []).filter((v) => v.secret)
  const nonEmpty = varDrafts.value.filter((d) => d.key.trim() !== '')
  variablesByEnv.value = {
    ...variablesByEnv.value,
    [env]: [
      ...existing,
      ...nonEmpty.map((d) => ({
        key: d.key.trim(),
        value: d.value,
        secret: false,
        credentialId: '',
      })),
    ],
  }
}

async function handleSave(): Promise<void> {
  // Validate all env names
  let hasError = false
  for (const row of envRows.value) {
    row.nameError = validateEnvName(row.name)
    if (row.nameError) hasError = true
  }
  if (hasError) return

  // Check for duplicates
  const dup = findDuplicateEnvName(envRows.value.map((r) => r.name))
  if (dup) {
    saveBanner.value = `环境名重复:「${dup}」`
    return
  }

  // Flush draft vars
  flushVarDrafts()

  saveSubmitting.value = true
  saveBanner.value = ''
  saveSuccess.value = false

  const stages: EnvStage[] = envRows.value.map((r) => ({
    name: r.name.trim(),
    gated: r.gated,
  }))

  // Build variables map — only include envs that are in the chain
  const variables: Record<string, Array<{ key: string; value: string; secret: boolean; credentialId?: string }>> = {}
  for (const s of stages) {
    const vars = variablesByEnv.value[s.name] ?? []
    if (vars.length > 0) {
      variables[s.name] = vars.map((v) => ({
        key: v.key,
        value: v.value,
        secret: v.secret,
        credentialId: v.credentialId || undefined,
      }))
    }
  }

  try {
    const res = await saveEnvironments(props.projectId, {
      environments: stages,
      variables: Object.keys(variables).length > 0 ? variables : undefined,
    })
    // Update from server response
    applyChain(res.environments, variablesByEnv.value)
    showSaveSuccess()
  } catch (err) {
    if (err instanceof HttpError) {
      if (err.status === 0) {
        saveBanner.value = '无法连接到服务器,请稍后重试。'
      } else if (err.status === 422 || err.status === 400) {
        saveBanner.value = err.apiError?.message ?? `保存失败(${err.status}):配置数据不合法`
      } else {
        saveBanner.value = err.apiError?.message ?? `保存失败(${err.status})`
      }
    } else {
      saveBanner.value = '保存失败,请稍后重试。'
    }
  } finally {
    saveSubmitting.value = false
  }
}

// ─── Data loading ─────────────────────────────────────────────────────────────

function applyChain(
  envs: EnvStage[],
  vars: Record<string, EnvVariable[]>,
): void {
  envRows.value = envs.map((e) => ({
    _key: ++_keySeq,
    name: e.name,
    gated: e.gated,
    nameError: '',
  }))
  variablesByEnv.value = vars
}

async function loadEnvironments(): Promise<void> {
  loadState.value = 'loading'
  loadError.value = ''
  try {
    const res = await getEnvironments(props.projectId)
    applyChain(res.environments, res.variables)
    loadState.value = 'idle'
  } catch (err) {
    if (err instanceof HttpError) {
      if (err.status === 0) {
        loadError.value = '无法连接到服务器,请检查后端是否运行后重试。'
      } else if (err.status === 404) {
        loadError.value = '项目不存在,请确认项目 ID 正确。'
      } else {
        loadError.value = err.apiError?.message ?? `加载环境配置失败(${err.status})`
      }
    } else {
      loadError.value = '加载环境配置失败,请稍后重试。'
    }
    loadState.value = 'error'
  }
}

watch(() => props.projectId, loadEnvironments)
onMounted(loadEnvironments)
</script>

<template>
  <div class="env-panel">

    <!-- ─── Load error ──────────────────────────────────────────────────── -->
    <div v-if="loadState === 'error'" class="ep-banner ep-banner--error" role="alert">
      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
        <circle cx="12" cy="12" r="9"/><path d="M12 8v4M12 16h.01"/>
      </svg>
      <span>{{ loadError }}</span>
      <button class="ep-banner-retry" @click="loadEnvironments">↻ 重试</button>
    </div>

    <!-- ─── Loading skeleton ────────────────────────────────────────────── -->
    <template v-if="loadState === 'loading'">
      <div class="ep-skel-card" aria-busy="true" aria-label="加载中">
        <div class="ep-skel ep-skel--title" aria-hidden="true" />
        <div class="ep-skel ep-skel--row" aria-hidden="true" />
        <div class="ep-skel ep-skel--row" aria-hidden="true" />
      </div>
    </template>

    <!-- ─── Main config ─────────────────────────────────────────────────── -->
    <template v-else-if="loadState === 'idle'">

      <!-- Save error/success banners -->
      <div v-if="saveBanner" class="ep-banner ep-banner--error" role="alert" aria-live="assertive">
        <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <circle cx="12" cy="12" r="9"/><path d="M12 8v4M12 16h.01"/>
        </svg>
        <span>{{ saveBanner }}</span>
      </div>
      <div v-if="saveSuccess" class="ep-banner ep-banner--success" role="status" aria-live="polite">
        <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true">
          <path d="M20 6 9 17l-5-5"/>
        </svg>
        <span>环境链已保存</span>
      </div>

      <!-- ═══ Environment chain card ══════════════════════════════════════ -->
      <section class="config-card" aria-labelledby="ep-chain-heading">
        <div class="card-head">
          <span class="card-icon" aria-hidden="true">
            <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9">
              <path d="M22 11.08V12a10 10 0 1 1-5.93-9.14"/><path d="M22 4 12 14.01l-3-3"/>
            </svg>
          </span>
          <h2 id="ep-chain-heading" class="card-title">环境晋级链</h2>
          <span class="card-sub">定义有序的晋级路径 · 可选审批门</span>
        </div>

        <!-- Chain flow diagram (read-only visual) -->
        <div v-if="envRows.length > 0" class="chain-flow" aria-label="环境链顺序预览" role="img">
          <template v-for="(row, idx) in envRows" :key="row._key">
            <div
              class="chain-node"
              :class="{ 'chain-node--gated': row.gated }"
              :aria-label="`环境 ${row.name || '(未命名)'}${row.gated ? ' · 需审批' : ''}`"
            >
              <span class="chain-node-name">{{ row.name || '…' }}</span>
              <span v-if="row.gated" class="chain-node-gate" aria-hidden="true">
                <svg width="9" height="9" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2">
                  <rect x="3" y="11" width="18" height="10" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/>
                </svg>
              </span>
            </div>
            <div v-if="idx < envRows.length - 1" class="chain-arrow" aria-hidden="true">
              <svg width="16" height="10" viewBox="0 0 16 10" fill="none">
                <path d="M0 5h14M10 1l4 4-4 4" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
              </svg>
            </div>
          </template>
        </div>
        <div v-else class="chain-flow chain-flow--empty">
          <span class="chain-empty-text">尚未定义环境链，请在下方添加</span>
        </div>

        <!-- Chain rows (editable) -->
        <div class="chain-table-head" aria-hidden="true">
          <span>序号</span><span>环境名</span><span>审批门</span><span>变量</span><span>排序</span><span></span>
        </div>

        <div v-if="envRows.length === 0" class="chain-empty-rows">
          <span>暂无环境,点下方「添加环境」开始配置</span>
        </div>

        <div
          v-for="(row, idx) in envRows"
          :key="row._key"
          class="chain-row"
          :class="{ 'chain-row--expanded': expandedEnv === row.name }"
        >
          <!-- Main row -->
          <div class="chain-row-main">
            <!-- Index badge -->
            <div class="chain-idx">
              <span class="chain-idx-num">{{ idx + 1 }}</span>
            </div>

            <!-- Name input -->
            <div class="chain-cell chain-cell--name">
              <input
                v-model="row.name"
                type="text"
                class="chain-input"
                :class="{ 'chain-input--error': row.nameError }"
                :placeholder="`env-${idx + 1}`"
                :aria-label="`环境名称(第 ${idx + 1} 行)`"
                :aria-invalid="row.nameError ? 'true' : undefined"
                maxlength="64"
                @input="onNameInput(row)"
                @blur="row.nameError = validateEnvName(row.name)"
              />
              <span v-if="row.nameError" class="chain-field-error" role="alert">{{ row.nameError }}</span>
            </div>

            <!-- Gated toggle -->
            <div class="chain-cell chain-cell--gated">
              <button
                class="gate-toggle"
                :class="{ 'gate-toggle--on': row.gated }"
                role="switch"
                :aria-checked="row.gated"
                :aria-label="`${row.name || '该环境'}审批门:${row.gated ? '已开启' : '已关闭'}`"
                @click="row.gated = !row.gated"
              >
                <span class="gate-toggle-knob" aria-hidden="true" />
              </button>
              <span class="gate-label">{{ row.gated ? '需审批' : '直通' }}</span>
            </div>

            <!-- Variables toggle -->
            <div class="chain-cell chain-cell--vars">
              <button
                class="vars-btn"
                :class="{ 'vars-btn--active': expandedEnv === row.name }"
                :aria-expanded="expandedEnv === row.name"
                :aria-label="`${row.name || '该环境'}变量 · ${(variablesByEnv[row.name] ?? []).length} 条`"
                @click="expandVars(row.name)"
              >
                <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                  <circle cx="12" cy="12" r="1"/><circle cx="12" cy="5" r="1"/><circle cx="12" cy="19" r="1"/>
                </svg>
                <span class="vars-btn-count">{{ (variablesByEnv[row.name] ?? []).length }}</span>
              </button>
            </div>

            <!-- Move up / down -->
            <div class="chain-cell chain-cell--order">
              <button
                class="order-btn"
                :disabled="idx === 0"
                :aria-label="`将 ${row.name || '该环境'} 上移`"
                @click="moveUp(idx)"
              >
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true">
                  <path d="M18 15l-6-6-6 6"/>
                </svg>
              </button>
              <button
                class="order-btn"
                :disabled="idx === envRows.length - 1"
                :aria-label="`将 ${row.name || '该环境'} 下移`"
                @click="moveDown(idx)"
              >
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true">
                  <path d="M6 9l6 6 6-6"/>
                </svg>
              </button>
            </div>

            <!-- Delete -->
            <div class="chain-cell chain-cell--del">
              <button
                class="del-btn"
                :aria-label="`删除环境「${row.name || '(未命名)'}」`"
                @click="removeEnvRow(row._key)"
              >
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" aria-hidden="true">
                  <path d="M3 6h18M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/><path d="M8 6V4a1 1 0 0 1 1-1h6a1 1 0 0 1 1 1v2"/>
                </svg>
              </button>
            </div>
          </div>

          <!-- Expanded variables panel -->
          <div v-if="expandedEnv === row.name" class="vars-panel" role="region" :aria-label="`${row.name} 变量`">
            <!-- Secret vars (read-only display) -->
            <div
              v-for="sv in (variablesByEnv[row.name] ?? []).filter(v => v.secret)"
              :key="sv.key"
              class="var-row var-row--secret"
            >
              <span class="var-key mono">{{ sv.key }}</span>
              <span class="var-val-placeholder">
                <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                  <rect x="3" y="11" width="18" height="10" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/>
                </svg>
                密钥 · 引用 vault 凭据
              </span>
              <span class="var-secret-badge">Secret</span>
            </div>

            <!-- Plain var drafts (editable) -->
            <div v-for="(draft, dIdx) in varDrafts" :key="dIdx" class="var-row var-row--edit">
              <input
                v-model="draft.key"
                type="text"
                class="var-input var-input--key mono"
                placeholder="KEY"
                :aria-label="`变量键(第 ${dIdx + 1} 行)`"
              />
              <span class="var-eq" aria-hidden="true">=</span>
              <input
                v-model="draft.value"
                type="text"
                class="var-input var-input--val"
                placeholder="VALUE"
                :aria-label="`变量值(第 ${dIdx + 1} 行)`"
              />
              <button
                class="var-del-btn"
                :aria-label="`删除变量行 ${dIdx + 1}`"
                @click="removeVarRow(dIdx)"
              >
                <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                  <path d="M18 6 6 18M6 6l12 12"/>
                </svg>
              </button>
            </div>

            <button class="var-add-btn" @click="addVarRow">
              <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" aria-hidden="true">
                <path d="M12 5v14M5 12h14"/>
              </svg>
              添加变量
            </button>
            <p class="vars-hint">Secret 变量须在保险库中创建凭据后引用;此处只可编辑明文变量。</p>
          </div>
        </div>

        <button class="add-env-btn" @click="addEnvRow">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" aria-hidden="true">
            <path d="M12 5v14M5 12h14"/>
          </svg>
          添加环境
        </button>

        <p class="chain-note">
          链上顺序即晋级顺序;开启审批门的环境在晋级时须有人批准后方可继续。
          变量仅注入到对应环境的运行,互不透传。
        </p>
      </section>

      <!-- ═══ Save bar ═══════════════════════════════════════════════════ -->
      <div class="ep-save-bar">
        <button
          class="ep-btn-primary"
          :disabled="saveSubmitting"
          :aria-busy="saveSubmitting"
          @click="handleSave"
        >
          <span v-if="saveSubmitting" class="ep-spinner" aria-hidden="true" />
          {{ saveSubmitting ? '保存中…' : '保存环境配置' }}
        </button>
      </div>

    </template>
  </div>
</template>

<style scoped>
.env-panel {
  display: flex;
  flex-direction: column;
  gap: var(--card-gap);
}

/* ─── Banner ──────────────────────────────────────── */
.ep-banner {
  display: flex;
  align-items: flex-start;
  gap: 9px;
  padding: 11px 14px;
  border-radius: var(--rounded);
  font-size: 0.83rem;
  line-height: 1.5;
}
.ep-banner--error {
  background: var(--color-red-soft);
  border: 1px solid var(--color-red-line);
  color: var(--color-red);
}
.ep-banner--success {
  background: var(--color-green-soft);
  border: 1px solid var(--color-green-line);
  color: var(--color-green);
}
.ep-banner-retry {
  margin-left: auto;
  flex-shrink: 0;
  background: none;
  border: none;
  color: var(--color-red);
  font-size: 0.83rem;
  font-weight: 600;
  cursor: pointer;
  padding: 0;
  text-decoration: underline;
  text-underline-offset: 2px;
}

/* ─── Skeleton ────────────────────────────────────── */
.ep-skel-card {
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-card);
  padding: 18px;
  display: flex;
  flex-direction: column;
  gap: 14px;
}
.ep-skel {
  display: block;
  background: linear-gradient(90deg, var(--color-inset) 0%, oklch(100% 0 0 / 0.06) 50%, var(--color-inset) 100%);
  background-size: 200% 100%;
  border-radius: var(--rounded-md);
  animation: ep-shimmer 1.4s ease-in-out infinite;
}
@keyframes ep-shimmer {
  0%   { background-position: 200% center; }
  100% { background-position: -200% center; }
}
@media (prefers-reduced-motion: reduce) { .ep-skel { animation: none; background: var(--color-inset); } }
.ep-skel--title { height: 16px; width: 38%; }
.ep-skel--row   { height: 40px; width: 100%; }

/* ─── Config card ─────────────────────────────────── */
.config-card {
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-card);
  box-shadow: var(--shadow);
  overflow: hidden;
  animation: ep-card-in 0.45s var(--ease-out-expo) both;
}
@keyframes ep-card-in {
  from { opacity: 0; transform: translateY(13px); }
  to   { opacity: 1; transform: none; }
}
@media (prefers-reduced-motion: reduce) { .config-card { animation: none; } }

.card-head {
  display: flex;
  align-items: center;
  gap: 9px;
  padding: 14px 18px;
  border-bottom: 1px solid var(--color-border);
}
.card-icon {
  width: 22px;
  height: 22px;
  border-radius: 6px;
  background: var(--color-primary-soft);
  color: var(--color-primary);
  display: grid;
  place-items: center;
  flex-shrink: 0;
}
.card-title {
  font-size: 0.88rem;
  font-weight: 600;
  color: var(--color-text);
}
.card-sub {
  margin-left: 6px;
  font-size: 0.75rem;
  color: var(--color-faint);
}

/* ─── Chain flow visual ───────────────────────────── */
.chain-flow {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 14px 18px;
  border-bottom: 1px solid var(--color-border);
  background: var(--color-inset);
  flex-wrap: wrap;
  min-height: 56px;
}
.chain-flow--empty {
  justify-content: center;
}
.chain-empty-text {
  font-size: 0.78rem;
  color: var(--color-faint);
  font-style: italic;
}
.chain-node {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 4px 10px;
  border-radius: var(--rounded-md);
  background: var(--color-card-2);
  border: 1px solid var(--color-border-strong);
  font-size: 0.78rem;
  font-weight: 500;
  color: var(--color-text);
  white-space: nowrap;
}
.chain-node--gated {
  border-color: var(--color-amber-line);
  background: var(--color-amber-soft);
  color: var(--color-amber);
}
.chain-node-name { }
.chain-node-gate { color: inherit; display: flex; align-items: center; }
.chain-arrow {
  color: var(--color-faint);
  flex-shrink: 0;
  display: flex;
  align-items: center;
}

/* ─── Chain table ─────────────────────────────────── */
.chain-table-head {
  display: grid;
  grid-template-columns: 36px 1fr 110px 72px 60px 44px;
  gap: 10px;
  padding: 0 18px;
  height: 36px;
  align-items: center;
  background: var(--color-card-2);
  border-bottom: 1px solid var(--color-border);
  font-size: var(--text-caps);
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: var(--color-faint);
}
.chain-empty-rows {
  padding: 20px 18px;
  font-size: 0.82rem;
  color: var(--color-faint);
  font-style: italic;
  text-align: center;
  border-bottom: 1px solid var(--color-border);
}
.chain-row {
  border-bottom: 1px solid var(--color-border);
  transition: background-color var(--duration-fast);
}
.chain-row--expanded {
  background: var(--color-inset);
}
.chain-row-main {
  display: grid;
  grid-template-columns: 36px 1fr 110px 72px 60px 44px;
  gap: 10px;
  padding: 8px 18px;
  align-items: start;
}
.chain-row-main:hover {
  background: var(--color-inset);
}
.chain-row--expanded .chain-row-main {
  background: var(--color-inset);
}

/* Chain idx badge */
.chain-idx {
  display: flex;
  align-items: center;
  justify-content: center;
  padding-top: 6px;
}
.chain-idx-num {
  width: 20px;
  height: 20px;
  border-radius: var(--rounded-full);
  background: var(--color-primary-soft);
  color: var(--color-primary);
  font-size: 0.7rem;
  font-weight: 700;
  display: grid;
  place-items: center;
}

/* Chain cells */
.chain-cell {
  display: flex;
  flex-direction: column;
  gap: 3px;
}
.chain-cell--gated { flex-direction: row; align-items: center; gap: 8px; padding-top: 5px; }
.chain-cell--vars { align-items: flex-start; padding-top: 5px; }
.chain-cell--order { flex-direction: row; align-items: center; gap: 3px; padding-top: 4px; }
.chain-cell--del { align-items: center; padding-top: 4px; }

/* Chain name input */
.chain-input {
  width: 100%;
  height: 34px;
  background: var(--color-inset);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded);
  padding: 0 10px;
  color: var(--color-text);
  font-family: var(--font-sans);
  font-size: 0.84rem;
  transition: border-color var(--duration-fast), box-shadow var(--duration-fast);
}
.chain-input::placeholder { color: var(--color-faint); }
.chain-input:focus { outline: none; border-color: var(--color-primary); box-shadow: 0 0 0 3px var(--color-primary-soft); }
.chain-input--error { border-color: var(--color-red); }
.chain-field-error { font-size: 0.72rem; color: var(--color-red); line-height: 1.4; }

/* Gated toggle */
.gate-toggle {
  position: relative;
  width: 34px;
  height: 18px;
  border-radius: var(--rounded-full);
  background: var(--color-border-strong);
  border: none;
  cursor: pointer;
  flex-shrink: 0;
  transition: background-color var(--duration-fast);
}
.gate-toggle--on { background: var(--color-amber); }
.gate-toggle-knob {
  position: absolute;
  top: 2px;
  left: 2px;
  width: 14px;
  height: 14px;
  border-radius: var(--rounded-full);
  background: #fff;
  transition: transform var(--duration-fast);
  pointer-events: none;
  box-shadow: 0 1px 3px oklch(0% 0 0 / 0.3);
}
.gate-toggle--on .gate-toggle-knob { transform: translateX(16px); }
@media (prefers-reduced-motion: reduce) { .gate-toggle, .gate-toggle-knob { transition: none; } }
.gate-label { font-size: 0.75rem; color: var(--color-dim); white-space: nowrap; }

/* Vars toggle button */
.vars-btn {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  height: 28px;
  padding: 0 10px;
  border: 1px solid var(--color-border);
  background: var(--color-card-2);
  color: var(--color-dim);
  font-family: var(--font-sans);
  font-size: 0.76rem;
  font-weight: 500;
  border-radius: var(--rounded-md);
  cursor: pointer;
  white-space: nowrap;
  transition: color var(--duration-fast), border-color var(--duration-fast), background-color var(--duration-fast);
}
.vars-btn:hover { color: var(--color-primary); border-color: var(--color-primary); }
.vars-btn--active {
  color: var(--color-primary);
  border-color: var(--color-primary);
  background: var(--color-primary-soft);
}
.vars-btn-count {
  display: inline-block;
  min-width: 16px;
  height: 16px;
  line-height: 16px;
  text-align: center;
  background: var(--color-border-strong);
  border-radius: var(--rounded-full);
  font-size: 0.66rem;
  font-weight: 700;
  padding: 0 3px;
}
.vars-btn--active .vars-btn-count { background: var(--color-primary); color: #fff; }

/* Order buttons */
.order-btn {
  width: 26px;
  height: 26px;
  border: 1px solid var(--color-border);
  background: transparent;
  color: var(--color-faint);
  border-radius: var(--rounded-md);
  cursor: pointer;
  display: grid;
  place-items: center;
  transition: color var(--duration-fast), border-color var(--duration-fast), background-color var(--duration-fast);
}
.order-btn:hover:not(:disabled) { color: var(--color-primary); border-color: var(--color-primary); background: var(--color-primary-soft); }
.order-btn:disabled { opacity: 0.25; cursor: not-allowed; }

/* Delete button */
.del-btn {
  width: 28px;
  height: 28px;
  border: 1px solid var(--color-border);
  background: transparent;
  color: var(--color-faint);
  border-radius: var(--rounded-md);
  cursor: pointer;
  display: grid;
  place-items: center;
  transition: color var(--duration-fast), border-color var(--duration-fast), background-color var(--duration-fast);
}
.del-btn:hover { color: var(--color-red); border-color: var(--color-red-line); background: var(--color-red-soft); }

/* ─── Variables panel ─────────────────────────────── */
.vars-panel {
  padding: 12px 18px 14px;
  border-top: 1px dashed var(--color-border-strong);
  display: flex;
  flex-direction: column;
  gap: 6px;
  background: var(--color-inset);
}

.var-row {
  display: flex;
  align-items: center;
  gap: 8px;
}

.var-row--secret {
  padding: 7px 10px;
  background: var(--color-amber-soft);
  border: 1px solid var(--color-amber-line);
  border-radius: var(--rounded-md);
  font-size: 0.79rem;
}
.var-key {
  font-family: var(--font-mono);
  font-size: 0.79rem;
  color: var(--color-text);
  min-width: 80px;
}
.var-val-placeholder {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: 0.75rem;
  color: var(--color-amber);
  flex: 1;
}
.var-secret-badge {
  font-size: 0.65rem;
  font-weight: 700;
  padding: 1px 6px;
  border-radius: var(--rounded-sm);
  background: var(--color-amber-soft);
  border: 1px solid var(--color-amber-line);
  color: var(--color-amber);
  white-space: nowrap;
  letter-spacing: 0.04em;
  text-transform: uppercase;
}

.var-row--edit { gap: 6px; }
.var-input {
  height: 32px;
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-md);
  padding: 0 8px;
  color: var(--color-text);
  font-family: var(--font-sans);
  font-size: 0.82rem;
  transition: border-color var(--duration-fast), box-shadow var(--duration-fast);
}
.var-input:focus { outline: none; border-color: var(--color-primary); box-shadow: 0 0 0 3px var(--color-primary-soft); }
.var-input--key { width: 140px; font-family: var(--font-mono); font-size: 0.79rem; }
.var-input--val { flex: 1; }
.var-eq { font-size: 0.82rem; color: var(--color-faint); flex-shrink: 0; }
.var-del-btn {
  width: 26px;
  height: 26px;
  border: 1px solid var(--color-border);
  background: transparent;
  color: var(--color-faint);
  border-radius: var(--rounded-md);
  cursor: pointer;
  display: grid;
  place-items: center;
  flex-shrink: 0;
  transition: color var(--duration-fast), border-color var(--duration-fast), background-color var(--duration-fast);
}
.var-del-btn:hover { color: var(--color-red); border-color: var(--color-red-line); background: var(--color-red-soft); }

.var-add-btn {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  align-self: flex-start;
  margin-top: 4px;
  padding: 5px 11px;
  border: 1px dashed var(--color-border-strong);
  background: transparent;
  color: var(--color-primary);
  font-family: var(--font-sans);
  font-size: 0.78rem;
  font-weight: 500;
  border-radius: var(--rounded-md);
  cursor: pointer;
  transition: border-color var(--duration-fast), background-color var(--duration-fast);
}
.var-add-btn:hover { border-color: var(--color-primary); background: var(--color-primary-soft); }

.vars-hint {
  font-size: 0.74rem;
  color: var(--color-faint);
  line-height: 1.5;
  margin-top: 4px;
}

/* ─── Add env button ──────────────────────────────── */
.add-env-btn {
  display: flex;
  align-items: center;
  gap: 7px;
  padding: 12px 18px;
  width: 100%;
  border: none;
  background: transparent;
  color: var(--color-primary);
  font-family: var(--font-sans);
  font-size: 0.8rem;
  font-weight: 500;
  cursor: pointer;
  text-align: left;
  transition: color var(--duration-fast), background-color var(--duration-fast);
  border-top: 1px solid var(--color-border);
}
.add-env-btn:hover { background: var(--color-inset); }

.chain-note {
  padding: 11px 18px 14px;
  font-size: 0.75rem;
  color: var(--color-faint);
  line-height: 1.6;
  border-top: 1px solid var(--color-border);
}

/* ─── Save bar ────────────────────────────────────── */
.ep-save-bar {
  display: flex;
  justify-content: flex-start;
  padding-bottom: 8px;
}
.ep-btn-primary {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  height: 34px;
  padding: 0 16px;
  border: none;
  background: var(--color-primary);
  color: #fff;
  font-family: var(--font-sans);
  font-size: 0.83rem;
  font-weight: 600;
  border-radius: var(--rounded);
  cursor: pointer;
  box-shadow: 0 5px 16px var(--color-primary-soft);
  transition: background-color var(--duration-fast), transform var(--duration-fast);
  white-space: nowrap;
}
.ep-btn-primary:hover:not(:disabled) { background: var(--color-primary-press); transform: translateY(-1px); }
.ep-btn-primary:disabled { opacity: 0.45; cursor: not-allowed; transform: none; box-shadow: none; }

.ep-spinner {
  display: inline-block;
  width: 13px;
  height: 13px;
  border: 2px solid rgba(255, 255, 255, 0.35);
  border-top-color: #fff;
  border-radius: var(--rounded-full);
  animation: ep-spin 0.7s linear infinite;
  flex-shrink: 0;
}
@keyframes ep-spin { to { transform: rotate(360deg); } }
@media (prefers-reduced-motion: reduce) { .ep-spinner { animation: none; border-top-color: currentColor; } }

.mono { font-family: var(--font-mono); }
</style>
