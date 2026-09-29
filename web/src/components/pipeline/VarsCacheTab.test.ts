import { defineComponent, h, nextTick, ref } from 'vue'
import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import type { BuildConfig } from '../../api/pipelineSettings'
import VarsCacheTab from './VarsCacheTab.vue'

function build(): BuildConfig {
  return {
    model: 'dockerfile',
    dockerfilePath: 'Dockerfile',
    toolchain: { language: '', version: '' },
    artifactType: 'image',
    vars: [],
    cache: { enabled: false, paths: [] },
  }
}

describe('VarsCacheTab', () => {
  it.each(['变量键', '变量值'])('keeps focus on a newly added %s while the parent echoes edits', async (label) => {
    const Parent = defineComponent({
      setup() {
        const config = ref<BuildConfig>(build())
        return () => h(VarsCacheTab, {
          build: config.value,
          credentials: [],
          onUpdate: (next: BuildConfig) => { config.value = next },
        })
      },
    })
    const wrapper = mount(Parent, { attachTo: document.body })
    try {
      await wrapper.find('button.addbtn').trigger('click')
      await nextTick()
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

  it('refreshes genuinely different external build variables', async () => {
    const wrapper = mount(VarsCacheTab, { props: { build: build(), credentials: [] } })
    try {
      await wrapper.setProps({
        build: { ...build(), vars: [{ id: 'var-1', key: 'NAME', secret: false, value: 'external' }] },
      })
      expect((wrapper.find('input[aria-label="变量值"]').element as HTMLInputElement).value).toBe('external')
    } finally {
      wrapper.unmount()
    }
  })

  it('refreshes a secret mask supplied by an external update', async () => {
    const initial = build()
    initial.vars = [{ id: 'var-1', key: 'TOKEN', secret: true, credentialId: 'cred-1', maskedValue: '••••_old' }]
    const wrapper = mount(VarsCacheTab, { props: { build: initial, credentials: [] } })
    try {
      expect(wrapper.text()).toContain('••••_old')
      await wrapper.setProps({
        build: { ...initial, vars: [{ ...initial.vars[0], maskedValue: '••••_new' }] },
      })
      expect(wrapper.text()).toContain('••••_new')
    } finally {
      wrapper.unmount()
    }
  })
})
