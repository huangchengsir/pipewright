/**
 * useToast — programmatic toast notifications.
 *
 * Toasts stack in the bottom-right corner.
 * - success / info: auto-dismiss after 4s
 * - error: manual close only
 * - warn: auto-dismiss after 6s
 *
 * Usage:
 *   const toast = useToast()
 *   toast.success('部署成功', { detail: 'acme-web #127' })
 *   toast.error('部署失败', { detail: '健康检查超时' })
 */

import { ref } from 'vue'

export type ToastType = 'success' | 'info' | 'error' | 'warn'

export interface ToastItem {
  id: number
  type: ToastType
  title: string
  detail?: string
  action?: { label: string; onClick: () => void }
  /** ms remaining until auto-dismiss (undefined = manual only) */
  duration?: number
}

export interface ShowToastOptions {
  detail?: string
  action?: { label: string; onClick: () => void }
  /** Override auto-dismiss duration in ms. Pass 0 for manual-only. */
  duration?: number
}

let _idCounter = 0
const toasts = ref<ToastItem[]>([])
// 跟踪每个自动消失 toast 的定时器句柄,以便手动关闭/清空时取消,避免野定时器残留。
const timers = new Map<number, ReturnType<typeof setTimeout>>()

const AUTO_DISMISS_MS: Record<ToastType, number | undefined> = {
  success: 4000,
  info:    4000,
  warn:    6000,
  error:   undefined, // manual close
}

function show(type: ToastType, title: string, options: ShowToastOptions = {}): number {
  const id = ++_idCounter
  const duration = options.duration !== undefined
    ? (options.duration === 0 ? undefined : options.duration)
    : AUTO_DISMISS_MS[type]

  const item: ToastItem = {
    id,
    type,
    title,
    detail: options.detail,
    action: options.action,
    duration,
  }

  toasts.value.push(item)

  if (duration !== undefined) {
    timers.set(id, setTimeout(() => dismiss(id), duration))
  }

  return id
}

function dismiss(id: number): void {
  const t = timers.get(id)
  if (t !== undefined) {
    clearTimeout(t)
    timers.delete(id)
  }
  const idx = toasts.value.findIndex(t => t.id === id)
  if (idx !== -1) toasts.value.splice(idx, 1)
}

function clear(): void {
  for (const t of timers.values()) clearTimeout(t)
  timers.clear()
  toasts.value = []
}

export function useToast() {
  return {
    /** All active toasts (reactive) */
    toasts,
    /** Manual dismiss by id */
    dismiss,
    /** Clear all */
    clear,

    success: (title: string, options?: ShowToastOptions) => show('success', title, options),
    info:    (title: string, options?: ShowToastOptions) => show('info',    title, options),
    warn:    (title: string, options?: ShowToastOptions) => show('warn',    title, options),
    error:   (title: string, options?: ShowToastOptions) => show('error',   title, options),
  }
}
