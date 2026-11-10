import {afterEach,beforeEach,describe,it,expect,vi} from 'vitest'
import {mount,flushPromises,type VueWrapper} from '@vue/test-utils'
import Release from './KubeReleasePanel.vue'
import Settings from './KubeReleaseSettings.vue'
import Editor from '../pipeline/KubernetesReleaseJobConfig.vue'
import Password from './KubePassword.vue'
import {newReleaseConfig} from '../pipeline/kubernetesReleaseConfig'
import {HttpError} from '../../api/http'
import {i18n} from '../../i18n'
const m=vi.hoisted(()=>({release:vi.fn(),rollbackRelease:vi.fn(),rollbackRequest:vi.fn(),reauth:vi.fn(),connections:vi.fn(),releaseGrants:vi.fn(),releaseProfile:vi.fn(),saveReleaseGrant:vi.fn(),confirm:vi.fn()}))
vi.mock('../../api/kubernetes',async()=>({...await vi.importActual('../../api/kubernetes'),kubeAPI:m}))
vi.mock('../../composables/useConfirm',()=>({useConfirm:()=>({open:m.confirm})}))
const wrappers:VueWrapper[]=[]
const tr=(key:string)=>i18n.global.t(key)
const button=(w:VueWrapper,key:string)=>w.findAll('button').find(b=>b.text()===tr(key))!
async function auth(w:VueWrapper){const gate=w.getComponent(Password).props('state');gate.password.value='synthetic-password';await gate.verify();await flushPromises()}
const row:any={id:'release',runId:'run',nodeKey:'node',revision:3,state:'ready',cleanup:'verified',ownerFenced:true,cursor:0,preview:{planId:'sealed',mode:'manifest',resources:[]},results:[]}
beforeEach(()=>{for(const fn of Object.values(m))fn.mockReset();sessionStorage.clear();m.release.mockResolvedValue(row);m.confirm.mockResolvedValue(true);m.reauth.mockResolvedValue({reauthenticatedUntil:new Date(Date.now()+300000).toISOString()});m.connections.mockResolvedValue([]);m.releaseGrants.mockResolvedValue([]);m.saveReleaseGrant.mockResolvedValue({})})
afterEach(()=>{for(const w of wrappers.splice(0))w.unmount();vi.restoreAllMocks()})
describe('review106 Kubernetes writable boundaries',()=>{
 for(const status of [400,403,409,422,408])it('rollback completed status '+status+' retains only uncertain request IDs',async()=>{
 m.rollbackRelease.mockRejectedValue(new HttpError(status,{code:'kubernetes_conflict',message:'closed'},'closed'))
 const w=mount(Release,{props:{release:row},global:{stubs:{KubePassword:true,RouterLink:true}}});wrappers.push(w);await flushPromises();await button(w,'kubernetes.release.rollback').trigger('click');await flushPromises();await auth(w)
 expect(m.rollbackRelease).toHaveBeenCalledTimes(1);expect(sessionStorage.getItem('pipewright:kubernetes-rollback:release')!==null).toBe(status===408)
 if(status!==408)expect(button(w,'kubernetes.release.rollback').attributes('disabled')).toBeUndefined()
 })
 it('grant edit and subsequent add send only writable DTO fields',async()=>{
 const grant:any={id:'old-id',connectionId:'connection',revision:7,createdAt:1,projectId:'project',environment:'dev',executor:{kind:'local',serverId:'not-local'},resources:[{resource:{group:'',version:'v1',kind:'ConfigMap',resource:'configmaps',namespace:'app',name:'approved',unexpected:'private'},actions:['get'],containers:[],fields:[],revision:9}],requireApproval:true,enabled:true}
 m.releaseGrants.mockResolvedValue([grant]);m.releaseProfile.mockResolvedValue({profile:{runtime:{},helmVersion:'v4.3.1'}})
 const w=mount(Settings,{props:{connection:{id:'connection',executors:[{kind:'local'}]} as any,projects:[{id:'project',name:'Project'}] as any},global:{stubs:{KubePassword:true}}});wrappers.push(w);await flushPromises();await button(w,'kubernetes.edit').trigger('click');await w.findAll('form').at(-1)!.trigger('submit');await flushPromises();await auth(w)
 const input=m.saveReleaseGrant.mock.calls[0][1];expect(Object.keys(input).sort()).toEqual(['projectId','environment','executor','resources','requireApproval','enabled'].sort());expect(input.executor).toEqual({kind:'local'});expect(input.resources[0]).not.toHaveProperty('revision');expect(input.resources[0].resource).not.toHaveProperty('unexpected');expect(m.saveReleaseGrant.mock.calls[0][2]).toBe('old-id');expect(m.saveReleaseGrant.mock.calls[0][3]).toBe(7)
 await button(w,'kubernetes.addGrant').trigger('click');expect((w.get('#kr-grant-project').element as HTMLSelectElement).value).toBe('');expect(w.find('#kr-scope-0-name').exists()).toBe(false)
 await button(w,'kubernetes.release.addResource').trigger('click');await w.get('#kr-grant-project').setValue('project');await w.get('#kr-grant-env').setValue('dev');await w.get('#kr-scope-0-name').setValue('new');await w.findAll('form').at(-1)!.trigger('submit');await flushPromises();expect(m.saveReleaseGrant.mock.calls[1][2]).toBeUndefined();expect(m.saveReleaseGrant.mock.calls[1][1]).not.toHaveProperty('id')
 })
 it('retains invalid draft by check identity across another row edit and earlier-row removal',async()=>{
 const job:any={id:'node',name:'Kubernetes',type:'deploy_kubernetes',summary:'',config:{}};const stage:any={id:'deploy',name:'Deploy',kind:'deploy',jobs:[job]};const config={...newReleaseConfig(),checks:JSON.stringify([{kind:'tcp',name:'earlier',target:'service:80'},{kind:'tcp',name:'retained',target:'service:81'}]),privateRef:'kcfg_original'};
 const w=mount(Editor,{props:{config,stages:[stage],stage,job,projectId:'project'}});wrappers.push(w);await flushPromises();
 const textareas=()=>w.findAll('textarea').filter(t=>t.attributes('aria-label')===tr('kubernetes.release.health'));
 await textareas()[1].setValue('null');await w.get('#kr-check-0-target').setValue('service:82');let next=w.emitted('update')!.at(-1)![0] as any;await w.setProps({config:next});expect((textareas()[1].element as HTMLTextAreaElement).value).toBe('null');expect(w.text()).toContain(tr('kubernetes.release.invalidJSON'));
 const holder=textareas()[0].element.closest('.kube-surface')!;const remove=Array.from(holder.querySelectorAll('button')).find(b=>b.textContent?.trim()===tr('kubernetes.remove'))!;remove.click();await flushPromises();next=w.emitted('update')!.at(-1)![0] as any;await w.setProps({config:next});expect(textareas()).toHaveLength(1);expect((textareas()[0].element as HTMLTextAreaElement).value).toBe('null');expect(w.text()).toContain(tr('kubernetes.release.invalidJSON'));
 await textareas()[0].setValue('{"kind":"tcp","name":"retained","target":"service:83"}');next=w.emitted('update')!.at(-1)![0] as any;await w.setProps({config:next});expect(w.text()).not.toContain(tr('kubernetes.release.invalidJSON'));expect(next.privateRef).toBe('kcfg_original');
 await textareas()[0].setValue('[]');await w.setProps({projectId:'other-project'});expect((textareas()[0].element as HTMLTextAreaElement).value).not.toBe('[]');expect(w.text()).not.toContain(tr('kubernetes.release.invalidJSON'));
 })
 for(const text of ['null','[]','"scalar"','1','true','{"kind":"tcp","target":[]}','{"kind":"dns","server":null}','{"expected":{}}','{"labels":{"a":1}}','{"waitSeconds":1.5}','{"serviceAccount":false}','{"positiveControl":[]}','{"imagePullSecrets":[2]}','{"resource":[]}','{"unknown":1}'])it('keeps invalid editable check draft '+text,async()=>{
 const job:any={id:'node',name:'Kubernetes',type:'deploy_kubernetes',summary:'',config:{}};const stage:any={id:'deploy',name:'Deploy',kind:'deploy',jobs:[job]};const config={...newReleaseConfig(),checks:'[{"kind":"tcp","name":"check","target":"service:80"}]',privateRef:'kcfg_original'}
 const w=mount(Editor,{props:{config,stages:[stage],stage,job,projectId:'project'}});wrappers.push(w);await flushPromises();const textarea=w.findAll('textarea').find(t=>t.attributes('aria-label')===tr('kubernetes.release.health'))!;await textarea.setValue(text);await flushPromises();expect(w.emitted('update')).toBeUndefined();expect(w.text()).toContain(tr('kubernetes.release.invalidJSON'));expect((textarea.element as HTMLTextAreaElement).value).toBe(text)
 await textarea.setValue('{"kind":"tcp","name":"check","target":"service:81","labels":{"safe":"yes"},"waitSeconds":0}');const next=w.emitted('update')!.at(-1)![0] as any;expect(next.privateRef).toBe('kcfg_original');expect(JSON.parse(next.checks)[0].target).toBe('service:81')
 })
})
