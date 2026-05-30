import { test, expect, type Page } from '@playwright/test'

// ai-diagnosis.real.spec.ts —— 旗舰真 e2e:失败 run 详情 → **从 UI 点「分析失败原因」** →
// 真后端调**真 DeepSeek** → 根因假说显示在诊断卡「AI 认为」区;日志里的凭据明文经出网脱敏
// 绝不出现在 UI。打真二进制不 mock。仅当 harness 配了 DeepSeek(PW_AI=1)时跑。

const ADMIN_PW = process.env.PW_ADMIN_PW || 'fs-admin-pw'
const AI_ON = process.env.PW_AI === '1'

async function login(page: Page): Promise<void> {
  await page.goto('/login')
  await page.locator('#username').fill('admin')
  await page.locator('#password').fill(ADMIN_PW)
  await page.getByRole('button', { name: '登录' }).click()
  await expect(page).not.toHaveURL(/\/login/, { timeout: 20_000 })
}

test.describe('真 AI 失败诊断(从 UI 触发 → 真 DeepSeek)', () => {
  test.skip(!AI_ON, '需 PW_AI=1(harness 配了 DeepSeek key)')

  test('失败 run → UI 点「分析失败原因」→ 真 DeepSeek 根因进诊断卡 + 凭据明文脱敏', async ({ page }) => {
    await login(page)
    await page.goto('/runs/fs-failrun')

    // 失败且未诊断 → 诊断面板出「分析失败原因」触发按钮(前端触发首诊)。
    const diagnoseBtn = page.getByRole('button', { name: /分析失败原因/ })
    await expect(diagnoseBtn).toBeVisible({ timeout: 15_000 })
    await diagnoseBtn.click()

    // 真 DeepSeek 返回(给足推理时间)→「AI 认为」区出现非空根因假说。
    const aiCol = page.getByRole('region', { name: 'AI 认为' })
    await expect(aiCol).toBeVisible({ timeout: 60_000 })
    await expect(aiCol.locator('.dp-hypothesis-text')).not.toBeEmpty()

    // 红线:失败日志里的项目凭据明文(ghp_fs_e2e_tok)经出网脱敏,绝不出现在 UI 任何处。
    await expect(page.getByText('ghp_fs_e2e_tok')).toHaveCount(0)
  })
})
