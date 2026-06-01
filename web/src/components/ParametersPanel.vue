<script setup lang="ts">
/**
 * ParametersPanel — 项目级「类型化运行参数定义」编辑器(P0 typed params)。
 *
 * 自包含卡片,自管载入/保存 /api/projects/{id}/parameters。定义一组参数
 * (key/label/type/default/options/required);手动触发弹窗据此渲染类型化控件并校验。
 * 无定义 = 触发回退自由 KV(向后兼容)。嵌于 TriggersPanel(ConcurrencyPanel 之后)。
 */
import { ref, onMounted, watch } from 'vue'
import {
  getParameters,
  saveParameters,
  PARAM_TYPE_OPTIONS,
  type ParamDef,
  type ParamType,
} from '../api/parameters'
import { HttpError } from '../api/http'

const props = defineProps<{
  projectId: string
}>()

interface Row extends ParamDef {
  rid: number
  optionsText: string
}

type LoadState = 'idle' | 'loading' | 'error'
const loadState = ref<LoadState>('idle')
const loadError = ref('')
const rows = ref<Row[]>([])
const saveSubmitting = ref(false)
const saveBanner = ref('')
const saveSuccess = ref(false)

let ridSeq = 0
function toRow(d: ParamDef): Row {
  return {
    rid: ++ridSeq,
    key: d.key,
    label: d.label,
    type: d.type,
    default: d.default,
    options: d.options ?? [],
    optionsText: (d.options ?? []).join(', '),
    required: d.required,
  }
}

async function load(): Promise<void> {
  loadState.value = 'loading'
  loadError.value = ''
  try {
    rows.value = (await getParameters(props.projectId)).map(toRow)
    loadState.value = 'idle'
  } catch (err) {
    loadState.value = 'error'
    loadError.value = err instanceof HttpError ? err.message : '加载参数定义失败'
  }
}

function addRow(): void {
  rows.value.push(toRow({ key: '', label: '', type: 'string', default: '', options: [], required: false }))
  clearBanner()
}
function removeRow(rid: number): void {
  rows.value = rows.value.filter((r) => r.rid !== rid)
  clearBanner()
}
function clearBanner(): void {
  saveBanner.value = ''
  saveSuccess.value = false
}
function onTypeChange(row: Row): void {
  if (row.type !== 'choice') row.optionsText = ''
  clearBanner()
}

async function handleSave(): Promise<void> {
  clearBanner()
  const defs: ParamDef[] = rows.value.map((r) => ({
    key: r.key.trim(),
    label: r.label.trim(),
    type: r.type as ParamType,
    default: r.default,
    options:
      r.type === 'choice'
        ? r.optionsText.split(',').map((s) => s.trim()).filter((s) => s !== '')
        : undefined,
    required: r.required,
  }))
  saveSubmitting.value = true
  try {
    rows.value = (await saveParameters(props.projectId, defs)).map(toRow)
    saveSuccess.value = true
    saveBanner.value = '参数定义已保存'
  } catch (err) {
    saveSuccess.value = false
    saveBanner.value =
      err instanceof HttpError
        ? (err.apiError?.message ?? `保存失败(${err.status})`)
        : '保存失败,请重试'
  } finally {
    saveSubmitting.value = false
  }
}

onMounted(load)
watch(() => props.projectId, load)
</script>

<template>
  <section class="config-card" aria-labelledby="param-heading">
    <div class="card-head">
      <span class="card-icon" aria-hidden="true">
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9">
          <path d="M4 7h16M4 12h16M4 17h10" />
        </svg>
      </span>
      <h2 id="param-heading" class="card-title">运行参数</h2>
      <span class="card-sub">手动触发时按类型填写的参数;无定义则触发用自由键值对</span>
    </div>

    <div class="card-body card-body--pad">
      <p v-if="loadState === 'loading'" class="muted">加载中…</p>
      <p v-else-if="loadState === 'error'" class="err" role="alert">{{ loadError }}</p>

      <template v-else>
        <p v-if="rows.length === 0" class="muted empty">
          还没有定义参数。点下方「+ 添加参数」后,手动触发弹窗会渲染对应的类型化控件(枚举下拉 / 布尔开关 / 数字框)。
        </p>

        <div v-for="row in rows" :key="row.rid" class="param-def">
          <button class="def-del" aria-label="移除参数" @click="removeRow(row.rid)">✕</button>
          <div class="def-grid">
            <label class="def-fld">
              <span class="def-lbl">键(KEY)</span>
              <input v-model="row.key" class="def-input is-mono" placeholder="env" autocomplete="off" @input="clearBanner" />
            </label>
            <label class="def-fld">
              <span class="def-lbl">显示标签</span>
              <input v-model="row.label" class="def-input" placeholder="部署环境" autocomplete="off" @input="clearBanner" />
            </label>
            <label class="def-fld def-fld--type">
              <span class="def-lbl">类型</span>
              <select v-model="row.type" class="def-input" @change="onTypeChange(row)">
                <option v-for="opt in PARAM_TYPE_OPTIONS" :key="opt.value" :value="opt.value">{{ opt.label }}</option>
              </select>
            </label>
          </div>
          <div class="def-grid">
            <label v-if="row.type === 'boolean'" class="def-fld">
              <span class="def-lbl">默认值</span>
              <select v-model="row.default" class="def-input" @change="clearBanner">
                <option value="false">false</option>
                <option value="true">true</option>
              </select>
            </label>
            <label v-else class="def-fld">
              <span class="def-lbl">默认值</span>
              <input
                v-model="row.default"
                class="def-input"
                :type="row.type === 'number' ? 'number' : 'text'"
                placeholder="prod"
                autocomplete="off"
                @input="clearBanner"
              />
            </label>
            <label v-if="row.type === 'choice'" class="def-fld def-fld--grow">
              <span class="def-lbl">选项(逗号分隔)</span>
              <input v-model="row.optionsText" class="def-input is-mono" placeholder="prod, staging, dev" autocomplete="off" @input="clearBanner" />
            </label>
            <label class="def-req">
              <input v-model="row.required" type="checkbox" @change="clearBanner" />
              <span>必填</span>
            </label>
          </div>
        </div>

        <button class="link-add" @click="addRow">+ 添加参数</button>

        <p v-if="saveBanner" class="banner" :class="saveSuccess ? 'banner--ok' : 'banner--err'" role="status">{{ saveBanner }}</p>

        <div class="save-row">
          <button class="btn-primary" :disabled="saveSubmitting" :aria-busy="saveSubmitting" @click="handleSave">
            <span v-if="saveSubmitting" class="spinner" aria-hidden="true" />
            {{ saveSubmitting ? '保存中…' : '保存参数定义' }}
          </button>
        </div>
      </template>
    </div>
  </section>
</template>

<style scoped>
.config-card { border: 1px solid var(--color-border); border-radius: var(--rounded-lg, 12px); background: var(--color-card); overflow: hidden; }
.card-head { display: flex; align-items: center; gap: 9px; padding: 14px 16px; border-bottom: 1px solid var(--color-border); }
.card-icon { display: grid; place-items: center; width: 26px; height: 26px; border-radius: var(--rounded-md); background: var(--color-primary-soft); color: var(--color-primary); flex: none; }
.card-title { font-size: 0.92rem; font-weight: 650; color: var(--color-text); }
.card-sub { font-size: 0.76rem; color: var(--color-faint); flex: 1; min-width: 0; }
.card-body--pad { padding: 16px; display: flex; flex-direction: column; gap: 12px; }

.muted { margin: 0; font-size: 0.82rem; color: var(--color-faint); }
.muted.empty { line-height: 1.55; }
.err { margin: 0; font-size: 0.82rem; color: var(--color-danger, #dc2626); }

.param-def {
  position: relative;
  border: 1px solid var(--color-border);
  border-left: 3px solid var(--color-primary);
  border-radius: var(--rounded-md);
  background: var(--color-inset);
  padding: 11px 12px;
  display: flex;
  flex-direction: column;
  gap: 9px;
}
.def-del {
  position: absolute; top: 8px; right: 8px;
  width: 22px; height: 22px; border: none; background: none;
  color: var(--color-faint); cursor: pointer; border-radius: 5px;
}
.def-del:hover { color: var(--color-danger, #dc2626); background: var(--color-card); }
.def-grid { display: grid; grid-template-columns: 1fr 1fr auto; gap: 9px; align-items: end; }
.def-fld { display: flex; flex-direction: column; gap: 4px; min-width: 0; }
.def-fld--type { width: 96px; }
.def-fld--grow { grid-column: 2 / 4; }
.def-lbl { font-size: 0.7rem; font-weight: 600; color: var(--color-faint); }
.def-input {
  height: 32px; padding: 0 9px;
  border: 1px solid var(--color-border-strong); border-radius: var(--rounded-md);
  background: var(--color-card); color: var(--color-text);
  font: inherit; font-size: 0.82rem; outline: none; width: 100%;
}
.def-input:focus { border-color: var(--color-primary); box-shadow: 0 0 0 2px var(--color-primary-soft); }
.is-mono { font-family: var(--font-mono, ui-monospace, monospace); font-size: 0.78rem; }
.def-req { display: inline-flex; align-items: center; gap: 6px; font-size: 0.78rem; color: var(--color-dim); height: 32px; white-space: nowrap; }

.link-add {
  align-self: flex-start; background: none; border: none;
  color: var(--color-primary); font: inherit; font-size: 0.8rem; font-weight: 600; cursor: pointer; padding: 0;
}
.link-add:hover { text-decoration: underline; }

.banner { margin: 0; font-size: 0.8rem; font-weight: 500; }
.banner--ok { color: var(--color-success, #16a34a); }
.banner--err { color: var(--color-danger, #dc2626); }

.save-row { display: flex; justify-content: flex-end; }
.btn-primary {
  display: inline-flex; align-items: center; gap: 7px; height: 34px; padding: 0 16px;
  border: none; background: var(--color-primary); color: #fff;
  font-family: var(--font-sans); font-size: 0.83rem; font-weight: 600;
  border-radius: var(--rounded); cursor: pointer;
  box-shadow: 0 5px 16px var(--color-primary-soft);
  transition: background-color var(--duration-fast), transform var(--duration-fast); white-space: nowrap;
}
.btn-primary:hover:not(:disabled) { background: var(--color-primary-press); transform: translateY(-1px); }
.btn-primary:disabled { opacity: 0.45; cursor: not-allowed; transform: none; box-shadow: none; }
.spinner { display: inline-block; width: 13px; height: 13px; border: 2px solid rgba(255,255,255,0.35); border-top-color: #fff; border-radius: var(--rounded-full); animation: spin 0.7s linear infinite; flex-shrink: 0; }
@keyframes spin { to { transform: rotate(360deg); } }
@media (prefers-reduced-motion: reduce) { .spinner { animation: none; border-top-color: currentColor; } }
</style>
