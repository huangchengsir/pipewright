<script setup lang="ts">
import { onBeforeUnmount, onMounted, useTemplateRef, watch } from 'vue'
import { createRenderGrid, type RenderGridEngine } from '../../lib/renderGrid'

const props = defineProps<{
  content: HTMLElement | null
  protectedElements: HTMLElement[]
}>()

const layer = useTemplateRef<HTMLDivElement>('layer')
const canvas = useTemplateRef<HTMLCanvasElement>('canvas')
let desktop: MediaQueryList | undefined
let reducedMotion: MediaQueryList | undefined
let grid: RenderGridEngine | null = null

function stopGrid(): void {
  grid?.destroy()
  grid = null
  if (canvas.value) {
    canvas.value.width = 0
    canvas.value.height = 0
  }
}

function syncGrid(): void {
  stopGrid()
  if (!desktop?.matches || !layer.value || !canvas.value || !props.content)
    return
  grid = createRenderGrid({
    container: layer.value,
    canvas: canvas.value,
    content: props.content,
    protectedElements: props.protectedElements,
    coverage: 'corners',
    diagonal: 'secondary',
    motion: !reducedMotion?.matches,
  })
}

watch([() => props.content, () => props.protectedElements], syncGrid, {
  flush: 'post',
})

onMounted(() => {
  desktop = window.matchMedia('(min-width: 1025px)')
  reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)')
  desktop.addEventListener('change', syncGrid)
  reducedMotion.addEventListener('change', syncGrid)
  syncGrid()
})

onBeforeUnmount(() => {
  desktop?.removeEventListener('change', syncGrid)
  reducedMotion?.removeEventListener('change', syncGrid)
  stopGrid()
})
</script>

<template>
  <div ref="layer" class="showcase-grid" aria-hidden="true">
    <canvas ref="canvas" width="0" height="0" />
  </div>
</template>

<style scoped>
.showcase-grid {
  --border-primary: var(--lp-grid-line);
  position: absolute;
  inset: 0;
  z-index: -1;
  overflow: hidden;
  pointer-events: none;
}
.showcase-grid canvas {
  position: absolute;
  inset: 0;
  display: block;
}
</style>
