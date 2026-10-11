<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'

defineOptions({ inheritAttrs: false })

export interface SelectOption {
  value: string
  label: string
}

const props = withDefaults(
  defineProps<{
    inputId?: string
    modelValue: string
    options: SelectOption[]
    placeholder?: string
    disabled?: boolean
    ariaLabel?: string
    minWidth?: string
    height?: string
    portal?: boolean
    searchable?: boolean
    searchPlaceholder?: string
  }>(),
  {
    inputId: undefined,
    placeholder: '',
    disabled: false,
    ariaLabel: undefined,
    minWidth: '180px',
    height: undefined,
    portal: false,
    searchable: false,
    searchPlaceholder: '',
  },
)

const emit = defineEmits<{
  'update:modelValue': [value: string]
  change: []
}>()

const root = ref<HTMLElement | null>(null)
const menu = ref<HTMLElement | null>(null)
const open = ref(false)
const activeIndex = ref(-1)
const menuStyle = ref<Record<string, string>>({})
const selected = computed(() =>
  props.options.find((option) => option.value === props.modelValue),
)
const query = ref('')
const visibleOptions = computed(() =>
  props.searchable && query.value
    ? props.options.filter((option) =>
        (option.label + ' ' + option.value)
          .toLowerCase()
          .includes(query.value.toLowerCase()),
      )
    : props.options,
)
watch(query, () => {
  activeIndex.value = 0
})
watch(
  () => props.disabled,
  (disabled) => {
    if (disabled) close()
  },
  { flush: 'sync' },
)
watch(
  visibleOptions,
  (options) => {
    if (!open.value) return
    const selectedIndex = options.findIndex(
      (option) => option.value === props.modelValue,
    )
    activeIndex.value =
      selectedIndex >= 0
        ? selectedIndex
        : Math.min(Math.max(0, activeIndex.value), options.length - 1)
    positionMenu()
  },
  { flush: 'sync' },
)

function positionMenu(): void {
  if (!props.portal || !root.value) return
  const rect = root.value.getBoundingClientRect()
  const availableBelow = window.innerHeight - rect.bottom - 8
  const availableAbove = rect.top - 8
  const desiredHeight = Math.min(
    240,
    props.options.length * 38 + 12 + (props.searchable ? 38 : 0),
  )
  const above =
    availableBelow < desiredHeight && availableAbove > availableBelow
  const maxHeight = Math.max(
    0,
    Math.min(240, above ? availableAbove : availableBelow),
  )
  menuStyle.value = {
    left: `${rect.left}px`,
    width: `${rect.width}px`,
    top: above ? 'auto' : `${rect.bottom + 6}px`,
    bottom: above ? `${window.innerHeight - rect.top + 6}px` : 'auto',
    maxHeight: `${maxHeight}px`,
  }
}

function close(): void {
  open.value = false
  activeIndex.value = -1
  query.value = ''
}

function toggle(): void {
  if (props.disabled) return
  if (open.value) {
    close()
    return
  }
  positionMenu()
  open.value = true
  activeIndex.value = Math.max(
    0,
    visibleOptions.value.findIndex(
      (option) => option.value === props.modelValue,
    ),
  )
}

function choose(option: SelectOption): void {
  if (props.disabled) return
  emit('update:modelValue', option.value)
  emit('change')
  close()
}

function moveActive(direction: 1 | -1): void {
  if (!visibleOptions.value.length) return
  activeIndex.value =
    (activeIndex.value + direction + visibleOptions.value.length) %
    visibleOptions.value.length
}

function handleKeydown(event: KeyboardEvent): void {
  if (props.disabled || event.isComposing || event.keyCode === 229) return
  if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
    event.preventDefault()
    if (!open.value) toggle()
    else moveActive(event.key === 'ArrowDown' ? 1 : -1)
  } else if ((event.key === 'Enter' || event.key === ' ') && open.value) {
    event.preventDefault()
    const option = visibleOptions.value[activeIndex.value]
    if (option) choose(option)
  } else if (event.key === 'Escape' && open.value) {
    event.preventDefault()
    close()
  }
}

function handlePointerdown(event: PointerEvent): void {
  if (
    open.value &&
    root.value &&
    !root.value.contains(event.target as Node) &&
    !menu.value?.contains(event.target as Node)
  )
    close()
}

onMounted(() => {
  document.addEventListener('pointerdown', handlePointerdown)
  window.addEventListener('resize', positionMenu)
  window.addEventListener('scroll', positionMenu, true)
})
onBeforeUnmount(() => {
  document.removeEventListener('pointerdown', handlePointerdown)
  window.removeEventListener('resize', positionMenu)
  window.removeEventListener('scroll', positionMenu, true)
})
</script>

<template>
  <div
    ref="root"
    class="app-select"
    :class="{ 'app-select--open': open }"
    :style="{ minWidth, '--app-select-height': height }"
  >
    <button
      v-bind="$attrs"
      :id="inputId"
      type="button"
      class="app-select__trigger"
      role="combobox"
      :aria-label="ariaLabel"
      :aria-expanded="open"
      :aria-controls="inputId ? `${inputId}-options` : undefined"
      aria-haspopup="listbox"
      :disabled="disabled"
      @click="toggle"
      @keydown="handleKeydown"
    >
      <span
        :class="selected ? 'app-select__value' : 'app-select__placeholder'"
        >{{ selected?.label || placeholder }}</span
      >
      <svg
        class="app-select__chevron"
        width="15"
        height="15"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        stroke-width="2"
        aria-hidden="true"
      >
        <path d="m6 9 6 6 6-6" />
      </svg>
    </button>
    <Teleport to="body" :disabled="!portal">
      <div
        v-if="open"
        :id="inputId ? `${inputId}-options` : undefined"
        ref="menu"
        class="app-select__menu"
        :class="{ 'app-select__menu--portal': portal }"
        :style="portal ? menuStyle : undefined"
        role="listbox"
      >
        <input
          v-if="searchable"
          v-model="query"
          class="app-select__search"
          type="search"
          :aria-label="searchPlaceholder"
          :placeholder="searchPlaceholder"
          @keydown="
            ['ArrowDown', 'ArrowUp', 'Enter', 'Escape'].includes($event.key) &&
            handleKeydown($event)
          "
        />
        <button
          v-for="(option, index) in visibleOptions"
          :key="option.value"
          type="button"
          class="app-select__option"
          :class="{
            'app-select__option--active': index === activeIndex,
            'app-select__option--selected': option.value === modelValue,
          }"
          role="option"
          :aria-selected="option.value === modelValue"
          @mouseenter="activeIndex = index"
          @click="choose(option)"
        >
          <span>{{ option.label }}</span>
          <svg
            v-if="option.value === modelValue"
            class="app-select__check"
            width="15"
            height="15"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="2.5"
            aria-hidden="true"
          >
            <path d="m5 12 4 4L19 6" />
          </svg>
        </button>
        <div v-if="!visibleOptions.length" class="app-select__empty">
          {{ placeholder }}
        </div>
      </div>
    </Teleport>
  </div>
</template>

<style scoped>
.app-select {
  position: relative;
  width: 100%;
}
.app-select__trigger {
  position: relative;
  display: flex;
  align-items: center;
  width: 100%;
  height: var(--app-select-height, 38px);
  min-height: 0;
  padding: 8px 34px 8px 12px;
  border: 1px solid var(--color-border);
  border-radius: var(--rounded);
  background: var(--color-card-2);
  color: var(--color-text);
  font: inherit;
  text-align: left;
  cursor: pointer;
  transition:
    border-color var(--duration-fast),
    box-shadow var(--duration-fast),
    background var(--duration-fast);
}
.app-select__trigger:hover:not(:disabled) {
  border-color: var(--color-border-strong);
  background: var(--color-inset);
}
.app-select--open .app-select__trigger,
.app-select__trigger:focus-visible {
  outline: none;
  border-color: var(--color-primary);
  box-shadow: 0 0 0 3px var(--color-primary-soft);
}
.app-select__trigger:disabled {
  opacity: 0.55;
  cursor: not-allowed;
}
.app-select__value,
.app-select__placeholder {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.app-select__placeholder {
  color: var(--color-faint);
}
.app-select__chevron {
  position: absolute;
  right: 11px;
  color: var(--color-faint);
  transition:
    transform var(--duration-fast),
    color var(--duration-fast);
}
.app-select--open .app-select__chevron {
  transform: rotate(180deg);
  color: var(--color-primary);
}
.app-select__menu {
  box-sizing: border-box;
  position: absolute;
  z-index: 20;
  top: calc(100% + 6px);
  left: 0;
  width: 100%;
  max-height: 240px;
  overflow-y: auto;
  padding: 5px;
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded);
  background: var(--color-card-2);
  box-shadow: 0 12px 28px rgb(0 0 0 / 24%);
}
.app-select__menu--portal {
  position: fixed;
  z-index: 10000;
}
.app-select__option {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  width: 100%;
  min-height: 38px;
  padding: 8px 9px;
  border: 0;
  border-radius: calc(var(--rounded) - 2px);
  background: transparent;
  color: var(--color-text);
  font: inherit;
  text-align: left;
  cursor: pointer;
}
.app-select__option:hover,
.app-select__option--active {
  background: var(--color-inset);
}
.app-select__option--selected,
.app-select__check {
  color: var(--color-primary);
}
.app-select__empty {
  padding: 10px 9px;
  color: var(--color-faint);
  font-size: 0.8rem;
}
.app-select__search {
  width: 100%;
  box-sizing: border-box;
  min-height: 38px;
  padding: 8px;
  border: 1px solid var(--color-border);
  border-radius: var(--rounded);
  background: var(--color-inset);
  color: var(--color-text);
  font: inherit;
  position: sticky;
  top: 0;
}
</style>
