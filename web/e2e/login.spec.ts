import { test, expect, type Page } from '@playwright/test'
import { stubLoggedOut } from './_stubs'

async function prepareLogin(
  page: Page,
  providersVisible = false,
  locale = 'zh-CN',
) {
  await page.addInitScript(
    (code) => localStorage.setItem('pipewright-locale', code),
    locale,
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

async function expectNoHorizontalOverflow(page: Page) {
  expect(
    await page.evaluate(() => document.documentElement.scrollWidth),
  ).toBeLessThanOrEqual(await page.evaluate(() => window.innerWidth))
}

test.describe('Login page', () => {
  for (const theme of ['light', 'dark'] as const) {
    test(`adapts the showcase and favicon logo across viewports in ${theme} mode`, async ({
      page,
    }) => {
      await prepareLogin(page)
      await page.addInitScript(
        (value) => localStorage.setItem('pipewright-theme', value),
        theme,
      )
      await page.goto('/login')
      await expect(page.locator('.login-page')).toHaveAttribute(
        'data-theme',
        theme,
      )
      const features = page.locator('.showcase-footer .trust-item')
      await expect(features).toHaveCount(3)
      await expect(features.locator('svg.trust-icon')).toHaveCount(3)
      const gradientIds = await page
        .locator('.brand-icon linearGradient')
        .evaluateAll((gradients) => gradients.map((gradient) => gradient.id))
      expect(gradientIds).toHaveLength(4)
      expect(new Set(gradientIds).size).toBe(gradientIds.length)
      for (const width of [
        1920, 1600, 1366, 1280, 1025, 1024, 900, 768, 390, 320,
      ]) {
        await page.setViewportSize({ width, height: 844 })
        const compact = width <= 1024
        const showcase = page.locator('.showcase')
        const compactBrand = page.locator('.login-access-brand')
        const logo = page.locator(
          compact ? '.login-access-brand svg' : '.showcase svg.brand-mark',
        )
        await expect(logo).toBeVisible()
        await expect(logo).toHaveAttribute('viewBox', '0 0 64 64')
        await expect(logo).toHaveAttribute(
          'preserveAspectRatio',
          'xMidYMid meet',
        )
        const metrics = await logo.evaluate((element) => {
          const svg = element as SVGSVGElement
          const box = svg.getBoundingClientRect()
          const transform = svg.getScreenCTM()!
          return {
            width: box.width,
            height: box.height,
            scaleX: transform.a,
            scaleY: transform.d,
          }
        })
        expect(metrics.width).toBeGreaterThan(0)
        expect(metrics.width).toBe(metrics.height)
        expect(metrics.scaleX).toBe(metrics.scaleY)
        if (compact) {
          await expect(showcase).toBeHidden()
          await expect(compactBrand).toBeVisible()
          await expect(compactBrand).toHaveCSS('position', 'fixed')
          const brand = await compactBrand.boundingBox()
          const locale = await page.locator('.locale-switcher').boundingBox()
          const access = await page.locator('.login-access').boundingBox()
          const form = await page.locator('.login-panel').boundingBox()
          expect(brand!.x).toBe(24)
          expect(brand!.y).toBe(12)
          expect(brand!.x + brand!.width + 12).toBeLessThan(locale!.x)
          expect(form!.y).toBeGreaterThanOrEqual(brand!.y + brand!.height + 24)
          expect(access!.x).toBe(0)
          expect(access!.width).toBe(width)
        } else {
          await expect(showcase).toBeVisible()
          await expect(compactBrand).toBeHidden()
          const left = await showcase.boundingBox()
          const right = await page.locator('.login-access').boundingBox()
          expect(left!.width / width).toBeCloseTo(0.4, 3)
          expect(right!.width / width).toBeCloseTo(0.6, 3)
          for (const content of await showcase
            .locator(
              '.brand-mark, .brand-word, .project-description, .trust-item',
            )
            .all()) {
            const box = await content.boundingBox()
            expect(box!.x).toBeGreaterThanOrEqual(left!.x)
            expect(box!.x + box!.width).toBeLessThanOrEqual(
              left!.x + left!.width,
            )
          }
          for (const feature of await features.all()) {
            const icon = await feature.locator('svg').boundingBox()
            const label = await feature.locator('span').boundingBox()
            expect(icon).not.toBeNull()
            expect(label).not.toBeNull()
            expect(icon!.width).toBe(icon!.height)
            expect(icon!.x + icon!.width).toBeLessThan(label!.x)
            expect(
              Math.abs(
                icon!.y + icon!.height / 2 - label!.y - label!.height / 2,
              ),
            ).toBeLessThan(1)
          }
        }
        await expectNoHorizontalOverflow(page)
      }
    })
  }

  test('keeps the compact logo fixed while the form scrolls', async ({
    page,
  }) => {
    await page.setViewportSize({ width: 390, height: 540 })
    await prepareLogin(page, true)
    await page.goto('/login')
    const logo = page.locator('.login-access-brand')
    const initial = await logo.boundingBox()
    await page.locator('[data-provider="microsoft"]').scrollIntoViewIfNeeded()
    expect(await page.evaluate(() => window.scrollY)).toBeGreaterThan(0)
    await expect(logo).toBeInViewport()
    const scrolled = await logo.boundingBox()
    expect(scrolled!.x).toBe(initial!.x)
    expect(scrolled!.y).toBe(initial!.y)
    await expectNoHorizontalOverflow(page)
  })

  test('renders the project showcase beside the login form on desktop', async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1366, height: 768 })
    await prepareLogin(page)
    await page.goto('/login')

    await expect(page.getByRole('form', { name: '登录表单' })).toBeVisible()
    await expect(page.getByLabel('账号', { exact: true })).toBeVisible()
    await expect(page.getByLabel('密码', { exact: true })).toBeVisible()
    await expect(
      page.getByRole('button', { name: '进入控制台', exact: true }),
    ).toBeVisible()
    await expect(page.locator('.brand-word')).toHaveText('Pipewright')
    await expect(page.locator('.project-description')).toHaveText(
      '自托管的 CI/CD 与部署编排,构建失败 AI 秒级定位。',
    )
    await expect(
      page.locator(
        '.pipeline, .capabilities, .intro-label, .login-security, .login-footer',
      ),
    ).toHaveCount(0)
    await expect(
      page.getByRole('heading', { name: 'Pipewright', exact: true }),
    ).toBeVisible()
    const showcase = await page.locator('.showcase').boundingBox()
    const form = await page.locator('.login-panel').boundingBox()
    expect(showcase).not.toBeNull()
    expect(form).not.toBeNull()
    expect(form!.x).toBeGreaterThanOrEqual(showcase!.x + showcase!.width)
    const submit = await page.locator('.submit').boundingBox()
    expect(submit!.y + submit!.height).toBeLessThanOrEqual(768)
    await expectNoHorizontalOverflow(page)
  })

  test('client-side validation blocks empty submit with field errors', async ({
    page,
  }) => {
    await prepareLogin(page)
    await page.goto('/login')
    await page.getByRole('button', { name: '进入控制台', exact: true }).click()
    await expect(page.getByText('请输入用户名', { exact: true })).toBeVisible()
    await expect(page.getByText('请输入密码', { exact: true })).toBeVisible()
    await expect(page.locator('#username')).toHaveAttribute(
      'aria-invalid',
      'true',
    )
  })

  test('password visibility and keyboard submission remain accessible', async ({
    page,
  }) => {
    await prepareLogin(page)
    await page.goto('/login')
    await page.getByLabel('密码', { exact: true }).fill('a-test-password')
    await page.getByRole('button', { name: '显示密码', exact: true }).click()
    await expect(page.locator('#password')).toHaveAttribute('type', 'text')
    await page.getByRole('button', { name: '隐藏密码', exact: true }).click()
    await expect(page.locator('#password')).toHaveAttribute('type', 'password')
    await page.locator('#password').press('Enter')
    await expect(page.getByText('请输入用户名', { exact: true })).toBeVisible()
  })

  test('a protected route redirects an unauthenticated visitor to /login', async ({
    page,
  }) => {
    await prepareLogin(page)
    await page.goto('/projects')
    await expect(page).toHaveURL(/\/login/)
  })

  test('hides the showcase on mobile and keeps platform login and errors reachable', async ({
    page,
  }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await prepareLogin(page, true)
    await page.goto('/login')
    await expect(page.locator('.showcase')).toBeHidden()
    await expect(page.locator('.login-access-brand')).toBeVisible()
    await expect(page.locator('.login-panel')).toBeVisible()
    await expect(
      page.getByRole('group', { name: '其他方式登录' }),
    ).toBeVisible()
    await expect(page.locator('[data-provider]')).toHaveCount(5)
    await page.locator('[data-provider="github"]').click()
    await expect(page.locator('.platform-note')).toBeVisible()
    const submit = await page.locator('.submit').boundingBox()
    const providers = await page.locator('.platform-options').boundingBox()
    expect(providers!.y).toBeGreaterThan(submit!.y + submit!.height)
    await page.getByRole('button', { name: '进入控制台', exact: true }).click()
    await expect(page.getByText('请输入用户名', { exact: true })).toBeVisible()
    await expectNoHorizontalOverflow(page)
  })

  test('remains scrollable on a short laptop viewport with all platform options', async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 600 })
    await prepareLogin(page, true)
    await page.goto('/login')
    await page.locator('[data-provider="microsoft"]').scrollIntoViewIfNeeded()
    await expect(page.locator('[data-provider="microsoft"]')).toBeInViewport()
    await page.locator('.submit').scrollIntoViewIfNeeded()
    await expect(page.locator('.submit')).toBeInViewport()
    await expectNoHorizontalOverflow(page)
  })

  test('uses original localized copy without extra promotional sections', async ({
    page,
  }) => {
    await page.emulateMedia({ reducedMotion: 'reduce', colorScheme: 'light' })
    await page.setViewportSize({ width: 1024, height: 768 })
    await prepareLogin(page, true, 'de')
    await page.goto('/login')
    await expect(
      page.getByRole('heading', { name: /Willkommen zurück/ }),
    ).toBeVisible()
    await expect(page.locator('.login-page')).not.toContainText('loginPage.')
    await expect(
      page.locator(
        '.pipeline, .capabilities, .access-label, .login-security, .login-footer',
      ),
    ).toHaveCount(0)
    await expectNoHorizontalOverflow(page)
    await page.setViewportSize({ width: 320, height: 740 })
    await expectNoHorizontalOverflow(page)
  })
})
