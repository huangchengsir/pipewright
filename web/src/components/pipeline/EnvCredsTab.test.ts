import { defineComponent, h, nextTick, ref } from 'vue'
import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import type { Environment } from '../../api/pipelineSettings'
import type { Server } from '../../api/servers'
import type { Credential } from '../../api/credentials'
import AppSelect from '../ui/AppSelect.vue'
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

function server(id: string, name = 'Production', host = '192.0.2.10'): Server {
  return { id, name, host, port: 22, user: 'deploy', credentialId: 'ssh-1', credentialName: 'SSH', createdAt: '', updatedAt: '' }
}

function credential(id: string, type: Credential['type']): Credential {
  return { id, type, name: id, scope: '', username: '', maskedValue: '••••', lastUsedAt: null, createdAt: '' }
}

function mountParent(initial: Environment, servers: Server[], credentials: Credential[] = []) {
  const environments = ref<Environment[]>([initial])
  const Parent = defineComponent({
    setup() {
      return () => h(EnvCredsTab, {
        environments: environments.value,
        credentials,
        servers,
        onUpdate: (next: Environment[]) => { environments.value = next },
      })
    },
  })
  return { wrapper: mount(Parent, { attachTo: document.body }), environments }
}

describe('EnvCredsTab', () => {
  it.each([
    '环境名',
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

  it('selects servers by checkbox and keeps focus across parent echoes', async () => {
    const initial = environment()
    const { wrapper, environments } = mountParent(initial, [server('srv-1'), server('srv-2', 'Staging', '192.0.2.20')])
    try {
      const first = wrapper.get('input[type="checkbox"][value="srv-1"]')
      const element = first.element as HTMLInputElement
      expect(first.element.closest('label')?.textContent).toContain('Production')
      expect(first.element.closest('label')?.textContent).toContain('192.0.2.10')
      element.focus()
      await first.setValue(true)
      await nextTick()
      expect(environments.value[0].targetServerIds).toEqual(['srv-1'])
      expect(initial.targetServerIds).toEqual([])
      expect(document.activeElement).toBe(element)
      expect(wrapper.get('input[value="srv-1"]').element).toBe(element)
      expect(element.checked).toBe(true)

      await wrapper.get('input[value="srv-2"]').setValue(true)
      await nextTick()
      expect(environments.value[0].targetServerIds).toEqual(['srv-1', 'srv-2'])
      await first.setValue(false)
      await nextTick()
      expect(environments.value[0].targetServerIds).toEqual(['srv-2'])
      expect(wrapper.findComponent(EnvCredsTab).emitted('update')).toHaveLength(3)
    } finally {
      wrapper.unmount()
    }
  })

  it('retains invisible selected server IDs through edits and allows explicit cancellation', async () => {
    const initial = { ...environment(), targetServerIds: ['hidden-1', 'deleted-2'] }
    const { wrapper, environments } = mountParent(initial, [server('srv-1')])
    try {
      const hidden = wrapper.get('input[value="hidden-1"]')
      expect((hidden.element as HTMLInputElement).checked).toBe(true)
      expect(hidden.element.closest('label')?.textContent).toContain('无法访问或已删除')
      expect(wrapper.findComponent(EnvCredsTab).emitted('update')).toBeUndefined()
      await wrapper.get('input[value="srv-1"]').setValue(true)
      await wrapper.get('input[aria-label="环境名"]').setValue('production')
      expect(environments.value[0].targetServerIds).toEqual(['hidden-1', 'deleted-2', 'srv-1'])
      await hidden.setValue(false)
      await nextTick()
      expect(environments.value[0].targetServerIds).toEqual(['deleted-2', 'srv-1'])
      expect(wrapper.find('input[value="hidden-1"]').exists()).toBe(false)
      expect((wrapper.get('input[value="deleted-2"]').element as HTMLInputElement).checked).toBe(true)
    } finally {
      wrapper.unmount()
    }
  })

  it('does not emit or clear selected IDs when server visibility changes', async () => {
    const initial = { ...environment(), targetServerIds: ['srv-1'] }
    const wrapper = mount(EnvCredsTab, { props: { environments: [initial], credentials: [], servers: [server('srv-1')] } })
    try {
      const checkbox = wrapper.get('input[value="srv-1"]').element
      await wrapper.setProps({ servers: [] })
      expect(wrapper.text()).toContain('没有可选服务器')
      expect(wrapper.text()).toContain('无法访问或已删除')
      expect((wrapper.get('input[value="srv-1"]').element as HTMLInputElement).checked).toBe(true)
      await wrapper.setProps({ servers: [server('srv-1', 'Renamed')] })
      expect(wrapper.text()).toContain('Renamed')
      expect(wrapper.text()).not.toContain('无法访问或已删除')
      expect(wrapper.get('input[value="srv-1"]').element).toBe(checkbox)
      expect(wrapper.emitted('update')).toBeUndefined()
      expect(initial.targetServerIds).toEqual(['srv-1'])
    } finally {
      wrapper.unmount()
    }
  })

  it('reflects genuinely changed parent selections and isolates environments', async () => {
    const first = { ...environment(), id: 'prod', targetServerIds: ['srv-1'] }
    const second = { ...environment(), id: 'test', targetServerIds: ['hidden-1'] }
    const wrapper = mount(EnvCredsTab, { props: { environments: [first, second], credentials: [], servers: [server('srv-1')] } })
    try {
      await wrapper.findAll('article')[1].get('input[value="srv-1"]').setValue(true)
      expect((wrapper.emitted('update')!.at(-1)![0] as Environment[]).map((env) => env.targetServerIds)).toEqual([['srv-1'], ['hidden-1', 'srv-1']])
      await wrapper.setProps({ environments: [{ ...first, targetServerIds: [] }, second] })
      expect((wrapper.findAll('article')[0].get('input[value="srv-1"]').element as HTMLInputElement).checked).toBe(false)
      expect((wrapper.findAll('article')[1].get('input[value="hidden-1"]').element as HTMLInputElement).checked).toBe(true)
    } finally {
      wrapper.unmount()
    }
  })

  it.each([undefined, []])('shows an honest empty state when servers are %s', (servers) => {
    const wrapper = mount(EnvCredsTab, { props: { environments: [environment()], credentials: [], servers } })
    try {
      expect(wrapper.text()).toContain('没有可选服务器')
      expect(wrapper.find('input[type="checkbox"]').exists()).toBe(false)
      expect(wrapper.text()).not.toContain('4-1')
      expect(wrapper.emitted('update')).toBeUndefined()
    } finally {
      wrapper.unmount()
    }
  })

  it('disables visible and unavailable server checkboxes without emitting changes', async () => {
    const initial = { ...environment(), targetServerIds: ['hidden-1'] }
    const wrapper = mount(EnvCredsTab, { props: { environments: [initial], credentials: [], servers: [server('srv-1')], disabled: true } })
    try {
      for (const checkbox of wrapper.findAll('input[type="checkbox"]')) {
        expect((checkbox.element as HTMLInputElement).disabled).toBe(true)
        await checkbox.setValue(!(checkbox.element as HTMLInputElement).checked)
        await checkbox.trigger('change')
      }
      expect(wrapper.findAllComponents(AppSelect).every((select) => select.props('disabled'))).toBe(true)
      expect(wrapper.emitted('update')).toBeUndefined()
      expect(initial.targetServerIds).toEqual(['hidden-1'])
    } finally {
      wrapper.unmount()
    }
  })

  it.each(['missing-registry', 'wrong-type'])('preserves and warns about unavailable registry reference %s', async (id) => {
    const initial = { ...environment(), imageRegistry: { type: 'custom' as const, url: 'registry.local', credentialId: id } }
    const { wrapper, environments } = mountParent(initial, [], [credential('registry-1', 'registry'), credential('wrong-type', 'ssh_key')])
    try {
      const select = wrapper.findAllComponents(AppSelect).find((component) => component.props('ariaLabel') === '镜像仓库凭据')!
      expect(select.props('modelValue')).toBe(id)
      expect(select.props('options').map((option: { value: string }) => option.value)).toEqual(['', 'registry-1', id])
      expect(select.text()).toContain(id)
      expect(wrapper.get('[role="status"]').text()).toContain('引用已保留')
      expect(wrapper.findComponent(EnvCredsTab).emitted('update')).toBeUndefined()
      await wrapper.get('input[aria-label="环境名"]').setValue('production')
      expect(environments.value[0].imageRegistry.credentialId).toBe(id)
      await select.get('button[role="combobox"]').trigger('click')
      const options = [...document.querySelectorAll<HTMLButtonElement>('button[role="option"]')]
      options.find((option) => option.textContent?.includes('registry-1'))!.click()
      await nextTick()
      await nextTick()
      expect(environments.value[0].imageRegistry.credentialId).toBe('registry-1')
      expect(wrapper.find('[role="status"]').exists()).toBe(false)
    } finally {
      wrapper.unmount()
    }
  })

  it('offers only registry credentials and preserves a reference when access disappears', async () => {
    const initial = { ...environment(), imageRegistry: { type: 'custom' as const, url: '', credentialId: 'registry-1' } }
    const wrapper = mount(EnvCredsTab, { props: { environments: [initial], credentials: [credential('registry-1', 'registry'), credential('git-1', 'git_token')] } })
    try {
      const select = wrapper.findAllComponents(AppSelect).find((component) => component.props('ariaLabel') === '镜像仓库凭据')!
      expect(select.props('options').map((option: { value: string }) => option.value)).toEqual(['', 'registry-1'])
      expect(wrapper.find('[role="status"]').exists()).toBe(false)
      await wrapper.setProps({ credentials: [] })
      expect(select.props('modelValue')).toBe('registry-1')
      expect(select.text()).toContain('registry-1')
      expect(wrapper.get('[role="status"]').text()).toContain('引用已保留')
      expect(wrapper.emitted('update')).toBeUndefined()
      await select.get('button[role="combobox"]').trigger('click')
      document.querySelector<HTMLButtonElement>('button[role="option"]')!.click()
      await nextTick()
      expect((wrapper.emitted('update')!.at(-1)![0] as Environment[])[0].imageRegistry.credentialId).toBeUndefined()
      expect(initial.imageRegistry.credentialId).toBe('registry-1')
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
