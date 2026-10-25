<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppButton from '../ui/AppButton.vue'
import { HttpError } from '../../api/http'
import {
  investigateReleaseGuard,
  type GuardReport,
} from '../../api/releaseGuard'
import {
  guardDate,
  guardNumber,
  guardPresentationText,
} from '../../utils/guardEvidencePresentation'

const props = defineProps<{ report: GuardReport }>()
const emit = defineEmits<{
  updated: [report: GuardReport]
  locateEvidence: [id: string]
}>()
const { t, locale } = useI18n()
const busy = ref(false)
const error = ref('')
let generation = 0
let alive = true
let controller: AbortController | undefined
function fact(value: string): string {
  return guardPresentationText(value, t)
}
function when(value?: string): string {
  return guardDate(value, locale.value, t)
}
async function update(): Promise<void> {
  if (busy.value || props.report.evidenceExpired) return
  const ticket = generation
  const projectId = props.report.projectId
  const id = props.report.id
  const request = new AbortController()
  controller = request
  busy.value = true
  error.value = ''
  try {
    const report = await investigateReleaseGuard(projectId, id, {
      signal: request.signal,
    })
    if (!alive || ticket !== generation || request.signal.aborted) return
    if (report.id !== id || report.projectId !== projectId)
      throw new Error('Mismatched report identity')
    if (
      !Number.isSafeInteger(report.eventsCursor) ||
      report.eventsCursor < props.report.eventsCursor
    ) {
      error.value = 'releaseGuardInvestigation.changed'
      return
    }
    emit('updated', report)
  } catch (cause) {
    if (!alive || ticket !== generation || request.signal.aborted) return
    error.value =
      cause instanceof HttpError && cause.status === 409
        ? 'releaseGuardInvestigation.changed'
        : 'releaseGuard.requestFailed'
  } finally {
    if (alive && ticket === generation) busy.value = false
  }
}
watch([() => props.report.id, () => props.report.projectId], () => {
  generation++
  controller?.abort()
  busy.value = false
  error.value = ''
})
onBeforeUnmount(() => {
  alive = false
  generation++
  controller?.abort()
})
</script>

<template>
  <section class="guard-investigation-update">
    <div class="guard-investigation-update__action">
      <p>{{ t('releaseGuardInvestigation.localOnly') }}</p>
      <AppButton
        :disabled="report.evidenceExpired"
        :loading="busy"
        @click="update"
        >{{ t('releaseGuardInvestigation.update') }}</AppButton
      >
    </div>
    <p v-if="error" class="guard-investigation-error" role="alert">
      {{ t(error) }}
    </p>
    <p
      v-if="report.investigation.status === 'stale'"
      class="guard-investigation-muted"
      role="status"
    >
      {{ t('releaseGuardInvestigation.stale') }}
    </p>
    <p
      v-if="report.investigation.investigatedAt"
      class="guard-investigation-muted"
    >
      {{
        t('releaseGuardInvestigation.updatedAt', {
          at: when(report.investigation.investigatedAt),
        })
      }}
    </p>
    <template v-if="report.investigation.baselines?.length"
      ><h3>{{ t('releaseGuardInvestigation.baselines') }}</h3>
      <article
        v-for="baseline in report.investigation.baselines"
        :key="baseline.targetId"
        class="guard-baseline"
      >
        <strong>{{
          report.targets.find((target) => target.id === baseline.targetId)
            ?.serverName || t('releaseGuardPresentation.unknownTarget')
        }}</strong>
        <p>{{ fact(baseline.status) }}</p>
        <p v-if="baseline.evidenceId">
          <a
            href="#guard-evidence-section"
            @click.prevent="emit('locateEvidence', baseline.evidenceId)"
            >{{
              t('releaseGuardPresentation.evidenceReference', {
                n: guardNumber(1, locale),
              })
            }}</a
          >
        </p>
        <p v-if="baseline.priorAttemptId">
          {{ t('releaseGuard.attempt') }}
          <code>{{ baseline.priorAttemptId }}</code> ·
          {{
            t('releaseGuardInvestigation.age', {
              seconds: guardNumber(baseline.ageSeconds, locale),
            })
          }}
        </p>
        <RouterLink
          v-if="baseline.priorRunId"
          :to="{ name: 'run-detail', params: { id: baseline.priorRunId } }"
          >{{ t('releaseGuard.sourceRun') }}</RouterLink
        >
        <ul v-if="baseline.historicalCandidates?.length">
          <li
            v-for="candidate in baseline.historicalCandidates"
            :key="candidate.attemptId"
          >
            <p>{{ fact(candidate.status) }}</p>
            <code>{{ candidate.attemptId }}</code> ·
            {{
              t('releaseGuardInvestigation.age', {
                seconds: guardNumber(candidate.ageSeconds, locale),
              })
            }}
            <RouterLink
              v-if="candidate.runId"
              :to="{ name: 'run-detail', params: { id: candidate.runId } }"
              >{{ t('releaseGuard.sourceRun') }}</RouterLink
            >
          </li>
        </ul>
        <ul v-if="baseline.missingEvidence.length">
          <li v-for="item in baseline.missingEvidence" :key="item">
            {{ fact(item) }}
          </li>
        </ul>
      </article></template
    >
    <template v-if="report.investigation.sourceComparisons?.length"
      ><h3>{{ t('releaseGuardInvestigation.sourceComparisons') }}</h3>
      <p>{{ t('releaseGuardInvestigation.directTrees') }}</p>
      <article
        v-for="comparison in report.investigation.sourceComparisons"
        :key="comparison.targetId"
        class="guard-source-comparison"
      >
        <strong>{{
          report.targets.find((target) => target.id === comparison.targetId)
            ?.serverName || t('releaseGuardPresentation.unknownTarget')
        }}</strong>
        <p v-if="comparison.available">
          <code>{{ comparison.baseSha }}</code> →
          <code>{{ comparison.currentSha }}</code>
        </p>
        <p v-else class="guard-investigation-muted">
          {{ t('releaseGuardInvestigation.unavailable') }}
        </p>
        <p v-if="comparison.reason">{{ fact(comparison.reason) }}</p>
        <ul v-if="comparison.available">
          <li v-for="file in comparison.files" :key="file.path">
            <code>{{ file.path }}</code> ·
            {{
              t(
                `releaseGuardPresentation.fileStatus.${['added', 'modified', 'deleted', 'renamed', 'copied'].includes(file.status) ? file.status : 'unknown'}`,
              )
            }}
          </li>
        </ul>
        <p v-if="comparison.truncated" class="guard-investigation-muted">
          {{ t('releaseGuardInvestigation.truncated') }}
        </p>
      </article></template
    >
  </section>
</template>

<style scoped>
.guard-investigation-update {
  display: grid;
  gap: 12px;
  margin: 16px 0;
}
.guard-investigation-update__action {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 14px;
  flex-wrap: wrap;
  padding: 16px;
  border: 1px solid var(--color-border);
  border-radius: var(--rounded);
}
p {
  margin: 0;
  line-height: 1.6;
  font-size: 0.85rem;
}
.guard-investigation-update__action p {
  flex: 1 1 260px;
  color: var(--color-dim);
}
.guard-investigation-update :deep(.app-btn) {
  min-height: 44px;
}
h3 {
  margin: 14px 0 0;
  font-size: 1rem;
}
.guard-investigation-muted {
  color: var(--color-dim);
}
.guard-investigation-error {
  color: var(--color-red);
}
.guard-baseline,
.guard-source-comparison {
  padding: 14px;
  border-left: 2px solid var(--color-border-strong);
  display: grid;
  gap: 8px;
  font-size: 0.85rem;
  overflow-wrap: anywhere;
}
code {
  font: 0.75rem var(--font-mono);
}
ul {
  margin: 0;
  padding-left: 18px;
  line-height: 1.7;
}
a {
  color: var(--color-primary);
}
</style>
