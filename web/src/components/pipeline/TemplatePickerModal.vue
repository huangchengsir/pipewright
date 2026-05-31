<script setup lang="ts">
/**
 * TemplatePickerModal — apply a reusable pipeline template to this project, or save the
 * current pipeline as a new template (FR-8-13).
 *
 * Two modes (segmented):
 *   - 应用模板:pick a template → applyTemplate() copies its stages into this project's pipeline.
 *   - 另存为模板:name + describe → createTemplate() snapshots the current canvas stages.
 *
 * Templates carry no plaintext secret (credentials remain vault references in 2-4 settings).
 */
import { ref, onMounted } from 'vue'
import {
  listTemplates,
  applyTemplate,
  createTemplate,
  type TemplateSummary,
} from '../../api/templates'
import type { PipelineDTO, PipelineStage } from '../../api/pipeline'
import { HttpError } from '../../api/http'

const props = defineProps<{
  projectId: string
  /** Current canvas stages — snapshotted when saving as a template. */
  stages: PipelineStage[]
}>()

const emit = defineEmits<{
  (e: 'close'): void
  /** A successful apply: parent reloads pipeline + settings. */
  (e: 'applied', dto: PipelineDTO): void
}>()

type Mode = 'apply' | 'save'
const mode = ref<Mode>('apply')

// ─── apply ──────────────────────────────────────────────────────────────────
const templates = ref<TemplateSummary[]>([])
const loading = ref(false)
const selectedId = ref('')
const applying = ref(false)
const errorMsg = ref('')

async function loadTemplates(): Promise<void> {
  loading.value = true
  errorMsg.value = ''
  try {
    templates.value = await listTemplates()
    if (templates.value.length > 0) selectedId.value = templates.value[0].id
  } catch (err: unknown) {
    errorMsg.value = err instanceof HttpError ? err.apiError?.message ?? '加载模板失败' : '加载模板失败'
  } finally {
    loading.value = false
  }
}

async function doApply(): Promise<void> {
  if (!selectedId.value) {
    errorMsg.value = '请先选择一个模板'
    return
  }
  if (
    !window.confirm('应用模板会用模板的阶段覆盖当前项目流水线。确认继续?')
  ) {
    return
  }
  applying.value = true
  errorMsg.value = ''
  try {
    const dto = await applyTemplate(props.projectId, selectedId.value)
    emit('applied', dto)
    emit('close')
  } catch (err: unknown) {
    errorMsg.value = err instanceof HttpError ? err.apiError?.message ?? '应用模板失败' : '应用模板失败'
  } finally {
    applying.value = false
  }
}

// ─── save as template ─────────────────────────────────────────────────────────
const saveName = ref('')
const saveDesc = ref('')
const saving = ref(false)
const saveDone = ref(false)

async function doSave(): Promise<void> {
  const name = saveName.value.trim()
  if (!name) {
    errorMsg.value = '请填写模板名称'
    return
  }
  saving.value = true
  errorMsg.value = ''
  try {
    await createTemplate({
      name,
      description: saveDesc.value.trim(),
      stages: props.stages.map((s) => ({
        id: undefined,
        name: s.name,
        kind: s.kind,
        needs: s.needs,
        allowFailure: s.allowFailure,
        when: s.when,
        gate: s.gate,
        jobs: s.jobs.map((j) => ({
          id: undefined,
          name: j.name,
          type: j.type,
          summary: j.summary,
          config: j.config,
        })),
      })),
    })
    saveDone.value = true
    // Refresh the apply list so the new template is pickable immediately.
    await loadTemplates()
  } catch (err: unknown) {
    errorMsg.value = err instanceof HttpError ? err.apiError?.message ?? '保存模板失败' : '保存模板失败'
  } finally {
    saving.value = false
  }
}

function switchMode(m: Mode): void {
  mode.value = m
  errorMsg.value = ''
  saveDone.value = false
}

onMounted(loadTemplates)
</script>

<template>
  <div class="scrim" @click.self="emit('close')">
    <div class="modal" role="dialog" aria-modal="true" aria-label="流水线模板">
      <header class="head">
        <h2 class="title">流水线模板</h2>
        <button class="close" aria-label="关闭" @click="emit('close')">✕</button>
      </header>

      <div class="seg" role="tablist">
        <button
          class="seg-item"
          role="tab"
          :aria-selected="mode === 'apply'"
          :class="{ active: mode === 'apply' }"
          @click="switchMode('apply')"
        >
          应用模板
        </button>
        <button
          class="seg-item"
          role="tab"
          :aria-selected="mode === 'save'"
          :class="{ active: mode === 'save' }"
          @click="switchMode('save')"
        >
          另存为模板
        </button>
      </div>

      <p v-if="errorMsg" class="err">{{ errorMsg }}</p>

      <!-- Apply -->
      <div v-if="mode === 'apply'" class="body">
        <p v-if="loading" class="hint">加载模板中…</p>
        <p v-else-if="templates.length === 0" class="hint">
          还没有任何模板。切到「另存为模板」把当前流水线沉淀为可复用模板。
        </p>
        <ul v-else class="tpl-list" role="list">
          <li v-for="t in templates" :key="t.id">
            <label class="tpl-opt" :class="{ sel: selectedId === t.id }">
              <input
                type="radio"
                name="tpl"
                :value="t.id"
                v-model="selectedId"
                class="tpl-radio"
              />
              <span class="tpl-info">
                <span class="tpl-name">{{ t.name }}</span>
                <span class="tpl-meta">{{ t.stageCount }} 阶段 · {{ t.description || '无说明' }}</span>
              </span>
            </label>
          </li>
        </ul>
        <footer class="foot">
          <button class="btn-secondary" @click="emit('close')">取消</button>
          <button
            class="btn-primary"
            :disabled="applying || templates.length === 0"
            @click="doApply"
          >
            {{ applying ? '应用中…' : '应用到当前流水线' }}
          </button>
        </footer>
      </div>

      <!-- Save as template -->
      <div v-else class="body">
        <p v-if="saveDone" class="ok">已保存为模板,可在「复用库」或上方「应用模板」中复用。</p>
        <div class="field">
          <label class="label" for="tpl-name">模板名称</label>
          <input
            id="tpl-name"
            v-model="saveName"
            class="input"
            placeholder="如 go-service-standard"
            autocomplete="off"
          />
        </div>
        <div class="field">
          <label class="label" for="tpl-desc">说明(可选)</label>
          <input
            id="tpl-desc"
            v-model="saveDesc"
            class="input"
            placeholder="这个模板的用途"
            autocomplete="off"
          />
        </div>
        <p class="hint">
          将快照当前画布的 {{ props.stages.length }} 个阶段。模板不含明文 secret(凭据保持保险库引用)。
        </p>
        <footer class="foot">
          <button class="btn-secondary" @click="emit('close')">关闭</button>
          <button class="btn-primary" :disabled="saving" @click="doSave">
            {{ saving ? '保存中…' : '保存为模板' }}
          </button>
        </footer>
      </div>
    </div>
  </div>
</template>

<style scoped>
.scrim {
  position: fixed;
  inset: 0;
  background: oklch(0% 0 0 / 0.55);
  display: grid;
  place-items: center;
  padding: 1rem;
  z-index: 60;
}
.modal {
  width: min(560px, 100%);
  max-height: 86vh;
  overflow-y: auto;
  background: var(--color-card);
  border: 1px solid var(--color-border-strong);
  border-radius: 18px;
  box-shadow: var(--shadow-modal);
  padding: 1.4rem;
}
.head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 1rem;
}
.title { margin: 0; font-size: 1.15rem; font-weight: 640; color: var(--color-text); }
.close {
  border: none;
  background: none;
  color: var(--color-dim);
  font-size: 1rem;
  cursor: pointer;
  width: 2rem;
  height: 2rem;
  border-radius: 8px;
}
.close:hover { background: var(--color-inset); color: var(--color-text); }

.seg {
  display: inline-flex;
  gap: 0.25rem;
  padding: 0.25rem;
  background: var(--color-inset);
  border: 1px solid var(--color-border);
  border-radius: 10px;
  margin-bottom: 1rem;
}
.seg-item {
  padding: 0.45rem 1rem;
  border: none;
  background: transparent;
  color: var(--color-dim);
  font-size: 0.88rem;
  font-weight: 560;
  border-radius: 8px;
  cursor: pointer;
}
.seg-item.active {
  background: var(--color-card);
  color: var(--color-text);
  box-shadow: var(--shadow);
}

.err {
  margin: 0 0 0.9rem;
  padding: 0.6rem 0.85rem;
  border-radius: 9px;
  font-size: 0.85rem;
  color: var(--color-red);
  background: var(--color-red-soft);
  border: 1px solid var(--color-red-line);
}
.ok {
  margin: 0 0 0.9rem;
  padding: 0.6rem 0.85rem;
  border-radius: 9px;
  font-size: 0.85rem;
  color: var(--color-green);
  background: var(--color-green-soft);
  border: 1px solid var(--color-green-line);
}
.hint { margin: 0.6rem 0 0; color: var(--color-dim); font-size: 0.85rem; line-height: 1.5; }

.tpl-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 0.5rem; }
.tpl-opt {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  padding: 0.7rem 0.85rem;
  border: 1px solid var(--color-border);
  border-radius: 11px;
  cursor: pointer;
  transition: border-color var(--duration-fast, 150ms), background var(--duration-fast, 150ms);
}
.tpl-opt:hover { border-color: var(--color-border-strong); }
.tpl-opt.sel { border-color: var(--color-primary); background: var(--color-primary-soft); }
.tpl-radio { accent-color: var(--color-primary); }
.tpl-info { display: flex; flex-direction: column; gap: 0.15rem; min-width: 0; }
.tpl-name { font-weight: 600; color: var(--color-text); }
.tpl-meta {
  font-size: 0.8rem;
  color: var(--color-dim);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.field { margin-bottom: 0.9rem; }
.label { display: block; margin-bottom: 0.35rem; font-size: 0.83rem; font-weight: 560; color: var(--color-dim); }
.input {
  width: 100%;
  padding: 0.55rem 0.75rem;
  background: var(--color-inset);
  border: 1px solid var(--color-border);
  border-radius: 9px;
  color: var(--color-text);
  font-size: 0.9rem;
}
.input:focus { outline: none; border-color: var(--color-primary); }

.foot { display: flex; justify-content: flex-end; gap: 0.6rem; margin-top: 1.2rem; }
.btn-primary {
  padding: 0.55rem 1.1rem;
  background: var(--color-primary);
  color: oklch(100% 0 0);
  border: none;
  border-radius: 10px;
  font-weight: 600;
  font-size: 0.9rem;
  cursor: pointer;
}
.btn-primary:hover:not(:disabled) { background: var(--color-primary-press); }
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
}
.btn-secondary:hover { border-color: var(--color-primary); }
</style>
