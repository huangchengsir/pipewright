import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { getCurrentUpdateTask, getUpdateTask, resumeUpdateTask, startUpdateTask, type UpgradeTask } from '../api/version'
import { HttpError } from '../api/http'

type OriginalAction = 'start' | 'resume'
interface Pending {
 requestId: string
 target: string
 taskId?: string
 resumeId?: string
 unconfirmedAction?: OriginalAction | null
}
const storageKey = 'pipewright.update.pending'
const opaqueID = /^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$/
const targetTag = /^v?\d+\.\d+\.\d+(?:-[a-zA-Z0-9.-]+)?$/
const resumable = ['interrupted', 'ready_blocked']
const replayBlocked = ['checking', 'downloading', 'verifying', 'extracting', 'installing', 'restarting', 'restart_required', 'unknown']

function requestId(): string {
 const bytes = new Uint8Array(16)
 crypto.getRandomValues(bytes)
 return Array.from(bytes, x => x.toString(16).padStart(2, '0')).join('')
}
function validID(value: unknown): value is string {
 return typeof value === 'string' && opaqueID.test(value)
}
export function useSelfUpdate() {
 const task = ref<UpgradeTask | null>(null)
 const connectionUnknown = ref(false)
 const submitting = ref(false)
 const submissionError = ref<{ status: number } | null>(null)
 const manual = ref<{ command?: string } | null>(null)
 const pending = ref<Pending | null>(null)
 let timer: ReturnType<typeof setTimeout> | undefined
 let controller: AbortController | undefined
 let generation = 0
 let disposed = false

 try {
  const saved = JSON.parse(sessionStorage.getItem(storageKey) || 'null')
  if (saved && validID(saved.requestId) && typeof saved.target === 'string' && targetTag.test(saved.target)) {
   const taskId = validID(saved.taskId) ? saved.taskId : undefined
   const resumeId = validID(saved.resumeId) ? saved.resumeId : undefined
   const action = saved.unconfirmedAction === 'start' || saved.unconfirmedAction === 'resume'
    ? saved.unconfirmedAction
    : saved.unconfirmedAction === null ? null : resumeId ? 'resume' : taskId ? null : 'start'
   pending.value = { requestId: saved.requestId, target: saved.target, taskId, resumeId, unconfirmedAction: action }
  }
 } catch { /* Server-side task state remains authoritative. */ }

 function remember() {
  try {
   if (pending.value) sessionStorage.setItem(storageKey, JSON.stringify(pending.value))
   else sessionStorage.removeItem(storageKey)
  } catch { /* Only opaque action identifiers are stored. */ }
 }
 function stop() {
  generation++
  if (timer) clearTimeout(timer)
  controller?.abort()
  controller = undefined
 }
 const active = computed(() => !!task.value && ['accepted', 'checking', 'downloading', 'verifying', 'extracting', 'installing', 'restarting'].includes(task.value.state))
 const busy = computed(() => submitting.value || active.value)
 const canStart = computed(() => !busy.value && !pending.value && (!task.value || ['completed', 'failed'].includes(task.value.state)))
 const canRecoverOriginal = computed(() => {
  const value = task.value
  const saved = pending.value
  if (value && replayBlocked.includes(value.state)) return false
  if (saved?.unconfirmedAction) {
   if (saved.unconfirmedAction === 'resume' && (!saved.taskId || !saved.resumeId)) return false
   if (!value || value.requestId !== saved.requestId) return true
   return saved.unconfirmedAction === 'start' ? value.state === 'accepted' : value.state === 'accepted' || resumable.includes(value.state)
  }
  // Accepted proves persistence, not a live worker. An explicit same-ID Submit
  // is safe both when a worker is running and when its first state write failed.
  return !!value && value.state === 'accepted' && validID(value.requestId) && targetTag.test(value.target)
 })

 async function read(gen: number): Promise<void> {
  controller = new AbortController()
  try {
   const value = pending.value?.taskId
    ? await getUpdateTask(pending.value.taskId, { signal: controller.signal })
    : await getCurrentUpdateTask({ signal: controller.signal })
   if (disposed || gen !== generation) return
   task.value = value
   const saved = pending.value
   if (value && saved?.requestId === value.requestId) {
    saved.taskId = value.id
    const action = saved.unconfirmedAction
    const stillUnconfirmed = action === 'start'
     ? value.state === 'accepted'
     : action === 'resume' && (value.state === 'accepted' || resumable.includes(value.state))
    if (action && !stillUnconfirmed) {
     saved.unconfirmedAction = null
     if (action === 'resume') saved.resumeId = undefined
    }
    if (['completed', 'failed'].includes(value.state)) pending.value = null
    remember()
   }
   connectionUnknown.value = !!pending.value && (!value || value.requestId !== pending.value.requestId || !!pending.value.unconfirmedAction)
  } catch {
   if (!disposed && gen === generation) connectionUnknown.value = true
  } finally {
   if (!disposed && gen === generation && (active.value || connectionUnknown.value)) timer = setTimeout(() => void read(gen), 1500)
  }
 }
 async function recheck(): Promise<void> {
  stop()
  await read(generation)
 }
 function submissionFailed(error: unknown) {
  submissionError.value = { status: error instanceof HttpError ? error.status : 0 }
  if (error instanceof HttpError && (error.status === 400 || error.status === 409)) {
   pending.value = null
   remember()
   connectionUnknown.value = false
  } else connectionUnknown.value = true
 }
 function writerSettled(gen: number) {
  if (disposed) return
  // Recheck replaces query generations, not this instance's only write. Its
  // stale response cannot replace a new query, but settling must release busy.
  submitting.value = false
  if (gen === generation) void read(gen)
 }
 async function start(target: string): Promise<void> {
  if (submitting.value) return
  if (pending.value?.unconfirmedAction === 'resume') {
   await recoverOriginal()
   return
  }
  stop()
  const gen = generation
  submitting.value = true
  manual.value = null
  submissionError.value = null
  if (!pending.value) pending.value = { requestId: requestId(), target }
  pending.value.unconfirmedAction = 'start'
  // Persist before dispatch: refresh/unmount can happen before any catch runs.
  remember()
  try {
   const result = await startUpdateTask({ requestId: pending.value.requestId, target: pending.value.target })
   if (disposed || gen !== generation) return
   if (result.status === 'manual') {
    manual.value = { command: result.command }
    pending.value = null
    remember()
    return
   }
   if (!result.task) throw new Error('Missing accepted task')
   task.value = result.task
   pending.value.taskId = result.task.id
   pending.value.unconfirmedAction = null
   connectionUnknown.value = false
   remember()
  } catch (error) {
   if (!disposed && gen === generation) submissionFailed(error)
  } finally { writerSettled(gen) }
 }
 async function submitContinuation(saved: Pending): Promise<void> {
  const taskId = saved.taskId
  const resumeId = saved.resumeId
  if (!taskId || !resumeId || submitting.value) return
  stop()
  const gen = generation
  submitting.value = true
  submissionError.value = null
  saved.unconfirmedAction = 'resume'
  remember()
  try {
   const result = await resumeUpdateTask(taskId, resumeId)
   if (disposed || gen !== generation) return
   if (!result?.id) throw new Error('Missing continuation task')
   task.value = result
   saved.unconfirmedAction = null
   saved.resumeId = undefined
   connectionUnknown.value = false
   remember()
  } catch (error) {
   if (!disposed && gen === generation) submissionFailed(error)
  } finally { writerSettled(gen) }
 }
 async function resume(): Promise<void> {
  const value = task.value
  if (!value || submitting.value || !resumable.includes(value.state)) return
  if (pending.value?.unconfirmedAction) {
   await recoverOriginal()
   return
  }
  pending.value = { requestId: value.requestId, target: value.target, taskId: value.id, resumeId: requestId(), unconfirmedAction: 'resume' }
  await submitContinuation(pending.value)
 }
 async function recoverOriginal(): Promise<void> {
  if (submitting.value || !canRecoverOriginal.value) return
  const saved = pending.value
  if (saved?.unconfirmedAction === 'resume') {
   await submitContinuation(saved)
  } else if (saved?.unconfirmedAction === 'start') {
   await start(saved.target)
  } else {
   const value = task.value
   if (!value || value.state !== 'accepted') return
   pending.value = { requestId: value.requestId, target: value.target, taskId: value.id, unconfirmedAction: 'start' }
   await start(value.target)
  }
 }
 onMounted(() => void recheck())
 onBeforeUnmount(() => { disposed = true; stop() })
 return { task, pending, connectionUnknown, submitting, submissionError, manual, busy, canStart, canRecoverOriginal, start, resume, recoverOriginal, recheck }
}
