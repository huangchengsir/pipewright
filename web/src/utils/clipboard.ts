/** Capture before temporarily selecting copy text; restore without scrolling. */
export function preserveFocusAndSelection(): () => void {
  const active =
    document.activeElement instanceof HTMLElement
      ? document.activeElement
      : null
  const input =
    active instanceof HTMLInputElement || active instanceof HTMLTextAreaElement
      ? active
      : null
  const start = input?.selectionStart ?? null
  const end = input?.selectionEnd ?? null
  const direction = input?.selectionDirection ?? undefined
  const selection = window.getSelection()
  const ranges: Range[] = []
  if (selection) {
    for (let i = 0; i < selection.rangeCount; i++)
      ranges.push(selection.getRangeAt(i).cloneRange())
  }
  const anchor = selection?.anchorNode
  const anchorOffset = selection?.anchorOffset ?? 0
  const focus = selection?.focusNode
  const focusOffset = selection?.focusOffset ?? 0

  return () => {
    // The DOM may change while Clipboard API is pending. Recovery is best-effort.
    try {
      if (active?.isConnected) active.focus({ preventScroll: true })
    } catch {
      /* focus must not change the copy result */
    }
    try {
      if (input?.isConnected && start !== null && end !== null)
        input.setSelectionRange(start, end, direction)
    } catch {
      /* an input may now have a non-selectable type */
    }
    try {
      const current = window.getSelection()
      if (!current) return
      current.removeAllRanges()
      if (
        anchor?.isConnected &&
        focus?.isConnected &&
        typeof current.setBaseAndExtent === 'function'
      ) {
        try {
          current.setBaseAndExtent(anchor, anchorOffset, focus, focusOffset)
          return
        } catch {
          /* stale offsets: try the live, cloned ranges instead */
        }
      }
      for (const range of ranges) {
        try {
          if (
            range.startContainer.isConnected &&
            range.endContainer.isConnected
          )
            current.addRange(range)
        } catch {
          /* a stale range must not block manual copy */
        }
      }
    } catch {
      /* selection recovery must never reject a copy operation */
    }
  }
}

/** False means the caller must offer visible, manually selectable text. */
export async function copyText(
  text: string,
  permitted: () => boolean = () => true,
): Promise<boolean> {
  if (!permitted()) return false
  const restore = preserveFocusAndSelection()
  let textarea: HTMLTextAreaElement | undefined
  try {
    try {
      if (typeof navigator.clipboard?.writeText === 'function') {
        await navigator.clipboard.writeText(text)
        return permitted()
      }
    } catch {
      // Permissions and browser policy can fail even in a secure context.
    }
    if (!permitted()) return false
    textarea = document.createElement('textarea')
    textarea.value = text
    textarea.readOnly = true
    textarea.style.cssText =
      'position:fixed;top:0;left:0;width:1px;height:1px;opacity:0;font-size:16px;'
    document.body.appendChild(textarea)
    textarea.focus({ preventScroll: true })
    textarea.select()
    return (
      typeof document.execCommand === 'function' &&
      document.execCommand('copy') === true
    )
  } catch {
    return false
  } finally {
    textarea?.remove()
    if (permitted()) restore()
  }
}
