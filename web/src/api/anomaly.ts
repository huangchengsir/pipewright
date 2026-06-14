/**
 * Anomaly detection & alerts API — Story 6-5 (FR-23).
 *
 * GET    /api/anomaly/rules                  → { items: AnomalyRule[] }
 * POST   /api/anomaly/rules                  → AnomalyRule           (needs CSRF)
 * DELETE /api/anomaly/rules/:id              → 204                   (needs CSRF)
 * POST   /api/anomaly/check                  → { alerts: AnomalyAlert[] }  (needs CSRF)
 * GET    /api/anomaly/alerts?serverId=&limit=→ { alerts: AnomalyAlert[] }
 *
 * Rules threshold server metrics (collected by the 6-1 SSH pipeline) and emit
 * alerts on a hit. `metric` is a usage percentage (cpu = loadavg1/cores×100,
 * memory = used/total×100, disk = used/total×100). Servers that are unreachable
 * or whose metric is unavailable are skipped — never false-positives.
 */

import { http } from './http'

/** Metric the rule thresholds. */
export type AnomalyMetric = 'cpu' | 'memory' | 'disk'

/** Comparison operator. */
export type AnomalyOperator = 'gt' | 'lt'

export interface AnomalyRule {
  id: string
  metric: AnomalyMetric
  operator: AnomalyOperator
  /** Percentage threshold. */
  threshold: number
  /** Null = global (applies to all servers); otherwise scoped to that server. */
  serverId: string | null
  enabled: boolean
  createdAt: string
}

export interface CreateAnomalyRuleInput {
  metric: AnomalyMetric
  operator: AnomalyOperator
  threshold: number
  /** Omit / null for a global rule. */
  serverId?: string | null
  enabled?: boolean
}

export interface AnomalyAlert {
  id: string
  serverId: string
  serverName: string
  metric: AnomalyMetric
  operator: AnomalyOperator
  threshold: number
  /** Actual metric value (percentage) at the time of the hit. */
  value: number
  /** Human-readable message, e.g. "磁盘使用率 92.3% > 85%". */
  message: string
  /** RFC3339 timestamp of the alert. */
  at: string
}

export interface ListAlertsParams {
  serverId?: string
  limit?: number
}

export async function listAnomalyRules(): Promise<AnomalyRule[]> {
  const res = await http.get<{ items: AnomalyRule[] }>('/api/anomaly/rules')
  return res.items
}

export async function createAnomalyRule(input: CreateAnomalyRuleInput): Promise<AnomalyRule> {
  return http.post<AnomalyRule>('/api/anomaly/rules', input)
}

/** Update a rule in full (metric/operator/threshold/scope/enabled). Needs CSRF. */
export async function updateAnomalyRule(
  id: string,
  input: CreateAnomalyRuleInput,
): Promise<AnomalyRule> {
  return http.patch<AnomalyRule>(`/api/anomaly/rules/${id}`, input)
}

export async function deleteAnomalyRule(id: string): Promise<void> {
  return http.delete<void>(`/api/anomaly/rules/${id}`)
}

/** Background detection cadence (seconds). intervalSeconds=0 → periodic detection off. */
export interface AnomalyConfig {
  intervalSeconds: number
  cooldownSeconds: number
}

export async function getAnomalyConfig(): Promise<AnomalyConfig> {
  return http.get<AnomalyConfig>('/api/anomaly/config')
}

/** One point on a server metric trend series. null = metric unavailable at that time. */
export interface MetricPoint {
  at: string
  cpu: number | null
  memory: number | null
  disk: number | null
}

/** Server metric history (CPU/memory/disk %) over the last `hours` hours, ascending. */
export async function getMetricsHistory(serverId: string, hours: number): Promise<MetricPoint[]> {
  const qs = new URLSearchParams({ serverId, hours: String(hours) })
  const res = await http.get<{ points: MetricPoint[]; hours: number }>(
    `/api/metrics/history?${qs.toString()}`,
  )
  return res.points
}

/** Run detection now: evaluate all enabled rules; returns the alerts created this run. */
export async function checkAnomaly(): Promise<AnomalyAlert[]> {
  const res = await http.post<{ alerts: AnomalyAlert[] }>('/api/anomaly/check')
  return res.alerts
}

export async function listAnomalyAlerts(params: ListAlertsParams = {}): Promise<AnomalyAlert[]> {
  const qs = new URLSearchParams()
  if (params.serverId) qs.set('serverId', params.serverId)
  if (params.limit != null) qs.set('limit', String(params.limit))
  const suffix = qs.toString() ? `?${qs.toString()}` : ''
  const res = await http.get<{ alerts: AnomalyAlert[] }>(`/api/anomaly/alerts${suffix}`)
  return res.alerts
}
