import { test, expect } from '@playwright/test'
import { stubLoggedIn } from './_stubs'

test.describe('App shell navigation', () => {
  test('renders the left rail with the primary nav destinations', async ({ page }) => {
    await stubLoggedIn(page)
    await page.goto('/')

    const nav = page.getByRole('navigation', { name: '主导航' })
    await expect(nav).toBeVisible()

    // Core rail destinations (frozen labels via aria-label).
    for (const label of ['概览', '项目', '运行', '服务器', '设置']) {
      await expect(nav.getByRole('link', { name: label, exact: true })).toBeVisible()
    }
  })

  test('clicking a rail link navigates within the shell', async ({ page }) => {
    await stubLoggedIn(page)
    await page.goto('/')

    const nav = page.getByRole('navigation', { name: '主导航' })
    await nav.getByRole('link', { name: '项目', exact: true }).click()
    await expect(page).toHaveURL(/\/projects$/)
    // Rail persists after navigation (shell layout, not a full reload).
    await expect(nav).toBeVisible()
  })
})
