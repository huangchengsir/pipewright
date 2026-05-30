<script setup lang="ts">
import { NConfigProvider, NMessageProvider } from 'naive-ui'
import { RouterView } from 'vue-router'
import { useNaiveTheme } from './composables/useNaiveTheme'
import ConfirmDialog from './components/ui/ConfirmDialog.vue'
import ToastHost from './components/ui/ToastHost.vue'

const { naiveTheme, naiveThemeOverrides } = useNaiveTheme()
</script>

<template>
  <n-config-provider
    :theme="naiveTheme"
    :theme-overrides="naiveThemeOverrides"
    :inline-theme-disabled="false"
  >
    <n-message-provider>
      <RouterView />
      <!-- Global singletons for useToast / useConfirm composables (Story 1.7+). -->
      <ToastHost />
      <ConfirmDialog />
    </n-message-provider>
  </n-config-provider>
</template>
