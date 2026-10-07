import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, shallowRef, toRaw } from 'vue'
import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import { useOpsChat } from './useOpsChat'
import { useOpsChatStore } from '../stores/opsChat'
import {
  opsApi,
  streamOps,
  type Snapshot,
  type Session,
  type Preview,
  type StreamEvent,
  type Call,
} from '../api/opsChat'
import { HttpError } from '../api/http'
import { resetOpsAuth } from '../utils/opsAuth'
vi.mock('../api/servers', () => ({
  listServers: vi.fn().mockResolvedValue([
    { id: 'a', name: 'A', host: 'host-a', port: 22 },
    { id: 'b', name: 'B', host: 'host-b', port: 22 },
  ]),
}))
vi.mock('../api/opsChat', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api/opsChat')>()
  return {
    ...actual,
    streamOps: vi.fn(),
    opsApi: Object.fromEntries(
      Object.keys(actual.opsApi).map((key) => [key, vi.fn()]),
    ),
  }
})
const stamp = '2026-10-07T00:00:00Z'
function session(id = 'chat'): Session {
  return {
    id,
    revision: 1,
    title: id,
    draft: '',
    serverIds: ['a'],
    activeRunId: '',
    watermark: 0,
    createdAt: stamp,
    updatedAt: stamp,
  }
}
function snapshot(s = session()): Snapshot {
  return {
    session: s,
    runs: [],
    calls: [],
    confirmations: [],
    entries: { entries: [], cursor: '', watermark: 0, reset: false },
    watermark: 0,
  }
}
function call(status: Call['status'] = 'running'): Call {
  return {
    callId: 'call',
    runId: 'run',
    sessionId: 'chat',
    serverId: 'a',
    serverName: 'Frozen A',
    toolId: 'docker_logs',
    args: { container: 'original', lines: 200 },
    object: 'original',
    status,
    argsHash: 'args',
    targetHash: 'target',
    output: '',
    error: '',
    truncated: false,
  }
}
function defer<T>() {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((a, b) => {
    resolve = a
    reject = b
  })
  return { promise, resolve, reject }
}
let chat: ReturnType<typeof useOpsChat>, view: VueWrapper | undefined
let db: Record<string, Snapshot>,
  active = 'chat'
const events: ((event: StreamEvent) => void)[] = []
async function open(initial = ['b']): Promise<void> {
  view = mount(
    defineComponent({
      setup() {
        chat = useOpsChat({
          active: shallowRef(true),
          initialServerIds: () => initial,
        })
        return () => null
      },
    }),
  )
  await flushPromises()
}
beforeEach(() => {
  vi.useFakeTimers()
  vi.clearAllMocks()
  resetOpsAuth()
  events.length = 0
  db = { chat: snapshot(), other: snapshot(session('other')) }
  active = 'chat'
  vi.mocked(opsApi.capabilities).mockResolvedValue({
    available: true,
    modelAvailable: true,
    provider: { provider: 'test', model: 'm', configHash: 'hash' },
    limits: {},
  })
  vi.mocked(opsApi.tools).mockResolvedValue([
    { toolId: 'host_resources', mutation: false, schema: {} },
  ])
  vi.mocked(opsApi.list).mockImplementation(async () => ({
    sessions: Object.values(db).map((s) => structuredClone(s.session)),
    activeSessionId: active,
    cursor: '',
  }))
  vi.mocked(opsApi.snapshot).mockImplementation(async (id) =>
    structuredClone(db[id]!),
  )
  vi.mocked(opsApi.activate).mockImplementation(async (id) => {
    active = id
    return { ok: true }
  })
  vi.mocked(opsApi.create).mockImplementation(async (ids) => {
    const s = session('new')
    s.serverIds = ids
    db.new = snapshot(s)
    return structuredClone(s)
  })
  vi.mocked(opsApi.patch).mockImplementation(async (id, input) => {
    const s = db[id]!.session
    if (s.revision !== input.revision)
      throw new HttpError(
        409,
        { code: 'ops_conflict', message: 'raw secret' },
        'conflict',
      )
    Object.assign(s, input, { revision: s.revision + 1 })
    return structuredClone(s)
  })
  vi.mocked(opsApi.turn).mockResolvedValue({
    id: 'r',
    sessionId: 'chat',
    clientRequestId: 'id',
    status: 'queued',
    cancelRequested: false,
    createdAt: stamp,
  })
  vi.mocked(streamOps).mockImplementation(
    async (_id, _after, signal, deliver) => {
      events.push(deliver)
      await new Promise<void>((resolve) =>
        signal.addEventListener('abort', () => resolve(), { once: true }),
      )
    },
  )
})
afterEach(() => {
  view?.unmount()
  view = undefined
  resetOpsAuth()
  vi.useRealTimers()
})
describe('local ops lifecycle and concurrency', () => {
  it('does not resurrect a finished run from a delayed same-revision PATCH ACK', async () => {
    db.chat!.session.activeRunId = 'run'
    await open()
    const d = defer<Session>()
    vi.mocked(opsApi.patch).mockReturnValueOnce(d.promise)
    chat.editDraft('local')
    const saving = chat.flush()
    await flushPromises()
    const ack = {
      ...db.chat!.session,
      revision: 2,
      draft: 'local',
      watermark: 1,
    }
    db.chat!.session = { ...ack, activeRunId: '', watermark: 2 }
    db.chat!.watermark = 2
    await chat.refresh()
    d.resolve(ack)
    expect(await saving).toBe(true)
    expect(chat.session.value?.activeRunId).toBe('')
    expect(chat.session.value?.watermark).toBe(2)
    expect(chat.state.sessions.find((s) => s.id === 'chat')?.activeRunId).toBe(
      '',
    )
    expect(chat.draftStatus.value).toBe('saved')
    expect(await chat.targets(['b'])).toBe(true)
  })
  it('preserves a newer snapshot conflict when an older successful save ACK arrives', async () => {
    await open()
    const d = defer<Session>()
    vi.mocked(opsApi.patch).mockReturnValueOnce(d.promise)
    chat.editDraft('local')
    const saving = chat.flush()
    await flushPromises()
    db.chat!.session = {
      ...session(),
      revision: 3,
      draft: 'remote',
      watermark: 2,
    }
    db.chat!.watermark = 2
    await chat.refresh()
    expect(chat.draftStatus.value).toBe('conflict')
    d.resolve({ ...session(), revision: 2, draft: 'local', watermark: 1 })
    expect(await saving).toBe(false)
    expect(chat.session.value?.revision).toBe(3)
    expect(chat.session.value?.draft).toBe('remote')
    expect(chat.draft.value).toBe('local')
    expect(chat.draftStatus.value).toBe('conflict')
    expect(chat.state.drafts.chat).toMatchObject({
      text: 'local',
      baseline: '',
      status: 'conflict',
    })
    await vi.advanceTimersByTimeAsync(2000)
    expect(await chat.flush()).toBe(false)
    expect(await chat.select('other')).toBe(false)
    expect(opsApi.patch).toHaveBeenCalledTimes(1)
    await chat.resolveDraft(true)
    expect(db.chat!.session.draft).toBe('local')
    expect(chat.draftStatus.value).toBe('saved')
  })
  it('orders runtime snapshots by watermark independently of PATCH revision', async () => {
    db.chat!.session.activeRunId = 'run'
    db.chat!.session.watermark = 1
    db.chat!.watermark = 1
    await open()
    const old = structuredClone(db.chat!)
    expect(await chat.rename('renamed')).toBe(true)
    db.chat!.session = { ...old.session, activeRunId: '', watermark: 3 }
    db.chat!.watermark = 3
    await chat.refresh()
    expect(chat.session.value?.activeRunId).toBe('')
    expect(chat.session.value?.title).toBe('renamed')
    expect(chat.session.value?.revision).toBe(2)
    db.chat = old
    await chat.refresh()
    expect(chat.session.value?.activeRunId).toBe('')
    expect(chat.state.snapshot?.watermark).toBe(3)
  })
  it('stays disconnected after an active stream reports executor unavailability', async () => {
    await open()
    events[0]!({ type: 'ready', id: '', data: { watermark: 0 } })
    expect(chat.connected.value).toBe(true)
    events[0]!({
      type: 'unavailable',
      id: '',
      data: { code: 'ops_unavailable' },
    })
    await flushPromises()
    await vi.advanceTimersByTimeAsync(60000)
    expect(chat.connected.value).toBe(false)
    expect(streamOps).toHaveBeenCalledTimes(1)
  })
  it('clears a submitted draft after server masking, without relying on original text equality', async () => {
    await open()
    vi.mocked(opsApi.patch).mockImplementation(async (id, input) => {
      const s = db[id]!.session
      Object.assign(s, input, {
        revision: s.revision + 1,
        draft: input.draft?.replace('secret', '[MASKED]') ?? s.draft,
      })
      return structuredClone(s)
    })
    chat.editDraft('inspect secret')
    expect(await chat.send()).toBe(true)
    expect(chat.draft.value).toBe('')
    expect(db.chat!.session.draft).toBe('')
    expect(await chat.send()).toBe(false)
    expect(opsApi.turn).toHaveBeenCalledOnce()
  })
  it('retains the exact idempotent payload across ambiguous HTTP 500 responses', async () => {
    await open()
    chat.editDraft('request')
    vi.mocked(opsApi.turn).mockRejectedValueOnce(
      new HttpError(
        500,
        { code: 'ops_storage_failed', message: 'failed' },
        'failed',
      ),
    )
    expect(await chat.send()).toBe(false)
    expect(chat.pending.value).not.toBeNull()
    chat.editDraft('new typing')
    expect(await chat.flush()).toBe(false)
    expect(await chat.submitPending()).toBe(true)
    expect(vi.mocked(opsApi.turn).mock.calls[0]).toEqual(
      vi.mocked(opsApi.turn).mock.calls[1],
    )
    expect(chat.draft.value).toBe('new typing')
  })
  it('preserves typing made while the submitted draft is being masked and saved', async () => {
    await open()
    const d = defer<Session>()
    vi.mocked(opsApi.patch).mockReturnValueOnce(d.promise)
    chat.editDraft('secret')
    const sending = chat.send()
    await flushPromises()
    chat.editDraft('new typing')
    db.chat!.session = { ...session(), revision: 2, draft: '[MASKED]' }
    d.resolve(structuredClone(db.chat!.session))
    expect(await sending).toBe(true)
    expect(chat.draft.value).toBe('new typing')
  })
  it('rapid submissions cannot replace the first in-flight request UUID', async () => {
    await open()
    const d = defer<never>()
    vi.mocked(opsApi.turn).mockReturnValueOnce(d.promise)
    const first = chat.tool('host_resources', {})
    const second = chat.tool('host_resources', {})
    await flushPromises()
    expect(opsApi.turn).toHaveBeenCalledTimes(1)
    const input = vi.mocked(opsApi.turn).mock.calls[0]![1]
    expect(chat.pending.value?.input).toEqual(input)
    expect(await second).toBe(false)
    d.reject(new HttpError(0, null, 'down'))
    expect(await first).toBe(false)
    expect(chat.pending.value?.input).toEqual(input)
  })
  it('does not advance CAS while an ambiguous request is unresolved', async () => {
    await open()
    chat.editDraft('original')
    vi.mocked(opsApi.turn).mockRejectedValueOnce(new HttpError(0, null, 'down'))
    expect(await chat.send()).toBe(false)
    const input = structuredClone(toRaw(chat.pending.value!.input))
    const patches = vi.mocked(opsApi.patch).mock.calls.length
    chat.editDraft('new typing')
    await vi.advanceTimersByTimeAsync(1000)
    expect(await chat.flush()).toBe(false)
    expect(await chat.rename('new title')).toBe(false)
    expect(opsApi.patch).toHaveBeenCalledTimes(patches)
    expect(chat.pending.value?.input).toEqual(input)
    expect(await chat.submitPending()).toBe(true)
    await vi.advanceTimersByTimeAsync(500)
    expect(vi.mocked(opsApi.turn).mock.calls[1]![1]).toEqual(input)
    expect(db.chat!.session.draft).toBe('new typing')
  })
  it('locks target changes until the first saved selection is acknowledged', async () => {
    await open()
    const d = defer<Session>()
    vi.mocked(opsApi.patch).mockReturnValueOnce(d.promise)
    const saving = chat.targets(['a', 'b'])
    await flushPromises()
    expect(chat.busy.value).toBe(true)
    expect(await chat.targets(['b'])).toBe(false)
    d.resolve({ ...session(), revision: 2, serverIds: ['a', 'b'] })
    expect(await saving).toBe(true)
    expect(chat.session.value?.serverIds).toEqual(['a', 'b'])
    expect(chat.busy.value).toBe(false)
  })
  it('heartbeat recovers the final snapshot after an event refresh failed', async () => {
    db.chat!.calls = [call()]
    await open()
    vi.mocked(opsApi.snapshot).mockRejectedValueOnce(
      new HttpError(0, null, 'down'),
    )
    events[0]!({
      type: 'entry',
      id: 'chat:1',
      data: {
        sessionId: 'chat',
        seq: 1,
        kind: 'call_status',
        callId: 'call',
        createdAt: stamp,
      },
    })
    await flushPromises()
    expect(chat.state.calls.call?.status).toBe('running')
    db.chat!.watermark = 1
    db.chat!.calls = [{ ...call('succeeded'), output: 'done' }]
    events[0]!({ type: 'heartbeat', id: '', data: { watermark: 1 } })
    await flushPromises()
    expect(chat.state.calls.call?.status).toBe('succeeded')
  })
  it('reset rebuilds a continuous history page without discarding unsaved text', async () => {
    db.chat!.entries = {
      entries: [
        {
          sessionId: 'chat',
          seq: 1,
          kind: 'user',
          text: 'old',
          createdAt: stamp,
        },
      ],
      cursor: 'chat:1',
      watermark: 1,
      reset: false,
    }
    db.chat!.watermark = 1
    await open()
    chat.editDraft('unsaved')
    db.chat!.entries = {
      entries: [
        {
          sessionId: 'chat',
          seq: 101,
          kind: 'assistant',
          text: 'new',
          createdAt: stamp,
        },
      ],
      cursor: 'chat:101',
      watermark: 150,
      reset: false,
    }
    db.chat!.watermark = 150
    events[0]!({ type: 'reset', id: '', data: { watermark: 150 } })
    await flushPromises()
    expect(chat.state.entries.map((e) => e.seq)).toEqual([101])
    expect(chat.historyCursor.value).toBe('chat:101')
    expect(chat.draft.value).toBe('unsaved')
    vi.mocked(opsApi.entries).mockResolvedValueOnce({
      entries: [
        {
          sessionId: 'chat',
          seq: 100,
          kind: 'user',
          text: 'middle',
          createdAt: stamp,
        },
      ],
      cursor: 'chat:100',
      watermark: 150,
      reset: false,
    })
    await chat.history()
    expect(opsApi.entries).toHaveBeenCalledWith('chat', 'chat:101')
    expect(chat.state.entries.map((e) => e.seq)).toEqual([100, 101])
  })
  it('explicit recovery refreshes capabilities and reconnects without replacing targets or draft', async () => {
    await open()
    chat.editDraft('unsaved')
    events[0]!({ type: 'unavailable', id: '', data: {} })
    await flushPromises()
    vi.mocked(opsApi.capabilities).mockResolvedValueOnce({
      available: true,
      modelAvailable: false,
      provider: { provider: '', model: '', configHash: '' },
      limits: {},
    })
    expect(await chat.recover()).toBe(true)
    await flushPromises()
    expect(chat.capabilities.value?.modelAvailable).toBe(false)
    expect(chat.session.value?.serverIds).toEqual(['a'])
    expect(chat.draft.value).toBe('unsaved')
    expect(streamOps).toHaveBeenCalledTimes(2)
    expect(chat.error.value).toBe('')
  })
  it('status bursts never evict visible history; empty older page ends pagination', async () => {
    db.chat!.entries = {
      entries: Array.from({ length: 100 }, (_, i) => ({
        sessionId: 'chat',
        seq: i + 101,
        kind: 'user',
        text: 'visible ' + i,
        createdAt: stamp,
      })),
      cursor: 'chat:101',
      watermark: 200,
      reset: false,
    }
    db.chat!.watermark = 200
    await open()
    for (let seq = 201; seq < 2500; seq++)
      events[0]!({
        type: 'entry',
        id: 'chat:' + seq,
        data: { sessionId: 'chat', seq, kind: 'run_status', createdAt: stamp },
      })
    await flushPromises()
    expect(chat.state.entries).toHaveLength(100)
    vi.mocked(opsApi.entries).mockResolvedValueOnce({
      entries: [
        {
          sessionId: 'chat',
          seq: 1,
          kind: 'user',
          text: 'oldest',
          createdAt: stamp,
        },
      ],
      cursor: 'chat:1',
      watermark: 200,
      reset: false,
    })
    await chat.history()
    expect(chat.state.entries[0]!.text).toBe('oldest')
    expect(chat.state.entries).toHaveLength(101)
    vi.mocked(opsApi.entries).mockResolvedValueOnce({
      entries: [],
      cursor: 'chat:1',
      watermark: 200,
      reset: false,
    })
    await chat.history()
    expect(chat.historyCursor.value).toBe('')
  })
  it('restores selected targets without overwriting with entry default, defaults only new sessions', async () => {
    await open(['b'])
    expect(chat.session.value?.serverIds).toEqual(['a'])
    await chat.create()
    expect(opsApi.create).toHaveBeenCalledWith(['b'])
    expect(chat.session.value?.serverIds).toEqual(['b'])
  })
  it('saves at 500ms and awaits the final dirty draft before close without canceling', async () => {
    await open()
    chat.editDraft('first')
    await vi.advanceTimersByTimeAsync(499)
    expect(opsApi.patch).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(1)
    expect(db.chat!.session.draft).toBe('first')
    chat.editDraft('last')
    expect(await chat.close()).toBe(true)
    expect(db.chat!.session.draft).toBe('last')
    expect(opsApi.cancel).not.toHaveBeenCalled()
  })
  it('retains abrupt-unmount unsaved text in memory, not browser storage', async () => {
    const storage = vi.spyOn(Storage.prototype, 'setItem')
    await open()
    chat.editDraft('unsaved')
    view!.unmount()
    view = undefined
    await open()
    expect(chat.draft.value).toBe('unsaved')
    expect(chat.draftStatus.value).toBe('dirty')
    expect(storage).not.toHaveBeenCalled()
    storage.mockRestore()
  })
  it('keeps failed save visible and prevents close/switch until handled', async () => {
    await open()
    vi.mocked(opsApi.patch).mockRejectedValueOnce(
      new HttpError(0, null, 'secret'),
    )
    chat.editDraft('local')
    expect(await chat.close()).toBe(false)
    expect(chat.draft.value).toBe('local')
    expect(chat.draftStatus.value).toBe('failed')
    expect(await chat.select('other')).toBe(true)
    expect(db.chat!.session.draft).toBe('local')
  })
  it('preserves CAS conflict and requires explicit remote or local choice', async () => {
    await open()
    chat.editDraft('local')
    db.chat!.session.revision++
    db.chat!.session.draft = 'remote'
    expect(await chat.flush()).toBe(false)
    expect(chat.draft.value).toBe('local')
    expect(chat.draftStatus.value).toBe('conflict')
    expect(await chat.select('other')).toBe(false)
    await chat.resolveDraft(true)
    expect(db.chat!.session.draft).toBe('local')
    expect(chat.draftStatus.value).toBe('saved')
  })
  it('reconciles masked ACK and never mistakes own canonical draft for a remote conflict', async () => {
    await open()
    vi.mocked(opsApi.patch).mockImplementationOnce(async (_id, input) => {
      db.chat!.session = {
        ...db.chat!.session,
        revision: 2,
        draft: input.draft!.replace('SECRET', '***'),
      }
      return structuredClone(db.chat!.session)
    })
    chat.editDraft('SECRET')
    expect(await chat.flush()).toBe(true)
    expect(chat.draft.value).toBe('***')
    await chat.refresh()
    expect(chat.draftStatus.value).toBe('saved')
  })
  it('preserves newer typing during masked ACK and saves it serially', async () => {
    await open()
    const d = defer<Session>()
    vi.mocked(opsApi.patch).mockReturnValueOnce(d.promise)
    chat.editDraft('SECRET')
    const saving = chat.flush()
    await flushPromises()
    chat.editDraft('new text')
    db.chat!.session = { ...db.chat!.session, revision: 2, draft: '***' }
    d.resolve(structuredClone(db.chat!.session))
    await saving
    expect(db.chat!.session.draft).toBe('new text')
    expect(chat.draft.value).toBe('new text')
  })
  it('does a trailing refresh when terminal call_status arrives during a stale snapshot', async () => {
    db.chat!.calls = [call()]
    await open()
    const d = defer<Snapshot>()
    vi.mocked(opsApi.snapshot).mockReturnValueOnce(d.promise)
    const stale = structuredClone(db.chat!)
    const first = chat.refresh()
    events[0]!({
      type: 'entry',
      id: 'chat:1',
      data: {
        sessionId: 'chat',
        seq: 1,
        kind: 'call_status',
        callId: 'call',
        createdAt: stamp,
      },
    })
    db.chat!.session.revision = 2
    db.chat!.calls = [
      { ...call('succeeded'), output: 'final output', exitCode: 0 },
    ]
    d.resolve(stale)
    await first
    expect(chat.session.value?.revision).toBe(2)
    expect(chat.state.calls.call?.status).toBe('succeeded')
    expect(chat.state.calls.call?.output).toBe('final output')
    expect(opsApi.snapshot).toHaveBeenCalledTimes(3)
  })
  it('loads historical calls outside the snapshot window by pure GET without replaying work', async () => {
    db.chat!.entries.cursor = 'chat:100'
    vi.mocked(opsApi.entries).mockResolvedValue({
      entries: [
        {
          sessionId: 'chat',
          seq: 1,
          kind: 'tool_call',
          callId: 'call',
          createdAt: stamp,
        },
      ],
      cursor: 'chat:1',
      watermark: 100,
      reset: false,
    })
    vi.mocked(opsApi.call).mockResolvedValue({
      ...call('succeeded'),
      output: 'historical output',
    })
    await open()
    await chat.history()
    await chat.refresh()
    expect(opsApi.call).toHaveBeenCalledOnce()
    expect(opsApi.call).toHaveBeenCalledWith('chat', 'call')
    expect(chat.state.calls.call?.serverName).toBe('Frozen A')
    expect(chat.state.calls.call?.object).toBe('original')
    expect(chat.state.calls.call?.output).toBe('historical output')
    expect(opsApi.turn).not.toHaveBeenCalled()
    expect(opsApi.retry).not.toHaveBeenCalled()
  })
  it('ignores a late snapshot after switching sessions', async () => {
    await open()
    const d = defer<Snapshot>()
    vi.mocked(opsApi.snapshot).mockReturnValueOnce(d.promise)
    const refreshing = chat.refresh()
    await chat.select('other')
    d.resolve(snapshot())
    await refreshing
    expect(chat.state.currentId).toBe('other')
    expect(chat.session.value?.id).toBe('other')
  })
  it('does not advance reset cursor when loading the replacement snapshot fails', async () => {
    await open()
    events[0]!({
      type: 'entry',
      id: 'chat:1',
      data: {
        sessionId: 'chat',
        seq: 1,
        kind: 'assistant',
        text: 'one',
        createdAt: stamp,
      },
    })
    await flushPromises()
    vi.mocked(opsApi.snapshot).mockRejectedValueOnce(
      new HttpError(0, null, 'down'),
    )
    events[0]!({ type: 'reset', id: '', data: { watermark: 999 } })
    await flushPromises()
    await vi.advanceTimersByTimeAsync(500)
    expect(vi.mocked(streamOps).mock.calls.at(-1)?.[1]).toBe('chat:1')
  })
  it('ready metadata does not skip replay and duplicate seq is ignored', async () => {
    await open()
    events[0]!({ type: 'ready', id: '', data: { watermark: 99 } })
    const entry = {
      sessionId: 'chat',
      seq: 1,
      kind: 'assistant',
      text: 'one',
      createdAt: stamp,
    }
    events[0]!({ type: 'entry', id: 'chat:1', data: entry })
    events[0]!({ type: 'entry', id: 'chat:1', data: entry })
    await flushPromises()
    expect(chat.state.entries.filter((e) => e.seq === 1)).toHaveLength(1)
  })
  it('uses per-preview generation so late consent data cannot replace a newer batch', async () => {
    await open()
    const d = defer<Preview>()
    const make = (id: string): Preview => ({
      callIds: [id],
      items: [],
      hash: id,
      provider: { provider: 'test', model: 'm', configHash: 'h' },
    })
    vi.mocked(opsApi.preview)
      .mockReturnValueOnce(d.promise)
      .mockResolvedValueOnce(make('new'))
    const first = chat.analyzePreview(['old'])
    await chat.analyzePreview(['new'])
    d.resolve(make('old'))
    await first
    expect(chat.preview.value?.callIds).toEqual(['new'])
  })
  it('retries ambiguous sends with identical UUID and payload, retaining new typing', async () => {
    await open()
    chat.editDraft('request')
    vi.mocked(opsApi.turn).mockRejectedValueOnce(new HttpError(0, null, 'down'))
    expect(await chat.send()).toBe(false)
    chat.editDraft('new typing')
    expect(await chat.submitPending()).toBe(true)
    expect(vi.mocked(opsApi.turn).mock.calls[0]).toEqual(
      vi.mocked(opsApi.turn).mock.calls[1],
    )
    expect(chat.draft.value).toBe('new typing')
  })
  it('does not patch or restore private content after auth invalidation during await', async () => {
    await open()
    const d = defer<Session>()
    vi.mocked(opsApi.patch).mockReturnValueOnce(d.promise)
    chat.editDraft('private')
    const saving = chat.flush()
    await flushPromises()
    resetOpsAuth()
    d.resolve({ ...session(), revision: 2, draft: 'private' })
    expect(await saving).toBe(false)
    expect(useOpsChatStore().state.sessions).toEqual([])
    expect(chat.draft.value).toBe('')
    expect(useOpsChatStore().state.drafts).toEqual({})
  })
  it('scopes explicit retries and analysis to the approved IDs and frozen provider', async () => {
    await open()
    await chat.retry(['failed-b'])
    expect(opsApi.retry).toHaveBeenCalledWith(
      'chat',
      expect.objectContaining({ callIds: ['failed-b'] }),
    )
    const p: Preview = {
      callIds: ['c'],
      items: [],
      hash: 'preview',
      provider: { provider: 'test', model: 'm', configHash: 'old' },
    }
    vi.mocked(opsApi.preview).mockResolvedValue(p)
    await chat.analyzePreview(['c'])
    vi.mocked(opsApi.analyze).mockRejectedValueOnce(
      new HttpError(
        409,
        { code: 'ops_consent_changed', message: 'raw' },
        'changed',
      ),
    )
    expect(await chat.analyze()).toBe(false)
    expect(opsApi.analyze).toHaveBeenCalledWith(
      'chat',
      expect.objectContaining({
        callIds: ['c'],
        provider: p.provider,
        previewHash: 'preview',
        consent: true,
      }),
    )
    expect(chat.error.value).toBe('ops_consent_changed')
  })
})
