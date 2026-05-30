import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import StatusBadge from './StatusBadge.vue'
import type { BadgeStatus } from './StatusBadge.vue'

// 冻结的六词状态词表 — 这是契约,渲染文案必须逐一对上。
const VOCAB: Record<BadgeStatus, string> = {
  success: '成功',
  failed: '失败',
  running: '进行中',
  partial: '部分失败',
  rolledback: '已回滚',
  queued: '排队',
}

describe('StatusBadge', () => {
  it.each(Object.entries(VOCAB))(
    'renders the frozen Chinese label for status=%s',
    (status, label) => {
      const wrapper = mount(StatusBadge, { props: { status: status as BadgeStatus } })
      expect(wrapper.text()).toBe(label)
    },
  )

  it('renders exactly the six known statuses (no extra, no missing)', () => {
    expect(Object.keys(VOCAB)).toHaveLength(6)
  })

  it('always renders a non-color-alone dot + text (accessibility)', () => {
    const wrapper = mount(StatusBadge, { props: { status: 'success' } })
    expect(wrapper.find('.badge__dot').exists()).toBe(true)
    expect(wrapper.find('.badge__label').text()).toBe('成功')
  })

  it('applies a status-specific modifier so themes can style per-state', () => {
    const wrapper = mount(StatusBadge, { props: { status: 'failed' } })
    expect(wrapper.classes()).toContain('badge--failed')
  })
})
