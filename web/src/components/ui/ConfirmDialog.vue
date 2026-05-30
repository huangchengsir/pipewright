<script setup lang="ts">
/**
 * ConfirmDialog — modal confirmation.
 * Supports:
 *   - Simple confirm (click confirm button)
 *   - Type-to-confirm (pass confirmText; user must type it exactly)
 *
 * Accessibility:
 *   - role=dialog, aria-modal=true
 *   - Focus trap (Tab / Shift+Tab cycles inside)
 *   - Esc closes (cancels)
 *   - Returns focus to trigger on close
 *
 * Mount once in App.vue / AppShell and wire to useConfirm().
 */
import { ref, computed, watch, nextTick } from 'vue'
import { useConfirm } from '../../composables/useConfirm'

const confirm = useConfirm()

const typeInput = ref('')
const dialogRef = ref<HTMLElement | null>(null)
const inputRef  = ref<HTMLInputElement | null>(null)
let focusedBeforeOpen: HTMLElement | null = null

const isOpen = computed(() => confirm.pending.value !== null)
const opts   = computed(() => confirm.pending.value?.options)
const variant = computed(() => opts.value?.variant ?? 'danger')

const typeMatch = computed(() => {
  if (!opts.value?.confirmText) return true
  return typeInput.value === opts.value.confirmText
})

// Trap focus inside dialog
function onKeydown(e: KeyboardEvent) {
  if (!isOpen.value) return

  if (e.key === 'Escape') {
    e.preventDefault()
    cancel()
    return
  }

  if (e.key === 'Tab' && dialogRef.value) {
    const focusable = dialogRef.value.querySelectorAll<HTMLElement>(
      'button:not(:disabled), input:not(:disabled), [tabindex]:not([tabindex="-1"])',
    )
    const first = focusable[0]
    const last  = focusable[focusable.length - 1]

    if (e.shiftKey) {
      if (document.activeElement === first) {
        e.preventDefault()
        last?.focus()
      }
    } else {
      if (document.activeElement === last) {
        e.preventDefault()
        first?.focus()
      }
    }
  }
}

watch(isOpen, async (opened) => {
  if (opened) {
    typeInput.value = ''
    focusedBeforeOpen = document.activeElement as HTMLElement
    await nextTick()
    if (opts.value?.confirmText && inputRef.value) {
      inputRef.value.focus()
    } else {
      // Focus the cancel button by default (safer for destructive ops)
      const cancelBtn = dialogRef.value?.querySelector<HTMLElement>('.cfm-dialog__cancel')
      cancelBtn?.focus()
    }
  } else {
    focusedBeforeOpen?.focus()
  }
})

function cancel() {
  confirm._resolve(false)
}

function doConfirm() {
  if (!typeMatch.value) return
  confirm._resolve(true)
}
</script>

<template>
  <Teleport to="body">
    <Transition name="cfm-overlay">
      <div
        v-if="isOpen"
        class="cfm-overlay"
        aria-hidden="true"
        @click.self="cancel"
      />
    </Transition>

    <Transition name="cfm-dialog">
      <div
        v-if="isOpen && opts"
        ref="dialogRef"
        class="cfm-dialog"
        :class="`cfm-dialog--${variant}`"
        role="dialog"
        aria-modal="true"
        :aria-labelledby="`cfm-title-${opts.title}`"
        @keydown="onKeydown"
      >
        <!-- Header -->
        <div class="cfm-dialog__header">
          <span class="cfm-dialog__icon" aria-hidden="true">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round">
              <path d="M12 9v4m0 4h.01M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z" />
            </svg>
          </span>
          <h2 :id="`cfm-title-${opts.title}`" class="cfm-dialog__title">{{ opts.title }}</h2>
        </div>

        <!-- Body -->
        <div class="cfm-dialog__body">
          <p>{{ opts.body }}</p>

          <!-- Type-to-confirm input -->
          <div v-if="opts.confirmText" class="cfm-dialog__type-field">
            <label :for="`cfm-input`" class="cfm-dialog__type-label">
              输入 <code class="mono">{{ opts.confirmText }}</code> 确认
            </label>
            <input
              id="cfm-input"
              ref="inputRef"
              v-model="typeInput"
              class="cfm-dialog__type-input"
              :class="{ 'cfm-dialog__type-input--match': typeMatch && typeInput.length > 0 }"
              type="text"
              :placeholder="`输入 ${opts.confirmText}…`"
              autocomplete="off"
              autocorrect="off"
              spellcheck="false"
              :aria-label="`输入 ${opts.confirmText} 确认操作`"
            />
          </div>
        </div>

        <!-- Footer -->
        <div class="cfm-dialog__footer">
          <span class="cfm-dialog__spacer" />
          <button
            class="cfm-dialog__cancel"
            type="button"
            @click="cancel"
          >取消</button>
          <button
            class="cfm-dialog__confirm"
            :class="{ 'cfm-dialog__confirm--disabled': !typeMatch }"
            type="button"
            :disabled="!typeMatch"
            @click="doConfirm"
          >
            {{ opts.confirmLabel ?? '确认' }}
          </button>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
/* ——— Overlay ——— */
.cfm-overlay {
  position: fixed;
  inset: 0;
  background: oklch(0% 0 0 / 0.55);
  z-index: 8000;
  backdrop-filter: blur(3px);
}

/* ——— Dialog ——— */
.cfm-dialog {
  position: fixed;
  top: 50%;
  left: 50%;
  translate: -50% -50%;
  z-index: 8001;
  width: min(400px, calc(100vw - 40px));
  background: var(--color-card);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded-xl);
  box-shadow: var(--shadow-modal);
  overflow: hidden;
}

/* ——— Header ——— */
.cfm-dialog__header {
  display: flex;
  align-items: center;
  gap: 11px;
  padding: 16px 18px 12px;
}

.cfm-dialog__icon {
  width: 32px;
  height: 32px;
  border-radius: 9px;
  display: grid;
  place-items: center;
  flex-shrink: 0;
}
.cfm-dialog__icon svg {
  width: 17px;
  height: 17px;
}

.cfm-dialog--danger .cfm-dialog__icon {
  background: var(--color-red-soft);
  color: var(--color-red);
}
.cfm-dialog--primary .cfm-dialog__icon {
  background: var(--color-primary-soft);
  color: var(--color-primary);
}

.cfm-dialog__title {
  font-size: 0.95rem;
  font-weight: 600;
  color: var(--color-text);
  line-height: 1.3;
}

/* ——— Body ——— */
.cfm-dialog__body {
  padding: 0 18px 14px;
  font-size: 0.82rem;
  color: var(--color-dim);
  line-height: 1.6;
}

/* Type-to-confirm */
.cfm-dialog__type-field {
  margin-top: 12px;
}
.cfm-dialog__type-label {
  display: block;
  font-size: 0.74rem;
  color: var(--color-faint);
  margin-bottom: 6px;
}
.cfm-dialog__type-input {
  width: 100%;
  height: 36px;
  background: var(--color-inset);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded);
  padding: 0 12px;
  color: var(--color-text);
  font-family: var(--font-mono);
  font-size: 0.83rem;
  transition:
    border-color var(--duration-fast),
    box-shadow var(--duration-fast);
}
.cfm-dialog__type-input::placeholder {
  color: var(--color-faint);
}
.cfm-dialog__type-input:focus {
  outline: none;
  border-color: var(--color-primary);
  box-shadow: 0 0 0 3px var(--color-primary-soft);
}
.cfm-dialog__type-input--match {
  border-color: var(--color-green);
  box-shadow: 0 0 0 2px var(--color-green-soft);
}

/* ——— Footer ——— */
.cfm-dialog__footer {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 12px 18px;
  border-top: 1px solid var(--color-border);
  background: var(--color-card-2);
}

.cfm-dialog__spacer { flex: 1; }

.cfm-dialog__cancel {
  height: 34px;
  border: 1px solid var(--color-border-strong);
  background: transparent;
  color: var(--color-dim);
  font-family: var(--font-sans);
  font-size: 0.82rem;
  font-weight: 500;
  border-radius: var(--rounded);
  padding: 0 15px;
  cursor: pointer;
  transition:
    color var(--duration-fast),
    border-color var(--duration-fast);
}
.cfm-dialog__cancel:hover { color: var(--color-text); border-color: var(--color-faint); }
.cfm-dialog__cancel:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}

.cfm-dialog__confirm {
  height: 34px;
  border: none;
  font-family: var(--font-sans);
  font-size: 0.82rem;
  font-weight: 600;
  border-radius: var(--rounded);
  padding: 0 15px;
  cursor: pointer;
  transition:
    opacity var(--duration-fast),
    transform var(--duration-fast);
}
.cfm-dialog--danger .cfm-dialog__confirm {
  background: var(--color-red);
  color: #fff;
}
.cfm-dialog--primary .cfm-dialog__confirm {
  background: var(--color-primary);
  color: #fff;
}
.cfm-dialog__confirm:hover:not(:disabled) {
  opacity: 0.88;
}
.cfm-dialog__confirm:focus-visible {
  outline: 2px solid var(--color-red);
  outline-offset: 2px;
}
.cfm-dialog--primary .cfm-dialog__confirm:focus-visible {
  outline-color: var(--color-primary);
}
.cfm-dialog__confirm--disabled,
.cfm-dialog__confirm:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

/* ——— Transitions ——— */
.cfm-overlay-enter-from,
.cfm-overlay-leave-to {
  opacity: 0;
}
.cfm-overlay-enter-active,
.cfm-overlay-leave-active {
  transition: opacity var(--duration-normal);
}

.cfm-dialog-enter-from {
  opacity: 0;
  transform: translateY(-50%) scale(0.95);
  translate: -50% -50%;
}
.cfm-dialog-enter-active {
  transition:
    opacity var(--duration-normal) var(--ease-out-expo),
    transform var(--duration-normal) var(--ease-out-expo);
}
.cfm-dialog-leave-to {
  opacity: 0;
  transform: translateY(-50%) scale(0.97);
  translate: -50% -50%;
}
.cfm-dialog-leave-active {
  transition:
    opacity var(--duration-fast),
    transform var(--duration-fast);
}

@media (prefers-reduced-motion: reduce) {
  .cfm-dialog-enter-from,
  .cfm-dialog-leave-to {
    transform: none;
  }
}
</style>
