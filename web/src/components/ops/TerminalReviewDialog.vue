<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { preserveFocusAndSelection } from '../../utils/clipboard'
withDefaults(
  defineProps<{
    title: string
    notice: string
    source: string
    text: string
    confirmLabel: string
    editable?: boolean
    busy?: boolean
  }>(),
  { editable: false, busy: false },
)
const emit = defineEmits<{
  close: []
  confirm: []
  'update:text': [text: string]
}>()
const { t } = useI18n()
const dialog = ref<HTMLElement | null>(null)
let restore: (() => void) | undefined
onMounted(() => {
  restore = preserveFocusAndSelection()
  dialog.value?.querySelector<HTMLButtonElement>('[data-cancel]')?.focus()
})
onBeforeUnmount(() => restore?.())
function key(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    event.preventDefault()
    event.stopPropagation()
    emit('close')
  }
  if (event.key === 'Tab') {
    const controls = Array.from(
      dialog.value?.querySelectorAll<HTMLElement>(
        'button:not(:disabled),textarea:not(:disabled)',
      ) ?? [],
    )
    const first = controls[0],
      last = controls.at(-1)
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault()
      last?.focus()
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault()
      first?.focus()
    }
  }
}
</script>
<template>
  <Teleport to="body"
    ><div
      class="terminal-review-overlay"
      data-theme="dark"
      @click.self="emit('close')"
    >
      <section
        ref="dialog"
        class="terminal-review"
        role="dialog"
        aria-modal="true"
        :aria-label="title"
        @keydown="key"
      >
        <h2>{{ title }}</h2>
        <p class="source">{{ source }}</p>
        <p>{{ notice }}</p>
        <textarea
          :value="text"
          :readonly="!editable"
          :aria-label="title"
          spellcheck="false"
          @input="
            emit('update:text', ($event.target as HTMLTextAreaElement).value)
          "
        />
        <footer>
          <button data-cancel type="button" @click="emit('close')">
            {{ t('serverTerminal.cancel') }}</button
          ><button
            data-confirm
            type="button"
            :disabled="busy || !text.trim()"
            @click="emit('confirm')"
          >
            {{ busy ? t('serverTerminal.reviewBusy') : confirmLabel }}
          </button>
        </footer>
      </section>
    </div></Teleport
  >
</template>
<style scoped>
.terminal-review-overlay {
  position: fixed;
  inset: 0;
  z-index: 10002;
  display: grid;
  place-items: center;
  padding: 16px;
  background: #000a;
}
.terminal-review {
  box-sizing: border-box;
  width: min(720px, 100%);
  max-height: calc(100dvh - 32px);
  overflow: auto;
  padding: 20px;
  background: var(--color-card, #15151c);
  color: var(--color-text, #e6e6ea);
  border: 1px solid var(--color-border-strong, #45454f);
  border-radius: 8px;
}
h2 {
  font-size: 20px;
  margin: 0 0 12px;
}
p {
  font-size: 14px;
  line-height: 1.6;
  overflow-wrap: anywhere;
}
.source {
  font-family: var(--font-mono);
  color: var(--color-primary, #7fe3f0);
}
textarea {
  box-sizing: border-box;
  width: 100%;
  min-height: 180px;
  height: 30dvh;
  padding: 12px;
  background: var(--color-inset, #08080b);
  color: inherit;
  border: 1px solid var(--color-border-strong, #45454f);
  border-radius: 4px;
  resize: vertical;
  font: 16px/1.5 var(--font-mono);
  tab-size: 4;
}
footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 16px;
  flex-wrap: wrap;
}
button {
  min-height: 44px;
  padding: 8px 16px;
  border: 1px solid var(--color-border-strong, #45454f);
  border-radius: 4px;
  background: transparent;
  color: inherit;
  font: inherit;
  cursor: pointer;
}
button[data-confirm] {
  border-color: var(--color-primary, #7fe3f0);
}
button:disabled {
  opacity: 0.5;
  cursor: default;
}
button:focus-visible,
textarea:focus-visible {
  outline: 2px solid var(--color-primary, #7fe3f0);
  outline-offset: 2px;
}
</style>
