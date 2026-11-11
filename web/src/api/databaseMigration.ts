import { http } from './http'
import { currentLocale } from '../i18n'
export interface ConnectionFields {
  driver: string
  dsn?: string
  host?: string
  port?: number
  database?: string
  schema?: string
  username?: string
  password?: string
  file?: string
}
export interface MigrationState {
  targetPermissions?: string
  info?: {
    driver: string
    version: string
    server?: string
    database?: string
    schema?: string
    file?: string
    identity?: string
  }
  completedTables: number
  totalTables: number
  checks?: string[]
  taskId?: string
  revision: number
  phase?: string
  current: string
  target?: string
  managed: boolean
  recovery: boolean
  blockers: { kind: string; count: number }[]
  blockersUnavailable: boolean
  report?: { tables: { table: string; rows: number; sha256: string }[] }
}
export class RecoveryError extends Error {
  constructor(
    public status: number,
    public code: string,
  ) {
    super(code)
  }
}
// Recovery deliberately bypasses the ordinary 401 redirect/session refresh.
export async function recoveryRequest<T>(
  path: string,
  body?: unknown,
  csrf?: string,
  signal?: AbortSignal,
): Promise<T> {
  const response = await fetch(`/api/database-recovery/${path}`, {
    method: body === undefined ? 'GET' : 'POST',
    credentials: 'same-origin',
    signal,
    headers: {
      'Content-Type': 'application/json',
      'X-Pipewright-Locale': currentLocale(),
      ...(csrf ? { 'X-CSRF-Token': csrf } : {}),
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  const data = await response.json()
  if (!response.ok)
    throw new RecoveryError(
      response.status,
      data.error?.code ?? 'database_migration_failed',
    )
  return data as T
}
export const databaseStatus = (signal?: AbortSignal) =>
  http.get<MigrationState>('/api/settings/database', {
    signal,
    throwOnUnauthorized: true,
  })
export const databasePreflight = (
  fields: ConnectionFields,
  managed: boolean,
  signal?: AbortSignal,
) =>
  http.post<MigrationState>(
    '/api/settings/database/preflight',
    { ...fields, managed },
    { signal, throwOnUnauthorized: true },
  )
export const databaseAction = (
  action: string,
  state: MigrationState,
  signal?: AbortSignal,
) =>
  http.post<MigrationState | { state: MigrationState; csrf: string }>(
    `/api/settings/database/${action}`,
    { taskId: state.taskId, revision: state.revision },
    { signal, throwOnUnauthorized: true },
  )
