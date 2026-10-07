<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, useTemplateRef } from 'vue'
import { useI18n } from 'vue-i18n'
import AiOpsPanel from './AiOpsPanel.vue'
withDefaults(defineProps<{ initialServerIds?: string[] }>(), {
  initialServerIds: () => [],
})
const emit = defineEmits<{ close: [] }>()
const { t } = useI18n()
const panel = useTemplateRef('panel'),
  drawer = useTemplateRef<HTMLElement>('drawer')
let previous: HTMLElement | null = null
function keyboard(e: KeyboardEvent): void {
  if (
    e.defaultPrevented ||
    document.querySelector('.copy-dialog, [role="listbox"]')
  )
    return
  if (e.key === 'Escape') {
    e.preventDefault()
    e.stopPropagation()
    void panel.value?.close()
  }
  if (e.key === 'Tab') {
    const list = [
      ...(drawer.value?.querySelectorAll<HTMLElement>(
        'button:not(:disabled),textarea:not(:disabled),input:not(:disabled),summary,[tabindex="0"]',
      ) ?? []),
    ].filter((el) => el.getClientRects().length > 0)
    const first = list[0],
      last = list[list.length - 1]
    if (!first) {
      e.preventDefault()
      drawer.value?.focus()
    } else if (
      e.shiftKey &&
      (document.activeElement === first ||
        document.activeElement === drawer.value)
    ) {
      e.preventDefault()
      last?.focus()
    } else if (
      !e.shiftKey &&
      (document.activeElement === last ||
        document.activeElement === drawer.value)
    ) {
      e.preventDefault()
      first?.focus()
    }
  }
}
onMounted(async () => {
  previous =
    document.activeElement instanceof HTMLElement
      ? document.activeElement
      : null
  await nextTick()
  drawer.value?.focus()
})
onBeforeUnmount(() => {
  if (previous?.isConnected) previous.focus()
})
</script>
<template>
  <Teleport to="body">
    <div class="ops-scrim">
      <aside
        ref="drawer"
        class="ops-drawer"
        role="dialog"
        aria-modal="true"
        :aria-label="t('opsChat.title')"
        tabindex="-1"
        @keydown="keyboard"
      >
        <AiOpsPanel
          ref="panel"
          :initial-server-ids="initialServerIds"
          @collapse="emit('close')"
        />
      </aside>
    </div>
  </Teleport>
</template>
<style scoped>
.ops-scrim {
  position: fixed;
  inset: 0;
  z-index: 500;
  background: oklch(0% 0 0 / 0.42);
  display: flex;
  justify-content: flex-end;
}
.ops-drawer {
  width: min(560px, 100vw);
  height: 100%;
  min-width: 0;
  background: var(--color-card);
  border-left: 1px solid var(--color-border-strong);
  outline: none;
}
</style>
