/**
 * DORA metrics API — Story FR-8-15.
 *
 * GET /api/metrics/dora?projectId=&window=30d → DoraMetrics
 *
 * Read-only aggregation over existing pipeline-run data (no new event capture).
 * Derivation口径(与后端 internal/dora 一致):
 *   - 一次「部署」  = 一条进入终态(success|failed|partial_failed|rolled_back)的运行。
 *   - 部署频率      = 成功部署数 / 窗口天数(日均;另给周均)。
 *   - 变更前置时长  = 每条成功部署 finished−commit 的中位数(秒);commit 不可得时用 created 代理。
 *   - 变更失败率    = 失败部署 / 终态部署总数 ∈ [0,1]。
 *   - MTTR          = 「失败 → 下一次成功」配对恢复时长的中位数(秒)。
 *
 * Errors follow the canonical envelope; callers inspect `err.apiError?.code` /
 * `.message` / `err.status` on HttpError.
 */

import { http } from './http'
import { t } from '../i18n'

/** DORA performance band (Elite/High/Medium/Low; `none` when sample is too small). */
export type DoraBand = 'elite' | 'high' | 'medium' | 'low' | 'none'

/** One metric: raw value + DORA band + sample size (0 ⇒ no data, render "—"). */
export interface DoraMetric {
  /** Frequency = deploys/day; lead/MTTR = seconds; CFR = ratio [0,1]. */
  value: number
  band: DoraBand
  /** Number of samples backing this metric. 0 ⇒ no data. */
  sampleCount: number
}

/** One time bucket of the trend series (day-aligned). */
export interface DoraTrendPoint {
  /** RFC3339 bucket start (UTC, aligned to 00:00). */
  bucketStart: string
  deployments: number
  successes: number
  failures: number
  /** Change failure rate within this bucket [0,1]; 0 when no deployments. */
  changeFailureRate: number
}

export interface DoraMetricsBlock {
  deploymentFrequency: DoraMetric
  leadTime: DoraMetric
  changeFailureRate: DoraMetric
  mttr: DoraMetric
}

export interface DoraMetrics {
  /** Normalized window label, e.g. "30d". */
  window: string
  windowDays: number
  /** Echoed project filter ("" = all projects). */
  projectId: string
  totalDeployments: number
  successfulDeployments: number
  failedDeployments: number

  /** Bare convenience values (also present, with bands, under `metrics`). */
  deploymentFrequency: number
  deploymentFrequencyPerWeek: number
  leadTimeSeconds: number
  changeFailureRate: number
  mttrSeconds: number

  metrics: DoraMetricsBlock
  trend: DoraTrendPoint[]
  /** RFC3339 aggregation timestamp ("data as of"). */
  generatedAt: string
}

export interface GetDoraMetricsParams {
  /** Optional project filter; omit / empty = all projects. */
  projectId?: string
  /** Window like "30d" / "7d" / "90d". Defaults server-side to "30d". */
  window?: string
}

/** Fetch the four DORA metrics for a window (read-only aggregation). */
export async function getDoraMetrics(params: GetDoraMetricsParams = {}): Promise<DoraMetrics> {
  const qs = new URLSearchParams()
  if (params.projectId) qs.set('projectId', params.projectId)
  if (params.window) qs.set('window', params.window)
  const suffix = qs.toString() ? `?${qs.toString()}` : ''
  return http.get<DoraMetrics>(`/api/metrics/dora${suffix}`)
}

// ─── Presentation helpers (pure; unit-tested) ────────────────────────────────

/** Human-readable label for a DORA band (localized; matches product copy). */
export function bandLabel(band: DoraBand): string {
  switch (band) {
    case 'elite':
      return t('metrics.band.elite')
    case 'high':
      return t('metrics.band.high')
    case 'medium':
      return t('metrics.band.medium')
    case 'low':
      return t('metrics.band.low')
    default:
      return t('metrics.band.none')
  }
}

/**
 * Format a duration in seconds to a compact human string (s/min/h/d).
 * Returns "—" for non-finite / negative inputs (callers also gate on sampleCount).
 */
export function formatDuration(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return '—'
  if (seconds < 60) return t('metrics.duration.seconds', { n: Math.round(seconds) })
  const mins = seconds / 60
  if (mins < 60) return t('metrics.duration.minutes', { n: round1(mins) })
  const hours = mins / 60
  if (hours < 24) return t('metrics.duration.hours', { n: round1(hours) })
  const days = hours / 24
  return t('metrics.duration.days', { n: round1(days) })
}

/** Format deployment frequency (deploys/day) to an intuitive cadence string. */
export function formatFrequency(perDay: number): string {
  if (!Number.isFinite(perDay) || perDay <= 0) return '—'
  if (perDay >= 1) return t('metrics.freq.perDay', { n: round1(perDay) })
  const perWeek = perDay * 7
  if (perWeek >= 1) return t('metrics.freq.perWeek', { n: round1(perWeek) })
  const perMonth = perDay * 30
  return t('metrics.freq.perMonth', { n: round1(perMonth) })
}

/** Format a [0,1] ratio as a percentage string. */
export function formatPercent(ratio: number): string {
  if (!Number.isFinite(ratio) || ratio < 0) return '—'
  return `${round1(ratio * 100)}%`
}

function round1(n: number): number {
  return Math.round(n * 10) / 10
}
