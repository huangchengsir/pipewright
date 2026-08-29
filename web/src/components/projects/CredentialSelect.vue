<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import type { Credential } from '../../api/credentials'

interface Props {
  inputId: string
  modelValue: string
  credentials: Credential[]
  disabled?: boolean
  loading?: boolean
  hasError?: boolean
  placeholder: string
  loadingLabel: string
  emptyLabel: string
}

const props = defineProps<Props>()
const emit = defineEmits<{
  'update:modelValue': [value: string]
  change: []
}>()

const root = ref<HTMLElement | null>(null)
const open = ref(false)
const activeIndex = ref(-1)

const selected = computed(() => props.credentials.find((credential) => credential.id === props.modelValue))

function close(): void {
  open.value = false
  activeIndex.value = -1
}

function toggle(): void {
  if (props.disabled || props.loading) return
  open.value = !open.value
  if (open.value) {
    activeIndex.value = Math.max(0, props.credentials.findIndex((credential) => credential.id === props.modelValue))
  }
}

function choose(credential: Credential): void {
  emit('update:modelValue', credential.id)
  emit('change')
  close()
}

function moveActive(direction: 1 | -1): void {
  if (!props.credentials.length) return
  const next = activeIndex.value + direction
  activeIndex.value = (next + props.credentials.length) % props.credentials.length
}

function handleKeydown(event: KeyboardEvent): void {
  if (props.disabled) return
  if (event.key === 'ArrowDown') {
    event.preventDefault()
    if (!open.value) toggle()
    else moveActive(1)
  } else if (event.key === 'ArrowUp') {
    event.preventDefault()
    if (!open.value) toggle()
    else moveActive(-1)
  } else if ((event.key === 'Enter' || event.key === ' ') && open.value) {
    event.preventDefault()
    const credential = props.credentials[activeIndex.value]
    if (credential) choose(credential)
  } else if (event.key === 'Escape' && open.value) {
    event.preventDefault()
    close()
  }
}

function handleDocumentPointerdown(event: PointerEvent): void {
  if (open.value && root.value && !root.value.contains(event.target as Node)) close()
}

onMounted(() => document.addEventListener('pointerdown', handleDocumentPointerdown))
onBeforeUnmount(() => document.removeEventListener('pointerdown', handleDocumentPointerdown))
</script>

<template>
  <div ref="root" class="credential-select" :class="{ 'credential-select--open': open }">
    <button
      :id="inputId"
      type="button"
      class="credential-select__trigger"
      :class="{ 'credential-select__trigger--error': hasError }"
      role="combobox"
      :aria-expanded="open"
      :aria-controls="`${inputId}-options`"
      :aria-haspopup="'listbox'"
      :aria-invalid="hasError ? 'true' : undefined"
      :disabled="disabled || loading"
      @click="toggle"
      @keydown="handleKeydown"
    >
      <span v-if="loading" class="credential-select__placeholder">{{ loadingLabel }}</span>
      <template v-else-if="selected">
        <span class="credential-select__main">{{ selected.name }}</span>
        <span class="credential-select__masked">{{ selected.maskedValue }}</span>
      </template>
      <span v-else class="credential-select__placeholder">{{ placeholder }}</span>
      <svg class="credential-select__chevron" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
        <path d="m6 9 6 6 6-6" />
      </svg>
    </button>

    <div v-if="open" :id="`${inputId}-options`" class="credential-select__menu" role="listbox" :aria-labelledby="inputId">
      <button
        v-for="(credential, index) in credentials"
        :key="credential.id"
        type="button"
        class="credential-select__option"
        :class="{ 'credential-select__option--active': index === activeIndex, 'credential-select__option--selected': credential.id === modelValue }"
        role="option"
        :aria-selected="credential.id === modelValue"
        @mouseenter="activeIndex = index"
        @click="choose(credential)"
      >
        <span class="credential-select__option-copy">
          <span class="credential-select__main">{{ credential.name }}</span>
          <span v-if="credential.username" class="credential-select__username">{{ credential.username }}</span>
        </span>
        <span class="credential-select__masked">{{ credential.maskedValue }}</span>
        <svg v-if="credential.id === modelValue" class="credential-select__check" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" aria-hidden="true">
          <path d="m5 12 4 4L19 6" />
        </svg>
      </button>
      <div v-if="!credentials.length" class="credential-select__empty">{{ emptyLabel }}</div>
    </div>
  </div>
</template>

<style scoped>
.credential-select {
  position: relative;
}

.credential-select__trigger {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  min-height: 40px;
  padding: 8px 38px 8px 12px;
  border: 1px solid var(--color-border);
  border-radius: var(--rounded);
  background: var(--color-inset);
  color: var(--color-text);
  font: inherit;
  font-size: 0.86rem;
  text-align: left;
  cursor: pointer;
  position: relative;
  transition: border-color var(--duration-fast), box-shadow var(--duration-fast), background var(--duration-fast);
}

.credential-select__trigger:hover:not(:disabled) {
  border-color: var(--color-border-strong);
  background: var(--color-card-2);
}

.credential-select--open .credential-select__trigger,
.credential-select__trigger:focus-visible {
  outline: none;
  border-color: var(--color-primary);
  box-shadow: 0 0 0 3px var(--color-primary-soft);
}

.credential-select__trigger--error {
  border-color: var(--color-red);
}

.credential-select__trigger:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.credential-select__main,
.credential-select__placeholder {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.credential-select__main {
  font-weight: 600;
}

.credential-select__placeholder {
  color: var(--color-faint);
}

.credential-select__masked {
  margin-left: auto;
  color: var(--color-faint);
  font-family: var(--font-mono);
  font-size: 0.74rem;
  white-space: nowrap;
}

.credential-select__chevron {
  position: absolute;
  right: 12px;
  color: var(--color-faint);
  transition: transform var(--duration-fast), color var(--duration-fast);
}

.credential-select--open .credential-select__chevron {
  transform: rotate(180deg);
  color: var(--color-primary);
}

.credential-select__menu {
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
  box-shadow: 0 12px 28px rgb(0 0 0 / 28%);
}

.credential-select__option {
  display: flex;
  align-items: center;
  gap: 9px;
  width: 100%;
  min-height: 42px;
  padding: 8px 9px;
  border: 0;
  border-radius: calc(var(--rounded) - 2px);
  background: transparent;
  color: var(--color-text);
  font: inherit;
  text-align: left;
  cursor: pointer;
}

.credential-select__option:hover,
.credential-select__option--active {
  background: var(--color-inset);
}

.credential-select__option--selected .credential-select__main,
.credential-select__check {
  color: var(--color-primary);
}

.credential-select__option-copy {
  display: grid;
  min-width: 0;
  gap: 2px;
}

.credential-select__username {
  overflow: hidden;
  color: var(--color-faint);
  font-size: 0.72rem;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.credential-select__check {
  flex: 0 0 auto;
}

.credential-select__empty {
  padding: 12px 9px;
  color: var(--color-faint);
  font-size: 0.8rem;
}
</style>
