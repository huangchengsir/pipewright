<script setup lang="ts">
import { NConfigProvider, NMessageProvider } from 'naive-ui'
import { RouterView } from 'vue-router'
import { useNaiveTheme } from './composables/useNaiveTheme'
import { useNaiveLocale } from './composables/useNaiveLocale'
import ConfirmDialog from './components/ui/ConfirmDialog.vue'
import ToastHost from './components/ui/ToastHost.vue'
import LocaleSwitcher from './components/LocaleSwitcher.vue'

const { naiveTheme, naiveThemeOverrides } = useNaiveTheme()
const { naiveLocale, naiveDateLocale } = useNaiveLocale()
</script>

<template>
  <n-config-provider
    :theme="naiveTheme"
    :theme-overrides="naiveThemeOverrides"
    :locale="naiveLocale"
    :date-locale="naiveDateLocale"
    :inline-theme-disabled="false"
  >
    <n-message-provider>
      <RouterView />
      <!-- Global language picker (top-right, visible on every page incl. login). -->
      <LocaleSwitcher />
      <!-- Global singletons for useToast / useConfirm composables (Story 1.7+). -->
      <ToastHost />
      <ConfirmDialog />
    </n-message-provider>
  </n-config-provider>
</template>
