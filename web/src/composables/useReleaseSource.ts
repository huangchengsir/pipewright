import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { getReleaseSourceSettings, setReleaseSourceSettings, type ReleaseSource, type ReleaseSourceSettings } from '../api/version'
import { HttpError } from '../api/http'

function valid(value: ReleaseSourceSettings): boolean {
  return !!value && ['github', 'gitee'].includes(value.source) && Number.isSafeInteger(value.revision) && value.revision >= 0 &&
    Array.isArray(value.sources) && ['github', 'gitee'].every(id => value.sources.filter(s => s.id === id).length === 1) &&
    value.sources.every(s => ['github', 'gitee'].includes(s.id) && typeof s.available === 'boolean' && typeof s.repository === 'string')
}

/** Instance settings only. Neither loading nor saving starts an external release check. */
export function useReleaseSource() {
  const settings = ref<ReleaseSourceSettings | null>(null)
  const draft = ref<ReleaseSource>('github')
  const loading = ref(true)
  const saving = ref(false)
  const saved = ref(false)
  const error = ref<'load' | 'save' | 'conflict' | ''>('')
  const needsReload = ref(false)
  let generation = 0
  let disposed = false
  let controller: AbortController | undefined
  const selected = computed(() => settings.value?.sources.find(s => s.id === draft.value))
  const dirty = computed(() => !!settings.value && draft.value !== settings.value.source)
  const canSave = computed(() => dirty.value && !!selected.value?.available && !loading.value && !saving.value && !needsReload.value)
  const canCheck = computed(() => !!settings.value && !dirty.value && !!selected.value?.available && !loading.value && !saving.value && !needsReload.value)

  async function load(): Promise<void> {
    if (saving.value) return
    const gen = ++generation
    controller?.abort()
    controller = new AbortController()
    loading.value = true
    saved.value = false
    error.value = ''
    try {
      const value = await getReleaseSourceSettings({ signal: controller.signal })
      if (disposed || gen !== generation) return
      if (!valid(value)) throw new Error('Invalid release source settings')
      settings.value = value
      draft.value = value.source
      needsReload.value = false
    } catch {
      if (!disposed && gen === generation) { error.value = 'load'; needsReload.value = true }
    } finally {
      if (!disposed && gen === generation) loading.value = false
    }
  }
  async function save(): Promise<boolean> {
    if (!canSave.value || !settings.value) return false
    const gen = ++generation
    const input = { source: draft.value, expectedRevision: settings.value.revision }
    saving.value = true
    saved.value = false
    error.value = ''
    try {
      const value = await setReleaseSourceSettings(input)
      if (disposed || gen !== generation) return false
      if (!valid(value) || value.source !== input.source || value.revision <= input.expectedRevision) throw new Error('Unconfirmed release source save')
      settings.value = value
      draft.value = value.source
      saved.value = true
      return true
    } catch (err) {
      if (!disposed && gen === generation) {
        error.value = err instanceof HttpError && err.status === 409 ? 'conflict' : 'save'
        // A transport failure may have committed. Read back before another write.
        needsReload.value = true
      }
      return false
    } finally {
      if (!disposed && gen === generation) saving.value = false
    }
  }
  onMounted(() => void load())
  onBeforeUnmount(() => { disposed = true; generation++; controller?.abort() })
  return { settings, draft, loading, saving, saved, error, needsReload, selected, dirty, canSave, canCheck, load, save }
}
