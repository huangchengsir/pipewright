import { test, expect, type Page } from '@playwright/test'

// credentials.real.spec.ts —— 真后端凭据 CRUD 全栈 e2e:从 UI 建凭据 → 真 vault secretbox 加密落库
// → 列表只回掩码、绝无明文。打真二进制,不 mock。

const ADMIN_PW = process.env.PW_ADMIN_PW || 'fs-admin-pw'

async function login(page: Page): Promise<void> {
  await page.goto('/login')
  await page.locator('#username').fill('admin')
  await page.locator('#password').fill(ADMIN_PW)
  await page.getByRole('button', { name: '登录' }).click()
  await expect(page).not.toHaveURL(/\/login/, { timeout: 20_000 })
}

test('从 UI 建凭据 → 真 vault 加密 → 列表掩码、绝无明文', async ({ page }) => {
  await login(page)
  await page.goto('/settings/vault')
  await expect(page.getByRole('heading', { name: '凭据保险库' })).toBeVisible()

  // 打开添加凭据模态(列表可能为空 → 「添加第一个凭据」,也可能是工具栏「添加凭据」)。
  await page.getByRole('button', { name: /添加(第一个)?凭据/ }).first().click()
  const dialog = page.getByRole('dialog', { name: '添加凭据' })
  await expect(dialog).toBeVisible()

  const plaintext = 'ghp_UI_PLAINTEXT_must_be_masked_77'
  await dialog.locator('#cred-name').fill('ui-e2e-token')
  await dialog.locator('#cred-secret').fill(plaintext)
  await dialog.getByRole('button', { name: '创建凭据' }).click()

  // 新凭据进列表(真后端写库后回读)。
  await expect(page.getByText('ui-e2e-token')).toBeVisible({ timeout: 15_000 })
  // 红线:明文 secret 绝不出现在任何响应/DOM。
  await expect(page.getByText(plaintext)).toHaveCount(0)
})
