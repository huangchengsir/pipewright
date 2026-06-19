<script setup lang="ts">
import type { Component } from 'vue'
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import {
  Dashboard,
  GitBranch,
  GitFork,
  ChartBar,
  Server,
  Box,
  Certificate,
  AlertTriangle,
  Bell,
  Settings,
  Stack2,
  Rocket,
  Browser,
  ChevronRight,
  Logout,
} from '@vicons/tabler'
import { NIcon } from 'naive-ui'
import ThemeToggle from '../components/ThemeToggle.vue'
import { logout } from '../api/auth'
import { useSessionStore } from '../stores/session'
import { useConfirm } from '../composables/useConfirm'
import { useToast } from '../composables/useToast'

const route = useRoute()
const router = useRouter()
const sessionStore = useSessionStore()
const confirm = useConfirm()
const toast = useToast()
const { t } = useI18n()

// 退出登录:确认 → POST /api/auth/logout(吊销当前会话)→ 清本地会话缓存 → 回登录页。
// 即便后端请求失败也照样清缓存跳转(本地一定登出),避免卡在"看似已登录但实际无效"。
const loggingOut = ref(false)
async function handleLogout(): Promise<void> {
  if (loggingOut.value) return
  const ok = await confirm.open({
    title: t('shell.logoutTitle'),
    body: t('shell.logoutBody'),
    confirmLabel: t('nav.logout'),
    variant: 'danger',
  })
  if (!ok) return
  loggingOut.value = true
  try {
    await logout()
  } catch {
    // 后端不可达也要本地登出 —— 失败不阻塞跳转。
    toast.info(t('shell.logoutLocalTitle'), { detail: t('shell.logoutLocalDetail') })
  } finally {
    sessionStore.clearSession()
    loggingOut.value = false
    void router.push('/login')
  }
}

interface NavItem {
  name: string
  to: string
  icon: Component
  /** i18n key under `nav.*` for the visible label. */
  labelKey: string
  /** i18n key under `nav.*` for the accessible name. */
  ariaKey: string
}

const navItems: NavItem[] = [
  { name: 'dashboard',     to: '/dashboard',     icon: Dashboard, labelKey: 'nav.dashboard',     ariaKey: 'nav.dashboard' },
  { name: 'projects',      to: '/projects',      icon: GitBranch,  labelKey: 'nav.projects',      ariaKey: 'nav.projects' },
  { name: 'runs',          to: '/runs',          icon: GitFork,    labelKey: 'nav.runs',          ariaKey: 'nav.runs' },
  // FR-8-13: 复用库(流水线模板 + 变量组)。
  { name: 'library',       to: '/library',       icon: Stack2,     labelKey: 'nav.library',       ariaKey: 'nav.library' },
  // 环境一等公民:按环境聚合的部署历史 + 一键回滚(对标 GitLab environments)。
  { name: 'environments',  to: '/environments',  icon: Rocket,     labelKey: 'nav.environments',  ariaKey: 'nav.ariaEnvironments' },
  // FR-8-15: DORA 四指标仪表盘(交付效能;只读聚合)。
  { name: 'dora',          to: '/metrics/dora',  icon: ChartBar,   labelKey: 'nav.dora',          ariaKey: 'nav.dora' },
  // Story 6-1: 多机状态总览(服务器层指标 FR-15);登记在 设置 → 服务器。
  { name: 'server-status', to: '/server-status', icon: Server,     labelKey: 'nav.serverStatus',  ariaKey: 'nav.serverStatus' },
  // 容器管理:跨服务器聚合容器总览 + 行内生命周期操作(docker over SSH)。
  { name: 'containers',    to: '/containers',    icon: Box,        labelKey: 'nav.containers',    ariaKey: 'nav.containers' },
  // R2 / E2.4: 证书总览大盘(跨主机跨域名一张表 + 到期高亮)。
  { name: 'proxy-overview', to: '/proxy',        icon: Certificate, labelKey: 'nav.proxyOverview', ariaKey: 'nav.proxyOverview' },
  // R4 / E4.1: PR 预览环境大盘(临时环境列表 + 手动回收)。
  { name: 'previews',      to: '/previews',      icon: Browser,    labelKey: 'nav.previews',      ariaKey: 'nav.previews' },
  // Story 6-5: configurable anomaly detection & alerts (FR-23)
  { name: 'anomaly',       to: '/anomaly',       icon: AlertTriangle, labelKey: 'nav.anomaly',    ariaKey: 'nav.anomaly' },
  { name: 'notifications', to: '/settings/notifications', icon: Bell, labelKey: 'nav.notifications', ariaKey: 'nav.notifications' },
]

const settingsItem: NavItem = {
  name: 'settings',
  to: '/settings',
  icon: Settings,
  labelKey: 'nav.settings',
  ariaKey: 'nav.settings',
}

function isActive(item: NavItem): boolean {
  if (item.name === 'overview') {
    return route.name === 'overview'
  }
  return route.path.startsWith(item.to)
}

// 侧栏展开/收起:固定按钮切换,状态持久化到 localStorage(刷新/重开保持)。
const STORAGE_KEY = 'pipewright_sidebar_expanded'
const expanded = ref(localStorage.getItem(STORAGE_KEY) === '1')
function toggleExpanded(): void {
  expanded.value = !expanded.value
  localStorage.setItem(STORAGE_KEY, expanded.value ? '1' : '0')
}
</script>

<template>
  <div class="app-shell" :class="{ 'is-expanded': expanded }">
    <!-- Left rail navigation -->
    <nav class="rail" :aria-label="t('nav.ariaMain')">
      <!-- 顶部:品牌 -->
      <div class="rail-head">
        <router-link to="/" class="brand" :aria-label="t('nav.ariaBrandHome')">
          <span class="brand-mark mono">p&gt;</span>
          <span class="brand-name">Pipewright</span>
        </router-link>
      </div>

      <!-- Primary navigation items -->
      <ul class="nav-list" role="list">
        <li v-for="item in navItems" :key="item.name">
          <router-link
            :to="item.to"
            class="nav-item"
            :class="{ 'nav-item--active': isActive(item) }"
            :aria-label="t(item.ariaKey)"
            :aria-current="isActive(item) ? 'page' : undefined"
          >
            <n-icon class="nav-icon" :component="item.icon" :size="20" />
            <span class="nav-label">{{ t(item.labelKey) }}</span>
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
        :aria-label="t(settingsItem.ariaKey)"
        :aria-current="isActive(settingsItem) ? 'page' : undefined"
      >
        <n-icon class="nav-icon" :component="settingsItem.icon" :size="20" />
        <span class="nav-label">{{ t(settingsItem.labelKey) }}</span>
      </router-link>

      <!-- Logout (bottom-most) -->
      <button
        type="button"
        class="nav-item nav-item--logout"
        :disabled="loggingOut"
        :aria-label="t('nav.logout')"
        @click="handleLogout"
      >
        <n-icon class="nav-icon" :component="Logout" :size="20" />
        <span class="nav-label">{{ t('nav.logout') }}</span>
      </button>
    </nav>

    <!-- 边缘切换:骑在侧栏右缘、与品牌齐平的圆形按钮(随 --rail-width 平移)。 -->
    <button
      class="rail-edge-toggle"
      type="button"
      :aria-label="expanded ? t('nav.collapse') : t('nav.expand')"
      :aria-pressed="expanded"
      @click="toggleExpanded"
    >
      <n-icon class="toggle-chevron" :class="{ flipped: expanded }" :component="ChevronRight" :size="16" />
    </button>

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
  /* minmax(0,1fr) 而非 1fr:1fr 的最小尺寸是 min-content,会被内部超宽元素(如终端里不换行的
     超长日志行)撑到比窗口还宽 → 整页横向溢出、卡片顶出右边。minmax(0,…) 允许主区收缩到窗口宽度,
     超长内容只在各自容器内(终端横向滚动)处理,布局自适应窗口。 */
  grid-template-columns: var(--rail-width) minmax(0, 1fr);
  min-height: 100vh;
  position: relative; /* 作为边缘切换按钮的定位上下文 */
  transition: grid-template-columns var(--duration-normal) var(--ease-out-expo, ease);
}
/* 展开态:覆盖 --rail-width,grid 主区与 active 指示条偏移随之自适应。 */
.app-shell.is-expanded {
  --rail-width: 212px;
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
  z-index: 10;
  overflow: hidden;
}
.is-expanded .rail {
  align-items: stretch;
  padding: 20px 12px;
}

/* 顶部头区:品牌 + 切换。收起态纵向叠放(logo 上、箭头下);展开态横向一行(品牌左、箭头右)。 */
.rail-head {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
  margin-bottom: 18px;
  flex-shrink: 0;
}
.is-expanded .rail-head {
  flex-direction: row;
  align-items: center;
  gap: 8px;
  padding-left: 4px;
}

/* Brand */
.brand {
  display: flex;
  align-items: center;
  gap: 10px;
  text-decoration: none;
  flex-shrink: 0;
}
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
  box-shadow: 0 4px 14px var(--color-primary-soft);
  transition: box-shadow var(--duration-fast);
  flex-shrink: 0;
}
.brand:hover .brand-mark {
  box-shadow: 0 6px 20px var(--color-primary-soft);
}
.brand-name {
  font-size: var(--text-body);
  font-weight: 700;
  letter-spacing: -0.01em;
  color: var(--color-text);
  white-space: nowrap;
  overflow: hidden;
  /* 收起时隐藏文字(无障碍名仍由 brand 的 aria-label 提供)。 */
  display: none;
}
.is-expanded .brand-name {
  display: inline;
}

/* Nav list */
.nav-list {
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 4px;
  width: 100%;
  align-items: center;
  /* 主菜单占据 head 与底部固定项之间的弹性空间;菜单项过多时自身滚动,
     绝不挤压(或被 .rail overflow:hidden 裁掉)底部的「设置 / 退出登录」。
     min-height:0 让 flex 子项能收缩到内容高度以下,overflow 才会生效。 */
  flex: 1 1 auto;
  min-height: 0;
  overflow-y: auto;
  overflow-x: hidden;
  scrollbar-width: thin;
}
.is-expanded .nav-list {
  align-items: stretch;
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
  background: none;
  border: none;
  cursor: pointer;
  transition:
    color var(--duration-fast),
    background-color var(--duration-fast);
}
.is-expanded .nav-item {
  width: 100%;
  display: flex;
  align-items: center;
  justify-content: flex-start;
  gap: 12px;
  padding: 0 12px;
}
.nav-icon {
  flex-shrink: 0;
}
.nav-item:hover {
  color: var(--color-text);
  background-color: var(--color-border);
}
.nav-item:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}

/* 退出登录:button 而非 link,补 font 继承;hover 转危险色与设置项区分。 */
.nav-item--logout {
  font: inherit;
}
.nav-item--logout:hover {
  color: var(--color-red);
  background-color: var(--color-red-soft);
}
.nav-item--logout:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.nav-label {
  font-size: var(--text-label);
  font-weight: 500;
  white-space: nowrap;
  overflow: hidden;
  display: none;
}
.is-expanded .nav-label {
  display: inline;
}

/* Active state */
.nav-item--active {
  color: var(--color-primary);
}
.is-expanded .nav-item--active {
  background: var(--color-primary-soft);
  font-weight: 600;
}
.nav-item--active::before {
  content: "";
  position: absolute;
  /* 收起态:指示条贴在 rail 左缘(图标水平居中,据 rail 宽度回推偏移)。 */
  left: calc(-1 * (var(--rail-width) / 2 - 20px + 1px));
  top: 9px;
  bottom: 9px;
  width: 2.5px;
  border-radius: 2px;
  background: var(--color-primary);
}
/* 展开态:指示条贴在条目自身左缘(条目左对齐、占满宽度)。 */
.is-expanded .nav-item--active::before {
  left: 0;
  top: 6px;
  bottom: 6px;
}

/* 边缘切换:骑在侧栏右缘、与品牌行齐平的圆形按钮。随 --rail-width 平移(展开/收起都跟手)。 */
.rail-edge-toggle {
  position: absolute;
  top: 23px; /* 与顶部品牌 mark 垂直居中对齐 */
  left: calc(var(--rail-width) - 13px); /* 圆心落在 rail 右缘分割线上 */
  width: 26px;
  height: 26px;
  border-radius: 50%;
  display: grid;
  place-items: center;
  background: var(--color-card);
  border: 1px solid var(--color-border);
  color: var(--color-dim);
  cursor: pointer;
  z-index: 30;
  box-shadow: 0 2px 8px -2px rgba(0, 0, 0, 0.14);
  transition:
    left var(--duration-normal) var(--ease-out-expo, ease),
    color var(--duration-fast),
    border-color var(--duration-fast),
    box-shadow var(--duration-fast);
}
.rail-edge-toggle:hover {
  color: var(--color-primary);
  border-color: var(--color-primary);
  box-shadow: 0 4px 12px -2px var(--color-primary-soft);
}
.rail-edge-toggle:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}
.toggle-chevron {
  transition: transform var(--duration-normal) var(--ease-out-expo, ease);
}
.toggle-chevron.flipped {
  transform: rotate(180deg);
}

.rail-spacer {
  /* nav-list 现已 flex:1 占满中间并把底部项推到底;spacer 不再伸展
     (否则会与 nav-list 平分空间、把菜单区压到一半)。保留为零高占位。 */
  flex: 0 0 0;
}

/* ——— Main area ——— */
.main-area {
  min-height: 100vh;
  /* 用 clip 而非 hidden:hidden 会让 overflow-y 被规范强制计算成 auto,使 .main-area 变成一个
     「装不下却又无法滚动」的隐性滚动容器,劫持滚轮/scrollIntoView,导致长页面(如运行详情的
     日志终端)滑不到底。clip 只裁横向、不改纵向(保持 visible),整页交给 window 单一滚动。 */
  overflow-x: clip;
}

.main-inner {
  max-width: var(--content-max);
  margin: 0 auto;
  /* 顶部多留一条 ~38px 的带:全局语言切换器(固定右上角)占这一带,页面头部
     的右侧操作按钮(如概览的「新建项目」)下移到带下方,避免被切换器压住。 */
  padding: calc(var(--main-pad-top) + 38px) var(--main-pad) var(--main-pad-bottom);
  min-height: 100vh;
}
</style>
