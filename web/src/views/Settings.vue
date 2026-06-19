<script setup lang="ts">
// Settings shell with secondary nav — business logic in Story 1.6+
import { useI18n } from 'vue-i18n'

const { t } = useI18n()
</script>

<template>
  <div class="settings-view">
    <header class="view-header">
      <h1 class="view-title">{{ t('settingsHub.title') }}</h1>
      <p class="view-sub">{{ t('settingsHub.subtitle') }}</p>
    </header>

    <!-- Secondary nav -->
    <nav class="settings-nav" :aria-label="t('settingsHub.navAria')">
      <router-link to="/settings/ai" class="settings-nav-item">{{ t('settingsHub.navAi') }}</router-link>
      <router-link to="/settings/oauth" class="settings-nav-item">{{ t('settingsHub.navOauth') }}</router-link>
      <router-link to="/settings/notifications" class="settings-nav-item">{{ t('settingsHub.navNotifications') }}</router-link>
      <router-link to="/settings/vault" class="settings-nav-item">{{ t('settingsHub.navVault') }}</router-link>
      <router-link to="/settings/dns-providers" class="settings-nav-item">{{ t('settingsHub.navDnsProviders') }}</router-link>
      <router-link to="/settings/account" class="settings-nav-item">{{ t('settingsHub.navAccount') }}</router-link>
      <router-link to="/settings/servers" class="settings-nav-item">{{ t('settingsHub.navServers') }}</router-link>
      <router-link to="/settings/diagnosis-stats" class="settings-nav-item">{{ t('settingsHub.navDiagnosisStats') }}</router-link>
      <router-link to="/settings/system" class="settings-nav-item">{{ t('settingsHub.navSystem') }}</router-link>
    </nav>

    <div class="settings-content">
      <router-view />
    </div>
  </div>
</template>

<style scoped>
.settings-view {
  display: flex;
  flex-direction: column;
  gap: 0;
}

.view-header {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding-bottom: 20px;
  border-bottom: 1px solid var(--color-border);
  margin-bottom: 0;
}

.view-title {
  font-size: var(--text-display);
  font-weight: 700;
  letter-spacing: -0.02em;
  color: var(--color-text);
}

.view-sub {
  font-size: var(--text-body);
  color: var(--color-faint);
  margin-top: 2px;
}

/* Settings secondary nav — tab-style with primary underline */
.settings-nav {
  display: flex;
  gap: 0;
  border-bottom: 1px solid var(--color-border);
  margin-bottom: 24px;
}

.settings-nav-item {
  padding: 12px 18px;
  font-size: var(--text-label);
  font-weight: 500;
  color: var(--color-faint);
  text-decoration: none;
  border-bottom: 2px solid transparent;
  margin-bottom: -1px;
  transition:
    color var(--duration-fast),
    border-color var(--duration-fast);
}

.settings-nav-item:hover {
  color: var(--color-dim);
}

.settings-nav-item.router-link-active {
  color: var(--color-text);
  font-weight: 600;
  border-bottom-color: var(--color-primary);
}

.settings-content {
  flex: 1;
}
</style>
