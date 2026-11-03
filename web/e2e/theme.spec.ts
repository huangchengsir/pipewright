import { test, expect, type Page } from '@playwright/test'
import { stubLoggedIn, stubLoggedOut } from './_stubs'

async function prepareLogin(page: Page, providersVisible = false) {
  await page.addInitScript(() =>
    localStorage.setItem('pipewright-locale', 'zh-CN'),
  )
  await stubLoggedOut(page)
  await page.route(
    (u) => u.pathname === '/api/auth/platform/providers',
    (route) =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          providers: ['github', 'gitlab', 'gitee', 'google', 'microsoft'].map(
            (provider) => ({
              provider,
              visible: providersVisible,
              available: false,
              reason: 'not_configured',
            }),
          ),
        }),
      }),
  )
}

async function palette(page: Page) {
  await page.locator('#username').waitFor({ state: 'visible' })
  return page.evaluate(() => {
    const color = (selector: string, property: 'color' | 'backgroundColor') =>
      getComputedStyle(document.querySelector(selector)!)[property]
    return {
      page: color('.login-page', 'backgroundColor'),
      showcase: color('.showcase', 'backgroundColor'),
      access: color('.login-access', 'backgroundColor'),
      input: color('#username', 'backgroundColor'),
      heading: color('#login-title', 'color'),
      label: color('.field__label', 'color'),
      description: color('.project-description', 'color'),
      field: color('.input-shell', 'backgroundColor'),
    }
  })
}

test.describe('Login theme switching', () => {
  test('uses the existing light default with a login-owned theme button at the bottom right', async ({
    page,
  }) => {
    await prepareLogin(page)
    await page.goto('/login')
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'light')
    await expect(page.locator('.login-page')).toHaveAttribute(
      'data-theme',
      'light',
    )
    const theme = page.getByRole('button', { name: '切换到深色', exact: true })
    await expect(theme).toBeVisible()
    const themeBox = await theme.boundingBox()
    const localeBox = await page.locator('.locale-switcher').boundingBox()
    await expect(page.locator('.login-page .theme-toggle')).toHaveCount(1)
    await expect(page.locator('.app-preferences')).toHaveCount(0)
    await expect(theme).toHaveCSS('position', 'fixed')
    const viewport = page.viewportSize()!
    expect(themeBox!.x + themeBox!.width).toBeCloseTo(viewport.width - 20, 1)
    expect(themeBox!.y + themeBox!.height).toBeCloseTo(viewport.height - 18, 1)
    expect(localeBox!.y).toBeLessThan(24)
  })

  test('changes the actual page colors and preserves the choice after reload', async ({
    page,
  }) => {
    await prepareLogin(page)
    await page.goto('/login')
    const light = await palette(page)
    await page.getByRole('button', { name: '切换到深色', exact: true }).click()
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')
    await expect(page.locator('.login-page')).toHaveAttribute(
      'data-theme',
      'dark',
    )
    const dark = await palette(page)
    for (const key of [
      'page',
      'showcase',
      'access',
      'heading',
      'label',
      'description',
      'field',
    ] as const) {
      expect(dark[key], `${key} should follow the selected theme`).not.toBe(
        light[key],
      )
    }
    expect(
      await page.evaluate(() => localStorage.getItem('pipewright-theme')),
    ).toBe('dark')
    await page.reload()
    await expect(page.locator('.login-page')).toHaveAttribute(
      'data-theme',
      'dark',
    )
    await expect(
      page.getByRole('button', { name: '切换到浅色', exact: true }),
    ).toBeVisible()
    await page.getByRole('button', { name: '切换到浅色', exact: true }).click()
    await expect(page.locator('.login-page')).toHaveAttribute(
      'data-theme',
      'light',
    )
    await expect.poll(() => palette(page)).toEqual(light)
  })

  test('respects a dark theme saved before visiting login', async ({
    page,
  }) => {
    await prepareLogin(page)
    await page.addInitScript(() =>
      localStorage.setItem('pipewright-theme', 'dark'),
    )
    await page.goto('/login')
    await expect(page.locator('.login-page')).toHaveAttribute(
      'data-theme',
      'dark',
    )
    await expect(
      page.getByRole('button', { name: '切换到浅色', exact: true }),
    ).toBeVisible()
  })

  test('keeps validation and platform authorization messages themed', async ({
    page,
  }) => {
    await prepareLogin(page, true)
    await page.goto('/login')
    await page.getByRole('button', { name: '进入控制台', exact: true }).click()
    await expect(page.locator('#username-err')).toBeVisible()
    await page.locator('[data-provider="github"]').click()
    await expect(page.locator('.platform-note')).toBeVisible()
    const readColors = () =>
      page.evaluate(() => ({
        error: getComputedStyle(document.querySelector('.field__error')!).color,
        note: getComputedStyle(document.querySelector('.platform-note')!)
          .backgroundColor,
        noteText: getComputedStyle(document.querySelector('.platform-note')!)
          .color,
        provider: getComputedStyle(document.querySelector('.platform-button')!)
          .backgroundColor,
        providerText: getComputedStyle(
          document.querySelector('.platform-button')!,
        ).color,
      }))
    const light = await readColors()
    await page.getByRole('button', { name: '切换到深色', exact: true }).click()
    await expect(page.locator('.login-page')).toHaveAttribute(
      'data-theme',
      'dark',
    )
    const dark = await readColors()
    for (const key of Object.keys(light) as (keyof typeof light)[])
      expect(dark[key]).not.toBe(light[key])
    await expect(page.locator('#username-err')).toHaveText('请输入用户名')
    await expect(page.locator('.platform-note')).toBeVisible()
  })

  test('keeps both controls usable by keyboard on a narrow phone', async ({
    page,
  }) => {
    await page.setViewportSize({ width: 320, height: 740 })
    await prepareLogin(page)
    await page.goto('/login')
    const theme = page.locator('.login-page .theme-toggle')
    await theme.focus()
    await page.keyboard.press('Enter')
    await expect(page.locator('.login-page')).toHaveAttribute(
      'data-theme',
      'dark',
    )
    const themeBox = await theme.boundingBox()
    const localeBox = await page.locator('.locale-switcher').boundingBox()
    expect(themeBox!.x).toBeGreaterThanOrEqual(0)
    expect(themeBox!.x + themeBox!.width).toBeCloseTo(300, 1)
    expect(themeBox!.y + themeBox!.height).toBeCloseTo(722, 1)
    expect(themeBox!.y).toBeGreaterThan(localeBox!.y + localeBox!.height)
    expect(localeBox!.y).toBeLessThan(24)
    expect(localeBox!.x + localeBox!.width).toBeLessThanOrEqual(320)
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth),
    ).toBe(320)
    await page.locator('.locale-switcher').focus()
    await page.keyboard.press('Enter')
    await expect(page.getByText('English', { exact: true })).toBeVisible()
  })

  test('does not add a second theme button to authenticated pages', async ({
    page,
  }) => {
    await page.addInitScript(() =>
      localStorage.setItem('pipewright-locale', 'zh-CN'),
    )
    await stubLoggedIn(page)
    await page.goto('/projects')
    await expect(page.locator('.locale-switcher')).toBeVisible()
    await expect(page.locator('.app-preferences')).toHaveCount(0)
    await expect(page.locator('.login-page .theme-toggle')).toHaveCount(0)
    await expect(page.locator('.theme-toggle')).toHaveCount(1)
  })
})
