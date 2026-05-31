/**
 * Custom nodes API — reuse library Tier 2 (à la Jenkins custom steps / 云效自建任务模板).
 *
 * Named, reusable single-node definitions: an underlying job type + a config snapshot.
 * The user configures a node on the canvas (often a `templated`/`script` custom node),
 * saves it ("存为自定义节点"), then picks it from the node picker to insert a Job with the
 * saved config pre-filled. Complements pipeline templates (which reuse a whole stage span).
 *
 *   GET    /api/custom-nodes       → { customNodes: CustomNode[] }
 *   POST   /api/custom-nodes       → CustomNode   (needs CSRF)
 *   GET    /api/custom-nodes/{id}  → CustomNode
 *   PUT    /api/custom-nodes/{id}  → CustomNode   (needs CSRF)
 *   DELETE /api/custom-nodes/{id}  → 204          (needs CSRF)
 *
 * config is a free-form KV snapshot of Job.config (no schema enforced); carries no
 * plaintext secret (same shape as a pipeline spec — secrets stay vault references).
 */

import { http } from './http'

export interface CustomNode {
  id: string
  name: string
  description: string
  /** Underlying job type (e.g. 'templated', 'script', 'build_frontend'). */
  nodeType: string
  summary: string
  config: Record<string, unknown>
  createdAt: string
  updatedAt: string
}

export interface SaveCustomNodeInput {
  name: string
  description?: string
  nodeType: string
  summary?: string
  config: Record<string, unknown>
}

interface ListCustomNodesResponse {
  customNodes: CustomNode[]
}

export async function listCustomNodes(): Promise<CustomNode[]> {
  const res = await http.get<ListCustomNodesResponse>('/api/custom-nodes')
  return res.customNodes ?? []
}

export async function getCustomNode(id: string): Promise<CustomNode> {
  return http.get<CustomNode>(`/api/custom-nodes/${id}`)
}

export async function createCustomNode(input: SaveCustomNodeInput): Promise<CustomNode> {
  return http.post<CustomNode>('/api/custom-nodes', input)
}

export async function updateCustomNode(
  id: string,
  input: SaveCustomNodeInput,
): Promise<CustomNode> {
  return http.put<CustomNode>(`/api/custom-nodes/${id}`, input)
}

export async function deleteCustomNode(id: string): Promise<void> {
  await http.delete<void>(`/api/custom-nodes/${id}`)
}
