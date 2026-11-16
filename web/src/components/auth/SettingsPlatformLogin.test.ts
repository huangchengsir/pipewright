import {ref} from 'vue'
import {createPinia,setActivePinia} from 'pinia'
import {useSessionStore} from '../../stores/session'
import {usePermissionStore} from '../../stores/permissions'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, DOMWrapper, type VueWrapper } from '@vue/test-utils'
import SettingsPlatformLogin from './SettingsPlatformLogin.vue'
import { PLATFORM_PROVIDERS, type PlatformSnapshot, type PlatformSettings } from '../../api/platformLogin'
import { HttpError } from '../../api/http'

const m = vi.hoisted(() => ({
  get: vi.fn(), visibility: vi.fn(), app: vi.fn(), reauth: vi.fn(), bind: vi.fn(), pending: vi.fn(), confirmBinding: vi.fn(),
  enabled: vi.fn(), unbind: vi.fn(), confirm: vi.fn(), success: vi.fn(), clearSession: vi.fn(), replace: vi.fn(),
  route: { path: '/settings/account', query: {} as Record<string, string> },
}))
vi.mock('../../api/platformLogin', async () => ({
  ...await vi.importActual<typeof import('../../api/platformLogin')>('../../api/platformLogin'),
  getPlatformSettings: (...args: unknown[]) => m.get(...args),
  savePlatformVisibility: (...args: unknown[]) => m.visibility(...args),
  savePlatformApp: (...args: unknown[]) => m.app(...args),
  reauthenticatePlatform: (...args: unknown[]) => m.reauth(...args),
  startPlatformBinding: (...args: unknown[]) => m.bind(...args),
  getPlatformCandidate: (...args: unknown[]) => m.pending(...args),
  confirmPlatformBinding: (...args: unknown[]) => m.confirmBinding(...args),
  setPlatformEnabled: (...args: unknown[]) => m.enabled(...args),
  unbindPlatform: (...args: unknown[]) => m.unbind(...args),
}))
const confirmPending=ref<{options:unknown;resolve:(value:boolean)=>void}|null>(null)
vi.mock('../../composables/useConfirm', () => ({ useConfirm: () => ({ open: m.confirm, pending:confirmPending, _resolve:(value:boolean)=>{confirmPending.value?.resolve(value);confirmPending.value=null} }) }))
vi.mock('../../composables/useToast', () => ({ useToast: () => ({ success: m.success }) }))
vi.mock('vue-router', () => ({ useRoute: () => m.route, useRouter: () => ({ replace: m.replace }) }))

function snapshot(recent = true): PlatformSnapshot {
  return {
    canManage: true, visibilityVersion: 4, reauthenticatedUntil: recent ? new Date(Date.now() + 300000).toISOString() : '',
    providers: PLATFORM_PROVIDERS.map(provider => ({
      provider, visible: true, enabled: false, configured: provider === 'github', hasSecret: provider === 'github',
      clientId: provider === 'github' ? 'saved-app' : '', microsoftTenant: 'common', giteeMode: '',
      callbackURL: 'https://pipewright.example/api/auth/platform/' + provider + '/callback', configVersion: 3,
    })),
  }
}
function row(extra: Partial<PlatformSettings> = {}): PlatformSettings { return { ...snapshot().providers[0], ...extra } }
let wrapper: VueWrapper | undefined
function button(label: string) {
  const found = wrapper!.findAll('button').find(b => b.text() === label)
  if (!found) throw new Error('Button not found: ' + label)
  return found
}
async function mountSettings() {
  wrapper = mount(SettingsPlatformLogin, { attachTo: document.body, global: { stubs: { teleport: true } } })
  await flushPromises()
}
beforeEach(() => {
  for (const value of Object.values(m)) if (typeof value === 'function' && 'mockReset' in value) value.mockReset()
  setActivePinia(createPinia())
  useSessionStore().setUser({username:'admin',userId:'admin',multiTenant:false,instanceAdmin:true})
  vi.spyOn(useSessionStore(),'clearSession').mockImplementation(m.clearSession)
  confirmPending.value=null
  m.route.query = {}
  m.get.mockImplementation(async () => snapshot())
  m.confirm.mockResolvedValue(true)
  m.replace.mockResolvedValue(undefined)
  m.reauth.mockResolvedValue({ reauthenticatedUntil: new Date(Date.now() + 300000).toISOString() })
  m.app.mockResolvedValue({ provider: row({ configVersion: 4 }), sessionRevoked: false })
  Object.defineProperty(HTMLDialogElement.prototype, 'showModal', { configurable: true, value() { this.setAttribute('open', '') } })
  Object.defineProperty(HTMLDialogElement.prototype, 'close', { configurable: true, value() { this.removeAttribute('open') } })
})
afterEach(() => { wrapper?.unmount(); wrapper = undefined; document.body.innerHTML = '' })

describe('account platform sign-in settings', () => {
  it('does not invent default instance settings after a failed read', async () => {
    m.get.mockRejectedValue(new Error('DO_NOT_RENDER_RAW_FAILURE'))
    await mountSettings()
    expect(wrapper!.findAll('[role="switch"]')).toHaveLength(0)
    expect(wrapper!.text()).toContain('无法加载登录设置')
    expect(wrapper!.text()).not.toContain('DO_NOT_RENDER_RAW_FAILURE')
  })
  it('saves a complete visibility policy independently from enabled and configured', async () => {
    const saved = snapshot(); saved.visibilityVersion = 5; saved.providers[0].visible = false
    m.visibility.mockResolvedValue(saved)
    await mountSettings()
    await wrapper!.get('#platform-visible-github').trigger('click')
    await button('保存显示设置').trigger('click'); await flushPromises()
    expect(m.visibility).toHaveBeenCalledWith(4, { github: false, gitlab: true, gitee: true, google: true, microsoft: true }, expect.objectContaining({throwOnUnauthorized:true}))
    expect(m.app).not.toHaveBeenCalled()
    expect(m.enabled).not.toHaveBeenCalled()
    expect(m.success).toHaveBeenCalledWith('显示设置已保存')
  })
  it('preserves visibility drafts on a conflict and reloads a new expected version', async () => {
    m.visibility.mockRejectedValue(new HttpError(409, { code: 'platform_config_conflict', message: 'raw' }, 'raw'))
    await mountSettings()
    await wrapper!.get('#platform-visible-github').trigger('click')
    await button('保存显示设置').trigger('click'); await flushPromises()
    expect(wrapper!.get('#platform-visible-github').attributes('aria-checked')).toBe('false')
    const current = snapshot(); current.visibilityVersion = 8
    m.get.mockResolvedValue(current)
    await button('刷新核对').trigger('click'); await flushPromises()
    expect(wrapper!.get('#platform-visible-github').attributes('aria-checked')).toBe('false')
    await button('保存显示设置').trigger('click'); await flushPromises()
    expect(m.visibility).toHaveBeenLastCalledWith(8, expect.objectContaining({ github: false }), expect.objectContaining({throwOnUnauthorized:true}))
  })
  it('blocks another ambiguous save until explicit read-back, without automatic retries', async () => {
    m.visibility.mockRejectedValue(new HttpError(0, null, 'response lost'))
    await mountSettings()
    await wrapper!.get('#platform-visible-github').trigger('click')
    await button('保存显示设置').trigger('click'); await flushPromises()
    expect(button('保存显示设置').attributes('disabled')).toBeDefined()
    expect(m.visibility).toHaveBeenCalledTimes(1)
    const actual = snapshot(); actual.visibilityVersion = 5; actual.providers[0].visible = false
    m.get.mockResolvedValue(actual)
    await button('刷新核对').trigger('click'); await flushPromises()
    expect(m.visibility).toHaveBeenCalledTimes(1)
    expect(button('保存显示设置').attributes('disabled')).toBeDefined()
    expect(m.success).not.toHaveBeenCalled()
  })
  it('never prefills or echoes a stored secret and prevents discarding an unsaved app in a bind action', async () => {
    const actual = snapshot(); Object.assign(actual.providers[0], { clientSecret: 'SERVER_SECRET_CANARY' })
    m.get.mockResolvedValue(actual)
    await mountSettings()
    await wrapper!.get('[data-provider="github"] .platform-configure').trigger('click'); await flushPromises()
    expect((wrapper!.get('#platform-client-secret').element as HTMLInputElement).value).toBe('')
    expect(wrapper!.text()).not.toContain('SERVER_SECRET_CANARY')
    await wrapper!.get('#platform-client-id').setValue('new-app')
    expect(button('绑定账号').attributes('disabled')).toBeDefined()
    expect(wrapper!.text()).toContain('先保存或取消应用更改')
    await wrapper!.get('.platform-app-form').trigger('submit'); await flushPromises()
    expect(m.app).not.toHaveBeenCalled()
  })
  it('clears passwords and secrets, handles a wrong password locally, then performs one deliberate save', async () => {
    m.get.mockResolvedValue(snapshot(false))
    m.reauth.mockRejectedValueOnce(new HttpError(422, { code: 'invalid_current_password', message: 'raw' }, 'raw'))
      .mockResolvedValueOnce({ reauthenticatedUntil: new Date(Date.now() + 300000).toISOString() })
    await mountSettings()
    await wrapper!.get('[data-provider="github"] .platform-configure').trigger('click'); await flushPromises()
    await wrapper!.get('#platform-client-secret').setValue('NEW_SECRET_CANARY')
    await wrapper!.get('.platform-app-form').trigger('submit'); await flushPromises()
    expect(wrapper!.find('#platform-client-secret').exists()).toBe(false)
    await wrapper!.get('#platform-current-password').setValue('wrong-fixture-value')
    await wrapper!.get('.platform-app-form').trigger('submit'); await flushPromises()
    expect(wrapper!.text()).toContain('当前密码不正确')
    expect((wrapper!.get('#platform-current-password').element as HTMLInputElement).value).toBe('')
    expect(m.app).not.toHaveBeenCalled()
    await wrapper!.get('#platform-current-password').setValue('correct-fixture-value')
    await wrapper!.get('.platform-app-form').trigger('submit'); await flushPromises()
    expect(m.app).toHaveBeenCalledTimes(1)
    expect(m.app).toHaveBeenCalledWith('github', { expectedVersion: 3, clientId: 'saved-app', clientSecret: 'NEW_SECRET_CANARY' }, expect.objectContaining({throwOnUnauthorized:true}))
    expect(wrapper!.text()).not.toContain('NEW_SECRET_CANARY')
    expect(wrapper!.find('#platform-current-password').exists()).toBe(false)
  })
  it('drops a pending sensitive action when password confirmation is canceled', async () => {
    m.get.mockResolvedValue(snapshot(false))
    await mountSettings()
    await wrapper!.get('[data-provider="github"] .platform-configure').trigger('click'); await flushPromises()
    await wrapper!.get('#platform-client-secret').setValue('DISCARDED_SECRET_CANARY')
    await wrapper!.get('.platform-app-form').trigger('submit'); await flushPromises()
    await button('取消').trigger('click'); await flushPromises()
    expect(m.app).not.toHaveBeenCalled()
    expect(wrapper!.find('#platform-current-password').exists()).toBe(false)
  })
  it('confirms only the server candidate flow and version, never a client supplied subject', async () => {
    m.route.query = { platform_binding: 'server-flow', subject: 'forged-client-subject' }
    m.pending.mockResolvedValue({ flowId: 'server-flow', provider: 'github', displayLabel: 'Server verified account', expiresAt: new Date(Date.now() + 300000).toISOString(), configVersion: 3 })
    m.confirmBinding.mockResolvedValue({ provider: row({ enabled: true, binding: { displayLabel: 'Server verified account', issuer: 'https://github.com', subject: '9007199254740993', boundAt: new Date().toISOString() } }), sessionRevoked: false })
    await mountSettings()
    expect(m.replace).toHaveBeenCalledWith({ path: '/settings/account', query: { subject: 'forged-client-subject' } })
    expect(wrapper!.text()).toContain('Server verified account')
    expect(wrapper!.text()).not.toContain('forged-client-subject')
    await button('确认绑定并启用').trigger('click'); await flushPromises()
    expect(m.confirmBinding).toHaveBeenCalledWith('github', 3, 'server-flow', expect.objectContaining({throwOnUnauthorized:true}))
  })
  it('returns to password login when the server says the current source session was revoked', async () => {
    const actual = snapshot(); actual.providers[0] = row({ enabled: true, binding: { displayLabel: 'linked', issuer: 'https://github.com', subject: '42', boundAt: '2026-11-02T00:00:00Z' } })
    m.get.mockResolvedValue(actual)
    m.enabled.mockResolvedValue({ provider: row({ enabled: false }), sessionRevoked: true })
    await mountSettings()
    await wrapper!.get('[data-provider="github"] .platform-configure').trigger('click'); await flushPromises()
    await button('停用').trigger('click'); await flushPromises()
    expect(m.confirm).toHaveBeenCalledTimes(1)
    expect(m.enabled).toHaveBeenCalledWith('github', 3, false, expect.objectContaining({throwOnUnauthorized:true}))
    expect(m.clearSession).toHaveBeenCalledTimes(1)
    expect(m.replace).toHaveBeenLastCalledWith({ name: 'login', query: { redirect: '/settings/account' } })
  })
})

it('review regression: keeps a new visibility draft made during an in-flight refresh', async () => {
 await mountSettings()
 let finish!: (v: PlatformSnapshot) => void
 m.get.mockReturnValueOnce(new Promise<PlatformSnapshot>(resolve => { finish = resolve }))
 await button('刷新核对').trigger('click')
 await wrapper!.get('#platform-visible-github').trigger('click')
 expect(wrapper!.get('#platform-visible-github').attributes('aria-checked')).toBe('false')
 finish(snapshot()); await flushPromises()
 expect(wrapper!.get('#platform-visible-github').attributes('aria-checked')).toBe('false')
})
it('review regression: a transient candidate-read failure remains retryable on explicit refresh', async () => {
 m.route.query = { platform_binding: 'unexpired-server-flow' }
 m.replace.mockImplementation(async value => { m.route.query = value.query })
 m.pending.mockRejectedValueOnce(new HttpError(0, null, 'transient network error')).mockResolvedValue({ flowId: 'unexpired-server-flow', provider: 'github', displayLabel: 'Verified candidate', expiresAt: new Date(Date.now() + 300000).toISOString(), configVersion: 3 })
 await mountSettings()
 expect(m.pending).toHaveBeenCalledTimes(1)
 expect(wrapper!.text()).toContain('暂时无法读取待绑定账号')
 expect(wrapper!.text()).not.toContain('授权已过期')
 expect(m.route.query.platform_binding).toBe('unexpired-server-flow')
 await button('刷新核对').trigger('click'); await flushPromises()
 expect(m.pending).toHaveBeenCalledTimes(2)
 expect(m.route.query.platform_binding).toBeUndefined()
 expect(wrapper!.text()).toContain('Verified candidate')
 expect(wrapper!.text()).not.toContain('暂时无法读取待绑定账号')
})


describe('employee platform bindings', () => {
 it('shows own binding controls without global application or visibility fields', async () => {
  const data = snapshot(); data.canManage = false; data.providers[0] = {...data.providers[0], enabled:true, binding:{displayLabel:'My GitHub',issuer:'https://github.com',subject:'own-subject',boundAt:'2026-01-01'}}
  m.get.mockResolvedValue(data)
  await mountSettings()
  expect(wrapper!.findAll('[role="switch"]')).toHaveLength(0)
  expect(wrapper!.text()).not.toContain('保存显示设置')
  await button('管理绑定').trigger('click'); await flushPromises()
  expect(wrapper!.find('#platform-client-id').exists()).toBe(false)
  expect(wrapper!.find('#platform-client-secret').exists()).toBe(false)
  expect(wrapper!.find('#platform-callback').exists()).toBe(false)
  expect(wrapper!.text()).toContain('My GitHub')
  expect(wrapper!.findAll('button').some(b=>b.text()==='停用')).toBe(false)
  m.unbind.mockResolvedValue({provider:row({enabled:true}),sessionRevoked:false})
  await button('解除绑定').trigger('click'); await flushPromises()
  expect(m.unbind).toHaveBeenCalledWith('github',3, expect.objectContaining({throwOnUnauthorized:true}))
  expect(m.app).not.toHaveBeenCalled(); expect(m.enabled).not.toHaveBeenCalled(); expect(m.visibility).not.toHaveBeenCalled()
 })
 it('denies global controls if capability is absent', async () => {
  const data = snapshot(); delete (data as Partial<PlatformSnapshot>).canManage
  m.get.mockResolvedValue(data); await mountSettings()
  expect(wrapper!.findAll('[role="switch"]')).toHaveLength(0)
  expect(wrapper!.text()).not.toContain('保存显示设置')
 })
})
function linkedSnapshot(recent=true):PlatformSnapshot {const data=snapshot(recent);data.providers[0]=row({enabled:true,binding:{displayLabel:'Old employee GitHub',issuer:'https://github.com',subject:'old-subject',boundAt:'2026-11-13T00:00:00Z'}});return data}
function employeeChange(){useSessionStore().setUser({username:'new-employee',userId:'new-user',multiTenant:true,instanceAdmin:false})}
it.each(['停用','解除绑定'])('does not execute old employee %s confirmation after identity changes',async(label)=>{
 let resolve!:(accepted:boolean)=>void;m.confirm.mockImplementation(()=>new Promise<boolean>(r=>resolve=r));m.get.mockResolvedValue(linkedSnapshot());m.enabled.mockResolvedValue({provider:row(),sessionRevoked:false});m.unbind.mockResolvedValue({provider:row(),sessionRevoked:false})
 await mountSettings();await wrapper!.get('[data-provider="github"] .platform-configure').trigger('click');await flushPromises();await button(label).trigger('click');employeeChange();resolve(true);await flushPromises();expect(m.enabled).not.toHaveBeenCalled();expect(m.unbind).not.toHaveBeenCalled();expect(wrapper!.text()).not.toContain('Old employee GitHub')
})
it('cancels pending reauthentication and clears secret fields immediately on employee change',async()=>{
 m.get.mockResolvedValue(snapshot(false));await mountSettings();await wrapper!.get('[data-provider="github"] .platform-configure').trigger('click');await flushPromises();await wrapper!.get('#platform-client-secret').setValue('old-secret');await wrapper!.get('.platform-app-form').trigger('submit');await flushPromises();await wrapper!.get('#platform-current-password').setValue('old-password');employeeChange();await flushPromises();expect(wrapper!.find('#platform-current-password').exists()).toBe(false);expect(wrapper!.find('[open]').exists()).toBe(false);expect(m.app).not.toHaveBeenCalled()
})
it('drops old successful reauthentication without executing its pending application save',async()=>{
 let resolve!:(v:{reauthenticatedUntil:string})=>void;m.reauth.mockImplementation(()=>new Promise(r=>resolve=r));m.get.mockResolvedValue(snapshot(false));await mountSettings();await wrapper!.get('[data-provider="github"] .platform-configure').trigger('click');await flushPromises();await wrapper!.get('#platform-client-secret').setValue('old-secret');await wrapper!.get('.platform-app-form').trigger('submit');await flushPromises();await wrapper!.get('#platform-current-password').setValue('old-password');await wrapper!.get('.platform-app-form').trigger('submit');employeeChange();resolve({reauthenticatedUntil:new Date(Date.now()+300000).toISOString()});await flushPromises();expect(m.app).not.toHaveBeenCalled();expect(wrapper!.find('[open]').exists()).toBe(false)
})
it('does not apply an old mutation or log out the new employee from its late sessionRevoked reply',async()=>{
 let resolve!:(v:unknown)=>void;m.unbind.mockImplementation(()=>new Promise(r=>resolve=r));m.get.mockResolvedValue(linkedSnapshot());await mountSettings();await wrapper!.get('[data-provider="github"] .platform-configure').trigger('click');await flushPromises();await button('解除绑定').trigger('click');await flushPromises();employeeChange();resolve({provider:row(),sessionRevoked:true});await flushPromises();expect(m.clearSession).not.toHaveBeenCalled();expect(m.replace).not.toHaveBeenCalled();expect(m.success).not.toHaveBeenCalled();expect(wrapper!.emitted('changed')).toBeUndefined()
})
it('discards old settings response on team switch',async()=>{
 let resolve!:(v:PlatformSnapshot)=>void;m.get.mockImplementation(()=>new Promise(r=>resolve=r));await mountSettings();usePermissionStore().setTeam('other-team');resolve(linkedSnapshot());await flushPromises();expect(wrapper!.text()).not.toContain('Old employee GitHub');expect(wrapper!.findAll('[data-provider]')).toHaveLength(0)
})
it('discards old binding candidate before routing or presenting it to another employee',async()=>{
 let resolve!:(v:unknown)=>void;m.route.query={platform_binding:'old-flow'};m.pending.mockImplementation(()=>new Promise(r=>resolve=r));await mountSettings();employeeChange();resolve({flowId:'old-flow',provider:'github',displayLabel:'Private old account',expiresAt:new Date(Date.now()+300000).toISOString(),configVersion:3});await flushPromises();expect(m.replace).not.toHaveBeenCalled();expect(wrapper!.text()).not.toContain('Private old account');expect(wrapper!.find('[open]').exists()).toBe(false)
})
it.each(PLATFORM_PROVIDERS)('keeps %s employee unbinding fixed to current account and team',async(provider)=>{
 useSessionStore().setUser({username:'employee',userId:'u',multiTenant:true,instanceAdmin:false});usePermissionStore().setTeam('team-a');const data=snapshot();data.canManage=false;data.providers=data.providers.map(p=>({...p,enabled:true,configured:true,binding:p.provider===provider?{displayLabel:'Own account',issuer:'https://'+provider+'.test',subject:'own-subject',boundAt:'2026-11-13T00:00:00Z'}:undefined}));m.get.mockResolvedValue(data);m.unbind.mockResolvedValue({provider:data.providers.find(p=>p.provider===provider),sessionRevoked:false});await mountSettings();await wrapper!.get('[data-provider="'+provider+'"] .platform-configure').trigger('click');await flushPromises();await button('解除绑定').trigger('click');await flushPromises();expect(m.get.mock.calls[0][1]).toMatchObject({headers:{'X-Pipewright-Team':'team-a'}});expect(m.unbind).toHaveBeenCalledWith(provider,3,expect.objectContaining({headers:{'X-Pipewright-Team':'team-a'},signal:expect.any(AbortSignal)}));expect(wrapper!.find('#platform-client-secret').exists()).toBe(false)
})
it('cancels old admin confirmation when a refreshed snapshot removes management capability',async()=>{
 let resolve!:(v:boolean)=>void;m.confirm.mockImplementation(()=>new Promise(r=>resolve=r));m.get.mockResolvedValue(linkedSnapshot());await mountSettings();await wrapper!.get('[data-provider="github"] .platform-configure').trigger('click');await flushPromises();await button('停用').trigger('click');const data=linkedSnapshot();data.canManage=false;m.get.mockResolvedValue(data);await button('刷新核对').trigger('click');await flushPromises();resolve(true);await flushPromises();expect(m.enabled).not.toHaveBeenCalled();expect(m.reauth).not.toHaveBeenCalled();expect(wrapper!.find('[open]').exists()).toBe(false)
})
it('aborts in-flight mutation context when the fixed employee changes',async()=>{
 let resolve!:(v:unknown)=>void;m.unbind.mockImplementation(()=>new Promise(r=>resolve=r));m.get.mockResolvedValue(linkedSnapshot());await mountSettings();await wrapper!.get('[data-provider="github"] .platform-configure').trigger('click');await flushPromises();await button('解除绑定').trigger('click');await flushPromises();const signal=m.unbind.mock.calls[0][2].signal as AbortSignal;employeeChange();expect(signal.aborted).toBe(true);resolve({provider:row(),sessionRevoked:false});await flushPromises();expect(wrapper!.find('[data-provider]').exists()).toBe(false)
})

it('settles only the owned global confirmation on identity change',async()=>{
 m.confirm.mockImplementation(options=>new Promise<boolean>(resolve=>{confirmPending.value={options,resolve}}));m.get.mockResolvedValue(linkedSnapshot());await mountSettings();await wrapper!.get('[data-provider="github"] .platform-configure').trigger('click');await flushPromises();await button('解除绑定').trigger('click');expect(confirmPending.value).not.toBeNull();employeeChange();await flushPromises();expect(confirmPending.value).toBeNull();expect(m.unbind).not.toHaveBeenCalled()
})
it('clicks callback copy and native application/password submit controls',async()=>{
 const copy=vi.fn().mockResolvedValue(undefined);Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText:copy}});
 m.get.mockResolvedValue(snapshot(false));await mountSettings();await wrapper!.get('[data-provider="github"] .platform-configure').trigger('click');await flushPromises();
 await button('复制').trigger('click');await flushPromises();expect(copy).toHaveBeenCalledWith(row().callbackURL);
 await wrapper!.get('#platform-client-secret').setValue('NEW_SECRET_CANARY');await wrapper!.get('.platform-app-form button[type="submit"]').trigger('click');await flushPromises();
 await wrapper!.get('#platform-current-password').setValue('fixture');await wrapper!.get('.platform-app-form button[type="submit"]').trigger('click');await flushPromises();expect(m.reauth).toHaveBeenCalledOnce();expect(m.app).toHaveBeenCalledOnce();
});
it('clicks binding start without allowing an unsafe redirect',async()=>{
 m.bind.mockResolvedValue({authorizeURL:'https://invalid.example/unsafe'});await mountSettings();await wrapper!.get('[data-provider="github"] .platform-configure').trigger('click');await flushPromises();await button('绑定账号').trigger('click');await flushPromises();expect(m.bind).toHaveBeenCalledWith('github',3,expect.objectContaining({throwOnUnauthorized:true}));
});
it.each(['app','candidate'])('clicks explicit %s result read-back without repeating a mutation',async kind=>{
 const show=vi.spyOn(HTMLDialogElement.prototype,'showModal');
 if(kind==='candidate'){m.route.query={platform_binding:'flow'};m.pending.mockResolvedValue({flowId:'flow',provider:'github',displayLabel:'Own',expiresAt:new Date(Date.now()+300000).toISOString(),configVersion:3});m.confirmBinding.mockRejectedValue(new HttpError(0,null,'lost'));}
 else m.app.mockRejectedValue(new HttpError(409,{code:'platform_config_conflict',message:'conflict'},'conflict'));
 wrapper=mount(SettingsPlatformLogin,{attachTo:document.body});await flushPromises();const view=new DOMWrapper(document.body);
 if(kind==='app'){await view.get('[data-provider="github"] .platform-configure').trigger('click');await flushPromises();await view.get('#platform-client-secret').setValue('NEW_SECRET_CANARY');await view.get('.platform-app-form button[type="submit"]').trigger('click');}
 else {expect(show).toHaveBeenCalled();expect(view.get('dialog').element.hasAttribute('open'),'candidate open before action').toBe(true);await view.findAll('button').find(b=>b.text()==='确认绑定并启用')!.trigger('click');}await flushPromises();
 expect(show).toHaveBeenCalled();const calls=m.get.mock.calls.length;const dialog=view.get('dialog[open]');const reload=dialog.findAll('button').find(b=>b.text()==='刷新核对');expect(reload).toBeDefined();await reload!.trigger('click');await flushPromises();expect(m.get.mock.calls.length).toBe(calls+1);expect(kind==='app'?m.app:m.confirmBinding).toHaveBeenCalledOnce();
});
it.each(['app-header','app-footer','reauth-header','reauth-footer','candidate-header','candidate-footer'])('clicks the %s dialog close control and clears pending state',async variant=>{
 if(variant.startsWith('candidate')){m.route.query={platform_binding:'flow'};m.pending.mockResolvedValue({flowId:'flow',provider:'github',displayLabel:'Own',expiresAt:new Date(Date.now()+300000).toISOString(),configVersion:3});}
 if(variant.startsWith('reauth'))m.get.mockResolvedValue(snapshot(false));
 wrapper=mount(SettingsPlatformLogin,{attachTo:document.body});await flushPromises();const view=new DOMWrapper(document.body);
 if(!variant.startsWith('candidate')){await view.get('[data-provider="github"] .platform-configure').trigger('click');await flushPromises();}
 if(variant.startsWith('reauth')){await view.get('#platform-client-secret').setValue('DRAFT');await view.get('.platform-app-form button[type="submit"]').trigger('click');await flushPromises();await view.get('#platform-current-password').setValue('fixture');}
 const dialog=view.get('dialog[open]');const buttons=variant.endsWith('header')?dialog.findAll('header button'):dialog.findAll('.platform-dialog__actions button');const close=buttons.find(b=>b.text()===(variant.endsWith('header')?'关闭':'取消'));expect(close).toBeDefined();await close!.trigger('click');await flushPromises();expect(view.find('dialog[open]').exists()).toBe(false);expect(m.app).not.toHaveBeenCalled();expect(m.confirmBinding).not.toHaveBeenCalled();
});
it('clicks failed-read retry without inventing settings or a mutation',async()=>{
 m.get.mockRejectedValueOnce(new Error('unavailable'));await mountSettings();m.get.mockResolvedValue(snapshot());const retry=wrapper!.get('.platform-feedback[role="alert"] button');await retry.trigger('click');await flushPromises();expect(m.get).toHaveBeenCalledTimes(2);expect(wrapper!.find('[data-provider="github"]').exists()).toBe(true);expect(m.app).not.toHaveBeenCalled();
});

describe('employee provider availability dialog', () => {
  it.each(PLATFORM_PROVIDERS.flatMap(provider => [
    {provider, configured:false}, {provider, configured:true},
  ]))('explains $provider availability when configured=$configured without an empty binding dialog', async ({provider,configured}) => {
    useSessionStore().setUser({username:'employee',userId:'employee-a',multiTenant:true,instanceAdmin:false})
    usePermissionStore().setTeam('team-a')
    const data=snapshot(); data.canManage=false
    data.providers=data.providers.map(p=>({...p,configured,enabled:false,binding:undefined}))
    m.get.mockResolvedValue(data)
    wrapper=mount(SettingsPlatformLogin,{attachTo:document.body,global:{stubs:{teleport:false}}})
    await flushPromises()
    await wrapper.get('[data-provider="'+provider+'"] .platform-configure').trigger('click')
    await flushPromises()
    const dialog=new DOMWrapper(document.querySelector('dialog[open]')!)
    expect(dialog.get('.platform-unavailable').text()).toContain(configured?'已暂停':'尚未')
    expect(dialog.get('#platform-dialog-heading').text()).toContain('账号绑定')
    expect(dialog.find('#platform-client-id').exists()).toBe(false)
    expect(dialog.find('#platform-client-secret').exists()).toBe(false)
    const footer=dialog.get('.platform-member-footer')
    await footer.findAll('button').find(b=>b.text()==='关闭')!.trigger('click')
    expect(document.querySelector('dialog[open]')).toBeNull()
    expect(m.bind).not.toHaveBeenCalled(); expect(m.app).not.toHaveBeenCalled()
  })
})


it('reads back the exact platform notice control without resubmitting after an uncertain write', async()=>{
  m.visibility.mockRejectedValue(new HttpError(0,null,'response lost'));
  await mountSettings(); await wrapper!.get('#platform-visible-github').trigger('click'); await button('保存显示设置').trigger('click'); await flushPromises();
  const notice=wrapper!.get('.platform-feedback[role="alert"] .platform-text-button');
  await notice.trigger('click'); await flushPromises();
  expect(m.get).toHaveBeenCalledTimes(2);expect(m.visibility).toHaveBeenCalledOnce();
  employeeChange();await flushPromises();notice.element.dispatchEvent(new MouseEvent('click',{bubbles:true}));await flushPromises();expect(m.get).toHaveBeenCalledTimes(2);
});
it.each(PLATFORM_PROVIDERS)('refreshes %s unavailable member dialog using only own settings read',async provider=>{
  useSessionStore().setUser({username:'employee',userId:'employee-a',multiTenant:true,instanceAdmin:false});usePermissionStore().setTeam('team-a');
  const data=snapshot();data.canManage=false;data.providers=data.providers.map(p=>({...p,configured:false,enabled:false,binding:undefined}));m.get.mockResolvedValue(data);
  wrapper=mount(SettingsPlatformLogin,{attachTo:document.body,global:{stubs:{teleport:false}}});await flushPromises();
  await wrapper.get('[data-provider="'+provider+'"] .platform-configure').trigger('click');await flushPromises();
  const dialog=new DOMWrapper(document.querySelector('dialog[open]')!);const refresh=dialog.get('.platform-member-footer button');
  await refresh.trigger('click');await flushPromises();expect(document.querySelector('dialog[open]')).toBeNull();expect(m.get).toHaveBeenCalledTimes(2);
  expect(m.get.mock.lastCall![1]).toMatchObject({headers:{'X-Pipewright-Team':'team-a'},throwOnUnauthorized:true});
  for(const fn of [m.app,m.bind,m.visibility,m.enabled,m.unbind,m.confirmBinding]) expect(fn).not.toHaveBeenCalled();
  employeeChange();await flushPromises();refresh.element.dispatchEvent(new MouseEvent('click',{bubbles:true}));await flushPromises();expect(m.get).toHaveBeenCalledTimes(2);
});
