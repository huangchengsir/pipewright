import { chromium } from '@playwright/test'

const BASE = 'http://localhost:8088'
const PW = 'pipewright888'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

const browser = await chromium.launch({ headless: false, slowMo: 700 })
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })

async function step(label, fn) {
  console.log('▶ ' + label)
  try { await fn() } catch (e) { console.log('  (跳过:' + e.message.split('\n')[0] + ')') }
  await sleep(1200)
}

// 1. 登录
await step('打开登录页 + 登录', async () => {
  await page.goto(BASE + '/login')
  await page.locator('#username').fill('admin')
  await page.locator('#password').fill(PW)
  await page.getByRole('button', { name: '登录' }).click()
  await page.waitForURL((u) => !u.pathname.includes('/login'), { timeout: 15000 })
})

// 2. 项目页
await step('看项目页(真后端数据)', async () => {
  await page.goto(BASE + '/projects')
  await page.getByText('门户站 portal').first().waitFor({ timeout: 10000 })
})

// 3. 运行列表
await step('看运行列表(成功 + 失败)', async () => {
  await page.goto(BASE + '/runs')
  await page.getByText('失败').first().waitFor({ timeout: 10000 })
})

// 4. 失败 run → AI 诊断(真 DeepSeek)
await step('进失败运行详情', async () => {
  await page.goto(BASE + '/runs/demo-failrun')
  await sleep(1500)
})
await step('点「分析失败原因」→ 真 DeepSeek 诊断', async () => {
  const btn = page.getByRole('button', { name: /分析失败原因|重新诊断/ })
  await btn.waitFor({ timeout: 8000 })
  await btn.click()
  // 等真 DeepSeek 返回(ready「AI 认为」或 unavailable)
  const ai = page.getByRole('region', { name: 'AI 认为' })
  const un = page.getByText('诊断不可用')
  await ai.or(un).first().waitFor({ timeout: 90000 })
})
await sleep(3000)

// 5. 设置:凭据(掩码)+ AI 配置
await step('设置 → 凭据保险库(掩码,无明文)', async () => {
  await page.goto(BASE + '/settings/vault')
  await sleep(1500)
})
await step('设置 → AI 配置(DeepSeek)', async () => {
  await page.goto(BASE + '/settings/ai')
  await sleep(1500)
})

console.log('✅ 有头演示走完;窗口保持打开,你可以接着自己点。')
// 保持窗口打开 20 分钟供观看/交互
await sleep(20 * 60 * 1000)
await browser.close()
