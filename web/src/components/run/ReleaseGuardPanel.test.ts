import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ReleaseGuardPanel from './ReleaseGuardPanel.vue'
import { deploymentAttempt } from '../../test/releaseGuardFixtures'

const api = vi.hoisted(() => ({ list: vi.fn(), push: vi.fn() }))
vi.mock('../../api/releaseGuard', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/releaseGuard')>()),
  listDeploymentAttempts: api.list,
}))
vi.mock('vue-router', () => ({ useRouter: () => ({ push: api.push }) }))
const options = {
  props: { runId: 'run-1', projectId: 'project-1', hasDeploymentTargets: true },
  global: {
    stubs: {
      RouterLink: {
        props: ['to'],
        template: '<a :data-route="JSON.stringify(to)"><slot /></a>',
      },
    },
  },
}

describe('release guard attempt panel', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    api.list.mockResolvedValue({ items: [] })
  })
  afterEach(() => {
    vi.useRealTimers()
  })
  it('keeps separate reports for two attempts from the same run', async () => {
    api.list.mockResolvedValue({
      items: [
        deploymentAttempt({
          guards: [
            {
              id: 'guard-first',
              executionStatus: 'completed',
              verificationResult: 'failed',
              createdAt: '2026-10-22T10:00:00Z',
            },
          ],
        }),
        deploymentAttempt({
          id: 'attempt-2',
          guards: [
            {
              id: 'guard-second',
              executionStatus: 'completed',
              verificationResult: 'passed',
              createdAt: '2026-10-22T11:00:00Z',
            },
          ],
        }),
      ],
    })
    const wrapper = mount(ReleaseGuardPanel, options)
    await flushPromises()
    expect(wrapper.findAll('[data-attempt-id]')).toHaveLength(2)
    expect(
      wrapper.get('[data-attempt-id="attempt-1"] a').attributes('data-route'),
    ).toContain('guard-first')
    expect(
      wrapper.get('[data-attempt-id="attempt-2"] a').attributes('data-route'),
    ).toContain('guard-second')
    expect(wrapper.text()).toContain('基础运行检查通过')
    wrapper.unmount()
  })
  it('does not enable observation for legacy or rolled-back identities', async () => {
    const attempt = deploymentAttempt()
    attempt.targets[0].receipts[0].kind = 'rolled_back'
    api.list.mockResolvedValue({ items: [attempt] })
    const wrapper = mount(ReleaseGuardPanel, options)
    await flushPromises()
    const button = wrapper
      .findAll('button')
      .find((button) => button.text() === '从现在观察')!
    expect(button.attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('缺少可绑定实际容器')
    wrapper.unmount()
  })
  it('never puts an attempt into another environment by matching its run', async () => {
    api.list.mockResolvedValue({ items: [deploymentAttempt()] })
    const wrapper = mount(ReleaseGuardPanel, {
      ...options,
      props: { ...options.props, environment: 'staging' },
    })
    await flushPromises()
    expect(wrapper.findAll('[data-attempt-id]')).toHaveLength(0)
    wrapper.unmount()
  })
  it('polls saved observations only and cancels polling on unmount', async () => {
    vi.useFakeTimers()
    api.list.mockResolvedValue({
      items: [
        deploymentAttempt({
          guards: [
            {
              id: 'g',
              executionStatus: 'observing',
              verificationResult: null,
              createdAt: '2026-10-22T10:00:00Z',
            },
          ],
        }),
      ],
    })
    const wrapper = mount(ReleaseGuardPanel, options)
    await flushPromises()
    await vi.advanceTimersByTimeAsync(4000)
    expect(api.list).toHaveBeenCalledTimes(2)
    const request = api.list.mock.calls.at(-1)![1] as { signal: AbortSignal }
    wrapper.unmount()
    expect(request.signal.aborted).toBe(true)
    await vi.advanceTimersByTimeAsync(12000)
    expect(api.list).toHaveBeenCalledTimes(2)
  })
})
