<script setup lang="ts">
/**
 * SkeletonBlock — shimmer loading placeholder.
 * width / height can be CSS value strings or numbers (treated as px).
 * circle = true renders as circle (avatar placeholder).
 * shimmer animation is 1.4s ease-in-out; respects prefers-reduced-motion via global.css.
 */
const props = withDefaults(defineProps<{
  width?: string | number
  height?: string | number
  circle?: boolean
  rounded?: boolean
}>(), {
  height: 11,
  circle: false,
  rounded: false,
})

function toPx(v: string | number | undefined): string | undefined {
  if (v === undefined) return undefined
  return typeof v === 'number' ? `${v}px` : v
}
</script>

<template>
  <span
    class="sk-block"
    :class="{
      'sk-block--circle': circle,
      'sk-block--rounded': rounded && !circle,
    }"
    :style="{
      width: toPx(width),
      height: toPx(height),
    }"
    aria-hidden="true"
  />
</template>

<style scoped>
.sk-block {
  display: block;
  background: linear-gradient(
    90deg,
    var(--color-inset) 25%,
    oklch(100% 0 0 / 0.06) 37%,
    var(--color-inset) 63%
  );
  background-size: 400% 100%;
  border-radius: var(--rounded-sm);
  animation: sk-shimmer 1.4s ease-in-out infinite;
}

.sk-block--circle {
  border-radius: var(--rounded-full);
}

.sk-block--rounded {
  border-radius: var(--rounded-md);
}

@keyframes sk-shimmer {
  0%   { background-position: 100% 0; }
  100% { background-position: -100% 0; }
}
</style>
