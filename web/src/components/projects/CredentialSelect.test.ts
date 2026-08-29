import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import CredentialSelect from './CredentialSelect.vue'
import type { Credential } from '../../api/credentials'

const credentials: Credential[] = [
  {
    id: 'cred-1', name: '部署账号', type: 'git_http', scope: '', username: 'deploy',
    maskedValue: '••••1234', lastUsedAt: null, createdAt: '2026-01-01T00:00:00Z',
  },
  {
    id: 'cred-2', name: '备用账号', type: 'git_token', scope: '', username: 'backup',
    maskedValue: 'ghp_••••5678', lastUsedAt: null, createdAt: '2026-01-02T00:00:00Z',
  },
]

function mountSelect() {
  return mount(CredentialSelect, {
    props: {
      inputId: 'project-credential', modelValue: '', credentials,
      placeholder: '选择凭据', loadingLabel: '加载中', emptyLabel: '暂无凭据',
    },
  })
}

describe('CredentialSelect', () => {
  it('opens a masked, accessible option list and emits the selected id', async () => {
    const wrapper = mountSelect()
    const trigger = wrapper.get('[role="combobox"]')

    await trigger.trigger('click')

    expect(wrapper.get('[role="listbox"]').text()).toContain('部署账号')
    expect(wrapper.get('[role="listbox"]').text()).toContain('••••1234')
    expect(wrapper.get('[role="listbox"]').text()).toContain('deploy')
    expect(wrapper.get('[role="listbox"]').text()).not.toContain('deploy-secret')

    await wrapper.get('[role="option"]').trigger('click')
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual(['cred-1'])
    expect(wrapper.emitted('change')).toHaveLength(1)
    expect(wrapper.find('[role="listbox"]').exists()).toBe(false)
  })

  it('moves through options with keyboard controls', async () => {
    const wrapper = mountSelect()
    const trigger = wrapper.get('[role="combobox"]')

    await trigger.trigger('keydown', { key: 'ArrowDown' })
    await trigger.trigger('keydown', { key: 'ArrowDown' })
    await trigger.trigger('keydown', { key: 'Enter' })

    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual(['cred-2'])
  })
})
