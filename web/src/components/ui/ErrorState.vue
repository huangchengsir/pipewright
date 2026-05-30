<script setup lang="ts">
/**
 * ErrorState — error / degraded state panel.
 * variant: 'error' (page-level load failure, retryable) | 'ai' (AI unavailable, non-blocking).
 * Emits 'retry' when user clicks retry button.
 */
export type ErrorStateVariant = 'error' | 'ai'

const props = withDefaults(defineProps<{
  variant?: ErrorStateVariant
  title?: string
  description?: string
  confidence?: number       // 0–100, optional, ai variant
  showRetry?: boolean
}>(), {
  variant: 'error',
  title: '加载失败',
  showRetry: true,
})

const emit = defineEmits<{
  retry: []
}>()
</script>

<template>
  <!-- AI Degraded variant -->
  <div v-if="variant === 'ai'" class="error-ai" role="note" aria-label="AI 功能暂不可用">
    <div class="error-ai__header">
      <span class="error-ai__icon" aria-hidden="true">
        <!-- AI / waveform icon -->
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M3 12h3.5l2.2-6 3.6 12 2.4-7 1.3 1h4.5" />
        </svg>
      </span>
      <span class="error-ai__title">{{ title ?? 'AI 失败诊断' }}</span>
      <span class="error-ai__tag">暂不可用</span>
    </div>
    <p class="error-ai__desc">
      {{ description ?? 'LLM 提供商无响应,本次未生成诊断。运行结果与日志照常记录,核心 CI/CD 不受影响。' }}
    </p>

    <!-- Confidence meter slot -->
    <div v-if="confidence !== undefined" class="error-ai__confidence">
      <span class="error-ai__conf-label">置信度 {{ confidence }}% · {{ confidence >= 80 ? '高' : confidence >= 50 ? '中' : '低' }}</span>
      <span class="error-ai__conf-bar">
        <span class="error-ai__conf-fill" :style="{ width: `${confidence}%` }" />
      </span>
    </div>

    <div v-if="$slots.actions" class="error-ai__actions">
      <slot name="actions" />
    </div>
  </div>

  <!-- Page/Card Error variant -->
  <div v-else class="error-state" role="alert" aria-live="assertive">
    <div class="error-state__icon" aria-hidden="true">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round">
        <path d="M12 9v4m0 4h.01M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z" />
      </svg>
    </div>

    <strong class="error-state__title">{{ title }}</strong>
    <p v-if="description" class="error-state__desc">{{ description }}</p>
    <slot name="description" />

    <div class="error-state__actions">
      <button
        v-if="showRetry"
        class="error-state__retry"
        type="button"
        @click="emit('retry')"
      >
        ↻ 重试
      </button>
      <slot name="actions" />
    </div>
  </div>
</template>

<style scoped>
/* ——— Page/Card Error ——— */
.error-state {
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-lg);
  padding: 28px 20px;
  text-align: center;
  background: var(--color-card-2);
}

.error-state__icon {
  width: 46px;
  height: 46px;
  border-radius: 13px;
  background: var(--color-red-soft);
  color: var(--color-red);
  display: grid;
  place-items: center;
  margin: 0 auto 12px;
}
.error-state__icon svg {
  width: 22px;
  height: 22px;
}

.error-state__title {
  font-size: 0.9rem;
  font-weight: 600;
  color: var(--color-text);
}

.error-state__desc {
  font-size: 0.79rem;
  color: var(--color-faint);
  margin: 6px 0 14px;
  line-height: 1.5;
}

.error-state__actions {
  margin-top: 14px;
  display: flex;
  justify-content: center;
  gap: 8px;
}

.error-state__retry {
  height: 34px;
  border: 1px solid var(--color-border-strong);
  background: var(--color-card);
  color: var(--color-text);
  font-family: var(--font-sans);
  font-size: 0.82rem;
  font-weight: 500;
  padding: 0 15px;
  border-radius: var(--rounded);
  cursor: pointer;
  transition:
    background-color var(--duration-fast),
    border-color var(--duration-fast);
}
.error-state__retry:hover {
  border-color: var(--color-faint);
}
.error-state__retry:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}

/* ——— AI Degraded ——— */
.error-ai {
  border: 1px dashed var(--color-cyan-line);
  border-radius: var(--rounded-lg);
  padding: 16px 18px;
  background: linear-gradient(180deg, var(--color-cyan-soft), transparent 70%);
}

.error-ai__header {
  display: flex;
  align-items: center;
  gap: 9px;
  font-size: 0.85rem;
  font-weight: 600;
  color: var(--color-text);
}

.error-ai__icon {
  width: 24px;
  height: 24px;
  border-radius: 7px;
  background: var(--color-cyan-soft);
  color: var(--color-cyan);
  display: grid;
  place-items: center;
  flex-shrink: 0;
}
.error-ai__icon svg {
  width: 14px;
  height: 14px;
}

.error-ai__title {
  flex: 1;
}

.error-ai__tag {
  margin-left: auto;
  font-size: 0.68rem;
  color: var(--color-faint);
  background: var(--color-inset);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-sm);
  padding: 2px 8px;
  font-weight: 500;
}

.error-ai__desc {
  font-size: 0.8rem;
  color: var(--color-dim);
  margin-top: 9px;
  line-height: 1.55;
}

.error-ai__confidence {
  display: inline-flex;
  align-items: center;
  gap: 9px;
  font-size: 0.74rem;
  color: var(--color-cyan);
  background: var(--color-cyan-soft);
  border: 1px solid var(--color-cyan-line);
  border-radius: var(--rounded-full);
  padding: 4px 11px;
  font-weight: 500;
  margin-top: 12px;
}

.error-ai__conf-label {
  white-space: nowrap;
}

.error-ai__conf-bar {
  width: 60px;
  height: 4px;
  border-radius: var(--rounded-full);
  background: oklch(82% 0.1 205 / 0.2);
  overflow: hidden;
  display: block;
}

.error-ai__conf-fill {
  display: block;
  height: 100%;
  background: var(--color-cyan);
  border-radius: var(--rounded-full);
  transition: width var(--duration-normal) var(--ease-out-expo);
}

.error-ai__actions {
  margin-top: 12px;
}
</style>
