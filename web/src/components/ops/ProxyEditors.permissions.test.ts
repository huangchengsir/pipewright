import {beforeEach,afterEach,it,expect,vi} from 'vitest'
import {mount,flushPromises,type VueWrapper} from '@vue/test-utils'
import {createPinia,setActivePinia} from 'pinia'
import Instant from './InstantSubdomain.vue'
import Advanced from './RouteAdvancedSettings.vue'
import {useSessionStore} from '../../stores/session'
import {usePermissionStore} from '../../stores/permissions'
const api=vi.hoisted(()=>({list:vi.fn(),allocate:vi.fn(),update:vi.fn(),decisions:vi.fn(),success:vi.fn(),error:vi.fn()}))
vi.mock('../../api/dnsProviders',()=>({listUsableDnsProviders:api.list}))
vi.mock('../../api/reverseProxy',()=>({allocateSubdomain:api.allocate,updateProxyRoute:api.update}))
vi.mock('../../api/access',()=>({access:{decisions:api.decisions}}))
vi.mock('../../composables/useToast',()=>({useToast:()=>({success:api.success,error:api.error})}))
let wrapper:VueWrapper|undefined
const route={id:'route',serverId:'server',domain:'site.example.invalid',upstreamContainer:'web',upstreamPort:80,tlsMode:'auto' as const,enabled:true,certStatus:'issued' as const,certDetail:'',config:{aliases:[],forceHttps:true,hsts:false,securityHeaders:false,compression:false,basicAuthUser:'',basicAuthEnabled:false,ipAllow:[],ipDeny:[],redirects:[]},createdAt:'',updatedAt:''}
beforeEach(()=>{vi.resetAllMocks();setActivePinia(createPinia());useSessionStore().setUser({username:'admin',multiTenant:false});api.list.mockResolvedValue([{id:'dns',name:'Available',type:'cloudflare',baseDomain:'example.invalid'}]);api.allocate.mockResolvedValue(route);api.update.mockResolvedValue(route)})
afterEach(()=>{wrapper?.unmount();wrapper=undefined})
function instant(){wrapper=mount(Instant,{props:{serverId:'server',containers:[]},global:{stubs:{RouterLink:true,Teleport:true}}});return wrapper.vm as unknown as {openModal:()=>void;providerId:string;upstreamContainer:string;upstreamPort:string;allocate:()=>Promise<void>;result:unknown;open:boolean}}
function advanced(){wrapper=mount(Advanced,{props:{route}});return wrapper.vm as unknown as {basicAuthUser:string;basicAuthPassword:string;save:()=>Promise<void>;open:boolean}}
it('allocates only the selected available DNS provider using captured request options',async()=>{const p=instant();await flushPromises();p.openModal();p.upstreamContainer='web';p.upstreamPort='80';await p.allocate();expect(api.allocate).toHaveBeenCalledWith({providerId:'dns',serverId:'server',upstreamContainer:'web',upstreamPort:80},expect.objectContaining({throwOnUnauthorized:true}));expect(wrapper!.emitted('allocated')).toHaveLength(1)})
it('drops DNS allocation result and closes modal when identity changes',async()=>{const p=instant();await flushPromises();p.openModal();p.upstreamContainer='web';p.upstreamPort='80';let finish!:(v:unknown)=>void;api.allocate.mockImplementationOnce(()=>new Promise(r=>{finish=r}));const work=p.allocate();useSessionStore().clearSession();finish(route);await work;expect(p.result).toBeNull();expect(p.open).toBe(false);expect(wrapper!.emitted('allocated')).toBeUndefined();expect(api.success).not.toHaveBeenCalled()})
it('saves advanced settings without returning or preserving a typed password after logout',async()=>{const p=advanced();await flushPromises();p.basicAuthUser='reader';p.basicAuthPassword='synthetic-form-password';let finish!:(v:unknown)=>void;api.update.mockImplementationOnce(()=>new Promise(r=>{finish=r}));const work=p.save();expect(api.update).toHaveBeenCalledTimes(1);useSessionStore().clearSession();expect(p.basicAuthPassword).toBe('');finish(route);await work;expect(wrapper!.emitted('saved')).toBeUndefined();expect(api.success).not.toHaveBeenCalled()})
it('does not send editor mutations while the common gate is unknown or denied',async()=>{useSessionStore().setUser({username:'worker',userId:'worker',multiTenant:true});usePermissionStore().setTeam('a');api.decisions.mockImplementation(async(team:string,qs:unknown[])=>({userId:'worker',teamId:team,modeEpoch:1,revision:1,catalogVersion:1,allowed:qs.map(()=>false)}));const p=advanced();await flushPromises();p.basicAuthUser='reader';p.basicAuthPassword='synthetic';await p.save();expect(api.update).not.toHaveBeenCalled();expect(api.list).not.toHaveBeenCalled();expect(wrapper!.find('.advsave').exists()).toBe(false)})

it('clicks advanced save only with the exact current server decision', async () => {
 useSessionStore().setUser({username:'worker',userId:'worker',multiTenant:true});
 usePermissionStore().setTeam('a');
 api.decisions.mockImplementation(async(team,queries)=>({userId:'worker',teamId:team,modeEpoch:1,revision:1,catalogVersion:1,allowed:queries.map((q:{action:string,resourceId:string})=>q.action==='proxy.manage'&&q.resourceId==='server')}));
 const p=advanced();
 await flushPromises();
 p.basicAuthUser='reader';
 p.basicAuthPassword='fixture-password';
 await flushPromises();
 await wrapper!.get('.adv__toggle').trigger('click');
 await flushPromises();
 const button=wrapper!.get('.advsave');
 await button.trigger('click');
 await flushPromises();
 expect(api.update).toHaveBeenCalledTimes(1);
 expect(api.update.mock.calls[0]!.at(-1)).toMatchObject({headers:{'X-Pipewright-Team':'a'}});
 usePermissionStore().reject('a',{action:'proxy.manage',resourceId:'server'});
 button.element.dispatchEvent(new MouseEvent('click',{bubbles:true}));
 await flushPromises();
 expect(api.update).toHaveBeenCalledTimes(1);
});
