import type { GuardEvidence } from '../api/releaseGuard'
import { releaseGuardText } from './releaseGuardText'

type Translate = (
  key: string,
  values?: Record<string, string | number>,
) => string
export interface EvidencePresentation {
  kind: string
  title: string
  tone: 'neutral' | 'danger' | 'warning'
  facts: Array<{ label: string; value: string }>
  notices: string[]
}
const namespace = 'releaseGuardPresentation.'
export function guardPresentationText(value: string, t: Translate): string {
  const known: Record<string, string> = {
    source_cache_busy: namespace + 'reasons.sourceCacheBusy',
    source_cache_missing: 'releaseGuardInvestigation.cacheMissing',
    source_cache_unreadable: namespace + 'reasons.sourceCacheUnreadable',
    base_commit_not_cached: namespace + 'reasons.baseCommitNotCached',
    current_commit_not_cached: namespace + 'reasons.currentCommitNotCached',
    source_tree_unavailable: 'releaseGuardInvestigation.unavailable',
    log_time_gap: namespace + 'reasons.logTimeGap',
    service_restarted: namespace + 'reasons.serviceRestarted',
    resolved_source_unavailable:
      namespace + 'reasons.resolvedSourceUnavailable',
    source_run_unavailable: namespace + 'reasons.sourceRunUnavailable',
    source_revision_conflict: namespace + 'reasons.sourceRevisionConflict',
    source_revision_not_confirmed:
      namespace + 'reasons.sourceRevisionNotConfirmed',
    current_deployment_identity_unavailable:
      namespace + 'reasons.currentDeploymentIdentityUnavailable',
    deployment_history_unavailable:
      namespace + 'reasons.deploymentHistoryUnavailable',
  }
  if (known[value]) return t(known[value])
  const translated = releaseGuardText(value, t)
  return translated === value ? t(namespace + 'unconfirmed') : translated
}
export function guardDate(
  value: unknown,
  locale: string,
  t: Translate,
): string {
  const timestamp =
    typeof value === 'string' &&
    /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/.test(
      value,
    )
      ? Date.parse(value)
      : NaN
  return Number.isFinite(timestamp)
    ? new Intl.DateTimeFormat(locale, {
        dateStyle: 'medium',
        timeStyle: 'medium',
      }).format(timestamp)
    : t(namespace + 'unknown')
}
export function guardNumber(value: number, locale: string): string {
  return new Intl.NumberFormat(locale).format(value)
}
export function guardTimelineTitle(value: string, t: Translate): string {
  const titles: Record<string, string> = {
    runtime_sample: 'releaseGuardPresentation.kinds.runtime',
    attached_prepared: 'releaseGuard.execution.prepared',
    deployment_started: 'releaseGuardPresentation.timeline.deploymentStarted',
    deployment_activation_started:
      'releaseGuardPresentation.timeline.activationStarted',
    container_activated: 'releaseGuard.binding',
    deployment_activated: 'releaseGuard.binding',
    activation_bound: 'releaseGuard.binding',
    deployment_succeeded: 'releaseGuardFacts.deploymentCompleted',
    deployment_deploy_failed: 'releaseGuardFacts.deploymentFailed',
    deployment_rolled_back: 'releaseGuardFacts.rolledBack',
    deployment_outcome_unknown: 'releaseGuardFacts.outcomeUnknown',
    deployment_finished: 'releaseGuardPresentation.timeline.deploymentFinished',
    baseline_already_failing:
      'releaseGuardPresentation.timeline.baselineFailing',
    logs_requested: 'releaseGuardPresentation.timeline.logsRequested',
    logs_collected: 'releaseGuardPresentation.timeline.logsCollected',
    logs_unavailable: 'releaseGuardPresentation.timeline.logsUnavailable',
    stop_requested: 'releaseGuardFacts.userStopped',
    waiting_for_observation_exit: 'releaseGuard.execution.stopping',
    finished: 'releaseGuardPresentation.timeline.finished',
    investigated: 'releaseGuardPresentation.timeline.investigated',
    investigation_stale: 'releaseGuardInvestigation.stale',
    interrupted: 'releaseGuard.execution.interrupted',
    evidence_expired: 'releaseGuardPresentation.timeline.evidenceExpired',
  }
  return titles[value] ? t(titles[value]) : guardPresentationText(value, t)
}
function record(value: unknown): Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {}
}
function count(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0
}

// A projection of the persisted, narrow Go evidence types. Collection success is
// never a business-health verdict. Do not coerce strings, absent fields or zeros.
export function presentGuardEvidence(
  evidence: GuardEvidence,
  t: Translate,
  locale: string,
): EvidencePresentation {
  const text = (key: string) => t(namespace + key)
  const value = record(evidence.value)
  const unknown = text('unknown')
  const facts: EvidencePresentation['facts'] = []
  const notices: string[] = []
  const add = (label: string, fact: string) =>
    facts.push({ label: text(label), value: fact })
  const knownKinds = [
    'runtime',
    'baseline',
    'expected_image',
    'deployment_health_gate',
    'logs',
  ]
  const kind = text(
    `kinds.${knownKinds.includes(evidence.kind) ? evidence.kind : 'unknown'}`,
  )
  let title = text('titles.unknown')
  let tone: EvidencePresentation['tone'] = 'warning'
  if (evidence.kind === 'runtime' || evidence.kind === 'baseline') {
    // Non-ok snapshots contain Go zero values. Those defaults are not observations.
    const imageOnlyTruncated =
      value.reason === 'image_identity_truncated' &&
      (!evidence.reason || evidence.reason === 'image_identity_truncated')
    const usable =
      value.outcome === 'ok' &&
      value.reason !== 'inspection_truncated' &&
      evidence.reason !== 'inspection_truncated' &&
      (imageOnlyTruncated ||
        (evidence.truncated !== true && value.truncated !== true))
    const bindingChanged = value.targetChanged === true
    const bindingUnknown =
      value.targetChanged !== undefined &&
      typeof value.targetChanged !== 'boolean'
    if (usable && imageOnlyTruncated)
      notices.push(text('runtimeImageTruncated'))
    if (usable && bindingChanged) notices.push(text('targetBindingChanged'))
    if (usable && bindingUnknown) notices.push(text('targetBindingUnknown'))
    const running =
      usable && typeof value.running === 'boolean' ? value.running : undefined
    const oom =
      usable && typeof value.oomKilled === 'boolean'
        ? value.oomKilled
        : undefined
    const notFound =
      value.outcome === 'not_found' && value.reason === 'container_not_found'
    const unhealthy = usable && value.health === 'unhealthy'
    title = notFound
      ? text('titles.notFound')
      : !usable
        ? text('titles.unavailable')
        : oom === true
          ? text('titles.oom')
          : running === false
            ? text('titles.exited')
            : unhealthy
              ? text('titles.unhealthy')
              : running === true
                ? text('titles.running')
                : text('titles.unknown')
    tone =
      notFound || running === false || oom === true || unhealthy
        ? 'danger'
        : bindingChanged || bindingUnknown || imageOnlyTruncated
          ? 'warning'
          : running === true
            ? 'neutral'
            : 'warning'
    add(
      'running',
      running === undefined
        ? unknown
        : text(running ? 'runningValue' : 'stoppedValue'),
    )
    add(
      'exitCode',
      usable && count(value.exitCode) && value.exitCode <= 255
        ? guardNumber(value.exitCode, locale)
        : unknown,
    )
    add('oom', oom === undefined ? unknown : text(oom ? 'yes' : 'no'))
    add(
      'restarts',
      usable && count(value.restartCount)
        ? guardNumber(value.restartCount, locale)
        : unknown,
    )
    const health =
      usable && typeof value.health === 'string' ? value.health : undefined
    // Empty string is the actual collector's explicit no-HEALTHCHECK value.
    const healthKey =
      health === ''
        ? 'absent'
        : ['healthy', 'unhealthy', 'starting'].includes(health ?? '')
          ? health
          : 'unknown'
    add('health', text(`healthValues.${healthKey}`))
    add(
      'imageIdentity',
      usable &&
        typeof value.configId === 'string' &&
        /^sha256:[a-f0-9]{64}$/i.test(value.configId)
        ? text('recorded')
        : unknown,
    )
  } else if (evidence.kind === 'expected_image') {
    const valid =
      typeof value.digest === 'string' &&
      /^sha256:[a-f0-9]{64}$/i.test(value.digest)
    const digestKind =
      typeof value.digestKind === 'string' &&
      ['config_id', 'manifest', 'index', 'repository_digest_unknown'].includes(
        value.digestKind,
      )
        ? value.digestKind
        : 'unknown'
    const localValid =
      digestKind !== 'config_id' ||
      value.identitySource === 'script_pre_activation'
    title =
      valid && localValid && digestKind !== 'unknown'
        ? text('titles.image')
        : text('titles.unknown')
    tone =
      valid && localValid && digestKind !== 'unknown' ? 'neutral' : 'warning'
    add('imageIdentity', text(`identityValues.${digestKind}`))
    add(
      'imageRecorded',
      valid && localValid && digestKind !== 'unknown'
        ? text('recorded')
        : unknown,
    )
  } else if (evidence.kind === 'deployment_health_gate') {
    const failed =
      value.healthGate === 'failed' &&
      typeof value.kind === 'string' &&
      ['deploy_failed', 'rolled_back'].includes(value.kind)
    title = text(failed ? 'titles.gateFailed' : 'titles.unknown')
    tone = failed ? 'danger' : 'warning'
    add('gate', failed ? text('failed') : unknown)
  } else if (evidence.kind === 'logs') {
    const failed = ['logs_unavailable', 'log_scope_invalid'].includes(
      evidence.reason,
    )
    const collected =
      !failed &&
      typeof value.text === 'string' &&
      ['untrusted_windowed_logs', 'logs_window_partial'].includes(
        evidence.reason,
      )
    const partial =
      collected &&
      (evidence.reason === 'logs_window_partial' || evidence.truncated === true)
    const lines =
      typeof value.text === 'string'
        ? value.text.split('\n').filter((line) => line.length > 0).length
        : 0
    title = text(
      failed
        ? 'titles.logsUnavailable'
        : partial
          ? 'titles.logsPartial'
          : collected
            ? lines === 0
              ? 'titles.logsEmpty'
              : 'titles.logs'
            : 'titles.unknown',
    )
    tone = collected && !partial ? 'neutral' : 'warning'
    if (partial) notices.push(text('logsPartialScope'))
    add('windowStart', guardDate(value.from, locale, t))
    add('windowEnd', guardDate(value.until, locale, t))
    add('logLines', collected ? guardNumber(lines, locale) : unknown)
    add(
      'logGap',
      collected && typeof value.gap === 'boolean'
        ? text(value.gap ? 'yes' : 'no')
        : unknown,
    )
  }
  return { kind, title, tone, facts, notices }
}
