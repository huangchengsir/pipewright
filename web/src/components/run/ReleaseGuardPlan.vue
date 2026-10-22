<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppButton from '../ui/AppButton.vue'
import { HttpError } from '../../api/http'
import { releaseGuardText } from '../../utils/releaseGuardText'
import {
  previewGuardPlan,
  startReleaseGuard,
  type AttachedGuardInput,
  type GuardDuration,
  type GuardPlan,
  type GuardReport,
} from '../../api/releaseGuard'

const props = withDefaults(
  defineProps<{
    projectId: string
    attemptId?: string
    attachedInput?: AttachedGuardInput
    parentGuardId?: string
    initialDurationSeconds?: GuardDuration
    initialIncludeLogs?: boolean
  }>(),
  { initialDurationSeconds: 600, initialIncludeLogs: false },
)
const emit = defineEmits<{
  close: []
  started: [report: GuardReport]
  prepared: [plan: GuardPlan]
  refresh: []
}>()
const { t, locale } = useI18n()
const duration = ref<GuardDuration>(props.initialDurationSeconds)
const includeLogs = ref(props.initialIncludeLogs)
const plan = ref<GuardPlan | null>(null)
const loading = ref(false)
const starting = ref(false)
const error = ref('')
const outcomeUnknown = ref(false)
const now = ref(Date.now())
const dialog = ref<HTMLElement>()
let alive = true
let generation = 0
let controller: AbortController | undefined
let idempotencyKey = ''
let returnFocus: HTMLElement | null = null
const clock = setInterval(() => {
  now.value = Date.now()
}, 1000)
const expired = computed(
  () =>
    !plan.value ||
    !Number.isFinite(Date.parse(plan.value.expiresAt)) ||
    Date.parse(plan.value.expiresAt) <= now.value,
)
const mode = computed(() => (props.attachedInput ? 'attached' : 'from_now'))
const canStart = computed(
  () =>
    !!plan.value &&
    plan.value.projectId === props.projectId &&
    (props.attachedInput
      ? plan.value.runId === props.attachedInput.runId &&
        plan.value.artifactId === props.attachedInput.artifactId &&
        JSON.stringify(
          plan.value.targets.map((target) => target.serverId).sort(),
        ) === JSON.stringify([...props.attachedInput.serverIds].sort())
      : plan.value.attemptId === props.attemptId) &&
    plan.value.mode === mode.value &&
    plan.value.durationSeconds === duration.value &&
    plan.value.includeLogs === includeLogs.value &&
    !!plan.value.hash &&
    plan.value.targets.some(
      (target) =>
        target.availability === 'available' &&
        (props.attachedInput || /^[a-f0-9]{64}$/i.test(target.containerId)),
    ) &&
    !expired.value &&
    !loading.value &&
    !starting.value &&
    !outcomeUnknown.value,
)
const incomplete = computed(
  () =>
    plan.value?.targets.some((target) => target.availability !== 'available') ??
    false,
)

async function preview(): Promise<void> {
  if (starting.value || outcomeUnknown.value) return
  const ticket = ++generation
  controller?.abort()
  const request = new AbortController()
  controller = request
  loading.value = true
  plan.value = null
  error.value = ''
  idempotencyKey = ''
  try {
    const result = await previewGuardPlan(
      props.projectId,
      {
        ...(props.attachedInput
          ? { mode: 'attached' as const, ...props.attachedInput }
          : { mode: 'from_now' as const, attemptId: props.attemptId ?? '' }),
        durationSeconds: duration.value,
        includeLogs: includeLogs.value,
        ...(props.parentGuardId ? { parentGuardId: props.parentGuardId } : {}),
      },
      { signal: request.signal },
    )
    if (!alive || ticket !== generation || request.signal.aborted) return
    plan.value = result
    now.value = Date.now()
  } catch (cause) {
    if (alive && ticket === generation && !request.signal.aborted)
      error.value =
        cause instanceof HttpError
          ? (cause.apiError?.message ?? t('releaseGuard.requestFailed'))
          : t('releaseGuard.requestFailed')
  } finally {
    if (alive && ticket === generation) loading.value = false
  }
}
async function start(): Promise<void> {
  if (!canStart.value || !plan.value) return
  if (props.attachedInput) {
    emit('prepared', plan.value)
    return
  }
  starting.value = true
  error.value = ''
  if (!idempotencyKey) idempotencyKey = crypto.randomUUID()
  const request = new AbortController()
  controller = request
  try {
    const result = await startReleaseGuard(
      props.projectId,
      { planId: plan.value.id, planHash: plan.value.hash, idempotencyKey },
      { signal: request.signal },
    )
    if (alive) emit('started', result)
  } catch (cause) {
    if (!alive || request.signal.aborted) return
    outcomeUnknown.value = cause instanceof HttpError && cause.status === 0
    error.value = outcomeUnknown.value
      ? t('releaseGuard.startUnknown')
      : cause instanceof HttpError
        ? (cause.apiError?.message ?? t('releaseGuard.requestFailed'))
        : t('releaseGuard.requestFailed')
    emit('refresh')
  } finally {
    if (alive) starting.value = false
  }
}
function close(): void {
  if (!starting.value) emit('close')
}
function keydown(event: KeyboardEvent): void {
  if (event.key === 'Escape') {
    event.preventDefault()
    close()
  }
  if (event.key !== 'Tab') return
  const controls = [
    ...(dialog.value?.querySelectorAll<HTMLElement>(
      'button:not(:disabled), select:not(:disabled), a[href], [tabindex="0"]',
    ) ?? []),
  ]
  const first = controls[0]
  const last = controls.at(-1)
  if (!first) {
    event.preventDefault()
    dialog.value?.focus()
    return
  }
  if (event.shiftKey && document.activeElement === first) {
    event.preventDefault()
    last?.focus()
  }
  if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault()
    first?.focus()
  }
}
function when(value: string): string {
  return new Date(value).toLocaleString(locale.value)
}
function availability(value: string): string {
  return t(
    `releaseGuard.availability.${['available', 'identity_unavailable', 'unsupported'].includes(value) ? value : 'unsupported'}`,
  )
}
function fact(value: string): string {
  return releaseGuardText(value, (key) => t(key))
}
watch([duration, includeLogs], () => void preview())
onMounted(async () => {
  returnFocus =
    document.activeElement instanceof HTMLElement
      ? document.activeElement
      : null
  await nextTick()
  dialog.value?.querySelector<HTMLElement>('button')?.focus()
  void preview()
})
onBeforeUnmount(() => {
  alive = false
  generation++
  controller?.abort()
  clearInterval(clock)
  returnFocus?.focus()
})
</script>

<template>
  <div class="guard-overlay" @keydown="keydown">
    <section
      ref="dialog"
      class="guard-plan"
      role="dialog"
      tabindex="-1"
      aria-modal="true"
      aria-labelledby="guard-plan-title"
      :aria-busy="loading || starting"
    >
      <header class="guard-plan__header">
        <div>
          <p class="guard-eyebrow">
            {{
              t(`releaseGuard.${mode === 'attached' ? 'attached' : 'fromNow'}`)
            }}
          </p>
          <h2 id="guard-plan-title">{{ t('releaseGuard.planTitle') }}</h2>
        </div>
        <AppButton :disabled="starting" @click="close">{{
          t('releaseGuard.close')
        }}</AppButton>
      </header>
      <p class="guard-notice">
        {{
          t(
            `releaseGuard.${mode === 'attached' ? 'attachedDescription' : 'fromNowGap'}`,
          )
        }}
      </p>
      <p v-if="parentGuardId" class="guard-muted">
        {{ t('releaseGuard.reverifyBinding') }}
      </p>
      <div class="guard-plan__identity">
        <span>{{
          t(`releaseGuard.${mode === 'attached' ? 'sourceRun' : 'attempt'}`)
        }}</span
        ><code>{{ attachedInput?.runId || attemptId }}</code>
      </div>
      <label class="guard-duration"
        >{{ t('releaseGuard.duration') }}
        <select
          v-model.number="duration"
          :disabled="starting || outcomeUnknown"
        >
          <option :value="300">
            {{ t('releaseGuard.minutes', { n: 5 }) }}
          </option>
          <option :value="600">
            {{ t('releaseGuard.minutes', { n: 10 }) }}
          </option>
          <option :value="900">
            {{ t('releaseGuard.minutes', { n: 15 }) }}
          </option>
        </select>
      </label>
      <label class="guard-log-choice"
        ><input
          v-model="includeLogs"
          type="checkbox"
          :disabled="starting || outcomeUnknown"
        />{{ t('releaseGuard.collectLogs') }}</label
      >
      <p class="guard-muted">{{ t('releaseGuard.collectLogsHint') }}</p>
      <p v-if="loading" role="status">{{ t('releaseGuard.previewing') }}</p>
      <p v-if="error" class="guard-error" role="alert">{{ error }}</p>
      <template v-if="plan">
        <div class="guard-table-wrap">
          <table class="guard-table">
            <thead>
              <tr>
                <th>{{ t('releaseGuard.target') }}</th>
                <th>{{ t('releaseGuard.expectedVersion') }}</th>
                <th>{{ t('releaseGuard.binding') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="target in plan.targets" :key="target.id">
                <td>
                  <strong>{{ target.serverName }}</strong
                  ><br />{{
                    target.containerName || t('releaseGuard.notProvided')
                  }}
                </td>
                <td>
                  <code>{{
                    target.expectedImage.repository &&
                    target.expectedImage.digest
                      ? `${target.expectedImage.repository}@${target.expectedImage.digest}`
                      : target.expectedImage.reference ||
                        t('releaseGuard.notProvided')
                  }}</code>
                </td>
                <td>
                  {{ availability(target.availability) }}
                  <ul v-if="target.limitations.length">
                    <li v-for="item in target.limitations" :key="item">
                      {{ fact(item) }}
                    </li>
                  </ul>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <p v-if="incomplete" class="guard-notice">
          {{ t('releaseGuard.incompletePlan') }}
        </p>
        <section class="guard-checks">
          <h3>{{ t('releaseGuard.checksTitle') }}</h3>
          <p>{{ t('releaseGuard.checksDescription') }}</p>
          <p>{{ t('releaseGuard.samplingDescription') }}</p>
          <p>{{ t('releaseGuard.localOnly') }}</p>
        </section>
        <ul v-if="plan.limitations.length" class="guard-limitations">
          <li v-for="item in plan.limitations" :key="item">{{ fact(item) }}</li>
        </ul>
        <p class="guard-notice">{{ t('releaseGuard.hostTrust') }}</p>
        <p class="guard-muted">
          {{ t('releaseGuard.planExpiry', { at: when(plan.expiresAt) }) }}
        </p>
        <p v-if="expired" class="guard-error" role="status">
          {{ t('releaseGuard.expiredPlan') }}
        </p>
      </template>
      <footer class="guard-plan__actions">
        <AppButton
          v-if="!outcomeUnknown"
          :disabled="loading || starting"
          @click="preview"
          >{{ t('releaseGuard.previewAgain') }}</AppButton
        ><AppButton
          variant="primary"
          :disabled="!canStart"
          :loading="starting"
          @click="start"
          >{{
            t(
              `releaseGuard.${mode === 'attached' ? 'usePlan' : 'startObservation'}`,
            )
          }}</AppButton
        >
      </footer>
    </section>
  </div>
</template>

<style scoped>
.guard-overlay {
  position: fixed;
  inset: 0;
  z-index: 200;
  background: rgb(0 0 0 / 0.45);
  display: flex;
  justify-content: flex-end;
}
.guard-plan {
  width: 100%;
  height: 100%;
  overflow: auto;
  padding: 20px;
  background: var(--color-card);
  color: var(--color-text);
  display: flex;
  flex-direction: column;
  gap: 18px;
  box-shadow: -8px 0 36px rgb(0 0 0 / 0.15);
}
.guard-plan__header,
.guard-plan__actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}
h2,
h3,
p {
  margin: 0;
}
h2 {
  font-size: 1.4rem;
}
h3 {
  font-size: 1rem;
  margin-bottom: 8px;
}
.guard-eyebrow,
.guard-muted {
  color: var(--color-dim);
  font-size: 0.875rem;
}
.guard-eyebrow {
  margin-bottom: 6px;
}
.guard-notice {
  padding: 12px;
  border-left: 3px solid var(--color-amber);
  background: var(--color-amber-soft);
  line-height: 1.6;
}
.guard-error {
  color: var(--color-red);
  line-height: 1.6;
}
.guard-plan__identity {
  display: grid;
  gap: 6px;
}
code {
  font-family: var(--font-mono);
  overflow-wrap: anywhere;
  font-size: 0.8rem;
}
.guard-duration {
  display: flex;
  align-items: center;
  gap: 12px;
}
.guard-log-choice {
  display: flex;
  align-items: center;
  gap: 10px;
  min-height: 44px;
  font-size: 0.875rem;
}
.guard-log-choice input {
  width: 18px;
  height: 18px;
  accent-color: var(--color-primary);
}
select {
  color: var(--color-text);
  background: var(--color-card-2);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded);
  padding: 8px 12px;
  min-height: 44px;
  font: inherit;
}
.guard-table-wrap {
  overflow-x: auto;
}
.guard-table {
  width: 100%;
  border-collapse: collapse;
  text-align: left;
  font-size: 0.875rem;
}
th,
td {
  border-bottom: 1px solid var(--color-border);
  padding: 12px 8px;
  vertical-align: top;
}
th {
  color: var(--color-dim);
  font-weight: 500;
}
td {
  min-width: 120px;
}
td:nth-child(2) {
  max-width: 220px;
}
ul {
  padding-left: 18px;
  margin: 8px 0 0;
  line-height: 1.6;
}
.guard-checks {
  padding: 16px;
  border: 1px solid var(--color-border);
  border-radius: var(--rounded);
  line-height: 1.7;
}
.guard-checks p + p {
  margin-top: 8px;
}
.guard-plan__actions {
  margin-top: auto;
  padding-top: 16px;
  flex-wrap: wrap;
}
.guard-plan :deep(.app-btn) {
  min-height: 44px;
}
@media (min-width: 700px) {
  .guard-plan {
    width: min(740px, 90vw);
    padding: 28px;
  }
}
</style>
