import {beforeEach,afterEach,it,expect,vi} from 'vitest'
import {mount,flushPromises,type VueWrapper} from '@vue/test-utils'
import {createPinia,setActivePinia,getActivePinia} from 'pinia'
import {useSessionStore} from '../../stores/session'
import {usePermissionStore} from '../../stores/permissions'
import Account from './SettingsAccount.vue'
import {PLATFORM_PROVIDERS,type PlatformSnapshot} from '../../api/platformLogin'
const m=vi.hoisted(()=>({settings:vi.fn(),sessions:vi.fn(),replace:vi.fn(),push:vi.fn()}))
vi.mock('vue-router',()=>({useRouter:()=>({replace:m.replace,push:m.push}),useRoute:()=>({path:'/settings/account',query:{}})}))
vi.mock('../../api/access',()=>({access:{sessions:m.sessions}}))
vi.mock('../../api/account',()=>({listSessions:m.sessions}))
vi.mock('../../api/platformLogin',async()=>({...await vi.importActual('../../api/platformLogin'),getPlatformSettings:m.settings}))
let wrapper:VueWrapper|undefined
function data(label:string):PlatformSnapshot{return {canManage:false,visibilityVersion:1,reauthenticatedUntil:'',providers:PLATFORM_PROVIDERS.map(provider=>({provider,visible:true,enabled:true,configured:true,hasSecret:true,clientId:'',microsoftTenant:'common',giteeMode:'',callbackURL:'',configVersion:1,binding:provider==='github'?{issuer:'https://github.com',subject:label,displayLabel:label,boundAt:'2026-11-13T00:00:00Z'}:undefined}))}}
async function settle(){for(let n=0;n<4;n++)await flushPromises()}
beforeEach(()=>{Object.defineProperty(HTMLDialogElement.prototype,'close',{configurable:true,value(){this.removeAttribute('open')}});Object.defineProperty(HTMLDialogElement.prototype,'showModal',{configurable:true,value(){this.setAttribute('open','')}});setActivePinia(createPinia());useSessionStore().setUser({username:'alice',userId:'alice',multiTenant:true,instanceAdmin:false});usePermissionStore().setTeam('team-a');vi.clearAllMocks();m.sessions.mockResolvedValue([]);m.settings.mockImplementation(async(_signal,options)=>data(useSessionStore().user!.username+'/'+options.headers['X-Pipewright-Team']))})
afterEach(()=>{wrapper?.unmount();wrapper=undefined})
it.each(['team','employee','network'] as const)('recreates usable own platform section after %s changes',async(kind)=>{
 wrapper=mount(Account,{global:{plugins:[getActivePinia()!]}});await settle();expect(wrapper.text()).toContain('alice/team-a');
 if(kind==='team')usePermissionStore().setTeam('team-b');else if(kind==='employee')useSessionStore().setUser({username:'bob',userId:'bob',multiTenant:true,instanceAdmin:false});else{useSessionStore().isNetworkError=true;await settle();useSessionStore().isNetworkError=false}
 await settle();expect(m.settings).toHaveBeenCalledTimes(2);expect(wrapper.findAll('.platform-row')).toHaveLength(5);expect(wrapper.text()).toContain(kind==='team'?'alice/team-b':kind==='employee'?'bob/team-a':'alice/team-a')
})
it('new team loads while old in-flight settings cannot overwrite its own binding',async()=>{
 let finish!:(v:PlatformSnapshot)=>void;m.settings.mockImplementationOnce(()=>new Promise<PlatformSnapshot>(r=>finish=r));wrapper=mount(Account,{global:{plugins:[getActivePinia()!]}});await settle();usePermissionStore().setTeam('team-b');await settle();expect(wrapper.text()).toContain('alice/team-b');finish(data('OLD PRIVATE BINDING'));await settle();expect(wrapper.text()).not.toContain('OLD PRIVATE BINDING');expect(wrapper.findAll('.platform-row')).toHaveLength(5)
})
