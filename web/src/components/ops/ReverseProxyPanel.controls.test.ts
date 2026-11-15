import {afterEach,beforeEach,describe,expect,it,vi} from 'vitest'
import {flushPromises,mount,type VueWrapper} from '@vue/test-utils'
import {createPinia,setActivePinia} from 'pinia'
import {defineComponent,h} from 'vue'
import ReverseProxyPanel from './ReverseProxyPanel.vue'
import ConfirmDialog from '../ui/ConfirmDialog.vue'
import {useConfirm} from '../../composables/useConfirm'
import {useSessionStore} from '../../stores/session'
import {usePermissionStore} from '../../stores/permissions'
import type {PermissionQuery} from '../../api/access'
import {setLocale} from '../../i18n'

const api=vi.hoisted(()=>({
  list:vi.fn(),status:vi.fn(),create:vi.fn(),toggle:vi.fn(),refresh:vi.fn(),delete:vi.fn(),
  install:vi.fn(),remove:vi.fn(),decisions:vi.fn(),success:vi.fn(),error:vi.fn(),
}))
vi.mock('../../api/reverseProxy',()=>({
  listProxyRoutes:api.list,getCaddyStatus:api.status,createProxyRoute:api.create,
  setProxyRouteEnabled:api.toggle,refreshProxyRoute:api.refresh,deleteProxyRoute:api.delete,
  prepareCaddyEnv:api.install,removeCaddyEnv:api.remove,
}))
vi.mock('../../api/access',()=>({access:{decisions:api.decisions}}))
vi.mock('../../composables/useToast',()=>({useToast:()=>({success:api.success,error:api.error})}))

const server='selected-server'
const team='selected-team'
const routeA={
  id:'route-a',serverId:server,domain:'alpha.example.test',upstreamContainer:'web',upstreamPort:80,
  tlsMode:'auto',enabled:true,certStatus:'failed',certDetail:'',config:{aliases:[],upstreamKind:'container'},createdAt:'',updatedAt:'',
}
const routeB={...routeA,id:'route-b',domain:'beta.example.test'}
const status={serverId:server,installed:true,running:true,routeCount:2}
const draft={serverId:server,domain:'new.example.test',upstreamContainer:'upstream.example.test',upstreamPort:8080,config:{upstreamKind:'address'}}
const rowA='.rp__routes .route:nth-child(1)'
const rowB='.rp__routes .route:nth-child(2)'
let wrapper:VueWrapper
let grants:Set<string>

function key(action:string,resource=server){return action+'@'+resource}
function fixedTeam(value=team){return expect.objectContaining({
  headers:{'X-Pipewright-Team':value},signal:expect.any(AbortSignal),throwOnUnauthorized:true,
})}
function writes(){return [api.install,api.remove,api.create,api.toggle,api.delete,api.refresh]}
async function settle(){for(let i=0;i<4;i++)await flushPromises()}
async function open(otherServer=server){
  wrapper=mount(defineComponent({setup:()=>()=>[h(ReverseProxyPanel,{serverId:otherServer,host:'192.0.2.1',containers:[]}),h(ConfirmDialog)]}),{
    attachTo:document.body,
    global:{stubs:{Teleport:true,RouteAdvancedSettings:true,InstantSubdomain:true}},
  })
  await settle()
}
async function fill(){
  await wrapper.findAll('.rp__seg-btn')[1]!.trigger('click')
  const inputs=wrapper.findAll('.rp__form input')
  await inputs[0]!.setValue(' New.Example.Test ')
  await inputs[1]!.setValue('upstream.example.test')
  await inputs[2]!.setValue('8080')
}
function revoke(action:string,resource=server){
  grants.delete(key(action,resource))
  usePermissionStore().invalidate()
}
function nativeSubmit(){wrapper.get<HTMLButtonElement>('.rp__submit').element.click()}

beforeEach(()=>{
  vi.resetAllMocks()
  const pinia=createPinia();setActivePinia(pinia)
  useSessionStore().setUser({username:'employee',userId:'u',multiTenant:true})
  usePermissionStore().setTeam(team)
  setLocale('zh-CN')
  grants=new Set([key('proxy.read'),key('server.use'),key('proxy.manage'),key('proxy.install')])
  api.decisions.mockImplementation(async (selected:string,queries:PermissionQuery[])=>({
    userId:'u',teamId:selected,modeEpoch:1,revision:1,catalogVersion:1,
    allowed:queries.map(query=>grants.has(key(query.action,query.resourceId))),
  }))
  api.list.mockResolvedValue([routeA,routeB])
  api.status.mockResolvedValue(status)
  api.install.mockResolvedValue(status)
  api.remove.mockResolvedValue({ok:true})
  api.create.mockResolvedValue({...routeA,...draft,id:'new',certStatus:'pending',config:{aliases:[],upstreamKind:'address'}})
  api.toggle.mockImplementation(async (id:string,enabled:boolean)=>({...routeB,id,enabled}))
  api.delete.mockResolvedValue({ok:true})
  api.refresh.mockResolvedValue({...routeB,certStatus:'issued',certDetail:'Synthetic certificate'})
})
afterEach(()=>{
  useConfirm()._resolve(false)
  wrapper?.unmount()
})

const consentActions=[
  {label:'install',selector:'.caddy__deploy',grant:'proxy.install',present:false,api:'install'},
  {label:'clean up',selector:'.caddy__remove',grant:'proxy.install',present:true,api:'remove'},
  {label:'delete selected route',selector:rowB+' .route__del',grant:'proxy.manage',present:true,api:'delete'},
  {label:'first route',selector:'.rp__submit',grant:'proxy.manage',present:false,api:'create'},
] as const
async function choose(spec:typeof consentActions[number]){
  api.status.mockResolvedValue({...status,installed:spec.present,running:spec.present})
  await open()
  if(spec.api==='create')await fill()
  const old=wrapper.get(spec.selector)
  if(spec.api==='create')nativeSubmit()
  else await old.trigger('click')
  await settle()
  expect(wrapper.find('.cfm-dialog').exists()).toBe(true)
  for(const call of writes())expect(call).not.toHaveBeenCalled()
  return old
}

describe('reverse proxy real confirmation buttons',()=>{
  it.each(consentActions)('dispatches $label only after selecting confirm',async spec=>{
    await choose(spec)
    await wrapper.get('.cfm-dialog__confirm').trigger('click')
    await settle()

    const target=spec.api==='delete'?routeB.id:spec.api==='create'?draft:server
    expect(api[spec.api]).toHaveBeenCalledExactlyOnceWith(target,fixedTeam())
    for(const call of writes().filter(call=>call!==api[spec.api]))expect(call).not.toHaveBeenCalled()
    expect(wrapper.find('.cfm-dialog').exists()).toBe(false)
    if(spec.api==='delete'){
      expect(wrapper.text()).toContain(routeA.domain)
      expect(wrapper.text()).not.toContain(routeB.domain)
    }
  })
  it.each(consentActions)('cancels $label with the actual cancel button and no request',async spec=>{
    await choose(spec)
    await wrapper.get('.cfm-dialog__cancel').trigger('click')
    await settle()
    expect(wrapper.find('.cfm-dialog').exists()).toBe(false)
    for(const call of writes())expect(call).not.toHaveBeenCalled()
  })
  it.each(consentActions)('blocks $label after the independent action is revoked while confirmation is open',async spec=>{
    const old=await choose(spec)
    revoke(spec.grant)
    await settle()
    expect(wrapper.find(spec.selector).exists()).toBe(false)
    await wrapper.get('.cfm-dialog__confirm').trigger('click')
    await old.trigger('click')
    await settle()
    expect(wrapper.find('.cfm-dialog').exists()).toBe(false)
    for(const call of writes())expect(call).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain(routeA.domain)
  })
})

it('does not transfer first-route consent to a modified domain and upstream draft',async()=>{
  await choose(consentActions[3])
  await wrapper.findAll('.rp__form input')[0]!.setValue('changed.example.test')
  await wrapper.findAll('.rp__form input')[1]!.setValue('changed-upstream.example.test')
  await wrapper.get('.cfm-dialog__confirm').trigger('click')
  await settle()
  expect(api.create).not.toHaveBeenCalled()
  expect(wrapper.get<HTMLInputElement>('.rp__form input').element.value).toBe('changed.example.test')
  nativeSubmit()
  await settle()
  expect(wrapper.find('.cfm-dialog').exists()).toBe(true)
  await wrapper.get('.cfm-dialog__confirm').trigger('click')
  await settle()
  expect(api.create).toHaveBeenCalledExactlyOnceWith({
    ...draft,domain:'changed.example.test',upstreamContainer:'changed-upstream.example.test',
  },fixedTeam())
})

it('binds the container draft directly when the proxy is installed and rejects an unscoped native form event',async()=>{
  await open()
  await fill()
  await wrapper.findAll('.rp__seg-btn')[0]!.trigger('click')
  await wrapper.findAll('.rp__form input')[1]!.setValue('frontend-web')
  nativeSubmit()
  await settle()
  expect(wrapper.find('.cfm-dialog').exists()).toBe(false)
  expect(api.create).toHaveBeenCalledExactlyOnceWith({
    ...draft,upstreamContainer:'frontend-web',config:{upstreamKind:'container'},
  },fixedTeam())
  await fill()
  revoke('server.use')
  await settle()
  expect(wrapper.find('.rp__submit').exists()).toBe(false)
  await wrapper.get('.rp__form').trigger('submit')
  await settle()
  expect(api.create).toHaveBeenCalledTimes(1)
})

it('toggles only the exact row using fixed team and blocks its retained button after server use is removed',async()=>{
  await open()
  const old=wrapper.get(rowB+' .route__toggle')
  await old.trigger('click');await settle()
  expect(api.toggle).toHaveBeenCalledExactlyOnceWith(routeB.id,false,fixedTeam())
  expect(wrapper.get(rowB).classes()).toContain('route--off')
  expect(wrapper.get(rowA).classes()).not.toContain('route--off')
  revoke('server.use');await settle()
  await old.trigger('click');await settle()
  expect(api.toggle).toHaveBeenCalledTimes(1)
  expect(wrapper.find(rowB+' .route__toggle').exists()).toBe(false)
  expect(api.delete).not.toHaveBeenCalled()
})

it('refreshes only the independently authorized route and blocks its retained button when revoked',async()=>{
  grants.delete(key('proxy.manage'));grants.delete(key('proxy.install'))
  grants.add(key('certificate.refresh',routeB.id))
  await open()
  expect(wrapper.find(rowA+' .route__retry').exists()).toBe(false)
  const old=wrapper.get(rowB+' .route__retry')
  await old.trigger('click');await settle()
  expect(api.refresh).toHaveBeenCalledExactlyOnceWith(routeB.id,fixedTeam())
  expect(wrapper.get(rowB).text()).toContain('Synthetic certificate')
  expect(wrapper.find('.route__toggle').exists()).toBe(false)
  revoke('certificate.refresh',routeB.id);await settle()
  await old.trigger('click');await settle()
  expect(api.refresh).toHaveBeenCalledTimes(1)
  expect(api.toggle).not.toHaveBeenCalled()
})

it('cancels an old team delete confirmation even while the same resource is authorized in the new team',async()=>{
  const old=await choose(consentActions[2])
  usePermissionStore().setTeam('other-team')
  await settle()
  expect(wrapper.find(rowB+' .route__del').exists()).toBe(true)
  await wrapper.get('.cfm-dialog__confirm').trigger('click')
  await old.trigger('click');await settle()
  expect(api.delete).not.toHaveBeenCalled()
  expect(api.list).toHaveBeenLastCalledWith(server,fixedTeam('other-team'))
  await wrapper.get(rowB+' .route__del').trigger('click')
  await wrapper.get('.cfm-dialog__confirm').trigger('click')
  await settle()
  expect(api.delete).toHaveBeenCalledExactlyOnceWith(routeB.id,fixedTeam('other-team'))
})


it('does not inherit host mutation permissions when opening another readable server',async()=>{
  const other='read-only-server'
  grants.add(key('proxy.read',other))
  api.list.mockResolvedValue([])
  await open(other)
  expect(api.list).toHaveBeenCalledExactlyOnceWith(other,fixedTeam())
  expect(api.status).not.toHaveBeenCalled()
  expect(wrapper.find('.caddy__deploy').exists()).toBe(false)
  expect(wrapper.find('.caddy__remove').exists()).toBe(false)
  await fill()
  expect(wrapper.find('.rp__submit').exists()).toBe(false)
  await wrapper.get('.rp__form').trigger('submit');await settle()
  for(const call of writes())expect(call).not.toHaveBeenCalled()
})
