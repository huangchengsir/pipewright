import { afterEach, describe, expect, it, vi } from 'vitest'
import { computed, defineComponent, onUnmounted, ref } from 'vue'
import { flushPromises, mount, RouterLinkStub } from '@vue/test-utils'
import RunRetryPanel from './RunRetryPanel.vue'
import type { useRunRetry } from '../../composables/useRunRetry'
import { retryContext, retryNode, retryPlan, retryRequest } from '../../test/runRetryFixtures'
import { i18n, setLocale, SUPPORTED_LOCALES } from '../../i18n'
function state() {
  const context = ref(retryContext()); const plan = ref(retryPlan({ rebuildGroups: [{ nodeIds: ['a'], consumerIds: ['b'], reasonCode: 'workspace_rebuild_required' }], warnings: [{ code: 'side_effect_reexecute', nodeKey: 'c' }] }))
  const value = { context, plan, contextError: ref(''), error: ref(''), planLoading: ref(false), submitting: ref(false), querying: ref(false), reconciling: ref(false), pending: ref(null), request: ref(null), attempts: ref({ page: 1, total: 1, items: [{ runId: 'root', revision: 1, status: 'failed', executionKind: 'original', outcomeUnknown: false, createdAt: '' }] }), failedNodes: computed(() => context.value.nodes.filter(n => n.decision.status === 'failed')), isHead: ref(true), rebuild: ref<string[]>([]), reconcileReasons: ref([]), preview: vi.fn(), openPlan: vi.fn(), queryOriginal: vi.fn(), reconcile: vi.fn(), submit: vi.fn().mockResolvedValue(retryRequest()), setHistoryPage: vi.fn() }
  return value as unknown as ReturnType<typeof useRunRetry>
}
function render(s = state()) { const wrapper = mount(RunRetryPanel, { props: { state: s }, global: { stubs: { RouterLink: RouterLinkStub, Teleport: true } } }); (wrapper.vm as unknown as { show: (key?: string, view?: string) => void }).show(undefined, 'plan'); return { wrapper, s } }
afterEach(() => setLocale('zh-CN'))
describe('human-readable recovery plan', () => {
  it('keeps the default page clear and opens only the exact node without requesting a plan', async () => {
    const s = state(); s.context.value!.nodes[0].decision.action = 'reuse'; s.context.value!.nodes[0].sourceStepId = 'source-a'; s.context.value!.nodes[0].sourceOrdinal = 7
    const wrapper = mount(RunRetryPanel, { props: { state: s }, global: { stubs: { RouterLink: RouterLinkStub, Teleport: true } } })
    expect(wrapper.find('[role=dialog]').exists()).toBe(false)
    await (wrapper.vm as unknown as { show: (key: string) => Promise<void> }).show('a'); await flushPromises()
    expect(wrapper.get('h2').text()).toBe('a'); expect(wrapper.text()).toContain('已复用成功结果')
    expect(wrapper.find('[data-retry-group]').exists()).toBe(false); expect(wrapper.find('.retry-history').exists()).toBe(false)
    expect(s.openPlan).not.toHaveBeenCalled(); expect(s.preview).not.toHaveBeenCalled(); expect(s.submit).not.toHaveBeenCalled()
    expect(wrapper.findAllComponents(RouterLinkStub).some(link => typeof link.props('to') !== 'string' && (link.props('to') as { query?: { stepId?: string } }).query?.stepId === 'source-a')).toBe(true)
    ;(wrapper.vm as unknown as { showHistory: () => void }).showHistory(); await flushPromises()
    expect(wrapper.get('h2').text()).toBe('运行尝试历史'); expect(wrapper.text()).toContain('不是单个节点'); expect(wrapper.find('.retry-history').exists()).toBe(true)
    wrapper.unmount()
  })
  it('contains keyboard focus including source links and restores it without forgetting pending work', async () => {
    const s = state(); s.context.value!.nodes[0].decision.action = 'reuse'; s.context.value!.nodes[0].sourceStepId = 'source-a'; s.context.value!.nodes[0].sourceOrdinal = 7
    s.pending.value = { runId: 'root', requestId: 'fixed-id' }
    const trigger = document.createElement('button'); document.body.append(trigger); trigger.focus()
    const wrapper = mount(RunRetryPanel, { attachTo: document.body, props: { state: s }, global: { stubs: { RouterLink: { props: ['to'], template: '<a href="/runs/root"><slot /></a>' }, Teleport: true } } })
    await (wrapper.vm as unknown as { show: (key: string) => Promise<void> }).show('a'); await flushPromises()
    const heading = wrapper.get('h2'); expect(document.activeElement).toBe(heading.element)
    await heading.trigger('keydown', { key: 'Tab', shiftKey: true }); expect(document.activeElement?.tagName).toBe('A')
    await wrapper.get('.retry-close').trigger('focus'); (wrapper.get('.retry-close').element as HTMLElement).focus()
    await wrapper.get('.retry-close').trigger('keydown', { key: 'Tab', shiftKey: true }); expect(document.activeElement?.tagName).toBe('A')
    await wrapper.get('[role=dialog]').trigger('keydown', { key: 'Escape' }); await flushPromises()
    expect(wrapper.find('[role=dialog]').exists()).toBe(false); expect(document.activeElement).toBe(trigger)
    expect(s.pending.value?.requestId).toBe('fixed-id'); expect(s.submit).not.toHaveBeenCalled()
    wrapper.unmount(); trigger.remove()
  })

  it('cycles plan focus through the technical summary and keeps explicit rebuild inputs available', async () => {
    const s = state(); const wrapper = mount(RunRetryPanel, { attachTo: document.body, props: { state: s }, global: { stubs: { RouterLink: { props: ['to'], template: '<a href="/runs/root"><slot /></a>' }, Teleport: true } } })
    await (wrapper.vm as unknown as { show: (key?: string, view?: string) => Promise<void> }).show(undefined, 'plan'); await flushPromises()
    const summary = wrapper.get('summary'); (summary.element as HTMLElement).focus()
    await summary.trigger('keydown', { key: 'Tab' }); expect(document.activeElement).toBe(wrapper.get('.retry-close').element)
    await wrapper.get('.retry-close').trigger('keydown', { key: 'Tab', shiftKey: true }); expect(document.activeElement).toBe(summary.element)
    expect((wrapper.get('input[type=checkbox]').element as HTMLInputElement).disabled).toBe(false)
    wrapper.unmount()
  })
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
    const { wrapper } = render(s); await flushPromises(); await wrapper.get('.retry-all').trigger('click')
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


describe('precise node terminal logs and retained recovery interactions', () => {
  const step = (id: string) => ({ id, name: id, status: 'failed' as const, startedAt: null, finishedAt: null, durationMs: null })
  function logs(s = state(), extra: Record<string, unknown> = {}) {
    const disposed = vi.fn()
    const terminal = defineComponent({ props: ['runId', 'live', 'filterOrdinal'], setup() { onUnmounted(disposed) }, template: '<div class="terminal-probe" />' })
    const wrapper = mount(RunRetryPanel, { props: { state: s, runId: 'root', steps: [step('step-a'), step('step-b')], runStatus: 'failed', ...extra }, global: { stubs: { RunTerminal: terminal, RouterLink: { props: ['to'], template: '<a href="/runs/root?step=0&stepId=step-a" @click.prevent><slot /></a>' }, Teleport: true } } })
    const show = (key: string | string[], title?: string) => (wrapper.vm as unknown as { showLogs: (key: string | string[], title?: string) => Promise<void> }).showLogs(key, title)
    return { wrapper, show, terminal, disposed, s }
  }
  it('filters current node logs by matching row ID and ordinal and destroys the terminal on close', async () => {
    const { wrapper, show, terminal, disposed, s } = logs(state(), { runStatus: 'running' })
    await show('b'); await flushPromises()
    expect(wrapper.findComponent(terminal).props()).toMatchObject({ runId: 'root', filterOrdinal: 1, live: true })
    expect(wrapper.find('.retry-log-surface').exists()).toBe(true); expect(s.openPlan).not.toHaveBeenCalled()
    await wrapper.get('.retry-close').trigger('click'); expect(wrapper.findComponent(terminal).exists()).toBe(false); expect(disposed).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })
  it('loads inherited output from the true producer rather than the current child', async () => {
    const s = state(); const node = s.context.value!.nodes[0]; node.decision.action = 'reuse'; node.decision.sourceAttemptId = 'producer'; node.sourceStepId = 'producer-row'; node.sourceOrdinal = 7
    const { wrapper, show, terminal } = logs(s, { runId: 'child', runStatus: 'running' })
    await show('a'); await flushPromises()
    expect(wrapper.findComponent(terminal).props()).toMatchObject({ runId: 'producer', filterOrdinal: 7, live: false })
    wrapper.unmount()
  })
  it.each(['mismatched-row', 'missing-row', 'missing-producer', 'control'])('does not mount a misleading whole-run terminal when mapping is %s', async kind => {
    const s = state(); const node = s.context.value!.nodes[1]
    if (kind === 'mismatched-row') node.ordinal = 0
    if (kind === 'missing-row') node.attemptStepId = 'absent'
    if (kind === 'missing-producer') { node.decision.action = 'reuse'; node.sourceStepId = ''; node.sourceOrdinal = -1 }
    if (kind === 'control') { node.node.controlKey = 'init'; node.ordinal = -1; node.attemptStepId = '' }
    const { wrapper, show, terminal } = logs(s); await show('b'); await flushPromises()
    expect(wrapper.findComponent(terminal).exists()).toBe(false)
    expect(wrapper.text()).toContain(i18n.global.t('runRetry.nodeLogsUnavailable'))
    wrapper.unmount()
  })
  it('keeps legacy recorded steps readable without recovery metadata', async () => {
    const s = state(); s.context.value = null
    const { wrapper, show, terminal } = logs(s); await show('step-b'); await flushPromises()
    expect(wrapper.findComponent(terminal).props()).toMatchObject({ runId: 'root', filterOrdinal: 1, live: false })
    wrapper.unmount()
  })
  it('closes on a same-run query link and releases its log view', async () => {
    const { wrapper, show, disposed } = logs(); await show('b'); await flushPromises()
    await wrapper.get('.retry-log-step > a').trigger('click'); await flushPromises()
    expect(wrapper.find('[role=dialog]').exists()).toBe(false); expect(disposed).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })
  it('uses vertical left step tabs, defaults to failed work and switches precisely between current and inherited logs', async () => {
    const s = state(); const producer = s.context.value!.nodes[0]; producer.decision.action = 'reuse'; producer.decision.sourceAttemptId = 'producer'; producer.sourceStepId = 'producer-row'; producer.sourceOrdinal = 7
    const { wrapper, show, terminal, disposed } = logs(s, { runId: 'child', runStatus: 'running' })
    await show(['a', 'b'], 'Original build'); await flushPromises()
    expect(wrapper.get('h2').text()).toBe('Original build'); expect(wrapper.get('[role=tablist]').attributes('aria-orientation')).toBe('vertical')
    const tabs = () => wrapper.findAll('[role=tab]'); expect(tabs()).toHaveLength(2); expect(tabs()[1].attributes('aria-selected')).toBe('true'); expect(tabs()[0].attributes('tabindex')).toBe('-1')
    expect(wrapper.findComponent(terminal).props()).toMatchObject({ runId: 'child', filterOrdinal: 1, live: true })
    await tabs()[0].trigger('click'); await flushPromises()
    expect(wrapper.findComponent(terminal).props()).toMatchObject({ runId: 'producer', filterOrdinal: 7, live: false }); expect(disposed).toHaveBeenCalledTimes(1)
    expect(wrapper.get('[role=tabpanel]').attributes('aria-labelledby')).toBe('retry-log-tab-0')
    await tabs()[0].trigger('keydown', { key: 'End' }); await flushPromises(); expect(tabs()[1].attributes('aria-selected')).toBe('true')
    await tabs()[1].trigger('keydown', { key: 'ArrowDown' }); await flushPromises(); expect(tabs()[0].attributes('aria-selected')).toBe('true')
    await tabs()[0].trigger('keydown', { key: 'ArrowUp' }); await flushPromises(); expect(tabs()[1].attributes('aria-selected')).toBe('true')
    await tabs()[1].trigger('keydown', { key: 'Home' }); await flushPromises(); expect(tabs()[0].attributes('aria-selected')).toBe('true')
    expect(s.openPlan).not.toHaveBeenCalled(); wrapper.unmount()
  })
  it('unmounts previous output when selecting a tab without exact log mapping', async () => {
    const s = state(); const { wrapper, show, terminal, disposed } = logs(s)
    s.context.value!.nodes[1].ordinal = 0
    await show(['a', 'b']); await flushPromises(); expect(wrapper.findComponent(terminal).exists()).toBe(false)
    await wrapper.findAll('[role=tab]')[0].trigger('click'); await flushPromises(); expect(wrapper.findComponent(terminal).props('filterOrdinal')).toBe(0)
    await wrapper.findAll('[role=tab]')[1].trigger('click'); await flushPromises(); expect(wrapper.findComponent(terminal).exists()).toBe(false); expect(disposed).toHaveBeenCalledTimes(1)
    expect(wrapper.text()).toContain(i18n.global.t('runRetry.nodeLogsUnavailable')); wrapper.unmount()
  })
  it('opens a plan and moves focus even when another plan is already loading', async () => {
    const s = state(); s.planLoading.value = true
    const trigger = document.createElement('button'); document.body.append(trigger); trigger.focus()
    const wrapper = mount(RunRetryPanel, { attachTo: document.body, props: { state: s }, global: { stubs: { RouterLink: RouterLinkStub } } })
    const panel = () => document.body.querySelector<HTMLElement>('[role=dialog]')!
    await (wrapper.vm as unknown as { show: (key: string) => Promise<void> }).show('b'); await flushPromises()
    panel().querySelector<HTMLButtonElement>('.retry-focused')!.click(); await flushPromises()
    expect(panel().classList.contains('retry-panel--plan')).toBe(true); expect(document.activeElement).toBe(panel().querySelector('h2')); expect(s.openPlan).toHaveBeenCalledWith(['b'])
    s.planLoading.value = false; await flushPromises(); const closeButton = panel().querySelector<HTMLButtonElement>('.retry-close')!; closeButton.focus(); expect(document.activeElement).toBe(closeButton)
    s.planLoading.value = true; await flushPromises(); expect(document.activeElement).toBe(panel().querySelector('.retry-close'))
    wrapper.unmount(); trigger.remove()
  })
  it('retains a closed submit refusal in a localized visible alert after the original request is resolved', async () => {
    const s = state(); s.pending.value = { runId: 'root', requestId: 'fixed-id' }
    const { wrapper, show } = logs(s); await show('b'); await wrapper.get('.retry-close').trigger('click')
    s.pending.value = null; s.error.value = 'retry_not_found'; await flushPromises()
    expect(wrapper.find('[role=dialog]').exists()).toBe(false)
    expect(wrapper.get('.retry-closed-error').text()).toContain(i18n.global.t('runRetry.reasons.retry_not_found'))
    expect(s.submit).not.toHaveBeenCalled(); expect(s.openPlan).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})


describe('legacy log feedback separation', () => {
  it.each(['unauthorized', 'csrf_invalid', 'retry_connection_unknown', 'retry_unavailable', 'retry_maintenance'])('keeps actionable %s context errors visible in log mode without recommending replay', async code => {
    const s = state(); s.failedNodes = computed(() => []); s.context.value = null; s.contextError.value = code
    const wrapper = mount(RunRetryPanel, { props: { state: s }, global: { stubs: { RouterLink: RouterLinkStub, Teleport: true } } })
    await (wrapper.vm as unknown as { showLogs: (key: string) => Promise<void> }).showLogs('step'); await flushPromises()
    expect(wrapper.get('.retry-unavailable [role=alert]').text()).toBe(i18n.global.t(`runRetry.reasons.${code}`))
    expect(wrapper.text()).not.toContain(i18n.global.t('runRetry.legacyUnavailable'))
    expect(s.submit).not.toHaveBeenCalled(); expect(s.openPlan).not.toHaveBeenCalled(); wrapper.unmount()
  })
  it('keeps a canonical evidence-unavailable response quiet only in ordinary logs', async () => {
    const s = state(); s.failedNodes = computed(() => []); s.context.value = null; s.contextError.value = 'retry_evidence_unavailable'
    const wrapper = mount(RunRetryPanel, { props: { state: s }, global: { stubs: { RouterLink: RouterLinkStub, Teleport: true } } })
    const vm = wrapper.vm as unknown as { showLogs: (key: string) => Promise<void>; show: () => Promise<void> }
    await vm.showLogs('step'); await flushPromises(); expect(wrapper.find('.retry-unavailable').exists()).toBe(false)
    await vm.show(); await flushPromises(); expect(wrapper.get('.retry-unavailable').text()).toContain(i18n.global.t('runRetry.reasons.retry_evidence_unavailable'))
    expect(s.submit).not.toHaveBeenCalled(); wrapper.unmount()
  })
  it('keeps ordinary legacy node logs clear and retains the recovery explanation outside logs', async () => {
    const s = state(); s.failedNodes = computed(() => []); s.context.value = null; s.contextError.value = 'manifest_missing'
    const wrapper = mount(RunRetryPanel, { props: { state: s, runId: 'root', steps: [{ id: 'old-step', name: 'checkout', status: 'success', startedAt: null, finishedAt: null, durationMs: null }] }, global: { stubs: { RunTerminal: { template: '<div class="legacy-output">checked out</div>' }, RouterLink: RouterLinkStub, Teleport: true } } })
    const vm = wrapper.vm as unknown as { showLogs: (key: string) => Promise<void>; show: (key?: string) => Promise<void> }
    await vm.showLogs('old-step'); await flushPromises()
    expect(wrapper.get('.legacy-output').text()).toBe('checked out')
    expect(wrapper.find('.retry-unavailable').exists()).toBe(false); expect(wrapper.find('.retry-warning').exists()).toBe(false)
    await vm.show(); await flushPromises()
    expect(wrapper.get('.retry-unavailable details').attributes('open')).toBeUndefined()
    expect(wrapper.get('.retry-unavailable').text()).toContain(i18n.global.t('runRetry.reasons.manifest_missing'))
    for (const { code } of SUPPORTED_LOCALES) { setLocale(code); await flushPromises(); expect(wrapper.get('.retry-unavailable').text()).not.toContain('runRetry.'); expect(wrapper.get('.retry-unavailable').text()).toContain(i18n.global.t('runRetry.legacyUnavailable')) }
    expect(s.submit).not.toHaveBeenCalled(); expect(s.openPlan).not.toHaveBeenCalled(); wrapper.unmount()
  })
  it('retains unresolved requests and real operation errors even when recovery context is absent', async () => {
    const s = state(); s.failedNodes = computed(() => []); s.context.value = null; s.contextError.value = 'manifest_missing'; s.error.value = 'retry_conflict'; s.pending.value = { runId: 'root', requestId: 'original' }
    const wrapper = mount(RunRetryPanel, { props: { state: s }, global: { stubs: { RouterLink: RouterLinkStub, Teleport: true } } })
    await (wrapper.vm as unknown as { showLogs: (key: string) => Promise<void> }).showLogs('missing'); await flushPromises()
    expect(wrapper.find('.retry-unavailable').exists()).toBe(false)
    expect(wrapper.get('[role="alert"]').text()).toContain(i18n.global.t('runRetry.reasons.retry_conflict'))
    expect(wrapper.text()).toContain(i18n.global.t('runRetry.queryOriginal')); expect(s.pending.value?.requestId).toBe('original')
    expect(s.submit).not.toHaveBeenCalled(); wrapper.unmount()
  })
})
