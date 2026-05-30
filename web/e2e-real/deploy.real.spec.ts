import { test, expect, type Page } from '@playwright/test'

// deploy.real.spec.ts —— 旗舰真全栈 e2e:多机 SSH 部署走 UI。
// 需 2~3 台真 sshd 容器 + 一个 status=success 的 run + 一个 dist 产物(harness 备好:
// 经 API 把每台容器登记为 server,seed 成功 run「fs-deployrun」+ run_artifacts 一行 type=dist;
// 透传 PW_DEPLOY=1 + PW_DEPLOY_RUN=run id)。登录 → /runs/{id} → 部署区(选产物 + 勾选多台
// 目标服务器 + 开始部署)→ 真打 POST /api/runs/{id}/deploy → 真后端经真 SSH 并行部署到多台
// → UI 部署目标结果区出现每台「成功」徽标。打真二进制,不 mock。
//
// 无容器环境(PW_DEPLOY 未置)→ 自动 skip。

const ADMIN_PW = process.env.PW_ADMIN_PW || 'fs-admin-pw'
const DEPLOY_ON = process.env.PW_DEPLOY === '1'
const DEPLOY_RUN = process.env.PW_DEPLOY_RUN || 'fs-deployrun'

async function login(page: Page): Promise<void> {
  await page.goto('/login')
  await page.locator('#username').fill('admin')
  await page.locator('#password').fill(ADMIN_PW)
  await page.getByRole('button', { name: '登录' }).click()
  await expect(page).not.toHaveURL(/\/login/, { timeout: 20_000 })
}

test.describe('真多机 SSH 部署(从 run 详情 UI 触发)', () => {
  test.skip(!DEPLOY_ON, '需 PW_DEPLOY=1(harness 起了多台 sshd 容器 + 成功 run + dist 产物)')

  test('成功 run → 部署面板选 dist + 勾全部目标 → 真并行 SSH 部署 → 每台成功徽标', async ({ page }) => {
    await login(page)
    await page.goto(`/runs/${DEPLOY_RUN}`)

    // 成功态 run 详情含部署入口。
    const openBtn = page.getByRole('button', { name: /部署到目标服务器/ })
    await expect(openBtn).toBeVisible({ timeout: 15_000 })
    await openBtn.click()

    // 部署面板出现(选产物 + 选服务器 + 触发)。
    const panel = page.getByRole('region', { name: '部署配置' })
    await expect(panel).toBeVisible()

    // 产物默认选中首个 dist 产物(select#deploy-artifact 已有选项)。
    await expect(panel.locator('#deploy-artifact')).toBeVisible()

    // 勾选所有目标服务器复选框(harness 登记的多台)。
    const checkboxes = panel.locator('.deploy-servers input[type="checkbox"]')
    const count = await checkboxes.count()
    expect(count).toBeGreaterThanOrEqual(2) // 至少 2 台多机
    for (let i = 0; i < count; i++) {
      await checkboxes.nth(i).check()
    }
    await expect(panel.getByText(`已选 ${count} 台`)).toBeVisible()

    // 点「开始部署」→ 真 POST /api/runs/{id}/deploy(body artifactId/serverIds)。
    await panel.getByRole('button', { name: '开始部署' }).click()

    // 真后端并行 SSH 部署 → 回填 targets slot;部署目标结果区出现且每台「成功」徽标。
    const results = page.getByRole('region', { name: '部署目标结果' })
    await expect(results).toBeVisible({ timeout: 40_000 })
    // 每台一张成功徽标(状态 aria-label「部署状态:成功」)。
    const okBadges = results.locator('[aria-label="部署状态:成功"]')
    await expect(okBadges).toHaveCount(count, { timeout: 40_000 })
    // 头部汇总「N 成功」。
    await expect(results.getByText(`${count} 成功`)).toBeVisible()
  })
})
