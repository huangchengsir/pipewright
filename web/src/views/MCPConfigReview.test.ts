import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { reactive } from 'vue'
import MCPConfigReview from './MCPConfigReview.vue'
import AppButton from '../components/ui/AppButton.vue'
import { HttpError } from '../api/http'
import type { MCPConfigApproval, MCPConfigReview as Review } from '../api/mcpConfig'

const mocks = vi.hoisted(() => ({ get: vi.fn(), approve: vi.fn(), route: { params: { id: 'draft-1' } } }))
vi.mock('vue-router', () => ({ useRoute: () => mocks.route }))
vi.mock('../api/mcpConfig', () => ({ getMCPConfigReview: mocks.get, approveMCPConfigDraft: mocks.approve }))
const wrappers: VueWrapper[] = []
const approval: MCPConfigApproval = { id: 'approval-1', state: 'granted', expiresAt: '2099-01-01T00:00:00Z', eligible: true }
function fixture(id = 'draft-1'): Review {
  return { draftId: id, projectId: 'project-1', baseRevision: 'revision-1', policyVersion: 'policy-1', reviewDigest: 'digest-1', state: 'active', expiresAt: '2099-01-01T00:00:00Z', diff: [{ type: 'project.set_name', field: 'name', before: 'Original', after: 'Updated', impact: 'identity' }], validation: { state: 'passed', applyAllowed: true, checks: [{ category: 'structural', status: 'passed' }], issues: [] }, approval: { state: 'none', eligible: false }, reviewUrl: `/mcp/config-drafts/${id}/review`, complete: true }
}
function deferred<T>() { let resolve!: (value: T) => void; let reject!: (error: Error) => void; const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no }); return { promise, resolve, reject } }
function render() { const wrapper = mount(MCPConfigReview); wrappers.push(wrapper); return wrapper }
function approveButton(wrapper: VueWrapper) { return wrapper.get('.review-approve-button') }
async function acknowledge(wrapper: VueWrapper) { await wrapper.get('.review-ack input').setValue(true) }

describe('MCP configuration review', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    mocks.route = reactive({ params: { id: 'draft-1' } })
    mocks.get.mockResolvedValue(fixture())
    mocks.approve.mockResolvedValue(approval)
  })
  afterEach(() => { for (const wrapper of wrappers.splice(0)) wrapper.unmount(); vi.useRealTimers() })

  it('loads the full safe diff without approving and requires human acknowledgement', async () => {
    const wrapper = render(); await flushPromises()
    expect(mocks.get).toHaveBeenCalledWith('draft-1', { signal: expect.any(AbortSignal) })
    expect(wrapper.text()).toContain('Original')
    expect(wrapper.text()).toContain('Updated')
    expect(wrapper.text()).toContain('已有定时任务')
    expect(wrapper.text()).toContain('脚本、环境值、凭据')
    expect(approveButton(wrapper).attributes('disabled')).toBeDefined()
    expect(mocks.approve).not.toHaveBeenCalled()
    await acknowledge(wrapper)
    expect(approveButton(wrapper).attributes('disabled')).toBeUndefined()
  })
  it('approves only the shown immutable binding and never applies configuration', async () => {
    const wrapper = render(); await flushPromises(); await acknowledge(wrapper)
    await approveButton(wrapper).trigger('click'); await flushPromises()
    expect(mocks.approve).toHaveBeenCalledExactlyOnceWith('draft-1', { reviewDigest: 'digest-1', baseRevision: 'revision-1', policyVersion: 'policy-1' }, { signal: expect.any(AbortSignal) })
    expect(wrapper.text()).toContain('请回到客户端继续')
    expect(approveButton(wrapper).attributes('disabled')).toBeDefined()
    expect(wrapper.findAll('button').map(button => button.text()).some(text => text.includes('应用'))).toBe(false)
  })
  it.each(['incomplete', 'over_budget', 'unknown_field', 'unknown_type', 'object_value', 'mismatched_id', 'no_digest'])('blocks approval of %s review content', async (kind) => {
    const item = fixture()
    if (kind === 'incomplete') item.complete = false
    if (kind === 'over_budget') item.diff = Array.from({ length: 33 }, () => ({ ...item.diff[0] }))
    if (kind === 'unknown_field') item.diff[0].field = 'commands'
    if (kind === 'unknown_type') item.diff[0].type = 'exec'
    if (kind === 'object_value') item.diff[0].after = { secret: 'must-not-display' }
    if (kind === 'mismatched_id') item.draftId = 'another-draft'
    if (kind === 'no_digest') item.reviewDigest = ''
    mocks.get.mockResolvedValue(item)
    const wrapper = render(); await flushPromises()
    wrapper.getComponent(AppButton).vm.$emit('click', new MouseEvent('click'))
    await flushPromises()
    expect(approveButton(wrapper).attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('完整审阅不可用')
    expect(wrapper.text()).not.toContain('must-not-display')
    expect(mocks.approve).not.toHaveBeenCalled()
  })
  it.each(['expired', 'invalidated', 'blocked', 'unknown'])('does not approve %s drafts', async (kind) => {
    const item = fixture()
    if (kind === 'expired') item.expiresAt = '2020-01-01T00:00:00Z'
    else if (kind === 'invalidated') item.state = 'invalidated'
    else { item.validation.state = kind; item.validation.applyAllowed = false }
    mocks.get.mockResolvedValue(item)
    const wrapper = render(); await flushPromises(); await acknowledge(wrapper)
    expect(approveButton(wrapper).attributes('disabled')).toBeDefined()
    expect(mocks.approve).not.toHaveBeenCalled()
  })
  it.each([false, true])('disables approval while apply is off even if applyAllowed is %s', async (applyAllowed) => {
    const item = fixture()
    item.validation.applyAllowed = applyAllowed
    item.approval = { state: 'none', eligible: false, reason: 'apply_disabled' }
    mocks.get.mockResolvedValue(item)
    const wrapper = render(); await flushPromises(); await acknowledge(wrapper)
    expect(wrapper.text()).toContain('配置应用当前未启用')
    expect(approveButton(wrapper).attributes('disabled')).toBeDefined()
    expect(mocks.approve).not.toHaveBeenCalled()
  })
  it('disables approval when the visible draft expires without a reload', async () => {
    vi.useFakeTimers(); vi.setSystemTime('2026-10-22T00:00:00Z')
    mocks.get.mockResolvedValue({ ...fixture(), expiresAt: '2026-10-22T00:00:02Z' })
    const wrapper = render(); await flushPromises(); await acknowledge(wrapper)
    expect(approveButton(wrapper).attributes('disabled')).toBeUndefined()
    await vi.advanceTimersByTimeAsync(2000)
    expect(approveButton(wrapper).attributes('disabled')).toBeDefined()
  })
  it('escapes untrusted text instead of rendering markup', async () => {
    const item = fixture(); item.diff[0].after = '<img src=x onerror=alert(1)>'
    mocks.get.mockResolvedValue(item)
    const wrapper = render(); await flushPromises()
    expect(wrapper.find('img').exists()).toBe(false)
    expect(wrapper.text()).toContain('<img src=x onerror=alert(1)>')
  })
  it('serializes approval and blocks refresh until its result is known', async () => {
    const result = deferred<MCPConfigApproval>(); mocks.approve.mockReturnValue(result.promise)
    const wrapper = render(); await flushPromises(); await acknowledge(wrapper)
    const button = wrapper.findAllComponents(AppButton).find(item => item.classes().includes('review-approve-button'))!
    button.vm.$emit('click', new MouseEvent('click')); button.vm.$emit('click', new MouseEvent('click'))
    await flushPromises()
    expect(mocks.approve).toHaveBeenCalledTimes(1)
    expect(wrapper.get('.review-header button').attributes('disabled')).toBeDefined()
    result.resolve(approval); await flushPromises()
  })
  it.each([0, 502])('does not retry uncertain approval response %s and refreshes its recorded status', async (status) => {
    mocks.approve.mockRejectedValue(new HttpError(status, null, 'sensitive transport error'))
    const wrapper = render(); await flushPromises(); await acknowledge(wrapper)
    await approveButton(wrapper).trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('批准响应丢失')
    expect(wrapper.text()).not.toContain('sensitive transport error')
    expect(approveButton(wrapper).attributes('disabled')).toBeDefined()
    mocks.get.mockResolvedValue({ ...fixture(), approval })
    await wrapper.get('.review-header button').trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('请回到客户端继续')
    expect(mocks.approve).toHaveBeenCalledTimes(1)
  })
  it('requires fresh review after a conflict and does not echo backend messages', async () => {
    mocks.approve.mockRejectedValue(new HttpError(409, { code: 'revision_conflict', message: 'secret SQL' }, 'secret SQL'))
    const wrapper = render(); await flushPromises(); await acknowledge(wrapper)
    await approveButton(wrapper).trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('读取新基线')
    expect(wrapper.text()).not.toContain('secret SQL')
    expect(wrapper.find('.review-approve-button').exists()).toBe(false)
  })
  it('ignores a stale read and cancels it when navigating to another draft', async () => {
    const stale = deferred<Review>(); mocks.get.mockReturnValueOnce(stale.promise)
    const wrapper = render(); await flushPromises()
    const oldSignal = mocks.get.mock.calls[0][1].signal
    mocks.get.mockResolvedValueOnce(fixture('draft-2'))
    mocks.route.params.id = 'draft-2'; await flushPromises()
    expect(oldSignal.aborted).toBe(true)
    stale.resolve(fixture()); await flushPromises()
    expect(wrapper.text()).toContain('draft-2')
    expect(wrapper.text()).not.toContain('draft-1')
  })
  it('ignores a late approval after navigating away from its draft', async () => {
    const old = deferred<MCPConfigApproval>(); mocks.approve.mockReturnValue(old.promise)
    const wrapper = render(); await flushPromises(); await acknowledge(wrapper)
    await approveButton(wrapper).trigger('click')
    mocks.get.mockResolvedValueOnce(fixture('draft-2'))
    mocks.route.params.id = 'draft-2'; await flushPromises()
    old.resolve(approval); await flushPromises()
    expect(wrapper.text()).toContain('draft-2')
    expect(wrapper.text()).not.toContain('请回到客户端继续')
  })
})
