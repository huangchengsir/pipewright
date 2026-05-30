import { test, expect } from '@playwright/test'
import { stubLoggedIn, stubEnvelopeDefaults } from './_stubs'

/**
 * Settings surfaces: credential vault (masked list + add form), AI provider
 * config (provider radiogroup), and the target-server registry incl. the
 * container-terminal entry (xterm + WS is degraded to "modal appears").
 *
 * DTO shapes follow the frozen contracts: Credential.maskedValue (never
 * plaintext), AISettings.apiKeyMasked, Server { items } envelope.
 */

function fulfillJson(body: unknown) {
  return { status: 200, contentType: 'application/json', body: JSON.stringify(body) }
}

const SSH_CRED = {
  id: 'c-ssh',
  name: 'prod-ssh',
  type: 'ssh_key',
  scope: '*',
  maskedValue: 'ssh-ed25519 ••••9f2a',
  lastUsedAt: null,
  createdAt: '2026-05-01T00:00:00Z',
}

test.describe('Settings · credential vault', () => {
  test('renders masked credentials and never exposes plaintext', async ({ page }) => {
    await stubLoggedIn(page)
    await page.route(
      (u) => u.pathname === '/api/credentials',
      (route) => {
        if (route.request().method() !== 'GET') return route.fallback()
        return route.fulfill(
          fulfillJson([
            { id: 'c-git', name: 'gitee-token', type: 'git_token', scope: '*', maskedValue: 'ghp_••••a91f', lastUsedAt: null, createdAt: '2026-05-01T00:00:00Z' },
            SSH_CRED,
          ]),
        )
      },
    )
    await page.goto('/settings/vault')

    await expect(page.getByRole('heading', { name: '凭据保险库' })).toBeVisible()
    await expect(page.getByText('gitee-token')).toBeVisible()
    await expect(page.getByText('ghp_••••a91f')).toBeVisible()
  })

  test('add-credential modal opens with the type segmented control', async ({ page }) => {
    await stubLoggedIn(page)
    await page.goto('/settings/vault')

    await page.getByRole('button', { name: '添加凭据' }).click()
    const dialog = page.getByRole('dialog', { name: '添加凭据' })
    await expect(dialog).toBeVisible()
    // Credential-type segmented group with the three frozen types.
    const types = dialog.getByRole('group', { name: '凭据类型' })
    await expect(types).toBeVisible()
    await expect(dialog.locator('#cred-name')).toBeVisible()
    await expect(dialog.locator('#cred-secret')).toHaveAttribute('type', 'password')
  })
})

test.describe('Settings · AI provider', () => {
  test('renders the provider radiogroup from GET /api/settings/ai', async ({ page }) => {
    await stubLoggedIn(page)
    await page.route(
      (u) => u.pathname === '/api/settings/ai',
      (route) => {
        if (route.request().method() !== 'GET') return route.fallback()
        return route.fulfill(
          fulfillJson({
            configured: false,
            enabled: false,
            provider: '',
            baseUrl: '',
            model: '',
            apiKeyMasked: '',
            budget: { monthlyTokenLimit: null },
            updatedAt: null,
          }),
        )
      },
    )
    await page.goto('/settings/ai')

    await expect(page.getByRole('heading', { name: 'AI 提供商' })).toBeVisible()
    const radios = page.getByRole('radiogroup', { name: 'AI 提供商选择' })
    await expect(radios).toBeVisible()
    await expect(radios.getByLabel('选择 Claude')).toBeVisible()
    await expect(radios.getByLabel('选择 OpenAI')).toBeVisible()
    await expect(radios.getByLabel('选择 Ollama')).toBeVisible()
  })

  test('selecting a provider reveals the config detail panel', async ({ page }) => {
    await stubLoggedIn(page)
    await page.route(
      (u) => u.pathname === '/api/settings/ai',
      (route) => {
        if (route.request().method() !== 'GET') return route.fallback()
        return route.fulfill(
          fulfillJson({
            configured: false, enabled: false, provider: '', baseUrl: '', model: '',
            apiKeyMasked: '', budget: { monthlyTokenLimit: null }, updatedAt: null,
          }),
        )
      },
    )
    await page.goto('/settings/ai')

    await page.getByRole('radiogroup', { name: 'AI 提供商选择' }).getByLabel('选择 Claude').click()
    // baseUrl gets auto-filled to the Claude default once a provider is chosen.
    await expect(page.locator('#ai-baseurl')).toHaveValue('https://api.anthropic.com')
  })
})

test.describe('Settings · servers + container terminal', () => {
  test('lists a registered server and opens the container-terminal modal (degraded: modal visible)', async ({ page }) => {
    await stubLoggedIn(page)
    await stubEnvelopeDefaults(page)
    // Needs an SSH credential present (gate) + one registered server.
    await page.route(
      (u) => u.pathname === '/api/credentials',
      (route) => {
        if (route.request().method() !== 'GET') return route.fallback()
        return route.fulfill(fulfillJson([SSH_CRED]))
      },
    )
    await page.route(
      (u) => u.pathname === '/api/servers',
      (route) => {
        if (route.request().method() !== 'GET') return route.fallback()
        return route.fulfill(
          fulfillJson({
            items: [
              {
                id: 'srv-1',
                name: 'web-prod-1',
                host: '10.0.0.5',
                port: 22,
                user: 'deploy',
                credentialId: 'c-ssh',
                credentialName: 'prod-ssh',
                createdAt: '2026-05-01T00:00:00Z',
                updatedAt: '2026-05-01T00:00:00Z',
              },
            ],
          }),
        )
      },
    )
    await page.goto('/settings/servers')

    await expect(page.getByRole('heading', { name: '服务器', exact: true })).toBeVisible()
    await expect(page.getByText('web-prod-1')).toBeVisible()

    // Container terminal entry — xterm + WebSocket are not driven here; we only
    // assert the modal surface appears (component-dependency degrade).
    await page.getByRole('button', { name: '终端' }).click()
    await expect(page.getByText(/容器终端 · web-prod-1/)).toBeVisible()
  })
})
