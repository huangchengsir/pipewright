<script setup lang="ts">
import type { Component } from 'vue'
import { useRoute } from 'vue-router'
import {
  LayoutGrid,
  GitBranch,
  GitFork,
  Server,
  Activity,
  AlertTriangle,
  Bell,
  Settings,
} from '@vicons/tabler'
import { NIcon } from 'naive-ui'
import ThemeToggle from '../components/ThemeToggle.vue'

const route = useRoute()

interface NavItem {
  name: string
  to: string
  icon: Component
  label: string
  ariaLabel: string
}

const navItems: NavItem[] = [
  { name: 'overview',      to: '/',              icon: LayoutGrid, label: '概览',  ariaLabel: '概览' },
  { name: 'projects',      to: '/projects',      icon: GitBranch,  label: '项目',  ariaLabel: '项目' },
  { name: 'runs',          to: '/runs',          icon: GitFork,    label: '运行',  ariaLabel: '运行' },
  { name: 'servers',       to: '/servers',       icon: Server,     label: '服务器', ariaLabel: '服务器' },
  // Story 6-1: multi-host status overview (server-layer metrics, FR-15)
  { name: 'server-status', to: '/server-status', icon: Activity,   label: '服务器状态', ariaLabel: '服务器状态' },
  // Story 6-5: configurable anomaly detection & alerts (FR-23)
  { name: 'anomaly',       to: '/anomaly',       icon: AlertTriangle, label: '异常检测', ariaLabel: '异常检测' },
  { name: 'notifications', to: '/notifications', icon: Bell,       label: '通知',  ariaLabel: '通知' },
]

const settingsItem: NavItem = {
  name: 'settings',
  to: '/settings',
  icon: Settings,
  label: '设置',
  ariaLabel: '设置',
}

function isActive(item: NavItem): boolean {
  if (item.name === 'overview') {
    return route.name === 'overview'
  }
  return route.path.startsWith(item.to)
}
</script>

<template>
  <div class="app-shell">
    <!-- Left rail navigation -->
    <nav class="rail" aria-label="主导航">
      <!-- Brand mark -->
      <router-link to="/" class="brand-mark" aria-label="Pipewright 主页">
        <span class="mono">d&gt;</span>
      </router-link>

      <!-- Primary navigation items -->
      <ul class="nav-list" role="list">
        <li v-for="item in navItems" :key="item.name">
          <router-link
            :to="item.to"
            class="nav-item"
            :class="{ 'nav-item--active': isActive(item) }"
            :aria-label="item.ariaLabel"
            :aria-current="isActive(item) ? 'page' : undefined"
          >
            <n-icon :component="item.icon" :size="20" />
            <span class="sr-only">{{ item.label }}</span>
          </router-link>
        </li>
      </ul>

      <!-- Spacer -->
      <div class="rail-spacer" aria-hidden="true" />

      <!-- Settings (bottom) -->
      <router-link
        :to="settingsItem.to"
        class="nav-item"
        :class="{ 'nav-item--active': isActive(settingsItem) }"
        :aria-label="settingsItem.ariaLabel"
        :aria-current="isActive(settingsItem) ? 'page' : undefined"
      >
        <n-icon :component="settingsItem.icon" :size="20" />
        <span class="sr-only">{{ settingsItem.label }}</span>
      </router-link>
    </nav>

    <!-- Main content area -->
    <main class="main-area" id="main-content">
      <div class="main-inner">
        <router-view />
      </div>
    </main>

    <!-- Theme toggle (bottom-right, always visible) -->
    <ThemeToggle />
  </div>
</template>

<style scoped>
.app-shell {
  display: grid;
  grid-template-columns: var(--rail-width) 1fr;
  min-height: 100vh;
}

/* ——— Rail ——— */
.rail {
  position: sticky;
  top: 0;
  height: 100vh;
  display: flex;
  flex-direction: column;
  align-items: center;
  padding: 20px 0;
  gap: 6px;
  border-right: 1px solid var(--color-border);
  background-color: var(--color-bg);
  /* Slightly above bg to separate rail from content at a glance */
  z-index: 10;
  overflow: hidden;
}

/* Brand mark */
.brand-mark {
  width: 30px;
  height: 30px;
  border-radius: var(--rounded);
  background: var(--color-primary);
  color: #fff;
  display: grid;
  place-items: center;
  font-family: var(--font-mono);
  font-weight: 700;
  font-size: 0.78rem;
  margin-bottom: 18px;
  box-shadow: 0 4px 14px var(--color-primary-soft);
  text-decoration: none;
  transition: box-shadow var(--duration-fast);
  flex-shrink: 0;
}

.brand-mark:hover {
  box-shadow: 0 6px 20px var(--color-primary-soft);
}

/* Nav list */
.nav-list {
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 4px;
  width: 100%;
  align-items: center;
}

/* Nav item */
.nav-item {
  position: relative;
  width: 40px;
  height: 40px;
  border-radius: var(--rounded);
  display: grid;
  place-items: center;
  color: var(--color-faint);
  text-decoration: none;
  transition:
    color var(--duration-fast),
    background-color var(--duration-fast);
  cursor: pointer;
}

.nav-item:hover {
  color: var(--color-text);
  background-color: var(--color-border);
}

.nav-item:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}

/* Active state: electric blue + 2.5px left bar */
.nav-item--active {
  color: var(--color-primary);
}

.nav-item--active::before {
  content: "";
  position: absolute;
  /* Extends to the left edge of the rail */
  left: calc(-1 * (var(--rail-width) / 2 - 20px + 1px));
  top: 9px;
  bottom: 9px;
  width: 2.5px;
  border-radius: 2px;
  background: var(--color-primary);
}

.rail-spacer {
  flex: 1;
}

/* ——— Main area ——— */
.main-area {
  min-height: 100vh;
  overflow-x: hidden;
}

.main-inner {
  max-width: var(--content-max);
  margin: 0 auto;
  padding: var(--main-pad-top) var(--main-pad) var(--main-pad-bottom);
  min-height: 100vh;
}
</style>
