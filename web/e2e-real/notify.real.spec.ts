import { test, expect, type Page } from '@playwright/test'

// notify.real.spec.ts —— 真全栈 e2e:通知 webhook 渠道走 UI。
// harness 起一个本地 HTTP 接收器(记录请求,绑 127.0.0.1:某端口),URL 经 PW_NOTIFY_URL 透传。
// 登录 → /settings/notifications → 新增 webhook 渠道指向接收器 → 列表出现 → 点「测试发送」
// → 真后端真打 POST 到接收器 → 断言 UI「发送成功」。打真二进制,不 mock。
// (webhook SSRF 收口放行回环/私网,故 127.0.0.1 接收器通过。)
//
// 无接收器环境(PW_NOTIFY 未置)→ 自动 skip。

const ADMIN_PW = process.env.PW_ADMIN_PW || 'fs-admin-pw'
const NOTIFY_ON = process.env.PW_NOTIFY === '1'
const NOTIFY_URL = process.env.PW_NOTIFY_URL || ''

async function login(page: Page): Promise<void> {
  await page.goto('/login')
  await page.locator('#username').fill('admin')
  await page.locator('#password').fill(ADMIN_PW)
  await page.getByRole('button', { name: '登录' }).click()
  await expect(page).not.toHaveURL(/\/login/, { timeout: 20_000 })
}

test.describe('真 webhook 通知(从 UI 建渠道 → 测试发送打真接收器)', () => {
  test.skip(!NOTIFY_ON, '需 PW_NOTIFY=1(harness 起了本地 HTTP 接收器)')

  test('新增 webhook 渠道指向本地接收器 → 测试发送 → UI 显示发送成功', async ({ page }) => {
    await login(page)
    await page.goto('/settings/notifications')
    await expect(page.getByRole('heading', { name: '通知渠道' })).toBeVisible()

    // 1. 点「新增渠道」打开编辑器。
    await page.getByRole('button', { name: '新增渠道' }).first().click()

    // 2. 渠道类型默认 webhook(radiogroup「渠道类型」)。确保选中 Webhook。
    await page.getByRole('radiogroup', { name: '渠道类型' }).getByRole('button', { name: /Webhook/ }).click()

    // 3. 填渠道名 + webhook 地址(本地接收器)。
    const channelName = `e2e-webhook-${Date.now()}`
    await page.locator('#nt-name').fill(channelName)
    await page.locator('#nt-url').fill(NOTIFY_URL)

    // 4. 创建 → 真 POST /api/notifications/channels。
    await page.getByRole('button', { name: '创建渠道' }).click()
    await expect(page.getByText('渠道已创建')).toBeVisible({ timeout: 15_000 })

    // 5. 列表出现该渠道。
    const card = page.locator('li.ch-card', { hasText: channelName })
    await expect(card).toBeVisible({ timeout: 15_000 })

    // 6. 点「测试发送」→ 真后端真打 POST 到本地接收器。
    await card.getByRole('button', { name: '测试发送' }).click()

    // 7. 真接收器返回 2xx → UI 显示「发送成功」。
    await expect(card.getByText(/发送成功/)).toBeVisible({ timeout: 30_000 })
  })
})
