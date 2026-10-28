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
