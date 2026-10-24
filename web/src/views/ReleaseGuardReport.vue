<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import AppButton from '../components/ui/AppButton.vue'
import GuardImageIdentity from '../components/run/GuardImageIdentity.vue'
import ReleaseGuardPlan from '../components/run/ReleaseGuardPlan.vue'
import ReleaseGuardInvestigation from '../components/run/ReleaseGuardInvestigation.vue'
import ReleaseGuardAnalysis from '../components/run/ReleaseGuardAnalysis.vue'
import { HttpError } from '../api/http'
import { releaseGuardText } from '../utils/releaseGuardText'
import {
  getReleaseGuard,
  guardIsActive,
  guardReportLocation,
  listGuardEvidence,
  stopReleaseGuard,
  type GuardDuration,
  type GuardEvidence,
  type GuardReport,
} from '../api/releaseGuard'

const route = useRoute()
const router = useRouter()
const { t, te, locale } = useI18n()
const projectId = computed(() => String(route.params.projectId ?? ''))
const guardId = computed(() => String(route.params.guardId ?? ''))
const report = ref<GuardReport | null>(null)
const evidence = ref<GuardEvidence[]>([])
const loading = ref(false)
const loadingEvidence = ref(false)
const stopping = ref(false)
const error = ref('')
const evidenceError = ref('')
const stopError = ref('')
const hasMore = ref(false)
const evidenceCursor = ref(0)
const showReverify = ref(false)
const tab = ref('verification')
const tabs = ['verification', 'investigation', 'plan'] as const
let alive = true
let generation = 0
let controller: AbortController | undefined
let evidenceController: AbortController | undefined
let stopController: AbortController | undefined
let poll: ReturnType<typeof setTimeout> | undefined
const active = computed(
  () => !!report.value && guardIsActive(report.value.executionStatus),
)
const reverifyDuration = computed<GuardDuration>(() =>
  [300, 600, 900].includes(report.value?.plan.durationSeconds ?? 0)
    ? (report.value!.plan.durationSeconds as GuardDuration)
    : 600,
)
const canStop = computed(
  () =>
    active.value &&
    report.value?.executionStatus !== 'stopping' &&
    !stopping.value &&
    !loading.value,
)
function when(value?: string): string {
  const time = Date.parse(value ?? '')
  return Number.isFinite(time)
    ? new Date(time).toLocaleString(locale.value)
    : t('releaseGuard.notProvided')
}
function result(value: string | null): string {
  const known = [
    'passed',
    'failed',
    'insufficient',
    'uncertain',
    'not_applicable',
  ]
  return t(
    `releaseGuard.result.${known.includes(value ?? '') ? value : 'pending'}`,
  )
}
function execution(value: string): string {
  return t(
    `releaseGuard.execution.${['prepared', 'queued', 'binding', 'observing', 'stopping', 'completed', 'stopped', 'interrupted', 'blocked', 'tool_error', 'expired'].includes(value) ? value : 'unknown'}`,
  )
}
function attribution(value: string): string {
  return t(
    `releaseGuard.attribution.${['not_investigated', 'candidate_relation', 'undetermined'].includes(value) ? value : 'undetermined'}`,
  )
}
function condition(value: string): string {
  return te(`releaseGuard.conditions.${value}`)
    ? t(`releaseGuard.conditions.${value}`)
    : value
}
function fact(value: string): string {
  return releaseGuardText(value, (key) => t(key))
}
function message(cause: unknown): string {
  return cause instanceof HttpError
    ? (cause.apiError?.message ?? t('releaseGuard.requestFailed'))
    : t('releaseGuard.requestFailed')
}
function schedule(): void {
  clearTimeout(poll)
  if (alive && active.value) poll = setTimeout(() => void load(true), 4000)
}
async function loadEvidence(ticket = generation): Promise<void> {
  if (loadingEvidence.value || report.value?.evidenceExpired) return
  const request = new AbortController()
  evidenceController = request
  loadingEvidence.value = true
  try {
    const page = await listGuardEvidence(
      projectId.value,
      guardId.value,
      evidenceCursor.value,
      { signal: request.signal },
    )
    if (!alive || ticket !== generation || request.signal.aborted) return
    const ids = new Set(evidence.value.map((item) => item.id))
    evidence.value.push(...page.items.filter((item) => !ids.has(item.id)))
    evidenceCursor.value = page.nextCursor
    hasMore.value = page.hasMore
    evidenceError.value = ''
  } catch (cause) {
    if (alive && ticket === generation && !request.signal.aborted)
      evidenceError.value = message(cause)
  } finally {
    if (alive && ticket === generation) loadingEvidence.value = false
  }
}
async function load(silent = false): Promise<void> {
  clearTimeout(poll)
  controller?.abort()
  const ticket = generation
  const request = new AbortController()
  controller = request
  if (!silent) loading.value = true
  try {
    const item = await getReleaseGuard(projectId.value, guardId.value, {
      signal: request.signal,
    })
    if (!alive || ticket !== generation || request.signal.aborted) return
    if (item.id !== guardId.value || item.projectId !== projectId.value)
      throw new Error('Mismatched report identity')
    report.value = item
    error.value = ''
    if (item.evidenceExpired) {
      evidenceController?.abort()
      loadingEvidence.value = false
      evidence.value = []
      hasMore.value = false
    } else await loadEvidence(ticket)
  } catch (cause) {
    if (alive && ticket === generation && !request.signal.aborted)
      error.value = message(cause)
  } finally {
    if (alive && ticket === generation && !request.signal.aborted) {
      loading.value = false
      schedule()
    }
  }
}
async function stop(): Promise<void> {
  if (!canStop.value) return
  const ticket = generation
  stopping.value = true
  stopError.value = ''
  clearTimeout(poll)
  controller?.abort()
  const request = new AbortController()
  stopController = request
  try {
    const item = await stopReleaseGuard(projectId.value, guardId.value, {
      signal: request.signal,
    })
    if (alive && ticket === generation && !request.signal.aborted)
      report.value = item
  } catch (cause) {
    if (alive && ticket === generation && !request.signal.aborted)
      stopError.value = message(cause)
  } finally {
    if (alive && ticket === generation) {
      stopping.value = false
      schedule()
    }
  }
}
function selectTab(event: KeyboardEvent, index: number): void {
  if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return
  event.preventDefault()
  const next =
    event.key === 'Home'
      ? 0
      : event.key === 'End'
        ? tabs.length - 1
        : (index + (event.key === 'ArrowRight' ? 1 : -1) + tabs.length) %
          tabs.length
  tab.value = tabs[next]
  document.getElementById(`guard-tab-${tab.value}`)?.focus()
}
async function started(item: GuardReport): Promise<void> {
  showReverify.value = false
  await router.push(guardReportLocation(item.projectId, item.id))
}
function updatedInvestigation(item: GuardReport): void {
  if (item.id !== guardId.value || item.projectId !== projectId.value) return
  if (report.value?.evidenceExpired && !item.evidenceExpired) return
  controller?.abort()
  loading.value = false
  report.value = item
  schedule()
}
function evidenceLink(id: string): string {
  return `#guard-evidence-${encodeURIComponent(id)}`
}
watch(
  () => [projectId.value, guardId.value],
  () => {
    generation++
    controller?.abort()
    evidenceController?.abort()
    stopController?.abort()
    clearTimeout(poll)
    report.value = null
    evidence.value = []
    evidenceCursor.value = 0
    hasMore.value = false
    loadingEvidence.value = false
    stopping.value = false
    showReverify.value = false
    error.value = ''
    evidenceError.value = ''
    stopError.value = ''
    void load()
  },
  { immediate: true },
)
onBeforeUnmount(() => {
  alive = false
  generation++
  controller?.abort()
  evidenceController?.abort()
  stopController?.abort()
  clearTimeout(poll)
})
</script>

<template>
  <div class="guard-report">
    <header class="guard-report__header">
      <div>
        <RouterLink
          v-if="report"
          :to="{ name: 'run-detail', params: { id: report.runId } }"
          class="guard-back"
          >← {{ t('releaseGuard.sourceRun') }}</RouterLink
        >
        <h1>{{ t('releaseGuard.reportTitle') }}</h1>
      </div>
      <AppButton :loading="loading" :disabled="stopping" @click="load()">{{
        t('common.refresh')
      }}</AppButton>
    </header>
    <p v-if="error" class="guard-error" role="alert">{{ error }}</p>
    <p v-if="loading && !report" role="status">
      {{ t('releaseGuard.loading') }}
    </p>
    <template v-if="report">
      <section
        class="guard-verdict"
        :data-result="report.verificationResult || 'pending'"
      >
        <p class="guard-eyebrow">{{ t('releaseGuard.basicVerification') }}</p>
        <h2>{{ result(report.verificationResult) }}</h2>
        <div class="guard-facts">
          <span
            >{{ t('releaseGuard.executionTitle') }}:
            <strong>{{ execution(report.executionStatus) }}</strong></span
          ><span
            >{{ t('releaseGuard.attributionTitle') }}:
            {{ attribution(report.attributionStatus) }}</span
          >
        </div>
        <p v-if="report.reason" class="guard-muted">
          {{ fact(report.reason) }}
        </p>
      </section>
      <section class="guard-context">
        <p>
          <strong>{{
            active
              ? t('releaseGuard.observationWindow')
              : t('releaseGuard.historicalReport')
          }}</strong>
          · {{ when(report.startedAt) }} →
          {{
            report.finishedAt
              ? when(report.finishedAt)
              : t('releaseGuard.windowInProgress')
          }}
        </p>
        <p>
          {{ t('releaseGuard.attempt') }} <code>{{ report.attemptId }}</code> ·
          {{ t('releaseGuard.reportId') }} <code>{{ report.id }}</code>
        </p>
        <p v-if="report.plan.mode === 'from_now'" class="guard-notice">
          {{ t('releaseGuard.fromNowGap') }}
        </p>
        <p
          v-if="report.identityTrust === 'host_identity_unverified'"
          class="guard-notice"
        >
          {{ t('releaseGuard.hostTrust') }}
        </p>
        <p v-if="report.evidenceExpired" class="guard-notice">
          {{ t('releaseGuard.evidenceExpired') }}
        </p>
        <p v-else class="guard-muted">
          {{
            t('releaseGuard.evidenceRetention', {
              at: when(report.evidenceExpiresAt),
            })
          }}
        </p>
        <template v-if="report.parentGuardId"
          ><RouterLink
            :to="guardReportLocation(projectId, report.parentGuardId)"
            >{{ t('releaseGuard.previousReport') }}</RouterLink
          >
          <p>
            {{
              report.comparison.sameConditions
                ? t('releaseGuard.sameConditions')
                : t('releaseGuard.changedConditions')
            }}
          </p>
          <ul v-if="report.comparison.changes.length">
            <li v-for="item in report.comparison.changes" :key="item">
              {{ fact(item) }}
            </li>
          </ul></template
        >
      </section>
      <div class="guard-actions">
        <AppButton
          v-if="active"
          variant="danger"
          :disabled="!canStop"
          :loading="stopping || report.executionStatus === 'stopping'"
          @click="stop"
          >{{
            report.executionStatus === 'stopping'
              ? t('releaseGuard.stopping')
              : t('releaseGuard.stopObservation')
          }}</AppButton
        ><AppButton v-if="!active" @click="showReverify = true">{{
          t('releaseGuard.reverify')
        }}</AppButton>
      </div>
      <p v-if="stopError" class="guard-error" role="alert">{{ stopError }}</p>
      <div
        class="guard-tabs"
        role="tablist"
        :aria-label="t('releaseGuard.reportSections')"
      >
        <button
          v-for="(item, index) in tabs"
          :id="`guard-tab-${item}`"
          :key="item"
          type="button"
          role="tab"
          :aria-selected="tab === item"
          :aria-controls="`guard-tabpanel-${item}`"
          :tabindex="tab === item ? 0 : -1"
          @click="tab = item"
          @keydown="selectTab($event, index)"
        >
          {{ t(`releaseGuard.tabs.${item}`) }}
        </button>
      </div>
      <section
        v-show="tab === 'verification'"
        id="guard-tabpanel-verification"
        class="guard-content"
        role="tabpanel"
        aria-labelledby="guard-tab-verification"
      >
        <h2>{{ t('releaseGuard.targetChecks') }}</h2>
        <p class="guard-muted">{{ t('releaseGuard.sampledOnly') }}</p>
        <div class="guard-table-wrap">
          <table>
            <thead>
              <tr>
                <th>{{ t('releaseGuard.target') }}</th>
                <th>{{ t('releaseGuard.coverage') }}</th>
                <th>{{ t('releaseGuard.observationWindow') }}</th>
                <th>{{ t('releaseGuard.conclusion') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="target in report.targets" :key="target.id">
                <td>
                  <strong>{{ target.serverName }}</strong
                  ><br /><code>{{
                    target.containerId || t('releaseGuard.notProvided')
                  }}</code>
                </td>
                <td>
                  {{ target.coverage.successfulSamples }} /
                  {{ target.coverage.expectedSamples }}<br /><span
                    class="guard-muted"
                    >{{
                      t('releaseGuard.maximumGap', {
                        seconds: target.coverage.maxGapSeconds,
                      })
                    }}</span
                  ><br />{{
                    target.coverage.complete
                      ? t('releaseGuard.coverageComplete')
                      : t('releaseGuard.coverageGap')
                  }}
                </td>
                <td>
                  {{ when(target.readyAt) }} → {{ when(target.deadline) }}
                </td>
                <td
                  class="guard-result"
                  :data-result="target.verificationResult || 'pending'"
                >
                  {{ result(target.verificationResult) }}
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <article
          v-for="target in report.targets"
          :key="`conditions-${target.id}`"
          class="guard-target"
        >
          <h3>{{ target.serverName }}</h3>
          <GuardImageIdentity v-if="target.expectedImage" :image="target.expectedImage" />
          <ul class="guard-condition-list">
            <li v-for="item in target.conditions" :key="item.name">
              <strong>{{ condition(item.name) }}</strong
              ><span class="guard-result" :data-result="item.result">{{
                result(item.result)
              }}</span>
              <p v-if="item.reason">{{ fact(item.reason) }}</p>
              <span class="guard-evidence-links"
                ><a
                  v-for="id in item.evidenceIds"
                  :key="id"
                  :href="evidenceLink(id)"
                  >{{ id }}</a
                ></span
              >
            </li>
          </ul>
          <ul v-if="target.limitations.length" class="guard-limitations">
            <li v-for="item in target.limitations" :key="item">
              {{ fact(item) }}
            </li>
          </ul>
        </article>
        <h2>{{ t('releaseGuard.timeline') }}</h2>
        <ol class="guard-timeline">
          <li v-for="event in report.timeline" :key="event.id">
            <time>{{ when(event.at) }}</time>
            <p>{{ fact(event.kind) }} · {{ fact(event.summary) }}</p>
            <span class="guard-evidence-links"
              ><a
                v-for="id in event.evidenceIds"
                :key="id"
                :href="evidenceLink(id)"
                >{{ id }}</a
              ></span
            >
          </li>
        </ol>
        <p v-if="!report.timeline.length" class="guard-muted">
          {{ t('releaseGuard.noTimeline') }}
        </p>
      </section>
      <section
        v-show="tab === 'investigation'"
        id="guard-tabpanel-investigation"
        class="guard-content"
        role="tabpanel"
        aria-labelledby="guard-tab-investigation"
      >
        <h2>{{ t('releaseGuard.investigationTitle') }}</h2>
        <ReleaseGuardInvestigation
          :report="report"
          @updated="updatedInvestigation"
        />
        <ReleaseGuardAnalysis :report="report" />
        <p class="guard-notice">{{ t('releaseGuard.candidateDisclaimer') }}</p>
        <p class="guard-muted">
          {{ t('releaseGuard.baseline') }}:
          {{ fact(report.investigation.baselineStatus) }}
        </p>
        <p v-if="!report.investigation.candidates.length" class="guard-muted">
          {{ t('releaseGuard.noCandidates') }}
        </p>
        <article
          v-for="candidate in report.investigation.candidates"
          :key="candidate.id"
          class="guard-candidate"
        >
          <h3>{{ fact(candidate.title) }}</h3>
          <div>
            <h4>{{ t('releaseGuard.supportingEvidence') }}</h4>
            <span v-if="!candidate.supportingEvidenceIds.length">{{
              t('releaseGuard.notProvided')
            }}</span
            ><span class="guard-evidence-links"
              ><a
                v-for="id in candidate.supportingEvidenceIds"
                :key="id"
                :href="evidenceLink(id)"
                >{{ id }}</a
              ></span
            >
          </div>
          <div>
            <h4>{{ t('releaseGuard.counterEvidence') }}</h4>
            <span v-if="!candidate.counterEvidenceIds.length">{{
              t('releaseGuard.notProvided')
            }}</span
            ><span class="guard-evidence-links"
              ><a
                v-for="id in candidate.counterEvidenceIds"
                :key="id"
                :href="evidenceLink(id)"
                >{{ id }}</a
              ></span
            >
            <ul>
              <li v-for="item in candidate.missingEvidence" :key="item">
                {{ fact(item) }}
              </li>
            </ul>
          </div>
          <div>
            <h4>{{ t('releaseGuard.nextCheck') }}</h4>
            <p>
              {{
                candidate.nextReadOnlyCheck
                  ? fact(candidate.nextReadOnlyCheck)
                  : t('releaseGuard.notProvided')
              }}
            </p>
          </div>
        </article>
        <ul
          v-if="report.investigation.limitations.length"
          class="guard-limitations"
        >
          <li v-for="item in report.investigation.limitations" :key="item">
            {{ fact(item) }}
          </li>
        </ul>
      </section>
      <section
        v-show="tab === 'plan'"
        id="guard-tabpanel-plan"
        class="guard-content"
        role="tabpanel"
        aria-labelledby="guard-tab-plan"
      >
        <h2>{{ t('releaseGuard.planAndScope') }}</h2>
        <dl class="guard-plan-facts">
          <dt>{{ t('releaseGuard.duration') }}</dt>
          <dd>
            {{
              t('releaseGuard.minutes', { n: report.plan.durationSeconds / 60 })
            }}
          </dd>
          <dt>{{ t('releaseGuard.planHash') }}</dt>
          <dd>
            <code>{{ report.plan.hash }}</code>
          </dd>
          <dt>{{ t('releaseGuard.checksTitle') }}</dt>
          <dd>{{ t('releaseGuard.checksDescription') }}</dd>
        </dl>
        <p>{{ t('releaseGuard.samplingDescription') }}</p>
        <p class="guard-notice">{{ t('releaseGuard.localOnly') }}</p>
        <ul v-if="report.plan.limitations.length">
          <li v-for="item in report.plan.limitations" :key="item">
            {{ fact(item) }}
          </li>
        </ul>
        <ul class="guard-plan-targets">
          <li v-for="target in report.plan.targets" :key="target.id">
            <strong>{{ target.serverName }} / {{ target.containerName }}</strong
            ><GuardImageIdentity :image="target.expectedImage" /><span>{{
              t(
                `releaseGuard.availability.${['available', 'identity_unavailable', 'unsupported'].includes(target.availability) ? target.availability : 'unsupported'}`,
              )
            }}</span>
          </li>
        </ul>
      </section>
      <section class="guard-content guard-evidence-section">
        <h2>{{ t('releaseGuard.evidenceTitle') }}</h2>
        <p v-if="evidenceError" class="guard-error" role="alert">
          {{ evidenceError }}
        </p>
        <p
          v-if="!evidence.length && !report.evidenceExpired"
          class="guard-muted"
        >
          {{ t('releaseGuard.noEvidence') }}
        </p>
        <article
          v-for="item in evidence"
          :id="`guard-evidence-${encodeURIComponent(item.id)}`"
          :key="item.id"
          class="guard-evidence"
        >
          <header>
            <code>{{ item.id }}</code
            ><span>{{ item.kind }} · {{ item.targetId }}</span>
          </header>
          <p>{{ fact(item.summary) }}</p>
          <dl>
            <dt>{{ t('releaseGuard.eventTime') }}</dt>
            <dd>{{ when(item.eventAt) }}</dd>
            <dt>{{ t('releaseGuard.collectedTime') }}</dt>
            <dd>{{ when(item.collectedAt) }}</dd>
          </dl>
          <p v-if="item.reason">{{ fact(item.reason) }}</p>
          <p v-if="item.truncated" class="guard-notice">
            {{ t('releaseGuard.truncated') }}
          </p>
          <details>
            <summary>{{ t('releaseGuard.evidenceDetails') }}</summary>
            <pre>{{ JSON.stringify(item.value, null, 2) }}</pre>
          </details>
        </article>
        <AppButton
          v-if="hasMore || evidenceError"
          :loading="loadingEvidence"
          @click="loadEvidence()"
          >{{
            hasMore
              ? t('releaseGuard.moreEvidence')
              : t('releaseGuard.retryEvidence')
          }}</AppButton
        >
      </section>
      <ReleaseGuardPlan
        v-if="showReverify"
        :key="report.id"
        :project-id="projectId"
        :attempt-id="report.attemptId"
        :parent-guard-id="report.id"
        :initial-duration-seconds="reverifyDuration"
        :initial-include-logs="report.plan.includeLogs"
        @close="showReverify = false"
        @started="started"
      />
    </template>
  </div>
</template>

<style scoped>
.guard-report {
  max-width: 1180px;
  margin: 0 auto;
  color: var(--color-text);
  display: grid;
  gap: 20px;
}
.guard-report__header,
.guard-actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}
h1 {
  margin: 8px 0 0;
  font-size: 1.6rem;
}
h2 {
  margin: 0 0 12px;
  font-size: 1.1rem;
}
h3 {
  margin: 0 0 10px;
  font-size: 1rem;
}
h4 {
  margin: 0 0 6px;
  font-size: 0.875rem;
  color: var(--color-dim);
}
p {
  margin: 0;
  line-height: 1.65;
}
a {
  color: var(--color-primary);
}
a:focus-visible,
button:focus-visible,
summary:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 3px;
}
.guard-back {
  font-size: 0.875rem;
  text-decoration: none;
}
.guard-eyebrow {
  color: var(--color-dim);
  font-size: 0.8rem;
  margin-bottom: 8px;
}
.guard-verdict {
  padding: 24px;
  border: 1px solid var(--color-border);
  border-left: 4px solid var(--color-border-strong);
  background: var(--color-card);
  border-radius: var(--rounded);
}
.guard-verdict h2 {
  font-size: 1.6rem;
  margin: 0 0 14px;
}
.guard-verdict[data-result='failed'] {
  border-left-color: var(--color-red);
}
.guard-verdict[data-result='passed'] {
  border-left-color: var(--color-green);
}
.guard-verdict[data-result='insufficient'],
.guard-verdict[data-result='uncertain'] {
  border-left-color: var(--color-amber);
}
.guard-facts {
  display: flex;
  gap: 12px 24px;
  flex-wrap: wrap;
  font-size: 0.875rem;
}
.guard-context {
  display: grid;
  gap: 10px;
}
.guard-muted {
  color: var(--color-dim);
  font-size: 0.8rem;
}
.guard-notice {
  padding: 10px 12px;
  background: var(--color-amber-soft);
  border-left: 2px solid var(--color-amber);
  font-size: 0.85rem;
}
.guard-error {
  color: var(--color-red);
}
code {
  font-family: var(--font-mono);
  font-size: 0.75rem;
  overflow-wrap: anywhere;
}
.guard-actions {
  justify-content: flex-start;
}
.guard-report :deep(.app-btn) {
  min-height: 44px;
}
.guard-tabs {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  border-bottom: 1px solid var(--color-border);
}
.guard-tabs button {
  border: 0;
  border-bottom: 2px solid transparent;
  padding: 12px 16px;
  min-height: 44px;
  font: inherit;
  font-size: 0.875rem;
  cursor: pointer;
  background: transparent;
  color: var(--color-dim);
}
.guard-tabs button[aria-selected='true'] {
  border-bottom-color: var(--color-primary);
  color: var(--color-text);
  font-weight: 600;
}
.guard-content {
  min-width: 0;
  background: var(--color-card);
  padding: 18px;
  border: 1px solid var(--color-border);
  border-radius: var(--rounded);
}
.guard-table-wrap {
  overflow-x: auto;
  margin: 16px 0 22px;
}
table {
  border-collapse: collapse;
  width: 100%;
  text-align: left;
  font-size: 0.875rem;
}
th,
td {
  border-bottom: 1px solid var(--color-border);
  padding: 12px;
  vertical-align: top;
}
th {
  color: var(--color-dim);
  font-weight: 500;
}
td:first-child {
  max-width: 260px;
  min-width: 160px;
}
td {
  min-width: 130px;
}
.guard-result {
  font-weight: 600;
  color: var(--color-dim);
}
.guard-result[data-result='failed'] {
  color: var(--color-red);
}
.guard-result[data-result='passed'] {
  color: var(--color-green);
}
.guard-result[data-result='insufficient'],
.guard-result[data-result='uncertain'] {
  color: var(--color-amber);
}
.guard-target {
  padding: 16px 0;
  border-top: 1px solid var(--color-border);
}
.guard-condition-list {
  padding: 0;
  list-style: none;
  display: grid;
  gap: 12px;
}
.guard-condition-list li {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px 16px;
  font-size: 0.875rem;
}
.guard-condition-list li p {
  flex-basis: 100%;
  color: var(--color-dim);
}
.guard-evidence-links {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  font: 0.75rem var(--font-mono);
}
.guard-limitations {
  color: var(--color-dim);
  font-size: 0.8rem;
  padding-left: 18px;
  line-height: 1.7;
}
.guard-timeline {
  list-style: none;
  padding: 0 0 0 16px;
  margin: 16px 0 0 6px;
  border-left: 1px solid var(--color-border-strong);
  display: grid;
  gap: 20px;
}
.guard-timeline time {
  color: var(--color-dim);
  font-size: 0.75rem;
}
.guard-timeline p {
  font-size: 0.875rem;
}
.guard-candidate {
  display: grid;
  gap: 14px;
  padding: 18px 0;
  border-bottom: 1px solid var(--color-border);
  font-size: 0.875rem;
}
.guard-candidate ul {
  margin: 8px 0 0;
  padding-left: 18px;
}
.guard-plan-facts,
.guard-evidence dl {
  display: grid;
  gap: 6px;
  margin: 12px 0;
  font-size: 0.875rem;
}
dt {
  color: var(--color-dim);
}
dd {
  margin: 0 0 8px;
  overflow-wrap: anywhere;
}
.guard-plan-targets {
  list-style: none;
  padding: 0;
  display: grid;
  gap: 12px;
}
.guard-plan-targets li {
  display: grid;
  gap: 6px;
  padding-top: 12px;
  border-top: 1px solid var(--color-border);
  font-size: 0.8rem;
}
.guard-evidence {
  padding: 18px 0;
  border-top: 1px solid var(--color-border);
  scroll-margin-top: 20px;
}
.guard-evidence header {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: center;
  font-size: 0.75rem;
  color: var(--color-dim);
  margin-bottom: 8px;
}
.guard-evidence p {
  font-size: 0.875rem;
  overflow-wrap: anywhere;
}
.guard-evidence dl {
  font-size: 0.75rem;
}
details {
  margin-top: 10px;
}
summary {
  cursor: pointer;
  font-size: 0.8rem;
  min-height: 44px;
  display: flex;
  align-items: center;
  color: var(--color-primary);
}
pre {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  font-family: var(--font-mono);
  font-size: 0.75rem;
  background: var(--color-card-2);
  padding: 14px;
  border-radius: var(--rounded);
  max-height: 340px;
  overflow: auto;
}
@media (min-width: 700px) {
  .guard-content {
    padding: 24px;
  }
  .guard-plan-facts,
  .guard-evidence dl {
    grid-template-columns: 160px minmax(0, 1fr);
  }
  dd {
    margin: 0;
  }
  .guard-candidate {
    grid-template-columns: 1fr 1fr;
  }
  .guard-candidate h3 {
    grid-column: 1 / -1;
  }
}
</style>
