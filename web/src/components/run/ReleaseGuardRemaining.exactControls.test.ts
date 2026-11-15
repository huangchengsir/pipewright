import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { useSessionStore } from '../../stores/session'
import { usePermissionStore } from '../../stores/permissions'
import { access } from '../../api/access'
import {
  investigateReleaseGuard, listDeploymentAttempts, listGuardAnalyses,
  previewGuardAnalysis, previewGuardPlan, sendGuardAnalysis, startReleaseGuard,
  type DeploymentAttempt, type GuardAnalysis, type GuardReport,
} from '../../api/releaseGuard'
import { deploymentAttempt, guardPlan, guardReport } from '../../test/releaseGuardFixtures'
import { t } from '../../i18n'
import ReleaseGuardAnalysis from './ReleaseGuardAnalysis.vue'
import ReleaseGuardInvestigation from './ReleaseGuardInvestigation.vue'
import ReleaseGuardPanel from './ReleaseGuardPanel.vue'
import ReleaseGuardPlan from './ReleaseGuardPlan.vue'

vi.mock('../../api/access', () => ({ access: { decisions: vi.fn() } }))
vi.mock('../../api/releaseGuard', async original => ({
  ...await original<typeof import('../../api/releaseGuard')>(),
  investigateReleaseGuard: vi.fn(), listDeploymentAttempts: vi.fn(), listGuardAnalyses: vi.fn(),
  previewGuardAnalysis: vi.fn(), previewGuardPlan: vi.fn(), sendGuardAnalysis: vi.fn(), startReleaseGuard: vi.fn(),
}))
const route = vi.hoisted(() => ({ push: vi.fn() }))
vi.mock('vue-router', () => ({ useRouter: () => ({ push: route.push }) }))
let pinia: ReturnType<typeof createPinia>
let wrapper: VueWrapper | undefined
let grants: Set<string>
const stubs = { RouterLink: true }
async function settle() { for (let i=0; i<4; i++) await flushPromises() }
beforeEach(() => {
  vi.resetAllMocks()
  pinia = createPinia()
  setActivePinia(pinia)
  useSessionStore().setUser({ userId: 'employee', username: 'employee', multiTenant: true })
  usePermissionStore().setTeam('team-a')
  grants = new Set(['guard.read/run-1'])
  vi.mocked(access.decisions).mockImplementation(async (team, queries) => ({
    userId: 'employee', teamId: team, modeEpoch: 1, revision: 1, catalogVersion: 1,
    allowed: queries.map(q => team === 'team-a' && grants.has(q.action + '/' + q.resourceId)),
  }))
  vi.mocked(listGuardAnalyses).mockResolvedValue({ items: [] })
  vi.mocked(listDeploymentAttempts).mockResolvedValue({ items: [] })
  vi.mocked(previewGuardPlan).mockResolvedValue(guardPlan())
})
afterEach(() => { wrapper?.unmount(); wrapper = undefined; vi.unstubAllGlobals() })

it('opens the exact counter-evidence citation independently of support and never sends data', async () => {
  const report = guardReport({ evidenceSetHash: 'evidence-hash' })
  const historical: GuardAnalysis = {
    id: 'fixture-analysis', guardId: report.id, status: 'completed',
    provider: { name: 'fixture', model: 'fixture', destination: 'https://model.invalid', configHash: 'hash' },
    input: { includeRuntime: true, includeLogs: false }, payload: '{}', payloadHash: 'payload-hash',
    planHash: 'plan-hash', evidenceSetHash: 'evidence-hash', evidenceIds: ['support-id','counter-id'],
    createdAt: '2026-10-22T10:12:00Z', expiresAt: '2099-01-01T00:00:00Z',
    revoked: false, stale: false, reason: '', limitations: [],
    result: { summary: 'Fixture summary', candidates: [{
      id: 'candidate', title: 'Fixture candidate', supportingEvidenceIds: ['support-id'],
      counterEvidenceIds: ['counter-id'], missingEvidence: [], nextReadOnlyCheck: '',
    }] },
  }
  vi.mocked(listGuardAnalyses).mockResolvedValueOnce({ items: [historical] })
  wrapper = mount(ReleaseGuardAnalysis, { props: { report }, global: { plugins: [pinia], stubs } })
  await settle()
  const counter = wrapper.findAll('.guard-analysis-result a')
    .find(link => link.attributes('aria-label')?.includes(t('releaseGuard.counterEvidence')))!
  expect(counter.attributes('href')).toBe('#guard-evidence-section')
  const click = new MouseEvent('click', { bubbles: true, cancelable: true })
  counter.element.dispatchEvent(click)
  expect(click.defaultPrevented).toBe(true)
  expect(wrapper.emitted('locateEvidence')).toEqual([['counter-id']])
  expect(previewGuardAnalysis).not.toHaveBeenCalled()
  expect(sendGuardAnalysis).not.toHaveBeenCalled()
  expect(listGuardAnalyses).toHaveBeenCalledOnce()
})

it('opens the baseline evidence for the current report without updating the investigation', async () => {
  const report = guardReport()
  report.investigation.baselines = [{
    targetId: 'target-1', status: 'available', evidenceId: 'current-baseline', priorAttemptId: 'older-attempt',
    priorRunId: 'older-run', ageSeconds: 60, missingEvidence: [],
  }, {
    targetId: 'unavailable-target', status: 'unavailable', evidenceId: '', priorAttemptId: '',
    priorRunId: '', ageSeconds: 0, missingEvidence: ['fixture-missing'],
  }]
  wrapper = mount(ReleaseGuardInvestigation, { props: { report }, global: { plugins: [pinia], stubs } })
  await settle()
  expect(wrapper.findAll('.guard-baseline')).toHaveLength(2)
  expect(wrapper.findAll('.guard-baseline a')).toHaveLength(1)
  expect(wrapper.find('.guard-investigation-update__action button').exists()).toBe(false)
  const click = new MouseEvent('click', { bubbles: true, cancelable: true })
  wrapper.get('.guard-baseline a').element.dispatchEvent(click)
  expect(click.defaultPrevented).toBe(true)
  expect(wrapper.emitted('locateEvidence')).toEqual([['current-baseline']])
  expect(investigateReleaseGuard).not.toHaveBeenCalled()
  expect(route.push).not.toHaveBeenCalled()
})

it('refreshes only the captured run in its team and invalidates a retained refresh after read revocation', async () => {
  wrapper = mount(ReleaseGuardPanel, {
    props: { runId: 'run-1', projectId: 'project-1', hasDeploymentTargets: true },
    global: { plugins: [pinia], stubs },
  })
  await settle()
  const api = vi.mocked(listDeploymentAttempts)
  expect(api).toHaveBeenCalledExactlyOnceWith('run-1', expect.objectContaining({
    headers: { 'X-Pipewright-Team': 'team-a' }, signal: expect.any(AbortSignal),
  }))
  let finish!: (value: { items: DeploymentAttempt[] }) => void
  api.mockReturnValueOnce(new Promise(resolve => { finish = resolve }))
  const button = wrapper.get('.guard-panel__head button')
  await button.trigger('click')
  await settle()
  expect(api).toHaveBeenNthCalledWith(2, 'run-1', expect.objectContaining({
    headers: { 'X-Pipewright-Team': 'team-a' }, signal: expect.any(AbortSignal),
  }))
  const signal = api.mock.calls[1]![1]!.signal!
  usePermissionStore().reject('team-a', { action: 'guard.read', resourceId: 'run-1' })
  button.element.dispatchEvent(new MouseEvent('click', { bubbles: true }))
  finish({ items: [deploymentAttempt()] })
  await settle()
  expect(signal.aborted).toBe(true)
  expect(api).toHaveBeenCalledTimes(2)
  expect(wrapper.find('.guard-panel').exists()).toBe(false)
  expect(previewGuardPlan).not.toHaveBeenCalled()
})

it('closes only the current plan from the header without starting or preparing another plan', async () => {
  grants.add('guard.observe/run-1')
  wrapper = mount(ReleaseGuardPlan, {
    props: { runId: 'run-1', projectId: 'project-1', attemptId: 'attempt-1' },
    global: { plugins: [pinia] }, attachTo: document.body,
  })
  await settle()
  const close = document.querySelector<HTMLButtonElement>('.guard-plan__header button')!
  expect(close.textContent).toContain(t('releaseGuard.close'))
  close.click()
  expect(wrapper.emitted('close')).toEqual([[]])
  expect(previewGuardPlan).toHaveBeenCalledOnce()
  expect(startReleaseGuard).not.toHaveBeenCalled()
  expect(wrapper.emitted('prepared')).toBeUndefined()
})

it('keeps the plan header close inert while the one start request settles', async () => {
  grants.add('guard.observe/run-1')
  let finish!: (value: GuardReport) => void
  vi.mocked(startReleaseGuard).mockReturnValueOnce(new Promise(resolve => { finish = resolve }))
  wrapper = mount(ReleaseGuardPlan, {
    props: { runId: 'run-1', projectId: 'project-1', attemptId: 'attempt-1' },
    global: { plugins: [pinia] }, attachTo: document.body,
  })
  await settle()
  const button = [...document.querySelectorAll<HTMLButtonElement>('.guard-plan button')]
    .find(item => item.textContent?.trim() === t('releaseGuard.startObservation'))!
  expect(button).toBeDefined()
  button.click()
  await settle()
  const close = document.querySelector<HTMLButtonElement>('.guard-plan__header button')!
  expect(close.disabled).toBe(true)
  close.dispatchEvent(new MouseEvent('click', { bubbles: true }))
  expect(wrapper.emitted('close')).toBeUndefined()
  expect(startReleaseGuard).toHaveBeenCalledOnce()
  finish(guardReport())
  await settle()
  expect(wrapper.emitted('started')).toHaveLength(1)
  expect(wrapper.emitted('close')).toBeUndefined()
})
