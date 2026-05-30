import { defineConfig, devices } from '@playwright/test'

/**
 * Playwright e2e (smoke) config.
 *
 * Drives the Vite dev server (no real Go backend needed): every spec stubs
 * `**​/api/**` with page.route, so flows are deterministic and offline.
 * Backend integration e2e lives elsewhere (whole-system run), not here.
 *
 * Run: npm run e2e   (browsers must be installed: npx playwright install chromium)
 */
const PORT = 5174
const BASE_URL = `http://localhost:${PORT}`

export default defineConfig({
  testDir: './e2e',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? 'github' : 'list',
  use: {
    baseURL: BASE_URL,
    trace: 'on-first-retry',
  },
  projects: [
    { name: 'chromium', use: { ...devices['Desktop Chrome'] } },
  ],
  webServer: {
    // Strict port so the baseURL is predictable; dev server is enough for smoke.
    command: `npm run dev -- --port ${PORT} --strictPort`,
    url: BASE_URL,
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
  },
})
