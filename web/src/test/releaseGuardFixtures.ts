import type {
  DeploymentAttempt,
  GuardPlan,
  GuardReport,
} from '../api/releaseGuard'

export const containerId = 'a'.repeat(64)
export const image = {
  reference: 'registry.test/app@sha256:123',
  repository: 'registry.test/app',
  digest: 'sha256:123',
  digestKind: 'manifest',
}
export function guardPlan(overrides: Partial<GuardPlan> = {}): GuardPlan {
  return {
    id: 'plan-1',
    hash: 'plan-hash',
    expiresAt: '2099-01-01T00:00:00Z',
    mode: 'from_now',
    projectId: 'project-1',
    runId: 'run-1',
    attemptId: 'attempt-1',
    durationSeconds: 600,
    includeLogs: false,
    targets: [
      {
        id: 'target-1',
        serverId: 'server-1',
        serverName: 'Web 1',
        containerName: 'app',
        containerId,
        expectedImage: image,
        availability: 'available',
        limitations: [],
      },
    ],
    limitations: [],
    ...overrides,
  }
}
export function guardReport(overrides: Partial<GuardReport> = {}): GuardReport {
  return {
    id: 'guard-1',
    projectId: 'project-1',
    runId: 'run-1',
    attemptId: 'attempt-1',
    plan: guardPlan(),
    executionStatus: 'completed',
    verificationResult: 'failed',
    attributionStatus: 'undetermined',
    createdAt: '2026-10-22T10:00:00Z',
    startedAt: '2026-10-22T10:00:00Z',
    finishedAt: '2026-10-22T10:10:00Z',
    reason: 'A required condition failed',
    identityTrust: 'host_identity_unverified',
    targets: [
      {
        id: 'target-1',
        serverId: 'server-1',
        serverName: 'Web 1',
        containerId,
        verificationResult: 'failed',
        coverage: {
          expectedSamples: 21,
          successfulSamples: 8,
          maxGapSeconds: 90,
          complete: false,
        },
        conditions: [
          {
            name: 'liveness',
            result: 'failed',
            reason: 'Container exited',
            evidenceIds: ['evidence-1'],
          },
        ],
        limitations: [],
      },
    ],
    timeline: [],
    investigation: {
      baselineStatus: 'unavailable',
      candidates: [],
      limitations: [],
    },
    eventsCursor: 1,
    evidenceExpiresAt: '2099-01-01T00:00:00Z',
    evidenceExpired: false,
    comparison: { sameConditions: true, changes: [] },
    ...overrides,
  }
}
export function deploymentAttempt(
  overrides: Partial<DeploymentAttempt> = {},
): DeploymentAttempt {
  return {
    id: 'attempt-1',
    source: {
      projectId: 'project-1',
      runId: 'run-1',
      environment: 'prod',
      stageId: 'deploy',
    },
    createdAt: '2026-10-22T10:00:00Z',
    targets: [
      {
        id: 'target-1',
        serverId: 'server-1',
        serverName: 'Web 1',
        containerName: 'app',
        expectedImage: image,
        outcome: 'succeeded',
        receipts: [
          {
            kind: 'activated',
            at: '2026-10-22T10:00:00Z',
            actualImage: { containerId },
          },
        ],
      },
    ],
    guards: [],
    ...overrides,
  }
}
