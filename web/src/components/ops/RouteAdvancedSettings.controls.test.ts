import {beforeEach,afterEach,it,expect,vi} from 'vitest'
import {mount,flushPromises,type VueWrapper} from '@vue/test-utils'
import {createPinia,setActivePinia} from 'pinia'
import Advanced from './RouteAdvancedSettings.vue'
import {useSessionStore} from '../../stores/session'
import {usePermissionStore} from '../../stores/permissions'
import {access} from '../../api/access'
import {i18n} from '../../i18n'
import {updateProxyRoute,type ProxyRoute} from '../../api/reverseProxy'
import {listUsableDnsProviders} from '../../api/dnsProviders'

vi.mock('../../api/access',()=>({access:{decisions:vi.fn()}}))
vi.mock('../../api/dnsProviders',()=>({listUsableDnsProviders:vi.fn()}))
vi.mock('../../api/reverseProxy',()=>({updateProxyRoute:vi.fn()}))
vi.mock('../../composables/useToast',()=>({useToast:()=>({success:vi.fn(),error:vi.fn()})}))

const route={id:'fixture-r',serverId:'fixture-host',domain:'site.example.invalid',upstreamContainer:'web',upstreamPort:8080,tlsMode:'auto',enabled:true,certStatus:'issued',certDetail:'',config:{aliases:[],forceHttps:true,hsts:false,securityHeaders:false,compression:false,basicAuthUser:'',basicAuthEnabled:false,ipAllow:[],ipDeny:[],redirects:[]},createdAt:'',updatedAt:''} as ProxyRoute
let w:VueWrapper|undefined
beforeEach(()=>{
  vi.resetAllMocks()
  setActivePinia(createPinia())
  useSessionStore().setUser({username:'member',userId:'u',multiTenant:true})
  usePermissionStore().setTeam('a')
  vi.mocked(access.decisions).mockImplementation(async(team,q)=>({teamId:team,userId:'u',modeEpoch:1,revision:1,catalogVersion:1,allowed:q.map(p=>team==='a'&&p.action==='proxy.manage'&&p.resourceId==='fixture-host')}))
  vi.mocked(listUsableDnsProviders).mockResolvedValue([])
})
afterEach(()=>{w?.unmount();w=undefined})
async function open(){
  w=mount(Advanced,{props:{route}})
  await flushPromises()
  await w.get('.adv__toggle').trigger('click')
  await flushPromises()
  return w
}
it('opens and closes the selected advanced draft without writing',async()=>{
  const view=await open()
  expect(view.find('.adv__body').exists()).toBe(true)
  await view.get('.adv__toggle').trigger('click')
  expect(view.find('.adv__body').exists()).toBe(false)
  expect(updateProxyRoute).not.toHaveBeenCalled()
})
it.each([
  ['alias',0,'alias.example.invalid'],
  ['allow',1,'192.0.2.0/24'],
  ['deny',2,'198.51.100.0/24'],
] as const)('adds and removes only the %s entry in the local draft',async(_name,index,value)=>{
  const view=await open()
  const inputs=view.findAll('.taginput')
  await inputs[index]!.get('input').setValue(value)
  await inputs[index]!.get('.advadd').trigger('click')
  expect(view.findAll('.chip')).toHaveLength(1)
  expect(view.get('.chip').text()).toContain(value)
  await view.get('.chip__x').trigger('click')
  expect(view.find('.chip').exists()).toBe(false)
  expect(updateProxyRoute).not.toHaveBeenCalled()
})
it.each(['Redirect','PathRule','Upstream'] as const)('adds and removes only the %s row in the local draft',async(kind)=>{
  const view=await open()
  const label=(prefix:string)=>i18n.global.t('reverseProxy.adv.'+prefix+kind)
  const add=view.findAll('.advghost').find(b=>b.text().includes(label('add')))!
  expect(add).toBeDefined()
  await add.trigger('click')
  const remove=view.findAll('button').find(b=>b.attributes('aria-label')===label('remove'))!
  expect(remove).toBeDefined()
  await remove.trigger('click')
  expect(view.findAll('button').some(b=>b.attributes('aria-label')===label('remove'))).toBe(false)
  expect(updateProxyRoute).not.toHaveBeenCalled()
})
