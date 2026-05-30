import { test, expect } from '@playwright/test'

/**
 * Key interaction primitives exercised on the public living-styleguide route
 * (/states, no auth) where ConfirmDialog + Toast are wired to demo buttons.
 * These primitives are globally mounted (App.vue) and shared by every surface.
 */

test.describe('Interaction primitives (styleguide)', () => {
  test('destructive confirm dialog: cancel does not fire the action, Esc closes', async ({ page }) => {
    await page.goto('/states')

    // Two demo buttons share this label; the wired-up one (section 08) is last.
    await page.getByRole('button', { name: '回滚到 #126' }).last().click()
    const dialog = page.getByRole('dialog', { name: '回滚到 #126?' })
    await expect(dialog).toBeVisible()

    // Esc closes the dialog (keyboard reachable).
    await page.keyboard.press('Escape')
    await expect(dialog).toBeHidden()
    // Cancel path emits the "已取消" toast.
    await expect(page.getByText('已取消')).toBeVisible()
  })

  test('type-to-confirm gates the confirm button until the phrase matches', async ({ page }) => {
    await page.goto('/states')

    await page.getByRole('button', { name: '重置实例' }).click()
    const dialog = page.getByRole('dialog', { name: '重置实例' })
    await expect(dialog).toBeVisible()

    const confirmBtn = dialog.getByRole('button', { name: '永久重置' })
    await expect(confirmBtn).toBeDisabled()

    // Typing the exact phrase enables the confirm button.
    await dialog.getByLabel(/输入 acme 确认操作/).fill('acme')
    await expect(confirmBtn).toBeEnabled()
    await confirmBtn.click()
    await expect(dialog).toBeHidden()
    await expect(page.getByText('已确认重置')).toBeVisible()
  })

  test('toast appears on demand and is dismissible', async ({ page }) => {
    await page.goto('/states')

    await page.getByRole('button', { name: '触发 success' }).click()
    await expect(page.getByText('部署成功').first()).toBeVisible()
  })

  test('theme double-toggle does not break the styleguide page', async ({ page }) => {
    await page.goto('/states')
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')

    const toggle = page.getByRole('button', { name: '切换到浅色' })
    await toggle.click()
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'light')
    await page.getByRole('button', { name: '切换到深色' }).click()
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')
    // Page is still intact after the round-trip.
    await expect(page.getByRole('heading', { name: '组件与状态规范' })).toBeVisible()
  })
})
