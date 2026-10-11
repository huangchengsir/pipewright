import { nextTick, onBeforeUnmount, shallowRef, watch, type Ref } from 'vue'

interface ScrollAnchor {
  element: HTMLElement | null
  offset: number
  top: number
  height: number
}

export function useOpsAutoScroll(options: {
  active: Ref<boolean>
  sessionId: () => string | undefined
  threshold?: number
}) {
  const viewport = shallowRef<HTMLElement | null>(null)
  const content = shallowRef<HTMLElement | null>(null)
  const threshold = options.threshold ?? 64
  let following = true
  let lastTop = 0
  let userIntentUntil = 0
  function onScrollIntent(): void {
    userIntentUntil = performance.now() + 300
  }
  function onPointerMove(event: PointerEvent): void {
    if (event.buttons && event.target === viewport.value) onScrollIntent()
  }
  function onPointerDown(event: PointerEvent): void {
    if (event.target === viewport.value) onScrollIntent()
  }
  function onFocusIn(event: FocusEvent): void {
    if (
      event.target instanceof HTMLElement &&
      event.target.matches(':focus-visible')
    )
      onScrollIntent()
  }
  function onKeydown(event: KeyboardEvent): void {
    if (
      [
        'ArrowUp',
        'ArrowDown',
        'PageUp',
        'PageDown',
        'Home',
        'End',
        ' ',
        'Tab',
      ].includes(event.key)
    )
      onScrollIntent()
  }
  let disposed = false
  let generation = 0
  let historyGeneration = 0
  let frame: number | undefined
  let observer: ResizeObserver | undefined
  let anchor: ScrollAnchor | null = null

  function cancelFrame(): void {
    if (frame !== undefined) cancelAnimationFrame(frame)
    frame = undefined
  }

  function nearBottom(): boolean {
    const el = viewport.value
    return !!el && el.scrollHeight - el.clientHeight - el.scrollTop <= threshold
  }

  function captureAnchor(): ScrollAnchor | null {
    const el = viewport.value
    if (!el) return null
    const top = el.getBoundingClientRect().top
    // Only history loading samples rows; streaming never walks the message DOM.
    const rows = content.value?.querySelectorAll<HTMLElement>(
      '[data-entry-seq], [data-run-id], [data-summary-run]',
    )
    const element =
      Array.from(rows ?? []).find(
        (row) => row.getBoundingClientRect().bottom > top,
      ) ?? null
    return {
      element,
      offset: element ? element.getBoundingClientRect().top - top : 0,
      top: el.scrollTop,
      height: el.scrollHeight,
    }
  }

  function restoreAnchor(): void {
    const el = viewport.value
    if (!el || !anchor) return
    if (anchor.element && content.value?.contains(anchor.element)) {
      el.scrollTop +=
        anchor.element.getBoundingClientRect().top -
        el.getBoundingClientRect().top -
        anchor.offset
    } else {
      el.scrollTop = anchor.top + el.scrollHeight - anchor.height
    }
    lastTop = el.scrollTop
  }

  function writeBottom(): void {
    const el = viewport.value
    if (el) {
      el.scrollTop = Math.max(0, el.scrollHeight - el.clientHeight)
      lastTop = el.scrollTop
    }
  }

  function schedule(): void {
    if (disposed || !options.active.value || frame !== undefined) return
    frame = requestAnimationFrame(() => {
      frame = undefined
      if (disposed || !options.active.value) return
      if (anchor) restoreAnchor()
      else if (following) writeBottom()
    })
  }

  function onScroll(): void {
    if (!options.active.value) return
    const el = viewport.value
    if (!el) return
    // A delayed programmatic event can arrive after another streamed chunk grows.
    // Only actual upward movement pauses following; layout clamping is not intent.
    const previous = Math.min(
      lastTop,
      Math.max(0, el.scrollHeight - el.clientHeight),
    )
    if (nearBottom()) following = true
    else if (
      performance.now() <= userIntentUntil &&
      el.scrollTop < previous - 1
    )
      following = false
    lastTop = el.scrollTop
    if (anchor) anchor = captureAnchor()
  }

  async function scrollToBottom(): Promise<void> {
    const current = generation
    historyGeneration++
    following = true
    userIntentUntil = 0
    anchor = null
    await nextTick()
    if (disposed || current !== generation || !options.active.value) return
    cancelFrame()
    writeBottom()
  }

  async function preserveHistory(load: () => Promise<unknown>): Promise<void> {
    if (anchor || disposed || !options.active.value) return
    const current = generation
    const history = ++historyGeneration
    anchor = captureAnchor()
    following = false
    cancelFrame()
    try {
      await load()
    } finally {
      await nextTick()
      if (
        !disposed &&
        current === generation &&
        history === historyGeneration &&
        options.active.value
      ) {
        cancelFrame()
        restoreAnchor()
        anchor = null
        following = nearBottom()
      }
    }
  }

  watch(
    [options.active, options.sessionId],
    ([active]) => {
      generation++
      anchor = null
      cancelFrame()
      if (active) void scrollToBottom()
    },
    { immediate: true, flush: 'sync' },
  )

  watch(
    [viewport, content],
    ([el, body], _previous, onCleanup) => {
      if (!el || !body) return
      el.addEventListener('scroll', onScroll, { passive: true })
      el.addEventListener('wheel', onScrollIntent, { passive: true })
      el.addEventListener('touchmove', onScrollIntent, { passive: true })
      el.addEventListener('pointerdown', onPointerDown, { passive: true })
      el.addEventListener('pointermove', onPointerMove, { passive: true })
      el.addEventListener('keydown', onKeydown)
      el.addEventListener('focusin', onFocusIn)
      if (typeof ResizeObserver !== 'undefined') {
        observer = new ResizeObserver(schedule)
        observer.observe(el)
        observer.observe(body)
      }
      if (options.active.value) void scrollToBottom()
      onCleanup(() => {
        el.removeEventListener('scroll', onScroll)
        el.removeEventListener('wheel', onScrollIntent)
        el.removeEventListener('touchmove', onScrollIntent)
        el.removeEventListener('pointerdown', onPointerDown)
        el.removeEventListener('pointermove', onPointerMove)
        el.removeEventListener('keydown', onKeydown)
        el.removeEventListener('focusin', onFocusIn)
        observer?.disconnect()
        observer = undefined
        cancelFrame()
      })
    },
    { flush: 'post' },
  )

  onBeforeUnmount(() => {
    disposed = true
    generation++
    anchor = null
    cancelFrame()
    observer?.disconnect()
  })

  return { viewport, content, scrollToBottom, preserveHistory }
}
