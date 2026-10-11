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
  type Run,
  type Entry,
  type Confirmation,
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
async function open(initial = ['b'], context?:()=>string): Promise<void> {
  view = mount(
    defineComponent({
      setup() {
        chat = useOpsChat({
          active: shallowRef(true),
          initialServerIds: () => initial,
          context,
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
  it.each(['assistant', 'analysis'])('recovers omitted %s text by revision without downgrading newer replies', async (kind) => {
    const partial: Entry = {sessionId:'chat',seq:1,revisionSeq:1,kind,text:'partial',status:'partial',createdAt:stamp}
    db.chat!.watermark=1
    db.chat!.entries.entries=[partial]
    await open()
    db.chat!.watermark=100
    db.chat!.entries.entries=Array.from({length:50},(_,i)=>({sessionId:'chat',seq:i+51,kind:'assistant',text:'later turn',createdAt:stamp}))
    await chat.refresh()
    events[0]!({type:'entry',id:'chat:9',data:{...partial,seq:9,replaceSeq:1,revisionSeq:9,text:'complete reply',status:'succeeded'}})
    expect(chat.state.entries.find(e=>e.seq===1)).toMatchObject({text:'complete reply',status:'succeeded',revisionSeq:9})
    events[0]!({type:'entry',id:'chat:10',data:{...partial,seq:10,replaceSeq:1,revisionSeq:8}})
    expect(chat.state.entries.find(e=>e.seq===1)?.text).toBe('complete reply')
    db.chat!.entries.entries=[{...partial,revisionSeq:20,text:'authoritative newer reply',status:'succeeded'}]
    await chat.refresh()
    events[0]!({type:'entry',id:'chat:11',data:{...partial,seq:11,replaceSeq:1,revisionSeq:9}})
    expect(chat.state.entries.find(e=>e.seq===1)).toMatchObject({text:'authoritative newer reply',status:'succeeded',revisionSeq:20})
    expect(opsApi.turn).not.toHaveBeenCalled()
    expect(opsApi.retry).not.toHaveBeenCalled()
  })
  it.each(['running','succeeded'] as const)('authoritatively recovers a retained %s call omitted by the 50-entry snapshot window', async (status) => {
    db.chat!.watermark=1
    db.chat!.calls=[call(status)]
    db.chat!.entries.entries=[{sessionId:'chat',seq:1,kind:'tool_call',callId:'call',createdAt:stamp}]
    await open()
    const d=defer<Call>()
    vi.mocked(opsApi.call).mockReturnValueOnce(d.promise)
    db.chat!.watermark=100
    db.chat!.calls=[]
    db.chat!.entries.entries=Array.from({length:50},(_,i)=>({sessionId:'chat',seq:i+51,kind:'assistant',text:'later turn',createdAt:stamp}))
    const refreshing=chat.refresh()
    await flushPromises()
    events[0]!({type:'entry',id:'chat:2',data:{sessionId:'chat',seq:2,kind:'call_status',callId:'call',call:{...call('succeeded'),output:'untrusted delayed projection'},createdAt:stamp}})
    await flushPromises()
    expect(opsApi.call).toHaveBeenCalledOnce()
    expect(chat.state.calls.call?.output).toBe('')
    d.resolve({...call('succeeded'),output:'authoritative final facts'})
    await refreshing
    await flushPromises()
    expect(chat.state.calls.call).toMatchObject({status:'succeeded',output:'authoritative final facts'})
    events[0]!({type:'entry',id:'chat:3',data:{sessionId:'chat',seq:3,kind:'call_status',call:call(),createdAt:stamp}})
    await flushPromises()
    expect(chat.state.calls.call?.output).toBe('authoritative final facts')
    expect(opsApi.call).toHaveBeenCalledOnce()
  })
  it('does not let an older recovery GET replace a newer live call projection', async () => {
    db.chat!.calls=[call()]
    await open()
    const d=defer<Call>()
    vi.mocked(opsApi.call).mockReturnValueOnce(d.promise)
    db.chat!.watermark=100
    db.chat!.calls=[]
    const refreshing=chat.refresh()
    await flushPromises()
    events[0]!({type:'entry',id:'chat:101',data:{sessionId:'chat',seq:101,kind:'call_status',call:{...call('succeeded'),output:'newer live facts'},createdAt:stamp}})
    d.resolve(call())
    await refreshing
    expect(chat.state.calls.call).toMatchObject({status:'succeeded',output:'newer live facts'})
  })
  it('retries failed snapshot hints on equal-watermark heartbeats until recovery completes', async () => {
    await open()
    vi.mocked(opsApi.snapshot).mockRejectedValueOnce(new HttpError(0,null,'down')).mockRejectedValueOnce(new HttpError(0,null,'still down'))
    events[0]!({type:'entry',id:'chat:1',data:{sessionId:'chat',seq:1,kind:'snapshot_required',createdAt:stamp}})
    events[0]!({type:'entry',id:'chat:2',data:{sessionId:'chat',seq:2,kind:'session_updated',session:{...session(),watermark:2},createdAt:stamp}})
    await vi.advanceTimersByTimeAsync(100)
    expect(chat.state.snapshot?.watermark).toBe(2)
    db.chat!.watermark=2
    db.chat!.calls=[{...call('succeeded'),output:'recovered hint facts'}]
    events[0]!({type:'heartbeat',id:'',data:{watermark:2}})
    await flushPromises()
    expect(opsApi.snapshot).toHaveBeenCalledTimes(3)
    expect(chat.state.calls.call).toBeUndefined()
    events[0]!({type:'heartbeat',id:'',data:{watermark:2}})
    await flushPromises()
    expect(opsApi.snapshot).toHaveBeenCalledTimes(4)
    expect(chat.state.calls.call?.output).toBe('recovered hint facts')
    events[0]!({type:'heartbeat',id:'',data:{watermark:2}})
    await flushPromises()
    expect(opsApi.snapshot).toHaveBeenCalledTimes(4)
  })
  it('keeps a hint gap pending when snapshot hydration fails and retries the omitted call on heartbeat', async () => {
    db.chat!.calls=[call()]
    await open()
    vi.mocked(opsApi.call).mockRejectedValueOnce(new HttpError(0,null,'down')).mockResolvedValueOnce({...call('succeeded'),output:'old call final facts'})
    events[0]!({type:'entry',id:'chat:1',data:{sessionId:'chat',seq:1,kind:'snapshot_required',createdAt:stamp}})
    events[0]!({type:'entry',id:'chat:2',data:{sessionId:'chat',seq:2,kind:'session_updated',session:{...session(),watermark:2},createdAt:stamp}})
    db.chat!.watermark=2
    db.chat!.calls=[]
    await vi.advanceTimersByTimeAsync(100)
    expect(chat.state.calls.call?.status).toBe('running')
    events[0]!({type:'heartbeat',id:'',data:{watermark:2}})
    await flushPromises()
    expect(opsApi.call).toHaveBeenCalledTimes(2)
    expect(chat.state.calls.call?.output).toBe('old call final facts')
    const requests=vi.mocked(opsApi.snapshot).mock.calls.length
    events[0]!({type:'heartbeat',id:'',data:{watermark:2}})
    await flushPromises()
    expect(opsApi.snapshot).toHaveBeenCalledTimes(requests)
  })
  it('updates the exposed clean composer on a remote draft event, but preserves dirty text', async () => {
    await open()
    const remote = {...session(),revision:2,draft:'remote',watermark:1}
    db.chat!.session = remote
    events[0]!({type:'entry',id:'chat:1',data:{sessionId:'chat',seq:1,kind:'session_updated',session:remote,createdAt:stamp}})
    expect(chat.draft.value).toBe('remote')
    expect(chat.dirty.value).toBe(false)
    expect(chat.state.drafts.chat).toMatchObject({text:'remote',baseline:'remote',status:'saved'})
    chat.editDraft('local')
    events[0]!({type:'entry',id:'chat:2',data:{sessionId:'chat',seq:2,kind:'session_updated',session:{...remote,revision:3,draft:'new remote'},createdAt:stamp}})
    expect(chat.draft.value).toBe('local')
    expect(chat.draftStatus.value).toBe('conflict')
    expect(await chat.flush()).toBe(false)
    expect(opsApi.patch).not.toHaveBeenCalled()
  })
  it.each(['session','run','call','confirmation','text','optimistic'])('rejects every late %s projection covered by a newer snapshot', async (kind) => {
    await open()
    const run: Run = {id:'run',sessionId:'chat',clientRequestId:'request',status:'succeeded',cancelRequested:false,createdAt:stamp}
    db.chat!.watermark = 10
    db.chat!.session = {...session(),revision:3,draft:'final',watermark:10}
    db.chat!.runs = [run]
    db.chat!.calls = [{...call('succeeded'),output:'full facts'}]
    db.chat!.entries.entries = [{sessionId:'chat',seq:1,revisionSeq:10,kind:'assistant',status:'succeeded',text:'complete',createdAt:stamp}]
    await chat.refresh()
    const entry: Entry = {sessionId:'chat',seq:9,kind:'run_status',createdAt:stamp}
    if(kind==='session') entry.session={...session(),revision:3,draft:'stale',activeRunId:'run'}
    if(kind==='run') entry.run={...run,status:'running'}
    if(kind==='call') entry.call=call()
    if(kind==='confirmation') entry.confirmation={runId:'run',nonce:'old',expiresAt:stamp,calls:[],valid:true}
    if(kind==='text') Object.assign(entry,{kind:'assistant',replaceSeq:1,revisionSeq:9,status:'partial',text:'stale'})
    if(kind==='optimistic') {
      chat.state.optimistic=[{...entry,seq:-1,kind:'user',requestId:'pending',text:'pending'}]
      entry.requestId='pending'
    }
    const before = structuredClone(toRaw(chat.state.snapshot))
    events[0]!({type:'entry',id:'chat:9',data:entry})
    events[0]!({type:'entry',id:'chat:10',data:{...entry,seq:10}})
    expect(chat.state.snapshot).toEqual(before)
    expect(chat.state.calls.call).toMatchObject({status:'succeeded',output:'full facts'})
    expect(chat.state.entries[0]).toMatchObject({text:'complete',status:'succeeded'})
    expect(chat.draft.value).toBe('final')
    expect(chat.draftStatus.value).toBe('saved')
    expect(chat.state.optimistic).toHaveLength(kind==='optimistic'?1:0)
  })
  it.each(['queued','running','succeeded'] as const)('clears consumed confirmation on a cross-tab %s event and snapshot', async (status) => {
    const confirmation: Confirmation = {runId:'run',nonce:'approval',expiresAt:stamp,calls:[],valid:true}
    const run: Run = {id:'run',sessionId:'chat',clientRequestId:'request',status:'awaiting_confirmation',cancelRequested:false,createdAt:stamp}
    db.chat!.session.activeRunId='run'
    db.chat!.runs=[run]
    db.chat!.confirmations=[confirmation]
    await open()
    expect(chat.confirmations.value).toHaveLength(1)
    events[0]!({type:'entry',id:'chat:1',data:{sessionId:'chat',seq:1,kind:'run_status',run:{...run,status},createdAt:stamp}})
    expect(chat.state.snapshot?.confirmations).toHaveLength(0)
    expect(chat.confirmations.value).toHaveLength(0)
    db.chat!.watermark=2
    db.chat!.runs=[{...run,status}]
    await chat.refresh()
    expect(chat.state.snapshot?.confirmations).toHaveLength(0)
  })
  it('recovers full escaped output from a snapshot hint even when a later event races the snapshot', async () => {
    await open()
    const d=defer<Snapshot>()
    vi.mocked(opsApi.snapshot).mockReturnValueOnce(d.promise)
    events[0]!({type:'entry',id:'chat:1',data:{sessionId:'chat',seq:1,kind:'snapshot_required',createdAt:stamp}})
    await vi.advanceTimersByTimeAsync(100)
    const stale=structuredClone(db.chat!)
    stale.watermark=1
    db.chat!.watermark=2
    db.chat!.calls=[{...call('succeeded'),output:'\x01'.repeat(65536)}]
    events[0]!({type:'entry',id:'chat:2',data:{sessionId:'chat',seq:2,kind:'session_updated',session:{...session(),watermark:2},createdAt:stamp}})
    d.resolve(stale)
    await flushPromises()
    expect(chat.state.calls.call?.output).toBe('\x01'.repeat(65536))
    expect(chat.state.snapshot?.watermark).toBe(2)
    expect(streamOps).toHaveBeenCalledOnce()
    expect(opsApi.snapshot).toHaveBeenCalledTimes(3)
  })
  it('does not replace a live call with an older history projection or in-flight historical GET', async () => {
    db.chat!.entries.cursor='chat:100'
    await open()
    const d=defer<Call>()
    vi.mocked(opsApi.call).mockReturnValueOnce(d.promise)
    vi.mocked(opsApi.entries).mockResolvedValueOnce({entries:[{sessionId:'chat',seq:1,kind:'tool_call',callId:'call',createdAt:stamp}],cursor:'chat:1',watermark:0,reset:false})
    const loading=chat.history()
    await flushPromises()
    events[0]!({type:'entry',id:'chat:2',data:{sessionId:'chat',seq:2,kind:'call_status',call:{...call('succeeded'),output:'final'},createdAt:stamp}})
    d.resolve(call())
    await loading
    vi.mocked(opsApi.entries).mockResolvedValueOnce({entries:[{sessionId:'chat',seq:1,kind:'tool_call',call:call(),createdAt:stamp}],cursor:'',watermark:1,reset:false})
    await chat.history()
    expect(chat.state.calls.call).toMatchObject({status:'succeeded',output:'final'})
  })
  it('keeps the draft and makes no turn request when no targets are selected', async () => {
    db.chat!.session.serverIds = []
    await open([])
    chat.editDraft('check resources')
    expect(await chat.send()).toBe(false)
    expect(chat.draft.value).toBe('check resources')
    expect(opsApi.turn).not.toHaveBeenCalled()
    expect(chat.state.optimistic).toHaveLength(0)
    expect(await chat.targets(['a'])).toBe(true)
    expect(await chat.send()).toBe(true)
    expect(opsApi.turn).toHaveBeenCalledOnce()
  })
  it('restores independent entry sessions, preserving drafts and refusing a failed-save switch',async()=>{
    const context=shallowRef('containers'),contexts:Record<string,string>={containers:'chat',overview:'other'}
    vi.mocked(opsApi.list).mockImplementation(async(key='')=>({sessions:Object.values(db).map(s=>structuredClone(s.session)),activeSessionId:contexts[key]??'',cursor:''}))
    vi.mocked(opsApi.activate).mockImplementation(async(id,key='')=>{contexts[key]=id;return {ok:true}})
    await open(['b'],()=>context.value)
    chat.editDraft('container draft')
    context.value='overview';await flushPromises()
    expect(chat.session.value?.id).toBe('other')
    expect(db.chat!.session.draft).toBe('container draft')
    chat.editDraft('overview draft')
    context.value='containers';await flushPromises()
    expect(chat.draft.value).toBe('container draft')
    expect(db.other!.session.draft).toBe('overview draft')
    chat.editDraft('unsaved')
    vi.mocked(opsApi.patch).mockRejectedValueOnce(new HttpError(0,null,'down'))
    context.value='overview';await flushPromises()
    expect(chat.session.value?.id).toBe('chat')
    expect(chat.draft.value).toBe('unsaved')
    expect(contexts.overview).toBe('other')
    expect(opsApi.cancel).not.toHaveBeenCalled()
  })
  it('streams stable revisions without full snapshots, ignores stale history, and reconciles a lost final event', async () => {
    await open()
    const initial=vi.mocked(opsApi.snapshot).mock.calls.length
    const send=(seq:number,text:string,status:'partial'|'succeeded',replaceSeq?:number)=>events[0]!({type:'entry',id:'chat:'+seq,data:{sessionId:'chat',seq,revisionSeq:seq,replaceSeq,kind:'assistant',text,status,createdAt:stamp}})
    send(1,'part','partial')
    const stable=chat.state.entries[0]
    for(let seq=2;seq<=30;seq++) send(seq,'part '+seq,'partial',1)
    expect(chat.state.entries).toHaveLength(1)
    expect(chat.state.entries[0]?.text).toBe('part 30')
    expect(opsApi.snapshot).toHaveBeenCalledTimes(initial)
    db.chat!.entries.entries=[{...stable!,text:'complete',status:'succeeded',revisionSeq:31}]
    db.chat!.watermark=31
    db.chat!.session.watermark=31
    events[0]!({type:'heartbeat',id:'',data:{watermark:31}})
    await flushPromises()
    expect(chat.state.entries).toHaveLength(1)
    expect(chat.state.entries[0]?.status).toBe('succeeded')
    expect(chat.state.entries[0]?.text).toBe('complete')
    db.chat!.entries.entries=[stable!]
    await chat.refresh()
    expect(chat.state.entries[0]?.text).toBe('complete')
  })
  it('immediately presents one optimistic request while a save waits, then deduplicates the persisted user entry', async () => {
    await open()
    const d=defer<Session>()
    vi.mocked(opsApi.patch).mockReturnValueOnce(d.promise)
    chat.editDraft('inspect')
    const sending=chat.send()
    expect(chat.state.optimistic).toHaveLength(1)
    expect(await chat.send()).toBe(false)
    await flushPromises()
    const id=chat.state.optimistic[0]!.requestId
    d.resolve({...session(),revision:2,draft:'inspect'})
    await sending
    events[0]!({type:'entry',id:'chat:1',data:{sessionId:'chat',seq:1,kind:'user',text:'inspect',requestId:id,createdAt:stamp}})
    expect(chat.state.optimistic).toHaveLength(0)
    expect(chat.state.entries.filter(e=>e.kind==='user')).toHaveLength(1)
    expect(opsApi.turn).toHaveBeenCalledOnce()
    expect(vi.mocked(opsApi.turn).mock.calls[0]?.[1]).toMatchObject({analyzeResults:true,provider:{configHash:'hash'}})
  })
  it('typing never proxies or replaces a 300-result history', async () => {
    db.chat!.entries.entries=Array.from({length:300},(_,i)=>({sessionId:'chat',seq:i+1,kind:'assistant',text:'x'.repeat(8192),createdAt:stamp}))
    await open()
    const entries=chat.state.entries,first=entries[0],calls=chat.state.calls
    for(let i=0;i<100;i++)chat.editDraft('typing '+i)
    expect(chat.state.entries).toBe(entries)
    expect(chat.state.entries[0]).toBe(first)
    expect(chat.state.calls).toBe(calls)
    expect(opsApi.snapshot).toHaveBeenCalledOnce()
  })
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
    await vi.advanceTimersByTimeAsync(100)
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
    expect(opsApi.create).toHaveBeenCalledWith(['b'], '')
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
    await vi.advanceTimersByTimeAsync(100)
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
    expect(await chat.analyze(['c'])).toBe(false)
    expect(opsApi.analyze).toHaveBeenCalledWith(
      'chat',
      expect.objectContaining({
        callIds: ['c'],
        provider: {provider:'test',model:'m',configHash:'hash'},
        analyzeResults: true,
      }),
    )
    expect(chat.error.value).toBe('ops_consent_changed')
  })
})
