<script setup lang="ts">
/**
 * FormField — labelled form field wrapper with error state.
 * Provides: label + input slot + error message with aria-describedby wiring.
 * The control inside must accept id + aria-describedby + aria-invalid props (via slot props).
 */
import { computed } from 'vue'

const props = defineProps<{
  label: string
  fieldId: string
  error?: string
  hint?: string
  required?: boolean
  disabled?: boolean
}>()

const errorId = computed(() => props.error ? `${props.fieldId}-err` : undefined)
const hintId  = computed(() => props.hint  ? `${props.fieldId}-hint` : undefined)
const describedBy = computed(() => [hintId.value, errorId.value].filter(Boolean).join(' ') || undefined)
</script>

<template>
  <div
    class="form-field"
    :class="{
      'form-field--error': !!error,
      'form-field--disabled': disabled,
    }"
  >
    <!-- Label row -->
    <div class="form-field__label-row">
      <label :for="fieldId" class="form-field__label">
        {{ label }}
        <span v-if="required" class="form-field__required" aria-hidden="true">*</span>
      </label>
    </div>

    <!-- Input slot — passes aria wiring to child via slot props -->
    <slot
      :field-id="fieldId"
      :aria-invalid="error ? 'true' : undefined"
      :aria-describedby="describedBy"
      :aria-required="required || undefined"
      :disabled="disabled || undefined"
    />

    <!-- Hint -->
    <p v-if="hint && !error" :id="hintId" class="form-field__hint">{{ hint }}</p>

    <!-- Error -->
    <p v-if="error" :id="errorId" class="form-field__error" role="alert" aria-live="polite">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" aria-hidden="true">
        <circle cx="12" cy="12" r="9" />
        <path d="M12 8v5M12 16h.01" />
      </svg>
      {{ error }}
    </p>
  </div>
</template>

<style scoped>
.form-field {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.form-field__label-row {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.form-field__label {
  font-size: var(--text-label);
  font-weight: 500;
  color: var(--color-dim);
  cursor: pointer;
}

.form-field--disabled .form-field__label {
  opacity: 0.5;
}

.form-field__required {
  color: var(--color-red);
  margin-left: 2px;
}

.form-field__hint {
  font-size: 0.76rem;
  color: var(--color-faint);
  line-height: 1.4;
}

.form-field__error {
  display: flex;
  align-items: center;
  gap: 5px;
  font-size: 0.74rem;
  color: var(--color-red);
  line-height: 1.4;
}
.form-field__error svg {
  width: 12px;
  height: 12px;
  flex-shrink: 0;
}

/* Pass error ring down to native input when used without slot-binding */
.form-field--error :deep(input),
.form-field--error :deep(textarea),
.form-field--error :deep(select) {
  border-color: var(--color-red) !important;
}
.form-field--error :deep(input:focus),
.form-field--error :deep(textarea:focus) {
  box-shadow: 0 0 0 3px var(--color-red-soft) !important;
}
</style>
