<script setup lang="ts">
import { useI18n } from 'vue-i18n'
const { t } = useI18n()
</script>
<template>
  <span class="waiting" role="status" :aria-label="t('opsChat.waiting')"
    ><span /><span /><span
  /></span>
</template>
<style scoped>
.waiting {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  height: 22px;
  width: 30px;
}
.waiting span {
  width: 5px;
  height: 5px;
  border-radius: 50%;
  background: var(--color-text-muted);
  animation: reply-dot 1.2s infinite;
}
.waiting span:nth-child(2) {
  animation-delay: 0.2s;
}
.waiting span:nth-child(3) {
  animation-delay: 0.4s;
}
@keyframes reply-dot {
  0%,
  70%,
  100% {
    opacity: 0.35;
  }
  35% {
    opacity: 1;
  }
}
@media (prefers-reduced-motion: reduce) {
  .waiting span {
    animation: none;
    opacity: 0.7;
  }
}
</style>
