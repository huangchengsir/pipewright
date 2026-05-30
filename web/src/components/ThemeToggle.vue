<script setup lang="ts">
import { computed } from 'vue'
import { useThemeStore } from '../stores/theme'

const themeStore = useThemeStore()

const label = computed(() =>
  themeStore.current === 'dark' ? '切换到浅色' : '切换到深色',
)

const displayText = computed(() =>
  themeStore.current === 'dark' ? '◐ 浅色' : '◑ 深色',
)
</script>

<template>
  <button
    class="theme-toggle"
    type="button"
    :aria-label="label"
    :title="label"
    @click="themeStore.toggle()"
  >
    {{ displayText }}
  </button>
</template>

<style scoped>
.theme-toggle {
  position: fixed;
  right: 20px;
  bottom: 18px;
  font-family: var(--font-sans);
  font-size: 0.74rem;
  color: var(--color-dim);
  border: 1px solid var(--color-border);
  background: var(--color-card);
  border-radius: var(--rounded);
  padding: 7px 13px;
  cursor: pointer;
  z-index: 200;
  /* No layout-affecting properties in transitions */
  transition:
    color var(--duration-fast),
    border-color var(--duration-fast),
    background-color var(--duration-fast);
  box-shadow: var(--shadow);
}

.theme-toggle:hover {
  color: var(--color-text);
  border-color: var(--color-border-strong);
}

.theme-toggle:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}
</style>
