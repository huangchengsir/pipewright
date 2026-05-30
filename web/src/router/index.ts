import { createRouter, createWebHistory } from 'vue-router'
import { useSessionStore } from '../stores/session'

// Lazy-loaded views
const Login       = () => import('../views/Login.vue')
const AppShell    = () => import('../layouts/AppShell.vue')
const Overview    = () => import('../views/Overview.vue')
// Story 1-7: first-run onboarding guide
const Onboarding  = () => import('../views/Onboarding.vue')
const Projects    = () => import('../views/Projects.vue')
const Runs        = () => import('../views/Runs.vue')
const Servers     = () => import('../views/Servers.vue')
// Story 6-1: multi-host status overview (server-layer CPU/memory/disk metrics, FR-15)
const ServerStatus = () => import('../views/ServerStatus.vue')
const Notifications = () => import('../views/Notifications.vue')
const Settings    = () => import('../views/Settings.vue')
const SettingsAI  = () => import('../views/settings/SettingsAI.vue')
const SettingsVault = () => import('../views/settings/SettingsVault.vue')
const SettingsAccount = () => import('../views/settings/SettingsAccount.vue')
const SettingsSystem = () => import('../views/settings/SettingsSystem.vue')
// Story 4-1: target server registry + shared SSH layer (FR-14)
const SettingsServers = () => import('../views/settings/SettingsServers.vue')
// Story 5-1: notification channels (FR-19)
const SettingsNotifications = () => import('../views/settings/SettingsNotifications.vue')
// Story 2-2: new pipeline editor
const ProjectPipeline = () => import('../views/ProjectPipeline.vue')
// Story 2-3: triggers (kept for backward compat; now a thin wrapper around TriggersPanel)
const ProjectTriggers = () => import('../views/ProjectTriggers.vue')
// Story 7-4: read-only code browsing (FR-4) — Monaco dynamic-imported, off the main bundle
const ProjectCode = () => import('../views/ProjectCode.vue')
const RunDetail = () => import('../views/RunDetail.vue')
// Story 1-6: living styleguide (public — no auth required for dev browsing)
const StatesShowcase = () => import('../views/StatesShowcase.vue')

const router = createRouter({
  history: createWebHistory(),
  routes: [
    // ——— Shell-outside: Login (no rail) ———
    {
      path: '/login',
      name: 'login',
      component: Login,
      meta: { public: true },
    },
    // ——— Story 1-6: Component library living styleguide (shell-free, public) ———
    {
      path: '/states',
      name: 'states-showcase',
      component: StatesShowcase,
      meta: { public: true },
    },
    // ——— Shell-inside: authenticated routes ———
    {
      path: '/',
      component: AppShell,
      meta: { requiresAuth: true },
      children: [
        { path: '', name: 'overview', component: Overview },
        // Story 1-7: first-run onboarding guide (inside shell, auth-required)
        { path: 'onboarding', name: 'onboarding', component: Onboarding },
        { path: 'projects', name: 'projects', component: Projects },
        // Story 2-2: 4-tab pipeline editor (primary config entry point)
        { path: 'projects/:id/pipeline', name: 'project-pipeline', component: ProjectPipeline },
        // Story 2-3: backward-compat standalone triggers page
        { path: 'projects/:id/triggers', name: 'project-triggers', component: ProjectTriggers },
        // Story 7-4: read-only code browsing (FR-4)
        { path: 'projects/:id/code', name: 'project-code', component: ProjectCode },
        { path: 'runs', name: 'runs', component: Runs },
        { path: 'runs/:id', name: 'run-detail', component: RunDetail },
        { path: 'servers', name: 'servers', component: Servers },
        // Story 6-1: multi-host status overview (server-layer metrics, FR-15)
        { path: 'server-status', name: 'server-status', component: ServerStatus },
        { path: 'notifications', name: 'notifications', component: Notifications },
        {
          path: 'settings',
          name: 'settings',
          component: Settings,
          children: [
            { path: '', redirect: { name: 'settings-ai' } },
            { path: 'ai', name: 'settings-ai', component: SettingsAI },
            { path: 'notifications', name: 'settings-notifications', component: SettingsNotifications },
            { path: 'vault', name: 'settings-vault', component: SettingsVault },
            { path: 'account', name: 'settings-account', component: SettingsAccount },
            { path: 'system', name: 'settings-system', component: SettingsSystem },
            // Story 4-1: target servers + shared SSH layer (FR-14)
            { path: 'servers', name: 'settings-servers', component: SettingsServers },
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

export default router
