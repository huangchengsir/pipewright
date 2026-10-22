/** A revision belongs to the DTO that was actually read, never a global latest cache. */
const revisionKey = Symbol('pipewright-config-revision')

export function bindConfigRevision<T>(value: T, revision?: string | null): T {
  if (value !== null && typeof value === 'object' && revision && /^cfg-[0-9]+$/.test(revision)) {
    Object.defineProperty(value, revisionKey, { value: revision, configurable: true, enumerable: false })
  }
  return value
}

export function configRevisionOf(value: unknown): string | undefined {
  if (!value || typeof value !== 'object') return undefined
  const object = value as { [revisionKey]?: string; configRevision?: unknown }
  const revision = object[revisionKey] ?? object.configRevision
  return typeof revision === 'string' && /^cfg-[0-9]+$/.test(revision) ? revision : undefined
}

export function configWriteOptions(revision?: string): RequestInit | undefined {
  return revision ? { headers: { 'If-Match': revision } } : undefined
}
