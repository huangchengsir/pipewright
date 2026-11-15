import {defineStore} from 'pinia'
import {ref,watch,computed} from 'vue'
import {access,type Team} from '../api/access'
import {useSessionStore} from './session'
import {usePermissionStore} from './permissions'

// Menu membership is distinct from button/data authorization. Unknown routes
// fail closed; all underlying services independently check actual resources.
export function routePages(path:string):string[]|null {
 path=path.replace(/\/+$/," ").trim().toLowerCase()||"/"
 if(path==='/settings/account'||path==='/settings')return []
 const settings:Record<string,string[]>={servers:['page.servers'],kubernetes:['page.kubernetes'],vault:['page.vault'],access:['page.roles','page.members'],mcp:['page.mcp'],'diagnosis-stats':['page.runs'],database:['page.database'],ai:['page.system'],oauth:['page.system'],notifications:['page.system'],'dns-providers':['page.system'],system:['page.system']}
 if(path.startsWith('/settings/'))return settings[path.slice(10)]??null
 const exact:Record<string,string[]>={'/dashboard':['page.dashboard'],'/projects':['page.projects'],'/runs':['page.runs'],'/library':['page.library'],'/metrics/dora':['page.dora'],'/environments':['page.environments'],'/servers':['page.servers'],'/server-status':['page.servers'],'/containers':['page.containers'],'/proxy':['page.certificates'],'/previews':['page.previews'],'/anomaly':['page.anomalies'],'/notifications':['page.system'],'/onboarding':['page.system'],'/':['page.dashboard'],'/mcp/config-maintenance':['page.mcp']}
 if(exact[path])return exact[path]!
 if(/^\/projects\/[^/]+\/(pipeline|triggers)$/.test(path))return ['page.pipeline']
 if(/^\/projects\/[^/]+\/code$/.test(path))return ['page.projects']
 if(/^\/projects\/[^/]+\/release-guards\/[^/]+$/.test(path))return ['page.guard']
 if(/^\/runs\/[^/]+$/.test(path))return ['page.runs']
 if(/^\/library\/studio(?:\/[^/]+)?$/.test(path))return ['page.library']
 if(/^\/servers\/[^/]+\/terminal$/.test(path))return ['page.servers']
 if(/^\/mcp\/config-drafts\/[^/]+\/review$/.test(path))return ['page.mcp']
 return null
}
export const useNavigationStore=defineStore('navigation',()=>{
 const session=useSessionStore(),permissions=usePermissionStore()
 const teams=ref<Team[]>([]),pages=ref<string[]>([]),ready=ref(false),expires=ref(0),clock=ref(Date.now())
 let generation=0,inflight:Promise<void>|undefined
 let version:{team:string;epoch:number;revision:number}|undefined
 const identity=computed(()=>JSON.stringify([session.user?.userId,session.user?.username,session.user?.multiTenant,session.user?.mustChangePassword,session.isNetworkError]))
 function clear(){generation++;teams.value=[];pages.value=[];ready.value=false;expires.value=0;inflight=undefined;version=undefined}
 watch(identity,()=>{clear();permissions.setTeam('')},{flush:'sync'})
 watch(()=>permissions.activeTeam,()=>{generation++;pages.value=[];ready.value=false;expires.value=0;inflight=undefined},{flush:'sync'})
 async function refresh(force=false){
  clock.value=Date.now()
  if(session.user?.multiTenant!==true||session.user.mustChangePassword||session.isNetworkError)return
  if(!force&&ready.value&&expires.value>clock.value+2000)return
  if(inflight)return inflight
  const user=session.user.userId,identityAt=identity.value
  const work=async()=>{
   let ticket=generation
   try{
    const choices=await access.teams();if(ticket!==generation||identityAt!==identity.value)return
    teams.value=choices
    if(!choices.some(t=>t.id===permissions.activeTeam)){permissions.setTeam(choices[0]?.id??'');ticket=generation}
    const team=permissions.activeTeam
    if(!team){ready.value=true;expires.value=Date.now()+5000;return}
    const result=await access.navigation(team)
    if(ticket!==generation||identityAt!==identity.value||team!==permissions.activeTeam)return
    if(result.userId!==user||result.teamId!==team||result.catalogVersion!==1||!result.modeEpoch||!result.revision||!Array.isArray(result.pages)||result.pages.some(p=>typeof p!=='string'))throw Error('Invalid navigation response')
    if(version?.team===team&&(result.modeEpoch<version.epoch||(result.modeEpoch===version.epoch&&result.revision<version.revision)))throw Error('Stale navigation response')
    version={team,epoch:result.modeEpoch,revision:result.revision}
    pages.value=result.pages;ready.value=true;expires.value=Date.now()+5000
   }catch{if(ticket===generation){pages.value=[];ready.value=false;expires.value=0}}
  }
  const current=work();inflight=current;try{await current}finally{if(inflight===current)inflight=undefined}
 }
 function allows(path:string){
  path=path.replace(/\/+$/," ").trim().toLowerCase()||"/"
  const user=session.user
  if(!user||session.isNetworkError)return false
  if(path==='/settings/account'||path==='/settings')return true
  if(user.mustChangePassword)return false
  if(user.multiTenant===false)return true
  if(user.multiTenant!==true||!ready.value||expires.value<=clock.value)return false
  const required=routePages(path);return required!==null&&required.some(p=>pages.value.includes(p))
 }
 function tick(){clock.value=Date.now();void refresh()}
 return{teams,pages,ready,refresh,allows,tick}
})
