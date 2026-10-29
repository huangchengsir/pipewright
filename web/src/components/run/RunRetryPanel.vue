<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { useRunRetry } from '../../composables/useRunRetry'
import { retryNodeLabel, sourceLogLocation, type RetryReason } from '../../api/runRetry'
import AppButton from '../ui/AppButton.vue'
import RunTerminal from './RunTerminal.vue'
import type { RunStep } from '../../api/runs'

const props = defineProps<{ state: ReturnType<typeof useRunRetry>; sourceRunId?: string; attemptNumber?: number; runId?: string; steps?: RunStep[]; runStatus?: string }>()
const emit = defineEmits<{ accepted: [runId: string]; fullLogs: [] }>()
const { t, te } = useI18n()
const { context, contextError, plan, error, planLoading, submitting, querying, reconciling, pending, request, attempts, failedNodes, isHead, selected, rebuild, reconcileReasons } = props.state
const evidenceUnavailable = computed(() => ['manifest_missing', 'snapshot_unavailable', 'retry_evidence_unavailable'].includes(contextError.value))
const open = ref(false)
const mode = ref<'details' | 'plan' | 'history' | 'logs'>('details')
const nodeKey = ref<string>()
const logNodeKeys = ref<string[]>([])
const logStageName = ref('')
const logTabs = computed(() => logNodeKeys.value.map(key => {
  const node = context.value?.nodes.find(n => n.node.key === key)
  const step = (props.steps ?? []).find(s => s.id === (node?.attemptStepId ?? key))
  return { key, label: node ? retryNodeLabel(node.node) : step?.name ?? key, status: node?.decision.status ?? step?.status, reused: node?.decision.action === 'reuse', control: !!node?.node.controlKey }
}))
const activeTabIndex = computed(() => logTabs.value.findIndex(tab => tab.key === nodeKey.value))
const heading = ref<HTMLElement | null>(null)
const dialog = ref<HTMLElement | null>(null)
let returnFocus: HTMLElement | null = null
const focusedNode = computed(() => context.value?.nodes.find(n => n.node.key === nodeKey.value))
const actualStep = computed(() => (props.steps ?? []).find(step => step.id === (focusedNode.value?.attemptStepId ?? nodeKey.value)))
const title = computed(() => mode.value === 'logs' && logStageName.value ? logStageName.value : focusedNode.value ? retryNodeLabel(focusedNode.value.node) : actualStep.value?.name ? actualStep.value.name : mode.value === 'history' ? t('runRetry.runHistory') : mode.value === 'plan' ? t('runRetry.retryAll') : t('runRetry.viewRecovery'))
async function show(key?: string, view: 'details' | 'plan' | 'history' | 'logs' = 'details') {
  nodeKey.value = key; mode.value = view
  if (!open.value) returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
  open.value = true
  await nextTick(); heading.value?.focus({ preventScroll: true })
}
const logSource = computed(() => {
  const node = focusedNode.value
  if (node?.decision.action === 'reuse') {
    const source = sourceLogLocation(node)
    return source ? { runId: source.params.id, ordinal: Number(source.query.step), stepId: source.query.stepId, live: false } : null
  }
  if (!props.runId) return null
  const ordinal = node ? node.ordinal : (props.steps ?? []).findIndex(step => step.id === nodeKey.value)
  const stepId = node?.attemptStepId ?? nodeKey.value
  if (!Number.isInteger(ordinal) || ordinal < 0 || !stepId || props.steps?.[ordinal]?.id !== stepId) return null
  return { runId: props.runId, ordinal, stepId, live: props.runStatus === 'running' }
})
const fullLogRunId = computed(() => focusedNode.value?.decision.action === 'reuse' ? focusedNode.value.decision.sourceAttemptId ?? props.runId : props.runId)
function showLogs(keys: string[] | string, stageName = '') {
  logNodeKeys.value = [...new Set(typeof keys === 'string' ? [keys] : keys)]
  logStageName.value = stageName
  const tabs = logTabs.value
  const failed = tabs.find(tab => !tab.control && ['failed', 'outcome_unknown'].includes(tab.status ?? '')) ?? tabs.find(tab => ['failed', 'outcome_unknown'].includes(tab.status ?? ''))
  return show((failed ?? tabs[0])?.key, 'logs')
}
async function selectLogTab(index: number, moveFocus = false) {
  const tab = logTabs.value[index]; if (!tab) return
  nodeKey.value = tab.key
  if (moveFocus) { await nextTick(); dialog.value?.querySelector<HTMLButtonElement>(`#retry-log-tab-${index}`)?.focus() }
}
function tabKeydown(event: KeyboardEvent, index: number) {
  let next = index
  if (event.key === 'ArrowDown') next = (index + 1) % logTabs.value.length
  else if (event.key === 'ArrowUp') next = (index + logTabs.value.length - 1) % logTabs.value.length
  else if (event.key === 'Home') next = 0
  else if (event.key === 'End') next = logTabs.value.length - 1
  else return
  event.preventDefault(); void selectLogTab(next, true)
}
function openFullLogs(event: MouseEvent) {
  if (fullLogRunId.value === props.runId) { event.preventDefault(); emit('fullLogs') }
  close()
}
function showHistory() { void show(undefined, 'history') }
function close() {
  open.value = false
  if (returnFocus?.isConnected) returnFocus.focus({ preventScroll: true })
}
function keydown(event: KeyboardEvent) {
  if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); close(); return }
  if (event.key !== 'Tab') return
  const controls = [...(dialog.value?.querySelectorAll<HTMLElement>('button:not(:disabled), a[href], input:not(:disabled), select:not(:disabled), textarea:not(:disabled), summary, [tabindex="0"]') ?? [])].filter(el => !(el.getAttribute('role') === 'tab' && el.tabIndex < 0) && !el.closest('[hidden], [inert], fieldset:disabled') && (!el.closest('details:not([open])') || el.tagName === 'SUMMARY'))
  const first = controls[0]; const last = controls.at(-1)
  if (!first) { event.preventDefault(); heading.value?.focus(); return }
  if (event.shiftKey && (document.activeElement === first || !controls.includes(document.activeElement as HTMLElement))) { event.preventDefault(); last?.focus() }
  else if (!event.shiftKey && (document.activeElement === last || !controls.includes(document.activeElement as HTMLElement))) { event.preventDefault(); first.focus() }
}
defineExpose({ show, showLogs, showHistory, close })
onBeforeUnmount(() => { if (open.value && returnFocus?.isConnected) returnFocus.focus({ preventScroll: true }) })
const groups = computed(() => [
  { key: 'execute', title: t('runRetry.execute'), nodes: plan.value?.nodes.filter(n => n.decision.action === 'execute') ?? [] },
  { key: 'reuse', title: t('runRetry.reuse'), nodes: plan.value?.nodes.filter(n => n.decision.action === 'reuse') ?? [] },
  { key: 'blocked', title: t('runRetry.blocked'), nodes: plan.value?.nodes.filter(n => n.decision.action === 'blocked') ?? [] },
])
const controls = computed(() => plan.value?.nodes.filter(n => n.decision.action === 'control') ?? [])
function reason(code: string) { return te(`runRetry.reasons.${code}`) ? t(`runRetry.reasons.${code}`) : t('runRetry.reasons.unknown_reason') }
function statusLabel(status?: string) {
  if (status === 'outcome_unknown') return t('runRetry.unknown')
  if (status === 'pending') return t('run.stageStatusPending')
  if (status === 'skipped') return t('run.stageStatusSkipped')
  return status && te(`runStatus.${status}`) ? t(`runStatus.${status}`) : t('runRetry.nodeRecovery')
}
function nodeName(key?: string) { const n = context.value?.graph.nodes.find(n => n.key === key); return n ? retryNodeLabel(n) : '' }
function dependencies(key?: string) { return context.value?.graph.edges.filter(edge => edge.to === key && edge.kind === 'control').map(edge => nodeName(edge.from)) ?? [] }
function detail(r: RetryReason) { return [nodeName(r.nodeKey), reason(r.code), ...(r.related ?? []).map(nodeName)].filter(Boolean).join(' · ') }
function toggleRebuild(nodeIds: string[], enabled: boolean) {
  const next = new Set(rebuild.value)
  nodeIds.forEach(id => enabled ? next.add(id) : next.delete(id))
  void props.state.preview(undefined, [...next])
}
function retryAll() { void show(undefined, 'plan'); if (failedNodes.value.length) void props.state.openPlan(failedNodes.value.map(n => n.node.key)) }
function retryFocused() { const key = nodeKey.value; if (key) { void show(key, 'plan'); void props.state.openPlan([key]) } }
async function confirmPlan() { const result = await props.state.submit(); if (result?.attemptRunId) emit('accepted', result.attemptRunId) }
</script>

<template>
  <div v-if="pending" class="retry-request retry-request-summary" role="status" aria-live="polite">
    <span>{{ request?.state === 'outcome_unknown' ? t('runRetry.unknown') : request ? t('runRetry.requestAccepted') : t('runRetry.verifyingOriginal') }}</span>
    <RouterLink @click="close" v-if="request?.attemptRunId" :to="{ name: 'run-detail', params: { id: request.attemptRunId } }">{{ t('runRetry.openAttempt') }}</RouterLink>
    <AppButton v-else :loading="querying" @click="state.queryOriginal">{{ t('runRetry.queryOriginal') }}</AppButton>
    <AppButton variant="ghost" @click="show()">{{ t('runRetry.viewRecovery') }}</AppButton>
  </div>
  <div v-if="!open && error" class="retry-closed-error" role="alert">
    <span>{{ reason(error) }}</span>
    <AppButton variant="ghost" @click="show()">{{ t('runRetry.viewRecovery') }}</AppButton>
  </div>
  <Teleport to="body">
  <div v-if="open" class="retry-overlay" @click.self="close" @keydown="keydown">
  <section ref="dialog" class="retry-panel" :class="`retry-panel--${mode}`" role="dialog" aria-modal="true" aria-labelledby="retry-dialog-title" :aria-busy="planLoading || submitting">
    <header class="retry-header">
      <div>
        <p class="retry-eyebrow">{{ mode === 'history' ? t('runRetry.runHistory') : mode === 'logs' ? t('runRetry.nodeLogs') : t('runRetry.nodeRecovery') }}</p>
        <h2 id="retry-dialog-title" ref="heading" tabindex="-1">{{ title }}</h2>
        <p v-if="sourceRunId && mode !== 'details' && mode !== 'logs'">{{ t('runRetry.attempt', { n: attemptNumber ?? context?.revision ?? 1 }) }} · <RouterLink @click="close" :to="{ name: 'run-detail', params: { id: sourceRunId } }">{{ t('runRetry.fromRun', { id: sourceRunId.slice(0, 8) }) }}</RouterLink></p>
      </div>
      <AppButton class="retry-close" @click="close">{{ t('runRetry.close') }}</AppButton>
    </header>
    <p v-if="context && !isHead" class="retry-warning" role="status">{{ t('runRetry.superseded') }} <RouterLink @click="close" :to="{ name: 'run-detail', params: { id: context.headAttemptId } }">{{ t('runRetry.latestAttempt') }}</RouterLink></p>
    <p v-if="context?.outcomeUnknown" class="retry-warning">{{ t('runRetry.unknown') }}</p>
    <div v-if="pending" class="retry-request" aria-live="polite">
      <p>{{ request?.state === 'outcome_unknown' ? t('runRetry.unknown') : request ? t('runRetry.requestAccepted') : t('runRetry.verifyingOriginal') }}</p>
      <code>{{ pending.requestId }}</code>
      <RouterLink @click="close" v-if="request?.attemptRunId" :to="{ name: 'run-detail', params: { id: request.attemptRunId } }">{{ t('runRetry.openAttempt') }}</RouterLink>
      <AppButton v-else :loading="querying" @click="state.queryOriginal">{{ t('runRetry.queryOriginal') }}</AppButton>
    </div>
    <div class="retry-content">
      <section v-if="mode === 'logs'" class="retry-log-view">
        <div v-if="logTabs.length" class="retry-log-tabs" role="tablist" aria-orientation="vertical" :aria-label="t('runRetry.logSteps')">
          <button v-for="(tab, index) in logTabs" :id="`retry-log-tab-${index}`" :key="tab.key" type="button" role="tab" :aria-selected="nodeKey === tab.key" aria-controls="retry-log-panel" :tabindex="nodeKey === tab.key ? 0 : -1" :title="tab.label" @click="selectLogTab(index)" @keydown="tabKeydown($event, index)">
            <span class="retry-log-tab-name">{{ tab.label }}</span>
            <span class="retry-log-tab-status">{{ tab.reused ? t('runRetry.reusedShort') : tab.status === 'outcome_unknown' ? t('runRetry.unknownShort') : statusLabel(tab.status) }}</span>
          </button>
        </div>
        <div id="retry-log-panel" class="retry-log-step" role="tabpanel" :aria-labelledby="activeTabIndex >= 0 ? `retry-log-tab-${activeTabIndex}` : 'retry-dialog-title'" tabindex="0">
        <div class="retry-log-status">
          <strong>{{ focusedNode ? retryNodeLabel(focusedNode.node) : actualStep?.name }}</strong>
          <span>{{ focusedNode?.decision.action === 'reuse' ? t('runRetry.reusedShort') : statusLabel(focusedNode?.decision.status ?? actualStep?.status) }}</span>
          <AppButton v-if="focusedNode && ['failed', 'outcome_unknown'].includes(focusedNode.decision.status ?? '')" class="retry-focused" :disabled="!!pending || submitting" @click="retryFocused">{{ t('runRetry.retryNode') }}</AppButton>
        </div>
        <div v-if="logSource" class="retry-log-surface">
          <RunTerminal :key="`${logSource.runId}:${logSource.stepId}`" :run-id="logSource.runId" :live="logSource.live" :filter-ordinal="logSource.ordinal" />
        </div>
        <p v-else class="retry-muted">{{ t('runRetry.nodeLogsUnavailable') }}</p>
        <RouterLink v-if="fullLogRunId" @click="openFullLogs" :to="{ name: 'run-detail', params: { id: fullLogRunId } }">{{ t('runRetry.fullLogs') }}</RouterLink>
        <details v-if="focusedNode" class="retry-node-evidence">
          <summary>{{ t('runRetry.viewRecovery') }}</summary>
          <p v-if="focusedNode.decision.sourceAttemptId"><RouterLink @click="close" :to="{ name: 'run-detail', params: { id: focusedNode.decision.sourceAttemptId } }">{{ t('runRetry.fromRun', { id: focusedNode.decision.sourceAttemptId.slice(0, 8) }) }}</RouterLink></p>
          <RouterLink v-if="sourceLogLocation(focusedNode)" @click="close" :to="sourceLogLocation(focusedNode)!">{{ t('runRetry.sourceLogs') }}</RouterLink>
          <p v-for="(r, index) in focusedNode.decision.reasons ?? []" :key="index">{{ detail(r) }}</p>
          <p v-if="focusedNode.reasonCode">{{ reason(focusedNode.reasonCode) }}</p>
          <p v-if="dependencies(nodeKey).length">{{ t('runRetry.dependsOn', { names: dependencies(nodeKey).join(', ') }) }}</p>
        </details>
        </div>
      </section>
      <section v-if="focusedNode && mode !== 'logs'" class="retry-node-detail">
        <p><strong>{{ focusedNode.decision.action === 'reuse' ? t('runRetry.reused') : focusedNode.decision.action === 'blocked' ? t('runRetry.blocked') : statusLabel(focusedNode.decision.status) }}</strong></p>
        <p v-if="focusedNode.node.controlKey" class="retry-muted">{{ t('runRetry.controlNode') }}</p>
        <RouterLink @click="close" v-if="focusedNode.decision.sourceAttemptId" :to="{ name: 'run-detail', params: { id: focusedNode.decision.sourceAttemptId } }">{{ t('runRetry.fromRun', { id: focusedNode.decision.sourceAttemptId.slice(0, 8) }) }}</RouterLink>
        <RouterLink @click="close" v-if="sourceLogLocation(focusedNode)" :to="sourceLogLocation(focusedNode)!">{{ t('runRetry.sourceLogs') }}</RouterLink>
        <p v-for="(r, index) in focusedNode.decision.reasons ?? []" :key="index">{{ detail(r) }}</p>
        <p v-if="focusedNode.reasonCode">{{ reason(focusedNode.reasonCode) }}</p>
        <AppButton class="retry-focused" v-if="mode !== 'plan' && ['failed', 'outcome_unknown'].includes(focusedNode.decision.status ?? '')" :disabled="!!pending || submitting" @click="retryFocused">{{ t('runRetry.retryNode') }}</AppButton>
      </section>
      <p v-if="mode === 'history'" class="retry-muted">{{ t('runRetry.historyNote') }}</p>
      <p v-if="mode === 'history' && !attempts?.items.length" class="retry-muted">{{ t('runRetry.none') }}</p>
      <AppButton v-if="mode !== 'history' && mode !== 'logs' && !focusedNode && failedNodes.length" class="retry-all" :disabled="!!pending || submitting" @click="retryAll">{{ t('runRetry.retryAll') }}</AppButton>
      <div v-if="!context && contextError && (mode !== 'logs' || !evidenceUnavailable)" class="retry-unavailable">
        <p v-if="evidenceUnavailable">{{ t('runRetry.legacyUnavailable') }}</p>
        <p v-else role="alert">{{ reason(contextError) }}</p>
        <details v-if="evidenceUnavailable"><summary>{{ t('runRetry.technical') }}</summary><p>{{ reason(contextError) }}</p></details>
      </div>
      <div v-if="context && mode === 'plan'" class="retry-source">
        <span>{{ t('runRetry.frozenSource') }}</span><code>{{ context.commitSha || t('runRetry.unboundSource') }}</code>
        <span>{{ t('runRetry.revision', { n: context.revision }) }}</span>
      </div>
      <p v-if="mode === 'plan' && planLoading" aria-live="polite" :aria-busy="true">{{ t('runRetry.loadingPlan') }}</p>
      <AppButton v-if="mode === 'plan' && !plan && selected.length && !pending && !planLoading" @click="state.preview()">{{ t('runRetry.refreshPlan') }}</AppButton>
      <div v-if="mode === 'plan' && plan">
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
                <RouterLink @click="close" v-if="group.key === 'reuse' && sourceLogLocation(item)" :to="sourceLogLocation(item)!">{{ t('runRetry.sourceLogs') }}</RouterLink>
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
      <section v-if="mode === 'history' && attempts?.items.length" class="retry-history">
        <h3>{{ t('runRetry.runHistory') }}</h3>
        <ul><li v-for="attempt in attempts.items" :key="attempt.runId">
          <RouterLink @click="close" :to="{ name: 'run-detail', params: { id: attempt.runId } }">{{ t('runRetry.attempt', { n: attempt.revision }) }} · {{ attempt.runId.slice(0, 8) }}</RouterLink>
          <span>{{ attempt.outcomeUnknown ? t('runRetry.unknown') : t(`runStatus.${attempt.status}`) }}</span>
          <span v-if="attempt.initiator">{{ attempt.initiator }}</span>
        </li></ul>
        <div class="retry-actions"><AppButton :disabled="attempts.page <= 1" @click="state.setHistoryPage(attempts.page - 1)">{{ t('runRetry.previous') }}</AppButton><AppButton :disabled="attempts.page * 20 >= attempts.total" @click="state.setHistoryPage(attempts.page + 1)">{{ t('runRetry.next') }}</AppButton></div>
      </section>
    </div>
    <p v-if="error" class="retry-warning" role="alert">{{ reason(error) }}</p>
  </section>
  </div>
  </Teleport>
</template>

<style scoped>
.retry-overlay {
  position: fixed;
  inset: 0;
  z-index: 8000;
  display: grid;
  place-items: center;
  padding: 16px;
  background: rgb(0 0 0 / 55%);
}
.retry-panel {
  box-sizing: border-box;
  width: min(640px, 100%);
  max-height: calc(100dvh - 32px);
  overflow: auto;
  overscroll-behavior: contain;
  padding: 20px;
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded);
  background: var(--color-card);
  color: var(--color-text);
  font-family: var(--font-sans);
  font-size: 16px;
  min-width: 0;
  box-shadow: var(--shadow-modal);
  overflow-wrap: anywhere;
}
.retry-panel--plan {
  width: min(960px, 100%);
}
.retry-panel--logs {
  width: min(900px, 100%);
}
.retry-panel--history {
  width: min(560px, 100%);
}
.retry-panel h2 {
  font-size: 18px;
  margin: 0;
  letter-spacing: -0.02em;
  line-height: 1.4;
}
.retry-panel h3,
.retry-panel legend {
  font-size: 16px;
  font-weight: 600;
  margin: 0 0 12px;
}
.retry-panel p {
  margin: 8px 0;
  line-height: 1.6;
}
.retry-header,
.retry-actions,
.retry-request {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 12px;
}
.retry-header {
  align-items: flex-start;
}
.retry-header > div {
  flex: 1 1 200px;
  min-width: 0;
}
.retry-panel .retry-eyebrow {
  color: var(--color-dim);
  font-size: 13px;
  margin: 0 0 8px;
}
.retry-close {
  flex: 0 0 auto;
}
.retry-content {
  margin-top: 20px;
  padding-top: 16px;
  border-top: 1px solid var(--color-border);
}
.retry-log-view {
  display: grid;
  grid-template-columns: minmax(88px, 24%) minmax(0, 1fr);
  gap: 12px;
  min-width: 0;
}
.retry-log-tabs {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
  max-height: 62dvh;
  overflow: auto;
  padding-right: 4px;
  border-right: 1px solid var(--color-border);
}
.retry-log-tabs button {
  display: flex;
  flex-direction: column;
  justify-content: center;
  gap: 4px;
  min-width: 0;
  width: 100%;
  min-height: 44px;
  padding: 8px;
  text-align: left;
  background: transparent;
  border: 1px solid transparent;
  border-radius: var(--rounded);
  font-family: var(--font-sans);
  font-size: 16px;
  color: var(--color-text);
  cursor: pointer;
}
.retry-log-tabs button[aria-selected='true'] {
  background: var(--color-primary-soft);
  border-color: var(--color-primary);
}
.retry-log-tabs button:focus-visible,
.retry-log-step:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: -2px;
}
.retry-log-tab-name {
  width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.retry-log-tab-status {
  font-size: 12px;
  color: var(--color-dim);
}
.retry-log-step {
  min-width: 0;
}
.retry-log-status strong {
  flex: 1 1 180px;
  min-width: 0;
  overflow-wrap: anywhere;
}
.retry-log-status {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  margin-bottom: 12px;
}
.retry-log-surface {
  min-width: 0;
}
.retry-log-surface :deep(.term),
.retry-log-surface :deep(.term-scroll) {
  background: #000;
  color: #f5f5f5;
}
.retry-log-surface :deep(.term-text),
.retry-log-surface :deep(.term-line--err .term-text) {
  color: #f5f5f5;
}
.retry-log-surface :deep(.term-label),
.retry-log-surface :deep(.term-ln),
.retry-log-surface :deep(.term-count),
.retry-log-surface :deep(.term-state) {
  color: #bec3cc;
}
.retry-node-evidence summary {
  min-height: 44px;
  display: flex;
  align-items: center;
  cursor: pointer;
}
.retry-closed-error {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  color: var(--color-amber);
  font-size: 14px;
  margin-bottom: 16px;
}
.retry-node-detail {
  padding-bottom: 16px;
  margin-bottom: 16px;
  border-bottom: 1px solid var(--color-border);
}
.retry-node-detail > a {
  margin-right: 16px;
}
.retry-groups {
  display: grid;
  gap: 16px;
  margin: 20px 0;
}
.retry-groups section,
.retry-controls {
  padding: 16px;
  background: var(--color-card-2);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded);
  min-width: 0;
}
.retry-panel ul {
  list-style: none;
  padding: 0;
  margin: 0;
}
.retry-panel li {
  padding: 8px 0;
  overflow-wrap: anywhere;
}
.retry-groups li + li {
  border-top: 1px solid var(--color-border);
}
.retry-groups strong,
.retry-groups .retry-muted {
  display: block;
}
.retry-muted,
.retry-note,
.retry-source,
.retry-header p {
  color: var(--color-dim);
}
.retry-panel a,
.retry-request-summary a {
  color: var(--color-primary);
  display: inline-flex;
  align-items: center;
  min-height: 44px;
  overflow-wrap: anywhere;
}
.retry-panel :deep(.app-btn),
.retry-request-summary :deep(.app-btn) {
  min-height: 44px;
  max-width: 100%;
  white-space: normal;
  height: auto;
  padding-top: 8px;
  padding-bottom: 8px;
}
.retry-panel a:focus-visible,
.retry-panel input:focus-visible,
.retry-panel summary:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 3px;
}
.retry-warning {
  padding: 12px;
  color: var(--color-amber);
  background: var(--color-amber-soft);
  border-left: 3px solid var(--color-amber);
  overflow-wrap: anywhere;
}
.retry-panel .retry-warning {
  padding-left: 12px;
}
.retry-source {
  display: flex;
  flex-direction: column;
  gap: 8px;
  overflow-wrap: anywhere;
}
.retry-rebuild {
  border: 1px solid var(--color-border);
  padding: 16px;
  margin: 16px 0;
  border-radius: var(--rounded);
  min-width: 0;
}
.retry-rebuild label {
  display: flex;
  align-items: center;
  gap: 12px;
  min-height: 44px;
  cursor: pointer;
  line-height: 1.5;
}
.retry-rebuild input {
  width: 20px;
  height: 20px;
  flex-shrink: 0;
  accent-color: var(--color-primary);
}
.retry-controls,
.retry-history,
.retry-actions,
.retry-technical {
  margin-top: 16px;
}
.retry-history li {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 12px;
}
.retry-technical summary {
  min-height: 44px;
  cursor: pointer;
  display: flex;
  align-items: center;
}
.retry-technical dd {
  margin: 8px 0;
  overflow-wrap: anywhere;
}
.retry-request-summary {
  margin: 0 0 16px;
  padding: 8px 12px;
  border-left: 2px solid var(--color-amber);
  background: var(--color-card-2);
  color: var(--color-dim);
  font-size: 14px;
  overflow-wrap: anywhere;
}
code {
  font-family: var(--font-mono);
  font-size: 13px;
  overflow-wrap: anywhere;
}
@media (min-width: 900px) {
  .retry-groups {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }
}
.retry-unavailable {
  margin-top: 12px;
  padding-top: 12px;
  border-top: 1px solid var(--color-border);
  color: var(--color-dim);
}
.retry-unavailable summary {
  min-height: 44px;
  display: flex;
  align-items: center;
  cursor: pointer;
  color: var(--color-primary);
}
@media (min-width: 640px) {
  .retry-panel {
    font-size: 13px;
    padding: 20px;
  }
  .retry-panel h2 {
    font-size: 17px;
  }
  .retry-panel h3,
  .retry-panel legend {
    font-size: 14px;
  }
  .retry-panel .retry-eyebrow {
    font-size: 12px;
    margin-bottom: 4px;
  }
  .retry-content {
    margin-top: 16px;
    padding-top: 16px;
  }
  .retry-log-view {
    grid-template-columns: minmax(140px, 22%) minmax(0, 1fr);
    gap: 16px;
  }
  .retry-log-tabs button {
    font-size: 13px;
    padding: 7px 10px;
    gap: 2px;
  }
  .retry-panel a,
  .retry-node-evidence summary,
  .retry-technical summary,
  .retry-unavailable summary,
  .retry-rebuild label {
    min-height: 34px;
  }
  .retry-panel :deep(.app-btn),
  .retry-request-summary :deep(.app-btn) {
    min-height: 34px;
    padding-block: 6px;
    font-size: 13px;
  }
  .retry-groups {
    gap: 12px;
    margin: 16px 0;
  }
}
</style>
