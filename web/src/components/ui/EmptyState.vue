<script setup lang="ts">
/**
 * EmptyState — zero-content placeholder.
 * icon slot: SVG path string passed via prop, or override entire icon via #icon slot.
 * title + description props; CTA via #cta slot.
 */
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

const props = withDefaults(defineProps<{
  title?: string
  description?: string
  iconPath?: string
}>(), {
  iconPath: 'M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z',
})

const { t } = useI18n()

// Fall back to the localized default title when none is passed.
const titleText = computed(() => props.title ?? t('misc.empty.defaultTitle'))
</script>

<template>
  <div class="empty-state">
    <!-- Icon -->
    <div class="empty-state__icon" aria-hidden="true">
      <slot name="icon">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">
          <path :d="props.iconPath" />
        </svg>
      </slot>
    </div>

    <!-- Text -->
    <strong class="empty-state__title">{{ titleText }}</strong>
    <p v-if="description" class="empty-state__desc">{{ description }}</p>
    <slot name="description" />

    <!-- CTA -->
    <div v-if="$slots.cta" class="empty-state__cta">
      <slot name="cta" />
    </div>
  </div>
</template>

<style scoped>
.empty-state {
  border: 1px dashed var(--color-border-strong);
  border-radius: var(--rounded-lg);
  padding: 32px 20px;
  text-align: center;
  background: var(--color-inset);
  display: flex;
  flex-direction: column;
  align-items: center;
}

.empty-state__icon {
  width: 48px;
  height: 48px;
  border-radius: 14px;
  background: var(--color-card);
  border: 1px solid var(--color-border);
  display: grid;
  place-items: center;
  margin-bottom: 13px;
  color: var(--color-faint);
  flex-shrink: 0;
}
.empty-state__icon svg {
  width: 22px;
  height: 22px;
}

.empty-state__title {
  font-size: 0.92rem;
  font-weight: 600;
  color: var(--color-text);
}

.empty-state__desc {
  font-size: 0.8rem;
  color: var(--color-faint);
  margin: 6px auto 0;
  max-width: 38ch;
  line-height: 1.55;
}

.empty-state__cta {
  margin-top: 16px;
}
</style>
