import { http } from './http'

export interface MCPConfigDiff {
  type: string
  stageId?: string
  jobId?: string
  field: string
  before?: unknown
  after?: unknown
  impact: string
}

export interface MCPConfigValidation {
  state: string
  applyAllowed: boolean
  checks: Array<{ category: string; status: string; issueCode?: string }>
  issues: Array<{ code: string; field?: string }>
}

export interface MCPConfigApproval {
  id?: string
  state: string
  expiresAt?: string
  eligible: boolean
  reason?: string
}

export interface MCPConfigReview {
  draftId: string
  projectId: string
  baseRevision: string
  policyVersion: string
  reviewDigest: string
  state: string
  expiresAt: string
  diff: MCPConfigDiff[]
  validation: MCPConfigValidation
  approval: MCPConfigApproval
  reviewUrl: string
  complete: boolean
}

export interface ApproveMCPConfigDraft {
  reviewDigest: string
  baseRevision: string
  policyVersion: string
}

export function getMCPConfigReview(id: string, options?: RequestInit): Promise<MCPConfigReview> {
  return http.get(`/api/mcp/config-drafts/${encodeURIComponent(id)}/review`, options)
}

/** Approve the exact reviewed draft. Applying it remains a separate MCP action. */
export function approveMCPConfigDraft(id: string, input: ApproveMCPConfigDraft, options?: RequestInit): Promise<MCPConfigApproval> {
  return http.post(`/api/mcp/config-drafts/${encodeURIComponent(id)}/approve`, input, options)
}
