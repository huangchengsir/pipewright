import {beforeEach,expect,it,vi} from 'vitest'
import type {HttpOptions} from './http'
import {PLATFORM_PROVIDERS,getPlatformSettings,savePlatformVisibility,savePlatformApp,reauthenticatePlatform,startPlatformBinding,getPlatformCandidate,confirmPlatformBinding,setPlatformEnabled,unbindPlatform} from './platformLogin'
const calls=vi.hoisted(()=>({get:vi.fn(),put:vi.fn(),post:vi.fn(),delete:vi.fn()}))
vi.mock('./http',async()=>({...await vi.importActual('./http'),http:calls}))
beforeEach(()=>{for(const fn of Object.values(calls)){fn.mockReset();fn.mockResolvedValue({})}})
const cases:[string,(context:HttpOptions)=>Promise<unknown>][]=[
 ['settings',c=>getPlatformSettings(undefined,c)],
 ['visibility',c=>savePlatformVisibility(1,{github:true,gitlab:true,gitee:true,google:true,microsoft:true},c)],
 ['app',c=>savePlatformApp('github',{expectedVersion:1,clientId:'app'},c)],
 ['reauth',c=>reauthenticatePlatform('synthetic-password',c)],
 ['binding-start',c=>startPlatformBinding('github',1,c)],
 ['candidate',c=>getPlatformCandidate('flow',c)],
 ['confirm-binding',c=>confirmPlatformBinding('github',1,'flow',c)],
 ['enabled',c=>setPlatformEnabled('github',1,false,c)],
 ['unbind',c=>unbindPlatform('github',1,c)],
]
it.each(cases)('%s preserves fixed team and unauthorized handling',async(_name,invoke)=>{
 const controller=new AbortController();await invoke({signal:controller.signal,headers:{'X-Pipewright-Team':'team-a'}});const fn=Object.values(calls).find(f=>f.mock.calls.length)!;const args=fn.mock.calls[0];expect(args.at(-1)).toMatchObject({headers:{'X-Pipewright-Team':'team-a'},signal:expect.any(AbortSignal),throwOnUnauthorized:true});expect(fn).toHaveBeenCalledTimes(1)
})
it.each(cases)('%s sends nothing when its employee scope is already aborted',async(_name,invoke)=>{
 const controller=new AbortController();controller.abort();await expect(invoke({signal:controller.signal})).rejects.toMatchObject({name:'AbortError'});for(const fn of Object.values(calls))expect(fn).not.toHaveBeenCalled()
})
it.each(cases)('%s propagates identity cancellation to the bounded request',async(_name,invoke)=>{
 for(const fn of Object.values(calls))fn.mockImplementation((...args:unknown[])=>new Promise((_resolve,reject)=>{const options=args.at(-1) as HttpOptions;options.signal!.addEventListener('abort',()=>reject(new DOMException('Request cancelled','AbortError')))}))
 const controller=new AbortController();const pending=invoke({signal:controller.signal});const check=expect(pending).rejects.toMatchObject({name:'AbortError'});controller.abort();await check
})
it.each(PLATFORM_PROVIDERS)('%s binding endpoints retain current team through start, candidate and confirm',async(provider)=>{
 const context={headers:{'X-Pipewright-Team':'team-a'}};await startPlatformBinding(provider,7,context);await confirmPlatformBinding(provider,7,'candidate',context);expect(calls.post.mock.calls.map(c=>c[0])).toEqual([`/api/account/platform-login/${provider}/bind/start`,`/api/account/platform-login/${provider}/bind/confirm`]);expect(calls.post.mock.calls.every(c=>c[2].headers['X-Pipewright-Team']==='team-a')).toBe(true)
})
it.each(['load','identity'])('settings cancels on %s signal independently',async(source)=>{
 const load=new AbortController(),identity=new AbortController();calls.get.mockImplementation((_path,options)=>new Promise((_resolve,reject)=>options.signal.addEventListener('abort',()=>reject(new DOMException('cancelled','AbortError')))));const pending=getPlatformSettings(load.signal,{signal:identity.signal});const check=expect(pending).rejects.toMatchObject({name:'AbortError'});(source==='load'?load:identity).abort();await check
})
