import { test, expect } from '@playwright/test'

// fullstack.spec.ts —— 真·全栈 e2e:真 chromium → 真前端 UI → **真 pipewright 二进制后端(不 mock)**
// → 真 worker pool 执行 → 真 SSE 推回 → 在 UI 上断言结果。这是「从前端触发、打真后端」的端到端,
// 区别于 web/e2e/*.spec.ts(mock 后端的前端冒烟)与 Go 层 e2e(直调后端 service)。
//
// 前置由 scripts/e2e/fullstack.sh 备好:真二进制已起、已 seed 一个项目「门户站 portal」、admin 口令经
// PW_ADMIN_PW 传入。本 spec 全程经真 UI 操作、打真 API。

const ADMIN_PW = process.env.PW_ADMIN_PW || 'fs-admin-pw'

test.describe('全栈真后端 e2e', () => {
  test('登录 → 项目页 → 从 UI 手动触发运行 → 运行详情看到「成功」(真后端+真 SSE)', async ({ page }) => {
    // 1. 真登录:填真表单 → 打真 /api/auth/login → 真 session。
    await page.goto('/login')
    await expect(page.getByRole('form', { name: '登录表单' })).toBeVisible()
    await page.locator('#username').fill('admin')
    await page.locator('#password').fill(ADMIN_PW)
    await page.getByRole('button', { name: '登录' }).click()

    // 登录成功后离开 /login(真后端发 session cookie,前端守卫放行)。
    await expect(page).not.toHaveURL(/\/login/, { timeout: 20_000 })

    // 2. 项目页:真后端返回 seed 的项目(非 mock)。
    await page.goto('/projects')
    await expect(page.getByText('门户站 portal')).toBeVisible()

    // 3. 从 UI 点卡片上的「手动触发」按钮 → 触发模态出现。
    await page.getByRole('button', { name: '手动触发项目 门户站 portal 的流水线运行' }).click()
    await expect(page.getByText('手动触发运行')).toBeVisible()

    // 4. 指定分支 → 点「立即运行」(真打 POST /api/projects/{id}/runs)。
    await page.locator('#trigger-branch').fill('main')
    await page.getByRole('button', { name: /立即运行/ }).click()

    // 5. 真后端创建 run → 前端跳运行详情;真 worker pool 执行 + 真 SSE 推状态/日志 → UI 状态徽标转「成功」。
    await expect(page).toHaveURL(/\/runs\//, { timeout: 20_000 })
    await expect(page.getByText('成功').first()).toBeVisible({ timeout: 40_000 })
  })

  test('未登录访问受保护页 → 真后端 session 守卫弹回 /login', async ({ page }) => {
    // 清掉登录态(新 context 本就无 cookie),直接访问受保护路由。
    await page.context().clearCookies()
    await page.goto('/projects')
    await expect(page).toHaveURL(/\/login/, { timeout: 15_000 })
  })
})
