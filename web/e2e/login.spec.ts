import { test, expect } from '@playwright/test'
import { stubLoggedOut } from './_stubs'

test.describe('Login page', () => {
  test('renders the brand + login form with both fields', async ({ page }) => {
    await stubLoggedOut(page)
    await page.goto('/login')

    await expect(page.getByRole('form', { name: '登录表单' })).toBeVisible()
    await expect(page.locator('#username')).toBeVisible()
    await expect(page.locator('#password')).toBeVisible()
    await expect(page.getByRole('button', { name: '登录' })).toBeVisible()
    await expect(page.getByText('Pipewright')).toBeVisible()
  })

  test('client-side validation blocks empty submit with field errors', async ({ page }) => {
    await stubLoggedOut(page)
    await page.goto('/login')

    await page.getByRole('button', { name: '登录' }).click()
    await expect(page.getByText('请输入用户名')).toBeVisible()
    await expect(page.getByText('请输入密码')).toBeVisible()
  })

  test('a protected route redirects an unauthenticated visitor to /login', async ({ page }) => {
    await stubLoggedOut(page)
    await page.goto('/projects')
    await expect(page).toHaveURL(/\/login/)
  })
})
