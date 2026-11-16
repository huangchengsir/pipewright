import {createPinia,setActivePinia} from 'pinia'
import {mount,flushPromises,type VueWrapper} from '@vue/test-utils'
import {beforeEach,afterEach,describe,it,expect,vi} from 'vitest'
import {access} from '../../api/access'
import {http,HttpError} from '../../api/http'
import {useSessionStore} from '../../stores/session'
import {usePermissionStore} from '../../stores/permissions'
import PromotionPanel from './PromotionPanel.vue'
vi.mock('../../api/access',()=>({access:{decisions:vi.fn()}}))
vi.mock('../../api/http',async original=>({...await original<typeof import('../../api/http')>(),http:{get:vi.fn(),post:vi.fn()}}))
let read=true,promote=true,approve=true,pending=false
const wrappers:VueWrapper[]=[]
const stage={name:'test',gated:false,environmentId:'env-test'}
const record={id:'promotion',projectId:'p1',sourceRunId:'r1',fromEnvironment:'',targetEnvironment:'test',status:'pending',promotedBy:'member',createdAt:'2026-11-13T00:00:00Z',decidedAt:''}
async function settle(){for(let i=0;i<5;i++)await flushPromises()}
function render(){const w=mount(PromotionPanel,{props:{runId:'r1',projectId:'p1',runStatus:'success'},attachTo:document.body});wrappers.push(w);return w}
beforeEach(()=>{
 vi.resetAllMocks();setActivePinia(createPinia());read=true;promote=true;approve=true;pending=false
 useSessionStore().setUser({username:'member',userId:'u1',multiTenant:true});usePermissionStore().setTeam('t1')
 vi.mocked(access.decisions).mockImplementation(async(team,queries)=>({userId:useSessionStore().user!.userId!,teamId:team,modeEpoch:1,revision:1,catalogVersion:1,allowed:queries.map(q=>q.action==='release.promote'?promote:q.action==='release.approve'?approve:read)}))
 vi.mocked(http.get).mockImplementation(async url=>String(url).endsWith('/promotion-plan')?{configured:true,atTop:false,environments:[stage],next:stage}:{items:pending?[record]:[]})
 vi.mocked(http.post).mockResolvedValue({...record,status:'promoted'})
})
afterEach(()=>{for(const w of wrappers.splice(0))w.unmount();vi.restoreAllMocks()})
describe('promotion button and data permissions',()=>{
 it('no run.read issues no data requests',async()=>{read=false;const w=render();await settle();expect(http.get).not.toHaveBeenCalled();expect(w.find('.promotion-panel').exists()).toBe(false)})
 it('reader sees minimal plan without configuration access or promotion button',async()=>{promote=false;const w=render();await settle();expect(w.text()).toContain('test');expect(w.find('.promo-btn--promote').exists()).toBe(false);expect(http.get).toHaveBeenCalledTimes(2);expect(vi.mocked(http.get).mock.calls.map(x=>x[0])).toEqual(expect.arrayContaining(['/api/runs/r1/promotion-plan','/api/runs/r1/promotions']));expect(vi.mocked(http.get).mock.calls.some(x=>String(x[0]).includes('/environments'))).toBe(false)})
 it('promotes exact displayed target with captured team and cancellation',async()=>{const w=render();await settle();await w.get('.promo-btn--promote').trigger('click');await settle();expect(http.post).toHaveBeenCalledWith('/api/runs/r1/promote',{targetEnvironment:'test'},expect.objectContaining({headers:{'X-Pipewright-Team':'t1'},signal:expect.any(AbortSignal),throwOnUnauthorized:true}))})
 it('requires approval action independently from promotion action',async()=>{pending=true;approve=false;const w=render();await settle();expect(w.text()).toContain('test');expect(w.find('.promo-btn--gate').exists()).toBe(false);expect(w.find('.promo-btn--cancel').exists()).toBe(false);expect(http.post).not.toHaveBeenCalled()})
 it('approves only the displayed pending environment with fixed identity',async()=>{pending=true;const w=render();await settle();vi.mocked(http.post).mockImplementation(async()=>{pending=false;return {ok:true}});await w.get('.promo-btn--gate').trigger('click');await settle();expect(http.post).toHaveBeenCalledWith('/api/runs/r1/approve',{stageId:'promote:test'},expect.objectContaining({headers:{'X-Pipewright-Team':'t1'},signal:expect.any(AbortSignal)}))})
 it('logout aborts requests and discards a stale plan',async()=>{let resolve!:(value:unknown)=>void;vi.mocked(http.get).mockImplementation(url=>String(url).endsWith('/promotion-plan')?new Promise(r=>{resolve=r}):Promise.resolve({items:[]}));const w=render();await settle();const options=vi.mocked(http.get).mock.calls[0]![1]!;useSessionStore().clearSession();await settle();resolve({configured:true,atTop:false,environments:[{name:'private-stale',gated:false}],next:stage});await settle();expect(options.signal?.aborted).toBe(true);expect(w.find('.promotion-panel').exists()).toBe(false);expect(w.text()).not.toContain('private-stale')})
 it('403 while loading clears the mounted data and invalidates permissions',async()=>{vi.mocked(http.get).mockRejectedValue(new HttpError(403,{code:'forbidden',message:'denied'},'denied'));const w=render();await settle();expect(w.text()).not.toContain('test');expect(w.find('.promo-btn--promote').exists()).toBe(false);expect(http.post).not.toHaveBeenCalled()})
})

describe('promotion gated actual DOM actions', () => {
  const gated = { ...stage, gated: true }
  const other = { name: 'production', gated: true, environmentId: 'env-prod' }
  const scope = () => expect.objectContaining({
    headers: { 'X-Pipewright-Team': 't1' }, signal: expect.any(AbortSignal), throwOnUnauthorized: true,
  })

  function exactAuthorization() {
    vi.mocked(access.decisions).mockImplementation(async (team, queries) => ({
      userId: useSessionStore().user!.userId!, teamId: team, modeEpoch: 1, revision: 1, catalogVersion: 1,
      allowed: queries.map(query => {
        if (query.action === 'run.read') return read && query.resourceId === 'r1'
        if (query.resourceId !== 'r1' || query.environmentId !== 'env-test') return false
        return query.action === 'release.promote' ? promote : query.action === 'release.approve' && approve
      }),
    }))
    vi.mocked(http.get).mockImplementation(async url => String(url).endsWith('/promotion-plan')
      ? { configured: true, atTop: false, environments: [gated, other], next: gated }
      : { items: pending ? [record] : [] })
  }

  it('submits the shown gated promotion only after the actual confirm button', async () => {
    approve = false
    exactAuthorization()
    vi.mocked(http.post).mockImplementation(async () => { pending = true; return record })
    const w = render()
    await settle()
    await w.get('.promo-btn--promote').trigger('click')
    await settle()
    expect(w.get('[role="alertdialog"]').text()).toContain('test')
    expect(http.post).not.toHaveBeenCalled()
    await w.get('.promo-confirm-actions .promo-btn--gate').trigger('click')
    await settle()
    expect(http.post).toHaveBeenCalledExactlyOnceWith('/api/runs/r1/promote',{targetEnvironment:'test'},scope())
    expect(w.emitted('promotion-pending')).toHaveLength(1)
    expect(w.find('.promo-confirm').exists()).toBe(false)
    expect(w.find('.promo-btn--gate').exists()).toBe(false)
  })

  it('cancels gated promotion and blocks its retained confirm button', async () => {
    exactAuthorization()
    vi.mocked(http.post).mockImplementation(async () => { pending = true; return record })
    const w = render()
    await settle()
    await w.get('.promo-btn--promote').trigger('click')
    await settle()
    const retained = w.get('.promo-confirm-actions .promo-btn--gate')
    await w.get('.promo-confirm-actions .promo-btn--cancel').trigger('click')
    await settle()
    expect(w.find('.promo-confirm').exists()).toBe(false)
    await retained.trigger('click')
    await settle()
    expect(http.post).not.toHaveBeenCalled()
    expect(w.emitted('promotion-pending')).toBeUndefined()
  })

  it('blocks the retained gated submit after independent environment promotion permission is withdrawn', async () => {
    exactAuthorization()
    const w = render()
    await settle()
    await w.get('.promo-btn--promote').trigger('click')
    await settle()
    const retained = w.get('.promo-confirm-actions .promo-btn--gate')
    promote = false
    usePermissionStore().invalidate()
    await settle()
    await retained.trigger('click')
    await settle()
    expect(w.find('.promotion-panel').exists()).toBe(true)
    expect(w.find('.promo-confirm-actions .promo-btn--gate').exists()).toBe(false)
    expect(http.post).not.toHaveBeenCalled()
  })

  it.each([
    ['approve','.promo-btn--gate'],['reject','.promo-btn--cancel'],
  ] as const)('uses exact environment approval with the actual %s button', async (decision, selector) => {
    pending = true
    promote = false
    exactAuthorization()
    vi.mocked(http.post).mockImplementation(async () => { pending = false; return { ok: true } })
    const w = render()
    await settle()
    await w.get('.promo-action-body .promo-btn-row ' + selector).trigger('click')
    await settle()
    expect(http.post).toHaveBeenCalledExactlyOnceWith('/api/runs/r1/' + decision, { stageId: 'promote:test' }, scope())
    expect(w.find('.promo-banner--pending').exists()).toBe(false)
  })

  it('rejects a retained denial button after independent approval permission is withdrawn', async () => {
    pending = true
    exactAuthorization()
    const w = render()
    await settle()
    const retained = w.get('.promo-btn-row .promo-btn--cancel')
    approve = false
    usePermissionStore().invalidate()
    await settle()
    await retained.trigger('click')
    await settle()
    expect(w.find('.promo-banner--pending').exists()).toBe(true)
    expect(w.find('.promo-btn-row .promo-btn--cancel').exists()).toBe(false)
    expect(http.post).not.toHaveBeenCalled()
  })
})
