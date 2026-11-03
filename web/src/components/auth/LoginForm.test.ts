import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import LoginForm from './LoginForm.vue'
import { HttpError } from '../../api/http'

const mocks = vi.hoisted(() => ({
  login: vi.fn(),
  setUser: vi.fn(),
  replace: vi.fn(),
  target: vi.fn(),
  route: { query: { redirect: '/projects?tab=recent' } },
}))
vi.mock('../../api/auth', () => ({ login: mocks.login }))
vi.mock('../../stores/session', () => ({
  useSessionStore: () => ({ setUser: mocks.setUser }),
}))
vi.mock('../../composables/useOnboarding', () => ({
  onboardingLoginTarget: mocks.target,
}))
vi.mock('vue-router', () => ({
  useRouter: () => ({ replace: mocks.replace }),
  useRoute: () => mocks.route,
}))

function render() {
  return mount(LoginForm, { global: { stubs: { PlatformLoginEntry: true } } })
}
async function fillAndSubmit(wrapper: ReturnType<typeof render>) {
  await wrapper.get('#username').setValue('  admin  ')
  await wrapper.get('#password').setValue('test-password')
  await wrapper.get('form').trigger('submit')
  await flushPromises()
}

beforeEach(() => {
  vi.resetAllMocks()
  mocks.target.mockResolvedValue('/projects?tab=recent')
  mocks.replace.mockResolvedValue(undefined)
  mocks.login.mockResolvedValue({ username: 'admin' })
})

describe('LoginForm', () => {
  it('renders labelled fields with password-manager autocomplete', () => {
    const wrapper = render()
    expect(wrapper.get('form').attributes('aria-label')).toBe('登录表单')
    expect(wrapper.get('label[for="username"]').text()).toBe('账号')
    expect(wrapper.get('label[for="password"]').text()).toBe('密码')
    expect(wrapper.get('#username').attributes('autocomplete')).toBe('username')
    expect(wrapper.get('#password').attributes('autocomplete')).toBe(
      'current-password',
    )
  })

  it('blocks empty submission and associates errors with each field', async () => {
    const wrapper = render()
    await wrapper.get('form').trigger('submit')
    expect(mocks.login).not.toHaveBeenCalled()
    expect(wrapper.get('#username-err').text()).toBe('请输入用户名')
    expect(wrapper.get('#password-err').text()).toBe('请输入密码')
    expect(wrapper.get('#username').attributes('aria-describedby')).toBe(
      'username-err',
    )
    expect(wrapper.get('#password').attributes('aria-invalid')).toBe('true')
    await wrapper.get('#username').setValue('admin')
    expect(wrapper.find('#username-err').exists()).toBe(false)
  })

  it('toggles password visibility without submitting', async () => {
    const wrapper = render()
    const toggle = wrapper.get('.reveal')
    await toggle.trigger('click')
    expect(wrapper.get('#password').attributes('type')).toBe('text')
    expect(toggle.attributes('aria-label')).toBe('隐藏密码')
    expect(toggle.attributes('aria-pressed')).toBe('true')
    await toggle.trigger('click')
    expect(wrapper.get('#password').attributes('type')).toBe('password')
    expect(mocks.login).not.toHaveBeenCalled()
  })

  it('trims the account, caches the session, and preserves the original redirect', async () => {
    const wrapper = render()
    await fillAndSubmit(wrapper)
    expect(mocks.login).toHaveBeenCalledWith('admin', 'test-password')
    expect(mocks.setUser).toHaveBeenCalledWith({ username: 'admin' })
    expect(mocks.target).toHaveBeenCalledWith('/projects?tab=recent')
    expect(mocks.replace).toHaveBeenCalledWith('/projects?tab=recent')
    expect(mocks.setUser.mock.invocationCallOrder[0]).toBeLessThan(
      mocks.replace.mock.invocationCallOrder[0],
    )
  })

  it('disables controls and prevents duplicate submissions while connecting', async () => {
    mocks.login.mockReturnValue(new Promise(() => {}))
    const wrapper = render()
    await fillAndSubmit(wrapper)
    expect(wrapper.get('.submit').attributes('aria-busy')).toBe('true')
    expect(wrapper.get('.submit').text()).toContain('正在连接')
    expect(wrapper.get('#username').attributes('disabled')).toBeDefined()
    expect(wrapper.get('.reveal').attributes('disabled')).toBeDefined()
    await wrapper.get('form').trigger('submit')
    expect(mocks.login).toHaveBeenCalledTimes(1)
  })

  it('shows credential errors without losing the account or navigating', async () => {
    mocks.login.mockRejectedValue(
      new HttpError(
        401,
        { code: 'unauthorized', message: '用户名或口令错误' },
        'Unauthorized',
      ),
    )
    const wrapper = render()
    await fillAndSubmit(wrapper)
    expect(wrapper.get('.banner').text()).toContain('用户名或口令错误')
    expect(wrapper.get('#password-err').text()).toContain('请检查用户名与密码')
    expect(wrapper.get<HTMLInputElement>('#username').element.value).toBe(
      '  admin  ',
    )
    expect(wrapper.get('.submit').attributes('disabled')).toBeUndefined()
    expect(mocks.replace).not.toHaveBeenCalled()
  })

  it('retains the server lockout state and disables further attempts', async () => {
    mocks.login.mockRejectedValue(
      new HttpError(429, { code: 'locked', message: '请稍后重试' }, 'Locked'),
    )
    const wrapper = render()
    await fillAndSubmit(wrapper)
    expect(wrapper.get('.banner--locked').text()).toContain('请稍后重试')
    expect(wrapper.get('.submit').attributes('disabled')).toBeDefined()
    expect(wrapper.get('#password').attributes('disabled')).toBeDefined()
    await wrapper.get('form').trigger('submit')
    expect(mocks.login).toHaveBeenCalledTimes(1)
  })

  it.each([
    [new HttpError(0, null, 'Network error'), '无法连接到服务器'],
    [new HttpError(503, null, 'Unavailable'), '登录失败(503)'],
    [new Error('sensitive details'), '未知错误'],
  ])('shows a safe, retryable error for %s', async (error, message) => {
    mocks.login.mockRejectedValue(error)
    const wrapper = render()
    await fillAndSubmit(wrapper)
    expect(wrapper.get('.banner').text()).toContain(message)
    expect(wrapper.text()).not.toContain('sensitive details')
    expect(wrapper.get('.submit').attributes('disabled')).toBeUndefined()
  })
})
