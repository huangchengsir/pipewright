import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ReleaseGuardPlan from './ReleaseGuardPlan.vue'
import { guardPlan, guardReport } from '../../test/releaseGuardFixtures'
import { HttpError } from '../../api/http'

const api = vi.hoisted(() => ({ preview: vi.fn(), start: vi.fn() }))
vi.mock('../../api/releaseGuard', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/releaseGuard')>()),
  previewGuardPlan: api.preview,
  startReleaseGuard: api.start,
}))
function panel() {
  return mount(ReleaseGuardPlan, {
    props: { projectId: 'project-1', attemptId: 'attempt-1' },
  })
}
function startButton(wrapper: ReturnType<typeof panel>) {
  return wrapper
    .findAll('button')
    .find((button) => button.text() === '开始观察')!
}

describe('release guard plan review', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    api.preview.mockImplementation((_project, input) =>
      Promise.resolve(
        guardPlan({
          durationSeconds: input.durationSeconds,
          includeLogs: input.includeLogs,
        }),
      ),
    )
    api.start.mockResolvedValue(guardReport())
  })
  it('previews metadata only, discloses gaps, and starts only after explicit confirmation', async () => {
    const wrapper = panel()
    await flushPromises()
    expect(api.preview).toHaveBeenCalledWith(
      'project-1',
      {
        mode: 'from_now',
        attemptId: 'attempt-1',
        durationSeconds: 600,
        includeLogs: false,
      },
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    )
    expect(api.start).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('不覆盖发布结束至此刻')
    expect(wrapper.text()).toContain('未校验主机密钥')
    await startButton(wrapper).trigger('click')
    await flushPromises()
    expect(api.start).toHaveBeenCalledTimes(1)
    expect(api.start).toHaveBeenCalledWith(
      'project-1',
      {
        planId: 'plan-1',
        planHash: 'plan-hash',
        idempotencyKey: expect.any(String),
      },
      expect.any(Object),
    )
    expect(wrapper.emitted('started')).toHaveLength(1)
    wrapper.unmount()
  })
  it('reviews attached plans without creating an observation or deploying', async () => {
    api.preview.mockResolvedValue(
      guardPlan({ mode: 'attached', attemptId: '', artifactId: 'image-1' }),
    )
    const input = {
      runId: 'run-1',
      artifactId: 'image-1',
      serverIds: ['server-1'],
      deployConfig: { containerName: 'app' },
    }
    const wrapper = mount(ReleaseGuardPlan, {
      props: { projectId: 'project-1', attachedInput: input },
    })
    await flushPromises()
    expect(api.preview).toHaveBeenCalledWith(
      'project-1',
      { mode: 'attached', ...input, durationSeconds: 600, includeLogs: false },
      expect.any(Object),
    )
    const usePlan = wrapper
      .findAll('button')
      .find((button) => button.text() === '使用此计划')!
    expect(usePlan.attributes('disabled')).toBeUndefined()
    await usePlan.trigger('click')
    expect(wrapper.emitted('prepared')).toHaveLength(1)
    expect(api.start).not.toHaveBeenCalled()
    wrapper.unmount()
  })
  it('reviews a local build pending identity honestly while preserving the explicit attached confirmation', async () => {
    const plan = guardPlan({ mode: 'attached', attemptId: '', artifactId: 'archive-1' })
    plan.targets[0].containerId = ''
    plan.targets[0].expectedImage = { reference: '', repository: '', digest: '', digestKind: 'config_id', identitySource: 'script_pre_activation' }
    plan.targets[0].limitations = ['local_image_identity_pending_build']
    api.preview.mockResolvedValue(plan)
    const wrapper = mount(ReleaseGuardPlan, { props: { projectId: 'project-1', attachedInput: { runId: 'run-1', artifactId: 'archive-1', serverIds: ['server-1'], deployConfig: { localDocker: 'true', containerName: 'blogback' } } } })
    await flushPromises()
    expect(wrapper.text()).toContain('本地 Docker Image ID')
    expect(wrapper.text()).toContain('当前计划尚未含 Image ID')
    expect(wrapper.text()).not.toContain('registry.test/app@')
    const confirm = wrapper.findAll('button').find(button => button.text() === '使用此计划')!
    expect(confirm.attributes('disabled')).toBeUndefined()
    expect(api.start).not.toHaveBeenCalled()
    await confirm.trigger('click')
    expect(wrapper.emitted('prepared')).toHaveLength(1)
    wrapper.unmount()
  })
  it('keeps local log collection separate from model sharing and freezes an explicit opt-in', async () => {
    const wrapper = panel()
    await flushPromises()
    expect(
      (wrapper.get('.guard-log-choice input').element as HTMLInputElement)
        .checked,
    ).toBe(false)
    await wrapper.get('.guard-log-choice input').setValue(true)
    await flushPromises()
    expect(api.preview.mock.calls.at(-1)![1].includeLogs).toBe(true)
    expect(wrapper.text()).toContain('不代表同意向模型分享')
    expect(api.start).not.toHaveBeenCalled()
    expect(startButton(wrapper).attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })
  it('keeps an unsupported target visible and disables an expired preview', async () => {
    const plan = guardPlan({ expiresAt: '2000-01-01T00:00:00Z' })
    plan.targets.push({
      ...plan.targets[0],
      id: 'unbound',
      serverName: 'Offline target',
      availability: 'identity_unavailable',
      containerId: '',
      limitations: ['identity_unavailable'],
    })
    api.preview.mockResolvedValue(plan)
    const wrapper = panel()
    await flushPromises()
    expect(wrapper.text()).toContain('Offline target')
    expect(wrapper.text()).toContain('部分目标无法形成完整验证')
    expect(startButton(wrapper).attributes('disabled')).toBeDefined()
    expect(api.start).not.toHaveBeenCalled()
    wrapper.unmount()
  })
  it('does not automatically retry a start whose outcome is unknown', async () => {
    api.start.mockRejectedValue(new HttpError(0, null, 'connection lost'))
    const wrapper = panel()
    await flushPromises()
    await startButton(wrapper).trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('启动结果尚不确定')
    expect(startButton(wrapper).attributes('disabled')).toBeDefined()
    expect(wrapper.get('select').attributes('disabled')).toBeDefined()
    expect(api.start).toHaveBeenCalledTimes(1)
    expect(wrapper.emitted('refresh')).toHaveLength(1)
    wrapper.unmount()
  })
  it('aborts an obsolete preview and ignores late results after a duration change', async () => {
    let resolveOld!: (value: ReturnType<typeof guardPlan>) => void
    api.preview.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveOld = resolve
      }),
    )
    const wrapper = panel()
    await flushPromises()
    const firstRequest = api.preview.mock.calls[0][2] as { signal: AbortSignal }
    await wrapper.get('select').setValue('300')
    await flushPromises()
    expect(firstRequest.signal.aborted).toBe(true)
    resolveOld(guardPlan({ hash: 'stale-plan' }))
    await flushPromises()
    await startButton(wrapper).trigger('click')
    await flushPromises()
    expect(api.start.mock.calls[0][1].planHash).toBe('plan-hash')
    wrapper.unmount()
  })
})
