import { computed, onMounted, onUnmounted, shallowRef } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate } from 'vue-router'

export interface PipelineDraftParticipant {
  readonly ready: boolean
  readonly dirty: boolean
  readonly saving: boolean
  readonly saveEvenIfClean?: boolean
  validate?: () => boolean
  save: () => Promise<boolean>
}

export function usePipelineDraftSave(options: {
  participants: () => Array<PipelineDraftParticipant | null>
  confirmDiscard: () => Promise<boolean>
}) {
  const saving = shallowRef(false)
  const dirty = computed(() => options.participants().some((part) => part?.dirty))
  const busy = computed(() => saving.value || options.participants().some((part) => part?.saving))

  async function save(): Promise<boolean> {
    if (busy.value) return false
    const parts = options.participants()
    if (parts.some((part) => !part?.ready)) return false
    const readyParts = parts as PipelineDraftParticipant[]
    // Validate every scope before starting any non-transactional API writes.
    if (readyParts.some((part) => part.validate && !part.validate())) return false
    const pending = readyParts.filter((part) => part.dirty)
    pending.push(...readyParts.filter((part) => !part.dirty && part.saveEvenIfClean))
    saving.value = true
    try {
      for (const part of pending) {
        if (!await part.save()) return false
      }
      return !dirty.value
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

  return { dirty, busy, save }
}
