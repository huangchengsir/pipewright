import {access} from '../../api/access'
import {usePermissionStore} from '../../stores/permissions'
import SettingsDatabaseContent from './SettingsDatabaseContent.vue'
import {createPinia,setActivePinia} from 'pinia'
import {useSessionStore} from '../../stores/session'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import SettingsDatabase from './SettingsDatabase.vue'
import {
  databaseStatus,
  databasePreflight,
  databaseTestConnection,
  databaseAction,
  recoveryRequest,
  RecoveryError,
  type MigrationState,
} from '../../api/databaseMigration'
import { HttpError } from '../../api/http'
import { i18n, setLocale } from '../../i18n'
import { useConfirm } from '../../composables/useConfirm'
const navigation = vi.hoisted(() => ({ push: vi.fn(), replace: vi.fn() }))
vi.mock('../../api/access',()=>({access:{decisions:vi.fn()}}))
vi.mock('vue-router', () => ({ useRouter: () => navigation }))
vi.mock('../../api/databaseMigration', async (original) => ({
  ...(await original<typeof import('../../api/databaseMigration')>()),
  databaseStatus: vi.fn(),
  databasePreflight: vi.fn(),
  databaseTestConnection: vi.fn(),
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
 setActivePinia(createPinia());useSessionStore().setUser({username:"admin",multiTenant:false})
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
      await wrapper
        .findAll('form button.app-btn--primary')
        .at(-1)!
        .trigger('click')
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
    await wrapper
      .findAll('form button.app-btn--primary')
      .at(-1)!
      .trigger('click')
    useConfirm()._resolve(true)
    await flushPromises()
    expect(databaseAction).toHaveBeenCalledWith(
      'start',
      expect.objectContaining({ taskId: 'page-a', revision: 1 }),
      expect.any(AbortSignal),
      expect.objectContaining({throwOnUnauthorized:true}),
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

describe('independent target connection probe', () => {
  async function form() {
    const wrapper = render()
    await flushPromises()
    await wrapper.get('#db-host').setValue('db.local')
    await wrapper.get('#db-name').setValue('nonempty')
    await wrapper.get('#db-user').setValue('operator')
    await wrapper.get('#db-secret').setValue('never-render-this')
    return wrapper
  }
  it('tests nonempty targets without granting migration approval or passing managed', async () => {
    const wrapper = await form()
    vi.mocked(databaseTestConnection).mockResolvedValue({ kind: 'connection' })
    await wrapper.get('[data-testid=database-test-connection]').trigger('click')
    await flushPromises()
    expect(databaseTestConnection).toHaveBeenCalledWith(
      {
        driver: 'postgres',
        host: 'db.local',
        port: 5432,
        database: 'nonempty',
        schema: '',
        username: 'operator',
        password: 'never-render-this',
      },
      expect.any(AbortSignal),
      expect.objectContaining({throwOnUnauthorized:true}),
    )
    expect(wrapper.get('[role=status]').text()).toContain('连接成功')
    expect(wrapper.find('.database-checks').exists()).toBe(false)
    expect(databasePreflight).not.toHaveBeenCalled()
    expect(databaseAction).not.toHaveBeenCalled()
    expect(wrapper.text()).not.toContain('never-render-this')
    await wrapper.get('#db-name').setValue('edited')
    expect(wrapper.find('.database-probe-success').exists()).toBe(false)
  })
  it('cancels edits and ignores an old successful response', async () => {
    const wrapper = await form()
    let resolve!: (value: { kind: 'connection' }) => void
    vi.mocked(databaseTestConnection).mockImplementation(
      () =>
        new Promise((r) => {
          resolve = r
        }),
    )
    await wrapper.get('[data-testid=database-test-connection]').trigger('click')
    const signal = vi.mocked(databaseTestConnection).mock.calls[0]![1]!
    await wrapper.get('[data-testid=database-test-connection]').trigger('click')
    expect(databaseTestConnection).toHaveBeenCalledTimes(1)
    await wrapper.get('#db-host').setValue('other.local')
    expect(signal.aborted).toBe(true)
    resolve({ kind: 'connection' })
    await flushPromises()
    expect(wrapper.find('.database-probe-success').exists()).toBe(false)
  })
  it('cancels the probe on unmount and does not start a migration', async () => {
    const wrapper = await form()
    vi.mocked(databaseTestConnection).mockImplementation(
      () => new Promise(() => {}),
    )
    await wrapper.get('[data-testid=database-test-connection]').trigger('click')
    const signal = vi.mocked(databaseTestConnection).mock.calls[0]![1]!
    wrapper.unmount()
    expect(signal.aborted).toBe(true)
    expect(databaseAction).not.toHaveBeenCalled()
  })
  it('bounds the UI wait and suppresses a late result after timeout', async () => {
    const wrapper = await form()
    let resolve!: (value: { kind: 'connection' }) => void
    vi.mocked(databaseTestConnection).mockImplementation(
      () =>
        new Promise((r) => {
          resolve = r
        }),
    )
    await wrapper.get('[data-testid=database-test-connection]').trigger('click')
    await vi.advanceTimersByTimeAsync(10000)
    expect(vi.mocked(databaseTestConnection).mock.calls[0]![1]!.aborted).toBe(
      true,
    )
    expect(wrapper.get('[role=status]').text()).toContain('连接超时')
    resolve({ kind: 'connection' })
    await flushPromises()
    expect(wrapper.find('.database-probe-success').exists()).toBe(false)
  })
  it('preserves an existing preflight approval when checking its connection', async () => {
    const wrapper = await form()
    vi.mocked(databasePreflight).mockResolvedValue({
      ...basic(),
      taskId: 'prepared',
      revision: 5,
      phase: 'prepared',
      target: 'postgres',
      checks: ['connection'],
    })
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(wrapper.find('.database-checks').exists()).toBe(true)
    vi.mocked(databaseTestConnection).mockResolvedValue({ kind: 'connection' })
    await wrapper.get('[data-testid=database-test-connection]').trigger('click')
    await flushPromises()
    expect(wrapper.find('.database-checks').exists()).toBe(true)
    expect(databaseAction).not.toHaveBeenCalled()
  })
  it('uses path wording for SQLite and clears a probe when maintenance starts', async () => {
    vi.mocked(databaseStatus).mockResolvedValue(basic('postgres'))
    const wrapper = render()
    await flushPromises()
    await wrapper.get('#db-file').setValue('/data/new.db')
    expect(
      wrapper.get('[data-testid=database-test-connection]').text(),
    ).toContain('检查路径')
    vi.mocked(databaseTestConnection).mockImplementation(
      () => new Promise(() => {}),
    )
    await wrapper.get('[data-testid=database-test-connection]').trigger('click')
    vi.mocked(databaseStatus).mockResolvedValue({
      ...basic('postgres'),
      phase: 'quiescing',
    })
    await vi.advanceTimersByTimeAsync(2500)
    await flushPromises()
    expect(vi.mocked(databaseTestConnection).mock.calls[0]![1]!.aborted).toBe(
      true,
    )
    expect(wrapper.find('form').exists()).toBe(false)
  })
  it('does not render arbitrary driver errors', async () => {
    const wrapper = await form()
    vi.mocked(databaseTestConnection).mockRejectedValue(
      new Error('secret-DSN-marker'),
    )
    await wrapper.get('[data-testid=database-test-connection]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[role=status]').text()).not.toContain(
      'secret-DSN-marker',
    )
    expect(databasePreflight).not.toHaveBeenCalled()
  })
})

describe('database settings permission and identity lifetime',()=>{
 function employee(allowed:boolean){
  useSessionStore().setUser({username:'member',userId:'employee',multiTenant:true});usePermissionStore().setTeam('team')
  vi.mocked(access.decisions).mockImplementation(async(team,queries)=>({userId:'employee',teamId:team,modeEpoch:1,revision:1,catalogVersion:1,allowed:queries.map(()=>allowed)}))
 }
 it('never loads database settings for an employee without system permission',async()=>{
  employee(false);const w=render();await flushPromises()
  expect(w.find('form').exists()).toBe(false);expect(databaseStatus).not.toHaveBeenCalled()
  expect(databasePreflight).not.toHaveBeenCalled();expect(databaseTestConnection).not.toHaveBeenCalled()
 })
 it('clears passwords, aborts the probe and discards its result on employee change',async()=>{
  let resolve!:(result:{kind:'connection'})=>void
  vi.mocked(databaseTestConnection).mockReturnValue(new Promise(r=>{resolve=r}))
  const w=render();await flushPromises()
  await w.get('#db-host').setValue('db.local');await w.get('#db-name').setValue('target');await w.get('#db-user').setValue('operator');await w.get('#db-secret').setValue('private-target-password')
  await w.get('[data-testid=database-test-connection]').trigger('click');await flushPromises()
  const signal=vi.mocked(databaseTestConnection).mock.calls[0][1]!
  useSessionStore().setUser({username:'different-admin',multiTenant:false});await flushPromises()
  resolve({kind:'connection'});await flushPromises()
  expect(signal.aborted).toBe(true);expect((w.get('#db-secret').element as HTMLInputElement).value).toBe('')
  expect(w.find('.database-probe-success').exists()).toBe(false)
 })
 it('rejects captured preflight and probe handlers after revocation',async()=>{
  employee(true);const w=render();await flushPromises()
  const vm=w.findComponent(SettingsDatabaseContent).vm as unknown as {preflight:()=>Promise<void>;testConnection:()=>Promise<void>}
  employee(false);await flushPromises()
  await vm.preflight();await vm.testConnection();await flushPromises()
  expect(databasePreflight).not.toHaveBeenCalled();expect(databaseTestConnection).not.toHaveBeenCalled()
 })
 it('keeps recovery authentication independent of the normal employee session',async()=>{
  useSessionStore().clearSession()
  vi.mocked(recoveryRequest).mockResolvedValue({state:basic(),csrf:'recovery-only'})
  const w=render(true);await flushPromises()
  expect(recoveryRequest).toHaveBeenCalled();expect(databaseStatus).not.toHaveBeenCalled()
  expect(w.findComponent(SettingsDatabaseContent).exists()).toBe(true)
 })
})

describe('database recovery and settings entry controls', () => {
  const currentTask = (phase: NonNullable<MigrationState['phase']>, recovery: boolean) => ({
    ...basic(), recovery, phase, taskId: 'confirmed-task', revision: 7, target: 'postgres',
  })
  const button = (wrapper: VueWrapper, action: string) => {
    const match = wrapper.findAll('button').find(control => control.text() === i18n.global.t('databaseMigration.' + action))
    expect(match, action).toBeDefined()
    return match!
  }

  it.each([
    ['continue', 'copying'], ['reconcile', 'awaiting_restart'], ['activate', 'verified'], ['abort', 'copying'],
  ] as const)('dispatches the recovery %s button with the current task and csrf', async (action, phase) => {
    const state = currentTask(phase, true)
    vi.mocked(recoveryRequest).mockImplementation(async path => path === 'session' ? { csrf: 'recovery-fixture', state } : state)
    const wrapper = render(true)
    await flushPromises()
    await button(wrapper, action).trigger('click')
    if (action !== 'continue') {
      expect(recoveryRequest).toHaveBeenCalledTimes(1)
      useConfirm()._resolve(true)
    }
    await flushPromises()
    expect(recoveryRequest).toHaveBeenLastCalledWith(action, { taskId: state.taskId, revision: 7 }, 'recovery-fixture', expect.any(AbortSignal))
    expect(databaseAction).not.toHaveBeenCalled()
  })

  it('submits recovery login through the native button and clears the password', async () => {
    vi.mocked(recoveryRequest)
      .mockRejectedValueOnce(new RecoveryError(401, 'database_recovery_auth'))
      .mockResolvedValueOnce({ recovery: true })
      .mockResolvedValueOnce({ csrf: 'recovery-fixture' })
      .mockResolvedValueOnce({ csrf: 'recovery-fixture', state: currentTask('copying', true) })
    const wrapper = render(true)
    document.body.appendChild(wrapper.element)
    await flushPromises()
    await wrapper.get('#db-username').setValue('fixture-admin')
    await wrapper.get('#db-password').setValue('fixture-password')
    ;(wrapper.get('button[type=submit]').element as HTMLButtonElement).click()
    await flushPromises()
    expect(recoveryRequest).toHaveBeenNthCalledWith(3, 'login', { username: 'fixture-admin', password: 'fixture-password' }, undefined, expect.any(AbortSignal))
    expect(wrapper.find('#db-password').exists()).toBe(false)
    expect(databaseStatus).not.toHaveBeenCalled()
  })

  it('switches the destination engine and preflights only when the native submit button is clicked', async () => {
    vi.mocked(databasePreflight).mockResolvedValue(currentTask('prepared', false))
    const wrapper = render()
    document.body.appendChild(wrapper.element)
    await flushPromises()
    const postgres = wrapper.findAll('.database-engines button').find(engine => engine.text() === 'PostgreSQL')
    expect(postgres).toBeDefined()
    await postgres!.trigger('click')
    expect(postgres!.attributes('aria-pressed')).toBe('true')
    await wrapper.get('#db-host').setValue('db.example.test')
    await wrapper.get('#db-name').setValue('target')
    await wrapper.get('#db-user').setValue('fixture-user')
    expect(databasePreflight).not.toHaveBeenCalled()
    ;(wrapper.get('form button[type=submit]').element as HTMLButtonElement).click()
    await flushPromises()
    expect(databasePreflight).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ driver: 'postgres' }), false, expect.any(AbortSignal), expect.any(Object))
  })

  it('aborts a prepared task only after confirmation in ordinary settings', async () => {
    const state = currentTask('prepared', false)
    vi.mocked(databaseStatus).mockResolvedValue(state)
    const wrapper = render()
    await flushPromises()
    await button(wrapper, 'discard').trigger('click')
    expect(databaseAction).not.toHaveBeenCalled()
    useConfirm()._resolve(true)
    await flushPromises()
    expect(databaseAction).toHaveBeenCalledExactlyOnceWith('abort', expect.objectContaining({ taskId: state.taskId, revision: 7 }), expect.any(AbortSignal), expect.any(Object))
    expect(recoveryRequest).not.toHaveBeenCalled()
  })

  it('opens the recovery route for the quiescing task without dispatching a mutation', async () => {
    vi.mocked(databaseStatus).mockResolvedValue(currentTask('quiescing', false))
    const wrapper = render()
    await flushPromises()
    await button(wrapper, 'recovery').trigger('click')
    expect(navigation.push).toHaveBeenCalledExactlyOnceWith('/database-recovery')
    expect(databaseAction).not.toHaveBeenCalled()
  })
})
