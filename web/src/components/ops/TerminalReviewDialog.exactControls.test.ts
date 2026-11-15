import { mount } from '@vue/test-utils'
import { expect, it } from 'vitest'
import TerminalReviewDialog from './TerminalReviewDialog.vue'

it('dismisses only direct clicks on the terminal review backdrop and never confirms them', async () => {
  const wrapper = mount(TerminalReviewDialog, {
    props: {
      title: 'Fixture terminal review', notice: 'Review only', source: 'Local fixture',
      text: 'Review text only', confirmLabel: 'Confirm fixture',
    },
  })
  try {
    const overlay=document.querySelector<HTMLElement>('.terminal-review-overlay')!
    const dialog=overlay.querySelector<HTMLElement>('.terminal-review')!
    expect(dialog.querySelector('[data-confirm]')).not.toBeNull()
    dialog.click()
    dialog.querySelector('textarea')!.click()
    expect(wrapper.emitted('close')).toBeUndefined()
    expect(wrapper.emitted('confirm')).toBeUndefined()
    overlay.click()
    expect(wrapper.emitted('close')).toEqual([[]])
    expect(wrapper.emitted('confirm')).toBeUndefined()
    expect(wrapper.emitted('update:text')).toBeUndefined()
  } finally { wrapper.unmount() }
})
