/**
 * useConfirm — programmatic confirmation dialog.
 *
 * Usage:
 *   const confirm = useConfirm()
 *
 *   // Simple confirm:
 *   const ok = await confirm.open({
 *     title: '回滚到 #126?',
 *     body: '将把 生产-1 的 acme-web 切回上一稳定版本。',
 *     confirmLabel: '确认回滚',
 *   })
 *
 *   // Type-to-confirm:
 *   const ok = await confirm.open({
 *     title: '重置实例',
 *     body: '将销毁所有数据,不可恢复。',
 *     confirmText: 'acme',   // user must type this
 *     confirmLabel: '永久重置',
 *   })
 *
 * The ConfirmDialog component must be mounted once in the app (e.g. App.vue or AppShell).
 */

import { ref } from 'vue'

export interface ConfirmOptions {
  title: string
  body: string
  /** Label for the confirm/destructive button */
  confirmLabel?: string
  /** If provided, user must type this exact string before confirm is enabled */
  confirmText?: string
  /** visual intent — affects confirm button colour */
  variant?: 'danger' | 'primary'
}

interface PendingConfirm {
  options: ConfirmOptions
  resolve: (result: boolean) => void
}

const pending = ref<PendingConfirm | null>(null)

export function useConfirm() {
  function open(options: ConfirmOptions): Promise<boolean> {
    return new Promise<boolean>((resolve) => {
      pending.value = { options, resolve }
    })
  }

  function _resolve(result: boolean): void {
    pending.value?.resolve(result)
    pending.value = null
  }

  return {
    /** Reactive pending request (consumed by ConfirmDialog component) */
    pending,
    open,
    /** Called by ConfirmDialog to settle the promise */
    _resolve,
  }
}
