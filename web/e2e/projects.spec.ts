import { test, expect } from '@playwright/test'
import { stubLoggedIn, projectFixture } from './_stubs'

/**
 * Projects list + create flow. GET /api/projects returns the list shape;
 * POST /api/projects echoes a created Project DTO. All offline via page.route.
 */

test.describe('Projects page', () => {
  test('renders the project grid from GET /api/projects', async ({ page }) => {
    await stubLoggedIn(page)
    await page.route(
      (u) => u.pathname === '/api/projects',
      (route) => {
        if (route.request().method() !== 'GET') return route.fallback()
        return route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify([
            projectFixture({ id: 'p1', name: 'acme-web', lastRunStatus: '成功' }),
            projectFixture({ id: 'p2', name: 'billing-svc', lastRunStatus: '失败' }),
          ]),
        })
      },
    )
    await page.goto('/projects')

    await expect(page.getByRole('heading', { name: '项目', exact: true })).toBeVisible()
    await expect(page.getByRole('heading', { name: 'acme-web' })).toBeVisible()
    await expect(page.getByRole('heading', { name: 'billing-svc' })).toBeVisible()
    // The search/filter toolbar appears only when projects exist.
    await expect(page.getByRole('search', { name: '项目搜索与筛选' })).toBeVisible()
  })

  test('shows the empty state with a primary CTA when no projects exist', async ({ page }) => {
    await stubLoggedIn(page) // catch-all returns [] for GET /api/projects
    await page.goto('/projects')

    const empty = page.getByRole('status').filter({ hasText: '还没有项目' })
    await expect(empty).toBeVisible()
    await expect(empty.getByRole('button', { name: '新建项目' })).toBeVisible()
  })

  test('create-project modal opens, validates empty submit, then creates via POST', async ({ page }) => {
    await stubLoggedIn(page)
    // Credentials dropdown source: one git_token so the form is completable.
    await page.route(
      (u) => u.pathname === '/api/credentials',
      (route) => {
        if (route.request().method() !== 'GET') return route.fallback()
        return route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify([
            { id: 'c-git', name: 'gitee-token', type: 'git_token', scope: '*', maskedValue: 'ghp_••••a91f', lastUsedAt: null, createdAt: '2026-05-01T00:00:00Z' },
          ]),
        })
      },
    )
    await page.route(
      (u) => u.pathname === '/api/projects',
      (route) => {
        if (route.request().method() === 'POST') {
          return route.fulfill({
            status: 200,
            contentType: 'application/json',
            body: JSON.stringify(projectFixture({ id: 'p-new', name: 'new-app', lastRunStatus: null })),
          })
        }
        return route.fulfill({ status: 200, contentType: 'application/json', body: '[]' })
      },
    )
    await page.goto('/projects')

    // Header CTA (empty state also has a "新建项目" button — scope to the header).
    await page.locator('header').getByRole('button', { name: '新建项目' }).click()
    const dialog = page.getByRole('dialog', { name: '新建项目' })
    await expect(dialog).toBeVisible()

    // Empty submit → field-level validation errors, no navigation.
    await dialog.getByRole('button', { name: '创建项目' }).click()
    await expect(page.getByText('请输入项目名称')).toBeVisible()
    await expect(page.getByText('请输入仓库地址')).toBeVisible()

    // Fill a valid form and submit → POST → card appears in the grid.
    await dialog.locator('#proj-name').fill('new-app')
    await dialog.locator('#proj-repo').fill('https://gitee.com/acme/new-app.git')
    await dialog.locator('#proj-cred').selectOption('c-git')
    await dialog.getByRole('button', { name: '创建项目' }).click()

    await expect(dialog).toBeHidden()
    await expect(page.getByRole('heading', { name: 'new-app' })).toBeVisible()
  })

  test('Esc closes the create modal (keyboard reachable)', async ({ page }) => {
    await stubLoggedIn(page)
    await page.goto('/projects')

    await page.locator('header').getByRole('button', { name: '新建项目' }).click()
    const dialog = page.getByRole('dialog', { name: '新建项目' })
    await expect(dialog).toBeVisible()
    await dialog.locator('#proj-name').press('Escape')
    await expect(dialog).toBeHidden()
  })
})
