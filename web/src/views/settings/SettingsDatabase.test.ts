import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import SettingsDatabase from './SettingsDatabase.vue'
import {
  databaseStatus,
  databasePreflight,
  databaseAction,
  recoveryRequest,
  RecoveryError,
  type MigrationState,
} from '../../api/databaseMigration'
import { HttpError } from '../../api/http'
import { setLocale } from '../../i18n'
import { useConfirm } from '../../composables/useConfirm'
const navigation = vi.hoisted(() => ({ push: vi.fn(), replace: vi.fn() }))
vi.mock('vue-router', () => ({ useRouter: () => navigation }))
vi.mock('../../api/databaseMigration', async (original) => ({
  ...(await original<typeof import('../../api/databaseMigration')>()),
  databaseStatus: vi.fn(),
  databasePreflight: vi.fn(),
  databaseAction: vi.fn(),
  recoveryRequest: vi.fn(),
}))
const basic = (current = 'sqlite'): MigrationState => ({
  current,
  revision: 0,
  managed: false,
  recovery: false,
  blockers: [],
  blockersUnavailable: false,
  completedTables: 0,
  totalTables: 106,
})
const wrappers: ReturnType<typeof mount>[] = []
const render = (recovery = false) => {
  const wrapper = mount(SettingsDatabase, { props: { recovery } })
  wrappers.push(wrapper)
  return wrapper
}
beforeEach(() => {
  vi.resetAllMocks()
  vi.useFakeTimers()
  vi.mocked(databaseStatus).mockResolvedValue(basic())
})
afterEach(() => {
  useConfirm()._resolve(false)
  for (const wrapper of wrappers.splice(0)) wrapper.unmount()
  vi.useRealTimers()
  setLocale('zh-CN')
})
describe('database migration controls', () => {
  const prepared = (taskId = 'page-a', revision = 1): MigrationState => ({
    ...basic(),
    taskId,
    revision,
    phase: 'prepared',
    target: 'postgres',
    checks: ['connection'],
  })
  async function approve() {
    const wrapper = render()
    await flushPromises()
    vi.mocked(databasePreflight).mockResolvedValue(prepared())
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    return wrapper
  }
  it('revokes page A approval when page B replaces the prepared task with the same engine', async () => {
    const wrapper = await approve()
    expect(wrapper.find('.database-checks').exists()).toBe(true)
    vi.mocked(databaseStatus).mockResolvedValue(prepared('page-b'))
    await vi.advanceTimersByTimeAsync(2500)
    await flushPromises()
    expect(wrapper.find('.database-checks').exists()).toBe(false)
    expect(databaseAction).not.toHaveBeenCalled()
  })
  it.each([
    ['page-b', 1],
    ['page-a', 2],
  ] as const)(
    'rejects a changed task/revision while confirmation is open: %s/%s',
    async (task, revision) => {
      const wrapper = await approve()
      await wrapper.findAll('form button.app-btn--primary').at(-1)!.trigger('click')
      expect(useConfirm().pending.value).not.toBeNull()
      vi.mocked(databaseStatus).mockResolvedValue(prepared(task, revision))
      await vi.advanceTimersByTimeAsync(2500)
      await flushPromises()
      useConfirm()._resolve(true)
      await flushPromises()
      expect(databaseAction).not.toHaveBeenCalled()
      expect(wrapper.get('[role=alert]').text()).toBeTruthy()
    },
  )
  it('sends the preflight task and revision confirmed by the operator', async () => {
    const wrapper = await approve()
    await wrapper.findAll('form button.app-btn--primary').at(-1)!.trigger('click')
    useConfirm()._resolve(true)
    await flushPromises()
    expect(databaseAction).toHaveBeenCalledWith(
      'start',
      expect.objectContaining({ taskId: 'page-a', revision: 1 }),
      expect.any(AbortSignal),
    )
  })

  it('selects a usable target when the current engine is PostgreSQL', async () => {
    vi.mocked(databaseStatus).mockResolvedValue(basic('postgres'))
    const wrapper = render()
    await flushPromises()
    expect(wrapper.find('#db-file').exists()).toBe(true)
    const selected = wrapper
      .findAll('.database-engines button')
      .find((button) => button.attributes('aria-pressed') === 'true')
    expect(selected?.text()).toBe('SQLite')
    expect(selected?.attributes('disabled')).toBeUndefined()
  })
  it('submits structured fields, invalidates preflight on edits and cancels requests on unmount', async () => {
    const wrapper = render()
    await flushPromises()
    await wrapper.get('#db-host').setValue('db.local')
    await wrapper.get('#db-name').setValue('dedicated')
    await wrapper.get('#db-user').setValue('operator')
    await wrapper.get('#db-secret').setValue('private-password')
    vi.mocked(databasePreflight).mockResolvedValue({
      ...basic(),
      taskId: 'task',
      revision: 1,
      phase: 'prepared',
      target: 'postgres',
      checks: ['connection'],
    })
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    const [fields, , signal] = vi.mocked(databasePreflight).mock.calls[0]!
    expect(fields).toMatchObject({
      driver: 'postgres',
      host: 'db.local',
      database: 'dedicated',
      username: 'operator',
      password: 'private-password',
    })
    expect(fields.dsn).toBeUndefined()
    expect(wrapper.find('.database-checks').exists()).toBe(true)
    await wrapper.get('#db-host').setValue('different.local')
    expect(wrapper.find('.database-checks').exists()).toBe(false)
    expect(databaseAction).not.toHaveBeenCalled()
    wrapper.unmount()
    expect(signal?.aborted).toBe(true)
  })
  it('shows specific nonempty-target errors without echoing transport text', async () => {
    const wrapper = render()
    await flushPromises()
    vi.mocked(databasePreflight).mockRejectedValue(
      new HttpError(
        409,
        { code: 'database_target_not_empty', message: 'SECRET-DSN' },
        'SECRET-DSN',
      ),
    )
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(wrapper.get('[role=alert]').text()).not.toContain('SECRET-DSN')
    expect(wrapper.get('[role=alert]').text()).toContain('目标')
  })
  it('retranslates an existing error when the interface locale changes', async () => {
    const wrapper = render()
    await flushPromises()
    vi.mocked(databasePreflight).mockRejectedValue(
      new HttpError(
        409,
        {
          code: 'database_connection_failed',
          message: 'private transport text',
        },
        'private transport text',
      ),
    )
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(wrapper.get('[role=alert]').text()).toContain('连接')
    setLocale('en')
    await flushPromises()
    expect(wrapper.get('[role=alert]').text()).toContain('Connection failed')
    expect(wrapper.get('[role=alert]').text()).not.toContain(
      'private transport text',
    )
  })
  it('uses the recovery login when its cookie expires, never the ordinary auth API', async () => {
    vi.mocked(recoveryRequest).mockImplementation(async (path) => {
      if (path === 'session')
        throw new RecoveryError(401, 'database_recovery_auth')
      return { recovery: true } as never
    })
    const wrapper = render(true)
    await flushPromises()
    expect(wrapper.find('#db-password').exists()).toBe(true)
    expect(databaseStatus).not.toHaveBeenCalled()
    expect(navigation.replace).not.toHaveBeenCalled()
  })
  it('returns to settings after the new process confirms normal mode', async () => {
    vi.mocked(recoveryRequest).mockImplementation(async (path) => {
      if (path === 'session')
        throw new RecoveryError(401, 'database_recovery_auth')
      return { recovery: false } as never
    })
    render(true)
    await flushPromises()
    expect(navigation.replace).toHaveBeenCalledWith('/settings/database')
  })
  it('shows real table progress and an explicit activation reconciliation action', async () => {
    vi.mocked(recoveryRequest).mockResolvedValue({
      csrf: 'csrf',
      state: {
        ...basic(),
        recovery: true,
        phase: 'copying',
        completedTables: 17,
      },
    })
    const wrapper = render(true)
    await flushPromises()
    expect(wrapper.text()).toContain('17')
    expect(wrapper.text()).toContain('106')
    expect(wrapper.text()).not.toContain('%')
    vi.mocked(recoveryRequest).mockResolvedValue({
      csrf: 'csrf',
      state: { ...basic(), recovery: true, phase: 'activation_intent' },
    })
    await vi.advanceTimersByTimeAsync(2500)
    await flushPromises()
    expect(wrapper.text()).toContain('核对激活并继续')
  })
})
