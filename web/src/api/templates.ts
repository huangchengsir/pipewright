/**
 * Pipeline templates API — FR-8-13 (reuse base · à la Jenkins Shared Library / 云效模板).
 *
 * Named, reusable pipeline definitions (same stages model as a project pipeline),
 * shared across projects. A project can instantiate a template into its own pipeline.
 *
 *   GET    /api/templates                              → { templates: TemplateSummary[] }
 *   POST   /api/templates                              → Template      (needs CSRF)
 *   GET    /api/templates/{id}                         → Template
 *   DELETE /api/templates/{id}                         → 204           (needs CSRF)
 *   POST   /api/projects/{id}/pipeline/apply-template  → PipelineDTO   (needs CSRF)
 *
 * Templates carry no plaintext secret (credentials stay vault references in 2-4 settings).
 */

import { http, type HttpOptions } from './http'
import { configWriteOptions } from './configRevision'
import type { PipelineStage, PipelineDTO, SavePipelineInput } from './pipeline'

/** List item — metadata + stage count only (lighter than the full spec). */
export interface TemplateSummary {
  id: string
  name: string
  description: string
  stageCount: number
  createdAt: string
  updatedAt: string
}

/** Full template — includes the complete stage spec. */
export interface Template {
  id: string
  name: string
  description: string
  stages: PipelineStage[]
  createdAt: string
  updatedAt: string
}

export interface CreateTemplateInput {
  name: string
  description?: string
  stages: SavePipelineInput['stages']
}

interface ListTemplatesResponse {
  templates: TemplateSummary[]
}

export async function listTemplates(options?: HttpOptions): Promise<TemplateSummary[]> {
  const res = await http.get<ListTemplatesResponse>('/api/templates', options)
  return res.templates ?? []
}

export async function getTemplate(id: string, options?: HttpOptions): Promise<Template> {
  return http.get<Template>(`/api/templates/${id}`, options)
}

export async function createTemplate(input: CreateTemplateInput, options?: HttpOptions): Promise<Template> {
  return http.post<Template>('/api/templates', input, options)
}

export async function deleteTemplate(id: string, options?: HttpOptions): Promise<void> {
  await http.delete<void>(`/api/templates/${id}`, options)
}

/** Apply a template's stages onto a project's pipeline; returns the resulting pipeline. */
export async function applyTemplate(projectId: string, templateId: string, expectedRevision?: string, options?: HttpOptions): Promise<PipelineDTO> {
  const requestOptions=configWriteOptions(expectedRevision,options)
  return requestOptions ? http.post<PipelineDTO>(`/api/projects/${projectId}/pipeline/apply-template`, { templateId }, requestOptions) : http.post<PipelineDTO>(`/api/projects/${projectId}/pipeline/apply-template`, { templateId })
}
