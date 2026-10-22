import { inject, onUnmounted, provide, ref, shallowRef, watch, type InjectionKey, type Ref } from 'vue'
import { configRevisionOf } from '../api/configRevision'

interface RevisionParticipant {
  projectId: () => string
  read: () => string | undefined
  advance: (from: string, to: string) => void
  ready: () => boolean
  reload?: () => Promise<void>
}
const key: InjectionKey<ReturnType<typeof createCoordinator>> = Symbol('project-configuration-revisions')

function createCoordinator(projectId: () => string) {
  // Only an acknowledged local commit can advance another form's baseline.
  // A fresh GET from one form must never rebase another form's editing buffer.
  const participants = shallowRef<Array<RevisionParticipant & { initialRead: Ref<boolean>; initialProject?: string; readTicket?: symbol }>>([])
  const aligning = ref(false)
  let pristine = () => false
  let windowOpen = true
  let attempts = 0
  let epoch = 0

  function commit(id: string, previous: string | undefined, value: unknown): void {
    const next = typeof value === 'string' && /^cfg-[0-9]+$/.test(value) ? value : configRevisionOf(value)
    if (id !== projectId() || !previous || !next) return
    windowOpen = false
    for (const participant of participants.value) {
      if (participant.projectId() === id && participant.ready() && participant.read() === previous) participant.advance(previous, next)
    }
  }
  function register(input: RevisionParticipant) {
    const item: RevisionParticipant & { initialRead: Ref<boolean>; initialProject?: string; readTicket?: symbol } = { ...input, initialRead: ref(false) }
    participants.value = [...participants.value, item]
    onUnmounted(() => { participants.value = participants.value.filter(value => value !== item) })
    return {
      initialRead: item.initialRead,
      isInitialRead: (id: string) => item.initialRead.value && item.initialProject === id,
      canAcceptInitialRead: () => windowOpen && pristine(),
      committed: (previous: string | undefined, result: unknown, id = input.projectId()) => commit(id, previous, result),
    }
  }
  function synchronizeInitial(isPristine: () => boolean): void {
    pristine = isPristine
    let currentProject = projectId()
    async function reconcile(): Promise<void> {
      if (aligning.value || !windowOpen) return
      if (!pristine()) { windowOpen = false; return }
      const id = projectId()
      const entries = participants.value.filter(item => item.projectId() === id)
      if (!entries.length || entries.some(item => !item.ready())) return
      const versions = entries.map(item => item.read())
      if (versions.every(value => !value) || (versions.every(Boolean) && new Set(versions).size === 1)) { windowOpen = false; return }
      if (attempts >= 2) { windowOpen = false; return }
      // Legacy GETs can create defaults and increment the shared revision. Re-read
      // only during this initial pristine window, and cap retries under contention.
      const ticket = epoch
      attempts++
      aligning.value = true
      try {
        await Promise.all(entries.map(async item => {
          if (!item.reload) return
          const readTicket = Symbol()
          item.readTicket = readTicket
          item.initialProject = id
          item.initialRead.value = true
          try { await item.reload() } finally { if (item.readTicket === readTicket) item.initialRead.value = false }
        }))
      } finally {
        if (ticket === epoch && id === projectId()) { aligning.value = false; void reconcile() }
      }
    }
    watch(() => [projectId(), ...participants.value.flatMap(item => [item.projectId(), item.ready(), item.read()])], () => {
      if (projectId() !== currentProject) { currentProject = projectId(); epoch++; attempts = 0; windowOpen = true; aligning.value = false }
      void reconcile()
    }, { immediate: true })
  }
  return { register, commit, synchronizeInitial, aligning }
}

export function provideProjectConfigRevisions(projectId: () => string) {
  const coordinator = createCoordinator(projectId)
  provide(key, coordinator)
  return coordinator
}

export function useProjectConfigRevisionParticipant(input: RevisionParticipant) {
  const coordinator = inject(key, null)
  return coordinator?.register(input) ?? { initialRead: ref(false), isInitialRead: () => false, canAcceptInitialRead: () => false, committed: () => undefined }
}

export function useProjectConfigCommit() {
  const coordinator = inject(key, null)
  return (projectId: string, previous: string | undefined, result: unknown) => coordinator?.commit(projectId, previous, result)
}
