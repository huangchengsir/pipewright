<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { Refresh, ShieldCheck } from '@vicons/tabler'
import AppButton from '../components/ui/AppButton.vue'
import { HttpError } from '../api/http'
import { approveMCPConfigDraft, getMCPConfigReview, type MCPConfigReview } from '../api/mcpConfig'
import { MCP_CONFIG_OPERATIONS } from '../api/mcp'
import '../styles/settings-surface.css'

const route = useRoute()
const { t, locale } = useI18n()
const review = ref<MCPConfigReview | null>(null)
const loading = ref(false)
const approving = ref(false)
const acknowledged = ref(false)
const error = ref('')
const approvalUncertain = ref(false)
const now = ref(Date.now())
let generation = 0
let alive = true
let controller: AbortController | undefined
const timer = setInterval(() => { now.value = Date.now() }, 1000)
const draftId = computed(() => typeof route.params.id === 'string' ? route.params.id : '')
const knownStates = new Set(['active', 'expired', 'invalidated', 'applied', 'none', 'granted', 'rejected', 'revoked', 'consumed', 'not_checked', 'passed', 'blocked', 'unknown', 'stale', 'failed', 'not_applicable'])
const knownChecks = new Set(['structural', 'crossSection', 'authorization', 'policy', 'sourceApplicability'])
const knownFields = new Set(['name', 'defaultBranch', 'timeoutSeconds', 'retries', 'cpu', 'memory', 'buildModel', 'toolchainLanguage', 'toolchainVersion', 'dockerfilePath', 'context'])
const knownImpacts = new Set(['identity', 'source', 'limits', 'build', 'display'])
const knownTypes = new Set<string>(MCP_CONFIG_OPERATIONS)
const expired = computed(() => !review.value || !Number.isFinite(Date.parse(review.value.expiresAt)) || Date.parse(review.value.expiresAt) <= now.value)
const granted = computed(() => review.value?.approval.state === 'granted' && Number.isFinite(Date.parse(review.value.approval.expiresAt ?? '')) && Date.parse(review.value.approval.expiresAt!) > now.value)
function scalar(value: unknown): boolean { return value == null || typeof value === 'string' || typeof value === 'boolean' || (typeof value === 'number' && Number.isFinite(value)) }
const complete = computed(() => {
  const item = review.value
  return !!item && item.complete === true && item.draftId === draftId.value && !!item.reviewDigest && !!item.baseRevision && !!item.policyVersion && Array.isArray(item.diff) && item.diff.length > 0 && item.diff.length <= 32 && item.diff.every(change => knownTypes.has(change.type) && knownFields.has(change.field) && scalar(change.before) && scalar(change.after))
})
const canApprove = computed(() => complete.value && !expired.value && !granted.value && review.value?.state === 'active' && review.value.validation.state === 'passed' && review.value.validation.applyAllowed === true && review.value.approval.reason !== 'apply_disabled' && !loading.value && !approving.value && !approvalUncertain.value && acknowledged.value)
function date(value?: string): string {
  const time = Date.parse(value ?? '')
  return Number.isFinite(time) ? new Date(time).toLocaleString(locale.value) : t('mcpConfig.notProvided')
}
function state(value?: string): string { return t(`mcpConfig.states.${knownStates.has(value ?? '') ? value : 'unknown'}`) }
function operation(value: string): string { return t(`mcpConfig.operations.${knownTypes.has(value) ? value.replaceAll('.', '_') : 'unknown'}`) }
function valueText(value: unknown): string { return value == null ? t('mcpConfig.notProvided') : scalar(value) ? String(value) : t('mcpConfig.withheld') }
function reason(code?: string): string {
  if (code === 'apply_disabled') return t('mcpConfig.applyDisabled')
  if (['snapshot_stale', 'revision_conflict', 'policy_changed', 'conflict'].includes(code ?? '')) return t('mcpConfig.reasonConflict')
  if (['approval_expired', 'draft_expired'].includes(code ?? '')) return t('mcpConfig.reasonExpired')
  if (['permission_denied', 'unauthorized', 'forbidden'].includes(code ?? '')) return t('mcpConfig.reasonPermission')
  if (['active_runs', 'project_busy'].includes(code ?? '')) return t('mcpConfig.reasonBusy')
  if (['template_coupled', 'topology_changed', 'unsupported_execution_source'].includes(code ?? '')) return t('mcpConfig.reasonUnsafe')
  return t('mcpConfig.reasonBlocked')
}

async function load(): Promise<void> {
  if (!alive || approving.value) return
  const ticket = ++generation
  controller?.abort()
  const request = new AbortController()
  controller = request
  review.value = null
  acknowledged.value = false
  error.value = ''
  loading.value = true
  try {
    const item = await getMCPConfigReview(draftId.value, { signal: request.signal })
    if (!alive || ticket !== generation) return
    review.value = item
    approvalUncertain.value = false
    now.value = Date.now()
  } catch {
    if (alive && ticket === generation) error.value = t('mcpConfig.loadFailed')
  } finally {
    if (alive && ticket === generation) loading.value = false
  }
}

async function approve(): Promise<void> {
  const item = review.value
  if (!canApprove.value || !item) return
  const ticket = generation
  const id = draftId.value
  approving.value = true
  error.value = ''
  try {
    const approval = await approveMCPConfigDraft(id, { reviewDigest: item.reviewDigest, baseRevision: item.baseRevision, policyVersion: item.policyVersion }, { signal: controller?.signal })
    if (!alive || ticket !== generation || review.value !== item) return
    item.approval = approval
    acknowledged.value = false
    now.value = Date.now()
  } catch (err) {
    if (!alive || ticket !== generation) return
    acknowledged.value = false
    if (err instanceof HttpError && err.status >= 400 && err.status < 500) {
      error.value = reason(err.apiError?.code)
      // A rejected approval must be freshly reviewed before it can be submitted again.
      review.value = null
    } else {
      approvalUncertain.value = true
      error.value = t('mcpConfig.approvalUnknown')
    }
  } finally {
    if (alive && ticket === generation) approving.value = false
  }
}

watch(draftId, () => { approving.value = false; approvalUncertain.value = false; void load() }, { immediate: true })
onBeforeUnmount(() => { alive = false; generation++; controller?.abort(); clearInterval(timer) })
</script>

<template>
  <main class="config-review settings-page" aria-labelledby="config-review-title">
    <header class="review-header">
      <div><p class="review-eyebrow">MCP</p><h1 id="config-review-title">{{ t('mcpConfig.title') }}</h1><p class="review-lede">{{ t('mcpConfig.subtitle') }}</p></div>
      <AppButton :loading="loading" :disabled="approving" @click="load"><Refresh class="review-icon" aria-hidden="true" />{{ t('common.refresh') }}</AppButton>
    </header>
    <p v-if="error" class="review-notice review-notice--error" role="alert">{{ error }}</p>
    <p v-if="loading" class="review-loading" role="status">{{ t('mcpConfig.loading') }}</p>
    <template v-if="review">
      <p class="review-identity"><span>{{ t('mcpConfig.project') }} · <code>{{ review.projectId }}</code></span><span>{{ t('mcpConfig.draft') }} · <code>{{ review.draftId }}</code></span></p>
      <div class="review-summary"><span class="review-status">{{ state(expired ? 'expired' : review.state) }}</span><span>{{ t('mcpConfig.expires') }} · {{ date(review.expiresAt) }}</span></div>
      <details class="review-details">
        <summary>{{ t('mcpConfig.technicalDetails') }}</summary>
        <dl class="review-meta">
          <div><dt>{{ t('mcpConfig.project') }}</dt><dd><code>{{ review.projectId }}</code></dd></div>
          <div><dt>{{ t('mcpConfig.draft') }}</dt><dd><code>{{ review.draftId }}</code></dd></div>
          <div><dt>{{ t('mcpConfig.baseRevision') }}</dt><dd><code>{{ review.baseRevision }}</code></dd></div>
          <div><dt>{{ t('mcpConfig.policyVersion') }}</dt><dd><code>{{ review.policyVersion }}</code></dd></div>
        </dl>
      </details>
      <details class="review-details review-context"><summary>{{ t('mcpConfig.reviewScope') }}</summary><div class="review-notice" role="note"><ShieldCheck class="review-icon" aria-hidden="true" /><div><p>{{ t('mcpConfig.hiddenPreserved') }}</p><p>{{ t('mcpConfig.notExecuted') }}</p></div></div></details>
      <p class="review-trigger-impact">{{ t('mcpConfig.triggerImpact') }}</p>
      <p v-if="!complete" class="review-notice review-notice--error" role="alert">{{ t('mcpConfig.incomplete') }}</p>
      <p v-if="expired" class="review-notice review-notice--error" role="alert">{{ t('mcpConfig.reasonExpired') }}</p>
      <p v-else-if="review.state === 'invalidated'" class="review-notice review-notice--error" role="alert">{{ t('mcpConfig.reasonConflict') }}</p>
      <section class="review-section" aria-labelledby="config-review-changes">
        <h2 id="config-review-changes">{{ t('mcpConfig.changes') }} <span>{{ review.diff.length }}</span></h2>
        <article v-for="(change, index) in review.diff" :key="index" class="review-change">
          <header><h3>{{ operation(change.type) }}</h3><code v-if="change.stageId || change.jobId">{{ [change.stageId, change.jobId].filter(Boolean).join(' / ') }}</code></header>
          <p class="review-field">{{ knownFields.has(change.field) ? t(`mcpConfig.fields.${change.field}`) : t('mcpConfig.withheld') }}</p>
          <p v-if="knownImpacts.has(change.impact)" class="review-impact">{{ t(`mcpConfig.impacts.${change.impact}`) }}</p>
          <div class="review-values"><div><span>{{ t('mcpConfig.before') }}</span><pre>{{ valueText(change.before) }}</pre></div><div class="review-value--after"><span>{{ t('mcpConfig.after') }}</span><pre>{{ valueText(change.after) }}</pre></div></div>
        </article>
      </section>
      <section class="review-section" aria-labelledby="config-review-validation">
        <h2 id="config-review-validation">{{ t('mcpConfig.validation') }} <span>{{ state(review.validation.state) }}</span></h2>
        <dl class="review-checks"><div v-for="(check, index) in review.validation.checks" :key="index"><dt>{{ knownChecks.has(check.category) ? t(`mcpConfig.checks.${check.category}`) : t('mcpConfig.validation') }}</dt><dd>{{ state(check.status) }}</dd></div></dl>
        <ul v-if="review.validation.issues.length" class="review-issues"><li v-for="(issue, index) in review.validation.issues" :key="index">{{ reason(issue.code) }}</li></ul>
      </section>
      <section class="review-approval" aria-labelledby="config-review-approval">
        <p><a href="/mcp/config-maintenance">{{ t('mcpConfig.maintenanceTitle') }}</a></p>
        <h2 id="config-review-approval">{{ t('mcpConfig.approvalTitle') }} <span>{{ state(review.approval.state) }}</span></h2>
        <p>{{ t('mcpConfig.approvalOnly') }}</p>
        <p v-if="granted" class="review-approved" role="status">{{ t('mcpConfig.approvedUntil', { time: date(review.approval.expiresAt) }) }}</p>
        <p v-else-if="review.approval.reason" class="review-issues">{{ reason(review.approval.reason) }}</p>
        <label class="review-ack"><input v-model="acknowledged" type="checkbox" :disabled="!complete || expired || approving || granted || approvalUncertain || review.state !== 'active'" />{{ t('mcpConfig.acknowledge') }}</label>
        <AppButton class="review-approve-button" variant="primary" :loading="approving" :disabled="!canApprove" @click="approve">{{ t('mcpConfig.approve') }}</AppButton>
      </section>
    </template>
  </main>
</template>

<style scoped>
.review-identity { display: flex; flex-wrap: wrap; gap: 6px 16px; margin: 8px 0 12px; color: var(--color-dim); font-size: 12px; overflow-wrap: anywhere; }
.config-review { width: 100%; max-width: 1120px; padding: 0 0 32px; margin: 0 auto; min-width: 0; color: var(--color-text); font-size: var(--settings-text-size, 13px); }
.review-header { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; margin-bottom: 18px; flex-wrap: wrap; }
.review-header > div { min-width: 0; }
.review-header :deep(.app-btn) { height: var(--settings-action-height, 34px); }
.review-eyebrow { margin: 0 0 5px; color: var(--color-primary); font: 600 11px var(--font-mono); }
h1 { margin: 0; font-size: 20px; line-height: 1.35; letter-spacing: -.02em; overflow-wrap: anywhere; }
.review-lede { max-width: 72ch; margin: 7px 0 0; color: var(--color-dim); line-height: 1.6; font-size: var(--settings-label-size, 13px); }
.review-icon { width: 16px; height: 16px; flex: 0 0 auto; }
.review-summary { display: flex; flex-wrap: wrap; align-items: center; gap: 10px 18px; font-size: 12px; color: var(--color-dim); margin: 0 0 12px; }
.review-status { color: var(--color-primary); background: var(--color-primary-soft); border-radius: 4px; padding: 3px 7px; }
.review-details { margin: 10px 0; color: var(--color-dim); font-size: 12px; }
.review-details > summary { min-height: 28px; cursor: pointer; width: fit-content; }
.review-details > summary:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 2px; }
.review-meta { margin: 8px 0 14px; display: grid; gap: 12px; padding: 14px 16px; border: 1px solid var(--color-border); border-radius: 7px; background: var(--color-card); }
.review-meta dt, .review-checks dt { color: var(--color-dim); font-size: 12px; margin-bottom: 5px; }
.review-meta dd, .review-checks dd { margin: 0; overflow-wrap: anywhere; }
code, pre { font-family: var(--font-mono); }
.review-meta code { font-size: 12px; }
.review-notice { display: flex; align-items: flex-start; gap: 8px; padding: 10px 12px; margin: 10px 0; background: var(--color-inset); border: 1px solid var(--color-border); border-radius: 6px; font-size: 12px; line-height: 1.65; }
.review-notice p { margin: 0; }
.review-notice p + p { margin-top: 5px; }
.review-notice--error { color: var(--color-red); border-color: var(--color-red-soft); background: var(--color-red-soft); }
.review-trigger-impact { color: var(--color-dim); font-size: 12px; line-height: 1.6; margin: 10px 0 18px; }
.review-loading { padding: 24px 0; color: var(--color-dim); }
.review-section { margin: 18px 0; padding: 16px 18px; border: 1px solid var(--color-border); border-radius: 9px; background: var(--color-card); }
h2 { display: flex; align-items: baseline; flex-wrap: wrap; gap: 10px; margin: 0 0 14px; font-size: 14px; }
h2 > span { color: var(--color-dim); font-size: 12px; font-weight: 400; }
.review-change { border-top: 1px solid var(--color-border); padding: 14px 0 0; }
.review-change + .review-change { margin-top: 14px; }
.review-change > header { display: flex; align-items: baseline; justify-content: space-between; flex-wrap: wrap; gap: 8px; }
h3 { margin: 0; font-size: 13px; font-weight: 600; }
.review-change header code { color: var(--color-dim); font-size: 11px; overflow-wrap: anywhere; }
.review-field { margin: 8px 0; color: var(--color-dim); font-size: 12px; }
.review-impact { margin: 0 0 10px; color: var(--color-amber); font-size: 12px; line-height: 1.6; }
.review-values { display: grid; gap: 10px; }
.review-values > div { padding: 11px 12px; background: var(--color-inset); border: 1px solid var(--color-border); border-radius: 6px; min-width: 0; }
.review-values span { font-size: 11px; color: var(--color-dim); }
.review-values pre { margin: 7px 0 0; color: var(--color-text); white-space: pre-wrap; overflow-wrap: anywhere; font-size: 12px; line-height: 1.65; }
.review-values .review-value--after { border-inline-start: 2px solid var(--color-primary); }
.review-checks { display: grid; gap: 10px; margin: 0; }
.review-checks > div { display: flex; align-items: baseline; justify-content: space-between; gap: 12px; padding-bottom: 9px; border-bottom: 1px solid var(--color-border); font-size: 12px; }
.review-checks dt { margin: 0; }
.review-issues { color: var(--color-amber); font-size: 13px; line-height: 1.6; padding-left: 18px; }
.review-approval { padding: 16px 18px; border: 1px solid var(--color-border); border-radius: 9px; background: var(--color-card); }
.review-approval > p { font-size: 12px; color: var(--color-dim); line-height: 1.65; max-width: 80ch; }
.review-approval > .review-approved { color: var(--color-green); }
.review-ack { display: flex; gap: 8px; align-items: flex-start; padding: 12px 0; font-size: 13px; line-height: 1.6; cursor: pointer; }
.review-ack input { margin: 3px 0 0; accent-color: var(--color-primary); width: 16px; height: 16px; flex-shrink: 0; }
.review-approve-button { width: 100%; min-height: var(--settings-action-height, 34px); height: var(--settings-action-height, 34px); }
@media (min-width: 640px) { .review-meta, .review-values { grid-template-columns: repeat(2, minmax(0, 1fr)); } .review-approve-button { width: auto; } }
@media (max-width: 639px) { .review-ack { min-height: 44px; font-size: 14px; } .review-details > summary { min-height: 44px; align-content: center; } .review-section, .review-approval { padding: 16px; } }
</style>
