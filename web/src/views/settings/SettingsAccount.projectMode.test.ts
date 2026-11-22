import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, disposePinia, setActivePinia, type Pinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { setLocale, SUPPORTED_LOCALES, i18n } from '../../i18n'
import { usePermissionStore } from '../../stores/permissions'
import { useProjectModeStore } from '../../stores/projectMode'
import { useSessionStore } from '../../stores/session'
import SettingsAccount from './SettingsAccount.vue'

vi.mock('vue-router', () => ({ useRouter: () => ({ push: vi.fn(), replace: vi.fn() }) }))
vi.mock('../../api/account', () => ({ listSessions: async () => [] }))
vi.mock('../../api/access', () => ({ access: { sessions: async () => [] } }))

const alice = { username: 'alice', userId: 'alice-id', multiTenant: true }
const bob = { username: 'bob', userId: 'bob-id', multiTenant: true }
const sectionSelector = '[aria-labelledby="acct-preferences-h"]'
const fetchProjectMode = vi.fn<typeof fetch>()
let pinia: Pinia
let wrapper: VueWrapper | undefined

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

function delayedResponse(): { promise: Promise<Response>; resolve: (value: Response) => void } {
  let resolve!: (value: Response) => void
  const promise = new Promise<Response>(accept => { resolve = accept })
  return { promise, resolve }
}

function render() {
  wrapper = mount(SettingsAccount, {
    attachTo: document.body,
    global: { plugins: [pinia], stubs: { SettingsPlatformLogin: true } },
  })
  return wrapper
}

function preferenceForm() {
  return wrapper!.get('section' + sectionSelector)
}

beforeEach(() => {
  pinia = createPinia()
  setActivePinia(pinia)
  useSessionStore().setUser(alice)
  setLocale('en')
  fetchProjectMode.mockReset()
  vi.stubGlobal('fetch', fetchProjectMode)
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  disposePinia(pinia)
  vi.unstubAllGlobals()
  document.cookie = 'pipewright_csrf=; Max-Age=0; path=/'
  setLocale('zh-CN')
})

describe('account project display mode', () => {
  it('updates the effective mode only after explicit save returns, using current-account CSRF', async () => {
    const pending = delayedResponse()
    document.cookie = 'pipewright_csrf=project-mode-fixture; path=/'
    fetchProjectMode.mockResolvedValueOnce(json({ mode: 'single' })).mockReturnValueOnce(pending.promise)
    const permissions = usePermissionStore()
    permissions.setTeam('another-team')
    render()
    await flushPromises()

    const store = useProjectModeStore()
    const form = preferenceForm()
    const save = form.get<HTMLButtonElement>('button[type=submit]')
    expect(form.get<HTMLInputElement>('input[value=single]').element.checked).toBe(true)
    expect(save.element.disabled).toBe(true)
    expect(fetchProjectMode).toHaveBeenNthCalledWith(1, '/api/account/project-mode', expect.objectContaining({ method: 'GET' }))

    await form.get('input[value=multi]').setValue()
    expect(store.mode).toBe('single')
    expect(fetchProjectMode).toHaveBeenCalledTimes(1)
    save.element.click()
    await form.get('form').trigger('submit')
    expect(store.mode).toBe('single')
    expect(save.element.disabled).toBe(true)
    expect(fetchProjectMode).toHaveBeenCalledTimes(2)
    const [path, init] = fetchProjectMode.mock.calls[1]!
    expect(path).toBe('/api/account/project-mode')
    expect(init).toMatchObject({ method: 'PUT', body: '{"mode":"multi"}', credentials: 'same-origin' })
    expect(new Headers(init?.headers).get('X-CSRF-Token')).toBe('project-mode-fixture')
    expect(new Headers(init?.headers).has('X-Pipewright-Team')).toBe(false)

    pending.resolve(json({ mode: 'multi' }))
    await flushPromises()
    expect(store.mode).toBe('multi')
    expect(form.get('[role=status]').text()).toBe('Project display mode saved.')
  })

  it('preserves the saved mode and allows retry after a failed save', async () => {
    fetchProjectMode
      .mockResolvedValueOnce(json({ mode: 'single' }))
      .mockResolvedValueOnce(json({ error: { code: 'write_failed', message: 'try again' } }, 500))
      .mockResolvedValueOnce(json({ mode: 'multi' }))
    render()
    await flushPromises()

    const form = preferenceForm()
    const store = useProjectModeStore()
    await form.get('input[value=multi]').setValue()
    form.get<HTMLButtonElement>('button[type=submit]').element.click()
    await flushPromises()

    expect(store.mode).toBe('single')
    expect(form.get<HTMLInputElement>('input[value=multi]').element.checked).toBe(true)
    expect(form.get('[role=alert]').text()).toContain('The saved mode remains active.')
    form.get<HTMLButtonElement>('button[type=submit]').element.click()
    await flushPromises()
    expect(store.mode).toBe('multi')
    expect(form.find('[role=alert]').exists()).toBe(false)
  })

  it('keeps the default during a failed read and reloads the saved mode on retry', async () => {
    fetchProjectMode.mockResolvedValueOnce(json({ mode: 'invalid' })).mockResolvedValueOnce(json({ mode: 'multi' }))
    render()
    await flushPromises()

    const form = preferenceForm()
    const store = useProjectModeStore()
    expect(store.mode).toBe('single')
    expect(form.get('fieldset').attributes('disabled')).toBeDefined()
    await form.get('[role=alert] button').trigger('click')
    await flushPromises()
    expect(store.mode).toBe('multi')
    expect(form.get<HTMLInputElement>('input[value=multi]').element.checked).toBe(true)
  })

  it('ignores a late read from the previous account and shares the mode across teams', async () => {
    const pending = delayedResponse()
    fetchProjectMode.mockReturnValueOnce(pending.promise).mockResolvedValueOnce(json({ mode: 'single' }))
    const store = useProjectModeStore()
    const oldSignal = fetchProjectMode.mock.calls[0]?.[1]?.signal
    useSessionStore().setUser(bob)
    await flushPromises()
    expect(oldSignal?.aborted).toBe(true)

    pending.resolve(json({ mode: 'multi' }))
    usePermissionStore().setTeam('team-b')
    await flushPromises()
    expect(store.loaded).toBe(true)
    expect(store.mode).toBe('single')
    expect(fetchProjectMode).toHaveBeenCalledTimes(2)
  })

  it('clears mode at sign-out and ignores a late save after the same account signs in again', async () => {
    const pending = delayedResponse()
    fetchProjectMode
      .mockResolvedValueOnce(json({ mode: 'multi' }))
      .mockReturnValueOnce(pending.promise)
      .mockResolvedValueOnce(json({ mode: 'multi' }))
    const session = useSessionStore()
    const store = useProjectModeStore()
    await flushPromises()
    expect(store.mode).toBe('multi')

    const saving = store.save('single')
    const oldSignal = fetchProjectMode.mock.calls[1]?.[1]?.signal
    session.clearSession()
    expect(store.mode).toBe('single')
    expect(store.available).toBe(false)
    session.setUser(alice)
    await flushPromises()
    expect(oldSignal?.aborted).toBe(true)
    expect(store.mode).toBe('multi')

    pending.resolve(json({ mode: 'single' }))
    await saving
    expect(store.mode).toBe('multi')
    expect(store.saveStatus).toBe('idle')
  })

  it('waits for a usable session and renders both options in all supported locales', async () => {
    useSessionStore().setUser({ ...alice, mustChangePassword: true })
    fetchProjectMode.mockResolvedValueOnce(json({ mode: 'single' }))
    const page = render()
    expect(page.find('section' + sectionSelector).exists()).toBe(false)
    expect(fetchProjectMode).not.toHaveBeenCalled()
    useSessionStore().setUser(alice)
    await flushPromises()

    for (const { code } of SUPPORTED_LOCALES) {
      setLocale(code)
      await flushPromises()
      const form = preferenceForm()
      for (const key of ['title', 'single', 'multi', 'singleDescription', 'multiDescription', 'save']) {
        expect(i18n.global.te('settingsAccount.projectMode.' + key, code)).toBe(true)
        expect(form.text()).toContain(i18n.global.t('settingsAccount.projectMode.' + key))
      }
    }
    expect(fetchProjectMode).toHaveBeenCalledTimes(1)
  })
})
