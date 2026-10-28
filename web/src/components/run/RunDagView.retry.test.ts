import { describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, RouterLinkStub } from '@vue/test-utils'
import RunDagView from './RunDagView.vue'
import { i18n } from '../../i18n'
import RunStepList from './RunStepList.vue'
import { buildFrozenRunStages } from './runDag'
import { retryContext, retryNode } from '../../test/runRetryFixtures'
const api = vi.hoisted(() => ({ pipeline: vi.fn() }))
vi.mock('../../api/pipeline', () => ({ getPipeline: api.pipeline }))
describe('immutable recovery graph', () => {
  it('selects exact matrix identity despite identical job names and preserves parallel empty dependencies', async () => {
    const nodes = ['linux', 'windows'].map((os, index) => retryNode(`logical-${os}`, index ? 'failed' : 'success', { node: { key: `logical-${os}`, name: 'Test', type: 'script', stageId: 'tests', jobId: 'test', executionStageId: `test-${os}`, executionJobId: 'test', axes: { os, version: '1+2' } } }))
    const context = retryContext({ nodes, graph: { version: 1, nodes: nodes.map(n => n.node), edges: [], stages: [{ id: 'test-linux', name: 'Frozen tests' }, { id: 'test-windows', name: 'Frozen tests' }] } })
    const wrapper = mount(RunDagView, { props: { projectId: 'project', runStatus: 'success', steps: [], recovery: context }, global: { stubs: { RouterLink: RouterLinkStub } } }); await flushPromises()
    expect(wrapper.text()).toContain('os=windows'); expect(wrapper.text()).toContain('os=linux')
    const windows = wrapper.findAll('.dag-stage-card').find(card => card.text().includes('os=windows'))!
    expect(windows.findAll('.node-logs')).toHaveLength(1); expect(windows.find('.step-row .node-logs').exists()).toBe(false)
    await windows.get('.node-logs').trigger('click')
    expect(wrapper.emitted('logs')?.[0][0]).toEqual(['logical-windows'])
    await wrapper.get('[data-logical-node-key="logical-windows"] .node-retry').trigger('click')
    expect(wrapper.emitted('retry')).toEqual([[['logical-windows']]])
    expect(wrapper.findAll('.dag-edge')).toHaveLength(0); expect(api.pipeline).not.toHaveBeenCalled(); wrapper.unmount()
  })
  it('keeps inherited nodes concise with no new duration or permanent dependency/source paragraphs', async () => {
    const context = retryContext(); context.nodes[0].decision.action = 'reuse'; context.nodes[0].sourceStepId = 'producer-row'; context.nodes[0].sourceOrdinal = 7
    const steps = [{ id: 'step-a', name: 'Renamed current label', status: 'success' as const, startedAt: null, finishedAt: null, durationMs: 90000 }]
    expect(buildFrozenRunStages(context, steps)[0].steps[0].durationMs).toBeNull()
    const wrapper = mount(RunDagView, { props: { projectId: 'p', runStatus: 'failed', steps, recovery: context }, global: { stubs: { RouterLink: RouterLinkStub } } }); await flushPromises()
    expect(wrapper.text()).not.toContain('依赖：'); expect(wrapper.text()).not.toContain('Renamed current label')
    const toggle = wrapper.get('.stage-toggle'); await toggle.trigger('click'); await toggle.trigger('click')
    expect(wrapper.findComponent(RouterLinkStub).exists()).toBe(false); expect(wrapper.findAll('.node-logs')).toHaveLength(1); await wrapper.get('.node-logs').trigger('click'); expect(wrapper.emitted('logs')?.[0][0]).toEqual(['a', 'b', 'c']); wrapper.unmount()
  })
  it('explains legacy evidence without loading today’s pipeline or inventing a retry node', async () => {
    const step = { id: 'legacy-row', name: 'Old task', status: 'failed' as const, startedAt: null, finishedAt: null, durationMs: null }
    const wrapper = mount(RunDagView, { props: { projectId: 'p', runStatus: 'failed', steps: [step] } }); await flushPromises()
    expect(wrapper.text()).toContain('未获取当时的完整拓扑'); await wrapper.get('.node-retry').trigger('click'); expect(wrapper.emitted('retry')).toEqual([[[]]]); expect(api.pipeline).not.toHaveBeenCalled(); wrapper.unmount()
  })
  it('keeps retry as a sibling of the existing log-selection button', async () => {
    const context = retryContext(); const step = { id: 'step-b', name: 'B', status: 'failed' as const, startedAt: null, finishedAt: null, durationMs: null }
    const wrapper = mount(RunStepList, { props: { steps: [step], recovery: context } }); await wrapper.get('.step-retry').trigger('click')
    await wrapper.get('.step-row:not(.step-row--all)').trigger('click'); expect(wrapper.emitted('select')).toEqual([[0]]); expect(wrapper.find('.step-logs').exists()).toBe(false); expect(wrapper.emitted('retry')).toEqual([[['b']]]); expect(wrapper.find('button button').exists()).toBe(false); wrapper.unmount()
  })
  it('keeps blocked pending descendants visible while hiding their detailed dependencies', async () => {
    const context = retryContext(); context.nodes[2].node.executionStageId = 'future'; context.nodes[2].decision = { action: 'blocked', status: 'pending' }
    const wrapper = mount(RunDagView, { props: { projectId: 'p', runStatus: 'failed', steps: [], recovery: context }, global: { stubs: { RouterLink: RouterLinkStub } } }); await flushPromises()
    const row = wrapper.get('[data-logical-node-key="c"]'); expect(row.text()).toContain('受阻'); expect(row.text()).not.toContain('依赖：'); wrapper.unmount()
  })
  it('marks step-list reuse concisely and never aggregates mixed pending work as success', async () => {
    const context = retryContext(); context.nodes[0].decision.action = 'reuse'; context.nodes[0].sourceStepId = 'source-a'; context.nodes[0].sourceOrdinal = 8
    const steps = [{ id: 'step-a', stage: 'Build', name: 'A', status: 'success' as const, startedAt: null, finishedAt: null, durationMs: 90000 }, { id: 'step-b', stage: 'Build', name: 'B', status: 'pending' as const, startedAt: null, finishedAt: null, durationMs: null }]
    const wrapper = mount(RunStepList, { props: { steps, recovery: context }, global: { stubs: { RouterLink: RouterLinkStub } } })
    expect(wrapper.find('.stage-head--pending').exists()).toBe(true); expect(wrapper.text()).toContain('复用'); expect(wrapper.find('.step-dur').exists()).toBe(false)
    expect(wrapper.findComponent(RouterLinkStub).exists()).toBe(false); expect(wrapper.find('.step-logs').exists()).toBe(false); await wrapper.findAll('.step-row:not(.step-row--all)')[0].trigger('click'); expect(wrapper.emitted('select')).toEqual([[0]]); expect(wrapper.find('button a').exists()).toBe(false); wrapper.unmount()
  })
  it('folds only successful system controls and keeps failed or unknown controls actionable', async () => {
    const nodes = ['success', 'failed', 'outcome_unknown'].map((status, i) => retryNode(`control-${i}`, status, { node: { key: `control-${i}`, stageId: 'build', executionStageId: 'build', name: `Control ${i}`, type: 'control', controlKey: `control-${i}` }, attemptStepId: '', ordinal: -1 }))
    const context = retryContext({ nodes, graph: { version: 1, nodes: nodes.map(n => n.node), edges: [], stages: [{ id: 'build', name: 'Frozen controls' }] } })
    const wrapper = mount(RunDagView, { props: { projectId: 'p', runStatus: 'failed', steps: [], recovery: context } }); await flushPromises()
    expect(wrapper.find('[data-logical-node-key="control-0"]').exists()).toBe(false)
    expect(wrapper.find('[data-logical-node-key="control-1"] .node-retry').exists()).toBe(true)
    expect(wrapper.find('[data-logical-node-key="control-2"] .node-retry').exists()).toBe(true)
    expect(wrapper.get('.system-steps button').attributes('aria-expanded')).toBe('false')
    expect(wrapper.findAll('.node-logs')).toHaveLength(1); await wrapper.get('.node-logs').trigger('click'); expect(wrapper.emitted('logs')?.[0][0]).toEqual(['control-1', 'control-2'])
    await wrapper.get('.system-steps button').trigger('click')
    expect(wrapper.find('[data-logical-node-key="control-0"]').exists()).toBe(true)
    await wrapper.get('[data-logical-node-key="control-2"] .node-retry').trigger('click')
    expect(wrapper.emitted('retry')).toEqual([[['control-2']]])
    wrapper.unmount()
  })
  it('keeps one card-level log entry available while rows are collapsed', async () => {
    const context = retryContext(); context.nodes.forEach(n => { n.decision.status = 'success' })
    const wrapper = mount(RunDagView, { props: { projectId: 'p', runStatus: 'success', steps: [], recovery: context } }); await flushPromises()
    expect(wrapper.find('.stage-steps').exists()).toBe(false); expect(wrapper.findAll('.node-logs')).toHaveLength(1)
    await wrapper.get('.node-logs').trigger('click'); expect(wrapper.emitted('logs')?.[0]).toEqual([['a', 'b', 'c'], 'Original build']); wrapper.unmount()
  })
  it('labels approval-waiting work accurately in concise node rows', async () => {
    const context = retryContext(); context.nodes[1].decision.status = 'waiting_approval'
    const wrapper = mount(RunDagView, { props: { projectId: 'p', runStatus: 'waiting_approval', steps: [], recovery: context } }); await flushPromises()
    await wrapper.get('.stage-toggle').trigger('click'); await flushPromises()
    expect(wrapper.get('[data-logical-node-key="b"] .node-mark').text()).toBe(i18n.global.t('runStatus.waiting_approval')); wrapper.unmount()
  })

})
