<script setup lang="ts">
/**
 * YamlImportModal — import a `.pipewright.yml` document into the pipeline editor (FR-8-12).
 *
 * Flow:
 *   1. Paste/edit YAML → 「预览」 calls importPipeline(save=false): server parses + validates,
 *      returns the parsed PipelineDTO. We emit `preview` so the canvas reflects it without saving.
 *   2. 「导入到画布」 commits the previewed stages into the editor's local state (still unsaved —
 *      user reviews on the canvas, then clicks 保存草稿 as usual).
 *   3. 「导入并保存」 calls importPipeline(save=true): parses, persists, reloads.
 *
 * Validation errors (422) are shown inline with the server's human-readable, secret-free message.
 */
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { importPipeline, type PipelineDTO } from '../../api/pipeline'
import { HttpError } from '../../api/http'

const props = defineProps<{
  projectId: string
}>()

const { t } = useI18n()

const emit = defineEmits<{
  (e: 'close'): void
  /** A successful preview (save=false): parent applies stages to the canvas without saving. */
  (e: 'preview', dto: PipelineDTO): void
  /** A successful save (save=true): parent reloads pipeline + settings. */
  (e: 'saved', dto: PipelineDTO): void
}>()

const STARTER = `version: 1
stages:
  - name: 流水线源
    kind: source
    jobs:
      - name: 源
        type: git_source
  - name: 构建
    kind: build
    jobs:
      - name: 运行测试
        type: script
        script:
          image: golang:1.23
          commands:
            - go test ./...
`

const yamlText = ref('')
const busy = ref<'preview' | 'save' | null>(null)
const errorMsg = ref('')
const previewedStageCount = ref<number | null>(null)

/** Maps 422 codes to user-visible hints; falls back to the server's (sanitized) message. */
function mapError(err: HttpError): string {
  const code = err.apiError?.code
  const msg = err.apiError?.message
  switch (code) {
    case 'invalid_yaml':  return msg ?? t('pipelinePanels.yiInvalidYaml')
    case 'invalid_stage': return msg ?? t('pipelinePanels.yiInvalidStage')
    case 'invalid_job':   return msg ?? t('pipelinePanels.yiInvalidJob')
    case 'duplicate_id':  return msg ?? t('pipelinePanels.yiDuplicateId')
    case 'project_not_found': return t('pipelinePanels.yiProjectNotFound')
    default:              return msg ?? t('pipelinePanels.yiImportFailed', { status: err.status })
  }
}

function handleError(err: unknown): void {
  if (err instanceof HttpError) {
    errorMsg.value = err.status === 0
      ? t('pipelinePanels.yiConnFailed')
      : mapError(err)
  } else {
    errorMsg.value = t('pipelinePanels.yiImportFailedRetry')
  }
}

async function runPreview(): Promise<PipelineDTO | null> {
  errorMsg.value = ''
  busy.value = 'preview'
  try {
    const dto = await importPipeline(props.projectId, yamlText.value, false)
    previewedStageCount.value = dto.stages.length
    return dto
  } catch (err) {
    handleError(err)
    previewedStageCount.value = null
    return null
  } finally {
    busy.value = null
  }
}

/** 预览:解析校验,把结果回灌画布(不落库)。 */
async function onPreview(): Promise<void> {
  const dto = await runPreview()
  if (dto) emit('preview', dto)
}

/** 导入到画布:与预览同一动作语义,关闭弹窗,让用户在画布上确认后再保存草稿。 */
async function onApplyToCanvas(): Promise<void> {
  const dto = await runPreview()
  if (dto) {
    emit('preview', dto)
    emit('close')
  }
}

/** 导入并保存:解析 + 持久化 + 重载。 */
async function onImportAndSave(): Promise<void> {
  errorMsg.value = ''
  busy.value = 'save'
  try {
    const dto = await importPipeline(props.projectId, yamlText.value, true)
    emit('saved', dto)
    emit('close')
  } catch (err) {
    handleError(err)
  } finally {
    busy.value = null
  }
}

function loadStarter(): void {
  yamlText.value = STARTER
}

function onBackdrop(e: MouseEvent): void {
  if (e.target === e.currentTarget) emit('close')
}
</script>

<template>
  <div class="yi-backdrop" role="dialog" aria-modal="true" aria-labelledby="yi-title" @mousedown="onBackdrop">
    <div class="yi-modal">
      <header class="yi-head">
        <div>
          <h2 id="yi-title" class="yi-title">{{ t('pipelinePanels.yiTitle') }}</h2>
          <p class="yi-sub">{{ t('pipelinePanels.yiSub') }}</p>
        </div>
        <button class="yi-close" :aria-label="t('pipelinePanels.yiCloseAria')" @click="emit('close')">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="M18 6 6 18M6 6l12 12"/></svg>
        </button>
      </header>

      <div class="yi-body">
        <div class="yi-editor-head">
          <label for="yi-yaml" class="yi-label">{{ t('pipelinePanels.yiYamlLabel') }}</label>
          <button class="yi-starter" type="button" @click="loadStarter">{{ t('pipelinePanels.yiLoadStarter') }}</button>
        </div>
        <textarea
          id="yi-yaml"
          v-model="yamlText"
          class="yi-yaml"
          spellcheck="false"
          autocomplete="off"
          placeholder="version: 1&#10;stages:&#10;  - name: 流水线源&#10;    kind: source&#10;    jobs: [...]"
          :disabled="busy !== null"
        />

        <div v-if="errorMsg" class="yi-banner yi-banner--error" role="alert">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><circle cx="12" cy="12" r="9"/><path d="M12 8v4M12 16h.01"/></svg>
          <span>{{ errorMsg }}</span>
        </div>
        <div v-else-if="previewedStageCount !== null" class="yi-banner yi-banner--ok" role="status">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true"><path d="M20 6 9 17l-5-5"/></svg>
          <span>{{ t('pipelinePanels.yiOkBanner', { count: previewedStageCount }) }}</span>
        </div>
      </div>

      <footer class="yi-foot">
        <button class="yi-btn" :disabled="busy !== null || !yamlText.trim()" @click="onPreview">
          <span v-if="busy === 'preview'" class="yi-spin" aria-hidden="true"/>
          {{ t('pipelinePanels.yiPreview') }}
        </button>
        <div class="yi-foot-right">
          <button class="yi-btn" :disabled="busy !== null || !yamlText.trim()" @click="onApplyToCanvas">{{ t('pipelinePanels.yiImportToCanvas') }}</button>
          <button class="yi-btn yi-btn--primary" :disabled="busy !== null || !yamlText.trim()" @click="onImportAndSave">
            <span v-if="busy === 'save'" class="yi-spin" aria-hidden="true"/>
            {{ t('pipelinePanels.yiImportAndSave') }}
          </button>
        </div>
      </footer>
    </div>
  </div>
</template>

<style scoped>
.yi-backdrop {
  position: fixed;
  inset: 0;
  z-index: 60;
  background: oklch(0% 0 0 / 0.42);
  backdrop-filter: blur(2px);
  display: grid;
  place-items: center;
  padding: 24px;
}

.yi-modal {
  width: min(720px, 100%);
  max-height: min(86vh, 760px);
  display: flex;
  flex-direction: column;
  background: var(--color-card);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded-card);
  box-shadow: 0 24px 64px oklch(0% 0 0 / 0.36);
  overflow: hidden;
}

.yi-head {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 18px 20px 14px;
  border-bottom: 1px solid var(--color-border);
}

.yi-title {
  font-size: 1.02rem;
  font-weight: 700;
  color: var(--color-text);
  letter-spacing: -0.01em;
}

.yi-sub {
  margin-top: 4px;
  font-size: 0.8rem;
  color: var(--color-faint);
  line-height: 1.5;
}

.yi-sub code {
  font-family: var(--font-mono, monospace);
  background: var(--color-inset);
  padding: 1px 5px;
  border-radius: 4px;
  font-size: 0.92em;
}

.yi-close {
  margin-left: auto;
  flex: none;
  width: 28px;
  height: 28px;
  display: grid;
  place-items: center;
  border: none;
  background: none;
  color: var(--color-faint);
  border-radius: 6px;
  cursor: pointer;
  transition: color var(--duration-fast), background-color var(--duration-fast);
}

.yi-close:hover { color: var(--color-text); background: var(--color-inset); }
.yi-close:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 2px; }

.yi-body {
  padding: 16px 20px;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.yi-editor-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.yi-label {
  font-size: 0.8rem;
  font-weight: 600;
  color: var(--color-dim);
}

.yi-starter {
  font-size: 0.76rem;
  color: var(--color-cyan);
  background: none;
  border: none;
  cursor: pointer;
  padding: 2px 4px;
  border-radius: 4px;
}

.yi-starter:hover { text-decoration: underline; }
.yi-starter:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 2px; }

.yi-yaml {
  width: 100%;
  min-height: 280px;
  resize: vertical;
  font-family: var(--font-mono, ui-monospace, monospace);
  font-size: 0.8rem;
  line-height: 1.6;
  color: var(--color-text);
  background: var(--color-canvas, var(--color-inset));
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded-md);
  padding: 12px 14px;
  tab-size: 2;
}

.yi-yaml:focus { outline: none; border-color: var(--color-primary); box-shadow: 0 0 0 3px var(--color-primary-soft); }
.yi-yaml:disabled { opacity: 0.6; }

.yi-banner {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  font-size: 0.8rem;
  line-height: 1.5;
  padding: 9px 12px;
  border-radius: var(--rounded-md);
  border: 1px solid transparent;
}

.yi-banner--error {
  background: var(--color-red-soft);
  border-color: var(--color-red-line);
  color: var(--color-red);
}

.yi-banner--ok {
  background: var(--color-green-soft);
  border-color: var(--color-green-line);
  color: var(--color-green);
}

.yi-banner svg { flex: none; margin-top: 2px; }

.yi-foot {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 14px 20px;
  border-top: 1px solid var(--color-border);
}

.yi-foot-right {
  margin-left: auto;
  display: flex;
  gap: 8px;
}

.yi-btn {
  height: 34px;
  font-size: 0.83rem;
  font-weight: 500;
  font-family: var(--font-sans);
  border: 1px solid var(--color-border-strong);
  background: var(--color-card-2);
  color: var(--color-text);
  border-radius: var(--rounded);
  padding: 0 14px;
  cursor: pointer;
  display: inline-flex;
  align-items: center;
  gap: 7px;
  transition: border-color var(--duration-fast), background-color var(--duration-fast);
}

.yi-btn:hover:not(:disabled) { border-color: var(--color-faint); }
.yi-btn:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 2px; }
.yi-btn:disabled { opacity: 0.45; cursor: not-allowed; }

.yi-btn--primary {
  background: var(--color-primary);
  color: #fff;
  border-color: transparent;
  font-weight: 600;
}

.yi-btn--primary:hover:not(:disabled) { background: var(--color-primary-press); }

.yi-spin {
  display: inline-block;
  width: 12px;
  height: 12px;
  border: 2px solid rgba(255, 255, 255, 0.35);
  border-top-color: currentColor;
  border-radius: var(--rounded-full);
  animation: yi-spin 0.7s linear infinite;
}

@keyframes yi-spin { to { transform: rotate(360deg); } }

@media (prefers-reduced-motion: reduce) {
  .yi-spin { animation: none; }
}
</style>
