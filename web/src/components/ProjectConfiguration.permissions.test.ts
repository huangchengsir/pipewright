import {createPinia,setActivePinia} from 'pinia'
import {mount,flushPromises,type VueWrapper} from '@vue/test-utils'
import {beforeEach,afterEach,describe,it,expect,vi} from 'vitest'
import {access} from '../api/access'
import {http} from '../api/http'
import {useSessionStore} from '../stores/session'
import {usePermissionStore} from '../stores/permissions'
import ConcurrencyPanel from './ConcurrencyPanel.vue'
import ParametersPanel from './ParametersPanel.vue'
import RunnerPanel from './RunnerPanel.vue'
import EnvironmentsPanel from './EnvironmentsPanel.vue'
vi.mock('../api/access',()=>({access:{decisions:vi.fn()}}))
vi.mock('../api/http',async original=>({...await original<typeof import('../api/http')>(),http:{get:vi.fn(),put:vi.fn()}}))
const cases=[{name:'concurrency',component:ConcurrencyPanel,save:'.btn-primary',payload:{maxConcurrent:2}},
{name:'parameters',component:ParametersPanel,save:'.btn-primary',payload:{parameters:[{key:'COUNT',label:'Count',type:'number',default:'2',required:false}]}},
{name:'runner',component:RunnerPanel,save:'.btn-primary',payload:{runnerServerId:''}},
{name:'environments',component:EnvironmentsPanel,save:'.ep-btn-primary',payload:{environments:[{name:'dev',gated:false}],variables:{dev:[{key:'MODE',value:'private-value',secret:false,credentialId:''}]}}}]
const wrappers:VueWrapper[]=[]
let read=true,edit=true,serverUse=true
async function settle(){for(let i=0;i<4;i++)await flushPromises()}
function render(c:typeof cases[number]){const w=mount(c.component,{props:{projectId:'p1'},attachTo:document.body});wrappers.push(w);return w}
beforeEach(()=>{
 vi.resetAllMocks();setActivePinia(createPinia());read=true;edit=true;serverUse=true
 useSessionStore().setUser({username:'member',userId:'u1',multiTenant:true});usePermissionStore().setTeam('t1')
 vi.mocked(access.decisions).mockImplementation(async(team,queries)=>({userId:useSessionStore().user!.userId!,teamId:team,modeEpoch:1,revision:1,catalogVersion:1,allowed:queries.map(q=>q.action==='pipeline.edit'?edit:q.action==='server.use'?serverUse:read)}))
 vi.mocked(http.get).mockImplementation(async url=>url==='/api/servers'?{items:[{id:'s1',name:'Build node',host:'local.invalid'}]}:structuredClone(cases.find(c=>url.endsWith('/'+c.name))!.payload))
 vi.mocked(http.put).mockImplementation(async(_url,body)=>body)
})
afterEach(()=>{for(const w of wrappers.splice(0))w.unmount();vi.restoreAllMocks()})
describe('all project configuration cards',()=>{
 it.each(cases)('$name: no permission means no configuration request',async c=>{read=false;const w=render(c);await settle();expect(http.get).not.toHaveBeenCalled();expect(w.find('section').exists()).toBe(false)})
 it.each(cases)('$name: readers cannot edit or save',async c=>{
  edit=false;const w=render(c);await settle()
  if(c.name==='environments'){await w.get('.vars-btn').trigger('click');await settle();expect(w.text()).not.toContain('one-time-secret')}
  for(const control of w.findAll('input,select,textarea'))expect(control.attributes('disabled')).toBeDefined()
  for(const control of w.findAll('button').filter(button=>!button.classes().includes('vars-btn')))expect(control.attributes('disabled')).toBeDefined()
  expect(w.find(c.save).exists()).toBe(false);expect(http.put).not.toHaveBeenCalled()
 })
 it.each(cases)('$name: editors save through fixed-team requests',async c=>{
  const w=render(c);await settle()
  if(c.name==='concurrency')await w.get('.stepper-btn--inc').trigger('click')
  if(c.name==='parameters')await w.get('.def-input').setValue('SIZE')
  if(c.name==='runner'){await w.get('#runner-select').setValue('s1');await settle()}
  if(c.name==='environments')await w.get('.chain-input').setValue('staging')
  await w.get(c.save).trigger('click');await settle()
  expect(http.put).toHaveBeenCalledOnce()
  const [url,body,options]=vi.mocked(http.put).mock.calls[0]!
  expect(new Headers(options?.headers).get('X-Pipewright-Team')).toBe('t1');expect(url).toBe('/api/projects/p1/'+c.name);expect(options).toEqual(expect.objectContaining({signal:expect.any(AbortSignal),throwOnUnauthorized:true}))
  if(c.name==='concurrency')expect(body).toEqual({maxConcurrent:3})
  if(c.name==='runner')expect(body).toEqual({runnerServerId:'s1'})
  if(c.name==='parameters')expect(body).toEqual(expect.objectContaining({parameters:[expect.objectContaining({key:'SIZE'})]}))
  if(c.name==='environments')expect(body).toEqual(expect.objectContaining({environments:[{name:'staging',gated:false}]}))
 })
 it.each(cases)('$name: identity changes discard loaded data and stop requests',async c=>{
  const w=render(c);await settle();const options=vi.mocked(http.get).mock.calls[0]![1]!
  useSessionStore().clearSession();await settle();expect(options.signal?.aborted).toBe(true);expect(w.find('section').exists()).toBe(false);expect(http.put).not.toHaveBeenCalled()
 })
 it('requires server.use separately from pipeline editing and permits choosing local execution',async()=>{
  serverUse=false;const w=render(cases[2]!);await settle();await w.get('#runner-select').setValue('s1');await settle()
  expect(w.get('.btn-primary').attributes('disabled')).toBeDefined();await w.get('.btn-primary').trigger('click');expect(http.put).not.toHaveBeenCalled()
  await w.get('#runner-select').setValue('');await settle();expect(w.get('.btn-primary').attributes('disabled')).toBeUndefined();await w.get('.btn-primary').trigger('click');await settle();expect(http.put).toHaveBeenCalledOnce()
 })
 it('lets a permitted editor add and remove parameter rows',async()=>{
  const w=render(cases[1]!);await settle();await w.get('.link-add').trigger('click');expect(w.findAll('.param-def')).toHaveLength(2);await w.findAll('.def-del')[1]!.trigger('click');expect(w.findAll('.param-def')).toHaveLength(1)
 })
 it('retries an environment read only for the selected team and project', async () => {
  vi.mocked(http.get).mockRejectedValueOnce(new Error('temporary fixture failure'))
  const wrapper = render(cases[3]!)
  await settle()
  await wrapper.get('.ep-banner-retry').trigger('click')
  await settle()
  expect(vi.mocked(http.get).mock.calls.map(([url]) => url)).toEqual([
   '/api/projects/p1/environments', '/api/projects/p1/environments',
  ])
  const [, options] = vi.mocked(http.get).mock.calls[1]!
  expect(new Headers(options?.headers).get('X-Pipewright-Team')).toBe('t1')
  expect(wrapper.find('.ep-banner-retry').exists()).toBe(false)
  expect(http.put).not.toHaveBeenCalled()
 })

 it('moves and removes only the selected environment without writing the draft', async () => {
  vi.mocked(http.get).mockResolvedValue({
   environments: [{ name: 'dev', gated: false }, { name: 'prod', gated: true }],
   variables: {},
  })
  const wrapper = render(cases[3]!)
  await settle()
  const rows = () => wrapper.findAll('.chain-row')
  const order = () => wrapper.findAll<HTMLInputElement>('.chain-input').map(input => input.element.value)
  await rows()[1]!.findAll('.order-btn')[0]!.trigger('click')
  expect(order()).toEqual(['prod', 'dev'])
  await rows()[0]!.findAll('.order-btn')[1]!.trigger('click')
  expect(order()).toEqual(['dev', 'prod'])
  await rows()[1]!.get('.del-btn').trigger('click')
  expect(order()).toEqual(['dev'])
  expect(http.put).not.toHaveBeenCalled()
 })

 it('lets a permitted editor adjust environment ordering, gate and plain variables',async()=>{
  const w=render(cases[3]!);await settle();await w.get('.gate-toggle').trigger('click');expect(w.get('.gate-toggle').attributes('aria-checked')).toBe('true')
  await w.get('.vars-btn').trigger('click');await w.get('.var-add-btn').trigger('click');expect(w.findAll('.var-del-btn')).toHaveLength(2);await w.findAll('.var-del-btn')[1]!.trigger('click');expect(w.findAll('.var-del-btn')).toHaveLength(1)
  await w.get('.add-env-btn').trigger('click');expect(w.findAll('.chain-row')).toHaveLength(2);await w.findAll('.chain-input')[1]!.setValue('prod');await w.findAll('.order-btn')[1]!.trigger('click');expect((w.findAll('.chain-input')[0]!.element as HTMLInputElement).value).toBe('prod')
 })
 it('sends an explicit empty variable list when the last variable is removed',async()=>{
  const w=render(cases[3]!);await settle();await w.get('.vars-btn').trigger('click');await w.get('.var-del-btn').trigger('click')
  await w.get('.ep-btn-primary').trigger('click');await settle()
  expect(http.put).toHaveBeenCalledOnce();expect(vi.mocked(http.put).mock.calls[0]![1]).toEqual({environments:[{name:'dev',gated:false}],variables:{dev:[]}})
 })
 it('preserves edited variables after collapsing and reopening the editor',async()=>{
  const w=render(cases[3]!);await settle();await w.get('.vars-btn').trigger('click');await w.get('.var-input--val').setValue('edited-value')
  await w.get('.vars-btn').trigger('click');await w.get('.vars-btn').trigger('click')
  expect((w.get('.var-input--val').element as HTMLInputElement).value).toBe('edited-value')
  await w.get('.ep-btn-primary').trigger('click');await settle()
  expect(vi.mocked(http.put).mock.calls[0]![1]).toEqual(expect.objectContaining({variables:{dev:[expect.objectContaining({key:'MODE',value:'edited-value'})]}}))
 })
 it('preserves each environment draft when changing the expanded environment',async()=>{
  vi.mocked(http.get).mockResolvedValue({environments:[{name:'dev',gated:false},{name:'prod',gated:true}],variables:{dev:[{key:'MODE',value:'dev-value',secret:false}],prod:[{key:'MODE',value:'prod-value',secret:false}]}})
  const w=render(cases[3]!);await settle();await w.findAll('.vars-btn')[0]!.trigger('click');await w.get('.var-input--val').setValue('new-dev')
  await w.findAll('.vars-btn')[1]!.trigger('click');await w.get('.var-input--val').setValue('new-prod')
  await w.get('.ep-btn-primary').trigger('click');await settle()
  expect(vi.mocked(http.put).mock.calls[0]![1]).toEqual(expect.objectContaining({variables:{dev:[expect.objectContaining({value:'new-dev'})],prod:[expect.objectContaining({value:'new-prod'})]}}))
 })

})
