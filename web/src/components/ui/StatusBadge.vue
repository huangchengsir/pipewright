<script setup lang="ts">
/**
 * StatusBadge — frozen six-state vocabulary.
 * Props: status = 'success' | 'failed' | 'running' | 'partial' | 'rolledback' | 'queued'
 * Visual: color token + dot (running dot pulses) + Chinese text label.
 * Accessibility: not colour-alone — dot shape + text always present.
 * Animation: pulse only on transform/opacity, reduced-motion degrades in global.css.
 */

export type BadgeStatus = 'success' | 'failed' | 'running' | 'partial' | 'rolledback' | 'queued'

const props = defineProps<{
  status: BadgeStatus
}>()

const labelMap: Record<BadgeStatus, string> = {
  success:    '成功',
  failed:     '失败',
  running:    '进行中',
  partial:    '部分失败',
  rolledback: '已回滚',
  queued:     '排队',
}
</script>

<template>
  <span class="badge" :class="`badge--${props.status}`">
    <span class="badge__dot" aria-hidden="true" />
    <span class="badge__label">{{ labelMap[props.status] }}</span>
  </span>
</template>

<style scoped>
.badge {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: var(--text-label);
  font-weight: 600;
  padding: 3px 9px 3px 8px;
  border-radius: var(--rounded-md);
  line-height: 1;
  white-space: nowrap;
}

.badge__dot {
  width: 6px;
  height: 6px;
  border-radius: var(--rounded-full);
  flex-shrink: 0;
}

/* ——— success ——— */
.badge--success {
  color: var(--color-green);
  background: var(--color-green-soft);
}
.badge--success .badge__dot {
  background: var(--color-green);
}

/* ——— failed ——— */
.badge--failed {
  color: var(--color-red);
  background: var(--color-red-soft);
  border: 1px solid var(--color-red-line);
}
.badge--failed .badge__dot {
  background: var(--color-red);
}

/* ——— running ——— */
.badge--running {
  color: var(--color-amber);
  background: var(--color-amber-soft);
}
.badge--running .badge__dot {
  background: var(--color-amber);
  animation: badge-pulse 1.1s ease-in-out infinite;
}

/* ——— partial ——— */
.badge--partial {
  color: var(--color-amber);
  background: var(--color-amber-soft);
  border: 1px solid var(--color-amber-line);
}
.badge--partial .badge__dot {
  background: var(--color-amber);
}

/* ——— rolledback ——— */
.badge--rolledback {
  color: var(--color-dim);
  background: var(--color-inset);
}
.badge--rolledback .badge__dot {
  background: var(--color-faint);
}

/* ——— queued ——— */
.badge--queued {
  color: var(--color-faint);
  background: var(--color-inset);
}
.badge--queued .badge__dot {
  background: var(--color-faint);
}

/* Pulse animation — only opacity, no layout shift */
@keyframes badge-pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.35; }
}

/* Reduced motion: no pulse */
@media (prefers-reduced-motion: reduce) {
  .badge--running .badge__dot {
    animation: none;
  }
}
</style>
