import { test, expect } from '@playwright/test'
import { stubLoggedIn } from './_stubs'

test.describe('App shell navigation', () => {
  test.beforeEach(async ({ page }) => {
    await page.addInitScript(() =>
      localStorage.setItem('pipewright-locale', 'zh-CN'),
    )
  })

  test('renders the left rail with the primary nav destinations', async ({
    page,
  }) => {
    await stubLoggedIn(page)
    await page.goto('/')

    const nav = page.getByRole('navigation', { name: '主导航' })
    await expect(nav).toBeVisible()

    // Core rail destinations (frozen labels via aria-label).
    for (const label of ['概览', '项目', '运行', '服务器', '设置']) {
      await expect(
        nav.getByRole('link', { name: label, exact: true }),
      ).toBeVisible()
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

  for (const theme of ['light', 'dark'] as const) {
    test(`shows the shared favicon icon in collapsed and expanded navigation in ${theme} mode`, async ({
      page,
    }) => {
      await page.addInitScript((value) => {
        localStorage.setItem('pipewright-theme', value)
        localStorage.setItem('pipewright_sidebar_expanded', '0')
      }, theme)
      await stubLoggedIn(page)
      await page.goto('/projects')
      await expect(page.locator('html')).toHaveAttribute('data-theme', theme)
      const brand = page.locator('.rail-head .brand')
      const icon = brand.locator('svg.brand-icon')
      await expect(brand).toHaveAccessibleName('Pipewright 主页')
      await expect(brand).not.toContainText('p>')
      await expect(icon).toHaveAttribute('viewBox', '0 0 64 64')
      await expect(icon).toHaveAttribute('preserveAspectRatio', 'xMidYMid meet')
      for (const width of [1366, 768, 390]) {
        await page.setViewportSize({ width, height: 844 })
        for (const expanded of [false, true]) {
          const toggle = page.locator('.rail-edge-toggle')
          if ((await toggle.getAttribute('aria-pressed')) !== String(expanded))
            await toggle.click()
          await expect(toggle).toHaveAttribute('aria-pressed', String(expanded))
          await expect(icon).toBeVisible()
          const metrics = await icon.evaluate((element) => {
            const svg = element as SVGSVGElement
            const box = svg.getBoundingClientRect()
            const transform = svg.getScreenCTM()!
            return {
              width: box.width,
              height: box.height,
              scaleX: transform.a,
              scaleY: transform.d,
              background: getComputedStyle(svg).backgroundColor,
            }
          })
          expect(metrics.width).toBe(30)
          expect(metrics.height).toBe(30)
          expect(metrics.scaleX).toBe(metrics.scaleY)
          expect(metrics.background).toBe('rgba(0, 0, 0, 0)')
          const name = brand.locator('.brand-name')
          await expect(name).toHaveText('Pipewright')
          if (expanded) {
            await expect(name).toBeVisible()
            const rootFontSize = await page
              .locator('html')
              .evaluate((element) =>
                parseFloat(getComputedStyle(element).fontSize),
              )
            await expect(name).toHaveCSS(
              'font-size',
              rootFontSize * 1.125 + 'px',
            )
            await expect(name).toHaveCSS('font-weight', '700')
            const accent = name.locator('.brand-name-accent')
            await expect(accent).toHaveText('wright')
            await expect(accent).toHaveCSS('font-weight', '600')
            await expect(accent).not.toHaveCSS(
              'color',
              await name.evaluate((element) => getComputedStyle(element).color),
            )
            const brandBox = await brand.boundingBox()
            const toggleBox = await toggle.boundingBox()
            expect(brandBox!.x + brandBox!.width + 12).toBeLessThan(
              toggleBox!.x,
            )
          } else {
            await expect(name).toBeHidden()
          }
        }
      }
    })
  }
})
