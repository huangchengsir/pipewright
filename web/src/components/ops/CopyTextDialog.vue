<script setup lang="ts">
import { onMounted, onBeforeUnmount, useTemplateRef } from 'vue'
import { useI18n } from 'vue-i18n'
import { Select, X } from '@vicons/tabler'
import { preserveFocusAndSelection } from '../../utils/clipboard'

defineProps<{ text: string }>()
const emit = defineEmits<{ close: [] }>()
const { t } = useI18n()
const textEl = useTemplateRef<HTMLTextAreaElement>('textEl')
let restore: (() => void) | undefined

function selectText(): void {
  textEl.value?.focus({ preventScroll: true })
  textEl.value?.select()
}

function onKeydown(event: KeyboardEvent): void {
  if (event.key === 'Escape') {
    event.preventDefault()
    event.stopPropagation()
    emit('close')
  } else if (event.key === 'Tab') {
    const dialog = event.currentTarget as HTMLElement
    const controls = Array.from(dialog.querySelectorAll<HTMLElement>('button, textarea'))
    const first = controls[0]
    const last = controls[controls.length - 1]
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault()
      last?.focus()
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault()
      first?.focus()
    }
  }
}

onMounted(() => {
  restore = preserveFocusAndSelection()
  selectText()
})
onBeforeUnmount(() => restore?.())
</script>

<template>
  <Teleport to="body">
    <div class="copy-overlay" @click.self="emit('close')">
      <section class="copy-dialog" role="dialog" aria-modal="true" :aria-label="t('opsContainer.manualCopy.title')" @keydown="onKeydown">
        <header class="copy-head">
          <h2 class="copy-title">{{ t('opsContainer.manualCopy.title') }}</h2>
          <button class="copy-close" type="button" :aria-label="t('opsContainer.close')" :title="t('opsContainer.close')" @click="emit('close')"><X aria-hidden="true" /></button>
        </header>
        <p class="copy-notice">{{ t('opsContainer.manualCopy.unavailable') }}</p>
        <textarea ref="textEl" class="copy-text" :value="text" readonly spellcheck="false" :aria-label="t('opsContainer.manualCopy.textAria')" />
        <footer class="copy-foot">
          <button class="copy-select" type="button" @click="selectText"><Select aria-hidden="true" />{{ t('opsContainer.manualCopy.selectAll') }}</button>
        </footer>
      </section>
    </div>
  </Teleport>
</template>

<style scoped>
.copy-overlay { position: fixed; inset: 0; z-index: 10001; display: grid; place-items: center; padding: 16px; background: rgb(0 0 0 / 55%); }
.copy-dialog { box-sizing: border-box; width: min(640px, 100%); max-height: calc(100dvh - 32px); overflow: auto; padding: 16px; border: 1px solid var(--color-border-strong); border-radius: 8px; background: var(--color-card); color: var(--color-text); box-shadow: var(--shadow-modal); }
.copy-head { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
.copy-title { margin: 0; font-size: 16px; overflow-wrap: anywhere; }
.copy-close { flex: 0 0 44px; width: 44px; height: 44px; display: grid; place-items: center; border: 1px solid var(--color-border); border-radius: var(--rounded-sm); color: var(--color-dim); background: transparent; cursor: pointer; }
.copy-close svg, .copy-select svg { width: 20px; height: 20px; flex-shrink: 0; }
.copy-notice { margin: 12px 0; font-size: 14px; line-height: 1.5; color: var(--color-dim); overflow-wrap: anywhere; }
.copy-text { box-sizing: border-box; display: block; width: 100%; height: min(280px, 40dvh); min-height: 80px; resize: none; padding: 12px; border: 1px solid var(--color-border-strong); border-radius: var(--rounded-sm); background: var(--color-inset); color: var(--color-text); font: 16px/1.5 var(--font-mono); overflow-wrap: anywhere; }
.copy-foot { display: flex; justify-content: flex-end; margin-top: 12px; }
.copy-select { display: inline-flex; align-items: center; justify-content: center; gap: 8px; min-height: 44px; padding: 8px 12px; border: 1px solid var(--color-border-strong); border-radius: var(--rounded-sm); background: var(--color-card-2); color: var(--color-text); font: inherit; cursor: pointer; }
.copy-close:focus-visible, .copy-select:focus-visible, .copy-text:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 2px; }
</style>
