import { computed, onBeforeUnmount, ref, watch, type Ref } from 'vue'
import { HttpError } from '../api/http'
import { getRetryAttempts, getRetryContext, getRetryPlan, getRetryRequest, reconcileRetry, submitRetry, type RetryAttemptPage, type RetryContext, type RetryPlan, type RetryRequest, type RetryReason } from '../api/runRetry'

interface PendingRetry { runId: string; requestId: string; rejectionCode?: string }
const keyFor = (id: string) => `pipewright.retry.pending:${id}`
const opaque = /^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$/
const rejections = new Map([['bad_request', 400], ['unauthorized', 401], ['csrf_invalid', 403], ['run_not_found', 404], ['retry_not_found', 404], ['retry_conflict', 409], ['retry_invalid', 422], ['retry_evidence_unavailable', 422], ['retry_outcome_unknown', 422], ['retry_maintenance', 503]])
function canonicalNotFound(e: unknown): e is HttpError { return e instanceof HttpError && e.status === 404 && e.apiError?.code === 'retry_not_found' && typeof e.apiError.message === 'string' }
function canonicalRejectionCode(e: unknown): string | undefined {
  return e instanceof HttpError && e.apiError && typeof e.apiError.message === 'string' && rejections.get(e.apiError.code) === e.status ? e.apiError.code : undefined
}
function rememberLateRejection(runId: string, requestId: string, rejectionCode: string) {
  try {
    const saved = JSON.parse(sessionStorage.getItem(keyFor(runId)) ?? 'null')
    if (saved?.runId === runId && saved?.requestId === requestId) sessionStorage.setItem(keyFor(runId), JSON.stringify({ runId, requestId, rejectionCode }))
  } catch { /* Missing storage cannot turn an uncertain operation into a replay. */ }
}
function errorCode(error: unknown): string {
  return error instanceof HttpError ? error.apiError?.code ?? (error.status === 0 ? 'retry_connection_unknown' : 'retry_unavailable') : 'retry_unavailable'
}

/** An unresolved POST is queried by its original ID; never automatically replayed. */
export function useRunRetry(runId: Ref<string>) {
  const context = ref<RetryContext | null>(null)
  const attempts = ref<RetryAttemptPage | null>(null)
  const plan = ref<RetryPlan | null>(null)
  const error = ref('')
  const contextError = ref('')
  const planLoading = ref(false)
  const submitting = ref(false)
  const querying = ref(false)
  const reconciling = ref(false)
  const pending = ref<PendingRetry | null>(null)
  const request = ref<RetryRequest | null>(null)
  const reconcileReasons = ref<RetryReason[]>([])
  const selected = ref<string[]>([])
  const rebuild = ref<string[]>([])
  const historyPage = ref(1)
  let generation = 0
  let planGeneration = 0
  let controller: AbortController | undefined
  let timer: ReturnType<typeof setInterval> | undefined
  let refreshing = false
  let disposed = false

  function restore() {
    pending.value = null
    try {
      const p = JSON.parse(sessionStorage.getItem(keyFor(runId.value)) ?? 'null')
      if (p && p.runId === runId.value && opaque.test(p.runId) && opaque.test(p.requestId)) pending.value = { runId: p.runId, requestId: p.requestId, ...(rejections.has(p.rejectionCode) ? { rejectionCode: p.rejectionCode } : {}) }
    } catch { /* Preserve the server ledger even when browser storage is unavailable. */ }
  }
  function remember(p: PendingRetry) {
    // Persist before dispatch. A reload or a lost response cannot create a new ID.
    // Session storage survives this tab's reload and cannot be overwritten by
    // a competing tab's different request. The server ledger remains durable.
    sessionStorage.setItem(keyFor(p.runId), JSON.stringify(p))
    pending.value = p
  }
  async function refresh() {
    if (refreshing || disposed) return
    const gen = generation; const id = runId.value
    refreshing = true
    const result = await Promise.allSettled([
      getRetryContext(id, { signal: controller?.signal, throwOnUnauthorized: true }),
      getRetryAttempts(id, historyPage.value, { signal: controller?.signal, throwOnUnauthorized: true }),
    ])
    if (gen !== generation || disposed) return
    refreshing = false
    if (result[0].status === 'fulfilled') { context.value = result[0].value; contextError.value = '' }
    else { contextError.value = errorCode(result[0].reason) }
    if (result[1].status === 'fulfilled' && result[1].value.page === historyPage.value) attempts.value = result[1].value
  }
  async function queryOriginal() {
    const p = pending.value
    if (!p || querying.value) return
    const gen = generation
    querying.value = true
    try {
      const value = await getRetryRequest(p.runId, p.requestId, { signal: controller?.signal, throwOnUnauthorized: true })
      if (gen !== generation || disposed) return
      request.value = value
      // A found ledger takes precedence over a conflicting POST response.
      if (p.rejectionCode) { try { remember({ runId: p.runId, requestId: p.requestId }) } catch { /* The known record remains visible. */ } }
      error.value = value.reasonCode ?? ''
      void refresh()
    } catch (e) {
      if (gen !== generation || disposed) return
      if (p.rejectionCode && canonicalNotFound(e)) {
        // Both facts are required: a canonical refusal of this POST and the
        // authenticated ledger's canonical absence. This enables an explicit
        // new preview, never a silent second POST.
        try { sessionStorage.removeItem(keyFor(p.runId)); pending.value = null } catch { /* Keep the ID if storage cannot be updated. */ }
        plan.value = null; error.value = p.rejectionCode; void refresh(); return
      }
      // 404 is not proof of non-acceptance after a transport failure. Keep ID.
      error.value = canonicalNotFound(e) ? 'retry_request_unconfirmed' : errorCode(e)
    } finally { if (gen === generation) querying.value = false }
  }
  async function preview(targets = selected.value, rebuildIds = rebuild.value) {
    const seq = ++planGeneration; const gen = generation; const id = runId.value
    selected.value = [...targets]; rebuild.value = [...rebuildIds]
    error.value = ''; planLoading.value = true
    try {
      const value = await getRetryPlan(id, targets, rebuildIds, { signal: controller?.signal, throwOnUnauthorized: true })
      if (seq === planGeneration && gen === generation && !disposed) plan.value = value
    } catch (e) {
      if (seq === planGeneration && gen === generation && !disposed) { plan.value = null; error.value = errorCode(e) }
    } finally { if (seq === planGeneration && gen === generation) planLoading.value = false }
  }
  function openPlan(targets: string[]) { plan.value = null; return preview(targets, []) }
  async function submit(): Promise<RetryRequest | null> {
    const p = plan.value
    if (!p?.canSubmit || planLoading.value || submitting.value || pending.value) return null
    const gen = generation; const id = runId.value
    const bytes = new Uint8Array(16); crypto.getRandomValues(bytes)
    const requestId = Array.from(bytes, v => v.toString(16).padStart(2, '0')).join('')
    error.value = ''
    try { remember({ runId: id, requestId }) }
    catch { error.value = 'retry_storage_unavailable'; return null }
    submitting.value = true
    try {
      const value = await submitRetry(id, { requestId, baseAttemptId: p.baseAttemptId, baseRevision: p.baseRevision, planFingerprint: p.planFingerprint, targetNodeIds: [...p.targetNodeIds], rebuildNodeIds: [...(p.rebuildNodeIds ?? [])] }, { throwOnUnauthorized: true })
      if (gen !== generation || disposed) return null
      request.value = value
      void refresh()
      return value
    } catch (e) {
      const rejectionCode = canonicalRejectionCode(e)
      // Preserve the original operation's canonical refusal even if its page
      // has changed, without mutating the new page or replacing another ID.
      if (rejectionCode) rememberLateRejection(id, requestId, rejectionCode)
      if (gen !== generation || disposed) return null
      error.value = errorCode(e)
      if (rejectionCode) {
        const rejected = { runId: id, requestId, rejectionCode }
        pending.value = rejected
        try { remember(rejected) } catch { /* In-memory ID stays blocked; no replay. */ }
      }
      await queryOriginal()
      return request.value
    } finally { if (gen === generation) submitting.value = false }
  }
  async function reconcile() {
    if (reconciling.value) return
    const gen = generation
    reconciling.value = true; error.value = ''
    try {
      const value = await reconcileRetry(runId.value, { signal: controller?.signal, throwOnUnauthorized: true })
      if (gen !== generation || disposed) return
      reconcileReasons.value = value.reasons
      void refresh()
      if (selected.value.length) void preview()
    } catch (e) { if (gen === generation && !disposed) error.value = errorCode(e) }
    finally { if (gen === generation) reconciling.value = false }
  }
  async function setHistoryPage(page: number) { historyPage.value = page; await refresh() }
  const failedNodes = computed(() => context.value?.nodes.filter(n => n.decision.status === 'failed') ?? [])
  const isHead = computed(() => !context.value || context.value.headAttemptId === runId.value)
  const unresolved = computed(() => !!pending.value && !request.value)
  watch(runId, () => {
    generation++; planGeneration++; controller?.abort(); controller = new AbortController()
    context.value = null; attempts.value = null; plan.value = null; request.value = null
    error.value = ''; contextError.value = ''; selected.value = []; rebuild.value = []; reconcileReasons.value = []
    planLoading.value = false; submitting.value = false; querying.value = false; refreshing = false; reconciling.value = false; historyPage.value = 1
    restore(); void refresh(); if (pending.value) void queryOriginal()
  }, { immediate: true })
  // Refresh terminal attempts too: cleanup, other tabs and reconciliation may
  // change recovery eligibility without emitting an old run's terminal SSE.
  timer = setInterval(() => { void refresh(); if (pending.value && !request.value) void queryOriginal() }, 4000)
  onBeforeUnmount(() => { disposed = true; generation++; planGeneration++; controller?.abort(); if (timer) clearInterval(timer) })
  return { context, contextError, attempts, historyPage, plan, error, planLoading, submitting, querying, reconciling, pending, request, reconcileReasons, selected, rebuild, failedNodes, isHead, unresolved, refresh, openPlan, preview, submit, queryOriginal, reconcile, setHistoryPage }
}
