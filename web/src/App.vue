<script setup lang="ts">
import { NConfigProvider, NMessageProvider } from 'naive-ui'
import { RouterView } from 'vue-router'
import { useNaiveTheme } from './composables/useNaiveTheme'

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
    </n-message-provider>
  </n-config-provider>
</template>
