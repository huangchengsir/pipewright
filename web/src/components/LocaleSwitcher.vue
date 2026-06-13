<script setup lang="ts">
/**
 * 语言切换器。固定在右下角(主题切换左侧),全局可见。
 * 只有两种语言时点击即在两者间切换;>2 种时循环到下一种。当前语言显示在按钮上。
 */
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SUPPORTED_LOCALES, setLocale, type LocaleCode } from '../i18n'

const { t, locale } = useI18n({ useScope: 'global' })

// 短标:中文显「中文」、英文显「EN」(按钮窄,用紧凑形式)。
const SHORT: Record<LocaleCode, string> = { 'zh-CN': '中文', en: 'EN' }

const displayText = computed(() => `🌐 ${SHORT[locale.value as LocaleCode] ?? locale.value}`)
const ariaLabel = computed(() => t('locale.label'))

function cycle(): void {
  const codes = SUPPORTED_LOCALES.map((l) => l.code)
  const idx = codes.indexOf(locale.value as LocaleCode)
  const next = codes[(idx + 1) % codes.length]
  setLocale(next)
}
</script>

<template>
  <button
    class="locale-switcher"
    type="button"
    :aria-label="ariaLabel"
    :title="ariaLabel"
    @click="cycle"
  >
    {{ displayText }}
  </button>
</template>

<style scoped>
.locale-switcher {
  position: fixed;
  right: 112px;
  bottom: 18px;
  font-family: var(--font-sans);
  font-size: 0.74rem;
  color: var(--color-dim);
  border: 1px solid var(--color-border);
  background: var(--color-card);
  border-radius: var(--rounded);
  padding: 7px 13px;
  cursor: pointer;
  z-index: 200;
  transition:
    color var(--duration-fast),
    border-color var(--duration-fast),
    background-color var(--duration-fast);
  box-shadow: var(--shadow);
}

.locale-switcher:hover {
  color: var(--color-text);
  border-color: var(--color-border-strong);
}

.locale-switcher:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}
</style>
