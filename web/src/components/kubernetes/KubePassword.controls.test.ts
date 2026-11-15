import { flushPromises, mount } from '@vue/test-utils'
import { ref } from 'vue'
import { expect, it, vi } from 'vitest'
import KubePassword from './KubePassword.vue'
import type { useKubeMutation } from '../../composables/useKubeMutation'

it('cancels or verifies the current Kubernetes password form only from its own controls', async () => {
  const state = {
    busy: ref(false),
    passwordOpen: ref(true),
    password: ref(''),
    error: ref(''),
    uncertain: ref(false),
    execute: vi.fn(),
    verify: vi.fn(async () => {}),
    cancel: vi.fn(),
    reset: vi.fn(),
  } satisfies ReturnType<typeof useKubeMutation>
  const prototype = HTMLDialogElement.prototype
  const methods = {
    showModal: Object.getOwnPropertyDescriptor(prototype, 'showModal'),
    close: Object.getOwnPropertyDescriptor(prototype, 'close'),
  }
  Object.defineProperties(prototype, {
    showModal: { configurable: true, value(this: HTMLDialogElement) { this.setAttribute('open', '') } },
    close: { configurable: true, value(this: HTMLDialogElement) { this.removeAttribute('open') } },
  })
  const wrapper = mount(KubePassword, { attachTo: document.body, props: { state } })
  try {
    await flushPromises()
    await wrapper.get('footer button:not([type="submit"])').trigger('click')
    expect(state.cancel).toHaveBeenCalledOnce()
    expect(state.verify).not.toHaveBeenCalled()
    await wrapper.get('input[type="password"]').setValue('synthetic-acceptance-phrase')
    wrapper.get<HTMLButtonElement>('footer button[type="submit"]').element.click()
    await flushPromises()
    expect(state.verify).toHaveBeenCalledOnce()
    expect(state.password.value).toBe('synthetic-acceptance-phrase')
    expect(state.execute).not.toHaveBeenCalled()
  } finally {
    wrapper.unmount()
    for (const [name, descriptor] of Object.entries(methods)) {
      if (descriptor) Object.defineProperty(prototype, name, descriptor)
      else Reflect.deleteProperty(prototype, name)
    }
  }
})
