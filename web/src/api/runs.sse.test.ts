import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { subscribeRunEvents } from './runs'
describe('run SSE polling cleanup', () => {
  afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals() })
  it('does not deliver a polled snapshot after the subscription was disposed', async () => {
    vi.useFakeTimers(); vi.stubGlobal('EventSource', undefined)
    let resolve!: (value: Response) => void
    vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>(r => { resolve = r })))
    const onStatus = vi.fn(); const onStep = vi.fn()
    const stop = subscribeRunEvents('old', { onStatus, onStep })
    await vi.advanceTimersByTimeAsync(3000); stop()
    resolve(new Response(JSON.stringify({ id: 'old', status: 'failed', steps: [{ id: 'old-row', status: 'failed' }] }), { status: 200, headers: { 'content-type': 'application/json' } })); await flushPromises()
    expect(onStatus).not.toHaveBeenCalled(); expect(onStep).not.toHaveBeenCalled()
  })
})

describe('team-bound run streams',()=>{
 afterEach(()=>{vi.useRealTimers();vi.unstubAllGlobals()})
 it('carries the captured team and suppresses queued events after abort',async()=>{
  let streamController!:ReadableStreamDefaultController<Uint8Array>
  const incoming=new ReadableStream<Uint8Array>({start(controller){streamController=controller;controller.enqueue(new TextEncoder().encode('event: status\ndata: {"runId":"r","status":"running"}\n\n'))}})
  const fetcher=vi.fn().mockResolvedValue(new Response(incoming,{headers:{'content-type':'text/event-stream'}}));vi.stubGlobal('fetch',fetcher)
  const signal=new AbortController(),onStatus=vi.fn(),onStep=vi.fn()
  const stop=subscribeRunEvents('r',{onStatus,onStep},{headers:{'X-Pipewright-Team':'team-a'},signal:signal.signal})
  await flushPromises();expect(new Headers(fetcher.mock.calls[0]![1].headers).get('X-Pipewright-Team')).toBe('team-a');expect(onStatus).toHaveBeenCalledWith({runId:'r',status:'running'})
  signal.abort();expect(fetcher.mock.calls[0]![1].signal.aborted).toBe(true);streamController.enqueue(new TextEncoder().encode('event: status\ndata: {"runId":"r","status":"success"}\n\n'));await flushPromises();expect(onStatus).toHaveBeenCalledOnce();stop()
 })
 it.each([401,403])('does not poll after authorization rejection %s',async(status)=>{
  vi.useFakeTimers();const fetcher=vi.fn().mockResolvedValue(new Response('',{status}));vi.stubGlobal('fetch',fetcher)
  const onAuthorizationError=vi.fn(),onStatus=vi.fn()
  const stop=subscribeRunEvents('r',{onStatus,onStep:vi.fn(),onAuthorizationError},{headers:{'X-Pipewright-Team':'team-a'}})
  await flushPromises();await vi.advanceTimersByTimeAsync(10000);expect(fetcher).toHaveBeenCalledOnce();expect(onAuthorizationError).toHaveBeenCalledOnce();expect(onStatus).not.toHaveBeenCalled();stop()
 })
 it('does not connect with an already revoked lifetime',()=>{
  const fetcher=vi.fn();vi.stubGlobal('fetch',fetcher);const controller=new AbortController();controller.abort()
  subscribeRunEvents('r',{onStatus:vi.fn(),onStep:vi.fn()},{headers:{'X-Pipewright-Team':'team-a'},signal:controller.signal});expect(fetcher).not.toHaveBeenCalled()
 })
})
