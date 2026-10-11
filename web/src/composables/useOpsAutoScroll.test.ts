import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h, nextTick, shallowRef } from 'vue'
import { mount, type VueWrapper } from '@vue/test-utils'
import { useOpsAutoScroll } from './useOpsAutoScroll'

let wrapper: VueWrapper | undefined
let resize: () => void
let frames: Map<number, FrameRequestCallback>
let frameId: number
const observe = vi.fn()
const disconnect = vi.fn()

beforeEach(() => {
  frames = new Map()
  frameId = 0
  observe.mockClear()
  disconnect.mockClear()
  vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => {
    frames.set(++frameId, callback)
    return frameId
  })
  vi.stubGlobal('cancelAnimationFrame', (id: number) => frames.delete(id))
  vi.stubGlobal(
    'ResizeObserver',
    class {
      observe = observe
      disconnect = disconnect
      constructor(callback: () => void) {
        resize = callback
      }
    },
  )
})
afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  vi.unstubAllGlobals()
})

function flushFrame(): void {
  const pending = [...frames.values()]
  frames.clear()
  pending.forEach((callback) => callback(0))
}

async function harness(initialActive = true) {
  const active = shallowRef(initialActive)
  const sessionId = shallowRef<string | undefined>('first')
  let api!: ReturnType<typeof useOpsAutoScroll>
  wrapper = mount(
    defineComponent({
      setup() {
        api = useOpsAutoScroll({ active, sessionId: () => sessionId.value })
        return () =>
          h('div', { ref: api.viewport }, [
            h('div', { ref: api.content }, [
              h('div', { 'data-entry-seq': '1' }),
            ]),
          ])
      },
    }),
    { attachTo: document.body },
  )
  const el = wrapper.element as HTMLElement
  const body = el.firstElementChild as HTMLElement
  const row = body.firstElementChild as HTMLElement
  const layout = { height: 1000, client: 200, rowTop: 200 }
  Object.defineProperties(el, {
    scrollHeight: { get: () => layout.height },
    clientHeight: { get: () => layout.client },
  })
  vi.spyOn(el, 'getBoundingClientRect').mockImplementation(
    () => ({ top: 100, bottom: 100 + layout.client }) as DOMRect,
  )
  vi.spyOn(row, 'getBoundingClientRect').mockImplementation(
    () =>
      ({
        top: 100 + layout.rowTop - el.scrollTop,
        bottom: 180 + layout.rowTop - el.scrollTop,
      }) as DOMRect,
  )
  function scroll(top: number): void {
    el.dispatchEvent(new Event('wheel'))
    el.scrollTop = top
    el.dispatchEvent(new Event('scroll'))
  }
  await nextTick()
  await nextTick()
  return { api, active, sessionId, el, body, row, layout, scroll }
}

describe('useOpsAutoScroll', () => {
  it('does not grant scroll intent when a result control is clicked', async () => {
    const { el, body, layout } = await harness()
    const button = document.createElement('button')
    body.appendChild(button)
    button.dispatchEvent(new Event('pointerdown', { bubbles: true }))
    layout.height = 600
    el.scrollTop = 400
    layout.height = 1800
    el.dispatchEvent(new Event('scroll'))
    resize()
    flushFrame()
    expect(el.scrollTop).toBe(1600)
  })

  it('preserves keyboard focus that scrolls to an earlier message control', async () => {
    const { el, body, layout } = await harness()
    const button = document.createElement('button')
    body.appendChild(button)
    vi.spyOn(button, 'matches').mockReturnValue(true)
    button.dispatchEvent(new FocusEvent('focusin', { bubbles: true }))
    el.scrollTop = 100
    el.dispatchEvent(new Event('scroll'))
    layout.height = 1400
    resize()
    flushFrame()
    expect(el.scrollTop).toBe(100)
  })
  it('follows after shrink and regrowth before a queued clamp event arrives', async () => {
    const { el, layout } = await harness()
    layout.height = 600
    el.scrollTop = 400
    layout.height = 1800
    el.dispatchEvent(new Event('scroll'))
    resize()
    flushFrame()
    expect(el.scrollTop).toBe(1600)
  })
  it('keeps following delayed programmatic scroll events after streamed growth', async () => {
    const { el, layout } = await harness()
    for (let i = 0; i < 8; i++) {
      layout.height += 400
      el.dispatchEvent(new Event('scroll'))
      resize()
      flushFrame()
      expect(el.scrollTop).toBe(layout.height - layout.client)
    }
  })

  it('does not mistake a shrinking message or viewport clamp for reading history', async () => {
    const { el, layout, scroll } = await harness()
    layout.height = 600
    scroll(400)
    layout.height = 1800
    el.dispatchEvent(new Event('scroll'))
    resize()
    flushFrame()
    expect(el.scrollTop).toBe(1600)
    scroll(300)
    layout.height = 2000
    el.dispatchEvent(new Event('scroll'))
    resize()
    flushFrame()
    expect(el.scrollTop).toBe(300)
  })

  it('opens at the bottom and observes only the viewport and content', async () => {
    const { el, body } = await harness()
    expect(el.scrollTop).toBe(800)
    expect(observe.mock.calls).toEqual([[el], [body]])
  })

  it('does not move an inactive panel, then opens at the bottom', async () => {
    const { el, active } = await harness(false)
    expect(el.scrollTop).toBe(0)
    resize()
    expect(frames.size).toBe(0)
    active.value = true
    await nextTick()
    await nextTick()
    expect(el.scrollTop).toBe(800)
  })

  it('forces an own-message scroll after nextTick even when reading history', async () => {
    const { api, el, layout, scroll } = await harness()
    scroll(100)
    const done = api.scrollToBottom()
    layout.height = 1200
    expect(el.scrollTop).toBe(100)
    await done
    expect(el.scrollTop).toBe(1000)
  })

  it('coalesces streaming resizes into one frame without scanning rows', async () => {
    const { el, body, layout } = await harness()
    const scan = vi.spyOn(body, 'querySelectorAll')
    layout.height = 1400
    resize()
    resize()
    resize()
    expect(frames.size).toBe(1)
    flushFrame()
    expect(el.scrollTop).toBe(1200)
    expect(scan).not.toHaveBeenCalled()
    expect(frames.size).toBe(0)
  })

  it('follows within 64px, stops outside that threshold and resumes at bottom', async () => {
    const { el, layout, scroll } = await harness()
    scroll(736)
    layout.height = 1100
    resize()
    flushFrame()
    expect(el.scrollTop).toBe(900)
    scroll(835)
    layout.height = 1300
    resize()
    flushFrame()
    expect(el.scrollTop).toBe(835)
    scroll(1100)
    layout.height = 1500
    resize()
    flushFrame()
    expect(el.scrollTop).toBe(1300)
  })

  it('honors scrolling away before a scheduled streaming frame', async () => {
    const { el, layout, scroll } = await harness()
    layout.height = 1200
    resize()
    scroll(100)
    flushFrame()
    expect(el.scrollTop).toBe(100)
  })

  it('follows viewport resizing only while pinned to the bottom', async () => {
    const { el, layout, scroll } = await harness()
    layout.client = 100
    resize()
    flushFrame()
    expect(el.scrollTop).toBe(900)
    scroll(100)
    layout.client = 300
    resize()
    flushFrame()
    expect(el.scrollTop).toBe(100)
  })

  it('preserves a visible row when prepending, excluding simultaneous tail growth', async () => {
    const { api, el, layout, scroll } = await harness()
    scroll(200)
    let complete!: () => void
    const load = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          complete = resolve
        }),
    )
    const done = api.preserveHistory(load)
    await api.preserveHistory(load)
    expect(load).toHaveBeenCalledTimes(1)
    layout.height += 400
    layout.rowTop += 300
    resize()
    flushFrame()
    expect(el.scrollTop).toBe(500)
    complete()
    await done
    expect(el.scrollTop).toBe(500)
    layout.height += 100
    resize()
    flushFrame()
    expect(el.scrollTop).toBe(500)
  })

  it('respects reader movement while older messages are pending', async () => {
    const { api, el, layout, scroll } = await harness()
    scroll(200)
    let complete!: () => void
    const done = api.preserveHistory(
      () =>
        new Promise<void>((resolve) => {
          complete = resolve
        }),
    )
    scroll(220)
    layout.height += 300
    layout.rowTop += 300
    complete()
    await done
    expect(el.scrollTop).toBe(520)
  })

  it('uses the height delta if the old visible row has been removed', async () => {
    const { api, el, row, layout, scroll } = await harness()
    scroll(200)
    await api.preserveHistory(async () => {
      row.remove()
      layout.height += 300
    })
    expect(el.scrollTop).toBe(500)
  })

  it('does not restore old history offsets after switching sessions', async () => {
    const { api, el, layout, sessionId, scroll } = await harness()
    scroll(200)
    let complete!: () => void
    const done = api.preserveHistory(
      () =>
        new Promise<void>((resolve) => {
          complete = resolve
        }),
    )
    layout.height = 1500
    sessionId.value = 'second'
    await nextTick()
    await nextTick()
    expect(el.scrollTop).toBe(1300)
    complete()
    await done
    expect(el.scrollTop).toBe(1300)
  })

  it('restores position and releases history mode even if loading fails', async () => {
    const { api, el, scroll, layout } = await harness()
    scroll(200)
    await expect(
      api.preserveHistory(async () => {
        throw new Error('load failed')
      }),
    ).rejects.toThrow('load failed')
    expect(el.scrollTop).toBe(200)
    scroll(800)
    layout.height = 1100
    resize()
    flushFrame()
    expect(el.scrollTop).toBe(900)
  })

  it('an older load cannot overwrite a newer history anchor after sending', async () => {
    const { api, el, layout, scroll } = await harness()
    scroll(200)
    let firstComplete!: () => void
    let secondComplete!: () => void
    const first = api.preserveHistory(
      () =>
        new Promise<void>((resolve) => {
          firstComplete = resolve
        }),
    )
    await api.scrollToBottom()
    scroll(200)
    const second = api.preserveHistory(
      () =>
        new Promise<void>((resolve) => {
          secondComplete = resolve
        }),
    )
    firstComplete()
    await first
    layout.rowTop += 300
    layout.height += 300
    secondComplete()
    await second
    expect(el.scrollTop).toBe(500)
  })

  it('disconnects and cancels frames and nextTick work on unmount', async () => {
    const { api, el, scroll, layout } = await harness()
    scroll(100)
    resize()
    const done = api.scrollToBottom()
    wrapper!.unmount()
    wrapper = undefined
    layout.height = 1200
    await done
    resize()
    expect(frames.size).toBe(0)
    expect(disconnect).toHaveBeenCalled()
    expect(el.scrollTop).toBe(100)
  })
})
