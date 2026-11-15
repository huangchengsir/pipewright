import {afterEach,beforeEach,describe,expect,it,vi} from 'vitest'
import {mount,flushPromises,type VueWrapper} from '@vue/test-utils'
import {createPinia,setActivePinia} from 'pinia'
import {useSessionStore} from '../stores/session'
import {usePermissionStore} from '../stores/permissions'
import {access} from '../api/access'
import {getCustomNode,createCustomNode,updateCustomNode,type CustomNode} from '../api/customNodes'
import CustomNodeStudioPage from './CustomNodeStudioPage.vue'
const mocks=vi.hoisted(()=>({route:{params:{} as Record<string,string>},push:vi.fn(),success:vi.fn(),error:vi.fn()}))
vi.mock('vue-router',()=>({useRoute:()=>mocks.route,useRouter:()=>({push:mocks.push})}))
vi.mock('naive-ui',()=>({useMessage:()=>({success:mocks.success,error:mocks.error})}))
vi.mock('../api/access',()=>({access:{decisions:vi.fn()}}))
vi.mock('../api/customNodes',()=>({getCustomNode:vi.fn(),createCustomNode:vi.fn(),updateCustomNode:vi.fn()}))
const node:CustomNode={id:'n',name:'Existing',description:'',summary:'',nodeType:'templated',config:{image:'alpine',commandTemplate:'echo test'},createdAt:'',updatedAt:''}
let allow=true
const wrappers:VueWrapper[]=[]
async function settle(){for(let i=0;i<4;i++)await flushPromises()}
function render(){const w=mount(CustomNodeStudioPage);wrappers.push(w);return w}
beforeEach(()=>{vi.resetAllMocks();setActivePinia(createPinia());allow=true;mocks.route.params={id:'n'};useSessionStore().setUser({userId:'u',username:'u',multiTenant:true});usePermissionStore().setTeam('t');vi.mocked(access.decisions).mockImplementation(async(team,qs)=>({userId:'u',teamId:team,modeEpoch:1,revision:1,catalogVersion:1,allowed:qs.map(()=>allow)}));vi.mocked(getCustomNode).mockResolvedValue(node);vi.mocked(createCustomNode).mockResolvedValue(node);vi.mocked(updateCustomNode).mockResolvedValue(node)})
afterEach(()=>{wrappers.splice(0).forEach(w=>w.unmount());vi.restoreAllMocks()})
describe('custom node studio authorization',()=>{
 it('rejects direct editing without permission before loading data',async()=>{allow=false;const w=render();await settle();expect(w.find('.studio-page').exists()).toBe(false);expect(getCustomNode).not.toHaveBeenCalled()})
 it('requires create permission for the new-node route',async()=>{mocks.route.params={};allow=false;const w=render();await settle();expect(w.find('.studio-page').exists()).toBe(false);expect(createCustomNode).not.toHaveBeenCalled()})
 it('saves the permitted node with captured identity and team',async()=>{const w=render();await settle();await w.get('.node-name').setValue('Edited');await w.get('.bar .primary').trigger('click');await settle();expect(updateCustomNode).toHaveBeenCalledWith('n',expect.objectContaining({name:'Edited',nodeType:'templated'}),expect.objectContaining({headers:{'X-Pipewright-Team':'t'},signal:expect.any(AbortSignal),throwOnUnauthorized:true}));expect(mocks.push).toHaveBeenCalledWith({name:'library',query:{tab:'customNodes'}})})
 it('retains step and parameter editing for authorized employees',async()=>{const w=render();await settle();const before=w.findAll('.step').length;await w.get('.pal-btn').trigger('click');expect(w.findAll('.step').length).toBe(before+1);await w.get('.add-link').trigger('click');await w.get('.bar .primary').trigger('click');await settle();expect(updateCustomNode).toHaveBeenCalledOnce();expect(vi.mocked(updateCustomNode).mock.calls[0]![1].config.__studio).toContain('param1')})
 it('edits the authorized draft step order and parameters, then cancels without writing', async () => {
  const wrapper = render()
  await settle()
  const steps = () => wrapper.findAll('.step')
  const order = () => steps().map(step => step.get('.step-kind').text())
  const initial = steps().length
  const palette = wrapper.findAll('.pal-btn')
  await palette[0]!.trigger('click')
  await palette[1]!.trigger('click')
  expect(steps()).toHaveLength(initial + 2)
  const added = order().slice(initial)
  expect(added[0]).not.toBe(added[1])
  await steps()[initial]!.findAll('.step-tools button')[1]!.trigger('click')
  expect(order().slice(initial)).toEqual([...added].reverse())
  await steps()[initial + 1]!.findAll('.step-tools button')[0]!.trigger('click')
  expect(order().slice(initial)).toEqual(added)
  await steps()[initial + 1]!.findAll('.step-tools button')[2]!.trigger('click')
  expect(order().slice(initial)).toEqual(added.slice(0, 1))

  const tabs = wrapper.findAll('.tabs .tab')
  await tabs[1]!.trigger('click')
  expect(wrapper.find('.meta-field').exists()).toBe(true)
  await tabs[0]!.trigger('click')
  expect(wrapper.find('.meta-field').exists()).toBe(false)
  const params = wrapper.findAll('.param-row').length
  await wrapper.get('.add-link').trigger('click')
  expect(wrapper.findAll('.param-row')).toHaveLength(params + 1)
  await wrapper.findAll('.param-del')[params]!.trigger('click')
  expect(wrapper.findAll('.param-row')).toHaveLength(params)
  await wrapper.get('.bar button.btn:not(.primary)').trigger('click')
  expect(mocks.push).toHaveBeenCalledExactlyOnceWith({ name: 'library', query: { tab: 'customNodes' } })
  expect(updateCustomNode).not.toHaveBeenCalled()
  expect(createCustomNode).not.toHaveBeenCalled()
 })
 it('removes the editor immediately after access is revoked',async()=>{const w=render();await settle();allow=false;usePermissionStore().invalidate();await settle();expect(w.find('.studio-page').exists()).toBe(false);expect(updateCustomNode).not.toHaveBeenCalled()})
 it('does not expose a late fetch after logging out',async()=>{let resolve!:(value:CustomNode)=>void;vi.mocked(getCustomNode).mockReturnValue(new Promise(r=>{resolve=r}));const w=render();await settle();const options=vi.mocked(getCustomNode).mock.calls[0]![1]!;useSessionStore().clearSession();resolve(node);await settle();expect(options.signal?.aborted).toBe(true);expect(w.text()).not.toContain('Existing')})
 it('ignores a late save result after switching teams',async()=>{let resolve!:(value:CustomNode)=>void;vi.mocked(updateCustomNode).mockReturnValue(new Promise(r=>{resolve=r}));const w=render();await settle();await w.get('.bar .primary').trigger('click');await settle();const options=vi.mocked(updateCustomNode).mock.calls[0]![2]!;usePermissionStore().setTeam('other');await settle();resolve(node);await settle();expect(options.signal?.aborted).toBe(true);expect(mocks.success).not.toHaveBeenCalled();expect(mocks.push).not.toHaveBeenCalled()})
})
