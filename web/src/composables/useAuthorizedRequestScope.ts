import {onBeforeUnmount,watch} from 'vue'
import {HttpError,type HttpOptions} from '../api/http'
import type {PermissionQuery} from '../api/access'
import {useSessionStore} from '../stores/session'
import {usePermissionStore} from '../stores/permissions'
import {usePermission} from './usePermission'

export interface AuthorizedRequestScope {
 active:()=>boolean
 run:<T>(work:()=>Promise<T>)=>Promise<T>
 requestOptions:HttpOptions
}
// UI lifetime protection only. Domain authorization remains the security boundary.
export function useAuthorizedRequestScope(query:PermissionQuery,clear:()=>void):AuthorizedRequestScope {
 const permission=usePermission(query),permissions=usePermissionStore()
 const team=permissions.activeTeam
 return useIdentityRequestScope(clear,()=>permission.allowed.value,()=>permissions.reject(team,query))
}

// Private account resources remain usable without global settings privileges.
export function useIdentityRequestScope(clear:()=>void,allowed:()=>boolean=()=>true,onDenied?:()=>void,isForbidden:(error:HttpError)=>boolean=()=>true):AuthorizedRequestScope {
 const session=useSessionStore(),permissions=usePermissionStore()
 const team=permissions.activeTeam,identity=JSON.stringify(session.user)
 const controller=new AbortController()
 let alive=true
 const active=()=>alive&&identity===JSON.stringify(session.user)&&team===permissions.activeTeam&&!session.isNetworkError&&!!session.user&&!session.user.mustChangePassword&&allowed()
 function dispose(){if(!alive)return;alive=false;controller.abort();clear()}
 watch(()=>[JSON.stringify(session.user),session.isNetworkError,permissions.activeTeam,allowed()],()=>{if(!active())dispose()},{flush:'sync'})
 onBeforeUnmount(dispose)
 const requestOptions:HttpOptions={signal:controller.signal,headers:session.user?.multiTenant===true?{'X-Pipewright-Team':team}:undefined,throwOnUnauthorized:true}
 async function run<T>(work:()=>Promise<T>):Promise<T>{
  if(!active())throw new Error('Authorization scope changed')
  try{const result=await work();if(!active())throw new Error('Authorization scope changed');return result}
  catch(error){
   if(error instanceof HttpError&&(error.status===401||(error.status===403&&isForbidden(error)))&&active()){
    dispose()
    if(error.status===401) session.clearSession()
    else onDenied?.()
   }
   throw error
  }
 }
 return {active,run,requestOptions}
}
