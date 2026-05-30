import { test, expect, type Page } from '@playwright/test'

// ai-config.real.spec.ts —— 真全栈 e2e:从 UI 配置 AI 提供商。登录 → /settings/ai →
// radiogroup「AI 提供商选择」点「选择 OpenAI」→ 填 baseUrl/model/apiKey(dummy key,非真 key)
// → 保存 → 断言 toast「AI 配置已保存」+ 重载后 provider/baseUrl 回显。真打 PUT /api/settings/ai
// (真后端经 vault 加密落 apiKey,GET 仅回掩码)。打真二进制,不 mock。
//
// 始终可跑(不需 docker / 不需真 LLM key);dummy key 仅用于走通保存链路,不发起真实 LLM 调用。

const ADMIN_PW = process.env.PW_ADMIN_PW || 'fs-admin-pw'

async function login(page: Page): Promise<void> {
  await page.goto('/login')
  await page.locator('#username').fill('admin')
  await page.locator('#password').fill(ADMIN_PW)
  await page.getByRole('button', { name: '登录' }).click()
  await expect(page).not.toHaveURL(/\/login/, { timeout: 20_000 })
}

test('从 UI 配 AI 提供商(OpenAI)→ 真 PUT 落库 → toast + 重载回显', async ({ page }) => {
  await login(page)
  await page.goto('/settings/ai')
  await expect(page.getByRole('heading', { name: 'AI 提供商' })).toBeVisible()

  // 1. radiogroup 里点「选择 OpenAI」provider 卡。
  const providerGroup = page.getByRole('radiogroup', { name: 'AI 提供商选择' })
  await providerGroup.getByRole('button', { name: '选择 OpenAI' }).click()

  // 选 OpenAI 后 baseUrl 自动填默认 → 改成一个可识别的自托管地址以验回显。
  const customBaseUrl = 'https://openai-proxy.e2e.internal'
  await page.locator('#ai-baseurl').fill(customBaseUrl)
  await page.locator('#ai-model').fill('gpt-4o-mini')
  // dummy key:仅用于走通写入链路,绝不是真 key。
  await page.locator('#ai-apikey').fill('sk-dummy-e2e')

  // 2. 点保存(按钮文案为「保存更改」)→ 真 PUT /api/settings/ai。
  await page.getByRole('button', { name: '保存更改' }).click()

  // 3. 断言成功 toast「AI 配置已保存」。
  await expect(page.getByText('AI 配置已保存')).toBeVisible({ timeout: 15_000 })

  // 4. 重载页面 → 真 GET /api/settings/ai 回显持久化的 provider/baseUrl(apiKey 仅掩码)。
  await page.reload()
  await expect(page.getByRole('heading', { name: 'AI 提供商' })).toBeVisible()
  // 已配置状态徽标。
  await expect(page.getByText('已配置')).toBeVisible({ timeout: 15_000 })
  // OpenAI 卡片为选中态(aria-pressed=true)。
  await expect(providerGroup.getByRole('button', { name: '选择 OpenAI' })).toHaveAttribute(
    'aria-pressed',
    'true',
  )
  // baseUrl 回显我们填的自托管地址。
  await expect(page.locator('#ai-baseurl')).toHaveValue(customBaseUrl)
  await expect(page.locator('#ai-model')).toHaveValue('gpt-4o-mini')

  // 红线:dummy key 明文绝不回显(GET 只回掩码)。
  await expect(page.getByText('sk-dummy-e2e')).toHaveCount(0)
})
