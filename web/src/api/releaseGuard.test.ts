import { describe, expect, it, vi } from 'vitest'
import {
  attemptHasIdentity,
  getReleaseGuard,
  listGuardEvidence,
  previewGuardPlan,
} from './releaseGuard'
import { deploymentAttempt } from '../test/releaseGuardFixtures'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('./http', () => ({ http: api }))

describe('release guard client boundaries', () => {
  it('requires a full activated container identity, never an outcome label or rollback receipt', () => {
    const attempt = deploymentAttempt()
    expect(attemptHasIdentity(attempt)).toBe(true)
    attempt.targets[0].receipts[0].kind = 'rolled_back'
    expect(attemptHasIdentity(attempt)).toBe(false)
    attempt.targets[0].receipts[0].kind = 'activated'
    attempt.targets[0].receipts[0].actualImage.containerId = 'short-container'
    expect(attemptHasIdentity(attempt)).toBe(false)
    attempt.targets[0].receipts = []
    expect(attemptHasIdentity(attempt)).toBe(false)
  })
  it('encodes project/report identities and passes abort signals to read and write calls', () => {
    const controller = new AbortController()
    getReleaseGuard('project/one', 'guard/two', { signal: controller.signal })
    expect(api.get).toHaveBeenCalledWith(
      '/api/projects/project%2Fone/release-guards/guard%2Ftwo',
      { signal: controller.signal },
    )
    listGuardEvidence('p', 'g', 15, { signal: controller.signal })
    expect(api.get).toHaveBeenLastCalledWith(
      '/api/projects/p/release-guards/g/evidence?after=15&limit=100',
      { signal: controller.signal },
    )
    const input = {
      mode: 'from_now' as const,
      attemptId: 'a',
      durationSeconds: 600 as const,
      includeLogs: false as const,
    }
    previewGuardPlan('p', input, { signal: controller.signal })
    expect(api.post).toHaveBeenCalledWith(
      '/api/projects/p/release-guard-plans',
      input,
      { signal: controller.signal },
    )
  })
  it('enables local receipts only with a full image identity frozen before activation', () => {
    const attempt = deploymentAttempt()
    attempt.targets[0].expectedImage = { reference: '', repository: '', digest: `sha256:${'b'.repeat(64)}`, digestKind: 'config_id', identitySource: 'script_pre_activation' }
    expect(attemptHasIdentity(attempt)).toBe(true)
    delete attempt.targets[0].expectedImage.identitySource
    expect(attemptHasIdentity(attempt)).toBe(false)
    attempt.targets[0].expectedImage.identitySource = 'script_pre_activation'
    attempt.targets[0].expectedImage.digest = 'sha256:short'
    expect(attemptHasIdentity(attempt)).toBe(false)
  })
})
