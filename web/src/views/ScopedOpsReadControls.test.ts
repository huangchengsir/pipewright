import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { access } from '../api/access'
import { getContainerInspect, type ContainerInspect } from '../api/containers'
import { getAllServerMetrics, listServers, type Server, type ServerMetrics } from '../api/servers'
import ContainerInspectModal from '../components/ops/ContainerInspectModal.vue'
import { usePermissionStore } from '../stores/permissions'
import { useSessionStore } from '../stores/session'
import ServerStatusContent from './ServerStatusContent.vue'

vi.mock('../api/access', () => ({ access: { decisions: vi.fn() } }))
vi.mock('../api/containers', () => ({ getContainerInspect: vi.fn() }))
vi.mock('../api/servers', () => ({ getAllServerMetrics: vi.fn(), listServers: vi.fn() }))

const cleanup: Array<() => void> = []
const inspection: ContainerInspect = {
  serverId: 'selected', containerId: 'preview', reachable: true, image: 'fixture:initial',
  env: [], mounts: [], networks: [], ports: [], labels: {},
}
const server: Server = {
  id: 'selected', name: 'Selected fixture', host: 'fixture.example', port: 22, user: 'fixture',
  credentialId: 'synthetic-reference', credentialName: 'Fixture', createdAt: '', updatedAt: '',
}
const metrics: ServerMetrics = {
  serverId: 'selected', reachable: true, error: '', cpu: null, memory: null, disk: null,
  collectedAt: '2026-11-15T01:00:00Z',
}
const selectedTeamOptions = expect.objectContaining({
  headers: { 'X-Pipewright-Team': 'team-a' },
  signal: expect.any(AbortSignal),
})

beforeEach(() => {
  vi.resetAllMocks()
  setActivePinia(createPinia())
  useSessionStore().setUser({ username: 'fixture employee', userId: 'member', multiTenant: true })
  usePermissionStore().setTeam('team-a')
  vi.mocked(access.decisions).mockImplementation(async (team, queries) => ({
    teamId: team, userId: 'member', modeEpoch: 1, revision: 1, catalogVersion: 1,
    allowed: queries.map(query => team === 'team-a' && query.resourceId === 'selected'
      && (query.action === 'container.inspect' || query.action === 'server.inspect')),
  }))
  vi.mocked(getContainerInspect).mockResolvedValue(inspection)
  vi.mocked(getAllServerMetrics).mockResolvedValue([metrics, { ...metrics, serverId: 'denied' }])
  vi.mocked(listServers).mockResolvedValue([server, { ...server, id: 'denied', name: 'Denied fixture' }])
})
afterEach(() => cleanup.splice(0).forEach(unmount => unmount()))

function inspect() {
  const wrapper = mount(ContainerInspectModal, { props: { serverId: 'selected', containerName: 'preview' } })
  cleanup.push(() => wrapper.unmount())
  return wrapper
}

describe('container inspection and server overview controls', () => {
  it('closes container inspection from the backdrop and keeps clicks inside the dialog open', async () => {
    const wrapper = inspect()
    await flushPromises()
    await wrapper.get('[role=dialog]').trigger('click')
    expect(wrapper.emitted('close')).toBeUndefined()
    await wrapper.get('.ci-backdrop').trigger('click')
    expect(wrapper.emitted('close')).toEqual([[]])
  })

  it('lets the user close container inspection after its read permission is withdrawn', async () => {
    const wrapper = inspect()
    await flushPromises()
    expect(wrapper.text()).toContain('fixture:initial')
    usePermissionStore().reject('team-a', { action: 'container.inspect', resourceId: 'selected' })
    await flushPromises()
    expect(wrapper.text()).not.toContain('fixture:initial')
    await wrapper.get('button.ci-close').trigger('click')
    expect(wrapper.emitted('close')).toEqual([[]])
  })

  it('scopes the exact inspection read to the selected team and drops a delayed result after switching', async () => {
    let finish!: (value: ContainerInspect) => void
    vi.mocked(getContainerInspect).mockImplementation(() => new Promise(resolve => { finish = resolve }))
    const wrapper = inspect()
    await flushPromises()
    expect(getContainerInspect).toHaveBeenCalledExactlyOnceWith('selected', 'preview', selectedTeamOptions)
    const signal = vi.mocked(getContainerInspect).mock.calls[0]![2]?.signal
    usePermissionStore().setTeam('team-b')
    await flushPromises()
    expect(signal?.aborted).toBe(true)
    finish({ ...inspection, image: 'fixture:previous-team' })
    await flushPromises()
    expect(wrapper.text()).not.toContain('fixture:previous-team')
    expect(getContainerInspect).toHaveBeenCalledTimes(1)
  })

  it('refreshes only the current team server overview and ignores previous-team refresh results', async () => {
    const wrapper = mount(ServerStatusContent)
    cleanup.push(() => wrapper.unmount())
    await flushPromises()
    expect(wrapper.text()).toContain('Selected fixture')
    expect(wrapper.text()).not.toContain('Denied fixture')
    let finish!: (items: ServerMetrics[]) => void
    vi.mocked(getAllServerMetrics).mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    const refresh = wrapper.get<HTMLButtonElement>('.view-header__actions button.app-btn--default')
    await refresh.trigger('click')
    expect(refresh.element.disabled).toBe(true)
    expect(getAllServerMetrics).toHaveBeenCalledTimes(2)
    expect(getAllServerMetrics).toHaveBeenLastCalledWith(selectedTeamOptions)
    expect(listServers).toHaveBeenCalledTimes(2)
    expect(listServers).toHaveBeenLastCalledWith(selectedTeamOptions)
    const signal = vi.mocked(getAllServerMetrics).mock.calls[1]![0]?.signal

    usePermissionStore().setTeam('team-b')
    await flushPromises()
    expect(signal?.aborted).toBe(true)
    finish([metrics])
    await flushPromises()
    expect(wrapper.text()).not.toContain('Selected fixture')
    refresh.element.click()
    expect(getAllServerMetrics).toHaveBeenCalledTimes(2)
  })
})
