import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h, reactive } from 'vue'
import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import ServerTerminal from './ServerTerminal.vue'
import { setLocale } from '../i18n'
import { resetOpsAuth } from '../utils/opsAuth'
import { completeCommand, previewTerminal } from '../api/aiOps'
import type { TerminalHandlers } from '../api/servers'
const m = vi.hoisted(() => ({
  input: undefined as unknown as (value: string) => void,
  key: undefined as unknown as (event: KeyboardEvent) => boolean,
  selection: 'selected error',
  alt: false,
  send: vi.fn(),
  paste: vi.fn(),
  open: vi.fn(),
  dispose: vi.fn(),
  attach: vi.fn(),
  prepare: vi.fn(),
  connections: [] as {
    handlers: TerminalHandlers
    close: ReturnType<typeof vi.fn>
  }[],
}))
let route = reactive({
  params: { id: 'server-a' },
  query: {} as Record<string, string>,
})
vi.mock('vue-router', () => ({
  useRoute: () => route,
  useRouter: () => ({ push: vi.fn() }),
}))
vi.mock('../router', () => ({ setDocumentTitle: vi.fn() }))
vi.mock('../api/aiOps', () => ({
  completeCommand: vi.fn(),
  previewTerminal: vi.fn(),
}))
vi.mock('../api/servers', () => ({
  listServers: vi.fn().mockResolvedValue([
    {
      id: 'server-a',
      name: 'Test A',
      user: 'test',
      host: '127.0.0.1',
      port: 22,
    },
  ]),
  openServerTerminal: vi.fn((_id: string, handlers: TerminalHandlers) => {
    const c = { handlers, close: vi.fn() }
    m.connections.push(c)
    return { send: m.send, close: c.close, resize: vi.fn() }
  }),
  openContainerTerminal: vi.fn(
    (_id: string, _name: string, handlers: TerminalHandlers) => {
      const c = { handlers, close: vi.fn() }
      m.connections.push(c)
      return { send: m.send, close: c.close, resize: vi.fn() }
    },
  ),
}))
vi.mock('../components/ops/AiOpsPanel.vue', () => ({
  default: defineComponent({
    props: ['terminalEpoch'],
    setup(_p, { expose }) {
      expose({
        terminalTarget: () => 'chat-binding',
        prepareTerminal: m.prepare,
        attachTerminal: m.attach,
        close: vi.fn(),
      })
      return () => h('aside', 'existing assistant')
    },
  }),
}))
vi.mock('@xterm/xterm', () => ({
  Terminal: class {
    cols = 80
    rows = 24
    options = {}
    buffer = {
      get active() {
        return { type: m.alt ? 'alternate' : 'normal', cursorX: 2, cursorY: 1 }
      },
    }
    loadAddon() {}
    attachCustomKeyEventHandler(handler: typeof m.key) {
      m.key = handler
    }
    open() {
      m.open()
    }
    onData(handler: typeof m.input) {
      m.input = handler
    }
    reset() {}
    focus() {}
    resize() {}
    clear() {}
    selectAll() {}
    write(_value: string, done?: () => void) {
      done?.()
    }
    dispose() {
      m.dispose()
    }
    getSelection() {
      return m.selection
    }
    hasSelection() {
      return !!m.selection
    }
    paste(text: string) {
      m.paste(text)
      m.input(m.alt ? '\x1b[200~' + text + '\x1b[201~' : text)
    }
  },
}))
vi.mock('@xterm/addon-fit', () => ({
  FitAddon: class {
    fit() {}
  },
}))
const provider = {
  provider: 'local-fake',
  model: 'fixture',
  configHash: 'hash',
}
let view: VueWrapper | undefined
async function open() {
  view = mount(ServerTerminal, { attachTo: document.body })
  await flushPromises()
  await vi.waitFor(() => expect(m.connections.length).toBeGreaterThan(0))
  m.connections.at(-1)!.handlers.onOpen?.()
  await flushPromises()
  m.send.mockClear()
  return view
}
function paste(text: string) {
  const event = new Event('paste', { bubbles: true, cancelable: true })
  Object.defineProperty(event, 'clipboardData', {
    value: { getData: () => text },
  })
  view!.get('.term-host').element.dispatchEvent(event)
  return event
}
function button(text: string) {
  return view!.findAll('button').find((el) => el.text().includes(text))!
}
function confirm() {
  return document.querySelector<HTMLButtonElement>('[data-confirm]')!
}
function cancel() {
  return document.querySelector<HTMLButtonElement>('[data-cancel]')!
}
beforeEach(() => {
  setLocale('en')
  vi.clearAllMocks()
  m.connections = []
  m.alt = false
  m.selection = 'selected error'
  route = reactive({
    params: { id: 'server-a' },
    query: {} as Record<string, string>,
  })
  localStorage.clear()
  m.prepare.mockResolvedValue('chat-binding')
  m.attach.mockReturnValue(true)
  vi.mocked(previewTerminal).mockImplementation(async (values) => ({
    values: [...values],
    provider,
  }))
  vi.mocked(completeCommand).mockResolvedValue({
    available: true,
    completion: 'ls -la',
  })
  Object.defineProperty(window, 'isSecureContext', {
    value: true,
    configurable: true,
  })
  Object.defineProperty(navigator, 'clipboard', {
    value: { readText: vi.fn().mockResolvedValue('ls') },
    configurable: true,
  })
})
afterEach(() => {
  view?.unmount()
  view = undefined
  resetOpsAuth()
  vi.useRealTimers()
  document.body.innerHTML = ''
})
describe('existing terminal input boundaries (no SSH/model)', () => {
  it('typing, paste and local completion never request a model automatically', async () => {
    await open()
    vi.useFakeTimers()
    m.input('ls')
    await vi.advanceTimersByTimeAsync(500)
    expect(previewTerminal).not.toHaveBeenCalled()
    expect(completeCommand).not.toHaveBeenCalled()
    paste('whoami')
    expect(m.paste).toHaveBeenCalledWith('whoami')
    expect(completeCommand).not.toHaveBeenCalled()
  })
  it.each([
    'echo a\necho b',
    'echo a\recho b',
    'echo a\r\necho b',
    'echo a\n',
    'echo a\x1b[31m',
    'echo a\x9b31m',
  ])('previews %j, cancel sends zero bytes', async (text) => {
    await open()
    const event = paste(text)
    await flushPromises()
    expect(event.defaultPrevented).toBe(true)
    expect(m.paste).not.toHaveBeenCalled()
    expect(m.send).not.toHaveBeenCalled()
    expect(confirm()).not.toBeNull()
    cancel().click()
    await flushPromises()
    expect(m.send).not.toHaveBeenCalled()
  })
  it.each([false, true])(
    'confirms exactly one paste, alternate=%s',
    async (alt) => {
      await open()
      m.alt = alt
      paste('echo a\necho b')
      await flushPromises()
      const confirmButton = confirm()
      confirmButton.click()
      confirmButton.click()
      await flushPromises()
      expect(m.paste).toHaveBeenCalledTimes(1)
      expect(m.send).toHaveBeenCalledTimes(1)
      expect(m.send.mock.calls[0]![0]).toBe(
        alt ? '\x1b[200~echo a\necho b\x1b[201~' : 'echo a\necho b',
      )
    },
  )
  it('invalidates paste on disconnect and ignores old handlers', async () => {
    await open()
    const old = m.connections[0]!
    paste('echo a\necho b')
    await flushPromises()
    button('Disconnect').element.dispatchEvent(new MouseEvent('click'))
    await flushPromises()
    expect(confirm()).toBeNull()
    old.handlers.onOpen?.()
    old.handlers.onClose?.('late')
    expect(view!.text()).not.toContain('LIVE')
    expect(m.send).not.toHaveBeenCalled()
  })
  it('drops delayed clipboard text after target change', async () => {
    await open()
    let resolve!: (text: string) => void
    vi.mocked(navigator.clipboard.readText).mockReturnValueOnce(
      new Promise((r) => (resolve = r)),
    )
    await view!.get('.term-host').trigger('contextmenu')
    await button('Paste').trigger('click')
    route.params.id = 'server-b'
    await flushPromises()
    resolve('echo old\n')
    await flushPromises()
    expect(m.paste).not.toHaveBeenCalled()
    expect(confirm()).toBeNull()
  })
  it('clears tracked input in sensitive mode and prevents sharing or model requests', async () => {
    await open()
    m.input('ls')
    await button('Sensitive mode: off').trigger('click')
    m.input('fake-password')
    expect(
      button('Request model completion').attributes('disabled'),
    ).toBeDefined()
    expect(button('Share selected output').attributes('disabled')).toBeDefined()
    expect(previewTerminal).not.toHaveBeenCalled()
    expect(completeCommand).not.toHaveBeenCalled()
  })
  it('requires a model preview and explicit confirm, then never executes completion', async () => {
    await open()
    m.input('ls')
    await button('Request model completion').trigger('click')
    await flushPromises()
    expect(completeCommand).not.toHaveBeenCalled()
    expect(document.body.textContent).toContain('local-fake')
    const modelConfirm = confirm()
    modelConfirm.click()
    modelConfirm.click()
    await flushPromises()
    expect(completeCommand).toHaveBeenCalledTimes(1)
    expect(m.send).toHaveBeenCalledTimes(1)
    expect(m.send).toHaveBeenCalledWith('ls')
    expect(view!.text()).toContain('-la')
  })
  it('rejects a late model result after new input or authentication loss', async () => {
    await open()
    m.input('ls')
    await button('Request model completion').trigger('click')
    await flushPromises()
    let resolve!: (value: { available: boolean; completion: string }) => void
    vi.mocked(completeCommand).mockReturnValueOnce(
      new Promise((r) => (resolve = r)),
    )
    confirm().click()
    await flushPromises()
    resetOpsAuth()
    resolve({ available: true, completion: 'ls -la' })
    await flushPromises()
    expect(confirm()).toBeNull()
    expect(view!.find('.caret-ghost').exists()).toBe(false)
  })
  it('opens folded assistant, previews a selection and attaches without a model call', async () => {
    await open()
    expect(view!.text()).not.toContain('existing assistant')
    await button('Share selected output').trigger('click')
    await flushPromises()
    expect(m.prepare).toHaveBeenCalledTimes(1)
    expect(m.attach).not.toHaveBeenCalled()
    expect(completeCommand).not.toHaveBeenCalled()
    confirm().click()
    await flushPromises()
    expect(m.attach).toHaveBeenCalledTimes(1)
    expect(m.attach.mock.calls[0]![0]).toContain('Untrusted terminal text')
    expect(completeCommand).not.toHaveBeenCalled()
  })
  it('redacts edited selection again and requires another review if it changed', async () => {
    await open()
    await button('Share selected output').trigger('click')
    await flushPromises()
    const area = document.querySelector<HTMLTextAreaElement>(
      '.terminal-review textarea',
    )!
    area.value = 'password=fixture'
    area.dispatchEvent(new Event('input', { bubbles: true }))
    vi.mocked(previewTerminal).mockResolvedValueOnce({
      values: ['[MASKED]', 'server-a', '', '', '/bin/sh', 'stamp'],
      provider,
    })
    confirm().click()
    await flushPromises()
    expect(m.attach).not.toHaveBeenCalled()
    expect(area.value).toBe('[MASKED]')
  })
  it('rejects byte-over-limit paste instead of truncating or sending it', async () => {
    await open()
    paste('中'.repeat(22000))
    await flushPromises()
    expect(m.paste).not.toHaveBeenCalled()
    expect(confirm()).toBeNull()
  })
  it('serializes terminal initialization when connect is clicked twice', async () => {
    view = mount(ServerTerminal, { attachTo: document.body })
    const connect = button('Connect')
    await connect.trigger('click')
    await connect.trigger('click')
    await flushPromises()
    expect(m.open).toHaveBeenCalledTimes(1)
    expect(m.connections).toHaveLength(1)
  })
  it('keeps Ctrl+C interrupt pass-through and native keyboard paste without a duplicate clipboard read', async () => {
    await open()
    m.selection = ''
    expect(
      m.key(new KeyboardEvent('keydown', { key: 'c', ctrlKey: true })),
    ).toBe(true)
    expect(
      m.key(new KeyboardEvent('keydown', { key: 'v', ctrlKey: true })),
    ).toBe(false)
    expect(navigator.clipboard.readText).not.toHaveBeenCalled()
    paste('safe single line')
    expect(m.paste).toHaveBeenCalledTimes(1)
  })
  it('discards delayed preview on target change before any model call', async () => {
    await open()
    m.input('ls')
    let resolve!: (value: {
      values: string[]
      provider: typeof provider
    }) => void
    vi.mocked(previewTerminal).mockReturnValueOnce(
      new Promise((r) => (resolve = r)),
    )
    await button('Request model completion').trigger('click')
    route.query.container = 'new-container'
    await flushPromises()
    resolve({
      values: ['ls', 'server-a', '', '', '/bin/sh', 'stamp'],
      provider,
    })
    await flushPromises()
    expect(confirm()).toBeNull()
    expect(completeCommand).not.toHaveBeenCalled()
  })
  it('does not open or retain a late terminal after unmount during font initialization', async () => {
    let release!: () => void
    Object.defineProperty(document, 'fonts', {
      value: { load: () => new Promise<void>((r) => (release = r)) },
      configurable: true,
    })
    view = mount(ServerTerminal, { attachTo: document.body })
    await flushPromises()
    view.unmount()
    view = undefined
    release()
    await flushPromises()
    expect(m.open).not.toHaveBeenCalled()
    expect(m.dispose).toHaveBeenCalled()
    Object.defineProperty(document, 'fonts', {
      value: undefined,
      configurable: true,
    })
  })
  it('restores both review entry buttons after a reference attaches successfully', async () => {
    await open()
    await button('Share selected output').trigger('click')
    await flushPromises()
    confirm().click()
    await flushPromises()
    expect(m.attach).toHaveBeenCalledTimes(1)
    expect(
      button('Share selected output').attributes('disabled'),
    ).toBeUndefined()
    expect(
      button('Request model completion').attributes('disabled'),
    ).toBeUndefined()
  })
  it('cancels the pending single-character completion timer before explicit model review', async () => {
    await open()
    vi.useFakeTimers()
    m.input('l')
    await button('Request model completion').trigger('click')
    await flushPromises()
    let resolve!: (value: { available: boolean; completion: string }) => void
    let signal: AbortSignal | undefined
    vi.mocked(completeCommand).mockImplementationOnce(
      (_partial, _context, _provider, abort) => {
        signal = abort
        return new Promise((r) => (resolve = r))
      },
    )
    confirm().click()
    await flushPromises()
    await vi.advanceTimersByTimeAsync(500)
    expect(signal?.aborted).toBe(false)
    resolve({ available: true, completion: 'ls -la' })
    await flushPromises()
    expect(view!.find('.caret-ghost').exists()).toBe(true)
    expect(
      button('Request model completion').attributes('disabled'),
    ).toBeUndefined()
  })
  it.each(['resolve', 'reject'])(
    'ignores a stale clipboard %s after a newer native paste review on the same target',
    async (mode) => {
      await open()
      let resolve!: (text: string) => void, reject!: (error: Error) => void
      vi.mocked(navigator.clipboard.readText).mockReturnValueOnce(
        new Promise((yes, no) => {
          resolve = yes
          reject = no
        }),
      )
      await view!.get('.term-host').trigger('contextmenu')
      await button('Paste').trigger('click')
      paste('new command\n')
      await flushPromises()
      if (mode === 'resolve') resolve('stale single line')
      else reject(new Error('old denial'))
      await flushPromises()
      expect(m.paste).not.toHaveBeenCalled()
      expect(m.send).not.toHaveBeenCalled()
      expect(
        document.querySelector<HTMLTextAreaElement>(
          '.terminal-review textarea',
        )!.value,
      ).toBe('new command\n')
      expect(view!.text()).not.toContain('Clipboard read denied')
    },
  )
  it('only applies the newest of two pending clipboard reads', async () => {
    await open()
    let first!: (text: string) => void, second!: (text: string) => void
    vi.mocked(navigator.clipboard.readText)
      .mockReturnValueOnce(new Promise((r) => (first = r)))
      .mockReturnValueOnce(new Promise((r) => (second = r)))
    await view!.get('.term-host').trigger('contextmenu')
    await button('Paste').trigger('click')
    await view!.get('.term-host').trigger('contextmenu')
    await button('Paste').trigger('click')
    second('new single line')
    await flushPromises()
    first('old single line')
    await flushPromises()
    expect(m.paste).toHaveBeenCalledTimes(1)
    expect(m.paste).toHaveBeenCalledWith('new single line')
  })
  it.each(['resolve', 'reject'])(
    'keeps review Cancel focused when an older real clipboard write later %ss',
    async (mode) => {
      await open()
      let resolve!: () => void, reject!: (error: Error) => void
      Object.defineProperty(navigator, 'clipboard', {
        configurable: true,
        value: {
          readText: vi.fn(),
          writeText: () =>
            new Promise<void>((yes, no) => {
              resolve = yes
              reject = no
            }),
        },
      })
      const fallback = vi.fn().mockReturnValue(false)
      Object.defineProperty(document, 'execCommand', {
        configurable: true,
        value: fallback,
      })
      ;(view!.get('.term-host').element as HTMLElement).focus()
      await view!.get('.term-host').trigger('mouseup')
      paste('review first\n')
      await flushPromises()
      const focusedCancel = cancel()
      expect(document.activeElement).toBe(focusedCancel)
      if (mode === 'resolve') resolve()
      else reject(new Error('late denial'))
      await flushPromises()
      expect(document.activeElement).toBe(focusedCancel)
      expect(fallback).not.toHaveBeenCalled()
      expect(document.querySelector('.copy-dialog')).toBeNull()
    },
  )
  it('blocks background PTY input while a review is open and resumes after cancel', async () => {
    await open()
    paste('review\n')
    await flushPromises()
    m.input('background')
    expect(m.send).not.toHaveBeenCalled()
    expect(confirm()).not.toBeNull()
    cancel().click()
    await flushPromises()
    m.input('normal input')
    expect(m.send).toHaveBeenCalledWith('normal input')
    m.send.mockClear()
    paste('review\n')
    await flushPromises()
    confirm().click()
    await flushPromises()
    expect(m.paste).toHaveBeenCalledTimes(1)
    expect(m.send).toHaveBeenCalledTimes(1)
  })
})
