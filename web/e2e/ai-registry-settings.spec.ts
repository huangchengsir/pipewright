import { test, expect, type Page, type Route } from '@playwright/test'
import { stubLoggedIn, stubEnvelopeDefaults } from './_stubs'

const json = (r: Route, body: unknown) =>
  r.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify(body),
  })
async function setup(page: Page, theme: string) {
  await page.addInitScript((theme) => {
    localStorage.setItem('pipewright-locale', 'zh-CN')
    localStorage.setItem('pipewright-theme', theme)
  }, theme)
  await stubLoggedIn(page)
  await stubEnvelopeDefaults(page)
}
for (const [width, theme] of [
  [1366, 'light'],
  [390, 'dark'],
] as const) {
  test(`AI models draft and manual fallback ${width} ${theme}`, async ({
    page,
  }, info) => {
    await page.setViewportSize({ width, height: 900 })
    await setup(page, theme)
    let fetches = 0
    let saved: Record<string, unknown> | undefined
    const config = {
      provider: 'openai',
      baseUrl: 'https://company.example/api/v1',
      model: 'manual/model',
      configured: true,
      enabled: true,
      apiKeyMasked: 'masked',
      budget: { monthlyTokenLimit: null },
      updatedAt: null,
    }
    await page.route(
      (u) => u.pathname === '/api/settings/ai',
      (r) => {
        if (r.request().method() === 'PUT') {
          saved = r.request().postDataJSON()
          return json(r, { ...config, ...saved, apiKeyMasked: 'masked' })
        }
        return json(r, config)
      },
    )
    await page.route(
      (u) => u.pathname === '/api/settings/ai/models',
      (r) => {
        fetches++
        expect(r.request().postDataJSON()).toEqual({
          provider: 'openai',
          baseUrl: config.baseUrl,
        })
        return json(
          r,
          fetches === 1
            ? {
                ok: true,
                items: [{ id: 'gateway/model:custom', name: 'Company model' }],
              }
            : { ok: false, items: [], error: 'Model endpoint unavailable' },
        )
      },
    )
    await page.goto('/settings/ai')
    await expect(page.locator('#ai-model')).toHaveValue('manual/model')
    await expect.poll(() => fetches).toBe(1)
    await page.locator('.protocol-help-button').focus()
    await expect(page.getByRole('tooltip')).toBeVisible()
    const tipBounds = await page.getByRole('tooltip').boundingBox()
    expect(tipBounds!.x).toBeGreaterThanOrEqual(0)
    expect(tipBounds!.x + tipBounds!.width).toBeLessThanOrEqual(width)
    await page.locator('.protocol-help-button').press('Escape')
    await page.getByRole('combobox', { name: '快捷配置', exact: true }).click()
    await page
      .getByRole('option', { name: '自定义网关', exact: true })
      .scrollIntoViewIfNeeded()
    await expect(
      page.getByRole('option', { name: '自定义网关', exact: true }),
    ).toBeVisible()
    const bounds = await page.locator('.app-select__menu--portal').boundingBox()
    expect(bounds!.y + bounds!.height).toBeLessThanOrEqual(900)
    expect(
      await page
        .getByRole('option', { name: '自定义网关', exact: true })
        .evaluate((el) => {
          const rect = el.getBoundingClientRect()
          return el.contains(
            document.elementFromPoint(rect.x + 10, rect.y + rect.height / 2),
          )
        }),
    ).toBe(true)
    await page.screenshot({
      path: info.outputPath('ai-preset-menu.png'),
      fullPage: true,
    })
    await page.getByRole('option', { name: '自定义网关', exact: true }).click()
    await page.getByRole('combobox', { name: '可用模型', exact: true }).click()
    await page
      .getByRole('searchbox', { name: '搜索模型', exact: true })
      .fill('custom')
    await page
      .getByRole('option')
      .filter({ hasText: 'gateway/model:custom' })
      .click()
    await expect(page.locator('#ai-model')).toHaveValue('gateway/model:custom')
    await page.getByRole('button', { name: '保存更改', exact: true }).click()
    await expect.poll(() => saved?.model).toBe('gateway/model:custom')
    await page.getByRole('button', { name: '获取模型', exact: true }).click()
    await expect(page.getByText('Model endpoint unavailable')).toBeVisible()
    await expect(page.locator('#ai-model')).toHaveValue('gateway/model:custom')
    expect(
      await page
        .locator('.ai-root')
        .evaluate((el) => el.scrollWidth <= el.clientWidth + 1),
    ).toBe(true)
    await page.screenshot({
      path: info.outputPath('ai-models.png'),
      fullPage: true,
    })
  })
  test(`Compatible vendor presets ${width} ${theme}`, async ({
    page,
  }, info) => {
    await page.setViewportSize({ width, height: 900 })
    await setup(page, theme)
    await page.route(
      (u) => u.pathname === '/api/settings/ai',
      (r) =>
        json(r, {
          provider: '',
          baseUrl: '',
          model: '',
          configured: false,
          enabled: false,
          apiKeyMasked: '',
          budget: { monthlyTokenLimit: null },
          updatedAt: null,
        }),
    )
    const requests: Record<string, unknown>[] = []
    await page.route(
      (u) => u.pathname === '/api/settings/ai/models',
      (r) => {
        requests.push(r.request().postDataJSON())
        return json(r, {
          ok: true,
          items: [{ id: 'compatible/model:1', name: 'Model' }],
        })
      },
    )
    await page.goto('/settings/ai')
    let count = 0
    for (const [name, url] of [
      ['Gemini', 'https://generativelanguage.googleapis.com/v1beta/openai'],
      ['GLM', 'https://open.bigmodel.cn/api/paas/v4'],
      ['Kimi', 'https://api.moonshot.cn/v1'],
      ['Xiaomi MiMo', 'https://api.xiaomimimo.com/v1'],
    ]) {
      await page
        .getByRole('combobox', { name: '快捷配置', exact: true })
        .click()
      await page.getByRole('option', { name, exact: true }).click()
      await expect(page.locator('#ai-baseurl')).toHaveValue(url!)
      await expect(page.locator('#ai-apikey')).toHaveValue('')
      await page.locator('#ai-apikey').fill('mock-vendor-key')
      await expect.poll(() => requests.length).toBe(++count)
      expect(requests[count - 1]).toEqual({
        provider: 'openai',
        baseUrl: url,
        apiKey: 'mock-vendor-key',
      })
      await page
        .getByRole('combobox', { name: '可用模型', exact: true })
        .click()
      await page
        .getByRole('option')
        .filter({ hasText: 'compatible/model:1' })
        .click()
      await expect(page.locator('#ai-model')).toHaveValue('compatible/model:1')
    }
    expect(
      await page
        .locator('.ai-root')
        .evaluate((el) => el.scrollWidth <= el.clientWidth + 1),
    ).toBe(true)
    await page.screenshot({
      path: info.outputPath('compatible-vendor-presets.png'),
      fullPage: true,
    })
  })
  test(`Registry separate fields and masked save ${width} ${theme}`, async ({
    page,
  }, info) => {
    await page.setViewportSize({ width, height: 900 })
    await setup(page, theme)
    let payload: Record<string, unknown> | undefined
    const credentials: Record<string, unknown>[] = []
    const passwords = new Map<string, string>()
    await page.route(
      (u) => u.pathname === '/api/credentials',
      (r) => {
        if (r.request().method() === 'POST') {
          payload = r.request().postDataJSON()
          const c = {
            id: 'registry-' + credentials.length,
            name: String(payload!.name),
            type: 'registry',
            username: String(payload!.username),
            scope: '',
            maskedValue: payload!.username
              ? payload!.username + ' ••••'
              : '••••',
            createdAt: new Date().toISOString(),
            lastUsedAt: null,
          }
          credentials.push(c)
          passwords.set(c.id, String(payload!.secret))
          return r.fulfill({
            status: 201,
            contentType: 'application/json',
            body: JSON.stringify(c),
          })
        }
        return json(r, credentials)
      },
    )
    let edit: Record<string, unknown> | undefined
    await page.route(
      (u) => /^\/api\/credentials\/registry-/.test(u.pathname),
      (r) => {
        edit = r.request().postDataJSON()
        const c = credentials.find((c) =>
          r.request().url().endsWith(String(c.id)),
        )!
        Object.assign(c, edit, {
          maskedValue: edit!.username ? edit!.username + ' ••••' : '••••',
        })
        if (edit!.secret) passwords.set(String(c.id), String(edit!.secret))
        return json(r, c)
      },
    )
    await page.goto('/settings/vault')
    await page.getByRole('button', { name: '添加凭据', exact: true }).click()
    await page.locator('.seg-item').filter({ hasText: '镜像' }).click()
    await page.locator('#cred-name').fill('Nexus')
    await page.locator('#cred-username').fill('alice')
    await page.locator('#cred-secret').fill(' p:a:ss ')
    await expect(page.locator('#cred-secret')).toHaveAttribute(
      'type',
      'password',
    )
    expect(
      await page
        .locator('.modal-form')
        .evaluate((el) => el.scrollWidth <= el.clientWidth + 1),
    ).toBe(true)
    await page.screenshot({
      path: info.outputPath('registry-fields.png'),
      animations: 'disabled',
      fullPage: true,
    })
    await page.locator('.modal-form button[type="submit"]').click()
    await expect.poll(() => payload?.secret).toBe(' p:a:ss ')
    expect(payload?.username).toBe('alice')
    await expect(page.locator('#cred-secret')).toHaveCount(0)
    await expect(page.getByText('alice ••••')).toBeVisible()
    expect(await page.locator('body').innerText()).not.toContain('p:a:ss')
    await page
      .getByRole('button', { name: '编辑凭据 Nexus', exact: true })
      .click()
    await expect(page.locator('#cred-username')).toHaveValue('alice')
    await expect(page.locator('#cred-secret')).toHaveValue('')
    await page.locator('#cred-username').fill('bob')
    await page.screenshot({
      path: info.outputPath('registry-edit.png'),
      animations: 'disabled',
      fullPage: true,
    })
    await page.locator('.modal-form button[type="submit"]').click()
    await expect(page.getByText('bob ••••')).toBeVisible()
    expect(edit?.secret).toBeUndefined()
    expect(edit?.username).toBe('bob')
    expect(passwords.get('registry-0')).toBe(' p:a:ss ')
    await page.getByRole('button', { name: '添加凭据', exact: true }).click()
    await page.locator('.seg-item').filter({ hasText: '镜像' }).click()
    await page.locator('#cred-name').fill('Legacy token')
    await page.locator('#cred-secret').fill('legacy-token-value')
    await page.locator('.modal-form button[type="submit"]').click()
    await expect(
      page.getByRole('button', { name: '编辑凭据 Legacy token', exact: true }),
    ).toBeVisible()
    expect(payload?.username).toBe('')
    expect(passwords.get('registry-1')).toBe('legacy-token-value')
    await page
      .getByRole('button', { name: '编辑凭据 Legacy token', exact: true })
      .click()
    await expect(page.locator('#cred-username')).toHaveValue('')
    await expect(page.locator('#cred-secret')).toHaveValue('')
    await page.screenshot({
      path: info.outputPath('registry-legacy-token.png'),
      animations: 'disabled',
      fullPage: true,
    })
    await page.locator('.modal-form button[type="submit"]').click()
    await expect(page.locator('#cred-secret')).toHaveCount(0)
    expect(edit?.secret).toBeUndefined()
    expect(passwords.get('registry-1')).toBe('legacy-token-value')
  })
}
