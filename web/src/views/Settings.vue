<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import '../styles/settings-surface.css'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const sections = computed(() => [
  { path: '/settings/ai', label: t('settingsHub.navAi') },
  { path: '/settings/oauth', label: t('settingsHub.navOauth') },
  { path: '/settings/notifications', label: t('settingsHub.navNotifications') },
  { path: '/settings/vault', label: t('settingsHub.navVault') },
  { path: '/settings/dns-providers', label: t('settingsHub.navDnsProviders') },
  { path: '/settings/account', label: t('settingsHub.navAccount') },
  { path: '/settings/mcp', label: 'MCP' },
  { path: '/settings/servers', label: t('settingsHub.navServers') },
  { path: '/settings/diagnosis-stats', label: t('settingsHub.navDiagnosisStats') },
  { path: '/settings/system', label: t('settingsHub.navSystem') },
])
const selectedSection = computed(() => sections.value.find(section => router.resolve(section.path).matched.at(-1) === route.matched.at(-1))?.path ?? '/settings/ai')
function navigate(event: Event): void {
  const control = event.target as HTMLSelectElement
  const path = control.value
  const restoreSelection = () => { control.value = selectedSection.value }
  if (!sections.value.some(section => section.path === path) || path === route.path) {
    restoreSelection()
    return
  }
  // An existing page guard can reject navigation (for example, an unsaved draft).
  // Keep the native control aligned with the route that actually remains active.
  void router.push(path).then(restoreSelection, restoreSelection)
}
</script>

<template>
  <div class="settings-view">
    <header class="view-header">
      <h1 class="view-title">{{ t('settingsHub.title') }}</h1>
      <p class="view-sub">{{ t('settingsHub.subtitle') }}</p>
    </header>

    <select class="settings-nav-select" :aria-label="t('settingsHub.navAria')" :value="selectedSection" @change="navigate">
      <option v-for="section in sections" :key="section.path" :value="section.path">{{ section.label }}</option>
    </select>
    <nav class="settings-nav" :aria-label="t('settingsHub.navAria')">
      <router-link v-for="section in sections" :key="section.path" :to="section.path" class="settings-nav-item">{{ section.label }}</router-link>
    </nav>
    <div class="settings-content"><router-view /></div>
  </div>
</template>

<style scoped>
.settings-view { display: flex; flex-direction: column; min-width: 0; }
.view-header { display: flex; flex-direction: column; gap: 4px; padding-bottom: 18px; }
.view-title { font-size: 21px; font-weight: 650; letter-spacing: -0.02em; color: var(--color-text); }
.view-sub { font-size: 13px; color: var(--color-dim); line-height: 1.55; }
.settings-nav-select { width: 100%; min-height: 44px; font: 16px var(--font-sans); color: var(--color-text); background: var(--color-card); border: 1px solid var(--color-border-strong); border-radius: var(--rounded); padding: 8px 12px; margin-bottom: 20px; }
.settings-nav-select:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 2px; }
.settings-nav { display: none; }
.settings-content { flex: 1; min-width: 0; }
@media (min-width: 640px) {
  .settings-nav-select { display: none; }
  .settings-nav { display: flex; flex-wrap: wrap; gap: 0 4px; border-bottom: 1px solid var(--color-border); margin-bottom: 22px; }
  .settings-nav-item { padding: 11px 12px; font-size: 13px; font-weight: 500; color: var(--color-dim); text-decoration: none; border-bottom: 2px solid transparent; margin-bottom: -1px; transition: color var(--duration-fast), border-color var(--duration-fast); }
  .settings-nav-item:hover { color: var(--color-text); }
  .settings-nav-item.router-link-active { color: var(--color-text); font-weight: 600; border-bottom-color: var(--color-primary); }
  .settings-nav-item:focus-visible { outline: 2px solid var(--color-primary); outline-offset: -2px; }
}
</style>
