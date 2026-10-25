<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import AppButton from '../components/ui/AppButton.vue'
import GuardImageIdentity from '../components/run/GuardImageIdentity.vue'
import ReleaseGuardPlan from '../components/run/ReleaseGuardPlan.vue'
import ReleaseGuardInvestigation from '../components/run/ReleaseGuardInvestigation.vue'
import ReleaseGuardAnalysis from '../components/run/ReleaseGuardAnalysis.vue'
import GuardEvidenceCard from '../components/run/GuardEvidenceCard.vue'
import { NIcon } from 'naive-ui'
import {
  ArrowLeft,
  Refresh,
  Stars,
  Search,
  ShieldCheck,
  AlertCircle,
  Help,
} from '@vicons/tabler'
import { HttpError } from '../api/http'
import {
  guardDate,
  guardNumber,
  guardPresentationText,
  guardTimelineTitle,
} from '../utils/guardEvidencePresentation'
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
const investigationError = ref('')
const hasMore = ref(false)
const evidenceCursor = ref(0)
const showReverify = ref(false)
const tab = ref('verification')
const tabs = ['verification', 'investigation', 'ai', 'plan'] as const
const referenceId = ref('')
const referenceState = ref<
  | 'idle'
  | 'found'
  | 'pending'
  | 'loading'
  | 'missing'
  | 'expired'
  | 'error'
  | 'stalled'
>('idle')
const paginationStalled = ref(false)
let alive = true
let generation = 0
let controller: AbortController | undefined
let evidenceController: AbortController | undefined
let stopController: AbortController | undefined
let poll: ReturnType<typeof setTimeout> | undefined
let pendingReferenceFocus:
  | { ticket: number; reportId: string; id: string }
  | undefined
const active = computed(
  () => !!report.value && guardIsActive(report.value.executionStatus),
)
const failures = computed(
  () =>
    report.value?.targets.flatMap((target) =>
      target.conditions
        .filter((item) => item.result === 'failed')
        .map((item) => ({
          ...item,
          targetId: target.id,
          targetName: target.serverName,
        })),
    ) ?? [],
)
const failureGroups = computed(() => {
  const groups = new Map<
    string,
    {
      name: string
      reason: string
      targetNames: string[]
      evidenceIds: string[]
    }
  >()
  for (const failure of failures.value) {
    const key = `${failure.name}\0${failure.reason}`
    const group = groups.get(key) ?? {
      name: failure.name,
      reason: failure.reason,
      targetNames: [],
      evidenceIds: [],
    }
    const name = targetName(failure.targetId)
    if (!group.targetNames.includes(name)) group.targetNames.push(name)
    group.evidenceIds = [
      ...new Set([...group.evidenceIds, ...failure.evidenceIds]),
    ]
    groups.set(key, group)
  }
  return [...groups.values()]
})
function targetName(id: string): string {
  const observed = report.value?.targets.find((target) => target.id === id)
  const planned = report.value?.plan.targets.find((target) => target.id === id)
  return (
    [observed?.serverName || planned?.serverName, planned?.containerName]
      .filter(Boolean)
      .join(' / ') || t('releaseGuardPresentation.unknownTarget')
  )
}
const summaryTitle = computed(() => {
  if (
    report.value?.verificationResult === 'failed' &&
    failureGroups.value.length === 1
  ) {
    const description = fact(failureGroups.value[0].reason)
    if (description !== t('releaseGuardPresentation.unconfirmed'))
      return description
  }
  return result(report.value?.verificationResult ?? null)
})
function openAI(): void {
  openTab('ai')
}
function openTab(value: (typeof tabs)[number]): void {
  if (pendingReferenceFocus) {
    pendingReferenceFocus = undefined
    referenceId.value = ''
    referenceState.value = 'idle'
  }
  tab.value = value
  void nextTick(() => document.getElementById(`guard-tab-${value}`)?.focus())
}
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
  return guardDate(value, locale.value, t)
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
    : t('releaseGuardPresentation.unknownCondition')
}
function fact(value: string): string {
  return guardPresentationText(value, t)
}
function message(cause: unknown): string {
  return cause instanceof HttpError && cause.status === 404
    ? 'releaseGuardPresentation.reportMissing'
    : 'releaseGuard.requestFailed'
}
function schedule(): void {
  clearTimeout(poll)
  if (alive && active.value) poll = setTimeout(() => void load(true), 4000)
}
async function loadEvidence(ticket = generation): Promise<void> {
  if (
    loadingEvidence.value ||
    report.value?.evidenceExpired ||
    paginationStalled.value
  )
    return
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
    for (const item of page.items) {
      if (!ids.has(item.id)) {
        evidence.value.push(item)
        ids.add(item.id)
      }
    }
    const advances =
      Number.isSafeInteger(page.nextCursor) &&
      page.nextCursor > evidenceCursor.value
    paginationStalled.value = page.hasMore && !advances
    if (advances) evidenceCursor.value = page.nextCursor
    hasMore.value = page.hasMore && !paginationStalled.value
    evidenceError.value = ''
  } catch (cause) {
    if (alive && ticket === generation && !request.signal.aborted) {
      if (cause instanceof HttpError && cause.status === 410 && report.value) {
        report.value = { ...report.value, evidenceExpired: true }
        evidence.value = []
        hasMore.value = false
      } else evidenceError.value = message(cause)
    }
  } finally {
    if (alive && ticket === generation) {
      loadingEvidence.value = false
      await finishReferenceLookup(ticket)
    }
  }
}
async function finishReferenceLookup(ticket: number): Promise<void> {
  const intent = pendingReferenceFocus
  const moveFocus =
    !!intent &&
    intent.ticket === ticket &&
    ticket === generation &&
    intent.reportId === guardId.value &&
    intent.id === referenceId.value
  if (moveFocus) pendingReferenceFocus = undefined
  await resolveReference(ticket, moveFocus)
}
async function resolveReference(
  ticket = generation,
  moveFocus = true,
): Promise<void> {
  if (!referenceId.value || ticket !== generation || !alive) return
  if (report.value?.evidenceExpired) referenceState.value = 'expired'
  else if (evidence.value.some((item) => item.id === referenceId.value)) {
    referenceState.value = 'found'
    if (!moveFocus) return
    const id = referenceId.value
    await nextTick()
    if (!alive || ticket !== generation || id !== referenceId.value) return
    const element = document.getElementById(
      `guard-evidence-${encodeURIComponent(id)}`,
    )
    element?.scrollIntoView?.({ block: 'center', behavior: 'auto' })
    element?.focus({ preventScroll: true })
    return
  } else if (loadingEvidence.value) referenceState.value = 'loading'
  else if (evidenceError.value) referenceState.value = 'error'
  else if (paginationStalled.value) referenceState.value = 'stalled'
  else referenceState.value = hasMore.value ? 'pending' : 'missing'
  if (!moveFocus) return
  const id = referenceId.value
  const state = referenceState.value
  await nextTick()
  if (
    alive &&
    ticket === generation &&
    referenceId.value === id &&
    referenceState.value === state
  ) {
    const status = document.getElementById('guard-reference-status')
    status?.scrollIntoView?.({ block: 'center', behavior: 'auto' })
    status?.focus({ preventScroll: true })
  }
}
async function locateEvidence(id: string): Promise<void> {
  referenceId.value = id
  tab.value = 'verification'
  pendingReferenceFocus =
    loadingEvidence.value &&
    !evidence.value.some((item) => item.id === id) &&
    !report.value?.evidenceExpired
      ? { ticket: generation, reportId: guardId.value, id }
      : undefined
  await resolveReference()
}
async function moreEvidence(): Promise<void> {
  if (loadingEvidence.value) return
  const ticket = generation
  if (referenceId.value) {
    referenceState.value = 'loading'
    pendingReferenceFocus = {
      ticket,
      reportId: guardId.value,
      id: referenceId.value,
    }
  }
  await loadEvidence(ticket)
  if (pendingReferenceFocus) await finishReferenceLookup(ticket)
}
async function load(silent = false): Promise<void> {
  clearTimeout(poll)
  controller?.abort()
  const ticket = generation
  const request = new AbortController()
  controller = request
  if (!silent) loading.value = true
  if (!silent && paginationStalled.value) {
    paginationStalled.value = false
    evidenceCursor.value = 0
    evidence.value = []
  }
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
    await resolveReference(ticket, false)
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
  openTab(tabs[next])
}
async function started(item: GuardReport): Promise<void> {
  showReverify.value = false
  await router.push(guardReportLocation(item.projectId, item.id))
}
function updatedInvestigation(item: GuardReport): void {
  if (item.id !== guardId.value || item.projectId !== projectId.value) return
  if (report.value?.evidenceExpired && !item.evidenceExpired) return
  if (
    !Number.isSafeInteger(item.eventsCursor) ||
    item.eventsCursor < (report.value?.eventsCursor ?? 0)
  ) {
    investigationError.value = 'releaseGuardInvestigation.changed'
    return
  }
  investigationError.value = ''
  controller?.abort()
  loading.value = false
  report.value = item
  schedule()
}
watch(
  [projectId, guardId],
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
    tab.value = 'verification'
    referenceId.value = ''
    referenceState.value = 'idle'
    pendingReferenceFocus = undefined
    paginationStalled.value = false
    error.value = ''
    evidenceError.value = ''
    stopError.value = ''
    investigationError.value = ''
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
        <AppButton
          v-if="report"
          variant="ghost"
          class="guard-back"
          @click="
            router.push({ name: 'run-detail', params: { id: report.runId } })
          "
          ><NIcon :size="16" aria-hidden="true"><ArrowLeft /></NIcon
          >{{ t('releaseGuard.sourceRun') }}</AppButton
        >
        <h1>{{ t('releaseGuard.reportTitle') }}</h1>
      </div>
      <div class="guard-header-actions">
        <AppButton v-if="report" variant="ai" @click="openAI"
          ><NIcon :size="16" aria-hidden="true"><Stars /></NIcon
          >{{ t('releaseGuardPresentation.aiTitle') }}</AppButton
        >
        <AppButton :loading="loading" :disabled="stopping" @click="load()"
          ><NIcon :size="16" aria-hidden="true"><Refresh /></NIcon
          >{{ t('common.refresh') }}</AppButton
        >
      </div>
    </header>
    <p v-if="error" class="guard-error" role="alert">{{ t(error) }}</p>
    <p v-if="loading && !report" role="status">
      {{ t('releaseGuard.loading') }}
    </p>
    <template v-if="report">
      <section
        class="guard-verdict"
        :data-result="report.verificationResult || 'pending'"
      >
        <div class="guard-summary-layout">
          <div class="guard-summary-primary">
            <p class="guard-eyebrow">
              {{ t('releaseGuard.basicVerification') }} ·
              <strong
                class="guard-result"
                :data-result="report.verificationResult"
                >{{ result(report.verificationResult) }}</strong
              >
            </p>
            <h2>
              <NIcon :size="24" aria-hidden="true"
                ><AlertCircle
                  v-if="report.verificationResult === 'failed'" /><ShieldCheck
                  v-else-if="report.verificationResult === 'passed'" /><Help
                  v-else /></NIcon
              >{{ summaryTitle }}
            </h2>
            <div
              v-if="report.verificationResult === 'failed'"
              class="guard-failure-summary"
            >
              <h3>{{ t('releaseGuardPresentation.why') }}</h3>
              <ul
                v-if="failureGroups.length"
                :tabindex="failureGroups.length > 2 ? 0 : undefined"
                :aria-label="t('releaseGuardPresentation.why')"
              >
                <li
                  v-for="item in failureGroups"
                  :key="`${item.name}-${item.reason}`"
                >
                  <strong
                    >{{ item.targetNames.join(' · ') }} ·
                    {{ condition(item.name) }}</strong
                  >
                  <p v-if="fact(item.reason) !== summaryTitle">
                    {{ fact(item.reason) }}
                  </p>
                  <button
                    v-for="(id, index) in item.evidenceIds"
                    :key="id"
                    type="button"
                    class="guard-citation"
                    @click="locateEvidence(id)"
                  >
                    {{
                      t('releaseGuardPresentation.evidenceReference', {
                        n: guardNumber(index + 1, locale),
                      })
                    }}
                  </button>
                </li>
              </ul>
              <p v-else>{{ t('releaseGuardPresentation.failedFallback') }}</p>
            </div>
            <p v-else class="guard-summary-description">
              {{ t('releaseGuard.sampledOnly') }}
            </p>
          </div>
          <div class="guard-summary-secondary">
            <div class="guard-summary-metrics">
              <div v-for="target in report.targets" :key="target.id">
                <strong>{{ targetName(target.id) }}</strong
                ><span
                  >{{ t('releaseGuard.coverage') }}:
                  {{ guardNumber(target.coverage.successfulSamples, locale) }} /
                  {{
                    guardNumber(target.coverage.expectedSamples, locale)
                  }}</span
                ><span>{{
                  target.coverage.complete
                    ? t('releaseGuard.coverageComplete')
                    : t('releaseGuard.coverageGap')
                }}</span>
              </div>
            </div>
            <div class="guard-summary-unknown">
              <strong>{{ t('releaseGuardPresentation.unknownTitle') }}</strong>
              <p>{{ t('releaseGuardPresentation.unknownScope') }}</p>
            </div>
          </div>
        </div>
        <div class="guard-facts">
          <span
            >{{ t('releaseGuard.executionTitle') }}:
            <strong>{{ execution(report.executionStatus) }}</strong></span
          ><span
            >{{ t('releaseGuard.attributionTitle') }}:
            {{ attribution(report.attributionStatus) }}</span
          >
          <p
            v-if="
              report.verificationResult !== 'failed' &&
              report.reason &&
              [
                'blocked',
                'tool_error',
                'interrupted',
                'stopped',
                'expired',
              ].includes(report.executionStatus)
            "
            class="guard-ending-reason guard-muted"
          >
            {{ fact(report.reason) }}
          </p>
        </div>
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
        <details class="guard-boundaries">
          <summary>{{ t('releaseGuardPresentation.boundaryDetails') }}</summary>
          <div>
            <p v-if="report.plan.mode === 'from_now'">
              {{ t('releaseGuard.fromNowGap') }}
            </p>
            <p v-if="report.identityTrust === 'host_identity_unverified'">
              {{ t('releaseGuard.hostTrust') }}
            </p>
            <p v-if="!report.evidenceExpired" class="guard-muted">
              {{
                t('releaseGuard.evidenceRetention', {
                  at: when(report.evidenceExpiresAt),
                })
              }}
            </p>
          </div>
        </details>
        <p v-if="report.evidenceExpired" class="guard-notice">
          {{ t('releaseGuard.evidenceExpired') }}
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
      <p v-if="stopError" class="guard-error" role="alert">
        {{ t(stopError) }}
      </p>
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
          @click="openTab(item)"
          @keydown="selectTab($event, index)"
        >
          {{
            item === 'ai'
              ? t('releaseGuardPresentation.aiTitle')
              : t(`releaseGuard.tabs.${item}`)
          }}
        </button>
      </div>
      <section
        v-show="tab === 'verification'"
        id="guard-tabpanel-verification"
        class="guard-content"
        role="tabpanel"
        aria-labelledby="guard-tab-verification"
      >
        <div class="guard-verification-layout">
          <div class="guard-checks">
            <h2>{{ t('releaseGuard.targetChecks') }}</h2>
            <p class="guard-muted">{{ t('releaseGuard.sampledOnly') }}</p>
            <div class="guard-table-wrap">
              <table>
                <thead>
                  <tr>
                    <th scope="col">{{ t('releaseGuard.target') }}</th>
                    <th scope="col">{{ t('releaseGuard.coverage') }}</th>
                    <th scope="col">
                      {{ t('releaseGuard.observationWindow') }}
                    </th>
                    <th scope="col">{{ t('releaseGuard.conclusion') }}</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="target in report.targets" :key="target.id">
                    <td :data-label="t('releaseGuard.target')">
                      <strong>{{ target.serverName }}</strong>
                    </td>
                    <td :data-label="t('releaseGuard.coverage')">
                      {{
                        guardNumber(target.coverage.successfulSamples, locale)
                      }}
                      / {{ guardNumber(target.coverage.expectedSamples, locale)
                      }}<br /><span class="guard-muted">{{
                        t('releaseGuard.maximumGap', {
                          seconds: guardNumber(
                            target.coverage.maxGapSeconds,
                            locale,
                          ),
                        })
                      }}</span
                      ><br />{{
                        target.coverage.complete
                          ? t('releaseGuard.coverageComplete')
                          : t('releaseGuard.coverageGap')
                      }}
                    </td>
                    <td :data-label="t('releaseGuard.observationWindow')">
                      {{ when(target.readyAt) }} → {{ when(target.deadline) }}
                    </td>
                    <td
                      :data-label="t('releaseGuard.conclusion')"
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
              <GuardImageIdentity
                v-if="target.expectedImage"
                :image="target.expectedImage"
                compact
              />
              <ul class="guard-condition-list">
                <li v-for="item in target.conditions" :key="item.name">
                  <strong>{{ condition(item.name) }}</strong
                  ><span class="guard-result" :data-result="item.result">{{
                    result(item.result)
                  }}</span>
                  <p v-if="item.reason">{{ fact(item.reason) }}</p>
                  <span class="guard-evidence-links"
                    ><a
                      v-for="(id, index) in item.evidenceIds"
                      :key="id"
                      href="#guard-evidence-section"
                      @click.prevent="locateEvidence(id)"
                      >{{
                        t('releaseGuardPresentation.evidenceReference', {
                          n: guardNumber(index + 1, locale),
                        })
                      }}</a
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
          </div>
          <aside class="guard-next">
            <h2>{{ t('releaseGuardPresentation.nextTitle') }}</h2>
            <p>{{ t('releaseGuardPresentation.nextDescription') }}</p>
            <AppButton @click="openTab('investigation')"
              ><NIcon :size="16" aria-hidden="true"><Search /></NIcon
              >{{ t('releaseGuardPresentation.investigateAction') }}</AppButton
            >
            <AppButton variant="ai" @click="openAI"
              ><NIcon :size="16" aria-hidden="true"><Stars /></NIcon
              >{{ t('releaseGuardPresentation.aiTitle') }}</AppButton
            >
            <p>{{ t('releaseGuardPresentation.snapshotScope') }}</p>
            <AppButton v-if="!active" @click="showReverify = true">{{
              t('releaseGuard.reverify')
            }}</AppButton>
          </aside>
        </div>
        <h2 class="guard-timeline-heading">{{ t('releaseGuard.timeline') }}</h2>
        <ol class="guard-timeline">
          <li v-for="event in report.timeline" :key="event.id">
            <time>{{ when(event.at) }}</time>
            <span v-if="event.targetId" class="guard-timeline-target">
              · {{ targetName(event.targetId) }}</span
            >
            <p>
              {{ guardTimelineTitle(event.kind, t)
              }}<template
                v-if="
                  event.summary !== event.kind &&
                  fact(event.summary) !== guardTimelineTitle(event.kind, t)
                "
              >
                · {{ fact(event.summary) }}</template
              >
            </p>
            <span class="guard-evidence-links"
              ><a
                v-for="(id, index) in event.evidenceIds"
                :key="id"
                href="#guard-evidence-section"
                @click.prevent="locateEvidence(id)"
                >{{
                  t('releaseGuardPresentation.evidenceReference', {
                    n: guardNumber(index + 1, locale),
                  })
                }}</a
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
          @locate-evidence="locateEvidence"
        />
        <p v-if="investigationError" class="guard-error" role="alert">
          {{ t(investigationError) }}
        </p>
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
                v-for="(id, index) in candidate.supportingEvidenceIds"
                :key="id"
                href="#guard-evidence-section"
                :aria-label="
                  t('releaseGuardPresentation.citationLabel', {
                    candidate: fact(candidate.title),
                    context: t('releaseGuard.supportingEvidence'),
                    n: guardNumber(index + 1, locale),
                  })
                "
                @click.prevent="locateEvidence(id)"
                >{{
                  t('releaseGuardPresentation.evidenceReference', {
                    n: guardNumber(index + 1, locale),
                  })
                }}</a
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
                v-for="(id, index) in candidate.counterEvidenceIds"
                :key="id"
                href="#guard-evidence-section"
                :aria-label="
                  t('releaseGuardPresentation.citationLabel', {
                    candidate: fact(candidate.title),
                    context: t('releaseGuard.counterEvidence'),
                    n: guardNumber(index + 1, locale),
                  })
                "
                @click.prevent="locateEvidence(id)"
                >{{
                  t('releaseGuardPresentation.evidenceReference', {
                    n: guardNumber(index + 1, locale),
                  })
                }}</a
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
        v-show="tab === 'ai'"
        id="guard-tabpanel-ai"
        class="guard-content guard-ai-panel"
        role="tabpanel"
        aria-labelledby="guard-tab-ai"
      >
        <ReleaseGuardAnalysis
          :report="report"
          @locate-evidence="locateEvidence"
        />
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
              t('releaseGuard.minutes', {
                n: guardNumber(report.plan.durationSeconds / 60, locale),
              })
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
      <section
        id="guard-evidence-section"
        class="guard-content guard-evidence-section"
      >
        <h2>{{ t('releaseGuard.evidenceTitle') }}</h2>
        <p class="guard-muted">
          {{
            t('releaseGuardPresentation.evidenceLoaded', {
              n: guardNumber(evidence.length, locale),
            })
          }}
        </p>
        <div
          v-if="referenceState !== 'idle'"
          id="guard-reference-status"
          class="guard-reference-status"
          role="status"
          tabindex="-1"
          aria-live="polite"
        >
          <p>{{ t(`releaseGuardPresentation.find.${referenceState}`) }}</p>
          <AppButton
            v-if="['pending', 'error'].includes(referenceState)"
            :loading="loadingEvidence"
            @click="moreEvidence"
            >{{ t('releaseGuardPresentation.continueFinding') }}</AppButton
          >
        </div>
        <p v-if="paginationStalled" class="guard-notice" role="status">
          {{ t('releaseGuardPresentation.find.stalled') }}
        </p>
        <p v-if="evidenceError" class="guard-error" role="alert">
          {{ t(evidenceError) }}
        </p>
        <p
          v-if="!evidence.length && !report.evidenceExpired && !loadingEvidence"
          class="guard-muted"
        >
          {{ t('releaseGuard.noEvidence') }}
        </p>
        <GuardEvidenceCard
          v-for="item in evidence"
          :key="item.id"
          :evidence="item"
          :target-name="targetName(item.targetId)"
          :highlighted="referenceId === item.id && referenceState === 'found'"
        />
        <AppButton
          v-if="
            (hasMore || evidenceError) &&
            !paginationStalled &&
            !report.evidenceExpired
          "
          :loading="loadingEvidence"
          @click="moreEvidence"
          >{{
            hasMore
              ? t('releaseGuard.moreEvidence')
              : t('releaseGuard.retryEvidence')
          }}</AppButton
        >
      </section>
      <details class="guard-report-technical">
        <summary>{{ t('releaseGuardPresentation.reportDetails') }}</summary>
        <p v-if="report.reason">{{ fact(report.reason) }}</p>
        <pre>{{ JSON.stringify(report, null, 2) }}</pre>
      </details>
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
  min-width: 0;
  overflow-wrap: anywhere;
}
.guard-header-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
.guard-verdict h2 {
  display: flex;
  align-items: center;
  gap: 10px;
}
.guard-failure-summary {
  margin: 18px 0;
}
.guard-failure-summary h3 {
  font-size: 0.8rem;
  color: var(--color-dim);
}
.guard-failure-summary ul {
  margin: 0;
  padding: 0;
  list-style: none;
  display: grid;
  gap: 14px;
}
.guard-failure-summary li {
  font-size: 0.9rem;
}
.guard-failure-summary p {
  color: var(--color-dim);
  margin: 4px 0;
}
.guard-summary-description,
.guard-summary-unknown {
  color: var(--color-dim);
  font-size: 0.85rem;
}
.guard-summary-unknown {
  margin: 18px 0;
  padding: 14px 0;
  border-top: 1px solid var(--color-border);
  border-bottom: 1px solid var(--color-border);
}
.guard-summary-unknown strong {
  font-weight: 500;
}
.guard-summary-unknown p {
  margin-top: 5px;
}
.guard-summary-metrics {
  display: grid;
  gap: 16px;
  margin-top: 18px;
  padding-top: 18px;
  border-top: 1px solid var(--color-border);
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 230px), 1fr));
}
.guard-summary-metrics > div {
  display: grid;
  gap: 4px;
  font-size: 0.8rem;
}
.guard-summary-metrics span {
  color: var(--color-dim);
}
.guard-verification-layout {
  display: grid;
  gap: 24px;
}
.guard-checks,
.guard-next {
  min-width: 0;
}
.guard-next {
  display: grid;
  gap: 14px;
  align-content: start;
  background: var(--color-card-2);
  padding: 18px;
  border-radius: var(--rounded);
  border: 1px solid var(--color-border);
}
.guard-next h2 {
  margin: 0;
}
.guard-next p {
  font-size: 0.8rem;
  color: var(--color-dim);
}
.guard-next :deep(.app-btn) {
  white-space: normal;
  height: auto;
  padding-block: 8px;
  text-align: start;
}
.guard-citation {
  font: inherit;
  font-size: 0.8rem;
  min-height: 44px;
  color: var(--color-primary);
  background: transparent;
  border: 0;
  padding: 4px 10px 4px 0;
  cursor: pointer;
  text-align: start;
}
.guard-timeline-heading {
  margin-top: 26px;
  padding-top: 22px;
  border-top: 1px solid var(--color-border);
}
.guard-reference-status {
  display: grid;
  justify-items: start;
  gap: 10px;
  padding: 14px;
  margin: 12px 0;
  background: var(--color-primary-soft);
  border-radius: var(--rounded);
  font-size: 0.85rem;
}
.guard-tabs button {
  white-space: normal;
  overflow-wrap: anywhere;
}
.guard-tabs #guard-tab-ai {
  color: var(--color-cyan);
}
.guard-tabs #guard-tab-ai[aria-selected='true'] {
  border-bottom-color: var(--color-cyan);
}
.guard-ai-panel {
  border-top: 2px solid var(--color-cyan);
}
.guard-report-technical {
  min-width: 0;
}
.guard-boundaries {
  margin: 0;
  font-size: 0.8rem;
  color: var(--color-dim);
}
.guard-boundaries > div {
  display: grid;
  gap: 10px;
  padding: 8px 0;
}
.guard-evidence-links a {
  display: inline-flex;
  align-items: center;
  min-height: 36px;
}
@media (min-width: 1024px) {
  .guard-verification-layout {
    grid-template-columns: minmax(0, 1.85fr) minmax(230px, 1fr);
  }
  .guard-report :deep(.app-btn) {
    min-height: 36px;
  }
}
@media (max-width: 600px) {
  .guard-verdict {
    padding: 18px;
  }
  .guard-content {
    padding: 16px;
  }
  .guard-table-wrap {
    overflow: visible;
  }
  .guard-table-wrap table,
  .guard-table-wrap tbody,
  .guard-table-wrap tr,
  .guard-table-wrap td {
    display: block;
    min-width: 0;
    max-width: none;
  }
  .guard-table-wrap thead {
    display: none;
  }
  .guard-table-wrap tr {
    padding: 10px 0;
    border-bottom: 1px solid var(--color-border);
  }
  .guard-table-wrap td {
    padding: 6px 0;
    border: 0;
  }
  .guard-table-wrap td::before {
    content: attr(data-label);
    display: block;
    color: var(--color-dim);
    font-size: 0.75rem;
    font-weight: 400;
    margin-bottom: 4px;
  }
  .guard-tabs button {
    flex: 1 1 45%;
    padding-inline: 8px;
  }
  .guard-report__header > div,
  .guard-header-actions {
    min-width: 0;
  }
  .guard-report :deep(.app-btn) {
    white-space: normal;
    height: auto;
    padding-block: 8px;
  }
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
.guard-ending-reason {
  flex-basis: 100%;
}
.guard-timeline-target {
  font-size: 0.75rem;
  color: var(--color-dim);
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
@media (min-width: 1024px) {
  .guard-report :deep(.app-btn) {
    min-height: 36px;
  }
}
.guard-report {
  gap: 16px;
}
.guard-verdict {
  padding: 20px;
}
.guard-verdict h2 {
  font-size: 1.35rem;
  line-height: 1.4;
  margin-bottom: 10px;
  align-items: flex-start;
}
.guard-summary-layout {
  display: grid;
  gap: 16px;
}
.guard-summary-primary,
.guard-summary-secondary {
  min-width: 0;
}
.guard-failure-summary {
  margin: 10px 0 0;
}
.guard-failure-summary h3 {
  margin-bottom: 4px;
  font-size: 0.75rem;
}
.guard-failure-summary ul {
  gap: 10px;
}
.guard-failure-summary li {
  font-size: 0.8rem;
}
.guard-summary-metrics {
  margin: 0;
  padding: 0;
  border: 0;
  gap: 12px;
  grid-template-columns: 1fr;
}
.guard-summary-metrics > div {
  display: flex;
  flex-wrap: wrap;
  gap: 3px 10px;
}
.guard-summary-metrics strong {
  flex-basis: 100%;
}
.guard-summary-unknown {
  margin: 10px 0 0;
  padding: 10px 0 0;
  border-bottom: 0;
  font-size: 0.8rem;
}
.guard-summary-unknown p {
  margin-top: 3px;
  line-height: 1.6;
}
.guard-facts {
  margin-top: 14px;
  padding-top: 12px;
  border-top: 1px solid var(--color-border);
  gap: 8px 20px;
  font-size: 0.8rem;
}
.guard-header-actions {
  flex-shrink: 0;
  max-width: 100%;
}
.guard-header-actions :deep(.app-btn) {
  word-break: keep-all;
}
@media (min-width: 900px) {
  .guard-summary-layout {
    grid-template-columns: minmax(0, 1.2fr) minmax(0, 1fr);
    gap: 22px;
  }
  .guard-summary-secondary {
    border-left: 1px solid var(--color-border);
    padding-left: 22px;
  }
  .guard-failure-summary ul {
    max-height: 160px;
    overflow: auto;
    overscroll-behavior: contain;
  }
  .guard-citation {
    min-height: 36px;
  }
}
@media (max-width: 900px) {
  .guard-verdict,
  .guard-content {
    padding: 16px;
  }
  .guard-verdict h2 {
    font-size: 1.2rem;
  }
}
</style>
