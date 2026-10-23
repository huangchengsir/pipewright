import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ReleaseGuardAnalysis from './ReleaseGuardAnalysis.vue'
import { guardReport } from '../../test/releaseGuardFixtures'
import type { GuardAnalysis } from '../../api/releaseGuard'
import { HttpError } from '../../api/http'

const api = vi.hoisted(() => ({
  list: vi.fn(),
  preview: vi.fn(),
  send: vi.fn(),
  revoke: vi.fn(),
}))
vi.mock('../../api/releaseGuard', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/releaseGuard')>()),
  listGuardAnalyses: api.list,
  previewGuardAnalysis: api.preview,
  sendGuardAnalysis: api.send,
  revokeGuardAnalysis: api.revoke,
}))
function analysis(overrides: Partial<GuardAnalysis> = {}): GuardAnalysis {
  return {
    id: 'analysis-1',
    guardId: 'guard-1',
    status: 'preview',
    provider: {
      name: 'Private model',
      model: 'configured-model',
      destination: 'https://model.example.test',
      configHash: 'provider-hash',
    },
    input: { includeRuntime: true, includeLogs: false },
    payload: '{"items":[{"target":"target-1","running":false}]}',
    payloadHash: 'payload-hash',
    planHash: 'plan-hash',
    evidenceSetHash: 'evidence-hash',
    evidenceIds: ['evidence-1'],
    createdAt: '2026-10-22T10:11:00Z',
    expiresAt: '2099-01-01T00:00:00Z',
    revoked: false,
    stale: false,
    reason: '',
    limitations: [],
    ...overrides,
  }
}
function panel() {
  return mount(ReleaseGuardAnalysis, {
    props: { report: guardReport({ evidenceSetHash: 'evidence-hash' }) },
    global: {
      stubs: {
        RouterLink: {
          props: ['to'],
          template: '<a :data-route="JSON.stringify(to)"><slot /></a>',
        },
      },
    },
  })
}
function button(wrapper: ReturnType<typeof panel>, text: string) {
  return wrapper.findAll('button').find((item) => item.text() === text)!
}

describe('one-snapshot AI sharing', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    api.list.mockResolvedValue({ items: [] })
    api.preview.mockResolvedValue(analysis())
    api.send.mockResolvedValue(
      analysis({
        status: 'completed',
        result: { summary: 'Possible association', candidates: [] },
      }),
    )
    api.revoke.mockResolvedValue(analysis({ status: 'revoked', revoked: true }))
  })
  it('keeps sharing off and requires scope, preview and a separate one-request confirmation', async () => {
    const wrapper = panel()
    await flushPromises()
    expect(
      wrapper
        .findAll('input[type="checkbox"]')
        .every((input) => !(input.element as HTMLInputElement).checked),
    ).toBe(true)
    expect(api.preview).not.toHaveBeenCalled()
    expect(api.send).not.toHaveBeenCalled()
    expect(button(wrapper, '预览本次快照').attributes('disabled')).toBeDefined()
    await wrapper.findAll('input[type="checkbox"]')[0].setValue(true)
    await button(wrapper, '预览本次快照').trigger('click')
    await flushPromises()
    expect(api.preview).toHaveBeenCalledWith(
      'project-1',
      'guard-1',
      { includeRuntime: true, includeLogs: false },
      expect.any(Object),
    )
    expect(wrapper.text()).toContain('https://model.example.test')
    expect(wrapper.get('pre').text()).toContain('"running":false')
    expect(
      button(wrapper, '仅发送这份快照').attributes('disabled'),
    ).toBeDefined()
    await wrapper.get('.guard-analysis-confirm input').setValue(true)
    await button(wrapper, '仅发送这份快照').trigger('click')
    await flushPromises()
    expect(api.send).toHaveBeenCalledWith(
      'project-1',
      'guard-1',
      'analysis-1',
      {
        payloadHash: 'payload-hash',
        providerConfigHash: 'provider-hash',
        confirmed: true,
      },
      expect.any(Object),
    )
    expect(api.send).toHaveBeenCalledTimes(1)
    expect(wrapper.text()).toContain('Possible association')
    wrapper.unmount()
  })
  it('blocks new snapshot IDs after an unknown send and never automatically retries', async () => {
    api.send.mockRejectedValue(new HttpError(0, null, 'lost'))
    const wrapper = panel()
    await flushPromises()
    await wrapper.findAll('input')[0].setValue(true)
    await button(wrapper, '预览本次快照').trigger('click')
    await flushPromises()
    await wrapper.get('.guard-analysis-confirm input').setValue(true)
    await button(wrapper, '仅发送这份快照').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('发送结果未知')
    expect(button(wrapper, '预览本次快照').attributes('disabled')).toBeDefined()
    expect(api.preview).toHaveBeenCalledTimes(1)
    expect(api.send).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })
  it('allows withdrawing an in-flight send and preserves the unknown outcome', async () => {
    let finish!: (item: GuardAnalysis) => void
    api.send.mockReturnValue(
      new Promise((resolve) => {
        finish = resolve
      }),
    )
    api.revoke.mockResolvedValue(analysis({ status: 'sending', revoked: true }))
    const wrapper = panel()
    await flushPromises()
    await wrapper.findAll('input')[0].setValue(true)
    await button(wrapper, '预览本次快照').trigger('click')
    await flushPromises()
    await wrapper.get('.guard-analysis-confirm input').setValue(true)
    await button(wrapper, '仅发送这份快照').trigger('click')
    expect(
      button(wrapper, '撤回后续发送').attributes('disabled'),
    ).toBeUndefined()
    await button(wrapper, '撤回后续发送').trigger('click')
    await flushPromises()
    expect(api.revoke).toHaveBeenCalledTimes(1)
    finish(analysis({ status: 'unknown', revoked: true }))
    await flushPromises()
    expect(wrapper.text()).toContain('已撤回后续发送')
    expect(wrapper.text()).toContain('发送结果未知')
    wrapper.unmount()
  })
  it('links to existing AI settings when no configured provider is available', async () => {
    api.preview.mockRejectedValue(new HttpError(503, null, 'unavailable'))
    const wrapper = panel()
    await flushPromises()
    await wrapper.findAll('input')[0].setValue(true)
    await button(wrapper, '预览本次快照').trigger('click')
    await flushPromises()
    expect(wrapper.get('a').attributes('data-route')).toContain('settings-ai')
    expect(api.send).not.toHaveBeenCalled()
    wrapper.unmount()
  })
  it('requires another confirmation after a scope change and clears expired evidence snapshots', async () => {
    const wrapper = panel()
    await flushPromises()
    await wrapper.findAll('input')[0].setValue(true)
    await button(wrapper, '预览本次快照').trigger('click')
    await flushPromises()
    await wrapper.get('.guard-analysis-confirm input').setValue(true)
    await wrapper.findAll('.guard-analysis-scope input')[1].setValue(true)
    expect(
      button(wrapper, '仅发送这份快照').attributes('disabled'),
    ).toBeDefined()
    expect(api.send).not.toHaveBeenCalled()
    await wrapper.setProps({ report: guardReport({ evidenceExpired: true }) })
    expect(wrapper.findAll('pre')).toHaveLength(0)
    expect(button(wrapper, '预览本次快照').attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })
})
