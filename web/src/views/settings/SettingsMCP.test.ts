import {HttpError} from '../../api/http'
import { access } from '../../api/access'
import { usePermissionStore } from '../../stores/permissions'
import { createPinia, setActivePinia } from 'pinia'
import { useSessionStore } from '../../stores/session'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { NSelect } from 'naive-ui'
import SettingsMCP from './SettingsMCP.vue'
import MCPTokenForm from '../../components/mcp/MCPTokenForm.vue'
import MCPTokenList from '../../components/mcp/MCPTokenList.vue'
import MCPTokenSecret from '../../components/mcp/MCPTokenSecret.vue'
import AppButton from '../../components/ui/AppButton.vue'
import type { MCPToken } from '../../api/mcp'

vi.mock('../../api/access', () => ({ access: { decisions: vi.fn() } }))
const mocks = vi.hoisted(() => ({ list: vi.fn(), projects: vi.fn(), create: vi.fn(), revoke: vi.fn(), confirm: vi.fn(), copy: vi.fn(), success: vi.fn(), error: vi.fn() }))
vi.mock('../../api/mcp', async () => ({ ...await vi.importActual('../../api/mcp'), listMCPTokens: mocks.list, createMCPToken: mocks.create, revokeMCPToken: mocks.revoke }))
vi.mock('../../api/projects', () => ({ listProjects: mocks.projects }))
vi.mock('../../composables/useConfirm', () => ({ useConfirm: () => ({ open: mocks.confirm }) }))
vi.mock('../../composables/useToast', () => ({ useToast: () => ({ success: mocks.success, error: mocks.error }) }))
vi.mock('../../utils/clipboard', () => ({ copyText: mocks.copy }))

const item = { id: 't1', name: 'test client', projectIds: ['p1'], canTrigger: false, createdAt: '2026-01-01T00:00:00Z', expiresAt: '2099-01-01T00:00:00Z', revokedAt: null }
const input = { name: 'new client', projectIds: ['p1'], canTrigger: false, expiresInDays: 7 }
beforeEach(() => { setActivePinia(createPinia()); useSessionStore().setUser({ username: 'admin', multiTenant: false }) })
const wrappers: VueWrapper[] = []
function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: Error) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
function render(attached = false) { const w = mount(SettingsMCP, { attachTo: attached ? document.body : undefined, global: { stubs: { NModal: { props: ['show'], template: '<div v-if="show"><slot name="header-extra"/><slot/><slot name="footer"/></div>' } } } }); wrappers.push(w); return w }
async function fill(w: VueWrapper) {
  await w.get('#mcp-name').setValue('new client')
  w.findComponent(MCPTokenForm).findComponent(NSelect).vm.$emit('update:value', ['p1'])
  await flushPromises()
}
describe('MCP settings', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    mocks.list.mockResolvedValue([item])
    mocks.projects.mockResolvedValue([{ id: 'p1', name: 'Local project' }])
    mocks.create.mockResolvedValue({ token: 'one-time-secret', item: { ...item, id: 't2' } })
    mocks.confirm.mockResolvedValue(true)
    mocks.revoke.mockResolvedValue(undefined)
    mocks.copy.mockResolvedValue(true)
  })
  afterEach(() => { for (const w of wrappers.splice(0)) w.unmount(); vi.restoreAllMocks(); vi.useRealTimers() })
  it('shows scoped metadata and a token-free connection example', async () => {
    const w = render(); await flushPromises()
    expect(w.text()).toContain('Local project')
    expect(w.text()).toContain('Streamable HTTP')
    expect(w.findComponent(MCPTokenSecret).props('token')).toBe('')
    expect(w.text()).not.toContain('one-time-secret')
    expect(w.text()).toContain('Bearer <MCP_TOKEN>')
  })
  it.each(['claude', 'codex'])('copies the %s example through the settings clipboard handler', async (client) => {
    const w = render(); await flushPromises()
    await w.get(`[data-client-tab="${client}"]`).trigger('click')
    const panel = w.get(`[data-client="${client}"]`)
    const config = panel.get('pre').text()
    await panel.get('button').trigger('click'); await flushPromises()
    expect(mocks.copy).toHaveBeenCalledExactlyOnceWith(config, expect.any(Function))
    expect(config).toContain('<MCP_TOKEN>')
  })
  it('opens creation explicitly without remounting or losing its draft', async () => {
    const w = render(); await flushPromises()
    const details = w.get('.mcp-create').element as HTMLDetailsElement
    expect(details.open).toBe(false)
    expect(w.findComponent(MCPTokenForm).exists()).toBe(true)
    const open = w.get('.mcp-list-actions').findAllComponents(AppButton).find((button: VueWrapper) => button.classes().includes('app-btn--primary'))!
    await open.trigger('click'); await flushPromises()
    expect(details.open).toBe(true)
    await w.get('#mcp-name').setValue('retained draft')
    details.open = false
    await open.trigger('click'); await flushPromises()
    expect((w.get('#mcp-name').element as HTMLInputElement).value).toBe('retained draft')
    expect(mocks.create).not.toHaveBeenCalled()
    expect(mocks.revoke).not.toHaveBeenCalled()
  })
  it('creates read-only by default and clears the one-time secret when closed', async () => {
    const spy = vi.spyOn(Storage.prototype, 'setItem')
    const w = render(); await flushPromises(); await fill(w)
    await w.get('form').trigger('submit'); await flushPromises()
    expect(mocks.create).toHaveBeenCalledWith({ name: 'new client', projectIds: ['p1'], canTrigger: false, expiresInDays: 7 }, expect.objectContaining({throwOnUnauthorized:true}))
    expect(w.findComponent(MCPTokenSecret).props('token')).toBe('one-time-secret')
    const examples = w.findAll('.mcp-config pre')
    expect(examples).toHaveLength(2)
    for (const pre of examples) {
      expect(pre.text()).toContain('<MCP_TOKEN>')
      expect(pre.text()).not.toContain('one-time-secret')
    }
    w.findComponent(MCPTokenSecret).vm.$emit('close'); await flushPromises()
    expect(w.findComponent(MCPTokenSecret).props('token')).toBe('')
    expect(w.text()).not.toContain('one-time-secret')
    expect(spy.mock.calls.some(call => String(call).includes('one-time-secret'))).toBe(false)
    spy.mockRestore()
  })
  it('requires explicit trigger permission and bounded expiry', async () => {
    const w = render(); await flushPromises(); await fill(w)
    await w.get('input[type="checkbox"]').setValue(true)
    await w.get('#mcp-expiry').setValue(31)
    await w.get('form').trigger('submit'); await flushPromises()
    expect(mocks.create).not.toHaveBeenCalled()
    await w.get('#mcp-expiry').setValue(1)
    await w.get('form').trigger('submit'); await flushPromises()
    expect(mocks.create).toHaveBeenCalledWith(expect.objectContaining({ canTrigger: true, expiresInDays: 1 }), expect.objectContaining({throwOnUnauthorized:true}))
  })
  it('keeps configuration grants off by default with dependent permissions disabled', async () => {
    const w = render(); await flushPromises(); await fill(w)
    const grant = w.get('[data-config-project="p1"]')
    expect((grant.get('[data-permission="read"]').element as HTMLInputElement).checked).toBe(false)
    expect(grant.get('[data-permission="propose"]').attributes('disabled')).toBeDefined()
    expect(grant.get('[data-permission="apply"]').attributes('disabled')).toBeDefined()
  })
  it('issues explicit project configuration scopes without granting pipeline triggers', async () => {
    const w = render(); await flushPromises(); await fill(w)
    const grant = w.get('[data-config-project="p1"]')
    await grant.get('[data-permission="read"]').setValue(true)
    await grant.get('[data-permission="propose"]').setValue(true)
    await grant.get('[data-permission="apply"]').setValue(true)
    await w.get('form').trigger('submit'); await flushPromises()
    expect(mocks.create).toHaveBeenCalledWith(expect.objectContaining({ canTrigger: false, configGrants: [{ projectId: 'p1', read: true, propose: true, apply: true, sections: ['project', 'pipeline', 'build', 'step_limits', 'source_and_impact'], operations: ['project.set_name', 'project.set_default_branch', 'stage.rename', 'job.rename', 'step.set_limits', 'build.set'] }] }), expect.objectContaining({throwOnUnauthorized:true}))
  })
  it('requires explicitly entered Kubernetes stage and job selectors', async () => {
    const w=render(); await flushPromises(); await fill(w); await w.get('[data-permission="read"]').setValue(true)
    const form=w.findComponent(MCPTokenForm); form.findAllComponents(NSelect)[1].vm.$emit('update:value',['kubernetes']); await flushPromises()
    await w.get('[data-permission="propose"]').setValue(true); await w.get('[data-permission="apply"]').setValue(true); await w.get('form').trigger('submit'); await flushPromises(); expect(mocks.create).not.toHaveBeenCalled()
    const select=(id:string)=>form.findAllComponents(NSelect).find(x=>x.props('inputProps')?.id===id)!
    select('mcp-stages-p1').vm.$emit('update:value',['deploy']); await flushPromises(); await w.get('form').trigger('submit'); await flushPromises(); expect(mocks.create).not.toHaveBeenCalled()
    select('mcp-jobs-p1').vm.$emit('update:value',['kube']); await flushPromises(); await w.get('form').trigger('submit'); await flushPromises()
    expect(mocks.create).toHaveBeenCalledWith(expect.objectContaining({canTrigger:false,configGrants:[expect.objectContaining({sections:['kubernetes'],operations:['kubernetes.add','kubernetes.update'],stageIds:['deploy'],jobIds:['kube']})]}), expect.objectContaining({throwOnUnauthorized:true}))
  })
  it('submits only the operation selected in the active configuration grant', async () => {
    const wrapper = render()
    await flushPromises()
    await fill(wrapper)
    await wrapper.get('[data-permission="read"]').setValue(true)
    const form = wrapper.findComponent(MCPTokenForm)
    const selects = () => form.findAllComponents(NSelect)
    selects()[1]!.vm.$emit('update:value', ['project'])
    await flushPromises()
    await wrapper.get('[data-permission="propose"]').setValue(true)
    const operations = selects().find(select => select.props('inputProps')?.id === 'mcp-operations-p1')
    expect(operations).toBeDefined()
    operations!.vm.$emit('update:value', ['project.set_default_branch'])
    await flushPromises()
    await form.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.create).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({
      canTrigger: false,
      configGrants: [{
        projectId: 'p1',
        read: true,
        propose: true,
        apply: false,
        sections: ['project'],
        operations: ['project.set_default_branch'],
      }],
    }), expect.objectContaining({ throwOnUnauthorized: true }))
  })

  it('clears higher privileges when read permission or project selection is removed', async () => {
    const w = render(); await flushPromises(); await fill(w)
    const grant = w.get('[data-config-project="p1"]')
    await grant.get('[data-permission="read"]').setValue(true)
    await grant.get('[data-permission="propose"]').setValue(true)
    await grant.get('[data-permission="apply"]').setValue(true)
    await grant.get('[data-permission="read"]').setValue(false)
    await w.get('form').trigger('submit'); await flushPromises()
    expect(mocks.create).toHaveBeenLastCalledWith(expect.not.objectContaining({ configGrants: expect.anything() }), expect.objectContaining({throwOnUnauthorized:true}))
    await grant.get('[data-permission="read"]').setValue(true)
    const selector = w.findComponent(MCPTokenForm).findComponent(NSelect)
    selector.vm.$emit('update:value', []); await flushPromises()
    selector.vm.$emit('update:value', ['p1']); await flushPromises()
    expect((w.get('[data-permission="read"]').element as HTMLInputElement).checked).toBe(false)
  })
  it('does not issue a grant with an empty readable section range', async () => {
    const w = render(); await flushPromises(); await fill(w)
    await w.get('[data-permission="read"]').setValue(true)
    const selects = w.findComponent(MCPTokenForm).findAllComponents(NSelect)
    selects[1].vm.$emit('update:value', []); await flushPromises()
    await w.get('form').trigger('submit'); await flushPromises()
    expect(mocks.create).not.toHaveBeenCalled()
  })
  it('confirms revocation and updates the visible status', async () => {
    const w = render(); await flushPromises()
    await w.get('[data-token-id="t1"] button').trigger('click'); await flushPromises()
    expect(mocks.confirm).toHaveBeenCalled()
    expect(mocks.revoke).toHaveBeenCalledWith('t1', expect.objectContaining({throwOnUnauthorized:true}))
    expect(w.get('[data-token-id="t1"]').text()).toContain('已撤销')
  })
  it('does not revoke after a cancelled confirmation', async () => {
    mocks.confirm.mockResolvedValue(false)
    const w = render(); await flushPromises()
    await w.get('[data-token-id="t1"] button').trigger('click'); await flushPromises()
    expect(mocks.revoke).not.toHaveBeenCalled()
  })
  it('shows errors without exposing an issuance response', async () => {
    mocks.create.mockRejectedValue(new Error('sensitive response'))
    const w = render(); await flushPromises(); await fill(w)
    await w.get('form').trigger('submit'); await flushPromises()
    expect(w.text()).toContain('创建失败')
    expect(w.text()).not.toContain('sensitive response')
  })
  it('does not retain a late issuance response after unmount', async () => {
    let resolve!: (data: unknown) => void
    mocks.create.mockReturnValue(new Promise(r => { resolve = r }))
    const w = render(); await flushPromises(); await fill(w)
    await w.get('form').trigger('submit')
    const secret = w.findComponent(MCPTokenSecret)
    w.unmount()
    resolve({ token: 'late-secret', item }); await flushPromises()
    expect(secret.props('token')).toBe('')
  })
  it('keeps creation disabled if loading fails', async () => {
    mocks.list.mockRejectedValue(new Error('unavailable'))
    const w = render(); await flushPromises()
    expect(w.text()).toContain('无法加载')
    expect(w.get('#mcp-name').attributes('disabled')).toBeDefined()
  })
  it.each(['resolved', 'rejected'])('ignores a %s stale project load after creation without stranding loading', async (outcome) => {
    const w = render(); await flushPromises(); await fill(w)
    const refresh = deferred<{ id: string; name: string }[]>()
    mocks.projects.mockReturnValueOnce(refresh.promise)
    await w.get('.mcp-list-header button').trigger('click'); await flushPromises()
    expect(mocks.projects).toHaveBeenCalledTimes(2)
    expect(w.findComponent(MCPTokenForm).props('busy')).toBe(true)

    // A submit can already be queued when the refresh starts.
    w.findComponent(MCPTokenForm).vm.$emit('create', input); await flushPromises()
    expect(w.findComponent(MCPTokenSecret).props('token')).toBe('one-time-secret')
    expect(w.find('[data-token-id="t2"]').exists()).toBe(true)
    expect(w.findComponent(MCPTokenForm).props('busy')).toBe(false)
    expect(w.get('.mcp-list-header button').attributes('aria-busy')).toBeUndefined()
    if (outcome === 'resolved') refresh.resolve([{ id: 'p1', name: 'stale project name' }])
    else refresh.reject(new Error('stale load failed'))
    await flushPromises()
    expect(w.get('[data-token-id="t2"]').text()).toContain('Local project')
    expect(w.findComponent(MCPTokenSecret).props('token')).toBe('one-time-secret')
    expect(w.find('.mcp-load-error').exists()).toBe(false)
    expect(w.findComponent(MCPTokenForm).props('busy')).toBe(false)
  })
  it('ignores a stale token response and skips its project request after creation', async () => {
    const w = render(); await flushPromises()
    const refresh = deferred<MCPToken[]>()
    mocks.list.mockReturnValueOnce(refresh.promise)
    await w.get('.mcp-list-header button').trigger('click')
    w.findComponent(MCPTokenForm).vm.$emit('create', input); await flushPromises()
    refresh.resolve([item]); await flushPromises()
    expect(w.find('[data-token-id="t2"]').exists()).toBe(true)
    expect(mocks.projects).toHaveBeenCalledTimes(1)
    expect(w.findComponent(MCPTokenForm).props('busy')).toBe(false)
  })
  it('invalidates metadata captured before a confirmed revocation', async () => {
    const w = render(); await flushPromises()
    const confirmation = deferred<boolean>()
    const refresh = deferred<{ id: string; name: string }[]>()
    mocks.confirm.mockReturnValueOnce(confirmation.promise)
    mocks.projects.mockReturnValueOnce(refresh.promise)
    const list = w.findComponent(MCPTokenList)
    const refreshing = w.get('.mcp-list-header button').trigger('click')
    list.vm.$emit('revoke', item)
    await refreshing; await flushPromises()
    expect(mocks.projects).toHaveBeenCalledTimes(2)
    confirmation.resolve(true); await flushPromises()
    expect(w.get('[data-token-id="t1"]').text()).toContain('已撤销')
    expect(w.get('.mcp-list-header button').attributes('aria-busy')).toBeUndefined()
    refresh.resolve([{ id: 'p1', name: 'stale project name' }]); await flushPromises()
    expect(w.get('[data-token-id="t1"]').text()).toContain('已撤销')
    expect(w.get('[data-token-id="t1"]').text()).toContain('Local project')
    expect(w.findComponent(MCPTokenForm).props('busy')).toBe(false)
  })
  it('lets a new refresh own loading while an invalidated refresh finishes', async () => {
    const w = render(); await flushPromises()
    const stale = deferred<{ id: string; name: string }[]>()
    const current = deferred<{ id: string; name: string }[]>()
    mocks.projects.mockReturnValueOnce(stale.promise)
    await w.get('.mcp-list-header button').trigger('click'); await flushPromises()
    w.findComponent(MCPTokenForm).vm.$emit('create', input); await flushPromises()
    mocks.list.mockResolvedValueOnce([{ ...item, id: 't2' }, item])
    mocks.projects.mockReturnValueOnce(current.promise)
    await w.get('.mcp-list-header button').trigger('click'); await flushPromises()
    stale.resolve([{ id: 'p1', name: 'stale project name' }]); await flushPromises()
    expect(w.findComponent(MCPTokenForm).props('busy')).toBe(true)
    expect(w.get('.mcp-list-header button').attributes('aria-busy')).toBe('true')
    current.resolve([{ id: 'p1', name: 'Current project' }]); await flushPromises()
    expect(w.get('[data-token-id="t2"]').text()).toContain('Current project')
    expect(w.text()).not.toContain('stale project name')
    expect(w.findComponent(MCPTokenForm).props('busy')).toBe(false)
    expect(w.get('.mcp-list-header button').attributes('disabled')).toBeUndefined()
    expect(w.findComponent(MCPTokenSecret).props('token')).toBe('one-time-secret')
  })
  it.each(['resolved', 'rejected'])('blocks refresh and other mutations during a %s creation', async (outcome) => {
    const w = render(); await flushPromises(); await fill(w)
    const creation = deferred<{ token: string; item: MCPToken }>()
    mocks.create.mockReturnValueOnce(creation.promise)
    await w.get('form').trigger('submit')
    expect(w.get('.mcp-list-header button').attributes('disabled')).toBeDefined()
    expect(w.get('[data-token-id="t1"] button').element.matches(':disabled')).toBe(true)
    w.findComponent(MCPTokenForm).vm.$emit('create', input)
    w.findComponent(MCPTokenList).vm.$emit('revoke', item)
    w.get('.mcp-list-header').getComponent(AppButton).vm.$emit('click', new MouseEvent('click'))
    await flushPromises()
    expect(mocks.create).toHaveBeenCalledTimes(1)
    expect(mocks.confirm).not.toHaveBeenCalled()
    expect(mocks.list).toHaveBeenCalledTimes(1)
    if (outcome === 'resolved') creation.resolve({ token: 'one-time-secret', item: { ...item, id: 't2' } })
    else creation.reject(new Error('creation failed'))
    await flushPromises()
    expect(w.get('.mcp-list-header button').attributes('disabled')).toBeUndefined()
    expect(w.get('[data-token-id="t1"] button').element.matches(':disabled')).toBe(false)
    expect(w.findComponent(MCPTokenForm).props('busy')).toBe(false)
  })
  it.each(['cancelled', 'failed', 'succeeded'])('serializes pending confirmation and releases controls after revocation is %s', async (outcome) => {
    const w = render(); await flushPromises()
    const confirmation = deferred<boolean>()
    const revocation = deferred<void>()
    mocks.confirm.mockReturnValueOnce(confirmation.promise)
    mocks.revoke.mockReturnValueOnce(revocation.promise)
    await w.get('[data-token-id="t1"] button').trigger('click')
    expect(w.findComponent(MCPTokenForm).props('busy')).toBe(true)
    expect(w.get('.mcp-list-header button').attributes('disabled')).toBeDefined()
    w.findComponent(MCPTokenForm).vm.$emit('create', input)
    w.findComponent(MCPTokenList).vm.$emit('revoke', { ...item, id: 'another-token' })
    await flushPromises()
    expect(mocks.confirm).toHaveBeenCalledTimes(1)
    expect(mocks.create).not.toHaveBeenCalled()
    confirmation.resolve(outcome !== 'cancelled'); await flushPromises()
    if (outcome !== 'cancelled') {
      expect(mocks.revoke).toHaveBeenCalledWith('t1', expect.objectContaining({throwOnUnauthorized:true}))
      expect(w.findComponent(MCPTokenForm).props('busy')).toBe(true)
      if (outcome === 'failed') revocation.reject(new Error('sensitive revocation failure'))
      else revocation.resolve(undefined)
      await flushPromises()
    } else expect(mocks.revoke).not.toHaveBeenCalled()
    expect(w.findComponent(MCPTokenForm).props('busy')).toBe(false)
    expect(w.get('.mcp-list-header button').attributes('disabled')).toBeUndefined()
    expect(w.get('[data-token-id="t1"] button').element.matches(':disabled')).toBe(outcome === 'succeeded')
    expect(mocks.error).toHaveBeenCalledTimes(outcome === 'failed' ? 1 : 0)
    expect(w.text()).not.toContain('sensitive revocation failure')
  })
  it('retains the existing 32-project limit with all independent permission decisions', async () => {
    useSessionStore().setUser({username:'member',userId:'employee',multiTenant:true});usePermissionStore().setTeam('team')
    mocks.projects.mockResolvedValue(Array.from({length:32},(_,i)=>({id:`p${i}`,name:`Project ${i}`})))
    vi.mocked(access.decisions).mockImplementation(async(team,queries)=>({userId:'employee',teamId:team,modeEpoch:1,revision:1,catalogVersion:1,allowed:queries.map(()=>true)}))
    const w=render();await flushPromises();await w.get('#mcp-name').setValue('All authorized projects')
    w.findComponent(MCPTokenForm).findComponent(NSelect).vm.$emit('update:value',Array.from({length:32},(_,i)=>`p${i}`));await flushPromises()
    await w.get('input[type="checkbox"]').setValue(true)
    await Promise.all(w.findAll('[data-permission="read"]').map(el=>el.setValue(true)))
    await Promise.all(w.findAll('[data-permission="propose"]').map(el=>el.setValue(true)))
    await flushPromises()
    expect(w.get('.mcp-form button[type="submit"]').attributes('disabled')).toBeUndefined()
    await w.get('form').trigger('submit');await flushPromises()
    expect(mocks.create).toHaveBeenCalledTimes(1)
    expect(mocks.create.mock.calls[0][0].projectIds).toHaveLength(32)
    expect(vi.mocked(access.decisions).mock.calls.every(call=>call[1].length<=64)).toBe(true)
  }, 20000)
  it('clears private metadata and blocks further actions after API permission denial', async () => {
    const w=render();await flushPromises();await fill(w)
    mocks.create.mockRejectedValueOnce(new HttpError(403,{code:'forbidden',message:'denied'},'denied'))
    await w.get('form').trigger('submit');await flushPromises()
    expect(w.findComponent(MCPTokenSecret).props('token')).toBe('')
    expect(w.findComponent(MCPTokenList).exists()).toBe(false)
    expect(w.get('.mcp-load-error').text()).not.toBe('')
    w.findComponent(MCPTokenForm).vm.$emit('create',input);await flushPromises()
    expect(mocks.create).toHaveBeenCalledTimes(1)
  })
  it('discards an old employee token response and aborts its request', async () => {
    const late=deferred<{token:string;item:MCPToken}>()
    mocks.create.mockReturnValueOnce(late.promise)
    const w=render(); await flushPromises(); await fill(w)
    await w.get('form').trigger('submit'); await flushPromises()
    const options=mocks.create.mock.calls[0][1]
    useSessionStore().setUser({username:'another-admin',multiTenant:false}); await flushPromises()
    late.resolve({token:'old-private-token',item}); await flushPromises()
    expect(options.signal.aborted).toBe(true)
    expect(w.findComponent(MCPTokenSecret).props('token')).toBe('')
    expect(w.text()).not.toContain('old-private-token')
  })
  it('does not revoke after confirmation crosses a team change', async () => {
    const approval=deferred<boolean>(); mocks.confirm.mockReturnValueOnce(approval.promise)
    const w=render(); await flushPromises()
    w.findComponent(MCPTokenList).vm.$emit('revoke',item); await flushPromises()
    usePermissionStore().setTeam('different-team'); await flushPromises()
    approval.resolve(true); await flushPromises()
    expect(mocks.revoke).not.toHaveBeenCalled()
  })
  it.each(['run.start','pipeline.edit'])('requires the separate %s grant even through a captured child event', async denied => {
    const session=useSessionStore(),permissions=usePermissionStore()
    session.setUser({username:'member',userId:'employee',multiTenant:true}); permissions.setTeam('team')
    vi.mocked(access.decisions).mockImplementation(async(team,queries)=>({userId:'employee',teamId:team,modeEpoch:1,revision:1,catalogVersion:1,allowed:queries.map(q=>q.action!==denied)}))
    const w=render(); await flushPromises(); await fill(w)
    if(denied==='run.start')await w.get('input[type="checkbox"]').setValue(true)
    else {await w.get('[data-permission="read"]').setValue(true);await w.get('[data-permission="propose"]').setValue(true)}
    await flushPromises()
    expect(w.get('.mcp-form button[type="submit"]').attributes('disabled')).toBeDefined()
    const forged={...input,canTrigger:denied==='run.start',...(denied==='pipeline.edit'?{configGrants:[{projectId:'p1',read:true,propose:true,apply:false,sections:['pipeline'],operations:['stage.rename']}]}:{})}
    w.findComponent(MCPTokenForm).vm.$emit('create',forged); await flushPromises()
    expect(mocks.create).not.toHaveBeenCalled()
  })
  it('uses the captured team on an authorized employee token request', async () => {
    useSessionStore().setUser({username:'member',userId:'employee',multiTenant:true}); usePermissionStore().setTeam('team')
    vi.mocked(access.decisions).mockImplementation(async(team,queries)=>({userId:'employee',teamId:team,modeEpoch:1,revision:1,catalogVersion:1,allowed:queries.map(()=>true)}))
    const w=render();await flushPromises();await fill(w);await w.get('form').trigger('submit');await flushPromises()
    expect(mocks.create).toHaveBeenCalledWith(input,expect.objectContaining({headers:{'X-Pipewright-Team':'team'},throwOnUnauthorized:true}))
    usePermissionStore().setTeam('other');await flushPromises()
    expect(w.findComponent(MCPTokenSecret).props('token')).toBe('')
  })
  it('retains an unrelated one-time secret after a delayed old-token revocation', async () => {
    const w = render(); await flushPromises(); await fill(w)
    await w.get('form').trigger('submit'); await flushPromises()
    const revocation = deferred<void>()
    mocks.revoke.mockReturnValueOnce(revocation.promise)
    w.findComponent(MCPTokenList).vm.$emit('revoke', item); await flushPromises()
    revocation.resolve(undefined); await flushPromises()
    expect(w.get('[data-token-id="t1"]').text()).toContain('已撤销')
    expect(w.findComponent(MCPTokenSecret).props('token')).toBe('one-time-secret')
    w.findComponent(MCPTokenList).vm.$emit('revoke', { ...item, id: 't2' }); await flushPromises()
    expect(w.findComponent(MCPTokenSecret).props('token')).toBe('')
  })
})

it('blocks issuing a multi-project token if any project action is denied, including form submit', async () => {
 useSessionStore().setUser({ username: 'member', userId: 'user', multiTenant: true }); usePermissionStore().setTeam('team')
 mocks.list.mockResolvedValue([]); mocks.projects.mockResolvedValue([{id:'p1',name:'One'},{id:'p2',name:'Two'}])
 vi.mocked(access.decisions).mockResolvedValue({userId:'user',teamId:'team',modeEpoch:1,revision:1,catalogVersion:1,allowed:[true,false]})
 const wrapper=render(); await flushPromises(); await wrapper.get('#mcp-name').setValue('Scoped')
 wrapper.findComponent(MCPTokenForm).findComponent(NSelect).vm.$emit('update:value',['p1','p2']); await flushPromises()
 expect(wrapper.get('.mcp-form button[type="submit"]').attributes('disabled')).toBeDefined()
 await wrapper.get('.mcp-form').trigger('submit'); await flushPromises(); expect(mocks.create).not.toHaveBeenCalled()
 wrapper.unmount()
})

describe('MCP actual token buttons with employee context',()=>{
 beforeEach(()=>{
  vi.resetAllMocks();useSessionStore().setUser({username:'employee',userId:'u1',multiTenant:true});usePermissionStore().setTeam('team-a');
  vi.mocked(access.decisions).mockImplementation(async(team,queries)=>({userId:useSessionStore().user!.userId!,teamId:team,modeEpoch:1,revision:1,catalogVersion:1,allowed:queries.map(()=>true)}));
  mocks.list.mockResolvedValue([item]);mocks.projects.mockResolvedValue([{id:'p1',name:'Local project'}]);mocks.create.mockResolvedValue({token:'one-time-secret',item:{...item,id:'t2'}});mocks.confirm.mockResolvedValue(true);mocks.revoke.mockResolvedValue(undefined);
 });
 afterEach(()=>{for(const w of wrappers.splice(0))w.unmount();vi.restoreAllMocks()});
 it('clicks the actual create submit and close buttons with fixed team',async()=>{
  const w=render(true);await flushPromises();await fill(w);await w.get('.mcp-list-actions button.app-btn--primary').trigger('click');await flushPromises();
  await w.get('form button[type="submit"]').trigger('click');await flushPromises();
  expect(mocks.create).toHaveBeenCalledOnce();expect(mocks.create.mock.calls[0][1]).toMatchObject({headers:{'X-Pipewright-Team':'team-a'},signal:expect.any(AbortSignal)});
  const close=document.querySelector<HTMLButtonElement>('.mcp-secret-close');expect(close).not.toBeNull();close!.click();await flushPromises();expect(w.findComponent(MCPTokenSecret).props('token')).toBe('');
 });
 it('cannot submit the real create button after issuance permission is revoked',async()=>{
  const w=render(true);await flushPromises();await fill(w);vi.mocked(access.decisions).mockImplementation(async(team,queries)=>({userId:'u1',teamId:team,modeEpoch:1,revision:2,catalogVersion:1,allowed:queries.map(()=>false)}));usePermissionStore().invalidate();await flushPromises();
  const b=w.get('form button[type="submit"]');expect((b.element as HTMLButtonElement).disabled).toBe(true);await b.trigger('click');await flushPromises();expect(mocks.create).not.toHaveBeenCalled();
 });
 it.each(['allow','cancel','team','employee'])('actual revoke row button respects %s confirmation',async mode=>{
  const pending=deferred<boolean>();mocks.confirm.mockReturnValue(pending.promise);const w=render(true);await flushPromises();await w.get('[data-token-id="t1"] button').trigger('click');await flushPromises();expect(mocks.confirm).toHaveBeenCalledOnce();
  if(mode==='team')usePermissionStore().setTeam('team-b');if(mode==='employee')useSessionStore().setUser({username:'other',userId:'u2',multiTenant:true});await flushPromises();pending.resolve(mode!=='cancel');await flushPromises();
  if(mode==='allow')expect(mocks.revoke).toHaveBeenCalledWith('t1',expect.objectContaining({headers:{'X-Pipewright-Team':'team-a'},signal:expect.any(AbortSignal)}));else expect(mocks.revoke).not.toHaveBeenCalled();
 });

 it('clicks endpoint copy, refresh, token copy and footer close',async()=>{
  mocks.copy.mockResolvedValue(true);const w=render(true);await flushPromises();await w.get('.mcp-copy').trigger('click');await flushPromises();expect(mocks.copy).toHaveBeenCalledWith(expect.stringContaining('/mcp'),expect.any(Function));
  const count=mocks.list.mock.calls.length;await w.get('.mcp-list-actions button').trigger('click');await flushPromises();expect(mocks.list.mock.calls.length).toBe(count+1);
  await fill(w);await w.get('.mcp-list-actions button.app-btn--primary').trigger('click');await flushPromises();await w.get('form button[type="submit"]').trigger('click');await flushPromises();
  const copy=document.querySelector<HTMLButtonElement>('.mcp-secret-copy');expect(copy).not.toBeNull();copy!.click();await flushPromises();expect(mocks.copy).toHaveBeenLastCalledWith('one-time-secret',expect.any(Function));
  const close=document.querySelector<HTMLButtonElement>('.mcp-secret-footer button');expect(close).not.toBeNull();close!.click();await flushPromises();expect(w.findComponent(MCPTokenSecret).props('token')).toBe('');
 });
});
