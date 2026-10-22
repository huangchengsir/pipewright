<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { Refresh, ShieldCheck } from '@vicons/tabler'
import AppButton from '../components/ui/AppButton.vue'
import { HttpError } from '../api/http'
import { approveMCPConfigDraft, getMCPConfigReview, type MCPConfigReview } from '../api/mcpConfig'
import { MCP_CONFIG_OPERATIONS } from '../api/mcp'

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
  <main class="config-review" aria-labelledby="config-review-title">
    <header class="review-header">
      <div><p class="review-eyebrow">MCP</p><h1 id="config-review-title">{{ t('mcpConfig.title') }}</h1><p class="review-lede">{{ t('mcpConfig.subtitle') }}</p></div>
      <AppButton :loading="loading" :disabled="approving" @click="load"><Refresh class="review-icon" aria-hidden="true" />{{ t('common.refresh') }}</AppButton>
    </header>
    <p v-if="error" class="review-notice review-notice--error" role="alert">{{ error }}</p>
    <p v-if="loading" class="review-loading" role="status">{{ t('mcpConfig.loading') }}</p>
    <template v-if="review">
      <dl class="review-meta">
        <div><dt>{{ t('mcpConfig.project') }}</dt><dd><code>{{ review.projectId }}</code></dd></div>
        <div><dt>{{ t('mcpConfig.draft') }}</dt><dd><code>{{ review.draftId }}</code></dd></div>
        <div><dt>{{ t('mcpConfig.baseRevision') }}</dt><dd><code>{{ review.baseRevision }}</code></dd></div>
        <div><dt>{{ t('mcpConfig.policyVersion') }}</dt><dd><code>{{ review.policyVersion }}</code></dd></div>
        <div><dt>{{ t('mcpConfig.expires') }}</dt><dd>{{ date(review.expiresAt) }}</dd></div>
        <div><dt>{{ t('settingsMCP.status') }}</dt><dd>{{ state(expired ? 'expired' : review.state) }}</dd></div>
      </dl>
      <div class="review-notice" role="note"><ShieldCheck class="review-icon" aria-hidden="true" /><div><p>{{ t('mcpConfig.hiddenPreserved') }}</p><p>{{ t('mcpConfig.notExecuted') }}</p><p>{{ t('mcpConfig.triggerImpact') }}</p></div></div>
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
.config-review { width: 100%; max-width: 1040px; padding: 24px 16px 48px; margin: 0 auto; min-width: 0; color: var(--color-text); }
.review-header { display: flex; align-items: flex-start; justify-content: space-between; gap: 20px; margin-bottom: 24px; }
.review-eyebrow { margin: 0 0 6px; color: var(--color-primary); font: 600 12px var(--font-mono); letter-spacing: .06em; }
h1 { margin: 0; font-size: 26px; line-height: 1.25; letter-spacing: -.02em; }
.review-lede { max-width: 65ch; margin: 12px 0 0; color: var(--color-dim); line-height: 1.65; }
.review-icon { width: 18px; height: 18px; flex: 0 0 auto; }
.review-meta { margin: 0 0 24px; display: grid; gap: 18px; padding: 20px; border: 1px solid var(--color-border-strong); border-radius: var(--rounded); background: var(--color-card); }
.review-meta dt, .review-checks dt { color: var(--color-dim); font-size: var(--text-label); margin-bottom: 5px; }
.review-meta dd, .review-checks dd { margin: 0; overflow-wrap: anywhere; }
code, pre { font-family: var(--font-mono); }
.review-meta code { font-size: 12px; }
.review-notice { display: flex; align-items: flex-start; gap: 12px; padding: 16px; margin: 16px 0; background: var(--color-inset); border-left: 3px solid var(--color-border-strong); font-size: 14px; line-height: 1.65; }
.review-notice p { margin: 0; }
.review-notice p + p { margin-top: 6px; }
.review-notice--error { color: var(--color-red); border-left-color: var(--color-red); background: var(--color-red-soft); }
.review-loading { padding: 36px 0; color: var(--color-dim); }
.review-section { margin: 28px 0; }
h2 { display: flex; align-items: baseline; flex-wrap: wrap; gap: 12px; margin: 0 0 16px; font-size: 18px; }
h2 > span { color: var(--color-dim); font-size: 13px; font-weight: 400; }
.review-change { border-top: 1px solid var(--color-border-strong); padding: 18px 0; }
.review-change > header { display: flex; align-items: baseline; justify-content: space-between; flex-wrap: wrap; gap: 8px; }
h3 { margin: 0; font-size: 15px; font-weight: 600; }
.review-change header code { color: var(--color-dim); font-size: 12px; overflow-wrap: anywhere; }
.review-field { margin: 10px 0; color: var(--color-dim); font-size: 13px; }
.review-impact { margin: 0 0 12px; color: var(--color-amber); font-size: 13px; line-height: 1.6; }
.review-values { display: grid; gap: 8px; }
.review-values > div { padding: 12px 14px; background: var(--color-inset); border: 1px solid var(--color-border); border-radius: var(--rounded); min-width: 0; }
.review-values span { font-size: 12px; color: var(--color-dim); }
.review-values pre { margin: 8px 0 0; color: var(--color-text); white-space: pre-wrap; overflow-wrap: anywhere; font-size: 13px; line-height: 1.6; }
.review-values .review-value--after { border-left: 3px solid var(--color-primary); }
.review-checks { display: grid; gap: 14px; margin: 0; }
.review-checks > div { display: flex; align-items: baseline; justify-content: space-between; gap: 12px; padding-bottom: 10px; border-bottom: 1px solid var(--color-border); }
.review-checks dt { margin: 0; }
.review-issues { color: var(--color-amber); font-size: 14px; line-height: 1.7; padding-left: 20px; }
.review-approval { padding: 22px 0 0; border-top: 1px solid var(--color-border-strong); }
.review-approval > p { font-size: 14px; color: var(--color-dim); line-height: 1.65; max-width: 72ch; }
.review-approval > .review-approved { color: var(--color-green); }
.review-ack { display: flex; gap: 10px; align-items: flex-start; padding: 16px 0; font-size: 16px; line-height: 1.6; cursor: pointer; }
.review-ack input { margin: 5px 0 0; accent-color: var(--color-primary); width: 18px; height: 18px; flex-shrink: 0; }
.review-approve-button { width: 100%; min-height: 44px; }
@media (min-width: 640px) { .config-review { padding: 32px 28px 64px; } .review-meta { grid-template-columns: repeat(2, minmax(0, 1fr)); } .review-values { grid-template-columns: repeat(2, minmax(0, 1fr)); } .review-approve-button { width: auto; } }
@media (min-width: 1000px) { .review-meta { grid-template-columns: repeat(3, minmax(0, 1fr)); } }
</style>
