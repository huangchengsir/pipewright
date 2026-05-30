/**
 * AI generate pipeline API — aligns to frozen 2.5 contract.
 *
 * POST /api/projects/{id}/pipeline/ai-generate  → AIGenerateResponse
 * POST /api/projects/{id}/pipeline/ai-apply     → AIApplyResponse
 *
 * Keys are never present in any request or response — AI provider keys
 * are server-side only, never exposed to the frontend.
 */

import { http } from './http'

// ─── Generate DTO (frozen) ────────────────────────────────────────────────────

export interface AIAnalysis {
  /** Whether the repo was successfully cloned. false = degraded mode. */
  cloned: boolean
  language: string
  languageVersion: string
  buildTool: string
  hasDockerfile: boolean
  artifactHint: string
  /** Detected signals e.g. ["package.json", "engines.node=22"] */
  signals: string[]
}

export interface AIProposalJob {
  id: string
  name: string
  type: string
  summary: string
}

export interface AIProposalStage {
  id: string
  name: string
  kind: string
  jobs: AIProposalJob[]
}

export interface AIToolchain {
  language: string
  version: string
}

export interface AIProposalBuild {
  model: string
  toolchain: AIToolchain
  artifactType: string
  dockerfilePath: string
}

export interface AIProposalBranchMapping {
  id: string
  branchPattern: string
  environment: string
}

export interface AIProposal {
  stages: AIProposalStage[]
  build: AIProposalBuild
  branchMappings: AIProposalBranchMapping[]
  rationale: string
}

export interface AIGenerateResponse {
  /** false when AI provider is not configured/enabled. HTTP 200. */
  available: boolean
  /** Non-empty when available=false or LLM call failed. Never contains API keys. */
  reason: string
  analysis: AIAnalysis
  proposal: AIProposal
}

// ─── Apply DTO (frozen) ───────────────────────────────────────────────────────

export interface AIApplySelections {
  /** Stage IDs from proposal to include in the spec write. */
  stageIds: string[]
  /** Whether to write the build config from proposal. */
  build: boolean
  /** BranchMapping IDs from proposal to merge into triggers. */
  branchMappingIds: string[]
}

export interface AIApplyResponse {
  applied: {
    spec: boolean
    settings: boolean
    triggers: boolean
  }
}

// ─── Request types ────────────────────────────────────────────────────────────

export interface AIGenerateInput {
  /** Optional natural-language supplement to guide the AI proposal. */
  nlSupplement?: string
}

export interface AIApplyInput {
  proposal: AIProposal
  selections: AIApplySelections
}

// ─── API functions ────────────────────────────────────────────────────────────

export async function aiGenerate(
  projectId: string,
  input: AIGenerateInput,
): Promise<AIGenerateResponse> {
  return http.post<AIGenerateResponse>(
    `/api/projects/${projectId}/pipeline/ai-generate`,
    input,
  )
}

export async function aiApply(
  projectId: string,
  input: AIApplyInput,
): Promise<AIApplyResponse> {
  return http.post<AIApplyResponse>(
    `/api/projects/${projectId}/pipeline/ai-apply`,
    input,
  )
}
