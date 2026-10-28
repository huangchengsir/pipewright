import { describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, RouterLinkStub } from '@vue/test-utils'
import RunDagView from './RunDagView.vue'
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
    await wrapper.get('[data-logical-node-key="logical-windows"] .node-retry').trigger('click')
    expect(wrapper.emitted('retry')).toEqual([[['logical-windows']]])
    expect(wrapper.findAll('.dag-edge')).toHaveLength(0); expect(api.pipeline).not.toHaveBeenCalled(); wrapper.unmount()
  })
  it('shows frozen dependencies, source logs and no new duration for inherited work', async () => {
    const context = retryContext(); context.nodes[0].decision.action = 'reuse'; context.nodes[0].sourceStepId = 'producer-row'; context.nodes[0].sourceOrdinal = 7
    const steps = [{ id: 'step-a', name: 'Renamed current label', status: 'success' as const, startedAt: null, finishedAt: null, durationMs: 90000 }]
    expect(buildFrozenRunStages(context, steps)[0].steps[0].durationMs).toBeNull()
    const wrapper = mount(RunDagView, { props: { projectId: 'p', runStatus: 'failed', steps, recovery: context }, global: { stubs: { RouterLink: RouterLinkStub } } }); await flushPromises()
    expect(wrapper.text()).toContain('依赖：a'); expect(wrapper.text()).not.toContain('Renamed current label')
    const toggle = wrapper.get('.stage-toggle'); await toggle.trigger('click'); await toggle.trigger('click')
    const link = wrapper.findAllComponents(RouterLinkStub).find(l => l.text().includes('来源日志')); expect(link?.props('to')).toEqual({ name: 'run-detail', params: { id: 'root' }, query: { step: '7', stepId: 'producer-row' } }); wrapper.unmount()
  })
  it('explains legacy evidence without loading today’s pipeline or inventing a retry node', async () => {
    const step = { id: 'legacy-row', name: 'Old task', status: 'failed' as const, startedAt: null, finishedAt: null, durationMs: null }
    const wrapper = mount(RunDagView, { props: { projectId: 'p', runStatus: 'failed', steps: [step] } }); await flushPromises()
    expect(wrapper.text()).toContain('未获取当时的完整拓扑'); await wrapper.get('.node-retry').trigger('click'); expect(wrapper.emitted('retry')).toEqual([[[]]]); expect(api.pipeline).not.toHaveBeenCalled(); wrapper.unmount()
  })
  it('keeps retry as a sibling of the existing log-selection button', async () => {
    const context = retryContext(); const step = { id: 'step-b', name: 'B', status: 'failed' as const, startedAt: null, finishedAt: null, durationMs: null }
    const wrapper = mount(RunStepList, { props: { steps: [step], recovery: context } }); await wrapper.get('.step-retry').trigger('click')
    expect(wrapper.emitted('retry')).toEqual([[['b']]]); expect(wrapper.find('button button').exists()).toBe(false); wrapper.unmount()
  })
  it('expands blocked pending descendants and names their frozen dependency', async () => {
    const context = retryContext(); context.nodes[2].node.executionStageId = 'future'; context.nodes[2].decision = { action: 'blocked', status: 'pending' }
    const wrapper = mount(RunDagView, { props: { projectId: 'p', runStatus: 'failed', steps: [], recovery: context }, global: { stubs: { RouterLink: RouterLinkStub } } }); await flushPromises()
    const row = wrapper.get('[data-logical-node-key="c"]'); expect(row.text()).toContain('暂不能继续'); expect(row.text()).toContain('依赖：b'); wrapper.unmount()
  })
  it('marks step-list reuse, links the producer and never aggregates mixed pending work as success', async () => {
    const context = retryContext(); context.nodes[0].decision.action = 'reuse'; context.nodes[0].sourceStepId = 'source-a'; context.nodes[0].sourceOrdinal = 8
    const steps = [{ id: 'step-a', stage: 'Build', name: 'A', status: 'success' as const, startedAt: null, finishedAt: null, durationMs: 90000 }, { id: 'step-b', stage: 'Build', name: 'B', status: 'pending' as const, startedAt: null, finishedAt: null, durationMs: null }]
    const wrapper = mount(RunStepList, { props: { steps, recovery: context }, global: { stubs: { RouterLink: RouterLinkStub } } })
    expect(wrapper.find('.stage-head--pending').exists()).toBe(true); expect(wrapper.text()).toContain('已复用成功结果'); expect(wrapper.find('.step-dur').exists()).toBe(false)
    expect(wrapper.findComponent(RouterLinkStub).props('to')).toEqual({ name: 'run-detail', params: { id: 'root' }, query: { step: '8', stepId: 'source-a' } }); expect(wrapper.find('button a').exists()).toBe(false); wrapper.unmount()
  })
})
