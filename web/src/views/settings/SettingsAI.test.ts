import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import SettingsAI from './SettingsAI.vue'
import AppSelect from '../../components/ui/AppSelect.vue'

const api = vi.hoisted(() => ({
  get: vi.fn(),
  save: vi.fn(),
  test: vi.fn(),
  models: vi.fn(),
}))
vi.mock('../../api/aiSettings', () => ({
  getAISettings: api.get,
  saveAISettings: api.save,
  testAIConnection: api.test,
  listAIModels: api.models,
}))
vi.mock('../../composables/useToast', () => ({
  useToast: () => ({ success: vi.fn(), error: vi.fn() }),
}))
let wrapper: VueWrapper | undefined
beforeEach(() => {
  vi.useFakeTimers()
  vi.resetAllMocks()
  api.models.mockResolvedValue({ ok: false, items: [], error: 'Unauthorized' })
  api.get.mockResolvedValue({
    provider: 'openai',
    baseUrl: 'https://company.example/api/v1',
    model: 'manual/model',
    configured: true,
    enabled: true,
    apiKeyMasked: 'masked',
    budget: { monthlyTokenLimit: null },
    updatedAt: null,
  })
})
afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  vi.useRealTimers()
})
async function setup() {
  wrapper = mount(SettingsAI)
  await flushPromises()
  return wrapper
}

describe('draft model configuration', () => {
  it.each([
    ['gemini', 'https://generativelanguage.googleapis.com/v1beta/openai'],
    ['glm', 'https://open.bigmodel.cn/api/paas/v4'],
    ['kimi', 'https://api.moonshot.cn/v1'],
    ['mimo', 'https://api.xiaomimimo.com/v1'],
  ])(
    'configures %s as OpenAI compatible and automatically fetches its catalog',
    async (vendor, url) => {
      api.get.mockResolvedValue({
        provider: '',
        baseUrl: '',
        model: '',
        configured: false,
        enabled: false,
        apiKeyMasked: '',
        budget: { monthlyTokenLimit: null },
        updatedAt: null,
      })
      api.models.mockResolvedValue({
        ok: true,
        items: [{ id: 'exact/model:1', name: 'Model' }],
      })
      const w = await setup()
      const select = w.findAllComponents(AppSelect)[0]!
      select.vm.$emit('update:modelValue', vendor)
      select.vm.$emit('change', vendor)
      await flushPromises()
      expect((w.get('#ai-baseurl').element as HTMLInputElement).value).toBe(url)
      await vi.advanceTimersByTimeAsync(1000)
      expect(api.models).not.toHaveBeenCalled()
      await w.get('#ai-model').setValue('manual/model')
      await w.get('#ai-apikey').setValue('mock-key')
      await vi.advanceTimersByTimeAsync(600)
      expect(api.models).toHaveBeenCalledExactlyOnceWith(
        { provider: 'openai', baseUrl: url, apiKey: 'mock-key' },
        expect.any(AbortSignal),
      )
      expect(w.findAllComponents(AppSelect)[1]!.props('options')[0].value).toBe(
        'exact/model:1',
      )
      expect((w.get('#ai-model').element as HTMLInputElement).value).toBe(
        'manual/model',
      )
    },
  )
  it('clears an unsaved key when a preset changes its destination', async () => {
    api.get.mockResolvedValue({
      provider: 'openai',
      baseUrl: 'https://api.moonshot.cn/v1/',
      model: 'manual/model',
      configured: true,
      enabled: true,
      apiKeyMasked: 'masked',
      budget: { monthlyTokenLimit: null },
      updatedAt: null,
    })
    const w = await setup()
    await w.get('#ai-apikey').setValue('kimi-test-key')
    const preset = w.findAllComponents(AppSelect)[0]!
    preset.vm.$emit('update:modelValue', 'mimo')
    preset.vm.$emit('change', 'mimo')
    await flushPromises()
    expect((w.get('#ai-apikey').element as HTMLInputElement).value).toBe('')
    expect((w.get('#ai-baseurl').element as HTMLInputElement).value).toBe(
      'https://api.xiaomimimo.com/v1',
    )
    await vi.advanceTimersByTimeAsync(1000)
    expect(api.models).not.toHaveBeenCalled()
  })
  it.each([
    'https://generativelanguage.googleapis.com/v1beta/openai',
    'https://open.bigmodel.cn/api/paas/v4',
    'https://open.bigmodel.cn/api/coding/paas/v4',
  ])('does not reuse a saved key when /v1 is appended to %s', async (url) => {
    api.get.mockResolvedValue({
      provider: 'openai',
      baseUrl: url.replace('https:', 'HTTPS:'),
      model: 'manual/model',
      configured: true,
      enabled: true,
      apiKeyMasked: 'masked',
      budget: { monthlyTokenLimit: null },
      updatedAt: null,
    })
    const w = await setup()
    await w.get('#ai-baseurl').setValue(url + '/v1')
    await vi.advanceTimersByTimeAsync(1000)
    expect(api.models).not.toHaveBeenCalled()
    expect(
      w.get('button[title="获取模型"]').attributes('disabled'),
    ).toBeDefined()
  })
  it('automatically fetches saved configuration, keeps custom URLs and manual model values on failure', async () => {
    const w = await setup()
    expect(api.models).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(600)
    expect(api.models).toHaveBeenCalledTimes(1)
    const preset = w.findAllComponents(AppSelect)[0]!
    preset.vm.$emit('update:modelValue', 'openrouter')
    preset.vm.$emit('change', 'openrouter')
    await flushPromises()
    expect((w.get('#ai-baseurl').element as HTMLInputElement).value).toBe(
      'https://company.example/api/v1',
    )
    api.models.mockResolvedValue({
      ok: false,
      items: [],
      error: 'Unauthorized',
    })
    await w.get('button[title="获取模型"] ').trigger('click')
    await flushPromises()
    expect(api.models).toHaveBeenCalledWith(
      { provider: 'openai', baseUrl: 'https://company.example/api/v1' },
      expect.any(AbortSignal),
    )
    expect((w.get('#ai-model').element as HTMLInputElement).value).toBe(
      'manual/model',
    )
    expect(w.text()).toContain('Unauthorized')
  })
  it('drops stale responses and aborts pending fetches on destination change and unmount', async () => {
    const w = await setup()
    let resolve!: (value: unknown) => void
    api.models.mockImplementation(
      () =>
        new Promise((r) => {
          resolve = r
        }),
    )
    await w.get('button[title="获取模型"]').trigger('click')
    const signal = api.models.mock.calls[0]![1] as AbortSignal
    await w.get('#ai-baseurl').setValue('https://new.example/v1')
    expect(signal.aborted).toBe(true)
    resolve({ ok: true, items: [{ id: 'old', name: 'Old' }] })
    await flushPromises()
    expect(w.findAllComponents(AppSelect)[1]!.props('options')).toEqual([])
    await w.get('#ai-apikey').setValue('new-test-key')
    await w.get('button[title="获取模型"]').trigger('click')
    const second = api.models.mock.calls[1]![1] as AbortSignal
    w.unmount()
    wrapper = undefined
    expect(second.aborted).toBe(true)
    resolve({ ok: true, items: [{ id: 'old', name: 'Old' }] })
    await flushPromises()
  })
  it('keeps fetched IDs exact and does not silently choose a model', async () => {
    const w = await setup()
    api.models.mockResolvedValue({
      ok: true,
      items: [{ id: 'provider/custom:1', name: 'Custom' }],
    })
    await w.get('button[title="获取模型"]').trigger('click')
    await flushPromises()
    const select = w.findAllComponents(AppSelect)[1]!
    expect(select.props('searchable')).toBe(true)
    expect(select.props('options')[0].value).toBe('provider/custom:1')
    expect((w.get('#ai-model').element as HTMLInputElement).value).toBe(
      'manual/model',
    )
    select.vm.$emit('update:modelValue', 'provider/custom:1')
    await flushPromises()
    expect((w.get('#ai-model').element as HTMLInputElement).value).toBe(
      'provider/custom:1',
    )
  })
  it('debounces edits and never automatically reuses a saved key at another destination', async () => {
    const w = await setup()
    await w.get('#ai-baseurl').setValue('https://new.example/v1')
    await vi.advanceTimersByTimeAsync(1000)
    expect(api.models).not.toHaveBeenCalled()
    await w.get('#ai-apikey').setValue('test')
    await vi.advanceTimersByTimeAsync(400)
    await w.get('#ai-apikey').setValue('test-key')
    await vi.advanceTimersByTimeAsync(599)
    expect(api.models).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(1)
    expect(api.models).toHaveBeenCalledTimes(1)
    expect(api.models.mock.calls[0]![0]).toEqual({
      provider: 'openai',
      baseUrl: 'https://new.example/v1',
      apiKey: 'test-key',
    })
    await vi.advanceTimersByTimeAsync(5000)
    expect(api.models).toHaveBeenCalledTimes(1)
  })
  it('does not fetch invalid URLs or after leaving the page', async () => {
    const w = await setup()
    await w.get('#ai-baseurl').setValue('https://user:password@example.com')
    await w.get('#ai-apikey').setValue('test-key')
    await vi.advanceTimersByTimeAsync(1000)
    expect(api.models).not.toHaveBeenCalled()
    await w.get('#ai-baseurl').setValue('https://new.example/v1')
    w.unmount()
    wrapper = undefined
    await vi.advanceTimersByTimeAsync(1000)
    expect(api.models).not.toHaveBeenCalled()
  })
  it('uses portals for both menus and supplies keyboard-accessible protocol guidance', async () => {
    const w = await setup()
    expect(w.findAllComponents(AppSelect).every((s) => s.props('portal'))).toBe(
      true,
    )
    const help = w.get('.protocol-help-button')
    await help.trigger('focusin')
    expect(w.get('[role="tooltip"]').isVisible()).toBe(true)
    expect(w.get('[role="tooltip"]').text()).toContain('DeepSeek')
    await help.trigger('keydown', { key: 'Escape' })
    expect(w.get('[role="tooltip"]').attributes('style')).toContain(
      'display: none',
    )
  })
})
