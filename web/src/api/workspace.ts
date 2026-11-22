import { http, type HttpOptions } from './http'
import type { CreateProjectInput, Project, RunStatus } from './projects'


export interface WorkspaceProduct {
  id: string
  businessProjectId: string
  name: string
  revision: number
}

export interface WorkspaceBusinessProject {
  id: string
  name: string
  revision: number
  products: WorkspaceProduct[]
}

export interface WorkspaceApplication extends Project {
  productId?: string
  businessProjectId?: string
  assignmentRevision: number
  latestRun?: { runId: string; status: string; commit: string; createdAt: string }
  productionDeployment?: { runId: string; environment: string; commit: string; deployedAt: string }
}

export interface WorkspaceCardScope {
  kind: 'all' | 'unassigned' | 'business' | 'product'
  id?: string
  label: string
  productNames: Record<string, string>
}

const runStatus: Record<RunStatus, string> = {
  '成功': 'success',
  '失败': 'failed',
  '进行中': 'running',
  '部分失败': 'partial_failed',
  '已回滚': 'rolled_back',
  '排队中': 'queued',
}

interface ApplicationPage {
  items: WorkspaceApplication[]
  page: number
  pageSize: number
  total: number
}

const root = '/api/workspace'
const businessURL = (id: string) => root + '/business-projects/' + encodeURIComponent(id)
const productURL = (id: string) => root + '/products/' + encodeURIComponent(id)

export const listWorkspace = (options?: HttpOptions) =>
  http.get<{ businessProjects: WorkspaceBusinessProject[] }>(root, { ...options, throwOnUnauthorized: true })

export function listWorkspaceApplications(scope: WorkspaceCardScope, page: number, pageSize: number, q: string, status: 'all' | RunStatus, options?: HttpOptions) {
  const params = new URLSearchParams({ page: String(page), pageSize: String(pageSize) })
  if (scope.kind === 'unassigned') params.set('unassigned', 'true')
  if (scope.kind === 'business' && scope.id) params.set('businessProjectId', scope.id)
  if (scope.kind === 'product' && scope.id) params.set('productId', scope.id)
  if (q) params.set('q', q)
  if (status !== 'all') params.set('status', runStatus[status])
  return http.get<ApplicationPage>(root + '/applications?' + params, { ...options, throwOnUnauthorized: true })
}

export const createBusinessProject = (name: string, options?: HttpOptions) =>
  http.post<WorkspaceBusinessProject>(root + '/business-projects', { name }, options)
export const updateBusinessProject = (id: string, name: string, expectedRevision: number, options?: HttpOptions) =>
  http.patch<WorkspaceBusinessProject>(businessURL(id), { name, expectedRevision }, options)
export const deleteBusinessProject = (id: string, expectedRevision: number, options?: HttpOptions) =>
  http.delete<void>(businessURL(id) + '?expectedRevision=' + expectedRevision, options)

export const createWorkspaceProduct = (businessId: string, name: string, options?: HttpOptions) =>
  http.post<WorkspaceProduct>(businessURL(businessId) + '/products', { name }, options)
export const updateWorkspaceProduct = (id: string, name: string, expectedRevision: number, options?: HttpOptions) =>
  http.patch<WorkspaceProduct>(productURL(id), { name, expectedRevision }, options)
export const deleteWorkspaceProduct = (id: string, expectedRevision: number, options?: HttpOptions) =>
  http.delete<void>(productURL(id) + '?expectedRevision=' + expectedRevision, options)

export const createWorkspaceApplication = (productId: string, input: CreateProjectInput, options?: HttpOptions) =>
  http.post<Project>(productURL(productId) + '/applications', input, options)
export const assignWorkspaceApplication = (appId: string, productId: string | null, expectedRevision: number, options?: HttpOptions) =>
  http.put<void>(root + '/applications/' + encodeURIComponent(appId) + '/assignment', { productId, expectedRevision }, options)
