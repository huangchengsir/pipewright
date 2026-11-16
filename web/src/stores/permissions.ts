import { defineStore } from 'pinia'
import { computed, reactive, ref, watch } from 'vue'
import { access, type PermissionQuery } from '../api/access'
import { permissionActions } from '../api/permissionCatalog'
import { useSessionStore } from './session'

const MAX_ACTIVE = 512
const TTL = 5_000
export const permissionKey = (team: string, query: PermissionQuery) => JSON.stringify([team, query.action, query.resourceId, query.environmentId ?? '', !!query.create])
interface Entry { team: string; query: PermissionQuery; allowed: boolean; pending: boolean; expires: number; refs: number }

// UI decisions are hints with a short lifetime, never bearer capabilities.
// All network results are bound to identity, team, mode epoch and revision.
export const usePermissionStore = defineStore('permissions', () => {
 const session = useSessionStore()
 const activeTeam = ref('')
 const capacityVersion = ref(0)
 const entries = reactive(new Map<string, Entry>())
 const clock = ref(Date.now())
 const versions = new Map<string, { epoch: number; revision: number; catalog: number }>()
 const rejected = reactive(new Map<string,{epoch:number;revision:number;catalog:number}|null>())
 let generation = 0
 let queued = false
 let timer: ReturnType<typeof setInterval> | undefined
 const identity = computed(() => JSON.stringify([session.user?.userId ?? session.user?.username, session.user?.multiTenant, session.user?.mustChangePassword, session.isNetworkError]))
 function invalidate() {
  generation++; versions.clear(); rejected.clear()
  for (const entry of entries.values()) { entry.allowed = false; entry.pending = false; entry.expires = 0 }
  schedule()
 }
 watch([identity, () => session.user], invalidate, { flush: 'sync' })
 function setTeam(team: string) { if (activeTeam.value !== team) { activeTeam.value = team; invalidate() } }
 function schedule() { if (!queued) { queued = true; queueMicrotask(() => { queued = false; void refresh() }) } }
 async function refresh() {
  clock.value = Date.now()
  const user = session.user
  if (!user || user.mustChangePassword || session.isNetworkError || user.multiTenant !== true) return
  const current = generation
  const groups = new Map<string, [string, Entry][]>()
  for (const pair of entries) {
   const entry = pair[1]
   if (entry.refs < 1 || entry.pending || entry.expires > clock.value + 2_000 || !entry.team || (activeTeam.value !== "" && entry.team !== activeTeam.value) || !permissionActions.has(entry.query.action)) continue
   const group = groups.get(entry.team) ?? []; group.push(pair); groups.set(entry.team, group)
  }
  for (const [team, group] of groups) for (let offset = 0; offset < group.length; offset += 64) {
   const batch = group.slice(offset, offset + 64)
   batch.forEach(([, entry]) => { if (entry.expires <= clock.value) entry.allowed = false; entry.pending = true })
   try {
    const response = await access.decisions(team, batch.map(([, entry]) => entry.query))
    if (current !== generation) continue
    if (response.userId !== user.userId || response.teamId !== team || response.allowed.length !== batch.length || response.catalogVersion !== 1 || !response.modeEpoch || !response.revision || response.allowed.some(value => typeof value !== 'boolean')) throw new Error('Invalid permission response')
    const version = { epoch: response.modeEpoch, revision: response.revision, catalog: response.catalogVersion }
    const previous = versions.get(team)
    if (previous && (version.epoch < previous.epoch || (version.epoch === previous.epoch && version.revision < previous.revision))) throw new Error('Stale permission response')
    if (previous && (previous.epoch !== version.epoch || previous.revision !== version.revision || previous.catalog !== version.catalog)) {
     // Old decisions from another revision must not coexist with this batch.
     for (const entry of entries.values()) if (entry.team === team) { entry.allowed = false; entry.expires = 0 }
    }
    for (const [key, rejectedVersion] of rejected) {
     if (rejectedVersion && entries.get(key)?.team === team && (rejectedVersion.epoch !== version.epoch || rejectedVersion.revision !== version.revision || rejectedVersion.catalog !== version.catalog)) rejected.delete(key)
    }
    versions.set(team, version)
    batch.forEach(([, entry], index) => { entry.allowed = response.allowed[index] === true; entry.expires = Date.now() + TTL })
   } catch {
    if (current === generation) batch.forEach(([, entry]) => { entry.allowed = false; entry.expires = Date.now() + TTL })
   } finally {
    if (current === generation) batch.forEach(([, entry]) => { entry.pending = false })
   }
  }
 }
 function retain(team: string, query: PermissionQuery): () => void {
  const key = permissionKey(team, query)
  let admitted = false
  let stopWaiting: (() => void) | undefined
  function admit(): boolean {
   if (admitted) return true
   let entry = entries.get(key)
   if (!entry) {
    if (entries.size >= MAX_ACTIVE) return false
    entry = { team, query: { ...query }, allowed: false, pending: false, expires: 0, refs: 0 }; entries.set(key, entry)
   }
   entry.refs++; admitted = true
   if (!timer) timer = setInterval(() => { clock.value = Date.now(); schedule() }, 1_000)
   schedule(); return true
  }
  // Waiting is owned by the mounted caller and disposed on unmount. The store
  // never grows its decision cache beyond MAX_ACTIVE; released slots wake it.
  if (!admit()) stopWaiting = watch(capacityVersion, () => { if (admit()) { stopWaiting?.(); stopWaiting = undefined } })
  return () => {
   stopWaiting?.(); stopWaiting = undefined
   if (admitted) { const current = entries.get(key); if (current && --current.refs < 1) { entries.delete(key); rejected.delete(key); capacityVersion.value++ } }
   if (!entries.size && timer) { clearInterval(timer); timer = undefined }
  }
 }
 function allowed(team: string, query: PermissionQuery): boolean {
  const user = session.user
  if (rejected.has(permissionKey(team,query))) return false
  if (!user || user.mustChangePassword || session.isNetworkError || !permissionActions.has(query.action)) return false
  if (user.multiTenant === false) return true
  if (user.multiTenant !== true || (activeTeam.value !== "" && team !== activeTeam.value)) return false
  const entry = entries.get(permissionKey(team, query))
  return !!entry && entry.allowed && entry.expires > clock.value
 }
 function pending(team: string, query: PermissionQuery): boolean {
  if (rejected.has(permissionKey(team,query))) return false
  const entry = entries.get(permissionKey(team, query))
  return !!session.user?.multiTenant && (!entry || entry.pending || entry.expires <= clock.value)
 }
 function reject(team:string,query:PermissionQuery) {
  const key=permissionKey(team,query)
  // The mounted gate owns a bounded entry. Retain denial until a changed policy
  // version, explicit refresh or navigation releases it; do not remount on a
  // contradictory "allowed" hint from the same policy version.
  if (!entries.has(key)) return
  rejected.set(key,versions.get(team) ? {...versions.get(team)!} : null)
  const entry=entries.get(key)!
  entry.allowed=false;entry.expires=0
  schedule()
 }
 return { activeTeam, setTeam, invalidate, reject, retain, allowed, pending }
})
