import { test, expect, type Page } from '@playwright/test'
import { stubLoggedIn, projectFixture } from './_stubs'

async function stubCode(page: Page): Promise<void> {
  await stubLoggedIn(page)
  await page.route((url) => url.pathname === '/api/projects', (route) => route.fulfill({
    json: [projectFixture()],
  }))
  await page.route((url) => url.pathname === '/api/projects/p-acme/source/tree', (route) => {
    const path = new URL(route.request().url()).searchParams.get('path') ?? ''
    const entries = path === 'src'
      ? Array.from({ length: 100 }, (_, i) => ({ name: `file-${i}.ts`, path: `src/file-${i}.ts`, type: 'file' }))
      : [{ name: 'src', path: 'src', type: 'dir' }, { name: 'README.md', path: 'README.md', type: 'file' }]
    return route.fulfill({ json: { ref: 'main', path, entries } })
  })
  await page.route((url) => url.pathname === '/api/projects/p-acme/source/blob', (route) => route.fulfill({
    json: {
      ref: 'main', path: 'README.md', size: 4000, binary: false, truncated: false,
      content: Array.from({ length: 200 }, (_, i) => `Source line ${i + 1}`).join('\n'),
    },
  }))
}

for (const viewport of [
  { width: 1280, height: 720 }, { width: 1920, height: 1080 },
  { width: 390, height: 844 }, { width: 760, height: 720 },
  { width: 761, height: 720 }, { width: 1280, height: 480 },
]) {
  test(`code reader keeps its height at ${viewport.width}x${viewport.height}`, async ({ page }, testInfo) => {
    await page.setViewportSize(viewport)
    await stubCode(page)
    await page.goto('/projects/p-acme/code?path=README.md')
    const reader = page.locator('.code-view')
    await expect(page.locator('.code-monaco .monaco-editor')).toBeVisible()
    await expect(page.locator('.view-line').filter({ hasText: /Source\s+line\s+2$/ })).toBeVisible()
    const before = await reader.boundingBox()
    expect(before!.height).toBeGreaterThanOrEqual(viewport.height < 600 ? 240 : 360)
    const editor = await page.locator('.monaco-editor .overflow-guard').boundingBox()
    expect(editor!.height).toBeGreaterThanOrEqual(before!.height - 50)
    await page.screenshot({ path: testInfo.outputPath('collapsed-directory.png'), fullPage: true })

    await page.getByRole('treeitem', { name: 'src', exact: true }).click()
    await expect(page.getByRole('treeitem', { name: 'file-99.ts', exact: true })).toBeAttached()
    const after = await reader.boundingBox()
    expect(Math.abs(after!.height - before!.height)).toBeLessThan(2)

    const tree = page.locator('.code-tree-body')
    expect(await tree.evaluate((element) => element.scrollHeight > element.clientHeight)).toBe(true)
    await tree.evaluate((element) => { element.scrollTop = element.scrollHeight })
    await expect(page.getByRole('treeitem', { name: 'file-99.ts', exact: true })).toBeInViewport()
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    const split = await page.locator('.code-split').boundingBox()
    expect(after!.x + after!.width).toBeLessThanOrEqual(split!.x + split!.width + 1)

    await page.getByRole('treeitem', { name: 'src', exact: true }).click()
    const collapsed = await reader.boundingBox()
    expect(Math.abs(collapsed!.height - before!.height)).toBeLessThan(2)
    await page.locator('.monaco-editor').scrollIntoViewIfNeeded()
    await page.locator('.view-line').filter({ hasText: /Source\s+line\s+1$/ }).click()
    await page.keyboard.press('Control+End')
    await expect(page.locator('.view-line').filter({ hasText: /Source\s+line\s+200/ })).toBeInViewport()
    await page.screenshot({ path: testInfo.outputPath('last-code-line.png'), fullPage: true })
  })
}

test('onboarding entry leaves space below the code reader', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 720 })
  await page.addInitScript(() => localStorage.setItem('onboarding_dismissed', '1'))
  await stubCode(page)
  await page.goto('/projects/p-acme/code?path=README.md')
  await expect(page.locator('.app-shell')).toHaveClass(/has-onboarding/)
  await expect(page.locator('.monaco-editor')).toBeVisible()
  const reader = await page.locator('.code-view').boundingBox()
  expect(reader!.y + reader!.height).toBeLessThanOrEqual(720 - 132 + 1)
  expect(await page.evaluate(() => document.documentElement.scrollHeight)).toBeLessThanOrEqual(721)
})

test('fallback source text scrolls within the same bounded reader', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 720 })
  await stubCode(page)
  await page.route((url) => url.pathname.includes('/editor.main-') || url.pathname.includes('/monaco-editor'), (route) => route.abort())
  await page.goto('/projects/p-acme/code?path=README.md')
  const fallback = page.locator('.code-fallback')
  await expect(fallback).toBeVisible()
  await fallback.evaluate((element) => { element.scrollTop = element.scrollHeight })
  await expect(page.locator('.code-fallback-line').last()).toBeInViewport()
  const body = await page.locator('.code-view-body').boundingBox()
  const text = await fallback.boundingBox()
  expect(text!.height).toBeLessThanOrEqual(body!.height)
})
