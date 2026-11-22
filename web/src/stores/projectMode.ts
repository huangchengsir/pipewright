import { defineStore } from 'pinia'
import { computed, onScopeDispose, ref, watch } from 'vue'
import { HttpError } from '../api/http'
import { getProjectMode, putProjectMode, type ProjectMode } from '../api/projectMode'
import { useSessionStore } from './session'

type LoadStatus = 'idle' | 'loading' | 'ready' | 'failed'
type SaveStatus = 'idle' | 'saving' | 'saved' | 'failed'

export const useProjectModeStore = defineStore('projectMode', () => {
  const session = useSessionStore()
  const identity = computed(() => {
    const user = session.user
    if (!user || user.mustChangePassword || session.isNetworkError) return null
    return JSON.stringify([user.multiTenant === true, user.userId ?? user.username])
  })
  const available = computed(() => identity.value !== null)
  const mode = ref<ProjectMode>('single')
  const loadStatus = ref<LoadStatus>('idle')
  const saveStatus = ref<SaveStatus>('idle')
  const loading = computed(() => loadStatus.value === 'loading')
  const loaded = computed(() => loadStatus.value === 'ready')
  let scope: AbortController | undefined

  function isCurrent(request: AbortController): boolean {
    return request === scope && !request.signal.aborted
  }

  function handleUnauthorized(error: unknown): void {
    if (error instanceof HttpError && error.status === 401) session.clearSession()
  }

  async function reload(): Promise<void> {
    const request = scope
    if (!request || loading.value || saveStatus.value === 'saving') return
    loadStatus.value = 'loading'
    saveStatus.value = 'idle'
    try {
      const saved = await getProjectMode({ signal: request.signal, throwOnUnauthorized: true })
      if (!isCurrent(request)) return
      mode.value = saved
      loadStatus.value = 'ready'
    } catch (error) {
      if (!isCurrent(request)) return
      loadStatus.value = 'failed'
      handleUnauthorized(error)
    }
  }

  async function save(next: ProjectMode): Promise<void> {
    const request = scope
    if (!request || !loaded.value || saveStatus.value === 'saving' || next === mode.value) return
    saveStatus.value = 'saving'
    try {
      const saved = await putProjectMode(next, { signal: request.signal, throwOnUnauthorized: true })
      if (!isCurrent(request)) return
      mode.value = saved
      saveStatus.value = 'saved'
    } catch (error) {
      if (!isCurrent(request)) return
      saveStatus.value = 'failed'
      handleUnauthorized(error)
    }
  }

  watch(identity, (current) => {
    scope?.abort()
    scope = current === null ? undefined : new AbortController()
    loadStatus.value = 'idle'
    saveStatus.value = 'idle'
    mode.value = 'single'
    if (scope) void reload()
  }, { immediate: true, flush: 'sync' })
  onScopeDispose(() => scope?.abort())

  return { available, mode, loadStatus, saveStatus, loading, loaded, reload, save }
})
