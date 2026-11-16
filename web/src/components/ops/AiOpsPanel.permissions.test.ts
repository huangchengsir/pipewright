import { beforeEach, afterEach, describe, it, expect, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import AiOpsPanel from './AiOpsPanel.vue'
import AiOpsComposer from './AiOpsComposer.vue'
import AiOpsSessionList from './AiOpsSessionList.vue'
import { useSessionStore } from '../../stores/session'
import { usePermissionStore } from '../../stores/permissions'
import { useOpsChatStore } from '../../stores/opsChat'
import { access } from '../../api/access'
import { opsApi, createOpsApi } from '../../api/opsChat'
import { t } from '../../i18n'
import { resetOpsAuth } from '../../utils/opsAuth'
vi.mock('vue-router',()=>({useRouter:()=>({resolve:()=>({href:'/terminal'})}),onBeforeRouteLeave:vi.fn()}))
vi.mock('../../api/servers',()=>({listServers:vi.fn().mockResolvedValue([])}))
vi.mock('../../api/access',async original=>({...await original<typeof import('../../api/access')>(),access:{decisions:vi.fn()}}))
vi.mock('../../api/opsChat',async original=>{
 const actual=await original<typeof import('../../api/opsChat')>()
 const mocked=Object.fromEntries(Object.keys(actual.opsApi).map(key=>[key,vi.fn()]))
 return {...actual,opsApi:mocked,createOpsApi:vi.fn(()=>mocked),streamOps:vi.fn(()=>new Promise(()=>{}))}
})
const session={id:'chat',title:'Private',revision:1,draft:'',serverIds:[],activeRunId:'',watermark:0,createdAt:'2026-11-13T00:00:00Z',updatedAt:'2026-11-13T00:00:00Z'}
const snapshot={session,runs:[],calls:[],confirmations:[],entries:{entries:[],cursor:'',watermark:0,reset:false},watermark:0}
let wrapper:VueWrapper|undefined
let allowed:Set<string>
beforeEach(()=>{
 setActivePinia(createPinia());vi.clearAllMocks();resetOpsAuth();useOpsChatStore().clear()
 useSessionStore().user={username:'member',userId:'member',multiTenant:true}
 usePermissionStore().setTeam('team-a')
 allowed=new Set(['opschat.read'])
 vi.mocked(access.decisions).mockImplementation(async(team,queries)=>({userId:'member',teamId:team,modeEpoch:1,revision:1,catalogVersion:1,allowed:queries.map(q=>allowed.has(q.action))}))
 vi.mocked(opsApi.capabilities).mockResolvedValue({available:true,modelAvailable:true,provider:{provider:'test',model:'test',configHash:'hash'},limits:{}})
 vi.mocked(opsApi.tools).mockResolvedValue([])
 vi.mocked(opsApi.list).mockResolvedValue({sessions:[session],activeSessionId:'chat',cursor:''})
 vi.mocked(opsApi.snapshot).mockResolvedValue(snapshot)
 vi.mocked(opsApi.activate).mockResolvedValue({ok:true})
})
afterEach(()=>{wrapper?.unmount();wrapper=undefined;resetOpsAuth();vi.restoreAllMocks()})
async function open(){wrapper=mount(AiOpsPanel);await flushPromises();await flushPromises()}
describe('assistant shared action gates',()=>{
 it('preserves own readable history while every write action is independently unavailable',async()=>{
  await open()
  expect(wrapper!.text()).toContain('Private')
  expect(wrapper!.find(`button[aria-label="${t('opsChat.newSession')}"]`).exists()).toBe(false)
  const composer=wrapper!.getComponent(AiOpsComposer)
  expect(composer.props()).toMatchObject({canEdit:false,canSubmit:false,canCancel:false})
  composer.vm.$emit('send');composer.vm.$emit('tool','host_resources',{});composer.vm.$emit('save');composer.vm.$emit('resend')
  await flushPromises()
  for(const key of ['turn','patch','create','cancel','confirm'] as const)expect(opsApi[key]).not.toHaveBeenCalled()
  expect(vi.mocked(createOpsApi).mock.calls[0]?.[0]).toEqual({throwOnUnauthorized:true,headers:{'X-Pipewright-Team':'team-a'}})
 })
 it('changing the team clears private data and binds the replacement client',async()=>{
  await open()
  vi.mocked(opsApi.list).mockResolvedValue({sessions:[],activeSessionId:'',cursor:''})
  usePermissionStore().setTeam('team-b')
  expect(useOpsChatStore().state.snapshot).toBeNull()
  await flushPromises()
  expect(wrapper!.text()).not.toContain('Private')
  expect(vi.mocked(createOpsApi).mock.calls.at(-1)?.[0]).toEqual({throwOnUnauthorized:true,headers:{'X-Pipewright-Team':'team-b'}})
 })
 it('revoked edit/delete controls clear immediately after the shared decision refresh',async()=>{
  allowed.add('opschat.edit');allowed.add('opschat.delete')
  await open()
  await wrapper!.get(`button[aria-label="${t('opsChat.history')}"]`).trigger('click')
  expect(wrapper!.getComponent(AiOpsSessionList).props()).toMatchObject({canEdit:true,canDelete:true})
  allowed.delete('opschat.edit');allowed.delete('opschat.delete');usePermissionStore().invalidate()
  await flushPromises()
  expect(wrapper!.getComponent(AiOpsSessionList).props()).toMatchObject({canEdit:false,canDelete:false})
  wrapper!.getComponent(AiOpsSessionList).vm.$emit('rename','blocked')
  await flushPromises();expect(opsApi.patch).not.toHaveBeenCalled()
 })
 it('unknown mode, logout and forced password changes cannot load private conversation data',async()=>{
  useSessionStore().user={username:'member',userId:'member'}
  await open();expect(opsApi.capabilities).not.toHaveBeenCalled()
  useSessionStore().user={username:'member',userId:'member',multiTenant:true,mustChangePassword:true}
  await flushPromises();expect(opsApi.capabilities).not.toHaveBeenCalled()
  useSessionStore().user=null
  await flushPromises();expect(useOpsChatStore().state.snapshot).toBeNull()
 })
})

it('creates from the header only with create scope and discards late session after revocation', async()=>{
  allowed.add('opschat.create')
  await open()
  const button=wrapper!.get('button[aria-label="'+t('opsChat.newSession')+'"]')
  let finish!:(value: typeof session)=>void
  vi.mocked(opsApi.create).mockReturnValueOnce(new Promise(resolve=>{finish=resolve}))
  await button.trigger('click')
  await flushPromises()
  expect(opsApi.create).toHaveBeenCalledOnce()
  usePermissionStore().reject('team-a',{action:'opschat.create',resourceId:'new-resource',create:true})
  button.element.dispatchEvent(new MouseEvent('click',{bubbles:true}))
  finish({...session,id:'late',title:'Late creation'})
  await flushPromises()
  expect(opsApi.create).toHaveBeenCalledOnce()
  expect(wrapper!.text()).not.toContain('Late creation')
})
