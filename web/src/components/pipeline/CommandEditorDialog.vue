<script setup lang="ts">
import {
  nextTick,
  onBeforeUnmount,
  shallowRef,
  useId,
  useTemplateRef,
  watch,
} from 'vue'
import AppButton from '../ui/AppButton.vue'

const props = defineProps<{
  open: boolean
  title: string
  value: string
  placeholder?: string
  closeLabel: string
  cancelLabel: string
  applyLabel: string
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'apply', value: string): void
}>()

const draft = shallowRef('')
const previousFocus = shallowRef<HTMLElement | null>(null)
const dialog = useTemplateRef<HTMLElement>('dialog')
const editor = useTemplateRef<HTMLTextAreaElement>('editor')
const titleId = useId()

let previousBodyOverflow = ''
let scrollLocked = false

function lockBodyScroll(): void {
  if (scrollLocked) return
  previousBodyOverflow = document.body.style.overflow
  document.body.style.overflow = 'hidden'
  scrollLocked = true
}

function unlockBodyScroll(): void {
  if (!scrollLocked) return
  document.body.style.overflow = previousBodyOverflow
  scrollLocked = false
}

function restoreFocus(): void {
  const target = previousFocus.value
  previousFocus.value = null
  if (target?.isConnected) target.focus()
}

watch(
  () => props.open,
  async (open) => {
    if (!open) {
      unlockBodyScroll()
      await nextTick()
      restoreFocus()
      return
    }

    draft.value = props.value
    previousFocus.value =
      document.activeElement instanceof HTMLElement
        ? document.activeElement
        : null
    lockBodyScroll()
    await nextTick()
    editor.value?.focus()
  },
  { immediate: true },
)

onBeforeUnmount(() => {
  unlockBodyScroll()
  restoreFocus()
})

function requestClose(): void {
  emit('close')
}

function apply(): void {
  emit('apply', draft.value)
}

function onKeydown(event: KeyboardEvent): void {
  if (event.key === 'Escape') {
    event.preventDefault()
    requestClose()
    return
  }

  if (event.key === 'Enter' && (event.ctrlKey || event.metaKey)) {
    event.preventDefault()
    apply()
    return
  }

  if (event.key !== 'Tab' || !dialog.value) return
  const controls = Array.from(
    dialog.value.querySelectorAll<HTMLElement>(
      'button:not(:disabled), textarea:not(:disabled), [tabindex]:not([tabindex="-1"])',
    ),
  )
  if (controls.length === 0) return

  const first = controls[0]
  const last = controls[controls.length - 1]
  if (event.shiftKey && document.activeElement === first) {
    event.preventDefault()
    last.focus()
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault()
    first.focus()
  }
}
</script>

<template>
  <Teleport to="body">
    <div
      v-if="open"
      class="command-editor-backdrop"
      @mousedown.self="requestClose"
    >
      <section
        ref="dialog"
        class="command-editor-dialog"
        role="dialog"
        aria-modal="true"
        :aria-labelledby="titleId"
        @keydown="onKeydown"
      >
        <header class="command-editor-dialog__header">
          <h2 :id="titleId" class="command-editor-dialog__title">
            {{ title }}
          </h2>
          <button
            type="button"
            class="command-editor-dialog__close"
            :aria-label="closeLabel"
            :title="closeLabel"
            @click="requestClose"
          >
            <svg
              width="18"
              height="18"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              stroke-width="2"
              aria-hidden="true"
            >
              <path d="M18 6 6 18M6 6l12 12" />
            </svg>
          </button>
        </header>

        <div class="command-editor-dialog__body">
          <textarea
            ref="editor"
            v-model="draft"
            class="command-editor-dialog__textarea"
            :placeholder="placeholder"
            :aria-label="title"
            autocomplete="off"
            autocapitalize="off"
            spellcheck="false"
          />
        </div>

        <footer class="command-editor-dialog__footer">
          <AppButton @click="requestClose">{{ cancelLabel }}</AppButton>
          <AppButton variant="primary" @click="apply">{{
            applyLabel
          }}</AppButton>
        </footer>
      </section>
    </div>
  </Teleport>
</template>

<style scoped>
.command-editor-backdrop {
  position: fixed;
  inset: 0;
  z-index: 90;
  display: grid;
  place-items: center;
  padding: 24px;
  background: oklch(0% 0 0 / 0.48);
  backdrop-filter: blur(3px);
}

.command-editor-dialog {
  box-sizing: border-box;
  display: flex;
  flex-direction: column;
  width: 80vw;
  height: 80dvh;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
  color: var(--color-text);
  background: var(--color-card);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded-card);
  box-shadow: var(--shadow-modal, 0 24px 64px oklch(0% 0 0 / 0.4));
}

.command-editor-dialog__header {
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 16px 20px;
  border-bottom: 1px solid var(--color-border);
}

.command-editor-dialog__title {
  min-width: 0;
  margin: 0;
  font-size: 1rem;
  font-weight: 700;
  line-height: 1.5;
}

.command-editor-dialog__close {
  display: grid;
  flex: none;
  width: 32px;
  height: 32px;
  margin-left: auto;
  padding: 0;
  place-items: center;
  color: var(--color-faint);
  background: transparent;
  border: 0;
  border-radius: var(--rounded);
  cursor: pointer;
}

.command-editor-dialog__close:hover {
  color: var(--color-text);
  background: var(--color-inset);
}

.command-editor-dialog__close:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}

.command-editor-dialog__body {
  display: flex;
  flex: 1;
  min-height: 0;
  padding: 16px 20px;
}

.command-editor-dialog__textarea {
  box-sizing: border-box;
  flex: 1;
  width: 100%;
  min-width: 0;
  min-height: 0;
  padding: 14px 16px;
  resize: none;
  color: var(--color-text);
  background: var(--color-canvas, var(--color-inset));
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded-md);
  outline: none;
  font-family: var(--font-mono);
  font-size: 0.88rem;
  line-height: 1.65;
  tab-size: 2;
}

.command-editor-dialog__textarea:focus {
  border-color: var(--color-primary);
  box-shadow: 0 0 0 3px var(--color-primary-soft);
}

.command-editor-dialog__textarea::placeholder {
  color: var(--color-muted);
}

.command-editor-dialog__footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  padding: 12px 20px 16px;
  border-top: 1px solid var(--color-border);
}

@media (max-width: 640px) {
  .command-editor-backdrop {
    padding: 12px;
  }

  .command-editor-dialog {
    width: calc(100vw - 24px);
    height: calc(100dvh - 24px);
  }

  .command-editor-dialog__header,
  .command-editor-dialog__body {
    padding-right: 14px;
    padding-left: 14px;
  }

  .command-editor-dialog__footer {
    padding-right: 14px;
    padding-left: 14px;
  }
}

@media (prefers-reduced-motion: reduce) {
  .command-editor-backdrop {
    backdrop-filter: none;
  }
}
</style>
