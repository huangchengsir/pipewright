import { computed, onMounted, onUnmounted, readonly, shallowRef } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate } from 'vue-router'

export interface PipelineDraftParticipant {
  readonly scopes?: readonly string[]
  readonly ready: boolean
  readonly dirty: boolean
  readonly saving: boolean
  readonly saveEvenIfClean?: boolean
  validate?: () => boolean
  save: () => Promise<boolean>
}

export type PipelineSaveState = 'saved' | 'dirty' | 'loading' | 'saving' | 'error' | 'partial' | 'unavailable'

export interface PipelineSaveOutcome {
  state: 'saved' | 'dirty' | 'error' | 'partial' | 'unavailable'
  completedScopes: string[]
  failedScopes: string[]
}

export function usePipelineDraftSave(options: {
  participants: () => Array<PipelineDraftParticipant | null>
  confirmDiscard: () => Promise<boolean>
}) {
  const saving = shallowRef(false)
  const outcome = shallowRef<PipelineSaveOutcome | null>(null)
  const dirty = computed(() => options.participants().some((part) => part?.dirty))
  const busy = computed(() => saving.value || options.participants().some((part) => part?.saving))
  const ready = computed(() => options.participants().every((part) => part?.ready))

  function resetOutcome(): void {
    outcome.value = null
  }

  async function save(): Promise<boolean> {
    if (busy.value) return false
    const parts = options.participants()
    if (parts.some((part) => !part?.ready)) {
      outcome.value = { state: 'unavailable', completedScopes: [], failedScopes: [] }
      return false
    }
    const readyParts = parts as PipelineDraftParticipant[]
    // Validate every scope before starting any non-transactional API writes.
    const invalid = readyParts.find((part) => part.validate && !part.validate())
    if (invalid) {
      outcome.value = { state: 'error', completedScopes: [], failedScopes: [...(invalid.scopes ?? [])] }
      return false
    }
    const pending = readyParts.filter((part) => part.dirty)
    pending.push(...readyParts.filter((part) => !part.dirty && part.saveEvenIfClean))
    saving.value = true
    outcome.value = null
    const completedScopes: string[] = []
    let completedCount = 0
    let currentScopes: string[] = []
    function recordFailure(): void {
      outcome.value = {
        state: completedCount ? 'partial' : 'error',
        completedScopes: [...completedScopes],
        failedScopes: currentScopes,
      }
    }
    try {
      for (const part of pending) {
        // Capture labels with the payload's scope, before acknowledgement clears dirty state.
        currentScopes = [...(part.scopes ?? [])]
        if (!await part.save()) {
          recordFailure()
          return false
        }
        completedCount += 1
        completedScopes.push(...currentScopes)
      }
      const saved = !dirty.value
      outcome.value = { state: saved ? 'saved' : 'dirty', completedScopes, failedScopes: [] }
      return saved
    } catch (err) {
      recordFailure()
      throw err
    } finally {
      saving.value = false
    }
  }

  async function canLeave(): Promise<boolean> {
    if (busy.value) return false
    if (!dirty.value) return true
    const discard = await options.confirmDiscard()
    return discard && !busy.value
  }

  onBeforeRouteLeave(canLeave)
  onBeforeRouteUpdate((to, from) => to.params.id === from.params.id || canLeave())

  function beforeUnload(event: BeforeUnloadEvent): void {
    if (!dirty.value && !busy.value) return
    event.preventDefault()
    event.returnValue = ''
  }
  onMounted(() => window.addEventListener('beforeunload', beforeUnload))
  onUnmounted(() => window.removeEventListener('beforeunload', beforeUnload))

  return { dirty, busy, ready, outcome: readonly(outcome), resetOutcome, save }
}
