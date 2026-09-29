import { defineComponent, h, nextTick, ref } from 'vue'
import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import type { Environment } from '../../api/pipelineSettings'
import EnvCredsTab from './EnvCredsTab.vue'

function environment(): Environment {
  return {
    id: '',
    name: '',
    targetServerIds: [],
    envVars: [{ id: '', key: '', secret: false, value: '' }],
    imageRegistry: { type: 'custom', url: '' },
  }
}

describe('EnvCredsTab', () => {
  it.each([
    '环境名',
    '目标服务器(逗号分隔)',
    '变量键',
    '变量值',
    '镜像仓库地址',
  ])('keeps focus while the parent echoes edits to %s', async (label) => {
    const Parent = defineComponent({
      setup() {
        const environments = ref<Environment[]>([environment()])
        return () => h(EnvCredsTab, {
          environments: environments.value,
          credentials: [],
          onUpdate: (next: Environment[]) => { environments.value = next },
        })
      },
    })
    const wrapper = mount(Parent, { attachTo: document.body })
    try {
      const input = [...wrapper.element.querySelectorAll('input')].find(
        (element) => element.getAttribute('aria-label') === label,
      ) as HTMLInputElement
      expect(input).toBeDefined()
      input.focus()
      input.value = 'a'
      input.dispatchEvent(new Event('input', { bubbles: true }))
      await nextTick()
      await nextTick()

      expect(document.activeElement).toBe(input)
      expect(input.isConnected).toBe(true)
      input.value += 'b'
      input.dispatchEvent(new Event('input', { bubbles: true }))
      await nextTick()
      expect(input.value).toBe('ab')
    } finally {
      wrapper.unmount()
    }
  })

  it('refreshes the form when an external environment really changes', async () => {
    const wrapper = mount(EnvCredsTab, {
      props: { environments: [environment()], credentials: [] },
    })
    try {
      const external = { ...environment(), name: 'production' }
      await wrapper.setProps({ environments: [external] })
      expect((wrapper.find('input[aria-label="环境名"]').element as HTMLInputElement).value).toBe('production')
    } finally {
      wrapper.unmount()
    }
  })

  it('refreshes a secret mask supplied by an external update', async () => {
    const initial = environment()
    initial.envVars = [{ id: 'var-1', key: 'TOKEN', secret: true, credentialId: 'cred-1', maskedValue: '••••_old' }]
    const wrapper = mount(EnvCredsTab, {
      props: { environments: [initial], credentials: [] },
    })
    try {
      expect(wrapper.text()).toContain('••••_old')
      await wrapper.setProps({
        environments: [{ ...initial, envVars: [{ ...initial.envVars[0], maskedValue: '••••_new' }] }],
      })
      expect(wrapper.text()).toContain('••••_new')
    } finally {
      wrapper.unmount()
    }
  })
})
