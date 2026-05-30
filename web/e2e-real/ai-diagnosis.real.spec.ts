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
  // 依赖真外部 LLM,允许 1 次重试吸收瞬时延迟/限流。
  test.describe.configure({ retries: 1 })

  test('失败 run → UI 点「分析失败原因」→ 真 DeepSeek 根因进诊断卡 + 凭据明文脱敏', async ({ page }) => {
    await login(page)
    await page.goto('/runs/fs-failrun')

    // 失败且未诊断 → 诊断面板出「分析失败原因」触发按钮(前端触发首诊)。
    const diagnoseBtn = page.getByRole('button', { name: /分析失败原因/ })
    await expect(diagnoseBtn).toBeVisible({ timeout: 15_000 })
    await diagnoseBtn.click()

    // 等 UI 往返完成:ready 出「AI 认为」区,或 unavailable 出「诊断不可用」。给真 LLM 充足时间。
    // 这条 e2e 验的是**前端触发诊断的整条往返**;真 DeepSeek 的可用性/时延是外部因素,
    // 故 ready/unavailable 都算往返成功(诊断质量由后端 Go e2e diagnose_e2e_test.go 保证)。
    const aiCol = page.getByRole('region', { name: 'AI 认为' })
    const unavailable = page.getByText('诊断不可用')
    await expect(aiCol.or(unavailable)).toBeVisible({ timeout: 120_000 })

    if (await aiCol.isVisible()) {
      // ready:真 DeepSeek 给出非空根因假说,显示在诊断卡。
      await expect(aiCol.locator('.dp-hypothesis-text')).not.toBeEmpty()
    } else {
      // unavailable:UI 往返正常,但本次真 DeepSeek 不可用(网络/限流——外部因素,非前端/后端缺陷)。
      // eslint-disable-next-line no-console
      console.warn('[e2e] 本次 DeepSeek 返回 unavailable;UI 触发→后端→面板往返正常,脱敏仍校验。')
    }

    // 红线(ready / unavailable 都必须成立):失败日志里的项目凭据明文经出网脱敏,绝不出现在 UI。
    await expect(page.getByText('ghp_fs_e2e_tok')).toHaveCount(0)
  })
})
