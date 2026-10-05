<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, shallowRef, toRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { ArrowRight, Check, Focus2, Refresh, X } from '@vicons/tabler'
import AppButton from '../ui/AppButton.vue'
import type { PipelineStage } from '../../api/pipeline'
import { ISSUE_KEYS } from '../../composables/onboardingState'
import type { Snapshot } from '../../api/onboarding'
import { guideJobs, guideIssueTarget, type GuideContext, type GuidePhase } from '../../composables/pipelineGuideState'
import { usePipelineGuide } from '../../composables/usePipelineGuide'

const props = defineProps<{ projectId: string; stages: PipelineStage[]; dirty: boolean; context: GuideContext; activeTab: string }>()
const emit = defineEmits<{ locate: [phase: GuidePhase | 'conditions']; close: [] }>()
const { t } = useI18n()
const router = useRouter()
const { snapshot, phase, refresh, review, configure } = usePipelineGuide({ projectId: toRef(props, 'projectId'),
  stages: toRef(props, 'stages'), dirty: toRef(props, 'dirty'), context: toRef(props, 'context') })
const root = shallowRef<HTMLElement | null>(null)
let highlighted: HTMLElement | null = null
let highlightSequence = 0
let pendingFocus = false
const locatable = computed(() => ['stage', 'task', 'configure', 'save', 'repository'].includes(phase.value))
const issues = computed(() => snapshot.value?.pipeline.issues.filter(i => i.code !== 'runtime_stub') ?? [])
const step = computed(() => ['stage', 'task', 'choose'].includes(phase.value) ? 1 : ['configure', 'save'].includes(phase.value) ? 2 : 3)
const selectedTask = computed(() => guideJobs(props.stages).some(j => j.id === props.context.selectedJobId))

function target(): HTMLElement | null {
  const scope = root.value?.closest('.pipeline-root')
  if (!scope) return null
  if (phase.value !== 'save' && props.activeTab !== 'canvas') return null
  const selectors: Partial<Record<GuidePhase, string>> = {
    stage: '[data-onboarding-target="add-stage"]', task: '[data-stage-kind]:not([data-stage-kind="source"]) [data-onboarding-target="add-task"]',
    configure: '.job-drawer [data-config-key]:not([style*="display: none"])',
    save: '[data-onboarding-target="save"]', repository: '[data-onboarding-target="repository"]',
  }
  const selector = selectors[phase.value]
  return selector ? scope.querySelector<HTMLElement>(selector) : null
}
function clearHighlight(): void { highlighted?.classList.remove('onboarding-target'); highlighted = null }
async function mark(focus = false): Promise<void> {
  const seq = ++highlightSequence
  clearHighlight()
  await nextTick()
  if (seq !== highlightSequence) return
  highlighted = target()
  if (!highlighted) return
  highlighted.classList.add('onboarding-target')
  if (focus) {
    pendingFocus = false
    highlighted.scrollIntoView({ block: 'center', inline: 'nearest' })
    const control = highlighted.matches('button, input, textarea') ? highlighted
      : highlighted.querySelector<HTMLElement>('input, textarea, button')
    control?.focus({ preventScroll: true })
  }
}
async function locate(): Promise<void> { pendingFocus = true; emit('locate', phase.value); await nextTick(); await mark(true) }
function locateIssue(event: MouseEvent, issue: Snapshot['pipeline']['issues'][number]): void {
  if (issue.scope !== 'canvas' || ['project_credential_missing', 'vault_unconfigured', 'credential_missing', 'server_missing'].includes(issue.code)) return
  event.preventDefault()
  if (issue.code === 'no_tasks') emit('locate', 'conditions')
  else { configure(); pendingFocus = true; emit('locate', 'configure') }
}
watch([phase, () => props.activeTab, () => props.context.selectedJobId], () => { void mark(pendingFocus) }, { flush: 'post', immediate: true })
onBeforeUnmount(() => { highlightSequence++; clearHighlight() })
defineExpose({ refresh })
</script>

<template>
  <section ref="root" class="pipeline-guide" data-testid="pipeline-guide" :data-phase="phase" :aria-label="t('onboardingGuide.title')">
    <div class="guide-progress" aria-hidden="true"><Check v-if="phase === 'ready'" /><span v-else>{{ step }} / 3</span></div>
    <div class="guide-copy" aria-live="polite">
      <h2 class="guide-title">{{ t(`onboardingGuide.phases.${phase}.title`) }}</h2>
      <p class="guide-description">{{ t(`onboardingGuide.phases.${phase}.description`) }}</p>
      <ul v-if="phase === 'issues'" class="guide-issues">
        <li v-for="issue in issues" :key="`${issue.code}-${issue.scope}`">
          <router-link :to="guideIssueTarget(issue.code, issue.scope, projectId)" @click="locateIssue($event, issue)">{{ t(`onboardingFlow.issues.${ISSUE_KEYS[issue.code] ?? 'pipeline'}`) }} <ArrowRight aria-hidden="true" /></router-link>
        </li>
      </ul>
      <p v-if="phase === 'runtime'" class="guide-description">{{ t('onboardingFlow.runtime.instructions') }}</p>
      <p v-if="dirty" class="guide-unsaved">{{ t('onboardingGuide.unsaved') }}</p>
    </div>
    <div class="guide-actions">
      <AppButton v-if="locatable" data-testid="guide-locate" @click="locate"><Focus2 aria-hidden="true" />{{ t('onboardingGuide.locate') }}</AppButton>
      <AppButton v-if="phase === 'configure' && selectedTask && activeTab === 'canvas'" data-testid="guide-review" variant="primary" @click="review"><ArrowRight aria-hidden="true" />{{ t('onboardingGuide.review') }}</AppButton>
      <AppButton v-if="['unknown', 'issues', 'runtime', 'repository'].includes(phase)" data-testid="guide-refresh" @click="refresh"><Refresh aria-hidden="true" />{{ t('onboardingGuide.refresh') }}</AppButton>
      <AppButton v-if="phase === 'ready'" variant="primary" data-testid="guide-run" @click="router.push(`/projects?onboardingRun=${encodeURIComponent(projectId)}`)"><ArrowRight aria-hidden="true" />{{ t('onboardingFlow.actions.goRun') }}</AppButton>
      <button class="guide-close" type="button" :aria-label="t('onboardingGuide.close')" :title="t('onboardingGuide.close')" @click="emit('close')"><X aria-hidden="true" /></button>
    </div>
  </section>
</template>

<style scoped>
.pipeline-guide { display: flex; align-items: flex-start; gap: 12px; padding: 14px 20px; border-bottom: 1px solid var(--color-border); border-left: 3px solid var(--color-primary); background: var(--color-card); flex: none; max-height: 230px; overflow: auto; letter-spacing: 0; }
.guide-progress { display: grid; place-items: center; flex: none; width: 44px; height: 32px; background: var(--color-primary-soft); color: var(--color-primary); border-radius: 4px; font-size: 12px; font-weight: 600; }
.guide-progress svg { width: 18px; height: 18px; }
.guide-copy { flex: 1; min-width: 0; }
.guide-title { font-size: 14px; font-weight: 650; margin: 0 0 4px; }
.guide-description, .guide-unsaved { margin: 0; font-size: 13px; line-height: 1.6; color: var(--color-dim); overflow-wrap: anywhere; }
.guide-unsaved { color: var(--color-primary); margin-top: 4px; }
.guide-actions { display: flex; flex-wrap: wrap; align-items: center; justify-content: flex-end; gap: 8px; max-width: 420px; }
.guide-actions svg { width: 16px; height: 16px; flex: none; }
.guide-actions :deep(.app-btn) { white-space: normal; height: auto; min-height: 36px; padding: 6px 10px; font-size: 13px; }
.guide-close { display: grid; place-items: center; width: 36px; height: 36px; flex: none; border: 0; border-radius: 4px; background: transparent; color: var(--color-dim); cursor: pointer; }
.guide-close:hover { background: var(--color-card-2); }
.guide-close:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 2px; }
.guide-issues { margin: 8px 0 0; padding-left: 18px; font-size: 13px; line-height: 1.6; }
.guide-issues a { color: var(--color-primary); overflow-wrap: anywhere; }
.guide-issues svg { display: inline; width: 14px; height: 14px; vertical-align: middle; }
@media (max-width: 900px) { .pipeline-guide { flex-wrap: wrap; padding: 12px; gap: 8px; } .guide-actions { flex: 1 0 100%; max-width: none; justify-content: flex-start; padding-left: 52px; } }
@media (max-width: 480px) { .guide-actions { padding-left: 0; } .pipeline-guide { max-height: 250px; } }
</style>
