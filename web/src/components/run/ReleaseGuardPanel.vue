<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import AppButton from '../ui/AppButton.vue'
import ReleaseGuardPlan from './ReleaseGuardPlan.vue'
import { HttpError } from '../../api/http'
import {
  attemptHasIdentity,
  guardIsActive,
  guardReportLocation,
  listDeploymentAttempts,
  type DeploymentAttempt,
  type GuardReport,
} from '../../api/releaseGuard'

const props = withDefaults(
  defineProps<{
    runId: string
    projectId: string
    environment?: string
    hasDeploymentTargets?: boolean
    refreshKey?: string
    runActive?: boolean
    compact?: boolean
  }>(),
  {
    hasDeploymentTargets: false,
    refreshKey: '',
    runActive: false,
    compact: false,
  },
)
const { t, locale } = useI18n()
const router = useRouter()
const attempts = ref<DeploymentAttempt[]>([])
const loading = ref(false)
const error = ref('')
const selected = ref<DeploymentAttempt | null>(null)
const parentGuardId = ref('')
let alive = true
let generation = 0
let controller: AbortController | undefined
let poll: ReturnType<typeof setTimeout> | undefined
const needsPoll = computed(
  () =>
    props.runActive ||
    attempts.value.some((attempt) =>
      attempt.guards.some((guard) => guardIsActive(guard.executionStatus)),
    ),
)
const displayedAttempts = computed(() =>
  props.environment === undefined
    ? attempts.value
    : attempts.value.filter(
        (attempt) => attempt.source.environment === props.environment,
      ),
)
function when(value: string): string {
  return new Date(value).toLocaleString(locale.value)
}
function state(value: string): string {
  const known = [
    'prepared',
    'queued',
    'binding',
    'observing',
    'stopping',
    'completed',
    'stopped',
    'interrupted',
    'blocked',
    'tool_error',
    'expired',
  ]
  return t(
    `releaseGuard.execution.${known.includes(value) ? value : 'unknown'}`,
  )
}
function result(value: string | null): string {
  return t(
    `releaseGuard.result.${['passed', 'failed', 'insufficient', 'uncertain'].includes(value ?? '') ? value : 'pending'}`,
  )
}
function schedule(): void {
  clearTimeout(poll)
  if (alive && needsPoll.value) poll = setTimeout(() => void load(true), 4000)
}
async function load(silent = false): Promise<void> {
  clearTimeout(poll)
  controller?.abort()
  const ticket = ++generation
  const request = new AbortController()
  controller = request
  if (!silent) loading.value = true
  try {
    const response = await listDeploymentAttempts(props.runId, {
      signal: request.signal,
    })
    if (!alive || ticket !== generation || request.signal.aborted) return
    attempts.value = response.items
    error.value = ''
  } catch (cause) {
    if (!alive || ticket !== generation || request.signal.aborted) return
    error.value =
      cause instanceof HttpError
        ? (cause.apiError?.message ?? t('releaseGuard.requestFailed'))
        : t('releaseGuard.requestFailed')
  } finally {
    if (alive && ticket === generation) {
      loading.value = false
      schedule()
    }
  }
}
async function started(report: GuardReport): Promise<void> {
  selected.value = null
  await router.push(guardReportLocation(report.projectId, report.id))
}
function closePlan(): void {
  selected.value = null
  void load(true)
}
watch(
  () => [props.runId, props.projectId],
  () => {
    attempts.value = []
    selected.value = null
    void load()
  },
  { immediate: true },
)
watch(
  () => props.refreshKey,
  () => void load(true),
)
watch(() => props.runActive, schedule)
onBeforeUnmount(() => {
  alive = false
  generation++
  controller?.abort()
  clearTimeout(poll)
})
</script>

<template>
  <section
    class="guard-panel"
    :class="{ 'guard-panel--compact': compact }"
    :aria-label="t('releaseGuard.title')"
  >
    <header class="guard-panel__head">
      <div>
        <h2>{{ t('releaseGuard.title') }}</h2>
        <p v-if="!compact">{{ t('releaseGuard.scope') }}</p>
      </div>
      <AppButton :loading="loading" @click="load()">{{
        t('common.refresh')
      }}</AppButton>
    </header>
    <p v-if="error" class="guard-error" role="alert">{{ error }}</p>
    <p
      v-else-if="loading && !attempts.length"
      class="guard-muted"
      role="status"
    >
      {{ t('releaseGuard.loading') }}
    </p>
    <p v-else-if="!displayedAttempts.length" class="guard-empty">
      {{
        hasDeploymentTargets
          ? t('releaseGuard.legacyIdentity')
          : t('releaseGuard.noDeployment')
      }}
    </p>
    <details v-if="displayedAttempts.length" class="guard-parent">
      <summary>{{ t('releaseGuard.linkOriginal') }}</summary>
      <label
        >{{ t('releaseGuard.originalReportId')
        }}<input
          v-model="parentGuardId"
          type="text"
          maxlength="128"
          autocomplete="off"
        /><span>{{ t('releaseGuard.linkOriginalHint') }}</span></label
      >
    </details>
    <article
      v-for="attempt in displayedAttempts"
      :key="attempt.id"
      class="guard-attempt"
      :data-attempt-id="attempt.id"
    >
      <div class="guard-attempt__head">
        <div>
          <p class="guard-attempt__label">
            {{ t('releaseGuard.attempt') }} <code>{{ attempt.id }}</code>
          </p>
          <p class="guard-muted">
            {{ when(attempt.createdAt) }} ·
            {{ attempt.source.environment || t('releaseGuard.noEnvironment') }}
            · {{ t('releaseGuard.targetCount', { n: attempt.targets.length }) }}
          </p>
        </div>
        <AppButton
          :disabled="!attemptHasIdentity(attempt)"
          :title="
            !attemptHasIdentity(attempt)
              ? t('releaseGuard.legacyIdentity')
              : t('releaseGuard.fromNowGap')
          "
          @click="selected = attempt"
          >{{ t('releaseGuard.fromNow') }}</AppButton
        >
      </div>
      <p class="guard-attempt__targets">
        {{
          attempt.targets
            .map(
              (target) =>
                `${target.serverName} / ${target.containerName || t('releaseGuard.notProvided')}`,
            )
            .join(' · ')
        }}
      </p>
      <p v-if="!attemptHasIdentity(attempt)" class="guard-notice">
        {{ t('releaseGuard.legacyIdentity') }}
      </p>
      <ul v-if="attempt.guards.length" class="guard-reports">
        <li v-for="guard in attempt.guards" :key="guard.id">
          <RouterLink :to="guardReportLocation(projectId, guard.id)"
            ><span
              class="guard-result"
              :data-result="guard.verificationResult || 'pending'"
              >{{ result(guard.verificationResult) }}</span
            ><span>{{ state(guard.executionStatus) }}</span
            ><time>{{ when(guard.createdAt) }}</time
            ><span class="guard-link-label"
              >{{ t('releaseGuard.viewReport') }} →</span
            ></RouterLink
          >
        </li>
      </ul>
      <p v-else class="guard-muted">{{ t('releaseGuard.notStarted') }}</p>
    </article>
    <ReleaseGuardPlan
      v-if="selected"
      :key="selected.id"
      :project-id="projectId"
      :attempt-id="selected.id"
      :parent-guard-id="parentGuardId.trim() || undefined"
      @close="closePlan"
      @started="started"
      @refresh="load(true)"
    />
  </section>
</template>

<style scoped>
.guard-panel {
  border: 1px solid var(--color-border);
  border-radius: var(--rounded);
  background: var(--color-card);
  padding: 18px;
  color: var(--color-text);
}
.guard-panel__head,
.guard-attempt__head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}
h2 {
  margin: 0 0 6px;
  font-size: 1rem;
}
p {
  margin: 0;
  line-height: 1.6;
}
.guard-panel__head > div > p,
.guard-muted {
  color: var(--color-dim);
  font-size: 0.8rem;
}
.guard-empty {
  color: var(--color-dim);
  padding-top: 16px;
}
.guard-attempt {
  border-top: 1px solid var(--color-border);
  margin-top: 16px;
  padding-top: 16px;
}
.guard-attempt__label {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  align-items: center;
  font-weight: 600;
  font-size: 0.85rem;
}
code {
  font-family: var(--font-mono);
  font-size: 0.75rem;
  overflow-wrap: anywhere;
}
.guard-attempt__targets {
  font-size: 0.8rem;
  margin: 10px 0;
  overflow-wrap: anywhere;
}
.guard-error {
  color: var(--color-red);
  margin-top: 12px;
}
.guard-notice {
  color: var(--color-dim);
  font-size: 0.8rem;
  margin: 12px 0;
}
.guard-reports {
  list-style: none;
  padding: 0;
  margin: 12px 0 0;
  display: grid;
  gap: 6px;
}
.guard-reports a {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
  padding: 10px;
  text-decoration: none;
  color: var(--color-text);
  background: var(--color-card-2);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded);
  font-size: 0.8rem;
  min-height: 44px;
}
.guard-reports a:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}
.guard-reports time {
  color: var(--color-dim);
}
.guard-link-label {
  color: var(--color-primary);
  margin-left: auto;
}
.guard-result {
  color: var(--color-dim);
  font-weight: 600;
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
.guard-panel :deep(.app-btn) {
  min-height: 44px;
}
.guard-panel--compact {
  padding: 12px;
  margin-top: 14px;
}
.guard-panel--compact .guard-panel__head h2 {
  font-size: 0.85rem;
}
.guard-parent {
  margin-top: 12px;
  font-size: 0.8rem;
  color: var(--color-dim);
}
.guard-parent summary {
  min-height: 44px;
  display: flex;
  align-items: center;
  cursor: pointer;
}
.guard-parent label {
  display: grid;
  gap: 8px;
}
.guard-parent input {
  min-height: 44px;
  width: 100%;
  box-sizing: border-box;
  border: 1px solid var(--color-border-strong);
  background: var(--color-card-2);
  color: var(--color-text);
  padding: 8px;
  border-radius: var(--rounded);
  font-size: 1rem;
}
@media (min-width: 700px) {
  .guard-panel {
    padding: 20px;
  }
  .guard-panel--compact {
    padding: 12px;
  }
}
</style>
