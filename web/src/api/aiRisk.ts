/**
 * AI script-risk annotation API (AI moat).
 *
 * POST /api/projects/{id}/pipeline/analyze-risks → AnalyzeRisksResponse
 *
 * Annotates the pipeline's script-step commands for risks: a deterministic
 * regex pre-pass (rm -rf /, curl|sh, chmod 777, plaintext secrets, unpinned
 * `latest` images) PLUS an optional LLM semantic pass. Always returns 200 —
 * when AI is unconfigured/failed it gracefully degrades to rule-only findings
 * with `aiEnhanced=false` and a human reason. Secrets are masked server-side;
 * findings never echo a detected plaintext secret.
 */

import { http } from './http'

export type RiskLevel = 'high' | 'medium' | 'low'
export type RiskSource = 'rule' | 'ai'

export interface RiskFinding {
  level: RiskLevel
  /** Step the finding belongs to (may be empty). */
  stepName: string
  /** 1-based command line within the step; 0 = step/image-level. */
  line: number
  title: string
  why: string
  suggestion: string
  /** `rule` = deterministic regex; `ai` = LLM semantic pass. */
  source: RiskSource
}

export interface AnalyzeRisksResponse {
  /** Never null — empty array means no risks found. */
  findings: RiskFinding[]
  /** Whether the LLM semantic pass actually ran (false = rule-only). */
  aiEnhanced: boolean
  /** Human reason when aiEnhanced=false (AI unconfigured/failed). Never contains secrets. */
  aiReason: string
  generatedAt: string
}

export async function analyzeRisks(projectId: string): Promise<AnalyzeRisksResponse> {
  return http.post<AnalyzeRisksResponse>(
    `/api/projects/${projectId}/pipeline/analyze-risks`,
  )
}
