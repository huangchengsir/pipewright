import { onBeforeUnmount, onMounted, ref, watch, type Ref } from 'vue'

/** Extra scrolling is confined to viewports that cannot fit the original card and the addon. */
export function useLoginViewport(card: Ref<HTMLElement | null>, space: Ref<number>) {
  const overflow = ref(false)
  const minHeight = ref('100vh')
  const cardTop = ref('110px')
  let observer: ResizeObserver | undefined
  function measure() {
    const height = card.value ? Number.parseFloat(getComputedStyle(card.value).height) || card.value.offsetHeight : 0
    const reserve = window.innerWidth <= 520 ? 20 : 52
    overflow.value = height > 0 && window.innerHeight < height + (space.value > 0 ? 2 * (14 + space.value + reserve) : 80)
    // Keep the original centered card position when a transient message needs scrolling.
    // Only short viewports use the top clearance needed by the brand.
    const top = Math.max(110, (window.innerHeight - height) / 2)
    cardTop.value = top + 'px'
    minHeight.value = Math.max(window.innerHeight, top + height + 14 + space.value + reserve) + 'px'
  }
  onMounted(() => {
    observer = new ResizeObserver(measure)
    if (card.value) observer.observe(card.value)
    window.addEventListener('resize', measure)
    measure()
  })
  watch(space, measure, { flush: 'post' })
  onBeforeUnmount(() => { observer?.disconnect(); window.removeEventListener('resize', measure) })
  return { overflow, minHeight, cardTop }
}
