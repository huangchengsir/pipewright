<script setup lang="ts">
/**
 * ToastHost — renders the stacked toast queue in the bottom-right corner.
 * Mount once in App.vue or AppShell.
 * Uses useToast() composable for state.
 * Accessibility: region role=status + aria-live=polite for the container.
 * Enter/leave uses transform + opacity only.
 */
import { useI18n } from 'vue-i18n'
import { useToast } from '../../composables/useToast'

const { t } = useI18n()

const toast = useToast()

// Icon paths per type
const icons: Record<string, string> = {
  success: 'm5 13 4 4 10-11',
  error:   'M18 6 6 18M6 6l12 12',
  warn:    'M12 9v4m0 4h.01M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z',
  info:    'M3 12h3.5l2.2-6 3.6 12 2.4-7 1.3 1h4.5',
}
</script>

<template>
  <div
    class="toast-host"
    role="status"
    aria-live="polite"
    :aria-label="t('misc.toast.hostAria')"
    aria-atomic="false"
    aria-relevant="additions"
  >
    <TransitionGroup name="toast" tag="div" class="toast-stack">
      <div
        v-for="item in toast.toasts.value"
        :key="item.id"
        class="toast-item"
        :class="`toast-item--${item.type}`"
        :aria-label="t('misc.toast.itemAria', { type: item.type, title: item.title })"
      >
        <!-- Accent edge -->
        <span class="toast-item__edge" aria-hidden="true" />

        <!-- Icon -->
        <span class="toast-item__icon" aria-hidden="true">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round">
            <path :d="icons[item.type]" />
          </svg>
        </span>

        <!-- Body -->
        <div class="toast-item__body">
          <strong class="toast-item__title">{{ item.title }}</strong>
          <span v-if="item.detail" class="toast-item__detail">{{ item.detail }}</span>
          <button
            v-if="item.action"
            class="toast-item__action"
            type="button"
            @click="() => { item.action!.onClick(); toast.dismiss(item.id) }"
          >{{ item.action.label }} →</button>
        </div>

        <!-- Progress bar for auto-dismiss toasts -->
        <span
          v-if="item.duration"
          class="toast-item__progress"
          :style="`animation-duration: ${item.duration}ms`"
          aria-hidden="true"
        />

        <!-- Close button -->
        <button
          class="toast-item__close"
          type="button"
          :aria-label="t('misc.toast.closeAria', { title: item.title })"
          @click="toast.dismiss(item.id)"
        >
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round">
            <path d="M18 6 6 18M6 6l12 12" />
          </svg>
        </button>
      </div>
    </TransitionGroup>
  </div>
</template>

<style scoped>
.toast-host {
  position: fixed;
  bottom: 24px;
  right: 20px;
  z-index: 9000;
  pointer-events: none;
}

.toast-stack {
  display: flex;
  flex-direction: column;
  gap: 10px;
  align-items: flex-end;
}

/* ——— Individual toast ——— */
.toast-item {
  position: relative;
  display: flex;
  align-items: flex-start;
  gap: 11px;
  background: var(--color-card-2);
  border: 1px solid var(--color-border-strong);
  border-radius: 12px;
  box-shadow: var(--shadow-modal);
  padding: 12px 14px 14px 17px;
  width: 340px;
  overflow: hidden;
  pointer-events: all;
}

/* Left accent bar */
.toast-item__edge {
  position: absolute;
  left: 0;
  top: 0;
  bottom: 0;
  width: 3px;
  display: block;
}

.toast-item--success .toast-item__edge { background: var(--color-green); }
.toast-item--error   .toast-item__edge { background: var(--color-red); }
.toast-item--warn    .toast-item__edge { background: var(--color-amber); }
.toast-item--info    .toast-item__edge { background: var(--color-cyan); }

/* ——— Icon ——— */
.toast-item__icon {
  width: 22px;
  height: 22px;
  border-radius: 7px;
  display: grid;
  place-items: center;
  flex-shrink: 0;
  margin-top: 1px;
}
.toast-item__icon svg {
  width: 13px;
  height: 13px;
}

.toast-item--success .toast-item__icon { background: var(--color-green-soft); color: var(--color-green); }
.toast-item--error   .toast-item__icon { background: var(--color-red-soft);   color: var(--color-red); }
.toast-item--warn    .toast-item__icon { background: var(--color-amber-soft); color: var(--color-amber); }
.toast-item--info    .toast-item__icon { background: var(--color-cyan-soft);  color: var(--color-cyan); }

/* ——— Body ——— */
.toast-item__body {
  flex: 1;
  min-width: 0;
}
.toast-item__title {
  font-size: 0.83rem;
  font-weight: 600;
  color: var(--color-text);
  display: block;
}
.toast-item__detail {
  display: block;
  font-size: 0.78rem;
  color: var(--color-dim);
  margin-top: 3px;
  line-height: 1.45;
}
.toast-item__action {
  display: inline-block;
  font-family: var(--font-sans);
  font-size: 0.78rem;
  font-weight: 600;
  color: var(--color-primary);
  background: none;
  border: none;
  padding: 0;
  margin-top: 7px;
  cursor: pointer;
}
.toast-item__action:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}

/* ——— Progress bar (shrinks to 0) ——— */
.toast-item__progress {
  position: absolute;
  left: 0;
  bottom: 0;
  height: 2px;
  background: currentColor;
  animation: toast-progress linear forwards;
  transform-origin: left;
}
.toast-item--success .toast-item__progress { background: var(--color-green); }
.toast-item--info    .toast-item__progress { background: var(--color-cyan); }
.toast-item--warn    .toast-item__progress { background: var(--color-amber); }

@keyframes toast-progress {
  from { transform: scaleX(1); }
  to   { transform: scaleX(0); }
}

/* ——— Close button ——— */
.toast-item__close {
  background: none;
  border: none;
  color: var(--color-faint);
  cursor: pointer;
  padding: 2px;
  flex-shrink: 0;
  border-radius: var(--rounded-sm);
  display: flex;
  align-items: center;
}
.toast-item__close svg {
  width: 14px;
  height: 14px;
}
.toast-item__close:hover { color: var(--color-dim); }
.toast-item__close:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}

/* ——— Enter / Leave transitions (transform + opacity only) ——— */
.toast-enter-from {
  opacity: 0;
  transform: translateY(16px) scale(0.96);
}
.toast-enter-active {
  transition:
    opacity var(--duration-normal) var(--ease-out-expo),
    transform var(--duration-normal) var(--ease-out-expo);
}
.toast-leave-to {
  opacity: 0;
  transform: translateX(24px) scale(0.95);
}
.toast-leave-active {
  transition:
    opacity var(--duration-fast),
    transform var(--duration-fast);
}

@media (prefers-reduced-motion: reduce) {
  .toast-enter-from,
  .toast-leave-to {
    transform: none;
  }
  .toast-item__progress {
    animation: none;
  }
}
</style>
