<script setup lang="ts">
// Settings shell with secondary nav — business logic in Story 1.6+
</script>

<template>
  <div class="settings-view">
    <header class="view-header">
      <h1 class="view-title">设置</h1>
      <p class="view-sub">系统配置与管理</p>
    </header>

    <!-- Secondary nav -->
    <nav class="settings-nav" aria-label="设置二级导航">
      <router-link to="/settings/ai" class="settings-nav-item">AI 提供商</router-link>
      <router-link to="/settings/oauth" class="settings-nav-item">OAuth 应用</router-link>
      <router-link to="/settings/notifications" class="settings-nav-item">通知</router-link>
      <router-link to="/settings/vault" class="settings-nav-item">凭据保险库</router-link>
      <router-link to="/settings/account" class="settings-nav-item">账户</router-link>
      <router-link to="/settings/servers" class="settings-nav-item">服务器</router-link>
      <router-link to="/settings/diagnosis-stats" class="settings-nav-item">诊断反馈</router-link>
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
