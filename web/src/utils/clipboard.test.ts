import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { copyText, preserveFocusAndSelection } from './clipboard'
import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import ServerTerminal from '../views/ServerTerminal.vue'
import CopyTextDialog from '../components/ops/CopyTextDialog.vue'
import { openServerTerminal } from '../api/servers'

const terminalMocks = vi.hoisted(() => ({
  send: vi.fn(),
  close: vi.fn(),
  selection: 'terminal selected output',
}))
vi.mock('vue-router', () => ({
  useRoute: () => ({ params: { id: 'server-a' }, query: {} }),
  useRouter: () => ({ push: vi.fn() }),
}))
vi.mock('../router', () => ({ setDocumentTitle: vi.fn() }))
vi.mock('../api/servers', () => ({
  listServers: vi.fn().mockResolvedValue([]),
  openServerTerminal: vi.fn(() => ({
    send: terminalMocks.send,
    close: terminalMocks.close,
    resize: vi.fn(),
  })),
  openContainerTerminal: vi.fn(),
}))
vi.mock('../api/aiOps', () => ({ completeCommand: vi.fn() }))
vi.mock('@xterm/xterm', () => ({
  Terminal: class {
    cols = 80
    rows = 24
    options = {}
    loadAddon() {}
    attachCustomKeyEventHandler() {}
    open() {}
    onData() {}
    reset() {}
    focus() {}
    dispose() {}
    getSelection() {
      return terminalMocks.selection
    }
    hasSelection() {
      return terminalMocks.selection.length > 0
    }
  },
}))
vi.mock('@xterm/addon-fit', () => ({
  FitAddon: class {
    fit() {}
  },
}))
let terminal: VueWrapper | undefined

const exec = vi.fn()
beforeEach(() => {
  Object.defineProperty(document, 'execCommand', {
    configurable: true,
    value: exec,
  })
  exec.mockReset().mockReturnValue(false)
  Object.defineProperty(navigator, 'clipboard', {
    configurable: true,
    value: undefined,
  })
})
afterEach(() => {
  terminal?.unmount()
  terminal = undefined
  vi.restoreAllMocks()
  document.body.replaceChildren()
  window.getSelection()?.removeAllRanges()
})

describe('ServerTerminal copy entry (mocked terminal, no SSH)', () => {
  async function openTerminal() {
    localStorage.clear()
    terminalMocks.selection = 'terminal selected output'
    terminalMocks.send.mockClear()
    vi.mocked(openServerTerminal).mockClear()
    terminal = mount(ServerTerminal, {
      attachTo: document.body,
      global: { stubs: { AiOpsPanel: true } },
    })
    await flushPromises()
    await vi.waitFor(() => expect(openServerTerminal).toHaveBeenCalled())
    await flushPromises()
    return terminal
  }

  it.each(['missing', 'denied'])(
    'does not show a copied toast when %s API and fallback fails',
    async (mode) => {
      if (mode === 'denied')
        Object.defineProperty(navigator, 'clipboard', {
          configurable: true,
          value: { writeText: vi.fn().mockRejectedValue(new Error('Denied')) },
        })
      const view = await openTerminal()
      await view.get('.term-host').trigger('mouseup')
      await flushPromises()
      expect(view.find('.toast.copy').exists()).toBe(false)
      expect(view.getComponent(CopyTextDialog).props('text')).toBe(
        terminalMocks.selection,
      )
      expect(terminalMocks.send).not.toHaveBeenCalled()
      view.getComponent(CopyTextDialog).vm.$emit('close')
      await flushPromises()
      expect(document.querySelector('.copy-dialog')).toBeNull()
    },
  )

  it.each(['api', 'fallback'])(
    'shows a copied toast for verified %s success without sending terminal data',
    async (mode) => {
      if (mode === 'api')
        Object.defineProperty(navigator, 'clipboard', {
          configurable: true,
          value: { writeText: vi.fn().mockResolvedValue(undefined) },
        })
      else exec.mockReturnValue(true)
      const view = await openTerminal()
      await view.get('.term-host').trigger('mouseup')
      await flushPromises()
      expect(view.get('.toast.copy').text()).toContain('已复制')
      expect(view.findComponent(CopyTextDialog).exists()).toBe(false)
      expect(terminalMocks.send).not.toHaveBeenCalled()
    },
  )

  it('does nothing for an empty terminal selection', async () => {
    const view = await openTerminal()
    terminalMocks.selection = ''
    await view.get('.term-host').trigger('mouseup')
    await flushPromises()
    expect(exec).not.toHaveBeenCalled()
    expect(view.find('.toast.copy').exists()).toBe(false)
    expect(view.findComponent(CopyTextDialog).exists()).toBe(false)
  })
})

describe('copyText', () => {
  it('does not insert old text or invoke fallback after a late rejection loses permission', async () => {
    let reject!: (error: Error) => void
    let allowed = true
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: {
        writeText: () =>
          new Promise<void>((_yes, no) => {
            reject = no
          }),
      },
    })
    const append = vi.spyOn(document.body, 'appendChild')
    const copying = copyText('private old output', () => allowed)
    allowed = false
    reject(new Error('Denied'))
    expect(await copying).toBe(false)
    expect(append).not.toHaveBeenCalled()
    expect(exec).not.toHaveBeenCalled()
  })
  it('does not start a copy when permission is already gone', async () => {
    expect(await copyText('private old output', () => false)).toBe(false)
    expect(exec).not.toHaveBeenCalled()
  })
  it.each(['api', 'fallback', 'manual'])(
    'does not reject the %s result when both document restore APIs throw',
    async (mode) => {
      const p = document.createElement('p')
      p.textContent = 'selected output'
      document.body.append(p)
      const selection = window.getSelection()!
      selection.setBaseAndExtent(p.firstChild!, 10, p.firstChild!, 1)
      let resolve!: () => void
      let reject!: (error: Error) => void
      Object.defineProperty(navigator, 'clipboard', {
        configurable: true,
        value: {
          writeText: () =>
            new Promise<void>((yes, no) => {
              resolve = yes
              reject = no
            }),
        },
      })
      exec.mockReturnValue(mode === 'fallback')
      const pending = copyText('snapshot')
      const fail = () => {
        throw new DOMException('Stale', 'IndexSizeError')
      }
      vi.spyOn(selection, 'setBaseAndExtent').mockImplementation(fail)
      vi.spyOn(selection, 'addRange').mockImplementation(fail)
      if (mode === 'api') resolve()
      else reject(new Error('Denied'))
      await expect(pending).resolves.toBe(mode !== 'manual')
    },
  )

  it.each(['api', 'fallback', 'manual'])(
    'keeps %s outcome when connected selection text shrinks during await',
    async (mode) => {
      const paragraph = document.createElement('p')
      paragraph.textContent = 'original long selection'
      document.body.append(paragraph)
      const node = paragraph.firstChild!
      window.getSelection()!.setBaseAndExtent(node, 20, node, 2)
      let resolve!: () => void
      let reject!: (error: Error) => void
      const writeText = vi.fn(
        () =>
          new Promise<void>((yes, no) => {
            resolve = yes
            reject = no
          }),
      )
      Object.defineProperty(navigator, 'clipboard', {
        configurable: true,
        value: { writeText },
      })
      exec.mockReturnValue(mode === 'fallback')
      const pending = copyText('copied snapshot')
      node.textContent = 'x'
      expect(node.isConnected).toBe(true)
      if (mode === 'api') resolve()
      else reject(new Error('Denied'))
      await expect(pending).resolves.toBe(mode !== 'manual')
      expect(document.querySelector('textarea')).toBeNull()
    },
  )

  it.each(['api', 'fallback', 'manual'])(
    'keeps %s outcome when the focused input changes type during await',
    async (mode) => {
      const input = document.createElement('input')
      input.value = 'draft'
      document.body.append(input)
      input.focus()
      input.setSelectionRange(1, 4)
      let resolve!: () => void
      let reject!: (error: Error) => void
      Object.defineProperty(navigator, 'clipboard', {
        configurable: true,
        value: {
          writeText: () =>
            new Promise<void>((yes, no) => {
              resolve = yes
              reject = no
            }),
        },
      })
      exec.mockReturnValue(mode === 'fallback')
      const pending = copyText('copied snapshot')
      input.type = 'number'
      if (mode === 'api') resolve()
      else reject(new Error('Denied'))
      await expect(pending).resolves.toBe(mode !== 'manual')
      expect(document.activeElement).toBe(input)
      expect(document.querySelector('textarea')).toBeNull()
    },
  )

  it('isolates all restore operations when browser selection methods throw', () => {
    const input = document.createElement('input')
    input.value = 'draft'
    const p = document.createElement('p')
    p.textContent = 'original text'
    document.body.append(input, p)
    input.focus()
    const selection = window.getSelection()!
    selection.setBaseAndExtent(p.firstChild!, 10, p.firstChild!, 1)
    const restore = preserveFocusAndSelection()
    const fail = () => {
      throw new DOMException('Stale', 'IndexSizeError')
    }
    vi.spyOn(input, 'focus').mockImplementation(fail)
    vi.spyOn(input, 'setSelectionRange').mockImplementation(fail)
    const base = vi
      .spyOn(selection, 'setBaseAndExtent')
      .mockImplementation(fail)
    const add = vi.spyOn(selection, 'addRange').mockImplementation(fail)
    expect(restore).not.toThrow()
    expect(base).toHaveBeenCalled()
    expect(add).toHaveBeenCalled()
    vi.spyOn(selection, 'removeAllRanges').mockImplementation(fail)
    expect(restore).not.toThrow()
  })

  it('reports success only after Clipboard API resolves and skips fallback', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    })
    expect(await copyText('line 1\nline 2')).toBe(true)
    expect(writeText).toHaveBeenCalledWith('line 1\nline 2')
    expect(exec).not.toHaveBeenCalled()
    expect(document.querySelector('textarea')).toBeNull()
  })

  it.each(['missing', 'rejected', 'throws'])(
    'uses verified synchronous fallback when API is %s',
    async (mode) => {
      if (mode !== 'missing') {
        const writeText =
          mode === 'throws'
            ? vi.fn(() => {
                throw new DOMException('Denied', 'NotAllowedError')
              })
            : vi
                .fn()
                .mockRejectedValue(
                  new DOMException('Denied', 'NotAllowedError'),
                )
        Object.defineProperty(navigator, 'clipboard', {
          configurable: true,
          value: { writeText },
        })
      }
      exec.mockImplementation(() => {
        const text = document.activeElement as HTMLTextAreaElement
        expect(text.value).toBe('docker logs app')
        expect(text.selectionStart).toBe(0)
        expect(text.selectionEnd).toBe(text.value.length)
        return true
      })
      expect(await copyText('docker logs app')).toBe(true)
      expect(exec).toHaveBeenCalledWith('copy')
      expect(document.querySelector('textarea')).toBeNull()
    },
  )

  it.each([false, undefined, 1])(
    'does not claim success for execCommand result %s',
    async (result) => {
      exec.mockReturnValue(result)
      expect(await copyText('text')).toBe(false)
      expect(document.querySelector('textarea')).toBeNull()
    },
  )

  it('returns false when execCommand is unavailable', async () => {
    Object.defineProperty(document, 'execCommand', {
      configurable: true,
      value: undefined,
    })
    expect(await copyText('text')).toBe(false)
  })

  it.each(['true', 'false', 'throws'])(
    'restores input focus and backward selection after fallback %s',
    async (mode) => {
      const input = document.createElement('input')
      input.value = 'original draft'
      document.body.append(input)
      input.focus()
      input.setSelectionRange(2, 8, 'backward')
      exec.mockImplementation(() => {
        if (mode === 'throws') throw new Error('Unsupported')
        return mode === 'true'
      })
      expect(await copyText('other text')).toBe(mode === 'true')
      expect(document.activeElement).toBe(input)
      expect([
        input.selectionStart,
        input.selectionEnd,
        input.selectionDirection,
      ]).toEqual([2, 8, 'backward'])
      expect(document.querySelector('textarea')).toBeNull()
    },
  )

  it('restores document text selection and its direction', async () => {
    const paragraph = document.createElement('p')
    paragraph.textContent = 'original selection'
    document.body.append(paragraph)
    const node = paragraph.firstChild!
    const selection = window.getSelection()!
    selection.setBaseAndExtent(node, 12, node, 2)
    expect(await copyText('other text')).toBe(false)
    expect(selection.anchorNode).toBe(node)
    expect(selection.anchorOffset).toBe(12)
    expect(selection.focusOffset).toBe(2)
  })
})
