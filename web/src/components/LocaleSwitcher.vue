<script setup lang="ts">
/**
 * 语言选择器。固定在右上角的「Change Language」下拉:地球图标 + 当前语言母语名,
 * 点开列出全部受支持语言(当前项打勾)。全局挂载(App.vue),登录页也可切换。
 */
import { computed, h } from 'vue'
import { useI18n } from 'vue-i18n'
import { NDropdown, NIcon } from 'naive-ui'
import { Language, Check } from '@vicons/tabler'
import { SUPPORTED_LOCALES, setLocale, type LocaleCode } from '../i18n'

const { t, locale } = useI18n({ useScope: 'global' })

const currentLabel = computed(
  () => SUPPORTED_LOCALES.find((l) => l.code === locale.value)?.label ?? locale.value,
)

const options = computed(() =>
  SUPPORTED_LOCALES.map((l) => ({
    key: l.code,
    label: l.label,
    // 当前语言左侧打勾,其余留白对齐。
    icon:
      l.code === locale.value
        ? () => h(NIcon, { component: Check, color: 'var(--color-primary)' })
        : () => h('span', { style: 'display:inline-block;width:14px' }),
  })),
)

function onSelect(key: string): void {
  setLocale(key as LocaleCode)
}
</script>

<template>
  <n-dropdown
    trigger="click"
    :options="options"
    :show-arrow="true"
    placement="bottom-end"
    @select="onSelect"
  >
    <button class="locale-switcher" type="button" :aria-label="t('locale.label')" :title="t('locale.label')">
      <n-icon class="locale-switcher__globe" :component="Language" :size="17" />
      <span class="locale-switcher__label">{{ currentLabel }}</span>
      <svg class="locale-switcher__chev" viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <path d="M6 9l6 6 6-6" />
      </svg>
    </button>
  </n-dropdown>
</template>

<style scoped>
.locale-switcher {
  position: fixed;
  top: 16px;
  right: 20px;
  z-index: 300;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-family: var(--font-sans);
  font-size: 0.78rem;
  color: var(--color-dim);
  border: 1px solid var(--color-border);
  background: var(--color-card);
  border-radius: var(--rounded);
  padding: 6px 11px;
  cursor: pointer;
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

.locale-switcher__globe {
  display: inline-flex;
}

.locale-switcher__label {
  font-weight: 600;
  line-height: 1;
}

.locale-switcher__chev {
  opacity: 0.6;
}
</style>
