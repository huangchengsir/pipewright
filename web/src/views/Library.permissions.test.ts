import {afterEach,beforeEach,describe,expect,it,vi} from 'vitest'
import {mount,flushPromises} from '@vue/test-utils'
import {createPinia,setActivePinia} from 'pinia'
import {useSessionStore} from '../stores/session'
import {usePermissionStore} from '../stores/permissions'
import {access} from '../api/access'
import {listTemplates,deleteTemplate} from '../api/templates'
import {listVariableGroups,updateVariableGroup,createVariableGroup,deleteVariableGroup} from '../api/variableGroups'
import {listCustomNodes,updateCustomNode,deleteCustomNode} from '../api/customNodes'
import {listCredentials} from '../api/credentials'
import Library from './Library.vue'
const routePush = vi.hoisted(() => vi.fn())
vi.mock('vue-router',()=>({useRoute:()=>({query:{}}),useRouter:()=>({push:routePush})}))
vi.mock('naive-ui',()=>({useMessage:()=>({success:vi.fn(),error:vi.fn()})}))
vi.mock('../api/access',()=>({access:{decisions:vi.fn()}}))
vi.mock('../api/templates',()=>({listTemplates:vi.fn(),deleteTemplate:vi.fn()}))
vi.mock('../api/variableGroups',()=>({listVariableGroups:vi.fn(),createVariableGroup:vi.fn(),updateVariableGroup:vi.fn(),deleteVariableGroup:vi.fn()}))
vi.mock('../api/customNodes',()=>({listCustomNodes:vi.fn(),updateCustomNode:vi.fn(),deleteCustomNode:vi.fn()}))
vi.mock('../api/credentials',()=>({listCredentials:vi.fn()}))
let wrappers:ReturnType<typeof mount>[]=[]
let allowed=new Set<string>()
function render(){const pinia=createPinia();setActivePinia(pinia);useSessionStore().setUser({userId:'u',username:'u',multiTenant:true});usePermissionStore().setTeam('team');const w=mount(Library,{global:{plugins:[pinia]}});wrappers.push(w);return w}
beforeEach(()=>{vi.resetAllMocks();allowed=new Set(['template.read:a','template.read:b','variable_group.read:g','custom_node.read:n']);vi.mocked(access.decisions).mockImplementation(async(team,queries)=>({userId:'u',teamId:team,modeEpoch:1,revision:1,catalogVersion:1,allowed:queries.map(q=>allowed.has(q.action+':'+(q.resourceId??'new')))}));vi.mocked(listTemplates).mockResolvedValue(['a','b'].map(id=>({id,name:id,description:'',stageCount:1,createdAt:'2026-01-01',updatedAt:'2026-01-01'})));vi.mocked(listVariableGroups).mockResolvedValue([{id:'g',name:'Group',description:'',vars:[],createdAt:'2026-01-01',updatedAt:'2026-01-01'}]);vi.mocked(listCustomNodes).mockResolvedValue([]);vi.mocked(listCredentials).mockResolvedValue([])})
afterEach(()=>{wrappers.forEach(w=>w.unmount());wrappers=[];vi.restoreAllMocks()})
describe('Library permission controls',()=>{
 it('shows only the allowed row action and sends that exact ID',async()=>{allowed.add('template.delete:a');const confirm=vi.spyOn(window,'confirm').mockReturnValue(true);const w=render();await flushPromises();const cards=w.findAll('.tpl-card');expect(cards[0]!.find('.link-danger').exists()).toBe(true);expect(cards[1]!.find('.link-danger').exists()).toBe(false);await cards[0]!.get('.link-danger').trigger('click');await flushPromises();expect(confirm).toHaveBeenCalledTimes(1);expect(deleteTemplate).toHaveBeenCalledExactlyOnceWith('a',expect.objectContaining({headers:{'X-Pipewright-Team':'team'},signal:expect.any(AbortSignal),throwOnUnauthorized:true}))})
 it('revokes save on an already opened editor and performs no mutation',async()=>{allowed.add('variable_group.edit:g');const w=render();await flushPromises();await w.get('.vg-card .btn-secondary').trigger('click');await flushPromises();expect(w.find('.modal .btn-primary').exists()).toBe(true);allowed.clear();usePermissionStore().invalidate();await flushPromises();expect(w.find('.modal .btn-primary').exists()).toBe(false);expect(updateVariableGroup).not.toHaveBeenCalled();expect(createVariableGroup).not.toHaveBeenCalled()})
 it('does not expose rows or counts after read access is revoked',async()=>{
  const w=render();await flushPromises();expect(w.findAll('.tpl-card')).toHaveLength(2)
  allowed.delete('template.read:a');usePermissionStore().invalidate();await flushPromises();expect(w.findAll('.tpl-card')).toHaveLength(1);expect(w.get('.tpl-card .tpl-name').text()).toBe('b')
 })
 it('rechecks authorization after deletion confirmation',async()=>{
  allowed.add('template.delete:a');vi.spyOn(window,'confirm').mockImplementation(()=>{allowed.clear();usePermissionStore().invalidate();return true})
  const w=render();await flushPromises();await w.get('.tpl-card .link-danger').trigger('click');await flushPromises();expect(deleteTemplate).not.toHaveBeenCalled()
 })
 it('aborts old-team requests and discards their late list contents',async()=>{
  let resolve!:(rows:any[])=>void;vi.mocked(listTemplates).mockReturnValueOnce(new Promise(r=>{resolve=r}));const w=render();await flushPromises()
  const options=vi.mocked(listTemplates).mock.calls[0]![0]!;usePermissionStore().setTeam('another');await flushPromises()
  resolve([{id:'private-old',name:'private-old-team',description:'',stageCount:1}]);await flushPromises()
  expect(options.signal?.aborted).toBe(true);expect(w.text()).not.toContain('private-old-team')
 })
 it('clears an open group editor and aborts requests after logout',async()=>{
  allowed.add('variable_group.edit:g');const w=render();await flushPromises();await w.get('.vg-card .btn-secondary').trigger('click');await flushPromises()
  expect(w.find('.modal').exists()).toBe(true);const options=vi.mocked(listVariableGroups).mock.calls[0]![0]!
  useSessionStore().clearSession();await flushPromises();expect(w.find('.modal').exists()).toBe(false);expect(w.text()).not.toContain('Group');expect(options.signal?.aborted).toBe(true)
 })
 it('creates a group with fixed-team requests and no plaintext for secret variables',async()=>{
  allowed.add('variable_group.create:');allowed.add('credential.use:c');vi.mocked(listCredentials).mockResolvedValue([{id:'c',name:'Credential',type:'git_token',scope:'',username:'',maskedValue:'***',lastUsedAt:null,createdAt:''}]);const w=render();await flushPromises()
  await w.findAll('.seg-item')[1]!.trigger('click');await w.get('.page-header .btn-primary').trigger('click');await flushPromises()
  await w.get('#vg-name').setValue('New group');await w.get('.input--key').setValue('TOKEN');await w.get('.var-secret-toggle').trigger('click');await flushPromises()
  await w.get('.input--cred').setValue('c');await flushPromises()
  await w.get('.modal .btn-primary').trigger('click');await flushPromises()
  expect(createVariableGroup).toHaveBeenCalledWith(expect.objectContaining({name:'New group',vars:[{id:undefined,key:'TOKEN',secret:true,credentialId:'c'}]}),expect.objectContaining({headers:{'X-Pipewright-Team':'team'}}))
 })
 it('does not save a secret reference without credential.use',async()=>{
  allowed.add('variable_group.edit:g');vi.mocked(listVariableGroups).mockResolvedValue([{id:'g',name:'Group',description:'',vars:[{id:'v',key:'TOKEN',secret:true,credentialId:'c',maskedValue:'***',value:''}],createdAt:'',updatedAt:''}])
  const w=render();await flushPromises();await w.get('.vg-card .btn-secondary').trigger('click');await flushPromises();expect(w.find('.modal .btn-primary').exists()).toBe(false);expect(updateVariableGroup).not.toHaveBeenCalled()
 })
 it('updates and deletes only the permitted custom node',async()=>{
  allowed.add('custom_node.edit:n');allowed.add('custom_node.delete:n');vi.mocked(listCustomNodes).mockResolvedValue([{id:'n',name:'Script',description:'',summary:'',nodeType:'script',config:{command:'echo ok'},createdAt:'',updatedAt:''}])
  vi.spyOn(window,'confirm').mockReturnValue(true);const w=render();await flushPromises();await w.get('.cn-card .btn-secondary').trigger('click');await flushPromises();await w.get('#cn-name').setValue('Edited')
  await w.get('.modal .btn-primary').trigger('click');await flushPromises();expect(updateCustomNode).toHaveBeenCalledWith('n',expect.objectContaining({name:'Edited'}),expect.objectContaining({headers:{'X-Pipewright-Team':'team'}}))
  await w.get('.cn-card .link-danger').trigger('click');await flushPromises();expect(deleteCustomNode).toHaveBeenCalledWith('n',expect.any(Object))
 })

})

const group = (id: string) => ({
  id, name:id, description:'', vars:[], createdAt:'', updatedAt:'',
})
const node = (id: string, type = 'script') => ({
  id, name:id, description:'', summary:'', nodeType:type,
  config:{ command:'echo ok' }, createdAt:'', updatedAt:'',
})
const fixedTeam = () => expect.objectContaining({
  headers:{'X-Pipewright-Team':'team'},
  signal:expect.any(AbortSignal),
})

it('edits and removes only the variable group granted independently of read access', async () => {
  allowed.add('variable_group.read:other')
  allowed.add('variable_group.edit:g')
  allowed.add('variable_group.delete:g')
  vi.mocked(listVariableGroups).mockResolvedValue([group('g'), group('other')])
  vi.spyOn(window,'confirm').mockReturnValue(true)
  const w = render()
  await flushPromises()
  const [granted, readerOnly] = w.findAll('.vg-card')
  expect(readerOnly!.find('.btn-secondary').exists()).toBe(false)
  expect(readerOnly!.find('.link-danger').exists()).toBe(false)
  await granted!.get('.btn-secondary').trigger('click')
  await flushPromises()
  await w.get('#vg-name').setValue('Updated group')
  await w.get('.modal .btn-primary').trigger('click')
  await flushPromises()
  expect(updateVariableGroup).toHaveBeenCalledExactlyOnceWith('g',
    expect.objectContaining({name:'Updated group'}), fixedTeam())
  await w.get('.vg-card .link-danger').trigger('click')
  await flushPromises()
  expect(deleteVariableGroup).toHaveBeenCalledExactlyOnceWith('g', fixedTeam())
})

it.each(['group','node'] as const)('rechecks the %s row removal after the confirmation loses that grant', async type => {
  const action = type === 'group' ? 'variable_group.delete:g' : 'custom_node.delete:n'
  allowed.add(action)
  vi.mocked(listCustomNodes).mockResolvedValue([node('n')])
  vi.spyOn(window,'confirm').mockImplementation(() => {
    allowed.delete(action)
    usePermissionStore().invalidate()
    return true
  })
  const w = render()
  await flushPromises()
  await w.get(type === 'group' ? '.vg-card .link-danger' : '.cn-card .link-danger').trigger('click')
  await flushPromises()
  expect(deleteVariableGroup).not.toHaveBeenCalled()
  expect(deleteCustomNode).not.toHaveBeenCalled()
  expect(w.find(type === 'group' ? '.vg-card' : '.cn-card').exists()).toBe(true)
})

it.each(['header','empty'] as const)('opens the authorized custom node studio from its %s entry', async entry => {
  allowed.add('custom_node.create:')
  if (entry === 'header') vi.mocked(listCustomNodes).mockResolvedValue([node('n')])
  const w = render()
  await flushPromises()
  await w.findAll('.seg-item')[2]!.trigger('click')
  await flushPromises()
  await w.get(entry === 'header' ? '.page-header .btn-primary' : '.empty-state .btn-primary').trigger('click')
  expect(routePush).toHaveBeenCalledExactlyOnceWith({name:'studio-create'})
  expect(updateCustomNode).not.toHaveBeenCalled()
})

it('routes a permitted studio row to its exact original resource ID', async () => {
  allowed.add('custom_node.edit:n')
  allowed.add('custom_node.read:other')
  vi.mocked(listCustomNodes).mockResolvedValue([node('n','templated'),node('other','templated')])
  const w = render()
  await flushPromises()
  const cards = w.findAll('.cn-card')
  expect(cards[1]!.find('.btn-secondary').exists()).toBe(false)
  await cards[0]!.get('.btn-secondary').trigger('click')
  expect(routePush).toHaveBeenCalledExactlyOnceWith({name:'studio-edit', params:{id:'n'}})
  expect(updateCustomNode).not.toHaveBeenCalled()
})

it('opens and cancels an empty-state variable group draft without a write', async () => {
  allowed.add('variable_group.create:')
  vi.mocked(listVariableGroups).mockResolvedValue([])
  const w = render()
  await flushPromises()
  await w.findAll('.seg-item')[1]!.trigger('click')
  await flushPromises()
  await w.get('.empty-state .btn-primary').trigger('click')
  await flushPromises()
  expect(w.find('.modal').exists()).toBe(true)
  await w.get('.modal .modal-foot .btn-secondary').trigger('click')
  expect(w.find('.modal').exists()).toBe(false)
  expect(createVariableGroup).not.toHaveBeenCalled()
})

it.each([
  [0, listTemplates], [1, listVariableGroups], [2, listCustomNodes],
] as const)('retries library tab %s using the same captured team', async (index, load) => {
  vi.mocked(load).mockRejectedValueOnce(new Error('synthetic read failure'))
  const w = render()
  await flushPromises()
  await w.findAll('.seg-item')[index]!.trigger('click')
  await flushPromises()
  const before = vi.mocked(load).mock.calls.length
  await w.get('.banner-retry').trigger('click')
  await flushPromises()
  expect(load).toHaveBeenCalledTimes(before+1)
  expect(load).toHaveBeenLastCalledWith(fixedTeam())
  expect(deleteTemplate).not.toHaveBeenCalled()
  expect(deleteVariableGroup).not.toHaveBeenCalled()
  expect(deleteCustomNode).not.toHaveBeenCalled()
})

it.each(['group','node'] as const)('adds and removes local %s editor rows and closes with no writes', async type => {
  allowed.add('variable_group.edit:g')
  allowed.add('custom_node.edit:n')
  vi.mocked(listCustomNodes).mockResolvedValue([node('n')])
  const w = render()
  await flushPromises()
  await w.get(type === 'group' ? '.vg-card .btn-secondary' : '.cn-card .btn-secondary').trigger('click')
  await flushPromises()
  const count = w.findAll('.modal .var-remove').length
  await w.get('.modal .link-add').trigger('click')
  expect(w.findAll('.modal .var-remove')).toHaveLength(count+1)
  await w.findAll('.modal .var-remove').at(-1)!.trigger('click')
  expect(w.findAll('.modal .var-remove')).toHaveLength(count)
  await w.get('.modal .modal-close').trigger('click')
  expect(w.find('.modal').exists()).toBe(false)
  expect(updateVariableGroup).not.toHaveBeenCalled()
  expect(updateCustomNode).not.toHaveBeenCalled()
})

it.each([
  ['group','backdrop'], ['node','backdrop'], ['node','cancel'],
] as const)('closes the %s draft via %s without submitting', async (type, path) => {
  allowed.add('variable_group.edit:g')
  allowed.add('custom_node.edit:n')
  vi.mocked(listCustomNodes).mockResolvedValue([node('n')])
  const w = render()
  await flushPromises()
  await w.get(type === 'group' ? '.vg-card .btn-secondary' : '.cn-card .btn-secondary').trigger('click')
  await flushPromises()
  await w.get(path === 'backdrop' ? '.modal-scrim' : '.modal-foot button.btn-secondary').trigger('click')
  expect(w.find('.modal').exists()).toBe(false)
  expect(updateVariableGroup).not.toHaveBeenCalled()
  expect(updateCustomNode).not.toHaveBeenCalled()
})
