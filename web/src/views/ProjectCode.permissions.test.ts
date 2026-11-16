import {beforeEach,afterEach,describe,it,expect,vi} from 'vitest'
import {mount,flushPromises,type VueWrapper} from '@vue/test-utils'
import {createPinia,setActivePinia} from 'pinia'
import {reactive} from 'vue'
import ProjectCode from './ProjectCode.vue'
import CodeTree from '../components/code/CodeTree.vue'
import CodeViewer from '../components/code/CodeViewer.vue'
import {useSessionStore} from '../stores/session'
import {usePermissionStore} from '../stores/permissions'
import {access} from '../api/access'
import {listProjects,type Project} from '../api/projects'
import {getSourceTree,getSourceBlob,type SourceTree,type SourceBlob} from '../api/source'
import {HttpError} from '../api/http'
const nav=vi.hoisted(()=>({replace:vi.fn()}))
const editor=vi.hoisted(()=>({create:vi.fn(),dispose:vi.fn(),setTheme:vi.fn()}))
const route=reactive<{params:{id:string},query:{path?:string}}>({params:{id:'a'},query:{}})
vi.mock('vue-router',()=>({useRoute:()=>route,useRouter:()=>nav}))
vi.mock('../api/access',()=>({access:{decisions:vi.fn()}}))
vi.mock('../api/projects',()=>({listProjects:vi.fn()}))
vi.mock('../api/source',()=>({getSourceTree:vi.fn(),getSourceBlob:vi.fn()}))
vi.mock('../components/code/monacoEnv',()=>({installMonacoEnvironment:vi.fn()}))
vi.mock('monaco-editor',()=>({editor:{create:editor.create,setTheme:editor.setTheme}}))
const project={id:'a',name:'Source A',defaultBranch:'main'} as Project
const tree:SourceTree={ref:'main',path:'',entries:[{name:'secret.txt',path:'secret.txt',type:'file',size:7},{name:'src',path:'src',type:'dir'}]}
const blob:SourceBlob={ref:'main',path:'secret.txt',size:7,binary:false,truncated:false,content:'private content'}
let wrapper:VueWrapper|undefined,allowed:boolean
const deferred=<T,>()=>{let resolve!:(v:T)=>void;const promise=new Promise<T>(r=>resolve=r);return {promise,resolve}}
async function settle(){for(let i=0;i<5;i++)await flushPromises()}
beforeEach(()=>{
 vi.resetAllMocks();setActivePinia(createPinia());route.params={id:'a'};route.query={};allowed=true
 useSessionStore().setUser({username:'member',userId:'u',multiTenant:true});usePermissionStore().setTeam('one')
 vi.mocked(access.decisions).mockImplementation(async(team,queries)=>({userId:useSessionStore().user!.userId!,teamId:team,modeEpoch:1,revision:1,catalogVersion:1,allowed:queries.map(()=>allowed)}))
 vi.mocked(listProjects).mockResolvedValue([project]);vi.mocked(getSourceTree).mockResolvedValue(tree);vi.mocked(getSourceBlob).mockResolvedValue(blob);editor.create.mockReturnValue({dispose:editor.dispose})
})
afterEach(()=>{wrapper?.unmount();wrapper=undefined})
async function open(){wrapper=mount(ProjectCode,{global:{stubs:{RouterLink:{template:'<a><slot/></a>'}}}});await settle();return wrapper}
describe('Project code permission lifetime',()=>{
 it('does not fetch project/source without project.read',async()=>{allowed=false;await open();expect(listProjects).not.toHaveBeenCalled();expect(getSourceTree).not.toHaveBeenCalled();expect(wrapper!.find('.code-page').exists()).toBe(false)})
 it('passes the fixed team to metadata, tree, blob and renders read-only editor',async()=>{
  route.query.path='secret.txt';await open();for(const options of [vi.mocked(listProjects).mock.calls[0]![0],vi.mocked(getSourceTree).mock.calls[0]![2],vi.mocked(getSourceBlob).mock.calls[0]![2]]){expect(options?.headers).toEqual({'X-Pipewright-Team':'one'});expect(options?.signal).toBeInstanceOf(AbortSignal)}
  expect(editor.create).toHaveBeenCalledWith(expect.anything(),expect.objectContaining({value:'private content',readOnly:true,domReadOnly:true}))
 })
 it('selects files and lazily opens directories using actual tree buttons',async()=>{
  await open();await wrapper!.get('[title="secret.txt"]').trigger('click');expect(nav.replace).toHaveBeenCalledWith({query:{path:'secret.txt'}})
  vi.mocked(getSourceTree).mockResolvedValue({...tree,path:'src',entries:[]});await wrapper!.get('[title="src"]').trigger('click');await settle();expect(getSourceTree).toHaveBeenLastCalledWith('a',{ref:'main',path:'src'},expect.objectContaining({headers:{'X-Pipewright-Team':'one'}}))
 })
 it('disposes the source editor and clears all data when read permission is revoked',async()=>{
  route.query.path='secret.txt';await open();const signal=vi.mocked(getSourceBlob).mock.calls[0]![2]!.signal!;allowed=false;usePermissionStore().invalidate();await settle();expect(signal.aborted).toBe(true);expect(editor.dispose).toHaveBeenCalled();expect(wrapper!.text()).not.toContain('Source A');expect(wrapper!.find('.code-view').exists()).toBe(false)
 })
 it('drops old-team blobs even if the transport ignores abort',async()=>{
  const pending=deferred<SourceBlob>();vi.mocked(getSourceBlob).mockReturnValueOnce(pending.promise);route.query.path='secret.txt';await open();const signal=vi.mocked(getSourceBlob).mock.calls[0]![2]!.signal!
  allowed=false;usePermissionStore().setTeam('two');await settle();pending.resolve(blob);await settle();expect(signal.aborted).toBe(true);expect(editor.create).not.toHaveBeenCalled();expect(wrapper!.text()).not.toContain('private content')
 })
 it.each([401,403])('ends source scope on HTTP %s without request loops',async(status)=>{
  vi.mocked(getSourceTree).mockRejectedValue(new HttpError(status,null,'denied'));await open();expect(wrapper!.find('.code-page').exists()).toBe(false);expect(getSourceTree).toHaveBeenCalledTimes(1);if(status===401)expect(useSessionStore().user).toBeNull()
 })
 it('does not apply stale project metadata after a project route change',async()=>{
  const pending=deferred<Project[]>();vi.mocked(listProjects).mockReturnValueOnce(pending.promise);await open();route.params={id:'b'};vi.mocked(listProjects).mockResolvedValue([{...project,id:'b',name:'Source B'}]);await settle();pending.resolve([project]);await settle();expect(wrapper!.text()).not.toContain('Source A');expect(wrapper!.text()).toContain('Source B')
 })
 it('tree discards stale directory results after ref change',async()=>{
  const pending=deferred<SourceTree>();vi.mocked(getSourceTree).mockReturnValueOnce(pending.promise).mockResolvedValueOnce({...tree,entries:[]});wrapper=mount(CodeTree,{props:{projectId:'a',treeRef:'old',selectedPath:null}});await settle();await wrapper.setProps({treeRef:'new'});await settle();pending.resolve(tree);await settle();expect(wrapper.text()).not.toContain('secret.txt');expect(wrapper.emitted('rootLoaded')).toHaveLength(1)
 })
 it('blob discards an old selection and never creates its editor',async()=>{
  const pending=deferred<SourceBlob>();vi.mocked(getSourceBlob).mockReturnValueOnce(pending.promise);wrapper=mount(CodeViewer,{props:{projectId:'a',treeRef:'main',path:'secret.txt'}});await settle();await wrapper.setProps({path:null});pending.resolve(blob);await settle();expect(editor.create).not.toHaveBeenCalled();expect(wrapper.text()).not.toContain('private content')
 })
 it('retains legacy off-mode source viewing without permission API requests',async()=>{useSessionStore().setUser({username:'admin',multiTenant:false});route.query.path='secret.txt';await open();expect(editor.create).toHaveBeenCalled();expect(access.decisions).not.toHaveBeenCalled();expect(vi.mocked(getSourceBlob).mock.calls[0]![2]!.headers).toBeUndefined()})
})
