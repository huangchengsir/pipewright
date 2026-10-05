<script setup lang="ts">
import { computed, shallowRef } from 'vue'
import { useI18n } from 'vue-i18n'
import { Check, Circle, Help, ArrowRight, Refresh, AlertTriangle } from '@vicons/tabler'
import AppButton from '../ui/AppButton.vue'
import AppSelect from '../ui/AppSelect.vue'
import type { Snapshot } from '../../api/onboarding'
import { ISSUE_KEYS, type FlowAction, type FlowState } from '../../composables/onboardingState'

const props = defineProps<{ snapshot: Readonly<Snapshot> | null; flow: FlowState; loading: boolean; error: boolean }>()
const emit = defineEmits<{ select: [id: string]; retry: []; skip: []; navigate: [to: string] }>()
const { t } = useI18n()
const helpOpen = shallowRef(false)
const browsedStep = shallowRef<number | null>(null)
const completed = computed(() => props.flow.kind === 'success' || props.flow.kind === 'legacy')
const stepKeys = ['project', 'pipeline', 'run'] as const
const options = computed(() => props.snapshot?.projects.map(p => ({ value: p.id, label: p.name })) ?? [])
const issues = computed(() => [...new Set(props.snapshot?.pipeline.issues.map(i => ISSUE_KEYS[i.code] ?? 'pipeline') ?? [])])
const serverMissing = computed(() => props.snapshot?.pipeline.issues.some(i => i.code === 'server_missing') ?? false)
const selected = computed(() => props.snapshot?.selectedProject)
const projectName = computed(() => completed.value ? props.snapshot?.success?.projectName || t('onboardingFlow.projectUnavailable') : selected.value?.name)
const browseLinks = computed(() => {
  if (completed.value && props.snapshot?.success) {
    const success = props.snapshot.success
    return [success.projectExists ? '/projects' : null,
      success.projectExists ? `/projects/${encodeURIComponent(success.projectId)}/pipeline` : null,
      `/runs/${encodeURIComponent(success.id)}`]
  }
  if (props.flow.kind === 'unknown' || !selected.value) return ['/projects', null, null]
  const id = encodeURIComponent(selected.value.id)
  return ['/projects', `/projects/${id}/pipeline`, `/projects?onboardingRun=${id}`]
})
function action(a: FlowAction): void {
  if (a.kind === 'retry') emit('retry')
  else if (a.kind === 'help') helpOpen.value = true
  else if (a.to) emit('navigate', a.to)
}
</script>

<template>
  <section class="onboarding" data-testid="onboarding-flow" aria-labelledby="onboarding-title">
    <header class="flow-header">
      <h1 id="onboarding-title" class="flow-title">{{ t('onboardingFlow.title') }}</h1>
      <p class="flow-intro">{{ t('onboardingFlow.intro') }}</p>
    </header>
    <section v-if="options.length || completed" class="project-choice">
      <label v-if="!completed && options.length > 1" for="onboarding-project">{{ t('onboardingFlow.projectLabel') }}</label>
      <span v-else>{{ completed ? t('onboardingFlow.resultProjectLabel') : t('onboardingFlow.projectLabel') }}</span>
      <AppSelect v-if="!completed && options.length > 1" input-id="onboarding-project" :model-value="selected?.id ?? ''"
        :options="options" min-width="0" height="44px" :aria-label="t('onboardingFlow.projectLabel')" @update:model-value="emit('select', $event)" />
      <p class="project-name" :title="projectName ?? options[0]?.label">{{ projectName ?? options[0]?.label }}</p>
    </section>
    <ol class="flow-steps" :aria-label="t('onboardingFlow.stepsLabel')">
      <li v-for="(key, index) in stepKeys" :key="key" class="flow-step" :data-state="flow.steps[index]">
        <button class="step-button" type="button" :aria-expanded="browsedStep === index" :aria-controls="`onboarding-step-${index}`"
          :aria-current="['current', 'saved'].includes(flow.steps[index]!) ? 'step' : undefined" @click="browsedStep = browsedStep === index ? null : index">
          <component :is="['done', 'saved'].includes(flow.steps[index]!) ? Check : flow.steps[index] === 'unknown' ? Help : Circle" aria-hidden="true" class="step-icon" />
          <span class="step-copy"><b>{{ t(`onboardingFlow.steps.${key}`) }}</b><span>{{ t(`onboardingFlow.stepStates.${flow.steps[index]}`) }}</span></span>
          <span class="step-number" aria-hidden="true">{{ index + 1 }}</span>
        </button>
        <div v-if="browsedStep === index" :id="`onboarding-step-${index}`" class="step-detail">
          <p>{{ t(`onboardingFlow.stepDescriptions.${key}`) }}</p>
          <AppButton v-if="browseLinks[index]" variant="ghost" @click="emit('navigate', browseLinks[index]!)"><span>{{ t('onboardingFlow.actions.open') }}</span><ArrowRight aria-hidden="true" /></AppButton>
        </div>
      </li>
    </ol>
    <section class="flow-next" aria-live="polite" :aria-busy="loading" data-testid="onboarding-next">
      <div class="status-heading">
        <Check v-if="flow.kind === 'success' || flow.kind === 'legacy'" aria-hidden="true" class="success-icon" />
        <AlertTriangle v-else-if="error || flow.kind === 'unknown' || flow.kind === 'failed'" aria-hidden="true" />
        <h2 class="next-title">{{ t(`onboardingFlow.states.${flow.kind}`) }}</h2>
      </div>
      <p class="next-description">{{ t(`onboardingFlow.descriptions.${flow.kind}`) }}</p>
      <ul v-if="issues.length && !['success', 'legacy'].includes(flow.kind)" class="flow-issues">
        <li v-for="key in issues" :key="key">{{ t(`onboardingFlow.issues.${key}`) }}</li>
      </ul>
      <div class="flow-actions">
        <AppButton v-if="flow.primary" variant="primary" data-testid="onboarding-primary" @click="action(flow.primary)">
          <Refresh v-if="flow.primary.kind === 'retry'" aria-hidden="true" /><Help v-else-if="flow.primary.kind === 'help'" aria-hidden="true" /><ArrowRight v-else aria-hidden="true" />
          {{ t(`onboardingFlow.actions.${flow.primary.label}`) }}
        </AppButton>
        <AppButton v-for="secondary in flow.secondary" :key="secondary.label" variant="ghost" @click="action(secondary)">{{ t(`onboardingFlow.actions.${secondary.label}`) }}</AppButton>
      </div>
    </section>
    <details v-if="!completed" class="runtime-help" :open="helpOpen" @toggle="helpOpen = ($event.target as HTMLDetailsElement).open">
      <summary>{{ t('onboardingFlow.actions.runtimeHelp') }}</summary>
      <p>{{ t(`onboardingFlow.runtime.${snapshot?.runtime ?? 'unknown'}`) }}</p>
      <p>{{ t('onboardingFlow.runtime.instructions') }}</p>
      <router-link v-if="serverMissing" to="/settings/servers">{{ t('onboardingFlow.actions.servers') }}</router-link>
    </details>
    <footer class="flow-footer">
      <AppButton variant="ghost" data-testid="onboarding-skip" @click="emit('skip')">{{ t('onboarding.skip') }}</AppButton>
      <router-link to="/" class="dashboard-link">{{ t('onboarding.dashboard') }}</router-link>
    </footer>
  </section>
</template>

<style scoped>
.onboarding { width: 100%; max-width: 880px; margin: 0 auto; padding: 24px 0 48px; color: var(--color-text); font-size: 16px; letter-spacing: 0; overflow-wrap: anywhere; }
.flow-header { margin-bottom: 24px; }
.flow-title { font-size: 1.5rem; font-weight: 650; margin: 0 0 8px; }
.flow-intro, .next-description, .step-detail, .runtime-help { line-height: 1.6; color: var(--color-dim); }
.project-choice { display: grid; gap: 8px; margin-bottom: 24px; min-width: 0; }
.project-choice label { font-size: 0.875rem; color: var(--color-dim); }
.project-name { font-weight: 600; margin: 0; line-height: 1.6; display: -webkit-box; -webkit-box-orient: vertical; -webkit-line-clamp: 2; overflow: hidden; }
.project-choice :deep(.app-select__trigger) { min-height: 44px; }
.project-choice :deep(.app-select__option span) { white-space: normal; overflow-wrap: anywhere; }
.flow-steps { list-style: none; padding: 0; margin: 0; border-top: 1px solid var(--color-border); }
.flow-step { border-bottom: 1px solid var(--color-border); min-width: 0; }
.step-button { display: flex; gap: 16px; align-items: center; width: 100%; min-height: 76px; padding: 16px 4px; text-align: left; background: transparent; border: 0; font: inherit; color: inherit; cursor: pointer; }
.step-icon { width: 24px; height: 24px; flex: none; color: var(--color-faint); }
.flow-step[data-state="done"] .step-icon, .success-icon { color: var(--color-green); }
.flow-step[data-state="current"] .step-icon, .flow-step[data-state="saved"] .step-icon { color: var(--color-primary); }
.step-copy { display: grid; gap: 4px; flex: 1; min-width: 0; }
.step-copy span, .step-number { font-size: 0.875rem; color: var(--color-dim); }
.step-detail { padding: 0 4px 16px 44px; }
.step-detail p { margin: 0 0 12px; }
.step-detail svg { width: 18px; height: 18px; flex: none; }
.step-detail :deep(.app-btn) { width: fit-content; gap: 8px; }
.step-detail .app-btn span { min-width: 0; }
.flow-next { padding: 28px 0 24px; }
.status-heading { display: flex; align-items: flex-start; gap: 10px; }
.status-heading svg, .flow-actions svg { width: 20px; height: 20px; flex: none; }
.next-title { font-size: 1.125rem; font-weight: 650; margin: 0 0 8px; }
.next-description { margin: 0 0 16px; }
.flow-issues { padding-left: 20px; margin-bottom: 16px; color: var(--color-dim); line-height: 1.6; }
.flow-actions { display: flex; flex-wrap: wrap; gap: 8px; }
.flow-actions :deep(.app-btn), .flow-footer :deep(.app-btn), .step-detail :deep(.app-btn) { min-height: 44px; height: auto; white-space: normal; overflow-wrap: anywhere; padding-top: 10px; padding-bottom: 10px; max-width: 100%; font-size: 16px; }
.runtime-help { border-top: 1px solid var(--color-border); padding: 16px 0; }
.runtime-help summary { cursor: pointer; min-height: 44px; display: list-item; padding-top: 8px; }
.runtime-help p { margin: 8px 0; }
.runtime-help a, .dashboard-link { color: var(--color-primary); }
.flow-footer { border-top: 1px solid var(--color-border); padding-top: 12px; display: flex; align-items: center; flex-wrap: wrap; gap: 16px; }
.dashboard-link { display: inline-flex; align-items: center; min-height: 44px; text-decoration: none; }
.step-button:focus-visible, .runtime-help summary:focus-visible, .dashboard-link:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 3px; }
@media (min-width: 720px) { .onboarding { padding: 32px 32px 56px; } .project-choice { max-width: 560px; } }
</style>
