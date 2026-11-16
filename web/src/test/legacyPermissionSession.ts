import {createPinia,setActivePinia} from 'pinia'
import {useSessionStore} from '../stores/session'
export function legacyPermissionSession(){setActivePinia(createPinia());useSessionStore().setUser({userId:'legacy',username:'admin',multiTenant:false})}
