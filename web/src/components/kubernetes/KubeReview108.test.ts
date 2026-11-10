import {afterEach,beforeEach,describe,it,expect,vi} from 'vitest'
import {mount,flushPromises,type VueWrapper} from '@vue/test-utils'
import Settings from './KubeReleaseSettings.vue'
import Password from './KubePassword.vue'
import {i18n,setLocale,SUPPORTED_LOCALES} from '../../i18n'
const m=vi.hoisted(()=>({releaseGrants:vi.fn(),releaseProfile:vi.fn(),saveReleaseGrant:vi.fn(),reauth:vi.fn(),confirm:vi.fn()}))
vi.mock('../../api/kubernetes',async()=>({...await vi.importActual('../../api/kubernetes'),kubeAPI:m}))
vi.mock('../../composables/useConfirm',()=>({useConfirm:()=>({open:m.confirm})}))
const wrappers:VueWrapper[]=[]
const action='related-controllerrevisions'
const oldGrant:any={id:'grant',revision:2,connectionId:'connection',projectId:'project',environment:'dev',executor:{kind:'local'},resources:[{resource:{group:'apps',version:'v1',resource:'daemonsets',kind:'DaemonSet',namespace:'app',name:'agent'},actions:['get','create','related-pods'],containers:['app'],fields:[]}],enabled:true,requireApproval:true}
beforeEach(()=>{for(const f of Object.values(m))f.mockReset();m.releaseGrants.mockResolvedValue([structuredClone(oldGrant)]);m.releaseProfile.mockResolvedValue({profile:{runtime:{},helmVersion:'v4.3.1'}});m.saveReleaseGrant.mockResolvedValue({});m.reauth.mockResolvedValue({reauthenticatedUntil:new Date(Date.now()+300000).toISOString()});m.confirm.mockResolvedValue(true)})
afterEach(()=>{for(const w of wrappers.splice(0))w.unmount();setLocale('en')})
describe('review108 explicit DaemonSet related reads',()=>{
 for(const {code} of SUPPORTED_LOCALES)it(code+' exposes translated opt-in and never upgrades an old grant',async()=>{
 setLocale(code);const w=mount(Settings,{props:{connection:{id:'connection',executors:[{kind:'local'}]} as any,projects:[{id:'project',name:'Project'}] as any},global:{stubs:{KubePassword:true}}});wrappers.push(w);await flushPromises()
 const button=w.findAll('button').find(b=>b.text()===i18n.global.t('kubernetes.edit'))!;await button.trigger('click')
 const key='kubernetes.release.actions.'+action;expect(i18n.global.te(key)).toBe(true)
 const label=w.findAll('label.kube-check').find(x=>x.text()===i18n.global.t(key))!;expect(label).toBeDefined();expect((label.get('input').element as HTMLInputElement).checked).toBe(false)
 await label.get('input').setValue(true);await w.findAll('form').at(-1)!.trigger('submit');await flushPromises();const gate=w.getComponent(Password).props('state');gate.password.value='test-only';await gate.verify();await flushPromises()
 expect(m.saveReleaseGrant).toHaveBeenCalledTimes(1);const scope=m.saveReleaseGrant.mock.calls[0][1].resources[0];expect(scope.actions).toEqual([...oldGrant.resources[0].actions,action]);expect(scope.resource).toEqual(oldGrant.resources[0].resource);expect(oldGrant.resources[0].actions).not.toContain(action)
 })
})
