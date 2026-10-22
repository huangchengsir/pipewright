import { defineComponent, h, reactive, ref } from 'vue'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { provideProjectConfigRevisions } from './useProjectConfigRevisions'

let wrapper: VueWrapper | undefined
afterEach(() => wrapper?.unmount())
function setup(versions: string[], unavailable = -1) {
  const projectId = ref('project-1')
  const parts = versions.map((revision, index) => reactive({ revision, ready: index !== unavailable, reload: vi.fn(async () => {}) }))
  let coordinator!: ReturnType<typeof provideProjectConfigRevisions>
  wrapper = mount(defineComponent({ setup() {
    coordinator = provideProjectConfigRevisions(() => projectId.value)
    for (const part of parts) coordinator.register({ projectId: () => projectId.value, read: () => part.revision, advance: (_from, to) => { part.revision = to }, ready: () => part.ready, reload: part.reload })
    coordinator.synchronizeInitial(() => true)
    return () => h('div')
  } }))
  return { coordinator, parts, projectId }
}
describe('page-scoped configuration revisions', () => {
  it('advances only loaded participants with the exact acknowledged previous revision', () => {
    const { coordinator, parts } = setup(['cfg-2', 'cfg-2', 'cfg-1', 'cfg-2'], 3)
    coordinator.commit('project-1', 'cfg-2', 'cfg-3')
    expect(parts.map(part => part.revision)).toEqual(['cfg-3', 'cfg-3', 'cfg-1', 'cfg-2'])
  })
  it('does not spread an unrelated read or a commit for another project', async () => {
    const { coordinator, parts } = setup(['cfg-1', 'cfg-1'])
    parts[0].revision = 'cfg-8'; await flushPromises()
    expect(parts[1].revision).toBe('cfg-1')
    expect(parts.every(part => part.reload.mock.calls.length === 0)).toBe(true)
    coordinator.commit('other-project', 'cfg-1', 'cfg-9')
    expect(parts[1].revision).toBe('cfg-1')
    coordinator.commit('project-1', 'cfg-8', 'cfg-9')
    expect(parts.map(part => part.revision)).toEqual(['cfg-9', 'cfg-1'])
  })
  it('rejects a delayed old-project acknowledgement after navigation', async () => {
    const { coordinator, parts, projectId } = setup(['cfg-1', 'cfg-1'])
    projectId.value = 'project-2'; await flushPromises()
    coordinator.commit('project-1', 'cfg-1', 'cfg-2')
    expect(parts.map(part => part.revision)).toEqual(['cfg-1', 'cfg-1'])
  })
})
