import { computed, onBeforeUnmount, shallowRef, watch } from 'vue'
import type { Ref } from 'vue'
import {
  opsApi,
  streamOps,
  errorCode,
  requestId,
  utf8Bytes,
} from '../api/opsChat'
import type {
  Analysis,
  Confirmation,
  Entry,
  Patch,
  Preview,
  Retry,
  Session,
  Snapshot,
  StreamEvent,
  Tool,
  ToolArgs,
  Turn,
  Capabilities,
} from '../api/opsChat'
import { listServers, type Server } from '../api/servers'
import { HttpError } from '../api/http'
import { useOpsChatStore, type PendingSubmission } from '../stores/opsChat'
import { onOpsAuthReset, opsAuthEpoch, resetOpsAuth } from '../utils/opsAuth'

let owner = 0
const relinquish = new Set<() => void>()
const visibleKinds = new Set([
  'user',
  'assistant',
  'analysis',
  'tool_request',
  'tool_call',
])
export function useOpsChat(options: {
  active: Ref<boolean>
  initialServerIds: () => string[]
}) {
  const store = useOpsChatStore()
  const state = store.state
  const capabilities = shallowRef<Capabilities | null>(null)
  const servers = shallowRef<Server[]>([])
  const tools = shallowRef<Tool[]>([])
  const draft = shallowRef('')
  const savedDraft = shallowRef('')
  const draftStatus = shallowRef<
    'saved' | 'dirty' | 'saving' | 'failed' | 'conflict'
  >('saved')
  const error = shallowRef('')
  const loading = shallowRef(false)
  const busy = shallowRef(false)
  const connected = shallowRef(false)
  const preview = shallowRef<Preview | null>(null)
  const historyCursor = shallowRef('')
  const session = computed(() => state.snapshot?.session ?? null)
  const dirty = computed(() => draft.value !== savedDraft.value)
  const activeRun = computed(
    () =>
      state.snapshot?.runs?.find((r) => r.id === session.value?.activeRunId) ??
      null,
  )
  const pending = computed(() => state.pending[state.currentId] ?? null)
  const confirmations = computed(() =>
    (state.snapshot?.confirmations ?? []).filter(
      (c) => c.runId === session.value?.activeRunId,
    ),
  )
  let generation = 0,
    mine = 0,
    previewGeneration = 0
  let controller: AbortController | null = null
  let timer: ReturnType<typeof setTimeout> | undefined
  let reconnect: ReturnType<typeof setTimeout> | undefined
  let cursor = '',
    backoff = 500
  let resetHistoryPending = false
  let userEditGeneration = 0
  let chain: Promise<unknown> = Promise.resolve()
  let refreshState: {
    generation: number
    again: boolean
    promise: Promise<boolean>
  } | null = null
  const valid = (g: number, auth: number) =>
    g === generation && auth === opsAuthEpoch() && mine === owner
  const token = () => ({
    g: generation,
    auth: opsAuthEpoch(),
    id: state.currentId,
  })
  function detach(): void {
    controller?.abort()
    controller = null
    clearTimeout(reconnect)
    reconnect = undefined
    connected.value = false
  }
  function invalidate(): void {
    generation++
    previewGeneration++
    detach()
    clearTimeout(timer)
    preview.value = null
    resetHistoryPending = false
  }
  function remember(): void {
    if (state.currentId && mine === owner)
      state.drafts[state.currentId] = {
        text: draft.value,
        baseline: savedDraft.value,
        editGeneration: userEditGeneration,
        status: draftStatus.value,
      }
  }
  const releaseOwnership = () => {
    if (mine === owner && mine !== 0) {
      remember()
      invalidate()
    }
  }
  relinquish.add(releaseOwnership)
  function fail(e: unknown): void {
    error.value = errorCode(e)
  }
  function mergeEntries(entries: Entry[]): void {
    const bySeq = new Map(state.entries.map((e) => [e.seq, e]))
    for (const entry of entries)
      if (entry.sessionId === state.currentId && visibleKinds.has(entry.kind))
        bySeq.set(entry.seq, entry)
    state.entries = [...bySeq.values()]
      .sort((a, b) => a.seq - b.seq)
      .slice(-2000)
  }
  function applySession(
    value: Session,
    fields?: Omit<Patch, 'revision'>,
  ): boolean {
    if (!session.value || value.revision >= session.value.revision) {
      // PATCH owns configuration fields only. Runtime facts come from snapshots.
      if (fields && session.value) {
        value = {
          ...session.value,
          revision: value.revision,
          updatedAt: value.updatedAt,
          ...(fields.title !== undefined ? { title: value.title } : {}),
          ...(fields.draft !== undefined ? { draft: value.draft } : {}),
          ...(fields.serverIds !== undefined
            ? { serverIds: value.serverIds }
            : {}),
        }
      }
      if (state.snapshot) state.snapshot = { ...state.snapshot, session: value }
      state.sessions = [
        ...state.sessions.filter((s) => s.id !== value.id),
        value,
      ].sort((a, b) => b.updatedAt.localeCompare(a.updatedAt))
      return true
    }
    return false
  }
  function applySnapshot(value: Snapshot, restoring = false): void {
    if (
      !restoring &&
      state.snapshot &&
      value.watermark < state.snapshot.watermark
    )
      return
    if (
      !restoring &&
      session.value &&
      value.session.revision < session.value.revision
    ) {
      value = {
        ...value,
        session: {
          ...session.value,
          activeRunId: value.session.activeRunId,
          watermark: value.session.watermark,
        },
      }
    }
    const replacingHistory = restoring || resetHistoryPending
    const keepEntries = replacingHistory ? [] : state.entries
    const oldCalls = replacingHistory ? {} : state.calls
    if (!restoring && dirty.value && value.session.draft !== savedDraft.value)
      draftStatus.value = 'conflict'
    if (restoring || !dirty.value) {
      draft.value = value.session.draft
      savedDraft.value = value.session.draft
      draftStatus.value = 'saved'
    }
    if (restoring) {
      const local = state.drafts[value.session.id]
      userEditGeneration = local?.editGeneration ?? 0
      if (local && local.text !== local.baseline) {
        draft.value = local.text
        savedDraft.value = local.baseline
        draftStatus.value =
          local.baseline === value.session.draft
            ? local.status === 'saving'
              ? 'dirty'
              : local.status
            : 'conflict'
      }
    }
    state.snapshot = value
    state.entries = keepEntries
    state.calls = {
      ...oldCalls,
      ...Object.fromEntries((value.calls ?? []).map((c) => [c.callId, c])),
    }
    mergeEntries(value.entries.entries ?? [])
    if (replacingHistory) historyCursor.value = value.entries.cursor
    resetHistoryPending = false
    applySession(value.session)
    remember()
  }
  async function hydrateCalls(): Promise<void> {
    const t = token()
    const ids = [
      ...new Set(
        state.entries
          .filter((e) => e.callId && !state.calls[e.callId])
          .map((e) => e.callId!),
      ),
    ]
    for (const id of ids) {
      if (!valid(t.g, t.auth)) return
      try {
        const call = await opsApi.call(t.id, id)
        if (valid(t.g, t.auth)) state.calls = { ...state.calls, [id]: call }
      } catch (e) {
        if (valid(t.g, t.auth)) fail(e)
      }
    }
  }
  async function refresh(): Promise<boolean> {
    if (refreshState?.generation === generation) {
      refreshState.again = true
      return refreshState.promise
    }
    const t = token()
    const job = {
      generation: t.g,
      again: false,
      promise: Promise.resolve(false),
    }
    refreshState = job
    job.promise = (async () => {
      let ok = false
      do {
        job.again = false
        try {
          const value = await opsApi.snapshot(t.id)
          if (!valid(t.g, t.auth)) return false
          applySnapshot(value)
          await hydrateCalls()
          ok = true
        } catch (e) {
          if (valid(t.g, t.auth)) fail(e)
          ok = false
        }
      } while (job.again && valid(t.g, t.auth))
      return ok
    })()
    try {
      return await job.promise
    } finally {
      if (refreshState === job) refreshState = null
    }
  }
  function event(event: StreamEvent, g: number, auth: number): void {
    if (!valid(g, auth)) return
    if (event.type === 'auth') {
      resetOpsAuth()
      return
    }
    if (event.type === 'reset') {
      resetHistoryPending = true
      detach()
      void refresh().then((ok) => {
        if (valid(g, auth) && state.snapshot) {
          if (ok) cursor = state.currentId + ':' + state.snapshot.watermark
          reconnect = setTimeout(connect, ok ? 0 : backoff)
        }
      })
      return
    }
    if (event.type === 'unavailable' || event.type === 'error') {
      error.value = 'ops_unavailable'
      detach()
      return
    }
    if (event.type === 'ready' || event.type === 'heartbeat') {
      connected.value = true
      backoff = 500
      const seq = Number(cursor.slice(cursor.lastIndexOf(':') + 1))
      if (
        resetHistoryPending ||
        (state.snapshot && seq > state.snapshot.watermark)
      )
        void refresh()
      return
    }
    if (event.type !== 'entry') return
    const entry = event.data as Entry
    if (
      entry.sessionId !== state.currentId ||
      !Number.isSafeInteger(entry.seq) ||
      event.id !== state.currentId + ':' + entry.seq
    )
      return
    const last = Number(cursor.slice(cursor.lastIndexOf(':') + 1))
    if (entry.seq <= last) return
    cursor = event.id
    mergeEntries([entry])
    void refresh()
  }
  function connect(): void {
    if (!options.active.value || !state.currentId || controller) return
    const t = token()
    const c = new AbortController()
    controller = c
    void streamOps(t.id, cursor, c.signal, (ev) => event(ev, t.g, t.auth))
      .catch((e) => {
        if (valid(t.g, t.auth) && !c.signal.aborted) fail(e)
      })
      .finally(() => {
        if (controller === c) controller = null
        if (!valid(t.g, t.auth) || c.signal.aborted || !options.active.value)
          return
        connected.value = false
        reconnect = setTimeout(connect, backoff)
        backoff = Math.min(15000, backoff * 2)
      })
  }
  async function recover(): Promise<boolean> {
    const t = token()
    try {
      const [cap, catalogue, registered] = await Promise.all([
        opsApi.capabilities(),
        opsApi.tools(),
        listServers(),
      ])
      if (!valid(t.g, t.auth)) return false
      capabilities.value = cap
      tools.value = catalogue
      servers.value = registered
      if (!cap.available) {
        error.value = 'ops_unavailable'
        return false
      }
      const ok = await refresh()
      if (ok && valid(t.g, t.auth)) {
        error.value = ''
        connect()
      }
      return ok
    } catch (e) {
      if (valid(t.g, t.auth)) fail(e)
      return false
    }
  }
  async function select(id: string): Promise<boolean> {
    if (!(await flush())) return false
    invalidate()
    const t = token()
    loading.value = true
    error.value = ''
    state.currentId = id
    state.snapshot = null
    state.entries = []
    state.calls = {}
    refreshState = null
    try {
      await opsApi.activate(id)
      const value = await opsApi.snapshot(id)
      if (!valid(t.g, t.auth)) return false
      applySnapshot(value, true)
      cursor = id + ':' + value.watermark
      await hydrateCalls()
      connect()
      return true
    } catch (e) {
      if (valid(t.g, t.auth)) fail(e)
      return false
    } finally {
      if (valid(t.g, t.auth)) loading.value = false
    }
  }
  async function create(): Promise<boolean> {
    const initial = token()
    if (!(await flush())) return false
    if (!valid(initial.g, initial.auth)) return false
    const t = token()
    busy.value = true
    try {
      const value = await opsApi.create(options.initialServerIds().slice(0, 8))
      if (!valid(t.g, t.auth)) return false
      state.sessions = [...state.sessions, value]
      return await select(value.id)
    } catch (e) {
      if (valid(t.g, t.auth)) fail(e)
      return false
    } finally {
      if (t.auth === opsAuthEpoch()) busy.value = false
    }
  }
  async function restore(): Promise<void> {
    for (const release of relinquish) release()
    mine = ++owner
    invalidate()
    const t = token()
    loading.value = true
    error.value = ''
    try {
      const [cap, catalogue, registered] = await Promise.all([
        opsApi.capabilities(),
        opsApi.tools(),
        listServers(),
      ])
      if (!valid(t.g, t.auth)) return
      capabilities.value = cap
      tools.value = catalogue
      servers.value = registered
      if (!cap.available) {
        error.value = 'ops_unavailable'
        return
      }
      const page = await opsApi.list()
      if (!valid(t.g, t.auth)) return
      state.sessions = (page.sessions ?? []).sort((a, b) =>
        b.updatedAt.localeCompare(a.updatedAt),
      )
      const id = page.activeSessionId || state.sessions[0]?.id
      // Entry defaults only apply to a genuinely new session.
      if (id) await select(id)
      else await create()
    } catch (e) {
      if (valid(t.g, t.auth)) fail(e)
    } finally {
      if (t.auth === opsAuthEpoch()) loading.value = false
    }
  }
  function serialize<T>(operation: () => Promise<T>): Promise<T> {
    const result = chain.then(operation, operation)
    chain = result.catch(() => undefined)
    return result
  }
  async function patch(
    input: Omit<Patch, 'revision'>,
  ): Promise<Session | null> {
    const t = token()
    return serialize(async () => {
      if (
        !valid(t.g, t.auth) ||
        !session.value ||
        t.id !== state.currentId ||
        pending.value
      )
        return null
      try {
        const value = await opsApi.patch(t.id, {
          ...input,
          revision: session.value.revision,
        })
        if (!valid(t.g, t.auth)) return null
        if (!applySession(value, input)) return null
        return value
      } catch (e) {
        if (valid(t.g, t.auth)) {
          fail(e)
          if (errorCode(e) === 'ops_conflict') draftStatus.value = 'conflict'
        }
        return null
      }
    })
  }
  async function flush(): Promise<boolean> {
    const t = token()
    clearTimeout(timer)
    // Wait for an already-started save before capturing the newest draft.
    await chain
    if (!valid(t.g, t.auth)) return false
    if (!dirty.value) return true
    if (pending.value) {
      remember()
      return false
    }
    if (draftStatus.value === 'conflict' || !session.value) return false
    if (utf8Bytes(draft.value) > 8192) {
      error.value = 'ops_invalid'
      draftStatus.value = 'failed'
      return false
    }
    const text = draft.value
    draftStatus.value = 'saving'
    const value = await patch({ draft: text })
    if (!valid(t.g, t.auth)) return false
    if (value) {
      savedDraft.value = value.draft
      if (draft.value === text) draft.value = value.draft
      draftStatus.value = dirty.value ? 'dirty' : 'saved'
      if (dirty.value) return flush()
    } else if ((draftStatus.value as string) !== 'conflict')
      draftStatus.value = 'failed'
    remember()
    return !!value
  }
  function editDraft(value: string): void {
    userEditGeneration++
    draft.value = value
    if (draftStatus.value !== 'conflict')
      draftStatus.value = dirty.value ? 'dirty' : 'saved'
    clearTimeout(timer)
    if (draftStatus.value !== 'conflict' && !pending.value)
      timer = setTimeout(() => void flush(), 500)
    remember()
  }
  async function resolveDraft(useLocal: boolean): Promise<void> {
    const t = token()
    const local = draft.value
    try {
      const value = await opsApi.snapshot(t.id)
      if (!valid(t.g, t.auth)) return
      delete state.drafts[t.id]
      applySnapshot(value, true)
      if (useLocal) {
        editDraft(local)
        await flush()
      }
    } catch (e) {
      if (valid(t.g, t.auth)) fail(e)
    }
  }
  async function targets(ids: string[]): Promise<boolean> {
    const t = token()
    if (
      busy.value ||
      pending.value ||
      ids.length > 8 ||
      session.value?.activeRunId
    )
      return false
    busy.value = true
    try {
      if (!(await flush()) || !valid(t.g, t.auth)) return false
      return !!(await patch({ serverIds: ids }))
    } finally {
      if (valid(t.g, t.auth)) busy.value = false
    }
  }
  async function rename(title: string): Promise<boolean> {
    const t = token()
    if (
      pending.value ||
      Array.from(title).length > 80 ||
      !title.trim() ||
      !(await flush())
    )
      return false
    if (!valid(t.g, t.auth)) return false
    return !!(await patch({ title }))
  }
  async function submitPending(): Promise<boolean> {
    const t = token(),
      p = pending.value
    if (!p || busy.value) return false
    busy.value = true
    error.value = ''
    try {
      if (p.kind === 'turn') await opsApi.turn(t.id, p.input as Turn)
      else if (p.kind === 'retry') await opsApi.retry(t.id, p.input as Retry)
      else await opsApi.analyze(t.id, p.input as Analysis)
      if (!valid(t.g, t.auth)) return false
      delete state.pending[t.id]
      preview.value = null
      await refresh()
      if (
        valid(t.g, t.auth) &&
        p.sentText !== undefined &&
        userEditGeneration === p.sentEditGeneration
      ) {
        editDraft('')
        await flush()
      }
      return true
    } catch (e) {
      if (valid(t.g, t.auth)) {
        fail(e)
        // Definitive rejections can be edited; ambiguous network failures retain the exact UUID and body.
        if (
          errorCode(e) !== 'network' &&
          !(e instanceof HttpError && e.status >= 500)
        )
          delete state.pending[t.id]
      }
      return false
    } finally {
      if (valid(t.g, t.auth)) {
        busy.value = false
        if (!pending.value && dirty.value && draftStatus.value !== 'conflict')
          timer = setTimeout(() => void flush(), 500)
      }
    }
  }
  async function submit(
    kind: PendingSubmission['kind'],
    input: Partial<Turn & Retry & Analysis>,
    sentText?: string,
  ): Promise<boolean> {
    const t = token()
    const sentEditGeneration = userEditGeneration
    if (pending.value) return submitPending()
    if (
      busy.value ||
      session.value?.activeRunId ||
      !(await flush()) ||
      !session.value
    )
      return false
    if (
      !valid(t.g, t.auth) ||
      busy.value ||
      pending.value ||
      session.value?.activeRunId
    )
      return false
    state.pending[state.currentId] = {
      kind,
      input: {
        ...input,
        clientRequestId: requestId(),
        revision: session.value.revision,
      } as Turn | Retry | Analysis,
      sentText,
      sentEditGeneration,
    }
    return submitPending()
  }
  async function send(): Promise<boolean> {
    const text = draft.value
    if (
      !text.trim() ||
      utf8Bytes(text) > 8192 ||
      !capabilities.value?.modelAvailable
    )
      return false
    return submit('turn', { text }, text)
  }
  async function tool(toolId: string, args: ToolArgs): Promise<boolean> {
    if (
      !tools.value.some((t) => t.toolId === toolId) ||
      !session.value?.serverIds?.length
    )
      return false
    return submit('turn', { toolId, args })
  }
  async function retry(callIds: string[]): Promise<boolean> {
    return submit('retry', { callIds })
  }
  async function localAction(action: () => Promise<unknown>): Promise<boolean> {
    if (busy.value) return false
    const t = token()
    busy.value = true
    error.value = ''
    try {
      await action()
      if (!valid(t.g, t.auth)) return false
      await refresh()
      return true
    } catch (e) {
      if (valid(t.g, t.auth)) fail(e)
      return false
    } finally {
      if (valid(t.g, t.auth)) busy.value = false
    }
  }
  async function stop(runId = session.value?.activeRunId): Promise<boolean> {
    if (!runId) return true
    const id = state.currentId
    return localAction(() => opsApi.cancel(id, runId))
  }
  function confirmBatch(confirmation: Confirmation): Promise<boolean> {
    const id = state.currentId
    return localAction(() => opsApi.confirm(id, confirmation))
  }
  function reissue(runId: string): Promise<boolean> {
    const id = state.currentId
    return localAction(() => opsApi.reissue(id, runId))
  }
  async function analyzePreview(ids: string[]): Promise<void> {
    if (!capabilities.value?.modelAvailable || busy.value || pending.value)
      return
    const t = token(),
      previewToken = ++previewGeneration
    preview.value = null
    try {
      const value = await opsApi.preview(t.id, ids)
      if (valid(t.g, t.auth) && previewToken === previewGeneration)
        preview.value = value
    } catch (e) {
      if (valid(t.g, t.auth) && previewToken === previewGeneration) fail(e)
    }
  }
  async function analyze(): Promise<boolean> {
    const value = preview.value
    if (!value) return false
    return submit('analysis', {
      callIds: value.callIds,
      previewHash: value.hash,
      provider: value.provider,
      consent: true,
    })
  }
  async function history(): Promise<void> {
    const t = token()
    if (!historyCursor.value) return
    try {
      const page = await opsApi.entries(t.id, historyCursor.value)
      if (valid(t.g, t.auth)) {
        mergeEntries(page.entries ?? [])
        historyCursor.value = page.entries?.length ? page.cursor : ''
        await hydrateCalls()
      }
    } catch (e) {
      if (valid(t.g, t.auth)) fail(e)
    }
  }
  async function remove(): Promise<boolean> {
    const initial = token()
    if (!(await flush()) || session.value?.activeRunId) {
      error.value = 'ops_conflict'
      return false
    }
    if (!valid(initial.g, initial.auth)) return false
    const t = token()
    try {
      await opsApi.delete(t.id)
      if (!valid(t.g, t.auth)) return false
      delete state.pending[t.id]
      delete state.drafts[t.id]
      state.sessions = state.sessions.filter((s) => s.id !== t.id)
      state.snapshot = null
      savedDraft.value = ''
      draft.value = ''
      if (state.sessions[0]) return select(state.sessions[0].id)
      return create()
    } catch (e) {
      if (valid(t.g, t.auth)) fail(e)
      return false
    }
  }
  async function close(): Promise<boolean> {
    if (!(await flush())) return false
    invalidate()
    return true
  }
  function discard(): void {
    draft.value = savedDraft.value
    draftStatus.value = 'saved'
    clearTimeout(timer)
    remember()
  }
  function beforeUnload(e: BeforeUnloadEvent): void {
    if (dirty.value) {
      e.preventDefault()
      e.returnValue = ''
    }
  }
  window.addEventListener('beforeunload', beforeUnload)
  const unAuth = onOpsAuthReset(() => {
    invalidate()
    draft.value = ''
    savedDraft.value = ''
    servers.value = []
    capabilities.value = null
    loading.value = false
    busy.value = false
    draftStatus.value = 'saved'
    error.value = 'unauthorized'
  })
  watch(
    options.active,
    (active) => {
      if (active) void restore()
      else {
        void flush()
        detach()
      }
    },
    { immediate: true },
  )
  onBeforeUnmount(() => {
    // Route guards and explicit close await flush. Abrupt unmount retains unsaved
    // text in memory only; a new panel can resolve/save it, never silently lose it.
    remember()
    invalidate()
    relinquish.delete(releaseOwnership)
    unAuth()
    window.removeEventListener('beforeunload', beforeUnload)
  })
  return {
    state,
    capabilities,
    tools,
    servers,
    draft,
    draftStatus,
    dirty,
    error,
    loading,
    busy,
    connected,
    preview,
    historyCursor,
    session,
    activeRun,
    pending,
    confirmations,
    restore,
    select,
    create,
    editDraft,
    flush,
    resolveDraft,
    targets,
    rename,
    send,
    tool,
    retry,
    submitPending,
    stop,
    confirmBatch,
    reissue,
    analyzePreview,
    analyze,
    history,
    remove,
    close,
    discard,
    refresh,
    recover,
  }
}
