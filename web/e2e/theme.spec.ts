import { test, expect } from '@playwright/test'
import { stubLoggedOut } from './_stubs'

// Theme toggle is available on the login page (no auth needed) and persists
// to localStorage under `pipewright-theme`, applied to <html data-theme>.
test.describe('Theme switching', () => {
  test('defaults to dark', async ({ page }) => {
    await stubLoggedOut(page)
    await page.goto('/login')
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')
  })

  test('toggling flips the theme and persists across reload', async ({ page }) => {
    await stubLoggedOut(page)
    await page.goto('/login')

    // ThemeToggle aria-label reads "切换到浅色" while in dark mode.
    await page.getByRole('button', { name: '切换到浅色' }).click()
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'light')

    const stored = await page.evaluate(() => localStorage.getItem('pipewright-theme'))
    expect(stored).toBe('light')

    await page.reload()
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'light')
  })
})
