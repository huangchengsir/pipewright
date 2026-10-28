<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { useRunRetry } from '../../composables/useRunRetry'
import { retryNodeLabel, sourceLogLocation, type RetryReason } from '../../api/runRetry'
import AppButton from '../ui/AppButton.vue'

const props = defineProps<{ state: ReturnType<typeof useRunRetry>; sourceRunId?: string; attemptNumber?: number }>()
const emit = defineEmits<{ accepted: [runId: string] }>()
const { t, te } = useI18n()
const { context, contextError, plan, error, planLoading, submitting, querying, reconciling, pending, request, attempts, failedNodes, isHead, selected, rebuild, reconcileReasons } = props.state
const open = ref(false)
const heading = ref<HTMLElement | null>(null)
function show() { open.value = true; void nextTick(() => { heading.value?.focus(); heading.value?.scrollIntoView?.({ block: 'start' }) }) }
defineExpose({ show })
watch(planLoading, value => { if (value) open.value = true })
const groups = computed(() => [
  { key: 'execute', title: t('runRetry.execute'), nodes: plan.value?.nodes.filter(n => n.decision.action === 'execute') ?? [] },
  { key: 'reuse', title: t('runRetry.reuse'), nodes: plan.value?.nodes.filter(n => n.decision.action === 'reuse') ?? [] },
  { key: 'blocked', title: t('runRetry.blocked'), nodes: plan.value?.nodes.filter(n => n.decision.action === 'blocked') ?? [] },
])
const controls = computed(() => plan.value?.nodes.filter(n => n.decision.action === 'control') ?? [])
function reason(code: string) { return te(`runRetry.reasons.${code}`) ? t(`runRetry.reasons.${code}`) : t('runRetry.reasons.unknown_reason') }
function nodeName(key?: string) { const n = context.value?.graph.nodes.find(n => n.key === key); return n ? retryNodeLabel(n) : '' }
function detail(r: RetryReason) { return [nodeName(r.nodeKey), reason(r.code), ...(r.related ?? []).map(nodeName)].filter(Boolean).join(' · ') }
function toggleRebuild(nodeIds: string[], enabled: boolean) {
  const next = new Set(rebuild.value)
  nodeIds.forEach(id => enabled ? next.add(id) : next.delete(id))
  void props.state.preview(undefined, [...next])
}
function retryAll() { show(); if (failedNodes.value.length) void props.state.openPlan(failedNodes.value.map(n => n.node.key)) }
async function confirmPlan() { const result = await props.state.submit(); if (result?.attemptRunId) emit('accepted', result.attemptRunId) }
</script>

<template>
  <section class="retry-panel" :aria-label="t('runRetry.title')">
    <header class="retry-header">
      <div>
        <h2 ref="heading" tabindex="-1">{{ t('runRetry.title') }}</h2>
        <p v-if="sourceRunId">{{ t('runRetry.attempt', { n: attemptNumber ?? context?.revision ?? 1 }) }} · <RouterLink :to="{ name: 'run-detail', params: { id: sourceRunId } }">{{ t('runRetry.fromRun', { id: sourceRunId.slice(0, 8) }) }}</RouterLink></p>
        <p v-else>{{ t('runRetry.intro') }}</p>
      </div>
      <AppButton v-if="failedNodes.length" @click="retryAll">{{ t('runRetry.retryAll') }}</AppButton>
      <AppButton v-else @click="open = !open" :aria-expanded="open">{{ t('runRetry.viewRecovery') }}</AppButton>
    </header>
    <p v-if="context && !isHead" class="retry-warning" role="status">{{ t('runRetry.superseded') }} <RouterLink :to="{ name: 'run-detail', params: { id: context.headAttemptId } }">{{ t('runRetry.latestAttempt') }}</RouterLink></p>
    <p v-if="context?.outcomeUnknown" class="retry-warning">{{ t('runRetry.unknown') }}</p>
    <div v-if="pending" class="retry-request" aria-live="polite">
      <p>{{ request?.state === 'outcome_unknown' ? t('runRetry.unknown') : request ? t('runRetry.requestAccepted') : t('runRetry.verifyingOriginal') }}</p>
      <code>{{ pending.requestId }}</code>
      <RouterLink v-if="request?.attemptRunId" :to="{ name: 'run-detail', params: { id: request.attemptRunId } }">{{ t('runRetry.openAttempt') }}</RouterLink>
      <AppButton v-else :loading="querying" @click="state.queryOriginal">{{ t('runRetry.queryOriginal') }}</AppButton>
    </div>
    <div v-if="open" class="retry-content">
      <p v-if="!context && contextError" class="retry-warning">{{ t('runRetry.legacyUnavailable') }} {{ reason(contextError) }}</p>
      <div v-if="context" class="retry-source">
        <span>{{ t('runRetry.frozenSource') }}</span><code>{{ context.commitSha || t('runRetry.unboundSource') }}</code>
        <span>{{ t('runRetry.revision', { n: context.revision }) }}</span>
      </div>
      <p v-if="planLoading" aria-live="polite" :aria-busy="true">{{ t('runRetry.loadingPlan') }}</p>
      <AppButton v-if="!plan && selected.length && !pending && !planLoading" @click="state.preview()">{{ t('runRetry.refreshPlan') }}</AppButton>
      <div v-if="plan">
        <p class="retry-note">{{ t('runRetry.planNote') }}</p>
        <div class="retry-groups">
          <section v-for="group in groups" :key="group.key" :data-retry-group="group.key">
            <h3>{{ group.title }} <span>{{ group.nodes.length }}</span></h3>
            <p v-if="!group.nodes.length" class="retry-muted">{{ t('runRetry.none') }}</p>
            <ul v-else>
              <li v-for="item in group.nodes" :key="item.node.key">
                <strong>{{ retryNodeLabel(item.node) }}</strong>
                <span v-if="item.node.controlKey" class="retry-muted">{{ t('runRetry.controlNode') }}</span>
                <span v-if="item.decision.sourceAttemptId" class="retry-muted">{{ t('runRetry.fromRun', { id: item.decision.sourceAttemptId.slice(0, 8) }) }}</span>
                <RouterLink v-if="group.key === 'reuse' && sourceLogLocation(item)" :to="sourceLogLocation(item)!">{{ t('runRetry.sourceLogs') }}</RouterLink>
                <p v-for="(r, index) in item.decision.reasons ?? []" :key="index">{{ detail(r) }}</p>
                <p v-if="item.reasonCode">{{ reason(item.reasonCode) }}</p>
              </li>
            </ul>
          </section>
        </div>
        <section v-if="controls.length" class="retry-controls">
          <h3>{{ t('runRetry.controlRechecks') }}</h3>
          <ul><li v-for="item in controls" :key="item.node.key">{{ retryNodeLabel(item.node) }}</li></ul>
          <p class="retry-muted">{{ t('runRetry.approvalNote') }}</p>
        </section>
        <fieldset v-if="plan.rebuildGroups?.length" class="retry-rebuild" :disabled="submitting || !!pending">
          <legend>{{ t('runRetry.rebuildTitle') }}</legend>
          <p>{{ t('runRetry.rebuildNote') }}</p>
          <label v-for="(group, index) in plan.rebuildGroups" :key="index">
            <input type="checkbox" :checked="group.nodeIds.every(id => rebuild.includes(id))" @change="toggleRebuild(group.nodeIds, ($event.target as HTMLInputElement).checked)">
            <span>{{ group.nodeIds.map(nodeName).join(', ') }} — {{ reason(group.reasonCode) }}</span>
          </label>
        </fieldset>
        <ul v-if="plan.warnings.length" class="retry-warning" role="status"><li v-for="(r, index) in plan.warnings" :key="index">{{ detail(r) }}</li></ul>
        <ul v-if="plan.blockingReasons.length" class="retry-warning"><li v-for="(r, index) in plan.blockingReasons" :key="index">{{ detail(r) }}</li></ul>
        <div class="retry-actions">
          <AppButton variant="primary" :loading="submitting" :disabled="!plan.canSubmit || planLoading || !!pending || !isHead" @click="confirmPlan">{{ t('runRetry.confirm') }}</AppButton>
          <AppButton :disabled="submitting || !!pending" @click="state.preview()">{{ t('runRetry.refreshPlan') }}</AppButton>
        </div>
        <details class="retry-technical"><summary>{{ t('runRetry.technical') }}</summary><dl><dt>{{ t('runRetry.manifest') }}</dt><dd><code>{{ plan.manifestFingerprint }}</code></dd><dt>{{ t('runRetry.planIdentity') }}</dt><dd><code>{{ plan.planFingerprint }}</code></dd></dl></details>
      </div>
      <div v-if="context?.outcomeUnknown" class="retry-controls">
        <p>{{ t('runRetry.reconcileNote') }}</p>
        <AppButton :loading="reconciling" @click="state.reconcile">{{ t('runRetry.reconcile') }}</AppButton>
        <ul><li v-for="(r, index) in reconcileReasons" :key="index">{{ detail(r) }}</li></ul>
      </div>
      <section v-if="attempts?.items.length" class="retry-history">
        <h3>{{ t('runRetry.history') }}</h3>
        <ul><li v-for="attempt in attempts.items" :key="attempt.runId">
          <RouterLink :to="{ name: 'run-detail', params: { id: attempt.runId } }">{{ t('runRetry.attempt', { n: attempt.revision }) }} · {{ attempt.runId.slice(0, 8) }}</RouterLink>
          <span>{{ attempt.outcomeUnknown ? t('runRetry.unknown') : t(`runStatus.${attempt.status}`) }}</span>
          <span v-if="attempt.initiator">{{ attempt.initiator }}</span>
        </li></ul>
        <div class="retry-actions"><AppButton :disabled="attempts.page <= 1" @click="state.setHistoryPage(attempts.page - 1)">{{ t('runRetry.previous') }}</AppButton><AppButton :disabled="attempts.page * 20 >= attempts.total" @click="state.setHistoryPage(attempts.page + 1)">{{ t('runRetry.next') }}</AppButton></div>
      </section>
    </div>
    <p v-if="error" class="retry-warning" role="alert">{{ reason(error) }}</p>
  </section>
</template>

<style scoped>
.retry-panel { padding: 20px; border: 1px solid var(--color-border); border-radius: var(--rounded); background: var(--color-card); margin-bottom: 24px; color: var(--color-text); font-family: var(--font-sans); font-size: 16px; min-width: 0; }
.retry-panel h2 { font-size: 18px; margin: 0 0 8px; letter-spacing: -.02em; }
.retry-panel h3, .retry-panel legend { font-size: 16px; font-weight: 600; margin: 0 0 12px; }
.retry-panel p { margin: 8px 0; line-height: 1.6; }
.retry-header, .retry-actions, .retry-request { display: flex; flex-wrap: wrap; align-items: center; gap: 12px; }
.retry-header > div { flex: 1 1 240px; min-width: 0; }
.retry-content { margin-top: 20px; padding-top: 20px; border-top: 1px solid var(--color-border); }
.retry-groups { display: grid; gap: 16px; margin: 20px 0; }
.retry-groups section, .retry-controls { padding: 16px; background: var(--color-card-2); border: 1px solid var(--color-border); border-radius: var(--rounded); min-width: 0; }
.retry-panel ul { list-style: none; padding: 0; margin: 0; }
.retry-panel li { padding: 8px 0; overflow-wrap: anywhere; }
.retry-groups li + li { border-top: 1px solid var(--color-border); }
.retry-groups strong, .retry-groups .retry-muted { display: block; }
.retry-muted, .retry-note, .retry-source, .retry-header p { color: var(--color-dim); }
.retry-panel a { color: var(--color-primary); display: inline-flex; align-items: center; min-height: 44px; overflow-wrap: anywhere; }
.retry-panel :deep(.app-btn) { min-height: 44px; max-width: 100%; white-space: normal; height: auto; padding-top: 8px; padding-bottom: 8px; }
.retry-panel a:focus-visible, .retry-panel input:focus-visible, .retry-panel summary:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 3px; }
.retry-warning { padding: 12px; color: var(--color-amber); background: var(--color-amber-soft); border-left: 3px solid var(--color-amber); overflow-wrap: anywhere; }
.retry-panel .retry-warning { padding-left: 12px; }
.retry-source { display: flex; flex-direction: column; gap: 8px; overflow-wrap: anywhere; }
.retry-rebuild { border: 1px solid var(--color-border); padding: 16px; margin: 16px 0; border-radius: var(--rounded); }
.retry-rebuild label { display: flex; align-items: center; gap: 12px; min-height: 44px; cursor: pointer; line-height: 1.5; }
.retry-rebuild input { width: 20px; height: 20px; flex-shrink: 0; accent-color: var(--color-primary); }
.retry-controls, .retry-history, .retry-actions, .retry-technical { margin-top: 16px; }
.retry-history li { display: flex; flex-wrap: wrap; align-items: center; gap: 12px; }
.retry-technical summary { min-height: 44px; cursor: pointer; display: flex; align-items: center; }
.retry-technical dd { margin: 8px 0; overflow-wrap: anywhere; }
code { font-family: var(--font-mono); font-size: 13px; overflow-wrap: anywhere; }
@media (min-width: 1000px) { .retry-groups { grid-template-columns: repeat(3, minmax(0, 1fr)); } }
</style>
