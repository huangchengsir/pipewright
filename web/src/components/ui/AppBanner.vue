<script setup lang="ts">
/**
 * AppBanner — inline contextual alert / banner.
 * Variants: info | warn | error | ai (success also supported).
 * Does not auto-dismiss. Action slot available.
 */

export type BannerVariant = 'info' | 'warn' | 'error' | 'success' | 'ai'

defineProps<{
  variant?: BannerVariant
  title?: string
}>()

// SVG paths per variant
const iconMap: Record<BannerVariant, string> = {
  info:    'M12 8h.01M11 12h1v4h1M12 3C6.48 3 2 7.48 2 12s4.48 9 10 9 10-4.48 10-9S17.52 3 12 3z',
  warn:    'M12 9v4m0 4h.01M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z',
  error:   'M12 8v5M12 16h.01M12 3C6.48 3 2 7.48 2 12s4.48 9 10 9 10-4.48 10-9S17.52 3 12 3z',
  success: 'm5 13 4 4 10-11',
  ai:      'M3 12h3.5l2.2-6 3.6 12 2.4-7 1.3 1h4.5',
}
</script>

<template>
  <div
    class="app-banner"
    :class="`app-banner--${variant ?? 'info'}`"
    role="note"
  >
    <!-- Icon -->
    <span class="app-banner__icon" aria-hidden="true">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <path :d="iconMap[variant ?? 'info']" />
      </svg>
    </span>

    <!-- Content -->
    <span class="app-banner__body">
      <strong v-if="title" class="app-banner__title">{{ title }}</strong>
      <slot />
    </span>

    <!-- Optional action slot -->
    <span v-if="$slots.action" class="app-banner__action">
      <slot name="action" />
    </span>
  </div>
</template>

<style scoped>
.app-banner {
  display: flex;
  align-items: center;
  gap: 10px;
  border-radius: var(--rounded-lg);
  padding: 11px 14px;
  font-size: var(--text-body);
  border: 1px solid transparent;
  line-height: 1.5;
}

.app-banner__icon {
  flex-shrink: 0;
  display: flex;
  align-items: center;
}
.app-banner__icon svg {
  width: 16px;
  height: 16px;
  display: block;
}

.app-banner__body {
  flex: 1;
  min-width: 0;
}

.app-banner__title {
  font-weight: 600;
  display: block;
}

.app-banner__action {
  flex-shrink: 0;
  font-weight: 600;
  font-size: var(--text-label);
  cursor: pointer;
  white-space: nowrap;
}

/* ——— info ——— */
.app-banner--info {
  background: var(--color-cyan-soft);
  border-color: var(--color-cyan-line);
  color: var(--color-text);
}
.app-banner--info .app-banner__icon { color: var(--color-cyan); }
.app-banner--info .app-banner__action { color: var(--color-cyan); }

/* ——— warn ——— */
.app-banner--warn {
  background: var(--color-amber-soft);
  border-color: var(--color-amber-line);
  color: var(--color-text);
}
.app-banner--warn .app-banner__icon { color: var(--color-amber); }
.app-banner--warn .app-banner__action { color: var(--color-amber); }

/* ——— error ——— */
.app-banner--error {
  background: var(--color-red-soft);
  border-color: var(--color-red-line);
  color: var(--color-text);
}
.app-banner--error .app-banner__icon { color: var(--color-red); }
.app-banner--error .app-banner__action { color: var(--color-red); }

/* ——— success ——— */
.app-banner--success {
  background: var(--color-green-soft);
  border-color: var(--color-green-line);
  color: var(--color-text);
}
.app-banner--success .app-banner__icon { color: var(--color-green); }
.app-banner--success .app-banner__action { color: var(--color-green); }

/* ——— ai ——— */
.app-banner--ai {
  background: var(--color-cyan-soft);
  border-color: var(--color-cyan-line);
  color: var(--color-text);
}
.app-banner--ai .app-banner__icon { color: var(--color-cyan); }
.app-banner--ai .app-banner__action { color: var(--color-cyan); }
</style>
