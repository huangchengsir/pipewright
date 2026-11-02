import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import DashServers from './DashServers.vue'
import { i18n } from '../../i18n'
import type { Server, ServerMetrics } from '../../api/servers'
vi.mock('vue-router', () => ({ useRouter: () => ({ push: vi.fn() }) }))
describe('actual server card sample states', () => {
 it.each([null, false, true])('distinguishes reachability %s without contradictory body copy', reachable => {
  const server = { id: 's', name: 'S', host: 'test' } as Server
  const metrics = reachable === null ? [] : [{ serverId: 's', reachable, error: '' }] as ServerMetrics[]
  const w = mount(DashServers, { props: { servers: [server], metrics, loading: false } })
  if (reachable === null) { expect(w.find('.srv-state').text()).toBe(i18n.global.t('runExperience.unknown')); expect(w.text()).not.toContain(i18n.global.t('dash.unreachable')); expect(w.find('.srv-err').exists()).toBe(false) }
  else if (!reachable) expect(w.find('.srv-err').text()).toBe(i18n.global.t('dash.unreachable'))
  else { expect(w.find('.srv-state').text()).toBe(i18n.global.t('dash.online')); expect(w.find('.srv-err').exists()).toBe(false) }
  w.unmount()
 })
})
