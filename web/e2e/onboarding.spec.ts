import { test, expect, type Page, type Route } from '@playwright/test'
import { stubLoggedIn, stubEnvelopeDefaults, projectFixture, runDetailFixture } from './_stubs'
import type { Snapshot, ExecutionMode } from '../src/api/onboarding'
import type { PipelineDTO } from '../src/api/pipeline'

const stamp = '2026-10-05T00:00:00Z'
function status(overrides: Partial<Snapshot> = {}): Snapshot {
  return { projectsState: 'known', projectCount: 1, projects: [{ id: 'a', name: 'Project A' }, { id: 'b', name: 'Project B' }],
    runsState: 'known', runCount: 0, selectedProject: { id: 'a', name: 'Project A', pacEnabled: false }, successState: 'known', success: null,
    latestRun: null, pipeline: { state: 'ready', savedAt: stamp, issues: [] }, runtime: 'available', ...overrides }
}
function run(mode: ExecutionMode = 'real', projectId = 'b') {
  return { id: `r-${projectId}`, projectId, projectName: `Project ${projectId.toUpperCase()}`, status: 'success', executionMode: mode, createdAt: stamp, projectExists: true }
}
const json = (route: Route, body: unknown, code = 200) => route.fulfill({ status: code, contentType: 'application/json', body: JSON.stringify(body) })

async function fixture(page: Page, initial = status()) {
  const state = { snapshot: initial as unknown, fail: false, projects: [projectFixture({ id: 'a', name: 'Project A', credentialId: 'c' }), projectFixture({ id: 'b', name: 'Project B', credentialId: 'c' })], authenticated: true }
  const apiRequests: { path: string; method: string }[] = []
  const external: string[] = []
  let origin = ''
  await page.addInitScript(() => localStorage.setItem('pipewright-locale', 'zh-CN'))
  await stubLoggedIn(page)
  await stubEnvelopeDefaults(page)
  await page.route(u => u.pathname === '/api/auth/session', r => json(r, state.authenticated ? { username: 'admin' } : { error: { code: 'unauthorized', message: 'Unauthorized' } }, state.authenticated ? 200 : 401))
  await page.route(u => u.pathname === '/api/auth/login', r => { state.authenticated = true; return json(r, { username: 'admin' }) })
  await page.route(u => u.pathname === '/api/onboarding/status', r => {
    if (state.fail) return json(r, { error: { code: 'internal', message: 'SECRET_BACKEND_LOG' } }, 500)
    const data = state.snapshot
    if (typeof data === 'object' && data !== null && !Array.isArray(data)) {
      const snapshot = data as Snapshot
      const preferred = new URL(r.request().url()).searchParams.get('projectId')
      const selected = snapshot.projects.find(p => p.id === preferred)
      if (selected) return json(r, { ...snapshot, selectedProject: { ...selected, pacEnabled: snapshot.selectedProject?.pacEnabled ?? false } })
    }
    return json(r, data)
  })
  await page.route(u => u.pathname === '/api/projects', r => {
    if (r.request().method() === 'POST') {
      const created = projectFixture({ id: 'new', name: 'New project', credentialId: 'c', lastRunStatus: null })
      state.projects.unshift(created)
      state.snapshot = status({ projects: [{ id: 'new', name: 'New project' }], selectedProject: { id: 'new', name: 'New project', pacEnabled: false }, pipeline: { state: 'absent', savedAt: null, issues: [] } })
      return json(r, created)
    }
    return json(r, state.projects)
  })
  await page.route(u => u.pathname === '/api/credentials', r => json(r, [{ id: 'c', name: 'Repository token', type: 'git_token', scope: '', maskedValue: 'masked', createdAt: stamp }]))
  await page.route(u => /\/parameters$/.test(u.pathname), r => json(r, []))
  await page.route(u => /^\/api\/runs\/r-[ab]$/.test(u.pathname), r => json(r, runDetailFixture({ id: new URL(r.request().url()).pathname.split('/').at(-1), projectId: 'b', projectName: 'Project B' })))
  await page.route(u => u.pathname === '/api/account/sessions', r => json(r, []))
  page.on('request', request => {
    const url = new URL(request.url())
    if (url.pathname.startsWith('/api/')) apiRequests.push({ path: url.pathname, method: request.method() })
    if (origin && /^https?:$/.test(url.protocol) && url.origin !== origin) external.push(request.url())
  })
  // Registered last: all routes, including API fixtures, first pass this guard.
  await page.route('**/*', async route => {
    const url = new URL(route.request().url())
    if (!origin && route.request().isNavigationRequest()) origin = url.origin
    if (origin && /^https?:$/.test(url.protocol) && url.origin !== origin) {
      if (!external.includes(url.href)) external.push(url.href)
      await route.abort('blockedbyclient')
    } else await route.fallback()
  })
  return { state, apiRequests, external, async assertOffline() {
    expect(origin).toBe(await page.evaluate(() => window.location.origin))
    expect(external).toEqual([])
  } }
}
const primary = (page: Page) => page.getByTestId('onboarding-primary')

function readyPipeline(): PipelineDTO {
  return { stages: [
    { id: 'source', name: 'Source', kind: 'source', jobs: [{ id: 'src', name: 'Source', type: 'git_source', summary: '', config: {} }] },
    { id: 'build', name: 'Build', kind: 'build', jobs: [{ id: 'script', name: 'Script', type: 'script', summary: '', config: { image: 'alpine', commands: 'true' } }] },
  ], yaml: '', status: 'draft', updatedAt: stamp }
}

test('YAML preview preserves persisted baseline until an explicit save', async ({ page }) => {
  const f = await fixture(page)
  let persisted = readyPipeline()
  const preview = readyPipeline()
  preview.stages[1]!.jobs[0]!.config.commands = 'echo changed'
  await page.route(u => u.pathname === '/api/projects/a/pipeline', r => {
    if (r.request().method() === 'PUT') persisted = { ...persisted, stages: r.request().postDataJSON().stages }
    return json(r, persisted)
  })
  await page.route(u => u.pathname === '/api/projects/a/pipeline/import', r => {
    expect(r.request().postDataJSON().save).toBe(false)
    return json(r, preview)
  })
  await page.goto('/projects/a/pipeline?onboardingGuide=1')
  const guide = page.getByTestId('pipeline-guide')
  await expect(guide).toHaveAttribute('data-phase', 'ready')
  await page.getByRole('button', { name: '从 YAML 导入', exact: true }).click()
  await page.locator('#yi-yaml').fill('version: 1\nstages: []')
  await page.getByRole('button', { name: '导入到画布', exact: true }).click()
  await expect(guide).toHaveAttribute('data-phase', 'configure')
  await expect(page.getByTestId('guide-run')).toHaveCount(0)
  expect(persisted.stages[1]!.jobs[0]!.config.commands).toBe('true')
  expect(f.apiRequests.filter(r => r.method === 'PUT')).toEqual([])
  await page.locator('[data-onboarding-target="save"]').click()
  await expect(guide).toHaveAttribute('data-phase', 'ready')
  expect(persisted.stages[1]!.jobs[0]!.config.commands).toBe('echo changed')
  expect(f.apiRequests.filter(r => r.method === 'PUT')).toHaveLength(1)
  await f.assertOffline()
})

test('PAC toggle refreshes guide evidence without fetching repository configuration', async ({ page }) => {
  const f = await fixture(page)
  await page.route(u => u.pathname === '/api/projects/a/pipeline', r => json(r, readyPipeline()))
  await page.route(u => u.pathname === '/api/projects/a', r => {
    const enabled = r.request().postDataJSON().pacEnabled
    const updated = projectFixture({ id: 'a', name: 'Project A', credentialId: 'c', pacEnabled: enabled })
    f.state.projects[0] = updated
    f.state.snapshot = status({ selectedProject: { id: 'a', name: 'Project A', pacEnabled: enabled },
      pipeline: { state: enabled ? 'repository' : 'ready', savedAt: stamp, issues: [] } })
    return json(r, updated)
  })
  await page.goto('/projects/a/pipeline?onboardingGuide=1')
  const guide = page.getByTestId('pipeline-guide')
  await expect(guide).toHaveAttribute('data-phase', 'ready')
  const toggle = page.getByRole('switch', { name: '流水线即代码', exact: true })
  await toggle.click()
  await expect(guide).toHaveAttribute('data-phase', 'repository')
  await expect(page.getByTestId('guide-run')).toHaveCount(0)
  await toggle.click()
  await expect(guide).toHaveAttribute('data-phase', 'ready')
  expect(f.apiRequests.filter(r => r.path === '/api/onboarding/status')).toHaveLength(3)
  expect(f.apiRequests.filter(r => /\/(refs|commits|pac-preview)$/.test(r.path))).toEqual([])
  expect(f.apiRequests.filter(r => r.method === 'POST')).toEqual([])
  await f.assertOffline()
})

test('skip leaves a persistent entry and explicit click resumes without forcing login', async ({ page }) => {
  const f = await fixture(page)
  await page.goto('/onboarding')
  await page.getByTestId('onboarding-skip').click()
  await page.goto('/projects')
  await page.reload()
  await expect(page.getByTestId('onboarding-continue')).toBeVisible()
  expect(await page.evaluate(() => localStorage.getItem('onboarding_dismissed'))).toBe('1')
  await page.getByTestId('onboarding-continue').click()
  await expect(page).toHaveURL(/\/onboarding$/)
  expect(await page.evaluate(() => localStorage.getItem('onboarding_dismissed'))).toBeNull()
  await page.goto('/projects')
  await expect(page.getByTestId('onboarding-continue')).toBeVisible()
  expect(f.apiRequests.filter(r => r.method !== 'GET')).toEqual([])
  await f.assertOffline()
})

for (const width of [375, 1440]) {
  for (const outcome of ['ready', 'runtime', 'issues'] as const) {
    test(`guided script creation requires explicit save and checks ${outcome} at ${width}px`, async ({ page }, testInfo) => {
      const f = await fixture(page, status({ pipeline: { state: 'needs_configuration', savedAt: null, issues: [{ code: 'no_tasks', scope: 'canvas' }] } }))
      let draft = { stages: [{ id: 'source', name: 'Source', kind: 'source', jobs: [{ id: 'src-job', name: 'Source', type: 'git_source', summary: '', config: {} }] }], yaml: '', status: 'draft', updatedAt: stamp }
      await page.route(u => u.pathname === '/api/projects/a/pipeline', r => {
        if (r.request().method() === 'PUT') {
          draft = { ...draft, stages: r.request().postDataJSON().stages }
          f.state.snapshot = status({ runtime: outcome === 'runtime' ? 'stub' : 'available', pipeline: {
            state: outcome === 'ready' ? 'ready' : 'needs_configuration', savedAt: stamp,
            issues: outcome === 'runtime' ? [{ code: 'runtime_stub', scope: 'vars' }] : outcome === 'issues' ? [{ code: 'script_incomplete', scope: 'canvas' }] : [],
          } })
        }
        return json(r, draft)
      })
      await page.setViewportSize({ width, height: 1000 })
      await page.goto('/onboarding')
      await primary(page).click()
      const guide = page.getByTestId('pipeline-guide')
      await expect(guide).toHaveAttribute('data-phase', 'stage')
      await page.getByTestId('guide-locate').click()
      await expect(page.locator('[data-onboarding-target="add-stage"]')).toBeFocused()
      await page.locator('[data-onboarding-target="add-stage"]').click()
      await expect(guide).toHaveAttribute('data-phase', 'task')
      await page.getByTestId('guide-locate').click()
      await page.locator('[data-onboarding-target="add-task"]').first().click()
      await expect(page.getByTestId('guide-picker-hint')).toBeVisible()
      await expect(page.locator('[data-job-type="script"]')).toHaveClass(/type-card--guide/)
      await page.locator('[data-job-type="script"]').click()
      await expect(guide).toHaveAttribute('data-phase', 'configure')
      const image = page.locator('.job-drawer [data-config-key="image"] input')
      const commands = page.locator('.job-drawer [data-config-key="commands"] textarea')
      await image.pressSequentially('alpine:3')
      await expect(image).toBeFocused()
      await commands.pressSequentially('echo acceptance')
      await expect(commands).toBeFocused()
      await page.screenshot({ path: testInfo.outputPath(`guide-configure-${width}.png`), fullPage: true })
      await page.getByTestId('guide-review').click()
      await expect(guide).toHaveAttribute('data-phase', 'save')
      expect(f.apiRequests.filter(r => r.method !== 'GET')).toEqual([])
      await page.getByTestId('guide-locate').click()
      await expect(page.locator('[data-onboarding-target="save"]')).toBeFocused()
      await page.locator('[data-onboarding-target="save"]').click()
      await expect(guide).toHaveAttribute('data-phase', outcome)
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
      expect(draft.stages[1]!.jobs[0]!.config).toMatchObject({ image: 'alpine:3', commands: 'echo acceptance' })
      expect(f.apiRequests.filter(r => r.method === 'PUT')).toHaveLength(1)
      expect(f.apiRequests.filter(r => r.method === 'POST')).toEqual([])
      await page.screenshot({ path: testInfo.outputPath(`guide-${outcome}-${width}.png`), fullPage: true })
      if (outcome === 'ready') {
        await page.getByTestId('guide-run').click()
        await expect(page).toHaveURL(/\/projects(?:\?onboardingRun=a)?$/)
        await expect(page.getByRole('dialog')).toBeVisible()
        expect(f.apiRequests.filter(r => r.method === 'POST')).toEqual([])
        expect(f.apiRequests.filter(r => /\/(refs|commits|pac-preview)$/.test(r.path))).toEqual([])
      } else if (outcome === 'issues') {
        await page.locator('.guide-issues a').click()
        await expect(guide).toHaveAttribute('data-phase', 'configure')
        await expect(image).toBeVisible()
      } else await expect(guide).toContainText('演示执行器')
      await f.assertOffline()
    })
  }
}

test('exiting walkthrough retains draft and ordinary editing has no guide status query', async ({ page }) => {
  const f = await fixture(page, status({ pipeline: { state: 'unconfirmed', savedAt: null, issues: [] } }))
  const draft = { stages: [{ id: 'source', name: 'Source', kind: 'source', jobs: [] }], yaml: '', status: 'draft', updatedAt: stamp }
  await page.route(u => u.pathname === '/api/projects/a/pipeline', r => json(r, draft))
  await page.goto('/projects/a/pipeline')
  await expect(page.locator('[data-onboarding-target="add-stage"]')).toBeVisible()
  expect(f.apiRequests.filter(r => r.path === '/api/onboarding/status')).toEqual([])
  await expect(page.getByTestId('pipeline-guide')).toHaveCount(0)
  await page.goto('/projects/a/pipeline?onboardingGuide=1')
  await expect(page.getByTestId('pipeline-guide')).toHaveAttribute('data-phase', 'stage')
  await page.locator('[data-onboarding-target="add-stage"]').click()
  await expect(page.getByTestId('pipeline-guide')).toHaveAttribute('data-phase', 'task')
  await page.getByRole('button', { name: '退出操作引导', exact: true }).click()
  await expect(page.getByTestId('pipeline-guide')).toHaveCount(0)
  await expect(page.locator('[data-stage-kind="build"]')).toBeVisible()
  await expect(page.locator('.onboarding-target')).toHaveCount(0)
  expect(f.apiRequests.filter(r => r.method !== 'GET')).toEqual([])
  await f.assertOffline()
})

for (const outcome of ['saved-with-issues', 'ready', 'failed'] as const) {
  test(`saving pipeline then returning acknowledges ${outcome} honestly`, async ({ page }) => {
    const f = await fixture(page, status({ pipeline: { state: 'unconfirmed', savedAt: null, issues: [] } }))
    const draft = { stages: [{ id: 'source', name: 'Source', kind: 'source', jobs: [{ id: 'source-job', name: 'Source', type: 'git_source', summary: '', config: {} }] }], yaml: '', status: 'draft', updatedAt: stamp }
    await page.route(u => u.pathname === '/api/projects/a/pipeline', r => {
      if (r.request().method() === 'PUT') {
        if (outcome === 'failed') return json(r, { error: { code: 'internal', message: 'Could not save' } }, 500)
        f.state.snapshot = status({ runtime: outcome === 'ready' ? 'available' : 'stub', pipeline: outcome === 'ready'
          ? { state: 'ready', savedAt: stamp, issues: [] }
          : { state: 'needs_configuration', savedAt: stamp, issues: [{ code: 'no_tasks', scope: 'canvas' }, { code: 'runtime_stub', scope: 'vars' }] } })
      }
      return json(r, draft)
    })
    await page.goto('/onboarding')
    await expect(page.locator('.flow-step').nth(1)).toHaveAttribute('data-state', 'unknown')
    await primary(page).click()
    await expect(page).toHaveURL(/\/projects\/a\/pipeline\?onboardingGuide=1$/)
    await page.getByRole('button', { name: '保存草稿', exact: true }).click()
    await expect(page.locator(outcome === 'failed' ? '.pipeline-banner--error' : '.pipeline-banner--success')).toContainText(outcome === 'failed' ? 'Could not save' : '保存')
    await page.getByTestId('onboarding-continue').click()
    const preparation = page.locator('.flow-step').nth(1)
    await expect(preparation).toHaveAttribute('data-state', outcome === 'failed' ? 'unknown' : outcome === 'ready' ? 'done' : 'saved')
    if (outcome === 'saved-with-issues') {
      await expect(preparation).toContainText('已保存')
      await expect(page.locator('.flow-issues')).toContainText('当前分支没有可执行的实际任务')
      await expect(page.locator('.flow-step').nth(2)).toHaveAttribute('data-state', 'pending')
    }
    expect(f.apiRequests.filter(r => r.method === 'PUT' && r.path.endsWith('/pipeline'))).toHaveLength(1)
    expect(f.apiRequests.filter(r => r.method === 'POST')).toEqual([])
    await f.assertOffline()
  })
}

for (const width of [375, 1440]) {
  test(`create backdrop blocks background and preserves draft at ${width}px`, async ({ page }) => {
    const f = await fixture(page)
    await page.setViewportSize({ width, height: 900 })
    await page.goto('/projects?onboardingCreate=1')
    const dialog = page.getByRole('dialog', { name: '新建项目', exact: true })
    await expect(dialog).toBeVisible()
    await dialog.locator('#proj-name').fill('Keep this draft')
    await dialog.locator('#proj-repo').fill('https://internal.example/draft.git')
    await page.mouse.click(20, 150)
    await expect(dialog).toBeVisible()
    await expect(dialog.locator('#proj-name')).toHaveValue('Keep this draft')
    await expect(dialog.locator('#proj-repo')).toHaveValue('https://internal.example/draft.git')
    await expect(page).toHaveURL(/\/projects$/)
    expect(await page.evaluate(() => document.elementFromPoint(20, 150)?.classList.contains('modal-scrim'))).toBe(true)
    expect(f.apiRequests.filter(r => r.method === 'POST')).toEqual([])
    await dialog.getByRole('button', { name: '取消', exact: true }).click()
    await expect(dialog).toHaveCount(0)
    await page.getByRole('button', { name: '新建项目', exact: true }).click()
    await expect(dialog).toBeVisible()
    await dialog.locator('.modal-close').click()
    await expect(dialog).toHaveCount(0)
    await f.assertOffline()
  })
}

for (const width of [375, 1440]) {
  test(`step detail button stays horizontal with bounded icon at ${width}px`, async ({ page }, testInfo) => {
    const f = await fixture(page)
    await page.setViewportSize({ width, height: 900 })
    await page.goto('/onboarding')
    await page.locator('.step-button').first().click()
    const button = page.locator('.step-detail .app-btn')
    await expect(button).toHaveText('打开此步骤')
    const bounds = await button.evaluate(el => {
      const icon = el.querySelector('svg')!.getBoundingClientRect()
      const label = el.querySelector('span')!.getBoundingClientRect()
      const rect = el.getBoundingClientRect()
      return { iconWidth: icon.width, iconHeight: icon.height, labelHeight: label.height, labelRight: label.right, iconLeft: icon.left, height: rect.height }
    })
    expect(bounds.iconWidth).toBe(18)
    expect(bounds.iconHeight).toBe(18)
    expect(bounds.height).toBe(44)
    expect(bounds.labelHeight).toBeLessThan(30)
    expect(bounds.labelRight).toBeLessThan(bounds.iconLeft)
    await page.screenshot({ path: testInfo.outputPath(`step-detail-${width}.png`), fullPage: true })
    await button.click()
    await expect(page).toHaveURL(/\/projects$/)
    await f.assertOffline()
  })
}

test('global floating entry survives other pages, avoids theme toggle and returns to onboarding', async ({ page }, testInfo) => {
  const f = await fixture(page)
  await page.goto('/onboarding')
  await expect(primary(page)).toBeVisible()
  await expect(page.getByTestId('onboarding-continue')).toHaveCount(0)
  for (const path of ['/dashboard', '/library', '/metrics/dora', '/settings/account']) {
    await page.goto(path)
    const entry = page.getByTestId('onboarding-continue')
    await expect(entry).toBeVisible()
    await expect(page.getByRole('link', { name: '继续上手', exact: true })).toHaveCount(1)
    expect(await entry.evaluate(el => getComputedStyle(el).position)).toBe('fixed')
  }
  await page.setViewportSize({ width: 375, height: 812 })
  await page.goto('/dashboard')
  const entry = page.getByTestId('onboarding-continue')
  await expect(entry).toBeVisible()
  const bounds = await entry.boundingBox()
  const theme = await page.locator('.theme-toggle').boundingBox()
  expect(bounds!.y + bounds!.height).toBeLessThanOrEqual(theme!.y)
  expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(375)
  await page.screenshot({ path: testInfo.outputPath('floating-entry-mobile.png'), fullPage: false })
  await entry.focus(); await entry.press('Enter')
  await expect(page).toHaveURL(/\/onboarding$/)
  await expect(entry).toHaveCount(0)
  await f.assertOffline()
})

test('create, leave, continue retains the chosen project without completing setup', async ({ page }) => {
  const f = await fixture(page, status({ projectCount: 0, projects: [], selectedProject: null }))
  f.state.projects = []
  await page.goto('/onboarding')
  await primary(page).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog).toBeVisible()
  await expect(page).toHaveURL(/\/projects$/)
  await dialog.locator('#proj-name').fill('New project')
  await dialog.locator('#proj-repo').fill('https://internal.example/new.git')
  await dialog.locator('#proj-branch').fill('main')
  await dialog.locator('#proj-cred').click()
  await dialog.getByRole('option').click()
  await dialog.getByRole('button', { name: '创建项目', exact: true }).click()
  await expect(dialog).toBeHidden()
  await page.getByRole('link', { name: '继续上手', exact: true }).click()
  await expect(page.locator('.project-name')).toHaveText('New project')
  await expect(page.locator('.flow-step').nth(0)).toHaveAttribute('data-state', 'done')
  await expect(page.locator('.flow-step').nth(1)).toHaveAttribute('data-state', 'current')
  expect(await page.evaluate(() => localStorage.getItem('onboarding_project_id'))).toBe('new')
  expect(await page.evaluate(() => localStorage.getItem('onboarding_completed'))).toBeNull()
  await f.assertOffline()
})

test('selection, latest failure and subsequent active run keep one project context', async ({ page }) => {
  const f = await fixture(page, status({ selectedProject: { id: 'b', name: 'Project B', pacEnabled: false }, latestRun: { ...run('real'), status: 'failed' } }))
  await page.goto('/onboarding')
  await expect(primary(page)).toHaveText('查看失败日志')
  await primary(page).click()
  await expect(page).toHaveURL(/\/runs\/r-b$/)
  await page.getByRole('link', { name: '继续上手', exact: true }).click()
  await page.getByRole('button', { name: '修改流水线', exact: true }).click()
  await expect(page).toHaveURL(/\/projects\/b\/pipeline/)
  f.state.snapshot = status({ latestRun: { ...run('pending', 'a'), status: 'running' } })
  await page.goto('/onboarding')
  const choice = page.getByRole('combobox', { name: '当前项目' })
  await choice.click()
  await page.getByRole('option', { name: 'Project A', exact: true }).click()
  await expect(primary(page)).toHaveText('查看运行')
  await primary(page).click()
  await expect(page).toHaveURL(/\/runs\/r-a$/)
  await f.assertOffline()
})

for (const mode of ['real', 'legacy_unknown', 'stub', 'mixed', 'pending'] as const) {
  test(`${mode} success has honest authority and project context`, async ({ page }) => {
    const qualified = mode === 'real' || mode === 'legacy_unknown'
    const f = await fixture(page, status({ success: qualified ? run(mode) : null, latestRun: qualified ? { ...run('real', 'a'), status: 'failed' } : run(mode, 'a'),
      projectsState: qualified ? 'unknown' : 'known', pipeline: { state: 'ready', savedAt: stamp, issues: [] } }))
    await page.goto('/onboarding')
    await expect(primary(page)).toBeVisible()
    expect(await page.evaluate(() => localStorage.getItem('onboarding_completed'))).toBe(qualified ? '1' : null)
    if (qualified) {
      await expect(page.locator('.project-name')).toHaveText('Project B')
      await expect(page.getByRole('combobox')).toHaveCount(0)
      await expect(primary(page)).toHaveText('查看运行结果')
      await page.locator('.step-button').nth(1).click()
      await page.getByRole('button', { name: '打开此步骤' }).click()
      await expect(page).toHaveURL(/\/projects\/b\/pipeline/)
    } else {
      await expect(page.locator('.flow-step').nth(2)).not.toHaveAttribute('data-state', 'done')
      await page.getByRole('button', { name: '执行环境帮助', exact: true }).click()
      await expect(page.locator('.runtime-help')).toHaveAttribute('open', '')
    }
    await f.assertOffline()
  })
}

test('query errors clear stale actions, retry recovers and skip remains available', async ({ page }) => {
  const f = await fixture(page)
  f.state.fail = true
  await page.goto('/onboarding')
  await expect(primary(page)).toHaveText('重试')
  await expect(page.getByTestId('onboarding-skip')).toBeVisible()
  await expect(page.getByTestId('onboarding-flow')).not.toContainText('SECRET_BACKEND_LOG')
  f.state.fail = false
  await primary(page).click()
  await expect(primary(page)).toHaveText('前往运行')
  f.state.snapshot = []
  await page.evaluate(() => window.dispatchEvent(new Event('focus')))
  await expect(primary(page)).toHaveText('重试')
  await page.getByTestId('onboarding-skip').click()
  expect(await page.evaluate(() => localStorage.getItem('onboarding_dismissed'))).toBe('1')
  await f.assertOffline()
})

test('skip and account reset control prompts but reread domain evidence', async ({ page }) => {
  const f = await fixture(page)
  await page.goto('/onboarding')
  await page.getByTestId('onboarding-skip').click()
  await page.goto('/projects')
  await expect(page.getByTestId('onboarding-continue')).toBeVisible()
  await page.goto('/settings/account')
  await page.getByRole('button', { name: '重新引导', exact: true }).click()
  await expect(page).toHaveURL(/\/onboarding$/)
  await expect(primary(page)).toHaveText('前往运行')
  expect(await page.evaluate(() => localStorage.getItem('onboarding_dismissed'))).toBeNull()
  await f.assertOffline()
})

test('explicit login redirect wins over empty-instance onboarding', async ({ page }) => {
  const f = await fixture(page, status({ projects: [], projectCount: 0, selectedProject: null }))
  f.state.authenticated = false
  await page.goto('/login?redirect=%2Fruns%2Fr-b')
  await page.locator('#username').fill('admin')
  await page.locator('#password').fill('password')
  await page.getByRole('form', { name: '登录表单' }).locator('button[type="submit"]').click()
  await expect(page).toHaveURL(/\/runs\/r-b$/)
  expect(f.apiRequests.filter(r => r.path === '/api/onboarding/status')).toEqual([])
  await f.assertOffline()
})

test('PAC and onboarding manual dialog never fetch repository suggestions or submit', async ({ page }) => {
  const f = await fixture(page, status({ pipeline: { state: 'repository', savedAt: null, issues: [] }, runtime: 'unknown' }))
  await page.goto('/onboarding')
  await expect(primary(page)).toHaveText('核对流水线')
  await expect(page.locator('.flow-step').nth(1)).toHaveAttribute('data-state', 'unknown')
  await page.getByRole('button', { name: '前往运行', exact: true }).click()
  await expect(page.getByRole('dialog')).toBeVisible()
  await page.getByRole('dialog').locator('input[list*="branch"]').fill('feature/offline')
  await expect(page).toHaveURL(/\/projects$/)
  expect(f.apiRequests.filter(r => /\/(refs|commits|pac\/preview)$/.test(r.path))).toEqual([])
  expect(f.apiRequests.filter(r => r.method === 'POST')).toEqual([])
  await f.assertOffline()
})

test('invalid run query is consumed without a dialog or writes', async ({ page }) => {
  const f = await fixture(page)
  await page.goto('/projects?onboardingRun=deleted&keep=1')
  await expect(page).toHaveURL(/\/projects\?keep=1$/)
  await expect(page.getByRole('dialog')).toHaveCount(0)
  expect(f.apiRequests.filter(r => r.method === 'POST')).toEqual([])
  await f.assertOffline()
})

for (const language of ['en', 'de'] as const) for (const theme of ['light', 'dark'] as const) {
  test(`375px ${language} ${theme} long names, keyboard and bundled-only resources`, async ({ page }, testInfo) => {
    const long = 'LongProjectWithoutBreaks'.repeat(12)
    const f = await fixture(page, status({ projects: [{ id: 'a', name: long }, { id: 'b', name: `${long}B` }], selectedProject: { id: 'a', name: long, pacEnabled: false } }))
    await page.addInitScript(({ language, theme }) => { localStorage.setItem('pipewright-locale', language); localStorage.setItem('pipewright-theme', theme) }, { language, theme })
    await page.setViewportSize({ width: 375, height: 812 })
    await page.goto('/onboarding')
    await expect(primary(page)).toBeVisible()
    const choice = page.getByRole('combobox')
    await choice.focus(); await choice.press('ArrowDown'); await choice.press('ArrowDown'); await choice.press('Enter')
    await expect(page.locator('.project-name')).toHaveText(`${long}B`)
    await expect(page.locator('.project-name')).toHaveAttribute('title', `${long}B`)
    expect(await page.locator('.project-name').evaluate(el => el.getBoundingClientRect().height)).toBeLessThanOrEqual(52)
    expect(await choice.evaluate(el => el.getBoundingClientRect().height)).toBe(44)
    const flow = page.getByTestId('onboarding-flow')
    expect(await flow.evaluate(el => el.scrollWidth <= el.clientWidth)).toBe(true)
    expect(await primary(page).evaluate(el => parseFloat(getComputedStyle(el).fontSize))).toBeGreaterThanOrEqual(16)
    await page.getByTestId('onboarding-skip').scrollIntoViewIfNeeded()
    await expect(page.getByTestId('onboarding-skip')).toBeVisible()
    await page.screenshot({ path: testInfo.outputPath(`onboarding-${language}-${theme}-375.png`), fullPage: true })
    await f.assertOffline()
  })
}
