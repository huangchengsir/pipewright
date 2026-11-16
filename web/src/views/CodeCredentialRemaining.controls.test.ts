import {beforeEach,afterEach,describe,it,expect,vi} from 'vitest'
import {mount,flushPromises,type VueWrapper} from '@vue/test-utils'
import {createPinia,setActivePinia} from 'pinia'
import {reactive} from 'vue'
import ProjectCode from './ProjectCode.vue'
import CredentialSelect from '../components/projects/CredentialSelect.vue'
import type {Credential} from '../api/credentials'
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

it('actual root retry reloads scoped current repository and stops after abort',async()=>{const controller=new AbortController();const opts={headers:{'X-Pipewright-Team':'one'},signal:controller.signal};vi.mocked(getSourceTree).mockRejectedValueOnce(new Error('transient'));wrapper=mount(CodeTree,{props:{projectId:'a',treeRef:'main',selectedPath:null,requestOptions:opts}});await settle();const retry=wrapper.get('.code-tree-retry');await retry.trigger('click');await settle();expect(getSourceTree).toHaveBeenCalledTimes(2);expect(getSourceTree).toHaveBeenLastCalledWith('a',{ref:'main',path:''},opts);expect(wrapper.get('[title="secret.txt"]').text()).toContain('secret.txt');controller.abort();await retry.trigger('click');await settle();expect(getSourceTree).toHaveBeenCalledTimes(2)})
it.each(['degraded','error'])('actual viewer %s retry reads current file and stops after abort',async(state)=>{const controller=new AbortController();const opts={headers:{'X-Pipewright-Team':'one'},signal:controller.signal};if(state==='error')vi.mocked(getSourceBlob).mockRejectedValueOnce(new Error('transient'));else vi.mocked(getSourceBlob).mockResolvedValueOnce({...blob,content:'',degraded:true});wrapper=mount(CodeViewer,{props:{projectId:'a',treeRef:'main',path:'secret.txt',requestOptions:opts}});await settle();const retry=wrapper.get('.code-view-retry');await retry.trigger('click');await settle();expect(getSourceBlob).toHaveBeenCalledTimes(2);expect(getSourceBlob).toHaveBeenLastCalledWith('a',{ref:'main',path:'secret.txt'},opts);expect(editor.create).toHaveBeenCalledWith(expect.anything(),expect.objectContaining({value:'private content',readOnly:true}));controller.abort();await retry.trigger('click');await settle();expect(getSourceBlob).toHaveBeenCalledTimes(2)})
it('actual project retry reloads metadata with fixed team and rejects retained button after logout',async()=>{vi.mocked(listProjects).mockRejectedValueOnce(new Error('transient'));await open();const retry=wrapper!.get('.code-page-retry');await retry.trigger('click');await settle();expect(listProjects).toHaveBeenCalledTimes(2);expect(listProjects).toHaveBeenLastCalledWith(expect.objectContaining({headers:{'X-Pipewright-Team':'one'}}));useSessionStore().clearSession();await settle();await retry.trigger('click');await settle();expect(listProjects).toHaveBeenCalledTimes(2)})
it('tree rejects retained node from the prior repository after reset',async()=>{wrapper=mount(CodeTree,{props:{projectId:'a',treeRef:'main',selectedPath:null}});await settle();const old=wrapper.get('[title="secret.txt"]');vi.mocked(getSourceTree).mockResolvedValueOnce({...tree,entries:[{name:'fresh.txt',path:'fresh.txt',type:'file'}]});await wrapper.setProps({projectId:'b'});await settle();await old.trigger('click');expect(wrapper.emitted('select')).toBeUndefined();await wrapper.get('[title="fresh.txt"]').trigger('click');expect(wrapper.emitted('select')).toEqual([[expect.objectContaining({path:'fresh.txt'})]])})
it.each(['project','path'])('viewer disposes old editor and selection before pending %s read completes',async(change)=>{wrapper=mount(CodeViewer,{props:{projectId:'a',treeRef:'main',path:'secret.txt'}});await settle();expect(editor.create).toHaveBeenCalledOnce();const pending=deferred<SourceBlob>();vi.mocked(getSourceBlob).mockReturnValueOnce(pending.promise);await wrapper.setProps(change==='project'?{projectId:'b'}:{path:'next.txt'});await settle();expect(editor.dispose).toHaveBeenCalledOnce();expect(editor.create).toHaveBeenCalledOnce();pending.resolve({...blob,content:'new content'});await settle();expect(editor.create).toHaveBeenLastCalledWith(expect.anything(),expect.objectContaining({value:'new content'}))})
const cred:Credential={id:'c1',name:'Credential one',type:'git_token',scope:'',username:'fixture',maskedValue:'***',lastUsedAt:null,createdAt:''}
async function selectCredential(){wrapper=mount(CredentialSelect,{props:{inputId:'credential',modelValue:'',credentials:[cred,{...cred,id:'c2',name:'Credential two'}],placeholder:'Choose',loadingLabel:'Loading',emptyLabel:'None'}});await wrapper.get('[role="combobox"]').trigger('click');return wrapper}
it('credential trigger keyboard hover and option click select only current entry',async()=>{const w=await selectCredential();expect(w.get('[role="combobox"]').attributes('aria-expanded')).toBe('true');await w.findAll('[role="option"]')[1]!.trigger('mouseenter');await w.get('[role="combobox"]').trigger('keydown',{key:'Enter'});expect(w.emitted('update:modelValue')).toEqual([['c2']]);await w.get('[role="combobox"]').trigger('click');await w.get('[role="option"]').trigger('click');expect(w.emitted('update:modelValue')).toEqual([['c2'],['c1']]);await w.get('[role="combobox"]').trigger('keydown',{key:'ArrowDown'});await w.get('[role="combobox"]').trigger('keydown',{key:'Escape'});expect(w.find('[role="listbox"]').exists()).toBe(false)})
it.each(['disabled','loading','replacement','unmount'])('credential retained option cannot select after %s boundary',async(boundary)=>{const w=await selectCredential();const old=w.get('[role="option"]');if(boundary==='unmount'){w.unmount();wrapper=undefined}else if(boundary==='replacement')await w.setProps({credentials:[{...cred,id:'other',name:'Other team credential'}]});else await w.setProps({[boundary]:true});await old.trigger('click');expect(w.emitted('update:modelValue')).toBeUndefined();if(boundary!=='unmount')expect(w.find('[role="listbox"]').exists()).toBe(false)})
