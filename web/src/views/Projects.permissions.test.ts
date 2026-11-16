import {beforeEach,afterEach,describe,it,expect,vi} from 'vitest'
import {mount,flushPromises,type VueWrapper} from '@vue/test-utils'
import {createPinia,setActivePinia} from 'pinia'
import {reactive} from 'vue'
import Projects from './Projects.vue'
import {useSessionStore} from '../stores/session'
import {usePermissionStore} from '../stores/permissions'
import {access} from '../api/access'
import {listProjects,createProject,updateProject,deleteProject,testClone,type Project} from '../api/projects'
import {listCredentials,createCredential} from '../api/credentials'
import {triggerManual,type RunDetail} from '../api/runs'
import {listRefs,listCommits} from '../api/refs'
import {getParameters} from '../api/parameters'
import {HttpError,type HttpOptions} from '../api/http'
const nav=vi.hoisted(()=>({push:vi.fn(),replace:vi.fn()}))
const route=reactive<{query:Record<string,string>}>({query:{}})
vi.mock('vue-router',()=>({useRouter:()=>nav,useRoute:()=>route}))
vi.mock('../api/access',()=>({access:{decisions:vi.fn()}}))
vi.mock('../api/projects',()=>({listProjects:vi.fn(),createProject:vi.fn(),updateProject:vi.fn(),deleteProject:vi.fn(),testClone:vi.fn()}))
vi.mock('../api/credentials',()=>({listCredentials:vi.fn(),createCredential:vi.fn()}))
vi.mock('../api/runs',()=>({triggerManual:vi.fn()}))
vi.mock('../api/refs',()=>({listRefs:vi.fn(),listCommits:vi.fn()}))
vi.mock('../api/parameters',()=>({getParameters:vi.fn(),validateParamValues:()=>null}))
const p:Project={id:'a',name:'Project A',repoUrl:'https://git.example/a',defaultBranch:'main',credentialId:'c',credentialName:'Credential',pacEnabled:false,prStatusEnabled:false,lastRunStatus:null,targetServers:[],createdAt:'now',updatedAt:'now'}
const c={id:'c',name:'Credential',type:'git_token' as const,scope:'',username:'',maskedValue:'masked',createdAt:'now',lastUsedAt:null}
let w:VueWrapper|undefined,grants:Set<string>
const pending=<T,>()=>{let resolve!:(v:T)=>void;const promise=new Promise<T>(r=>resolve=r);return {promise,resolve}}
beforeEach(()=>{
 vi.clearAllMocks();localStorage.clear();route.query={};setActivePinia(createPinia())
 useSessionStore().setUser({username:'member',userId:'u',multiTenant:true});usePermissionStore().setTeam('one')
 grants=new Set(['project.read','pipeline.read','project.create','project.edit','project.delete','run.start','credential.use','credential.create'])
 vi.mocked(access.decisions).mockImplementation(async(team,queries)=>({userId:useSessionStore().user!.userId!,teamId:team,modeEpoch:1,revision:1,catalogVersion:1,allowed:queries.map(q=>grants.has(q.action)&&q.resourceId!=='hidden')}))
 vi.mocked(listProjects).mockResolvedValue([p]);vi.mocked(listCredentials).mockResolvedValue([c]);vi.mocked(getParameters).mockResolvedValue([])
 vi.mocked(listRefs).mockResolvedValue({branches:[],tags:[]});vi.mocked(listCommits).mockResolvedValue([])
 vi.mocked(createProject).mockResolvedValue({...p,id:'new'});vi.mocked(updateProject).mockResolvedValue({...p,name:'Renamed'})
 vi.mocked(deleteProject).mockResolvedValue(undefined);vi.mocked(testClone).mockResolvedValue({ok:true,defaultBranch:'main'})
 vi.mocked(createCredential).mockResolvedValue({...c,id:'new-credential'});vi.mocked(triggerManual).mockResolvedValue({id:'run-a'} as RunDetail)
})
afterEach(()=>{w?.unmount();w=undefined})
async function open(){w=mount(Projects,{attachTo:document.body,global:{stubs:{Teleport:true,RouterLink:{props:['to'],template:'<a><slot/></a>'}}}});await settle();return w}
async function settle(){for(let i=0;i<4;i++)await flushPromises()}
function action(text:string){return w!.findAll('button').find(b=>b.attributes('aria-label')?.includes(text))!}
function options(value:HttpOptions|undefined){expect(value?.headers).toEqual({'X-Pipewright-Team':'one'});expect(value?.signal).toBeInstanceOf(AbortSignal);expect(value?.throwOnUnauthorized).toBe(true)}
async function createForm(){await w!.get('.page-header .btn-primary').trigger('click');await w!.get('#proj-name').setValue('New');await w!.get('#proj-repo').setValue('https://git.example/new');await settle();await w!.get('#proj-cred').trigger('click');await w!.get('[role="option"]').trigger('click');await settle()}
describe('Projects actual permission controls',()=>{
 it('renders only granted rows and buttons, including navigation',async()=>{
  grants=new Set(['project.read']);vi.mocked(listProjects).mockResolvedValue([p,{...p,id:'hidden',name:'Hidden project'}]);await open()
  expect(w!.text()).toContain('Project A');expect(w!.text()).not.toContain('Hidden project')
  expect(w!.find('.page-header .btn-primary').exists()).toBe(false)
  expect(action('重命名')).toBeUndefined();expect(action('删除')).toBeUndefined();expect(w!.find('.btn-run').exists()).toBe(false)
  expect(action('配置项目')).toBeUndefined();await action('代码').trigger('click');expect(nav.push).toHaveBeenCalledWith({name:'project-code',params:{id:'a'}})
 })
 it('runs each granted navigation and run button with a fixed request context',async()=>{
  await open();await action('配置项目').trigger('click');expect(nav.push).toHaveBeenCalledWith({name:'project-pipeline',params:{id:'a'}})
  await w!.get('.project-card .action-btn--run').trigger('click');await settle();options(vi.mocked(getParameters).mock.calls[0]?.[1]);options(vi.mocked(listRefs).mock.calls[0]?.[1]);options(vi.mocked(listCommits).mock.calls[0]?.[3])
  await w!.get('.modal-footer button[type=submit]').trigger('click');await settle();expect(triggerManual).toHaveBeenCalledTimes(1);options(vi.mocked(triggerManual).mock.calls[0]?.[2]);expect(nav.push).toHaveBeenCalledWith('/runs/run-a')
 })
 it('creates and probes only with credential use permission',async()=>{
  await open();await createForm();await w!.get('.test-clone-row button').trigger('click');await settle();expect(testClone).toHaveBeenCalledTimes(1);options(vi.mocked(testClone).mock.calls[0]?.[1])
  await w!.get('.modal-footer button[type=submit]').trigger('click');await settle();expect(createProject).toHaveBeenCalledTimes(1);options(vi.mocked(createProject).mock.calls[0]?.[1])
 })
 it('blocks probe and create on a credential outside the usable data scope',async()=>{
  grants.delete('credential.use');await open();await createForm();expect(w!.find('.test-clone-row button').exists()).toBe(false)
  expect(w!.get('.modal-footer button[type="submit"]').attributes('disabled')).toBeDefined();await w!.get('.modal-form').trigger('submit');expect(createProject).not.toHaveBeenCalled();expect(testClone).not.toHaveBeenCalled()
 })
 it('hides inline credential creation without its action',async()=>{
  grants.delete('credential.create');await open();await w!.get('.page-header .btn-primary').trigger('click');expect(w!.find('.credential-create-toggle').exists()).toBe(false)
 })
 it('creates an inline credential through the actual form and keeps its secret out of the result',async()=>{
  await open();await w!.get('.page-header .btn-primary').trigger('click');await settle();await w!.get('.credential-create-toggle').trigger('click')
  await w!.get('#inline-cred-name').setValue('New credential');await w!.get('#inline-cred-username').setValue('git-user');await w!.get('#inline-cred-secret').setValue('synthetic-test-value');await w!.get('.inline-credential-submit').trigger('click');await settle()
  expect(createCredential).toHaveBeenCalledTimes(1);options(vi.mocked(createCredential).mock.calls[0]?.[1]);expect(w!.find('#inline-cred-secret').exists()).toBe(false);expect(w!.html()).not.toContain('synthetic-test-value')
 })
 it('drops inline credential secrets when creation is revoked',async()=>{
  await open();await w!.get('.page-header .btn-primary').trigger('click');await settle();await w!.get('.credential-create-toggle').trigger('click');await w!.get('#inline-cred-secret').setValue('synthetic-test-value');const button=w!.get('.inline-credential-submit')
  grants.delete('credential.create');usePermissionStore().invalidate();await settle();expect(w!.find('#inline-cred-secret').exists()).toBe(false);await button.trigger('click');expect(createCredential).not.toHaveBeenCalled();expect(w!.html()).not.toContain('synthetic-test-value')
 })
 it('closes a create draft on revocation and rejects retained submit handlers',async()=>{
  await open();await createForm();const form=w!.get('.modal-form');grants.delete('project.create');usePermissionStore().invalidate();await settle();expect(w!.find('[role="dialog"]').exists()).toBe(false);await form.trigger('submit');expect(createProject).not.toHaveBeenCalled()
 })
 it('renames and deletes using the fixed team instead of current ambient state',async()=>{
  await open();await action('重命名').trigger('click');await w!.get('#rename-input').setValue('Renamed');await w!.get('.modal-footer button[type=submit]').trigger('click');await settle();options(vi.mocked(updateProject).mock.calls[0]?.[3])
  await action('删除').trigger('click');await w!.get('.modal-footer .btn-danger').trigger('click');await settle();options(vi.mocked(deleteProject).mock.calls[0]?.[2]);expect(w!.text()).not.toContain('Project A')
 })
 it.each(['project.edit','project.delete','run.start'])('closes %s modal on revocation and blocks retained controls',async(actionName)=>{
  await open();if(actionName==='project.edit')await action('重命名').trigger('click');else if(actionName==='project.delete')await action('删除').trigger('click');else await w!.get('.project-card .action-btn--run').trigger('click')
  await settle();const button=w!.get('.modal-footer button:last-child');grants.delete(actionName);usePermissionStore().invalidate();await settle();expect(w!.find('[role="dialog"]').exists()).toBe(false);await button.trigger('click');expect(updateProject).not.toHaveBeenCalled();expect(deleteProject).not.toHaveBeenCalled();expect(triggerManual).not.toHaveBeenCalled()
 })
 it('does not navigate on a late trigger response after team switch',async()=>{
  const deferred=pending<RunDetail>();vi.mocked(triggerManual).mockReturnValue(deferred.promise);await open();await w!.get('.project-card .action-btn--run').trigger('click');await settle();await w!.get('.modal-form').trigger('submit');const request=vi.mocked(triggerManual).mock.calls[0]![2]!
  usePermissionStore().setTeam('two');await settle();expect(request.signal?.aborted).toBe(true);deferred.resolve({id:'old-team-run'} as RunDetail);await settle();expect(nav.push).not.toHaveBeenCalledWith('/runs/old-team-run')
 })
 it('does not navigate on a late trigger response after action revocation',async()=>{
  const deferred=pending<RunDetail>();vi.mocked(triggerManual).mockReturnValue(deferred.promise);await open();await w!.get('.project-card .action-btn--run').trigger('click');await settle();await w!.get('.modal-form').trigger('submit');grants.delete('run.start');usePermissionStore().invalidate();await settle();deferred.resolve({id:'revoked-run'} as RunDetail);await settle();expect(nav.push).not.toHaveBeenCalledWith('/runs/revoked-run')
 })
 it('discards stale project data after employee identity changes',async()=>{
  const deferred=pending<Project[]>();vi.mocked(listProjects).mockReturnValueOnce(deferred.promise).mockResolvedValueOnce([]);await open();const request=vi.mocked(listProjects).mock.calls[0]![0]!
  useSessionStore().setUser({username:'other',userId:'v',multiTenant:true});await settle();expect(request.signal?.aborted).toBe(true);deferred.resolve([{...p,name:'Old employee project'}]);await settle();expect(w!.text()).not.toContain('Old employee project')
 })
 it.each([401,403])('clears project data and aborts pending scopes on HTTP %s',async(status)=>{
  await open();vi.mocked(deleteProject).mockRejectedValue(new HttpError(status,null,'denied'));await action('删除').trigger('click');await w!.get('.modal-footer .btn-danger').trigger('click');await settle();expect(w!.text()).not.toContain('Project A');expect(w!.find('[role="dialog"]').exists()).toBe(false);expect(vi.mocked(deleteProject).mock.calls[0]![2]!.signal?.aborted).toBe(true);if(status===401)expect(useSessionStore().user).toBeNull()
 })
})

describe('Projects remaining control interactions',()=>{
 it('retries an actual load error through the retry button with fixed team',async()=>{
  vi.mocked(listProjects).mockRejectedValueOnce(new HttpError(500,null,'load failed')).mockResolvedValueOnce([p]);await open()
  expect(w!.find('.banner-retry').exists()).toBe(true);await w!.get('.banner-retry').trigger('click');await settle()
  expect(w!.text()).toContain('Project A');expect(w!.find('.banner-retry').exists()).toBe(false);options(vi.mocked(listProjects).mock.calls[1]?.[0])
 })
 it('executes every status filter and clears the empty search without expanding visible data',async()=>{
  grants=new Set(['project.read']);vi.mocked(listProjects).mockResolvedValue([p,{...p,id:'hidden',name:'Hidden project'}]);await open()
  const filters=w!.findAll('.filter-tab');expect(filters.length).toBe(7)
  for(const filter of filters){await filter.trigger('click');expect(filter.classes()).toContain('filter-tab--active');expect(w!.text()).not.toContain('Hidden project')}
  await w!.get('.empty-state .btn-secondary').trigger('click');await w!.get('.search-input').setValue('missing')
  await w!.get('.empty-state .btn-secondary').trigger('click');await settle();expect(w!.text()).toContain('Project A');expect(w!.text()).not.toContain('Hidden project');expect((w!.get('.search-input').element as HTMLInputElement).value).toBe('')
 })
 it.each([true,false])('checks the separate empty-list create button, granted=%s',async(granted)=>{
  if(!granted)grants.delete('project.create');vi.mocked(listProjects).mockResolvedValue([]);await open()
  expect(w!.find('.empty-state .btn-primary').exists()).toBe(granted)
  if(granted){await w!.get('.empty-state .btn-primary').trigger('click');await settle();expect(w!.find('#proj-name').exists()).toBe(true)}
  expect(createProject).not.toHaveBeenCalled()
 })
 for(const modal of ['create','rename','delete','run']) for(const close of ['header','cancel','escape','scrim']){
  it(`${modal}: handles ${close} without a write`,async()=>{
   await open()
   if(modal==='create')await w!.get('.page-header .btn-primary').trigger('click')
   else if(modal==='rename')await action('重命名').trigger('click')
   else if(modal==='delete')await action('删除').trigger('click')
   else await w!.get('.project-card .action-btn--run').trigger('click')
   await settle();const dialog=w!.get('[role="dialog"]')
   if(close==='header')await dialog.get('.modal-close').trigger('click')
   else if(close==='cancel')await dialog.get('.modal-footer button:first-child').trigger('click')
   else if(close==='escape')await dialog.trigger('keydown',{key:'Escape'})
   else await dialog.trigger('click')
   await settle();expect(w!.find('[role="dialog"]').exists()).toBe(modal==='create' && close==='scrim')
   expect(createProject).not.toHaveBeenCalled();expect(updateProject).not.toHaveBeenCalled();expect(deleteProject).not.toHaveBeenCalled();expect(triggerManual).not.toHaveBeenCalled()
  })
 }
})

it('leaves the same-team read-only project visible with only its own code route', async () => {
  const sibling = { ...p, id: 'b', name: 'Project B' }
  const mutations = new Set(['project.edit', 'project.delete', 'run.start', 'pipeline.read'])
  vi.mocked(listProjects).mockResolvedValue([p, sibling])
  vi.mocked(access.decisions).mockImplementation(async (team, queries) => ({
    userId: 'u', teamId: team, modeEpoch: 1, revision: 1, catalogVersion: 1,
    allowed: queries.map(query => grants.has(query.action) &&
      !(query.resourceId === sibling.id && mutations.has(query.action))),
  }))
  await open()

  const allowed = w!.findAll('.project-card').find(card => card.text().includes('Project A'))!
  const readOnly = w!.findAll('.project-card').find(card => card.text().includes('Project B'))!
  expect(allowed.findAll('.card-actions button')).toHaveLength(5)
  expect(readOnly.findAll('.card-actions button')).toHaveLength(1)
  expect(readOnly.find('.action-btn--run').exists()).toBe(false)
  expect(readOnly.find('.action-btn--danger').exists()).toBe(false)
  await readOnly.get('.card-actions button').trigger('click')
  expect(nav.push).toHaveBeenCalledExactlyOnceWith({ name: 'project-code', params: { id: 'b' } })
  expect(updateProject).not.toHaveBeenCalled()
  expect(deleteProject).not.toHaveBeenCalled()
  expect(triggerManual).not.toHaveBeenCalled()

  await allowed.get('.action-btn--run').trigger('click')
  await settle()
  await w!.get('.modal-footer button[type=submit]').trigger('click')
  await settle()
  expect(triggerManual).toHaveBeenCalledTimes(1)
  expect(vi.mocked(triggerManual).mock.calls[0]![0]).toBe('a')
  await action('重命名').trigger('click')
  await w!.get('#rename-input').setValue('Renamed')
  await w!.get('.modal-footer button[type=submit]').trigger('click')
  await settle()
  expect(updateProject).toHaveBeenCalledTimes(1)
  expect(vi.mocked(updateProject).mock.calls[0]![0]).toBe('a')
  await action('删除').trigger('click')
  await w!.get('.modal-footer .btn-danger').trigger('click')
  await settle()
  expect(deleteProject).toHaveBeenCalledTimes(1)
  expect(vi.mocked(deleteProject).mock.calls[0]![0]).toBe('a')
  expect(w!.text()).toContain('Project B')
})

it.each([
  ['https://git.example/new', 0, 'git_token'],
  ['https://git.example/new', 1, 'git_http'],
  ['ssh://git@git.example/new', 0, 'ssh_key'],
  ['ssh://git@git.example/new', 1, 'ssh_password'],
] as const)('uses the selected inline credential type in the request for %s #%s (%s)', async (repo, index, type) => {
  await open()
  await w!.get('.page-header .btn-primary').trigger('click')
  await w!.get('#proj-repo').setValue(repo)
  await w!.get('.credential-create-toggle').trigger('click')
  await settle()
  const types = w!.findAll('.inline-credential-type')
  expect(types).toHaveLength(2)
  await types[index]!.trigger('click')
  await settle()
  expect(w!.findAll('.inline-credential-type')[index]!.classes()).toContain('inline-credential-type--active')
  await w!.get('#inline-cred-name').setValue('New credential')
  const username = w!.find('#inline-cred-username')
  if (username.exists()) await username.setValue('git-user')
  await w!.get('#inline-cred-secret').setValue('synthetic-test-value')
  await w!.get('.inline-credential-submit').trigger('click')
  await settle()
  expect(createCredential).toHaveBeenCalledWith(expect.objectContaining({ type }), expect.any(Object))
})
