<script setup lang="ts">
/**
 * ProgressBar — determinate or indeterminate progress.
 * value: 0–100 for determinate; omit (undefined) for indeterminate.
 * variant: 'default' | 'success' | 'warn' | 'error'
 */
export type ProgressVariant = 'default' | 'success' | 'warn' | 'error'

const props = withDefaults(defineProps<{
  value?: number
  variant?: ProgressVariant
  label?: string
}>(), {
  variant: 'default',
})

const isDeterminate = () => props.value !== undefined
</script>

<template>
  <div class="progress-bar" :aria-label="label">
    <div
      class="progress-bar__track"
      :class="{ 'progress-bar__track--indeterminate': !isDeterminate() }"
      role="progressbar"
      :aria-valuenow="isDeterminate() ? value : undefined"
      :aria-valuemin="isDeterminate() ? 0 : undefined"
      :aria-valuemax="isDeterminate() ? 100 : undefined"
      :aria-label="label"
    >
      <!-- Determinate fill -->
      <span
        v-if="isDeterminate()"
        class="progress-bar__fill"
        :class="`progress-bar__fill--${variant}`"
        :style="{ width: `${Math.min(100, Math.max(0, value ?? 0))}%` }"
      />

      <!-- Indeterminate shimmer -->
      <span
        v-else
        class="progress-bar__indeterminate"
        :class="`progress-bar__indeterminate--${variant}`"
        aria-hidden="true"
      />
    </div>
  </div>
</template>

<style scoped>
.progress-bar {
  width: 100%;
}

.progress-bar__track {
  height: 6px;
  border-radius: var(--rounded-full);
  background: var(--color-inset);
  overflow: hidden;
  position: relative;
}

/* ——— Determinate fill ——— */
.progress-bar__fill {
  display: block;
  height: 100%;
  border-radius: var(--rounded-full);
  transition: width var(--duration-normal) var(--ease-out-expo);
}
.progress-bar__fill--default { background: var(--color-primary); }
.progress-bar__fill--success { background: var(--color-green); }
.progress-bar__fill--warn    { background: var(--color-amber); }
.progress-bar__fill--error   { background: var(--color-red); }

/* ——— Indeterminate flow ——— */
.progress-bar__indeterminate {
  position: absolute;
  inset: 0;
  width: 40%;
  background: linear-gradient(90deg, transparent, var(--color-primary), transparent);
  animation: pb-flow 1.5s linear infinite;
}
.progress-bar__indeterminate--warn {
  background: linear-gradient(90deg, transparent, var(--color-amber), transparent);
}
.progress-bar__indeterminate--success {
  background: linear-gradient(90deg, transparent, var(--color-green), transparent);
}
.progress-bar__indeterminate--error {
  background: linear-gradient(90deg, transparent, var(--color-red), transparent);
}

@keyframes pb-flow {
  0%   { transform: translateX(-120%); }
  100% { transform: translateX(320%); }
}

@media (prefers-reduced-motion: reduce) {
  .progress-bar__indeterminate {
    animation: none;
    opacity: 0.5;
  }
}
</style>
