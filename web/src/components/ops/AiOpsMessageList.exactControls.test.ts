import { mount } from '@vue/test-utils'
import { expect, it } from 'vitest'
import { t } from '../../i18n'
import AiOpsMessageList from './AiOpsMessageList.vue'

it('requests older messages only from the visible enabled history button', async () => {
  const wrapper = mount(AiOpsMessageList, {
    props: {
      entries: [{ sessionId: 'fixture', seq: 7, kind: 'assistant', text: 'Current retained result', createdAt: '' }],
      calls: {}, disabled: false, modelAvailable: false, hasHistory: false,
    },
  })
  try {
    const older = () => wrapper.findAll('button').find(item => item.text() === t('opsChat.older'))
    expect(older()).toBeUndefined()
    await wrapper.setProps({ hasHistory: true, disabled: true })
    expect(older()?.attributes('disabled')).toBeDefined()
    ;(older()!.element as HTMLButtonElement).click()
    expect(wrapper.emitted('history')).toBeUndefined()
    await wrapper.setProps({ disabled: false })
    await older()!.trigger('click')
    expect(wrapper.emitted('history')).toEqual([[]])
    expect(wrapper.text()).toContain('Current retained result')
    expect(wrapper.emitted('copy')).toBeUndefined()
    expect(wrapper.emitted('analyze')).toBeUndefined()
    expect(wrapper.emitted('analyzeRun')).toBeUndefined()
  } finally { wrapper.unmount() }
})
