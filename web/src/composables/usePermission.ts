import { computed, onScopeDispose, toValue, watch, type MaybeRefOrGetter } from 'vue'
import { usePermissionStore, permissionKey } from '../stores/permissions'
import { useSessionStore } from '../stores/session'
import type { PermissionQuery } from '../api/access'

// Navigation, keyboard handlers and buttons share this exact decision path.
export function usePermission(query: MaybeRefOrGetter<PermissionQuery>, team?: MaybeRefOrGetter<string | undefined>) {
 const permissions = usePermissionStore()
 const session = useSessionStore()
 const resolvedTeam = () => team === undefined ? permissions.activeTeam : (toValue(team) ?? permissions.activeTeam)
 let release: (() => void) | undefined
 const stop = watch(() => JSON.stringify([session.user?.multiTenant,permissionKey(resolvedTeam(), toValue(query))]), (_value,_old,onCleanup) => {
  let current = true
  // Child setup can run inside its parent's render effect. Cache bookkeeping
  // must not subscribe that render to another control's reference count.
  queueMicrotask(() => { if (current && session.user?.multiTenant === true) release = permissions.retain(resolvedTeam(), toValue(query)) })
  onCleanup(() => { current = false; const previous = release; release = undefined; if(previous) queueMicrotask(previous) })
 }, { immediate: true })
 onScopeDispose(stop)
 const allowed = computed(() => permissions.allowed(resolvedTeam(), toValue(query)))
 const pending = computed(() => permissions.pending(resolvedTeam(), toValue(query)))
 return { allowed, pending, disabled: computed(() => !allowed.value), refresh: permissions.invalidate }
}

// A compound action (for example issuing one token for several projects) must
// satisfy every concrete resource decision. Entries still share bounded batches.
export function usePermissions(queries: MaybeRefOrGetter<readonly PermissionQuery[]>, team?: MaybeRefOrGetter<string | undefined>) {
 const permissions = usePermissionStore()
 const session = useSessionStore()
 const resolvedTeam = () => team === undefined ? permissions.activeTeam : (toValue(team) ?? permissions.activeTeam)
 const valid = computed(() => toValue(queries).length > 0 && toValue(queries).length <= 256)
 let releases: (() => void)[] = []
 const stop = watch(() => JSON.stringify([session.user?.multiTenant,resolvedTeam(), toValue(queries)]), (_value,_old,onCleanup) => {
  let current = true
  queueMicrotask(() => { if (current && session.user?.multiTenant === true && valid.value) releases = toValue(queries).map(query => permissions.retain(resolvedTeam(), query)) })
  onCleanup(() => { current = false; const previous = releases; releases = []; queueMicrotask(() => previous.forEach(release => release())) })
 }, { immediate: true })
 onScopeDispose(stop)
 const allowed = computed(() => valid.value && toValue(queries).every(query => permissions.allowed(resolvedTeam(), query)))
 const pending = computed(() => valid.value && toValue(queries).some(query => permissions.pending(resolvedTeam(), query)))
 return { allowed, pending, disabled: computed(() => !allowed.value), refresh: permissions.invalidate }
}
