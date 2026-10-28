import { afterEach, describe, expect, it, vi } from 'vitest'
import { computed, ref } from 'vue'
import { flushPromises, mount, RouterLinkStub } from '@vue/test-utils'
import RunRetryPanel from './RunRetryPanel.vue'
import type { useRunRetry } from '../../composables/useRunRetry'
import { retryContext, retryNode, retryPlan, retryRequest } from '../../test/runRetryFixtures'
import { i18n, setLocale } from '../../i18n'
function state() {
  const context = ref(retryContext()); const plan = ref(retryPlan({ rebuildGroups: [{ nodeIds: ['a'], consumerIds: ['b'], reasonCode: 'workspace_rebuild_required' }], warnings: [{ code: 'side_effect_reexecute', nodeKey: 'c' }] }))
  const value = { context, plan, contextError: ref(''), error: ref(''), planLoading: ref(false), submitting: ref(false), querying: ref(false), reconciling: ref(false), pending: ref(null), request: ref(null), attempts: ref({ page: 1, total: 1, items: [{ runId: 'root', revision: 1, status: 'failed', executionKind: 'original', outcomeUnknown: false, createdAt: '' }] }), failedNodes: computed(() => context.value.nodes.filter(n => n.decision.status === 'failed')), isHead: ref(true), rebuild: ref<string[]>([]), reconcileReasons: ref([]), preview: vi.fn(), openPlan: vi.fn(), queryOriginal: vi.fn(), reconcile: vi.fn(), submit: vi.fn().mockResolvedValue(retryRequest()), setHistoryPage: vi.fn() }
  return value as unknown as ReturnType<typeof useRunRetry>
}
function render(s = state()) { const wrapper = mount(RunRetryPanel, { props: { state: s }, global: { stubs: { RouterLink: RouterLinkStub } } }); (wrapper.vm as unknown as { show: () => void }).show(); return { wrapper, s } }
afterEach(() => setLocale('zh-CN'))
describe('human-readable recovery plan', () => {
  it('shows execution, reuse, blocked, safe producer links and explicit unchecked rebuild choices', async () => {
    const { wrapper, s } = render(); await flushPromises()
    expect(wrapper.findAll('[data-retry-group]')).toHaveLength(3)
    expect(wrapper.get('[data-retry-group="execute"]').text()).toContain('b')
    expect(wrapper.get('[data-retry-group="reuse"]').text()).toContain('a')
    const link = wrapper.findAllComponents(RouterLinkStub).find(l => l.text().includes('来源日志'))
    expect(link?.props('to')).toEqual({ name: 'run-detail', params: { id: 'root' }, query: { step: '0', stepId: 'step-a' } })
    const check = wrapper.get('input[type=checkbox]'); expect((check.element as HTMLInputElement).checked).toBe(false)
    await check.setValue(true); expect(s.preview).toHaveBeenCalledWith(undefined, ['a']); expect(s.submit).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('外部效果'); expect(wrapper.findAll('button button')).toHaveLength(0)
    wrapper.unmount()
  })
  it('keeps control failures visible even without an ordinary run step', async () => {
    const s = state(); const control = retryNode('init-key', 'failed', { node: { key: 'init-key', stageId: 's', executionStageId: 's', name: 'Original stage', type: 'control', controlKey: 'init' }, attemptStepId: '' })
    s.context.value!.nodes = [control]
    const { wrapper } = render(s); await flushPromises(); await wrapper.get('.retry-header button').trigger('click')
    expect(s.openPlan).toHaveBeenCalledWith(['init-key']); wrapper.unmount()
  })
  it('retranslates an already shown conflict and preserves the original request without new work', async () => {
    const s = state(); s.error.value = 'retry_conflict'
    const { wrapper } = render(s); await flushPromises(); const before = wrapper.get('[role=alert]').text()
    setLocale('de'); await flushPromises(); expect(wrapper.get('[role=alert]').text()).toBe(i18n.global.t('runRetry.reasons.retry_conflict')); expect(wrapper.get('[role=alert]').text()).not.toBe(before)
    expect(s.submit).not.toHaveBeenCalled(); expect(s.openPlan).not.toHaveBeenCalled(); wrapper.unmount()
  })
  it('emits navigation only for a recorded attempt and blocks confirm during unresolved work', async () => {
    const { wrapper, s } = render(); await flushPromises(); await wrapper.get('.app-btn--primary').trigger('click'); await flushPromises()
    expect(wrapper.emitted('accepted')).toEqual([['child']])
    s.pending.value = { runId: 'root', requestId: 'fixed-id' }; await flushPromises()
    expect((wrapper.get('.app-btn--primary').element as HTMLButtonElement).disabled).toBe(true)
    expect(wrapper.text()).toContain('固定') // pinned source remains visible while request is checked
    wrapper.unmount()
  })
})
