<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, shallowRef, useId, useTemplateRef, watch } from 'vue'
import AppButton from '../ui/AppButton.vue'

const props = defineProps<{
  open: boolean
  title: string
  value: string
  trigger: HTMLElement | null
  placeholder?: string
  closeLabel: string
  cancelLabel: string
  applyLabel: string
  conflictLabel: string
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'apply', value: string): void
}>()

const draft = shallowRef('')
const initialValue = shallowRef('')
const hasConflict = computed(() => props.value !== initialValue.value)
const dialog = useTemplateRef<HTMLDialogElement>('dialog')
const editor = useTemplateRef<HTMLTextAreaElement>('editor')
const titleId = useId()
const conflictId = useId()

let previousBodyOverflow: string | null = null
let backdropPointerId: number | null = null
let composing = false

function closeDialog(): void {
  backdropPointerId = null
  composing = false
  if (dialog.value?.open) dialog.value.close()
  if (previousBodyOverflow === null) return
  document.body.style.overflow = previousBodyOverflow
  previousBodyOverflow = null
  if (props.trigger?.isConnected) props.trigger.focus()
}

watch(
  () => props.open,
  async (open) => {
    if (!open) {
      closeDialog()
      return
    }

    initialValue.value = props.value
    draft.value = initialValue.value
    await nextTick()
    if (!props.open || !dialog.value || dialog.value.open) return

    dialog.value.showModal()
    previousBodyOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    editor.value?.focus()
  },
  { immediate: true },
)

onBeforeUnmount(closeDialog)

function requestClose(): void {
  emit('close')
}

function apply(): void {
  if (props.open && !hasConflict.value) emit('apply', draft.value)
}

function onCancel(): void {
  if (!composing) requestClose()
}

function isBackdrop(event: PointerEvent): boolean {
  if (!dialog.value || event.target !== dialog.value) return false
  const { left, right, top, bottom } = dialog.value.getBoundingClientRect()
  return event.clientX < left || event.clientX >= right
    || event.clientY < top || event.clientY >= bottom
}

function onPointerDown(event: PointerEvent): void {
  if (event.isPrimary) {
    backdropPointerId = event.button === 0 && isBackdrop(event) ? event.pointerId : null
  }
}

function onPointerUp(event: PointerEvent): void {
  if (event.pointerId !== backdropPointerId) return
  backdropPointerId = null
  if (composing || draft.value !== initialValue.value) return
  if (event.button === 0 && isBackdrop(event)) requestClose()
}

function onKeydown(event: KeyboardEvent): void {
  if (composing || event.isComposing || event.keyCode === 229) return

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
    <dialog
      v-if="open"
      ref="dialog"
      class="command-editor-dialog"
      :aria-labelledby="titleId"
      @cancel.prevent="onCancel"
      @keydown.stop="onKeydown"
      @pointerdown="onPointerDown"
      @pointerup="onPointerUp"
      @pointercancel="backdropPointerId = null"
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
          :aria-describedby="hasConflict ? conflictId : undefined"
          autofocus
          @compositionstart="composing = true"
          @compositionend="composing = false"
          autocomplete="off"
          autocapitalize="off"
          spellcheck="false"
        />
      </div>

      <p v-if="hasConflict" :id="conflictId" class="command-editor-dialog__conflict" role="alert">
        {{ conflictLabel }}
      </p>

      <footer class="command-editor-dialog__footer">
        <AppButton @click="requestClose">{{ cancelLabel }}</AppButton>
        <AppButton variant="primary" :disabled="hasConflict" @click="apply">{{
          applyLabel
        }}</AppButton>
      </footer>
    </dialog>
  </Teleport>
</template>

<style scoped>
.command-editor-dialog::backdrop {
  background: oklch(0% 0 0 / 0.48);
  backdrop-filter: blur(3px);
}

.command-editor-dialog {
  position: fixed;
  inset: 0;
  box-sizing: border-box;
  flex-direction: column;
  width: 80vw;
  height: 80dvh;
  max-width: none;
  max-height: none;
  min-width: 0;
  min-height: 0;
  margin: auto;
  padding: 0;
  overflow: hidden;
  color: var(--color-text);
  background: var(--color-card);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded-card);
  box-shadow: var(--shadow-modal, 0 24px 64px oklch(0% 0 0 / 0.4));
}

.command-editor-dialog[open] {
  display: flex;
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

.command-editor-dialog__conflict {
  margin: 0 20px 12px;
  padding: 8px 10px;
  background: var(--color-amber-soft);
  border-radius: var(--rounded);
  font-size: 0.875rem;
  line-height: 1.45;
}

.command-editor-dialog__footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  padding: 12px 20px 16px;
  border-top: 1px solid var(--color-border);
}

@media (max-width: 640px) {
  .command-editor-dialog {
    width: calc(100vw - 24px);
    height: calc(100dvh - 24px);
  }

  .command-editor-dialog__header,
  .command-editor-dialog__body {
    padding-right: 14px;
    padding-left: 14px;
  }

  .command-editor-dialog__conflict {
    margin-right: 14px;
    margin-left: 14px;
  }

  .command-editor-dialog__footer {
    padding-right: 14px;
    padding-left: 14px;
  }
}

@media (prefers-reduced-motion: reduce) {
  .command-editor-dialog::backdrop {
    backdrop-filter: none;
  }
}
</style>
