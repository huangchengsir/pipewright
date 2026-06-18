import { createRouter, createWebHistory } from 'vue-router'
import { useSessionStore } from '../stores/session'

// Lazy-loaded views
const Login       = () => import('../views/Login.vue')
const AppShell    = () => import('../layouts/AppShell.vue')
// Story 1-7: first-run onboarding guide
const Onboarding  = () => import('../views/Onboarding.vue')
const Dashboard   = () => import('../views/Dashboard.vue')
const Projects    = () => import('../views/Projects.vue')
const Runs        = () => import('../views/Runs.vue')
// FR-8-13: 复用库(流水线模板 + 变量组)
const Library     = () => import('../views/Library.vue')
// FR-8-15: DORA 四指标仪表盘(部署频率 / 前置时长 / 变更失败率 / MTTR;只读聚合)
const DoraDashboard = () => import('../views/DoraDashboard.vue')
// 环境一等公民:按环境聚合部署历史 + 一键回滚(对标 GitLab environments;只读聚合 + 复用部署链路回滚)
const Environments = () => import('../views/Environments.vue')
// Story 6-1: multi-host status overview (server-layer CPU/memory/disk metrics, FR-15)
const ServerStatus = () => import('../views/ServerStatus.vue')
// 容器管理:跨所有已登记服务器聚合容器(docker ps over SSH)+ 行内生命周期操作
const Containers = () => import('../views/Containers.vue')
// R2 / E2.4: 证书总览大盘(跨主机跨域名一张表 + 到期高亮;只读聚合)
const ProxyOverview = () => import('../views/ProxyOverview.vue')
// R4 / E4.1: PR 预览环境大盘(临时环境列表 + 手动回收;对标 Vercel preview deployments)
const Previews = () => import('../views/Previews.vue')
// Story 6-5: configurable anomaly detection & alerts (FR-23)
const AnomalyDetection = () => import('../views/AnomalyDetection.vue')
const Settings    = () => import('../views/Settings.vue')
const SettingsAI  = () => import('../views/settings/SettingsAI.vue')
// OAuth app config: per-provider Client ID/Secret for one-click "连接" in the vault
const SettingsOAuth = () => import('../views/settings/SettingsOAuth.vue')
const SettingsVault = () => import('../views/settings/SettingsVault.vue')
// R3 / E3.1: DNS providers (Cloudflare / DNSPod / 阿里云 DNS) — unlocks DNS-01 wildcard + instant subdomains
const SettingsDnsProviders = () => import('../views/settings/SettingsDnsProviders.vue')
const SettingsAccount = () => import('../views/settings/SettingsAccount.vue')
// Story 4-1: target server registry + shared SSH layer (FR-14)
const SettingsServers = () => import('../views/settings/SettingsServers.vue')
// Story 5-1: notification channels (FR-19)
const SettingsNotifications = () => import('../views/settings/SettingsNotifications.vue')
// Story 7-5: diagnosis feedback-loop stats (FR-26)
const SettingsDiagnosisStats = () => import('../views/settings/SettingsDiagnosisStats.vue')
// 系统信息 + 一键检查更新
const SettingsSystem = () => import('../views/settings/SettingsSystem.vue')
// Story 2-2: new pipeline editor
const ProjectPipeline = () => import('../views/ProjectPipeline.vue')
// Story 2-3: triggers (kept for backward compat; now a thin wrapper around TriggersPanel)
const ProjectTriggers = () => import('../views/ProjectTriggers.vue')
// Story 7-4: read-only code browsing (FR-4) — Monaco dynamic-imported, off the main bundle
const ProjectCode = () => import('../views/ProjectCode.vue')
const RunDetail = () => import('../views/RunDetail.vue')
// Story 1-6: living styleguide (public — no auth required for dev browsing)
const StatesShowcase = () => import('../views/StatesShowcase.vue')
// 自定义节点工作室:路由级聚焦低代码编辑页(shell 外全屏,进出经路由)
const CustomNodeStudioPage = () => import('../views/CustomNodeStudioPage.vue')
// AI 运维终端:独立全屏页(左终端 / 右 AI 助手),shell 外但需鉴权(对标阿里云 Cloud Shell)
const ServerTerminal = () => import('../views/ServerTerminal.vue')

const router = createRouter({
  history: createWebHistory(),
  routes: [
    // ——— Shell-outside: Login (no rail) ———
    {
      path: '/login',
      name: 'login',
      component: Login,
      meta: { public: true, title: '登录' },
    },
    // ——— Story 1-6: Component library living styleguide (shell-free, public) ———
    {
      path: '/states',
      name: 'states-showcase',
      component: StatesShowcase,
      meta: { public: true },
    },
    // ——— 自定义节点工作室:聚焦全屏编辑器(shell 外,但需鉴权)———
    {
      path: '/library/studio',
      name: 'studio-create',
      component: CustomNodeStudioPage,
      meta: { requiresAuth: true, title: '节点工作室' },
    },
    {
      path: '/library/studio/:id',
      name: 'studio-edit',
      component: CustomNodeStudioPage,
      meta: { requiresAuth: true, title: '节点工作室' },
    },
    // ——— AI 运维终端:独立全屏页(query: ?container=&shell=);shell 外,需鉴权 ———
    {
      path: '/servers/:id/terminal',
      name: 'server-terminal',
      component: ServerTerminal,
      meta: { requiresAuth: true, title: '运维终端' },
    },
    // ——— Shell-inside: authenticated routes ———
    {
      path: '/',
      component: AppShell,
      meta: { requiresAuth: true },
      children: [
        // 首页:概览仪表盘尚未建,暂重定向到项目页(避免落到占位页)。
        { path: '', name: 'overview', redirect: { name: 'dashboard' } },
        { path: 'dashboard', name: 'dashboard', component: Dashboard, meta: { title: '概览' } },
        // Story 1-7: first-run onboarding guide (inside shell, auth-required)
        { path: 'onboarding', name: 'onboarding', component: Onboarding, meta: { title: '快速上手' } },
        { path: 'projects', name: 'projects', component: Projects, meta: { title: '项目' } },
        // Story 2-2: 4-tab pipeline editor (primary config entry point)
        { path: 'projects/:id/pipeline', name: 'project-pipeline', component: ProjectPipeline, meta: { title: '流水线' } },
        // Story 2-3: backward-compat standalone triggers page
        { path: 'projects/:id/triggers', name: 'project-triggers', component: ProjectTriggers, meta: { title: '触发器' } },
        // Story 7-4: read-only code browsing (FR-4)
        { path: 'projects/:id/code', name: 'project-code', component: ProjectCode, meta: { title: '代码' } },
        { path: 'runs', name: 'runs', component: Runs, meta: { title: '运行' } },
        // FR-8-13: 复用库(流水线模板 + 变量组)
        { path: 'library', name: 'library', component: Library, meta: { title: '复用库' } },
        { path: 'runs/:id', name: 'run-detail', component: RunDetail, meta: { title: '运行详情' } },
        // FR-8-15: DORA 指标仪表盘(只读聚合;projectId / window 经 query 即状态)
        { path: 'metrics/dora', name: 'dora', component: DoraDashboard, meta: { title: 'DORA 指标' } },

        // 环境一等公民:按环境聚合部署历史 + 一键回滚。projectId 落 query(URL 即状态)。
        { path: 'environments', name: 'environments', component: Environments, meta: { title: '环境部署历史' } },
        // 顶层「服务器」占位页 → 重定向到真实的多机状态页(登记在 /settings/servers)。
        { path: 'servers', name: 'servers', redirect: { name: 'server-status' } },
        // Story 6-1: multi-host status overview (server-layer metrics, FR-15)
        { path: 'server-status', name: 'server-status', component: ServerStatus, meta: { title: '服务器状态' } },

        { path: 'containers', name: 'containers', component: Containers, meta: { title: '容器' } },
        // R2 / E2.4: 证书总览大盘(跨主机跨域名 + 到期高亮)
        { path: 'proxy', name: 'proxy-overview', component: ProxyOverview, meta: { title: '证书总览' } },
        // R4 / E4.1: PR 预览环境大盘(临时环境列表 + 手动回收)
        { path: 'previews', name: 'previews', component: Previews, meta: { title: '预览环境' } },
        // Story 6-5: configurable anomaly detection & alerts (FR-23)
        { path: 'anomaly', name: 'anomaly', component: AnomalyDetection, meta: { title: '异常检测' } },
        // 顶层「通知」占位页 → 重定向到真实的通知配置页。
        { path: 'notifications', name: 'notifications', redirect: { name: 'settings-notifications' } },
        {
          path: 'settings',
          name: 'settings',
          component: Settings,
          meta: { title: '设置' },
          children: [
            { path: '', redirect: { name: 'settings-ai' } },
            { path: 'ai', name: 'settings-ai', component: SettingsAI, meta: { title: 'AI 设置' } },
            { path: 'oauth', name: 'settings-oauth', component: SettingsOAuth, meta: { title: 'OAuth 设置' } },
            { path: 'notifications', name: 'settings-notifications', component: SettingsNotifications, meta: { title: '通知设置' } },
            { path: 'vault', name: 'settings-vault', component: SettingsVault, meta: { title: '凭据保险库' } },
            // R3 / E3.1: DNS 提供商(DNS-01 通配符 + 一键分配子域名)
            { path: 'dns-providers', name: 'settings-dns-providers', component: SettingsDnsProviders, meta: { title: 'DNS 提供商' } },
            { path: 'account', name: 'settings-account', component: SettingsAccount, meta: { title: '账户设置' } },
            // 系统信息 + 一键检查更新
            { path: 'system', name: 'settings-system', component: SettingsSystem, meta: { title: '系统信息' } },
            // Story 4-1: target servers + shared SSH layer (FR-14)
            { path: 'servers', name: 'settings-servers', component: SettingsServers, meta: { title: '服务器' } },
            // Story 7-5: diagnosis feedback-loop stats (FR-26)
            { path: 'diagnosis-stats', name: 'settings-diagnosis-stats', component: SettingsDiagnosisStats, meta: { title: '诊断统计' } },
          ],
        },
      ],
    },
    // Fallback
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
})

// ——— Route guard: session check with Pinia cache ———
//
// Error semantics:
//   kind:'ok'            → proceed
//   kind:'unauthenticated' → redirect to /login (confirmed 401)
//   kind:'error'          → backend unreachable / 5xx:
//       • if user already had a cached session → stay on current page
//         (do NOT kick them to /login; the page should show a fault state)
//       • if session was never established → redirect to /login
//         (we can't display authenticated content anyway)
router.beforeEach(async (to) => {
  if (to.meta.public) return true

  // useSessionStore() must be called inside the guard (after pinia is installed)
  const sessionStore = useSessionStore()
  const result = await sessionStore.ensureSession()

  if (result.kind === 'ok') return true

  if (result.kind === 'unauthenticated') {
    return {
      name: 'login',
      query: { redirect: to.fullPath },
    }
  }

  // kind === 'error' (5xx / network)
  if (sessionStore.user !== undefined && sessionStore.user !== null) {
    // Had a previously confirmed session — let the navigation proceed;
    // the view can show a degraded/retry state via isNetworkError.
    return true
  }

  // Session was never established — can't show authenticated content
  return {
    name: 'login',
    query: { redirect: to.fullPath },
  }
})

// ——— 标签页标题:每个路由独立 document.title,多标签(尤其终端在新标签开)可区分 ———
// 静态标题来自 route.meta.title;数据相关页面(终端带服务器名、运行详情带 ID 等)由各
// 组件加载到数据后自行覆写 document.title,这里只兜底,避免出现一堆同名「Pipewright」。
export const APP_TITLE = 'Pipewright'

export function setDocumentTitle(part?: string): void {
  document.title = part ? `${part} · ${APP_TITLE}` : APP_TITLE
}

router.afterEach((to) => {
  const title = to.meta.title
  setDocumentTitle(typeof title === 'string' ? title : undefined)
})

export default router
