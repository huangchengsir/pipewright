import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import ServerMetricsCard from './ServerMetricsCard.vue'
import type { ServerMetrics } from '../../api/servers'

const metrics: ServerMetrics = {
  serverId: 'a',
  reachable: true,
  error: '',
  cpu: null,
  memory: null,
  disk: null,
  collectedAt: '2026-10-07T00:00:00Z',
}

describe('server metric card actions', () => {
  it.each([true, false])(
    'keeps optional actions inside the card when reachable=%s',
    (reachable) => {
      const w = mount(ServerMetricsCard, {
        props: { name: 'Server A', metrics: { ...metrics, reachable } },
        slots: { actions: '<button>AI assistant</button>' },
      })
      expect(w.get('article .metrics-card__actions button').text()).toBe(
        'AI assistant',
      )
      expect(w.findAll('.metrics-card__actions')).toHaveLength(1)
      w.unmount()
    },
  )
  it('does not add an empty actions area to other uses', () => {
    const w = mount(ServerMetricsCard, { props: { name: 'Server A', metrics } })
    expect(w.find('.metrics-card__actions').exists()).toBe(false)
    w.unmount()
  })
})
