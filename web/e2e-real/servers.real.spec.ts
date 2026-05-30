import { test, expect, type Page } from '@playwright/test'

// servers.real.spec.ts —— 真全栈 e2e:从 UI 登记服务器 + 测试真 SSH 连通。
// 需真 sshd 容器(harness 起的 alpine/centos+sshd,映射端口经 PW_SSH_PORT 透传)+
// vault 里一条 type=ssh_key 的凭据(harness 经 API 建,私钥注入容器 authorized_keys,
// 凭据名经 PW_SSH_CRED 透传)。登录 → /settings/servers → 登记服务器(host=127.0.0.1 /
// port=容器端口 / user=root / 选该 SSH 凭据)→ 保存 → 列表出现 → 点「测试连接」→ 断言真 SSH
// 握手 + 远端 `uname -a` 输出回显「连接成功」。打真二进制,不 mock。
//
// 无容器环境(PW_SERVERS 未置)→ 自动 skip,不挂红。

const ADMIN_PW = process.env.PW_ADMIN_PW || 'fs-admin-pw'
const SERVERS_ON = process.env.PW_SERVERS === '1'
const SSH_PORT = process.env.PW_SSH_PORT || ''
const SSH_CRED = process.env.PW_SSH_CRED || ''

async function login(page: Page): Promise<void> {
  await page.goto('/login')
  await page.locator('#username').fill('admin')
  await page.locator('#password').fill(ADMIN_PW)
  await page.getByRole('button', { name: '登录' }).click()
  await expect(page).not.toHaveURL(/\/login/, { timeout: 20_000 })
}

test.describe('真 SSH 容器:从 UI 登记服务器 → 测试连接', () => {
  test.skip(!SERVERS_ON, '需 PW_SERVERS=1(harness 起了 sshd 容器 + 建了 SSH 凭据)')

  test('登记 127.0.0.1:容器端口 / root / SSH 凭据 → 列表出现 → 测试连接真握手成功', async ({ page }) => {
    await login(page)
    await page.goto('/settings/servers')
    await expect(page.getByRole('heading', { name: '服务器' })).toBeVisible()

    // 1. 打开登记模态(按钮文案「登记服务器」)。
    await page.getByRole('button', { name: '登记服务器' }).click()
    const dialog = page.getByRole('dialog', { name: '登记服务器' })
    await expect(dialog).toBeVisible()

    // 2. 填表单:唯一名 + host 127.0.0.1 + 容器映射端口 + root + 选 harness 建的 SSH 凭据。
    const serverName = `e2e-ssh-${Date.now()}`
    await dialog.locator('input[type="text"]').first().fill(serverName) // 名称
    await dialog.locator('input[placeholder="10.0.0.5"]').fill('127.0.0.1') // 主机
    await dialog.locator('input[type="number"]').fill(SSH_PORT) // 端口
    await dialog.locator('input[placeholder="deploy"]').fill('root') // 登录用户
    await dialog.locator('select').selectOption({ label: SSH_CRED }) // SSH 凭据

    // 3. 保存 → 真 POST /api/servers。
    await dialog.getByRole('button', { name: '保存' }).click()
    await expect(dialog).toBeHidden({ timeout: 15_000 })

    // 4. 列表出现该服务器(真后端写库后回读)。
    await expect(page.getByText(serverName)).toBeVisible({ timeout: 15_000 })
    // 定位该服务器行(harness 可能已登记同端口的部署目标,故按唯一名取行,避免端口串扰)。
    const row = page.locator('li.server-row', { hasText: serverName })
    // 地址回显(user@host:port)——在本行内断言。
    await expect(row.getByText(`root@127.0.0.1:${SSH_PORT}`)).toBeVisible()

    // 5. 点「测试连接」→ 真后端经 vault 取私钥 → 真 SSH 握手 → 远端 uname -a。
    await row.getByRole('button', { name: '测试连接' }).click()

    // 真握手成功 → 行内出现「连接成功」+ uname 输出(给足 SSH 拨号时间)。
    await expect(row.getByText(/连接成功/)).toBeVisible({ timeout: 30_000 })
  })
})
