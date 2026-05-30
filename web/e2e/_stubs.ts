import type { Page, Route } from '@playwright/test'

/**
 * Shared API stubs for smoke e2e — keeps flows offline & deterministic.
 * Each helper installs page.route handlers that fulfill the frozen API shapes.
 */

function json(route: Route, status: number, body: unknown) {
  return route.fulfill({
    status,
    contentType: 'application/json',
    body: JSON.stringify(body),
  })
}

/**
 * Only intercept real backend calls (URL path begins with `/api/`).
 * A glob like `**​/api/**` would wrongly match Vite source modules such as
 * `/src/api/http.ts` and break module loading — so we match on the parsed path.
 */
function isApiPath(url: URL, suffix?: string): boolean {
  if (!url.pathname.startsWith('/api/')) return false
  return suffix ? url.pathname === suffix : true
}

/** Treat the user as NOT logged in: session → 401, so guards bounce to /login. */
export async function stubLoggedOut(page: Page): Promise<void> {
  await page.route(
    (u) => isApiPath(u, '/api/auth/session'),
    (route) => json(route, 401, { error: { code: 'unauthorized', message: '未登录' } }),
  )
}

/** Treat the user as logged in, with an empty-but-valid backend. */
export async function stubLoggedIn(page: Page, username = 'admin'): Promise<void> {
  // Most-recently-registered route wins → broad catch-all FIRST, specifics AFTER.
  await page.route(
    (u) => isApiPath(u),
    (route) => {
      if (route.request().method() === 'GET') return json(route, 200, [])
      return json(route, 204, {})
    },
  )
  await page.route(
    (u) => isApiPath(u, '/api/projects'),
    (route) => json(route, 200, []),
  )
  await page.route(
    (u) => isApiPath(u, '/api/auth/session'),
    (route) => json(route, 200, { username }),
  )
  await page.route(
    (u) => isApiPath(u, '/api/auth/login'),
    (route) => json(route, 200, { username }),
  )
}
