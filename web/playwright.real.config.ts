import { defineConfig, devices } from '@playwright/test'

// playwright.real.config.ts —— **真全栈 e2e** 配置:对**真运行的 pipewright 二进制**(真后端 +
// go:embed 的真前端)跑,**不 mock 任何 /api**(与 playwright.config.ts 的 mock 版互补)。
// 二进制由 scripts/e2e/fullstack.sh 外部起停;此处只经 PW_BASE_URL 指向它,无 webServer。
//
// 跑法:由 scripts/e2e/fullstack.sh 编排(起二进制 → seed → 本配置跑 → 清理)。
const baseURL = process.env.PW_BASE_URL || 'http://127.0.0.1:18088'

export default defineConfig({
  testDir: './e2e-real',
  timeout: 90_000,
  expect: { timeout: 20_000 },
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: [['list']],
  use: {
    baseURL,
    headless: true,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
})
