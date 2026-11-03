<script setup lang="ts">
import { useId } from 'vue'

const iconId = useId()
const tileId = `${iconId}-tile`
const sheenId = `${iconId}-sheen`
</script>

<template>
  <svg
    class="brand-icon"
    xmlns="http://www.w3.org/2000/svg"
    viewBox="0 0 64 64"
    width="64"
    height="64"
    preserveAspectRatio="xMidYMid meet"
    aria-hidden="true"
    focusable="false"
  >
    <defs>
      <linearGradient :id="tileId" x1="0" y1="0" x2="0" y2="1">
        <stop offset="0" stop-color="#5d8bff" />
        <stop offset="1" stop-color="#3b66f0" />
      </linearGradient>
      <linearGradient :id="sheenId" x1="0" y1="0" x2="0" y2="1">
        <stop offset="0" stop-color="#ffffff" stop-opacity="0.22" />
        <stop offset="0.5" stop-color="#ffffff" stop-opacity="0" />
      </linearGradient>
    </defs>
    <rect
      x="4"
      y="4"
      width="56"
      height="56"
      rx="16"
      :fill="`url(#${tileId})`"
    />
    <rect
      x="4"
      y="4"
      width="56"
      height="56"
      rx="16"
      :fill="`url(#${sheenId})`"
    />
    <g
      fill="none"
      stroke="#ffffff"
      stroke-width="6"
      stroke-linecap="round"
      stroke-linejoin="round"
    >
      <circle cx="21" cy="32" r="1.5" fill="#ffffff" stroke="none" />
      <circle cx="21" cy="32" r="5.5" />
      <path d="M32 20 L46 32 L32 44" />
    </g>
  </svg>
</template>

<style scoped>
.brand-icon {
  display: block;
  flex-shrink: 0;
  aspect-ratio: 1;
}
</style>
