/**
 * Settings API — diagnosis feedback-loop stats (Story 7-5 frozen contract — FR-26).
 *
 * GET /api/settings/diagnosis-stats → DiagnosisStats (auth, read-only)
 *
 * Aggregate of all diagnosis 👍/👎 feedback: accuracy, counts, recent trend, and
 * recent corrections (knowledge-base seeds). When no feedback exists the server
 * returns all-zero counts, accuracy:null and empty arrays — never an error.
 *
 * FROZEN shape: knowledge-base retrieval (later) only consumes these fields and
 * does not change them.
 */

import { http } from './http'

/** A single point in the accuracy trend (most-recent-N bucketing, simple impl). */
export interface DiagnosisTrendBucket {
  period: string    // bucket marker (RFC3339 of last item in bucket)
  accuracy: number  // up/total within the bucket
  count: number
}

/** A 👎 correction with a supplied correct root cause (knowledge-base seed). */
export interface DiagnosisCorrection {
  runId: string
  correctRootCause: string // already masked server-side
  at: string               // RFC3339
}

export interface DiagnosisStats {
  totalFeedback: number
  thumbsUp: number
  thumbsDown: number
  /** up/total; null when no feedback yet. */
  accuracy: number | null
  recentTrend: DiagnosisTrendBucket[]
  recentCorrections: DiagnosisCorrection[]
}

export function getDiagnosisStats(): Promise<DiagnosisStats> {
  return http.get<DiagnosisStats>('/api/settings/diagnosis-stats')
}
