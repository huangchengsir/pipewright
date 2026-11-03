import { test, expect, type Page } from '@playwright/test'

async function prepare(
  page: Page,
  reducedMotion: 'reduce' | 'no-preference' = 'no-preference',
) {
  await page.setViewportSize({ width: 1366, height: 768 })
  await page.emulateMedia({ reducedMotion })
  await page.addInitScript(() => {
    localStorage.setItem('pipewright-locale', 'zh-CN')
    localStorage.setItem('pipewright-theme', 'light')
  })
  await page.route(
    (url) => url.pathname.startsWith('/api/'),
    (route) => {
      const path = new URL(route.request().url()).pathname
      const providers = [
        'github',
        'gitlab',
        'gitee',
        'google',
        'microsoft',
      ].map((provider) => ({
        provider,
        visible: false,
        available: false,
        reason: 'not_configured',
      }))
      return route.fulfill({
        status: path.endsWith('/session') ? 401 : 200,
        contentType: 'application/json',
        body: JSON.stringify(path.endsWith('/providers') ? { providers } : {}),
      })
    },
  )
  await page.goto('/login')
  await expect
    .poll(() =>
      page
        .locator('.showcase-grid canvas')
        .evaluate((canvas) => (canvas as HTMLCanvasElement).width),
    )
    .toBeGreaterThan(0)
}

async function snapshot(page: Page) {
  return page
    .locator('.showcase-grid canvas')
    .evaluate((canvas) => (canvas as HTMLCanvasElement).toDataURL())
}

test.describe('Login corner canvas', () => {
  test('paints the left-hand corner grid, leaves the content clear and animates', async ({
    page,
  }) => {
    const errors: string[] = []
    page.on('pageerror', (error) => errors.push(error.message))
    await prepare(page)
    const canvas = page.locator('.showcase-grid canvas')
    const pixels = await canvas.evaluate((element) => {
      const canvas = element as HTMLCanvasElement
      const context = canvas.getContext('2d')!
      const canvasBox = canvas.getBoundingClientRect()
      const contentBox = document
        .querySelector('.showcase-content')!
        .getBoundingClientRect()
      const scale = canvas.width / canvasBox.width
      function count(x: number, y: number, width: number, height: number) {
        const data = context.getImageData(
          Math.round(x * scale),
          Math.round(y * scale),
          Math.max(1, Math.floor(width * scale)),
          Math.max(1, Math.floor(height * scale)),
        ).data
        let count = 0
        for (let i = 3; i < data.length; i += 4) if (data[i]) count++
        return count
      }
      return {
        topLeft: count(0, 0, 128, 128),
        bottomRight: count(
          canvasBox.width - 128,
          canvasBox.height - 128,
          128,
          128,
        ),
        content: count(
          contentBox.x - canvasBox.x + 8,
          contentBox.y - canvasBox.y + 8,
          contentBox.width - 16,
          contentBox.height - 16,
        ),
      }
    })
    expect(pixels.topLeft).toBeGreaterThan(0)
    expect(pixels.bottomRight).toBeGreaterThan(0)
    expect(pixels.content).toBe(0)
    const original = await snapshot(page)
    await expect
      .poll(() => snapshot(page), { timeout: 5000 })
      .not.toBe(original)
    const box = await canvas.boundingBox()
    expect(box!.width / 1366).toBeCloseTo(0.4, 3)
    await expect(page.locator('.login-access canvas')).toHaveCount(0)
    await expect(page.locator('.showcase-grid')).toHaveCSS(
      'pointer-events',
      'none',
    )
    await page.locator('.submit').click()
    await expect(page.locator('#username-err')).toBeVisible()
    expect(errors).toEqual([])
  })

  test('keeps reduced-motion graphics static and changes the stroke color with the theme', async ({
    page,
  }) => {
    await prepare(page, 'reduce')
    const original = await snapshot(page)
    await page.waitForTimeout(850)
    expect(await snapshot(page)).toBe(original)
    const stroke = () =>
      page
        .locator('.showcase-grid canvas')
        .evaluate((c) => (c as HTMLCanvasElement).getContext('2d')!.strokeStyle)
    const light = await stroke()
    await page.locator('.theme-toggle').click()
    await expect.poll(stroke).not.toBe(light)
    const dark = await snapshot(page)
    await page.waitForTimeout(850)
    expect(await snapshot(page)).toBe(dark)
  })

  test('stops on compact screens and restores the grid after returning to desktop', async ({
    page,
  }) => {
    await prepare(page)
    const canvas = page.locator('.showcase-grid canvas')
    for (const width of [1024, 390, 320]) {
      await page.setViewportSize({ width, height: 844 })
      await expect(page.locator('.showcase')).toBeHidden()
      await expect(canvas).toHaveJSProperty('width', 0)
      await expect(page.locator('.login-access-brand')).toBeVisible()
    }
    await page.setViewportSize({ width: 1600, height: 900 })
    await expect(page.locator('.showcase')).toBeVisible()
    await expect
      .poll(() => canvas.evaluate((c) => (c as HTMLCanvasElement).width))
      .toBeGreaterThan(0)
    expect((await canvas.boundingBox())!.width / 1600).toBeCloseTo(0.4, 3)
  })

  test('honors system reduced-motion changes without reloading', async ({
    page,
  }) => {
    await prepare(page)
    await page.emulateMedia({ reducedMotion: 'reduce' })
    await page.waitForTimeout(250)
    const still = await snapshot(page)
    await page.waitForTimeout(850)
    expect(await snapshot(page)).toBe(still)
    await page.emulateMedia({ reducedMotion: 'no-preference' })
    await expect.poll(() => snapshot(page), { timeout: 5000 }).not.toBe(still)
  })
})
