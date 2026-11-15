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
import { ref, computed, onBeforeUnmount, watch } from 'vue'
import PermissionGate from '../PermissionGate.vue'
import {usePermissions} from '../../composables/usePermission'
import {useAuthorizedRequestScope} from '../../composables/useAuthorizedRequestScope'
import type {PermissionQuery} from '../../api/access'
import { useI18n } from 'vue-i18n'
import {
  listTemplates,
  applyTemplate,
  createTemplate,
  type TemplateSummary,
} from '../../api/templates'
import type { PipelineDTO, PipelineStage } from '../../api/pipeline'
import { HttpError } from '../../api/http'
import { useProjectConfigCommit } from '../../composables/useProjectConfigRevisions'

const props = defineProps<{
  projectId: string
  baseRevision?: string
  /** Current canvas stages — snapshotted when saving as a template. */
  stages: PipelineStage[]
  beforeReplace?: () => Promise<boolean>
}>()

const { t } = useI18n()
const initialRevision = props.baseRevision
const initialProjectId = props.projectId
const publishCommit = useProjectConfigCommit()
let generation = 0
let alive = true
const open = ref(true)
watch(() => props.projectId, () => { generation++ })
onBeforeUnmount(() => { alive = false; generation++ })
const current = (ticket: number, id: string) => open.value && scope.active() && alive && ticket === generation && id === props.projectId

const emit = defineEmits<{
  (e: 'close'): void
  /** A successful apply: parent reloads pipeline + settings. */
  (e: 'applied', dto: PipelineDTO): void
}>()

function close() {
  if (!open.value) return
  open.value = false
  generation++
  emit('close')
}

type Mode = 'apply' | 'save'
const mode = ref<Mode>('apply')

// ─── apply ──────────────────────────────────────────────────────────────────
const templates = ref<TemplateSummary[]>([])
const loading = ref(false)
const selectedId = ref('')
const applying = ref(false)
const errorMsg = ref('')

async function loadTemplates(): Promise<void> {
  if (!open.value || !scope.active()) return
  loading.value = true
  errorMsg.value = ''
  try {
    templates.value = await scope.run(()=>listTemplates(scope.requestOptions))
    if (templates.value.length > 0) selectedId.value = templates.value[0].id
  } catch (err: unknown) {
    if (!scope.active()) return
    errorMsg.value = err instanceof HttpError ? err.apiError?.message ?? t('pipelinePanels.tplLoadFailed') : t('pipelinePanels.tplLoadFailed')
  } finally {
    loading.value = false
  }
}

async function doApply(): Promise<void> {
  if (!open.value || !scope.active() || !applyPermission.allowed.value) return
  if (props.projectId !== initialProjectId) { errorMsg.value = t('mcpConfig.reloadRequired'); return }
  if (applying.value) return
  if (!selectedId.value) {
    errorMsg.value = t('pipelinePanels.tplSelectFirst')
    return
  }
  applying.value = true
  errorMsg.value = ''
  const templateId = selectedId.value
  const projectId = props.projectId
  const ticket = generation
  try {
    if (!(props.beforeReplace ? await props.beforeReplace() : window.confirm(t('pipelinePanels.tplConfirmApply')))) return
    if (!current(ticket, projectId) || !applyPermission.allowed.value) return
    const dto = await scope.run(()=>applyTemplate(projectId,templateId,initialRevision,scope.requestOptions))
    if (!current(ticket, projectId)) return
    publishCommit(projectId, initialRevision, dto)
    emit('applied', dto)
    close()
  } catch (err: unknown) {
    if (!scope.active()) return
    if (!current(ticket, projectId)) return
    errorMsg.value = err instanceof HttpError ? err.apiError?.message ?? t('pipelinePanels.tplApplyFailed') : t('pipelinePanels.tplApplyFailed')
  } finally {
    applying.value = false
  }
}

// ─── save as template ─────────────────────────────────────────────────────────
const saveName = ref('')
const saveDesc = ref('')
const saving = ref(false)
const saveDone = ref(false)

async function doSave(event: MouseEvent): Promise<void> {
  if ((event.currentTarget as HTMLButtonElement | null)?.dataset.saveGeneration !== String(saveGeneration.value)) return
  if (!open.value || mode.value !== 'save' || !scope.active() || !savePermission.allowed.value || saving.value) return
  const projectId = props.projectId
  if (projectId !== initialProjectId) { errorMsg.value = t('mcpConfig.reloadRequired'); return }
  const name = saveName.value.trim()
  if (!name) {
    errorMsg.value = t('pipelinePanels.tplNameRequired')
    return
  }
  const ticket = generation, permissionTicket = saveGeneration.value
  const currentSave = () => current(ticket, projectId) && permissionTicket === saveGeneration.value && savePermission.allowed.value
  saving.value = true
  errorMsg.value = ''
  try {
    await scope.run(()=>createTemplate({
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
    },scope.requestOptions))
    if (!currentSave()) return
    saveDone.value = true
    // Refresh the apply list so the new template is pickable immediately.
    await loadTemplates()
  } catch (err: unknown) {
    if (!currentSave()) return
    errorMsg.value = err instanceof HttpError ? err.apiError?.message ?? t('pipelinePanels.tplSaveFailed') : t('pipelinePanels.tplSaveFailed')
  } finally {
    saving.value = false
  }
}

function switchMode(m: Mode): void {
  if (applying.value || saving.value) return
  if (mode.value !== m) saveGeneration.value++
  mode.value = m
  errorMsg.value = ''
  saveDone.value = false
}

const applyQueries=computed<PermissionQuery[]>(()=>[{action:'pipeline.edit',resourceId:props.projectId},{action:'template.read',resourceId:selectedId.value}])
const saveQueries=computed<PermissionQuery[]>(()=>[{action:'pipeline.read',resourceId:props.projectId},{action:'template.create',resourceId:'',create:true}])
const applyPermission = usePermissions(applyQueries)
const savePermission = usePermissions(saveQueries)
const saveGeneration = ref(0)
watch(savePermission.allowed, allowed => {
  if (allowed) return
  saveGeneration.value++
  saveName.value = ''
  saveDesc.value = ''
  saveDone.value = false
  if (mode.value === 'save') errorMsg.value = ''
}, { flush: 'sync' })
const scope=useAuthorizedRequestScope({action:'pipeline.read',resourceId:props.projectId},()=>{
  templates.value = []
  selectedId.value = ''
  saveName.value = ''
  saveDesc.value = ''
  errorMsg.value = ''
  saveDone.value = false
  if (alive) close()
})
watch(scope.active, active => { if (active) void loadTemplates() }, {immediate:true})
</script>

<template>
  <div v-if="open && scope.active()" class="scrim" @click.self="!applying && !saving && close()">
    <div class="modal" role="dialog" aria-modal="true" :aria-label="t('pipelinePanels.tplAria')">
      <header class="head">
        <h2 class="title">{{ t('pipelinePanels.tplTitle') }}</h2>
        <button class="close" :disabled="applying || saving" :aria-label="t('pipelinePanels.tplCloseAria')" @click="close">✕</button>
      </header>

      <div class="seg" role="tablist">
        <button
          class="seg-item"
          role="tab"
          :aria-selected="mode === 'apply'"
          :class="{ active: mode === 'apply' }"
          :disabled="applying || saving"
          @click="switchMode('apply')"
        >
          {{ t('pipelinePanels.tplApplyTab') }}
        </button>
        <button
          class="seg-item"
          role="tab"
          :aria-selected="mode === 'save'"
          :class="{ active: mode === 'save' }"
          :disabled="applying || saving"
          @click="switchMode('save')"
        >
          {{ t('pipelinePanels.tplSaveTab') }}
        </button>
      </div>

      <p v-if="errorMsg" class="err">{{ errorMsg }}</p>

      <!-- Apply -->
      <div v-if="mode === 'apply'" class="body">
        <p v-if="loading" class="hint">{{ t('pipelinePanels.tplLoading') }}</p>
        <p v-else-if="templates.length === 0" class="hint">
          {{ t('pipelinePanels.tplNoTemplates') }}
        </p>
        <ul v-else class="tpl-list" role="list">
          <PermissionGate v-for="tpl in templates" :key="tpl.id" :query="{action:'template.read',resourceId:tpl.id}"><li>
            <label class="tpl-opt" :class="{ sel: selectedId === tpl.id }">
              <input
                type="radio"
                name="tpl"
                :value="tpl.id"
                v-model="selectedId"
                :disabled="applying"
                class="tpl-radio"
              />
              <span class="tpl-info">
                <span class="tpl-name">{{ tpl.name }}</span>
                <span class="tpl-meta">{{ t('pipelinePanels.tplStageCount', { count: tpl.stageCount, desc: tpl.description || t('pipelinePanels.tplNoDesc') }) }}</span>
              </span>
            </label>
          </li></PermissionGate>
        </ul>
        <footer class="foot">
          <button class="btn-secondary" :disabled="applying" @click="close">{{ t('pipelinePanels.tplCancel') }}</button>
          <PermissionGate :query="applyQueries"><button
            class="btn-primary"
            :disabled="applying || templates.length === 0"
            @click="doApply"
          >
            {{ applying ? t('pipelinePanels.tplApplying') : t('pipelinePanels.tplApplyToPipeline') }}
          </button></PermissionGate>
        </footer>
      </div>

      <!-- Save as template -->
      <div v-else class="body">
        <p v-if="saveDone" class="ok">{{ t('pipelinePanels.tplSavedOk') }}</p>
        <div class="field">
          <label class="label" for="tpl-name">{{ t('pipelinePanels.tplName') }}</label>
          <input
            id="tpl-name"
            v-model="saveName"
            class="input"
            :placeholder="t('pipelinePanels.tplNamePlaceholder')"
            autocomplete="off"
          />
        </div>
        <div class="field">
          <label class="label" for="tpl-desc">{{ t('pipelinePanels.tplDescLabel') }}</label>
          <input
            id="tpl-desc"
            v-model="saveDesc"
            class="input"
            :placeholder="t('pipelinePanels.tplDescPlaceholder')"
            autocomplete="off"
          />
        </div>
        <p class="hint">
          {{ t('pipelinePanels.tplSnapshotHint', { count: props.stages.length }) }}
        </p>
        <footer class="foot">
          <button class="btn-secondary" @click="close">{{ t('pipelinePanels.tplClose') }}</button>
          <PermissionGate :query="saveQueries"><button class="btn-primary" :disabled="saving" :data-save-generation="saveGeneration" @click="doSave">
            {{ saving ? t('pipelinePanels.tplSaving') : t('pipelinePanels.tplSaveAsTemplate') }}
          </button></PermissionGate>
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
