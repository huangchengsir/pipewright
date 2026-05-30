<script setup lang="ts">
/**
 * AppButton — unified button component.
 * Variants: primary | default | ghost | danger | ai
 * States: hover / focus-visible / active / disabled / loading
 * Animation: transform only (translateY -1px on hover), spinner on loading.
 */

export type ButtonVariant = 'primary' | 'default' | 'ghost' | 'danger' | 'ai'

const props = withDefaults(defineProps<{
  variant?: ButtonVariant
  disabled?: boolean
  loading?: boolean
  type?: 'button' | 'submit' | 'reset'
}>(), {
  variant: 'default',
  disabled: false,
  loading: false,
  type: 'button',
})

const emit = defineEmits<{
  click: [event: MouseEvent]
}>()

function handleClick(event: MouseEvent) {
  if (!props.disabled && !props.loading) {
    emit('click', event)
  }
}
</script>

<template>
  <button
    class="app-btn"
    :class="[`app-btn--${variant}`, { 'app-btn--loading': loading }]"
    :type="type"
    :disabled="disabled || loading"
    :aria-busy="loading || undefined"
    :aria-disabled="disabled || loading || undefined"
    @click="handleClick"
  >
    <span v-if="loading" class="app-btn__spinner" aria-hidden="true" />
    <slot />
  </button>
</template>

<style scoped>
.app-btn {
  height: 36px;
  font-family: var(--font-sans);
  font-size: var(--text-label);
  font-weight: 600;
  border-radius: var(--rounded);
  padding: 0 var(--space-4);
  cursor: pointer;
  border: 1px solid transparent;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 7px;
  white-space: nowrap;
  transition:
    background-color var(--duration-fast),
    border-color var(--duration-fast),
    box-shadow var(--duration-fast),
    transform var(--duration-fast),
    opacity var(--duration-fast);
}

.app-btn:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}

.app-btn:disabled,
.app-btn--loading {
  opacity: 0.45;
  cursor: not-allowed;
  transform: none !important;
  box-shadow: none !important;
}

/* ——— primary ——— */
.app-btn--primary {
  background: var(--color-primary);
  color: #fff;
  box-shadow: 0 5px 16px var(--color-primary-soft);
}
.app-btn--primary:hover:not(:disabled):not(.app-btn--loading) {
  background: var(--color-primary-press);
  transform: translateY(-1px);
  box-shadow: 0 7px 20px var(--color-primary-soft);
}
.app-btn--primary:active:not(:disabled):not(.app-btn--loading) {
  background: var(--color-primary-press);
  transform: translateY(0);
}

/* ——— default (secondary) ——— */
.app-btn--default {
  background: var(--color-card-2);
  color: var(--color-text);
  border-color: var(--color-border-strong);
}
.app-btn--default:hover:not(:disabled):not(.app-btn--loading) {
  border-color: var(--color-faint);
  transform: translateY(-1px);
}
.app-btn--default:active:not(:disabled):not(.app-btn--loading) {
  transform: translateY(0);
}

/* ——— ghost ——— */
.app-btn--ghost {
  background: transparent;
  color: var(--color-dim);
  border-color: var(--color-border-strong);
}
.app-btn--ghost:hover:not(:disabled):not(.app-btn--loading) {
  color: var(--color-text);
}

/* ——— danger ——— */
.app-btn--danger {
  background: var(--color-red-soft);
  color: var(--color-red);
  border-color: var(--color-red-line);
}
.app-btn--danger:hover:not(:disabled):not(.app-btn--loading) {
  box-shadow: 0 0 0 3px var(--color-red-soft);
}
.app-btn--danger:focus-visible {
  outline-color: var(--color-red);
}

/* ——— ai ——— */
.app-btn--ai {
  background: var(--color-cyan-soft);
  color: var(--color-cyan);
  border-color: var(--color-cyan-line);
}
.app-btn--ai:hover:not(:disabled):not(.app-btn--loading) {
  box-shadow: 0 0 0 3px var(--color-cyan-soft);
}
.app-btn--ai:focus-visible {
  outline-color: var(--color-cyan);
}

/* ——— Loading spinner ——— */
.app-btn__spinner {
  display: inline-block;
  width: 13px;
  height: 13px;
  border-radius: var(--rounded-full);
  border: 2px solid oklch(100% 0 0 / 0.35);
  border-top-color: #fff;
  animation: btn-spin 0.7s linear infinite;
  flex-shrink: 0;
}

.app-btn--default .app-btn__spinner,
.app-btn--ghost .app-btn__spinner {
  border-color: var(--color-border-strong);
  border-top-color: var(--color-dim);
}

.app-btn--danger .app-btn__spinner {
  border-color: var(--color-red-soft);
  border-top-color: var(--color-red);
}

.app-btn--ai .app-btn__spinner {
  border-color: var(--color-cyan-soft);
  border-top-color: var(--color-cyan);
}

@keyframes btn-spin {
  to { transform: rotate(360deg); }
}

@media (prefers-reduced-motion: reduce) {
  .app-btn__spinner {
    animation: none;
  }
}
</style>
