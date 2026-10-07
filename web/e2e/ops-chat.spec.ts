import { test, expect, type Page, type Route } from '@playwright/test'
import { stubLoggedIn, stubEnvelopeDefaults } from './_stubs'
import type {
  Call,
  Entry,
  Run,
  Session,
  Snapshot,
  Tool,
} from '../src/api/opsChat'
const stamp = '2026-10-07T00:00:00Z'
const json = (r: Route, body: unknown, status = 200) =>
  r.fulfill({
    status,
    contentType: 'application/json',
    body: JSON.stringify(body),
  })
const server = (id: string) => ({
  id,
  name: 'Server ' + id.toUpperCase(),
  host: 'mock-' + id,
  port: 22,
  user: 'test',
  credentialId: 'c',
  credentialName: 'test',
  createdAt: stamp,
  updatedAt: stamp,
})
const session = (id: string, targets: string[] = []): Session => ({
  id,
  title: 'Session ' + id,
  revision: 1,
  draft: '',
  serverIds: targets,
  activeRunId: '',
  watermark: 0,
  createdAt: stamp,
  updatedAt: stamp,
})
const snap = (s: Session): Snapshot => ({
  session: s,
  runs: [],
  calls: [],
  confirmations: [],
  entries: { entries: [], cursor: '', watermark: 0, reset: false },
  watermark: 0,
})
const toolIds = [
  'host_resources',
  'host_processes',
  'host_ports',
  'docker_containers',
  'docker_stats',
  'docker_images',
  'docker_inspect',
  'docker_logs',
  'systemd_status',
  'systemd_logs',
  'docker_action',
  'systemd_action',
]
async function fixture(
  page: Page,
  options: { empty?: boolean; theme?: string } = {},
) {
  await page.addInitScript(
    ({ theme }) => {
      localStorage.setItem('pipewright-locale', 'zh-CN')
      localStorage.setItem('pipewright-theme', theme)
      Object.defineProperty(navigator, 'clipboard', {
        configurable: true,
        value: undefined,
      })
      document.execCommand = () => false
    },
    { theme: options.theme ?? 'light' },
  )
  await stubLoggedIn(page)
  await stubEnvelopeDefaults(page)
  const state = {
    db: options.empty
      ? ({} as Record<string, Snapshot>)
      : ({
          chat: snap(session('chat')),
          other: snap(session('other', ['b'])),
        } as Record<string, Snapshot>),
    active: options.empty ? '' : 'chat',
    patchFailure: '',
    holdRuns: false,
    dropSend: false,
    changeProvider: false,
    auth: true,
    next: 0,
    requests: [] as {
      path: string
      method: string
      body: Record<string, unknown> | null
    }[],
    external: [] as string[],
  }
  const catalog: Tool[] = toolIds.map((toolId) => ({
    toolId,
    mutation: toolId.endsWith('_action'),
    schema: {},
  }))
  const inventory = {
    items: ['a', 'b'].map((id) => ({
      serverId: id,
      reachable: true,
      runtime: 'docker',
      error: '',
      containers: [
        {
          id: id.repeat(64),
          names: 'web-' + id,
          image: 'test:local',
          state: 'running',
          status: 'Up',
          ports: '',
          createdAt: stamp,
        },
      ],
      running: 1,
      total: 1,
      collectedAt: stamp,
    })),
  }
  function entry(s: Snapshot, kind: string, text = '', callId?: string): void {
    s.watermark++
    s.session.watermark = s.watermark
    s.entries.entries!.push({
      sessionId: s.session.id,
      seq: s.watermark,
      kind,
      text,
      callId,
      createdAt: stamp,
    })
    s.entries.watermark = s.watermark
  }
  function complete(
    s: Snapshot,
    run: Run,
    toolId = 'host_resources',
    args: Record<string, unknown> = {},
  ): void {
    const calls = (s.session.serverIds ?? []).map((id) => {
      const c: Call = {
        callId: 'call-' + run.id + '-' + id,
        runId: run.id,
        sessionId: s.session.id,
        serverId: id,
        serverName: 'Frozen ' + id.toUpperCase(),
        toolId,
        args,
        object: toolId.endsWith('_action') ? id.repeat(64) : '',
        status: id === 'a' ? 'succeeded' : 'failed',
        argsHash: 'args-' + id,
        targetHash: 'target-' + id,
        collectedAt: stamp,
        exitCode: id === 'a' ? 0 : 1,
        output: id === 'a' ? 'Result on A\n' + 'long-command-'.repeat(40) : '',
        error: id === 'b' ? 'Mock target unavailable' : '',
        truncated: false,
      }
      if (toolId === 'host_resources' && id === 'a')
        c.resources = {
          disks: [
            {
              filesystem: '/dev/mock',
              mount: '/',
              totalBytes: 10737418240,
              usedBytes: 5368709120,
              availableBytes: 5368709120,
              usedPercent: 50,
            },
          ],
          memory: {
            totalBytes: 1073741824,
            usedBytes: 536870912,
            availableBytes: 536870912,
            swapTotalBytes: 0,
            swapUsedBytes: 0,
          },
          load: { one: 0.1, five: 0.2, fifteen: 0.3, uptimeSeconds: 1234 },
          unavailable: [],
        }
      entry(s, 'tool_call', '', c.callId)
      entry(s, 'call_status', '', c.callId)
      return c
    })
    s.calls!.push(...calls)
    run.status = calls.some((c) => c.status === 'failed')
      ? 'partial_failed'
      : 'succeeded'
    s.session.activeRunId = ''
    s.session.revision++
    entry(s, 'run_status')
  }
  await page.route(
    (u) => u.pathname === '/api/servers',
    (r) => json(r, { items: ['a', 'b'].map(server) }),
  )
  await page.route(
    (u) => u.pathname === '/api/servers/containers',
    (r) => json(r, inventory),
  )
  await page.route(
    (u) => u.pathname === '/api/servers/metrics',
    (r) => json(r, { items: [] }),
  )
  await page.route(
    (u) => u.pathname === '/api/credentials',
    (r) =>
      json(r, [
        {
          id: 'c',
          name: 'test',
          type: 'ssh_password',
          maskedValue: '***',
          scope: '*',
          createdAt: stamp,
        },
      ]),
  )
  await page.route(
    (u) =>
      u.pathname.startsWith('/api/servers/') && u.pathname.endsWith('/stats'),
    (r) => json(r, { items: [] }),
  )
  await page.route(
    (u) =>
      u.pathname.startsWith('/api/servers/') && u.pathname.endsWith('/logs'),
    (r) =>
      json(r, {
        source: 'docker',
        target: 'web-a',
        lines: [{ text: 'log history' }],
      }),
  )
  await page.route(
    (u) => u.pathname.startsWith('/api/ai/ops/'),
    async (r) => {
      const u = new URL(r.request().url()),
        path = u.pathname.slice('/api/ai/ops'.length),
        method = r.request().method()
      const body =
        method === 'GET'
          ? null
          : (r.request().postDataJSON() as Record<string, unknown> | null)
      state.requests.push({ path, method, body })
      if (!state.auth)
        return json(
          r,
          { error: { code: 'unauthorized', message: 'raw-secret' } },
          401,
        )
      if (path === '/capabilities')
        return json(r, {
          available: true,
          modelAvailable: true,
          provider: {
            provider: 'mock-provider',
            model: 'mock-model',
            configHash: 'config',
          },
          limits: { targets: 8 },
        })
      if (path === '/tools') return json(r, catalog)
      if (path === '/sessions' && method === 'GET')
        return json(r, {
          sessions: Object.values(state.db).map((s) => s.session),
          activeSessionId: state.active,
          cursor: '',
        })
      if (path === '/sessions' && method === 'POST') {
        const id = 'new' + ++state.next
        const s = session(id, body?.serverIds as string[])
        state.db[id] = snap(s)
        return json(r, s, 201)
      }
      const match = path.match(/^\/sessions\/([^/]+)(.*)$/),
        id = match?.[1] ?? '',
        suffix = match?.[2] ?? '',
        s = state.db[id]
      if (!s)
        return json(
          r,
          { error: { code: 'ops_not_found', message: 'raw-secret' } },
          404,
        )
      if (!suffix && method === 'GET') return json(r, s)
      if (!suffix && method === 'PATCH') {
        if (state.patchFailure) {
          const code = state.patchFailure
          state.patchFailure = ''
          if (code === 'ops_conflict') {
            s.session.revision++
            s.session.draft = 'remote draft'
          }
          return json(
            r,
            { error: { code, message: 'RAW_SECRET' } },
            code === 'ops_conflict' ? 409 : 500,
          )
        }
        if (body?.revision !== s.session.revision)
          return json(
            r,
            { error: { code: 'ops_conflict', message: 'RAW_SECRET' } },
            409,
          )
        Object.assign(s.session, body, { revision: s.session.revision + 1 })
        return json(r, s.session)
      }
      if (!suffix && method === 'DELETE') {
        delete state.db[id]
        state.active = ''
        return r.fulfill({ status: 204 })
      }
      if (suffix === '/activate') {
        state.active = id
        return json(r, { ok: true })
      }
      if (suffix === '/entries')
        return json(r, {
          entries: [],
          cursor: '',
          watermark: s.watermark,
          reset: false,
        })
      if (suffix.startsWith('/calls/') && method === 'GET')
        return json(
          r,
          s.calls!.find((c) => c.callId === suffix.slice('/calls/'.length)),
        )
      if (suffix === '/events') {
        const after = Number(
          (u.searchParams.get('after') ?? '').split(':').at(-1) ?? 0,
        )
        const frames = s.entries
          .entries!.filter((e) => e.seq > after)
          .map(
            (e) =>
              'id: ' +
              id +
              ':' +
              e.seq +
              '\nevent: entry\ndata: ' +
              JSON.stringify(e) +
              '\n\n',
          )
          .join('')
        return r.fulfill({
          contentType: 'text/event-stream',
          body:
            'event: ready\ndata: ' +
            JSON.stringify({ watermark: s.watermark }) +
            '\n\n' +
            frames,
        })
      }
      if (suffix === '/turns') {
        if (state.dropSend) {
          state.dropSend = false
          return r.abort('failed')
        }
        const run: Run = {
          id: 'r' + ++state.next,
          sessionId: id,
          clientRequestId: body!.clientRequestId as string,
          status: 'queued',
          cancelRequested: false,
          createdAt: stamp,
        }
        s.runs!.push(run)
        s.session.activeRunId = run.id
        s.session.revision++
        entry(
          s,
          body?.text ? 'user' : 'tool_request',
          (body?.text as string) ?? '',
        )
        if (body?.toolId?.toString().endsWith('_action')) {
          run.status = 'awaiting_confirmation'
          const calls = (s.session.serverIds ?? []).map((target) => ({
            ...({
              callId: 'call-' + run.id + '-' + target,
              runId: run.id,
              sessionId: id,
              serverId: target,
              serverName: 'Frozen ' + target.toUpperCase(),
              toolId: body.toolId,
              args: body.args,
              object: target.repeat(64),
              status: 'awaiting_confirmation',
              argsHash: 'args-' + target,
              targetHash: 'target-' + target,
              output: '',
              error: '',
              truncated: false,
            } as Call),
          }))
          s.calls!.push(...calls)
          for (const c of calls) entry(s, 'tool_call', '', c.callId)
          s.confirmations = [
            {
              runId: run.id,
              nonce: 'nonce',
              expiresAt: new Date(Date.now() + 600000).toISOString(),
              valid: true,
              calls: calls.map((c) => ({
                callId: c.callId,
                serverId: c.serverId,
                toolId: c.toolId,
                object: c.object,
                argsHash: c.argsHash,
                targetHash: c.targetHash,
              })),
            },
          ]
        } else if (!state.holdRuns) {
          if (body?.text) {
            entry(s, 'assistant', 'Manual advice: docker compose up -d')
            run.status = 'succeeded'
            s.session.activeRunId = ''
            s.session.revision++
          } else
            complete(
              s,
              run,
              body?.toolId as string,
              body?.args as Record<string, unknown>,
            )
        }
        return json(r, run, 202)
      }
      if (suffix === '/retry') {
        const chosen = (body!.callIds as string[]).map(
          (cid) => s.calls!.find((c) => c.callId === cid)!,
        )
        expect(
          chosen.every(
            (c) => c.status === 'failed' || c.status === 'partial_failed',
          ),
        ).toBe(true)
        const run: Run = {
          id: 'retry' + ++state.next,
          sessionId: id,
          clientRequestId: body!.clientRequestId as string,
          status: 'succeeded',
          cancelRequested: false,
          createdAt: stamp,
        }
        s.runs!.push(run)
        for (const old of chosen) {
          const c = {
            ...old,
            callId: 'retry-' + old.callId,
            runId: run.id,
            status: 'succeeded' as const,
            output: 'Retry B succeeded',
            error: '',
            exitCode: 0,
          }
          s.calls!.push(c)
          entry(s, 'tool_call', '', c.callId)
        }
        s.session.revision++
        return json(r, run, 202)
      }
      if (suffix === '/analysis/preview') {
        const callIds = u.searchParams.get('callIds')!.split(',')
        return json(r, {
          callIds,
          items: callIds.map((cid) => {
            const c = s.calls!.find((c) => c.callId === cid)!
            return {
              callId: cid,
              targetAlias: 'target_0',
              toolId: c.toolId,
              output: c.output,
              error: c.error,
              status: c.status,
            }
          }),
          hash: 'preview-hash',
          provider: {
            provider: 'mock-provider',
            model: 'mock-model',
            configHash: 'config',
          },
        })
      }
      if (suffix === '/analysis') {
        if (state.changeProvider)
          return json(
            r,
            { error: { code: 'ops_consent_changed', message: 'RAW_SECRET' } },
            409,
          )
        expect(body?.consent).toBe(true)
        entry(s, 'assistant', 'Analysis of explicitly shared batch')
        return json(
          r,
          {
            id: 'analysis',
            sessionId: id,
            clientRequestId: body!.clientRequestId,
            status: 'succeeded',
            cancelRequested: false,
            createdAt: stamp,
          },
          202,
        )
      }
      if (suffix.endsWith('/confirmation')) {
        s.confirmations![0]!.valid = true
        s.confirmations![0]!.nonce = 'new-nonce'
        s.confirmations![0]!.expiresAt = new Date(
          Date.now() + 600000,
        ).toISOString()
        return json(r, s.confirmations![0])
      }
      if (suffix === '/calls/confirm') {
        expect(body?.calls).toEqual(s.confirmations![0]!.calls)
        expect(body?.nonce).toBe(s.confirmations![0]!.nonce)
        const run = s.runs!.find((run) => run.id === body!.runId)!
        run.status = 'succeeded'
        s.session.activeRunId = ''
        s.session.revision++
        s.confirmations = []
        for (const c of s.calls!.filter((c) => c.runId === run.id)) {
          c.status = 'succeeded'
          c.exitCode = 0
          c.collectedAt = stamp
          entry(s, 'call_status', '', c.callId)
        }
        return json(r, run, 202)
      }
      if (suffix.endsWith('/cancel')) {
        const run = s.runs!.find((run) => run.id === suffix.split('/')[2])!
        run.cancelRequested = true
        run.status = 'interrupted'
        s.session.activeRunId = ''
        s.session.revision++
        s.confirmations = []
        return json(r, run, 202)
      }
      return json(
        r,
        { error: { code: 'ops_invalid', message: 'unknown fixture route' } },
        422,
      )
    },
  )
  const origin = 'http://localhost:5174'
  await page.route('**/*', (r) => {
    const u = new URL(r.request().url())
    if (/^https?:$/.test(u.protocol) && u.origin !== origin) {
      state.external.push(u.href)
      return r.abort('blockedbyclient')
    }
    return r.fallback()
  })
  return { state, complete, entry, inventory }
}
const panel = (page: Page) => page.getByTestId('ops-panel')
const button = (page: Page, name: string) =>
  panel(page).getByRole('button', { name, exact: true })
const launcher = (page: Page) =>
  page.getByRole('button', { name: /^✦? ?AI 助手$/ })
async function open(page: Page): Promise<void> {
  await page.goto('/containers')
  await launcher(page).click()
  await expect(
    panel(page).getByRole('textbox', { name: '输入运维需求' }),
  ).toBeEnabled()
}
async function selectTargets(page: Page): Promise<void> {
  await panel(page).locator('.ops-section details summary').first().click()
  await panel(page)
    .locator('.server-choice')
    .filter({ hasText: 'Server A' })
    .getByRole('checkbox')
    .check()
  await expect(panel(page).locator('.target-summary')).toContainText('Server A')
  await panel(page)
    .locator('.server-choice')
    .filter({ hasText: 'Server B' })
    .getByRole('checkbox')
    .check()
  await expect(panel(page).locator('.target-summary')).toContainText('Server B')
}
async function collect(page: Page): Promise<void> {
  await panel(page).getByText('运维工具', { exact: true }).click()
  await button(page, '采集数据').click()
  await expect(panel(page).locator('[data-call-id]')).toHaveCount(2)
}

test('saved sessions survive hide, reopen and refresh without cancel or model request', async ({
  page,
}) => {
  const f = await fixture(page)
  await open(page)
  await page.keyboard.press('Shift+Tab')
  expect(
    await page.evaluate(() => !!document.activeElement?.closest('.ops-drawer')),
  ).toBe(true)
  const input = panel(page).getByRole('textbox', { name: '输入运维需求' })
  await input.fill('saved locally')
  await button(page, '收起助手').click()
  await expect(panel(page)).toHaveCount(0)
  await launcher(page).click()
  await expect(input).toHaveValue('saved locally')
  await page.reload()
  await launcher(page).click()
  await expect(input).toHaveValue('saved locally')
  expect(
    f.state.requests.some(
      (r) => r.path.endsWith('/turns') || r.path.endsWith('/cancel'),
    ),
  ).toBe(false)
  expect(f.state.external).toEqual([])
})
test('failed save and CAS preserve local text with explicit choices', async ({
  page,
}) => {
  const f = await fixture(page)
  await open(page)
  f.state.patchFailure = 'ops_storage_failed'
  await panel(page)
    .getByRole('textbox', { name: '输入运维需求' })
    .fill('local text')
  await expect(panel(page)).toContainText('保存失败')
  await button(page, '保存').click()
  await expect(panel(page)).toContainText('草稿已保存')
  f.state.patchFailure = 'ops_conflict'
  await panel(page)
    .getByRole('textbox', { name: '输入运维需求' })
    .fill('local conflicted')
  await expect(panel(page)).toContainText('草稿版本冲突')
  await expect(
    panel(page).getByRole('textbox', { name: '输入运维需求' }),
  ).toHaveValue('local conflicted')
  await button(page, '保留本地草稿并保存').click()
  await expect(panel(page)).toContainText('草稿已保存')
  expect(f.state.db.chat!.session.draft).toBe('local conflicted')
  await expect(panel(page)).not.toContainText('RAW_SECRET')
})
test('entry defaults only new session; restored targets never overwritten', async ({
  page,
}) => {
  const f = await fixture(page, { empty: true })
  await page.goto('/settings/servers')
  await page
    .getByRole('button', { name: 'AI 运维助手', exact: true })
    .first()
    .click()
  await expect(panel(page).locator('.target-summary')).toContainText('Server A')
  expect(f.state.db[f.state.active]!.session.serverIds).toEqual(['a'])
  await button(page, '收起助手').click()
  await page
    .getByRole('button', { name: 'AI 运维助手', exact: true })
    .nth(1)
    .click()
  await expect(panel(page).locator('.target-summary')).toContainText('Server A')
  await button(page, '新建会话').click()
  await expect(panel(page).locator('.target-summary')).toContainText('Server B')
})
test('global new session has no targets; read-only tools, partial results and isolated retry', async ({
  page,
}) => {
  const f = await fixture(page, { empty: true })
  await open(page)
  await expect(panel(page).locator('.target-summary')).toHaveText(
    '未选择服务器',
  )
  await selectTargets(page)
  await collect(page)
  await expect(panel(page)).toContainText('Frozen A')
  await expect(panel(page)).toContainText('Frozen B')
  await expect(panel(page)).toContainText('Mock target unavailable')
  await panel(page)
    .locator('[data-call-id]')
    .filter({ hasText: 'Frozen B' })
    .getByRole('button', { name: '仅重试此失败调用' })
    .click()
  await expect(panel(page)).toContainText('Retry B succeeded')
  const retries = f.state.requests.filter((r) => r.path.endsWith('/retry'))
  expect(retries).toHaveLength(1)
  expect(retries[0]!.body!.callIds).toEqual([
    f.state.db[f.state.active]!.calls!.find((c) => c.status === 'failed')!
      .callId,
  ])
  expect(
    f.state.requests.filter((r) => r.path.endsWith('/turns')),
  ).toHaveLength(1)
})
test('analysis previews exact content, consent defaults off and provider change rejects', async ({
  page,
}) => {
  const f = await fixture(page)
  await open(page)
  await selectTargets(page)
  await collect(page)
  const result = panel(page)
    .locator('[data-call-id]')
    .filter({ hasText: 'Frozen A' })
  await result.getByRole('button', { name: '交给 AI 分析' }).click()
  const consent = panel(page).locator('.consent')
  await expect(consent).toContainText('mock-provider')
  await expect(consent).toContainText('Result on A')
  await expect(
    consent.getByRole('button', { name: '交给 AI 分析' }),
  ).toBeDisabled()
  expect(
    f.state.requests.filter((r) => r.path.endsWith('/analysis')),
  ).toHaveLength(0)
  f.state.changeProvider = true
  await consent.getByRole('checkbox').check()
  await consent.getByRole('button', { name: '交给 AI 分析' }).click()
  await expect(panel(page)).toContainText('分析内容或模型配置已变化')
  const payload = f.state.requests.find((r) =>
    r.path.endsWith('/analysis'),
  )!.body!
  expect(payload.previewHash).toBe('preview-hash')
  expect(payload.provider).toEqual({
    provider: 'mock-provider',
    model: 'mock-model',
    configHash: 'config',
  })
})
test('batch reissue is explicit and confirms every frozen target exactly once', async ({
  page,
}) => {
  const f = await fixture(page)
  await open(page)
  await selectTargets(page)
  await panel(page).getByText('运维工具', { exact: true }).click()
  await panel(page).getByRole('combobox', { name: '运维工具' }).click()
  await page.getByRole('option', { name: '容器操作', exact: true }).click()
  await panel(page).getByRole('textbox', { name: '容器名称或 ID' }).fill('web')
  await button(page, '准备修改').click()
  const batch = panel(page).locator('.confirmation')
  await expect(batch).toContainText('Frozen A')
  await expect(batch).toContainText('Frozen B')
  f.state.db.chat!.confirmations![0]!.valid = false
  await button(page, '收起助手').click()
  await launcher(page).click()
  await button(page, '重新核验确认').click()
  await expect(button(page, '确认这一批')).toBeEnabled()
  expect(
    f.state.requests.filter((r) => r.path.endsWith('/calls/confirm')),
  ).toHaveLength(0)
  await button(page, '确认这一批').click()
  await expect(batch).toHaveCount(0)
  expect(
    f.state.requests.filter((r) => r.path.endsWith('/calls/confirm')),
  ).toHaveLength(1)
  expect(
    f.state.requests.find((r) => r.path.endsWith('/calls/confirm'))!.body!
      .calls as unknown[],
  ).toHaveLength(2)
})
test('ambiguous submission preserves exact request ID and newer typed draft', async ({
  page,
}) => {
  const f = await fixture(page)
  await open(page)
  f.state.dropSend = true
  const input = panel(page).getByRole('textbox', { name: '输入运维需求' })
  await input.fill('original request')
  await button(page, '发送').click()
  await expect(button(page, '重新确认提交')).toBeVisible()
  await input.fill('new typed text')
  await button(page, '重新确认提交').click()
  await expect(panel(page)).toContainText('Manual advice')
  await expect(input).toHaveValue('new typed text')
  const sent = f.state.requests.filter((r) => r.path.endsWith('/turns'))
  expect(sent).toHaveLength(2)
  expect(sent[0]!.body).toEqual(sent[1]!.body)
})
test('running work survives close; manual advice is copy-only; nested overlays own escape', async ({
  page,
}) => {
  const f = await fixture(page)
  await open(page)
  f.state.holdRuns = true
  await panel(page)
    .getByRole('textbox', { name: '输入运维需求' })
    .fill('check servers')
  await button(page, '发送').click()
  await expect(button(page, '停止任务')).toBeVisible()
  await button(page, '收起助手').click()
  expect(f.state.requests.some((r) => r.path.endsWith('/cancel'))).toBe(false)
  const s = f.state.db.chat!
  s.session.activeRunId = ''
  s.runs![0]!.status = 'succeeded'
  f.entry(s, 'assistant', 'Manual advice only: docker compose up -d')
  await launcher(page).click()
  await expect(panel(page)).toContainText('Manual advice only')
  await panel(page)
    .locator('.message')
    .getByRole('button', { name: '复制' })
    .click()
  const copy = page.locator('.copy-dialog')
  await expect(copy).toBeVisible()
  await expect(copy.getByRole('textbox')).toHaveValue(
    'Manual advice only: docker compose up -d',
  )
  await page.keyboard.press('Tab')
  await page.keyboard.press('Escape')
  await expect(copy).toHaveCount(0)
  await expect(panel(page)).toBeVisible()
  await panel(page).getByText('运维工具', { exact: true }).click()
  await panel(page).getByRole('combobox', { name: '运维工具' }).click()
  await expect(page.getByRole('listbox')).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(page.getByRole('listbox')).toHaveCount(0)
  await expect(panel(page)).toBeVisible()
  await page.mouse.click(10, 150)
  await expect(panel(page)).toBeVisible()
  expect(f.state.requests.some((r) => r.path.includes('execute'))).toBe(false)
})
test('logout clears displayed chat and rejects later protected reads', async ({
  page,
}) => {
  const f = await fixture(page)
  await open(page)
  await panel(page)
    .getByRole('textbox', { name: '输入运维需求' })
    .fill('private text')
  await expect(panel(page)).toContainText('草稿已保存')
  f.state.auth = false
  await button(page, '会话历史').click()
  await panel(page)
    .getByRole('button')
    .filter({ hasText: 'Session other' })
    .click()
  await expect(page).toHaveURL(/\/login/)
  await expect(page.locator('body')).not.toContainText('private text')
})
test('container log dropdown is custom and latest numeric tail wins delayed history', async ({
  page,
}) => {
  await fixture(page)
  let release: () => void = () => undefined
  let first = true
  const counts: number[] = []
  await page.route(
    (u) => u.pathname.endsWith('/logs'),
    async (r) => {
      const n = Number(new URL(r.request().url()).searchParams.get('lines'))
      counts.push(n)
      if (first) {
        first = false
        await new Promise<void>((resolve) => {
          release = resolve
        })
      }
      return json(r, {
        source: 'docker',
        target: 'web-a',
        lines: [{ text: 'tail ' + n }],
      })
    },
  )
  await page.goto('/containers')
  await page.getByRole('button', { name: '日志', exact: true }).first().click()
  const logs = page
    .locator('.drawer')
    .filter({ has: page.getByRole('combobox') })
  await logs.getByRole('combobox').click()
  await page.getByRole('option', { name: '500', exact: true }).click()
  await expect(logs).toContainText('tail 500')
  release()
  await page.waitForTimeout(100)
  await expect(logs).not.toContainText('tail 200')
  expect(counts).toEqual([200, 500])
  expect(await logs.locator('select').count()).toBe(0)
})

test('narrow assistant hide preserves PTY dimensions until terminal is restored', async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 500 })
  await fixture(page)
  await page.addInitScript(() =>
    localStorage.setItem('pw-terminal-ai-open', '0'),
  )
  const sizes: { cols: number; rows: number }[] = []
  const inputs: string[] = []
  await page.routeWebSocket('**/api/servers/a/terminal*', (ws) => {
    ws.onMessage((raw) => {
      try {
        const frame = JSON.parse(String(raw))
        if (frame.type === 'resize')
          sizes.push({ cols: frame.cols, rows: frame.rows })
      } catch {
        inputs.push(String(raw))
      }
    })
  })
  await page.goto('/servers/a/terminal')
  await page.evaluate(() => document.fonts.ready)
  await expect.poll(() => sizes.length).toBeGreaterThan(0)
  await page.evaluate(
    () =>
      new Promise<void>((r) =>
        requestAnimationFrame(() => requestAnimationFrame(() => r())),
      ),
  )
  const before = sizes.length
  const dimensions = sizes.at(-1)
  await page.locator('.xterm-helper-textarea').pressSequentially('do')
  await expect(page.locator('.caret-ghost')).toBeVisible()
  const inputCount = inputs.length
  await page.locator('.ai-launcher').click()
  await expect(
    panel(page).getByRole('textbox', { name: '输入运维需求' }),
  ).toBeEnabled()
  await expect(page.locator('.term-wrap')).toBeHidden()
  await expect(page.locator('.caret-ghost')).toBeHidden()
  await page.evaluate(
    () =>
      new Promise<void>((r) =>
        requestAnimationFrame(() => requestAnimationFrame(() => r())),
      ),
  )
  expect(sizes.length).toBe(before)
  expect(inputs.length).toBe(inputCount)
  await button(page, '收起助手').click()
  await expect(page.locator('.term-wrap')).toBeVisible()
  await expect.poll(() => sizes.length).toBeGreaterThan(before)
  expect(sizes.at(-1)!.cols).toBe(dimensions!.cols)
  expect(sizes.at(-1)!.rows).toBeGreaterThanOrEqual(dimensions!.rows - 1)
  expect(sizes.at(-1)!.rows).toBeLessThanOrEqual(dimensions!.rows + 1)
})

for (const viewport of [
  { width: 390, height: 500 },
  { width: 390, height: 844 },
  { width: 1366, height: 500 },
])
  test(`embedded terminal assistant ${viewport.width}x${viewport.height}`, async ({
    page,
  }, testInfo) => {
    await page.setViewportSize(viewport)
    const f = await fixture(page)
    const ids = ['a', ...Array.from({ length: 7 }, (_, i) => 'target' + i)]
    f.state.db.chat!.session.serverIds = ids
    f.state.db.chat!.session.title = 'Long session title '.repeat(4)
    for (let i = 0; i < 12; i++)
      f.entry(f.state.db.chat!, 'assistant', 'content '.repeat(100))
    for (let i = 0; i < 15; i++)
      f.state.db['history' + i] = snap(session('history' + i))
    await page.route(
      (u) => u.pathname === '/api/servers',
      (r) =>
        json(r, {
          items: ids.map((id) => ({
            ...server(id),
            name: 'Long server name '.repeat(5) + id,
          })),
        }),
    )
    await page.addInitScript(() =>
      localStorage.setItem('pw-terminal-ai-open', '1'),
    )
    await page.routeWebSocket('**/api/servers/a/terminal*', () => {})
    await page.goto('/servers/a/terminal')
    const input = panel(page).getByRole('textbox', { name: '输入运维需求' })
    await expect(input).toBeEnabled()
    await button(page, '会话历史').click()
    await panel(page).getByTestId('ops-navigation').locator('summary').click()
    await input.scrollIntoViewIfNeeded()
    const panelBox = (await panel(page).boundingBox())!
    const inputBox = (await input.boundingBox())!
    expect(inputBox.y + inputBox.height).toBeLessThanOrEqual(
      panelBox.y + panelBox.height + 1,
    )
    await panel(page).getByText('运维工具', { exact: true }).click()
    f.state.patchFailure = 'ops_storage_failed'
    await input.fill('recoverable draft')
    await expect(panel(page)).toContainText('保存失败')
    await expect(panel(page).getByRole('alert')).toBeVisible()
    await input.scrollIntoViewIfNeeded()
    const bodyBox = (await panel(page)
      .getByTestId('ops-messages-scroll')
      .boundingBox())!
    const composerBox = (await panel(page).locator('.composer').boundingBox())!
    expect(bodyBox.height).toBeGreaterThanOrEqual(48)
    expect(composerBox.height).toBeGreaterThan(40)
    expect(composerBox.y + composerBox.height).toBeLessThanOrEqual(
      panelBox.y + panelBox.height + 1,
    )
    await panel(page)
      .locator('.composer')
      .getByRole('button', { name: '保存', exact: true })
      .click()
    await expect(panel(page)).toContainText('草稿已保存')
    await page.screenshot({
      path: testInfo.outputPath('embedded-terminal.png'),
    })
    await button(page, '收起助手').click()
    await expect(panel(page)).toHaveCount(0)
    await expect(page.locator('.term-wrap')).toBeVisible()
    expect(
      f.state.requests.some(
        (r) => r.path.endsWith('/turns') || r.path.endsWith('/cancel'),
      ),
    ).toBe(false)
  })

test('short panel keeps long targets, rename and expanded tools reachable', async ({
  page,
}, testInfo) => {
  await page.setViewportSize({ width: 390, height: 500 })
  const f = await fixture(page)
  const ids = Array.from({ length: 8 }, (_, i) => 'target' + i)
  f.state.db.chat!.session.serverIds = ids
  f.state.db.chat!.session.title = 'Long session title '.repeat(4)
  for (let i = 0; i < 10; i++)
    f.entry(f.state.db.chat!, 'assistant', 'content '.repeat(100))
  await page.route(
    (u) => u.pathname === '/api/servers',
    (r) =>
      json(r, {
        items: ids.map((id) => ({
          ...server(id),
          name: 'Long server name '.repeat(5) + id,
        })),
      }),
  )
  await open(page)
  await button(page, '会话历史').click()
  const nav = panel(page).getByTestId('ops-navigation')
  await nav.locator('summary').click()
  await button(page, '重命名').click()
  await panel(page).getByText('运维工具', { exact: true }).click()
  const input = panel(page).getByRole('textbox', { name: '输入运维需求' })
  await input.fill('short panel draft')
  await expect(panel(page)).toContainText('草稿已保存')
  const box = (await panel(page)
    .getByTestId('ops-messages-scroll')
    .boundingBox())!
  const summary = (await nav.locator('.target-summary').boundingBox())!
  const list = (await nav.locator('.server-list').boundingBox())!
  expect(box.height).toBeGreaterThanOrEqual(48)
  expect(summary.y + summary.height).toBeLessThanOrEqual(box.y + 1)
  expect(list.y + list.height).toBeLessThanOrEqual(summary.y + 1)
  await input.scrollIntoViewIfNeeded()
  await input.focus()
  expect(
    (await input.boundingBox())!.y + (await input.boundingBox())!.height,
  ).toBeLessThanOrEqual(501)
  await expect(button(page, '保存')).toBeVisible()
  await page.screenshot({ path: testInfo.outputPath('short-panel.png') })
})

for (const viewport of [
  { width: 1366, height: 900 },
  { width: 1920, height: 900 },
  { width: 390, height: 844 },
  { width: 390, height: 500 },
])
  for (const theme of ['light', 'dark'])
    test(`fixed navigation ${viewport.width}x${viewport.height} ${theme}`, async ({
      page,
    }, testInfo) => {
      await page.setViewportSize(viewport)
      const f = await fixture(page, { theme })
      for (let i = 0; i < 30; i++)
        f.state.db['history' + i] = snap(session('history' + i))
      for (let i = 0; i < 40; i++)
        f.entry(
          f.state.db.chat!,
          'assistant',
          'Message ' + i + '\n' + 'long content '.repeat(30),
        )
      await page.route(
        (u) => u.pathname === '/api/servers',
        (r) =>
          json(r, {
            items: Array.from({ length: 30 }, (_, i) =>
              server(i === 0 ? 'a' : i === 1 ? 'b' : 'extra' + i),
            ),
          }),
      )
      await open(page)
      const header = panel(page).getByTestId('ops-header')
      const nav = panel(page).getByTestId('ops-navigation')
      const messages = panel(page).getByTestId('ops-messages-scroll')
      const input = panel(page).getByRole('textbox', { name: '输入运维需求' })
      await expect(messages.locator('.message')).toHaveCount(40)
      await button(page, '会话历史').click()
      await nav.locator('summary').click()
      const before = await Promise.all([
        header.boundingBox(),
        nav.boundingBox(),
        input.boundingBox(),
      ])
      await messages.evaluate((el) => {
        el.scrollTop = el.scrollHeight
      })
      await expect
        .poll(() => messages.evaluate((el) => el.scrollTop))
        .toBeGreaterThan(0)
      const after = await Promise.all([
        header.boundingBox(),
        nav.boundingBox(),
        input.boundingBox(),
      ])
      for (let i = 0; i < before.length; i++) {
        expect(after[i]!.y).toBeCloseTo(before[i]!.y, 1)
        expect(after[i]!.height).toBeCloseTo(before[i]!.height, 1)
      }
      expect(after[0]!.y).toBeGreaterThanOrEqual(0)
      expect(after[2]!.y + after[2]!.height).toBeLessThanOrEqual(
        viewport.height,
      )
      expect((await messages.boundingBox())!.height).toBeGreaterThan(40)
      const serversBox = (await nav.locator('.server-list').boundingBox())!
      const summaryBox = (await nav.locator('.target-summary').boundingBox())!
      const historyBox = (await nav.locator('.session-list').boundingBox())!
      const navBox = (await nav.boundingBox())!
      expect(serversBox.y + serversBox.height).toBeLessThanOrEqual(
        summaryBox.y + 1,
      )
      expect(historyBox.y + historyBox.height).toBeLessThanOrEqual(
        navBox.y + navBox.height + 1,
      )
      for (const list of [
        nav.locator('.session-list'),
        nav.locator('.server-list'),
      ]) {
        await list.evaluate((el) => {
          el.scrollTop = el.scrollHeight
        })
        await expect
          .poll(() => list.evaluate((el) => el.scrollTop))
          .toBeGreaterThan(0)
        await list.evaluate((el) => {
          el.scrollTop = 0
        })
      }
      await input.fill('layout draft')
      await expect(panel(page)).toContainText('草稿已保存')
      await nav
        .locator('.session-row')
        .filter({ hasText: 'Session other' })
        .click()
      await expect(input).toHaveValue('')
      await expect(nav.locator('.target-summary')).toContainText('Server B')
      await nav
        .locator('.session-row')
        .filter({ hasText: 'Session chat' })
        .click()
      await expect(input).toHaveValue('layout draft')
      await expect(messages.locator('.message')).toHaveCount(40)
      expect(
        await panel(page).evaluate(
          (el) => el.scrollWidth <= el.clientWidth + 1,
        ),
      ).toBe(true)
      await page.screenshot({
        path: testInfo.outputPath('fixed-navigation.png'),
        fullPage: true,
      })
      expect(
        f.state.requests.some(
          (r) => r.path.endsWith('/turns') || r.path.endsWith('/cancel'),
        ),
      ).toBe(false)
      expect(f.state.external).toEqual([])
    })

for (const width of [1366, 1920, 390])
  for (const theme of ['light', 'dark'])
    test(`server entry layout ${width} ${theme}`, async ({
      page,
    }, testInfo) => {
      await page.setViewportSize({ width, height: 900 })
      await fixture(page, { theme })
      await page.route(
        (u) => u.pathname === '/api/servers',
        (r) =>
          json(r, {
            items: [
              server('a'),
              { ...server('b'), name: 'Long-server-name-'.repeat(20) },
            ],
          }),
      )
      await page.route(
        (u) => u.pathname === '/api/servers/metrics',
        (r) =>
          json(r, {
            items: ['a', 'b'].map((serverId) => ({
              serverId,
              reachable: serverId === 'a',
              error: serverId === 'b' ? 'Offline fixture' : '',
              cpu: null,
              memory: null,
              disk: null,
              collectedAt: stamp,
            })),
          }),
      )
      await page.goto('/servers')
      const actions = page.locator('.view-header__actions')
      await expect(actions.getByRole('button')).toHaveCount(2)
      const buttons = await actions.getByRole('button').all()
      const a = (await buttons[0]!.boundingBox())!,
        b = (await buttons[1]!.boundingBox())!
      expect(Math.abs(a.y - b.y)).toBeLessThan(2)
      expect(b.x - (a.x + a.width)).toBeLessThanOrEqual(17)
      await expect(page.locator('.metrics-card')).toHaveCount(2)
      await expect(
        page.locator('.metrics-card .metrics-card__actions button'),
      ).toHaveCount(2)
      for (const card of await page.locator('.metrics-card').all()) {
        expect(
          await card.evaluate((el) => el.scrollWidth <= el.clientWidth + 1),
        ).toBe(true)
        const rect = (await card.boundingBox())!,
          button = (await card
            .locator('.metrics-card__actions button')
            .boundingBox())!
        expect(button.y + button.height).toBeLessThan(rect.y + rect.height)
        expect(rect.x + rect.width).toBeLessThanOrEqual(width + 1)
      }
      await page.screenshot({
        path: testInfo.outputPath('server-entries.png'),
        fullPage: true,
      })
    })

for (const width of [1366, 1920, 390])
  for (const theme of ['light', 'dark'])
    test('layout ' + width + ' ' + theme, async ({ page }, testInfo) => {
      await page.setViewportSize({ width, height: width === 390 ? 844 : 900 })
      const f = await fixture(page, { theme })
      await open(page)
      await selectTargets(page)
      await collect(page)
      await panel(page)
        .getByRole('textbox', { name: '输入运维需求' })
        .fill('连续输入测试 abcXYZ 123')
      await expect(panel(page)).toContainText('草稿已保存')
      const bounds = await panel(page).evaluate((el) => {
        const input = el.querySelector('textarea')!,
          style = getComputedStyle(input),
          rect = el.getBoundingClientRect()
        const canvas = document.createElement('canvas'),
          ctx = canvas.getContext('2d')!
        function rgb(color: string): number[] {
          ctx.clearRect(0, 0, 1, 1)
          ctx.fillStyle = color
          ctx.fillRect(0, 0, 1, 1)
          return [...ctx.getImageData(0, 0, 1, 1).data].slice(0, 3)
        }
        const luminance = (color: string) =>
          rgb(color)
            .map((v) => {
              v /= 255
              return v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4
            })
            .reduce((sum, v, i) => sum + v * [0.2126, 0.7152, 0.0722][i]!, 0)
        const fg = luminance(style.color),
          bg = luminance(style.backgroundColor)
        return {
          left: rect.left,
          right: rect.right,
          width: innerWidth,
          scroll: el.scrollWidth,
          client: el.clientWidth,
          contrast: (Math.max(fg, bg) + 0.05) / (Math.min(fg, bg) + 0.05),
          placeholder: getComputedStyle(input, '::placeholder').color,
          text: style.color,
        }
      })
      expect(bounds.left).toBeGreaterThanOrEqual(0)
      expect(bounds.right).toBeLessThanOrEqual(bounds.width + 1)
      expect(bounds.scroll).toBeLessThanOrEqual(bounds.client + 1)
      expect(bounds.contrast).toBeGreaterThanOrEqual(4.5)
      expect(bounds.placeholder).not.toBe('rgba(0, 0, 0, 0)')
      await page.screenshot({
        path: testInfo.outputPath('ops-' + width + '-' + theme + '.png'),
        fullPage: true,
      })
      await panel(page).getByRole('combobox', { name: '运维工具' }).click()
      const menu = page.getByRole('listbox')
      await expect(menu).toBeVisible()
      const rect = await menu.boundingBox()
      expect(rect!.x).toBeGreaterThanOrEqual(0)
      expect(rect!.x + rect!.width).toBeLessThanOrEqual(width + 1)
      await page.screenshot({
        path: testInfo.outputPath('menu-' + width + '-' + theme + '.png'),
        fullPage: true,
      })
      expect(f.state.external).toEqual([])
    })
