import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { openContainerTerminal, openServerTerminal } from './servers'
import { http, HttpError } from './http'

vi.mock('./http', async importOriginal => {
  const original = await importOriginal<typeof import('./http')>()
  return { ...original, http: { get: vi.fn(), post: vi.fn(), delete: vi.fn() } }
})
class Socket {
  static instances: Socket[] = []
  binaryType = ''
  onopen?: () => void
  onmessage?: (event: { data: unknown }) => void
  onclose?: (event: { reason: string }) => void
  onerror?: () => void
  send = vi.fn()
  close = vi.fn()
  constructor(public url: string) { Socket.instances.push(this) }
  open(): void { this.onopen?.() }
  ready(): void { this.onmessage?.({ data: '{"type":"ready"}' }) }
  fail(reason = ''): void { this.onclose?.({ reason }) }
}
const flush = async (): Promise<void> => { for (let i = 0; i < 12; i++) await Promise.resolve() }
describe('terminal transport start barrier and HTTP compatibility', () => {
  beforeEach(() => {
    vi.useFakeTimers(); vi.clearAllMocks(); Socket.instances = []; vi.stubGlobal('WebSocket', Socket)
    vi.mocked(http.delete).mockResolvedValue(undefined)
    vi.mocked(http.get).mockImplementation(() => new Promise(() => {}))
    vi.mocked(http.post).mockResolvedValue({ id: 'session' })
  })
  afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals() })
  it('waits for ready and never falls back after sending start', async () => {
    const onOpen = vi.fn(), onClose = vi.fn(), onData = vi.fn()
    const c = openServerTerminal('s', { onOpen, onClose, onData })
    const ws = Socket.instances[0]!
    expect(ws.url).toContain('/terminal/ws-v2?')
    ws.open(); expect(ws.send).toHaveBeenCalledWith('{"type":"start"}'); expect(onOpen).not.toHaveBeenCalled()
    c.send('dangerous'); expect(ws.send).toHaveBeenCalledTimes(1)
    ws.ready(); expect(onOpen).toHaveBeenCalledOnce(); c.send('hello'); expect(ws.send).toHaveBeenLastCalledWith('hello')
    ws.fail('SSH failed'); await flush(); expect(http.post).not.toHaveBeenCalled(); expect(onClose).toHaveBeenCalledWith('SSH failed')
  })
  it('locks HTTP before late open, waits for renderer before cursor ACK', async () => {
    let rendered!: () => void
    vi.mocked(http.get).mockResolvedValueOnce({ data: btoa('abc'), next: 3, closed: false, reason: '' })
    const onData = vi.fn(() => new Promise<void>(resolve => { rendered = resolve })), onTransport = vi.fn()
    const c = openContainerTerminal('s', 'app', { onData, onTransport })
    const ws = Socket.instances[0]!
    await vi.advanceTimersByTimeAsync(3000); ws.open(); ws.ready(); await flush()
    expect(ws.send).not.toHaveBeenCalled(); expect(onTransport).toHaveBeenCalledWith('http')
    expect(onData).toHaveBeenCalledOnce(); expect(http.get).toHaveBeenCalledTimes(1)
    rendered(); await flush(); expect(http.get).toHaveBeenLastCalledWith('/api/servers/s/containers/app/terminal/sessions/session?after=3', expect.any(Object))
    c.close()
  })
  it('fallback sends input and resize in order, never retries uncertain input', async () => {
    const onClose = vi.fn();const c = openServerTerminal('s', { onData: vi.fn(), onClose })
    Socket.instances[0]!.fail(); await flush()
    vi.mocked(http.post).mockResolvedValueOnce(undefined).mockRejectedValueOnce(new HttpError(0, null, 'network'))
    c.send('中\x03'); c.resize(100, 30); c.send('last'); await flush()
    const inputs = vi.mocked(http.post).mock.calls.filter(call => call[0].endsWith('/input'))
    expect(inputs).toHaveLength(2); expect(inputs[0]![1]).toEqual({ seq: 1, data: '5LitAw==' }) // UTF-8 中 + Ctrl+C
    expect(inputs[1]![1]).toEqual({ seq: 2, cols: 100, rows: 30 }); expect(onClose).toHaveBeenCalledOnce()
  })
  it('does not revive callbacks after close racing creation', async () => {
    let created!: (value: unknown) => void
    vi.mocked(http.post).mockImplementationOnce(() => new Promise(resolve => { created = resolve }))
    const onOpen = vi.fn(), onClose = vi.fn();const c = openServerTerminal('s', { onData: vi.fn(), onOpen, onClose })
    Socket.instances[0]!.fail(); await flush(); c.close(); created({ id: 'late' }); await flush()
    expect(onOpen).not.toHaveBeenCalled();expect(onClose).not.toHaveBeenCalled()
    expect(http.delete).toHaveBeenCalledWith('/api/servers/s/terminal/sessions/late', expect.any(Object))
  })
  it('start timeout never creates HTTP and old server fails with update guidance', async () => {
    const onClose = vi.fn();const c = openServerTerminal('s', { onData: vi.fn(), onClose })
    Socket.instances[0]!.open(); await vi.advanceTimersByTimeAsync(15000)
    expect(http.post).not.toHaveBeenCalled();expect(onClose).toHaveBeenCalledOnce();c.close()
    vi.mocked(http.post).mockRejectedValueOnce(new HttpError(404, null, 'Not Found'))
    const close2 = vi.fn();openServerTerminal('s', { onData: vi.fn(), onClose: close2 });Socket.instances[1]!.fail();await flush()
    expect(close2).toHaveBeenCalledOnce();expect(Socket.instances).toHaveLength(2)
  })

  it('bounds an uncertain create request without retrying it', async () => {
    vi.mocked(http.post).mockImplementationOnce((_url, _body, options) => new Promise((_resolve, reject) => {
      options?.signal?.addEventListener('abort', () => reject(new HttpError(0, null, 'timeout')))
    }))
    const onClose = vi.fn();openServerTerminal('s', { onData: vi.fn(), onClose })
    Socket.instances[0]!.fail();await flush();await vi.advanceTimersByTimeAsync(20000)
    expect(http.post).toHaveBeenCalledOnce();expect(onClose).toHaveBeenCalledOnce()
  })

  it('retries a failed output response at the last consumed cursor', async () => {
    vi.mocked(http.get)
      .mockResolvedValueOnce({ data: btoa('a'), next: 1, closed: false, reason: '' })
      .mockRejectedValueOnce(new HttpError(0, null, 'body connection terminated'))
      .mockResolvedValueOnce({ data: btoa('b'), next: 2, closed: true, reason: 'session ended' })
    const onData = vi.fn(), onClose = vi.fn()
    openServerTerminal('s', { onData, onClose })
    Socket.instances[0]!.fail(); await flush()
    expect(onClose).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(250); await flush()
    expect(vi.mocked(http.get).mock.calls.map(call => call[0])).toEqual([
      '/api/servers/s/terminal/sessions/session?after=0',
      '/api/servers/s/terminal/sessions/session?after=1',
      '/api/servers/s/terminal/sessions/session?after=1',
    ])
    expect(onData.mock.calls.map(call => new TextDecoder().decode(call[0]))).toEqual(['a', 'b'])
    expect(onClose).toHaveBeenCalledWith('session ended')
  })

  it('preserves an expired-session response instead of asking for an update', async () => {
    vi.mocked(http.get).mockRejectedValueOnce(new HttpError(404, {
      code: 'terminal_not_found', message: 'terminal expired',
    }, 'terminal expired'))
    const onClose = vi.fn()
    openServerTerminal('s', { onData: vi.fn(), onClose })
    Socket.instances[0]!.fail(); await flush()
    expect(onClose).toHaveBeenCalledWith('terminal expired')
    expect(http.post).toHaveBeenCalledOnce()
  })

  it('rejects an unsupported create payload before reporting a live terminal', async () => {
    vi.mocked(http.post).mockResolvedValueOnce('<html>proxy error</html>')
    const onOpen = vi.fn(), onClose = vi.fn()
    openServerTerminal('s', { onData: vi.fn(), onOpen, onClose })
    Socket.instances[0]!.fail(); await flush()
    expect(onOpen).not.toHaveBeenCalled()
    expect(onClose).toHaveBeenCalledWith(expect.stringMatching(/更新|update/i))
    expect(http.get).not.toHaveBeenCalled()
  })

  it('batches pending keystrokes while preserving the resize barrier', async () => {
    const c = openServerTerminal('s', { onData: vi.fn() })
    Socket.instances[0]!.fail(); await flush()
    let delivered!: () => void
    vi.mocked(http.post).mockImplementationOnce(() => new Promise<void>(resolve => { delivered = resolve }))
    c.send('a'); c.send('b'); c.send('c'); c.resize(120, 40); c.send('d'); c.send('e')
    await flush(); delivered(); await flush()
    const inputs = vi.mocked(http.post).mock.calls.filter(call => call[0].endsWith('/input'))
    expect(inputs.map(call => call[1])).toEqual([
      { seq: 1, data: btoa('a') },
      { seq: 2, data: btoa('bc') },
      { seq: 3, cols: 120, rows: 40 },
      { seq: 4, data: btoa('de') },
    ])
    c.close()
  })

  it('drains final output when EOF precedes an initial resize', async () => {
    let output!: (value: unknown) => void
    vi.mocked(http.get).mockImplementationOnce(() => new Promise(resolve => { output = resolve }))
    const onData = vi.fn(), onClose = vi.fn()
    let c!: ReturnType<typeof openServerTerminal>
    c = openServerTerminal('s', { onData, onClose, onOpen: () => c.resize(120, 40) })
    vi.mocked(http.post).mockResolvedValueOnce({ id: 'session' }).mockRejectedValueOnce(new HttpError(410, {
      code: 'terminal_ended', message: 'terminal ended',
    }, 'terminal ended'))
    Socket.instances[0]!.fail(); await flush()
    expect(onClose).not.toHaveBeenCalled()
    expect(http.delete).not.toHaveBeenCalled()
    c.send('must not run'); c.resize(140, 50); await flush()
    expect(http.post).toHaveBeenCalledTimes(2) // create, then initial resize only
    output({ data: btoa('shell not found\r\n'), next: 17, closed: true, reason: 'session ended' })
    await flush()
    expect(new TextDecoder().decode(onData.mock.calls[0]![0])).toBe('shell not found\r\n')
    expect(onClose).toHaveBeenCalledWith('session ended')
    expect(http.delete).toHaveBeenCalledOnce()
  })

  it('still ends immediately for an uncertain write returning 410', async () => {
    const onClose = vi.fn()
    const c = openServerTerminal('s', { onData: vi.fn(), onClose })
    Socket.instances[0]!.fail(); await flush()
    vi.mocked(http.post).mockRejectedValueOnce(new HttpError(410, {
      code: 'terminal_closed', message: 'write uncertain',
    }, 'write uncertain'))
    c.send('maybe sent'); await flush()
    expect(onClose).toHaveBeenCalledOnce()
    expect(http.delete).toHaveBeenCalledOnce()
  })
})
