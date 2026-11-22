import { http, type HttpOptions } from './http'

export interface ReleaseEnvironment {
  name: string
  targetIds: string[]
  targets?: { id: string; name: string }[]
}

export interface ReleaseApplication {
  applicationId: string
  name: string
  defaultBranch: string
  environments: ReleaseEnvironment[]
  canRelease?: boolean
  reason?: string
}

export interface ReleaseSelection {
  applicationId: string
  branch?: string
  commitSha?: string
  environment: string
}

export interface ReleasePlanItem {
  applicationId: string
  environment: string
  commitSha: string
  targetIds: string[]
  configDigest: string
  snapshotRef: string
}

export interface ReleasePlan {
  productId: string
  items: ReleasePlanItem[]
}

export interface ReleaseBatch {
  id: string
  productId: string
  revision: number
  state: string
  plan: ReleasePlan
  items: { applicationId: string; state: string; runId?: string; reason?: string }[]
  createdAt: number
  updatedAt: number
}

const root = '/api/workspace'
const product = (id: string) => root + '/products/' + encodeURIComponent(id)
const batch = (id: string) => root + '/releases/' + encodeURIComponent(id)

export const getReleaseOptions = (id: string, page: number, options: HttpOptions) =>
  http.get<{ applications: ReleaseApplication[]; hasMore?: boolean }>(product(id) + '/release-options?' + new URLSearchParams({ page: String(page) }), options)

export const previewRelease = (id: string, items: ReleaseSelection[], options: HttpOptions) =>
  http.post<ReleasePlan>(product(id) + '/release-preview', { items }, options)

export const listProductReleases = (id: string, page: number, options: HttpOptions) =>
  http.get<{ items: ReleaseBatch[]; hasMore?: boolean }>(product(id) + '/releases?' + new URLSearchParams({ page: String(page) }), options)

export const confirmRelease = (requestId: string, plan: ReleasePlan, options: HttpOptions) =>
  http.post<ReleaseBatch>(product(plan.productId) + '/releases', { requestId, plan }, options)

export const advanceRelease = (id: string, expectedRevision: number, options: HttpOptions) =>
  http.post<ReleaseBatch>(batch(id) + '/advance', { expectedRevision }, options)

export const reconcileRelease = (id: string, expectedRevision: number, options: HttpOptions) =>
  http.post<ReleaseBatch>(batch(id) + '/reconcile', { expectedRevision }, options)
