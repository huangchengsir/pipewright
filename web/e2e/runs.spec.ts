import { test, expect } from '@playwright/test'
import {
  stubLoggedIn,
  stubEnvelopeDefaults,
  runListItemFixture,
  runDetailFixture,
  diagnosisReadyFixture,
} from './_stubs'

/**
 * Runs list (grouped + filtered) and run detail (steps timeline, failed run +
 * AI diagnosis card with 👍/👎 feedback). Run-detail DTO follows the frozen 3.1
 * shape; diagnosis follows the frozen 7.2 DiagnosisDTO.
 *
 * Note: GET /api/runs returns an envelope { items, page, total }; the run-detail
 * endpoint /api/runs/{id} returns a bare RunDetail object — both stubbed here.
 */

function fulfillJson(body: unknown) {
  return {
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify(body),
  }
}

test.describe('Runs list', () => {
  test('renders grouped rows from GET /api/runs and a failed row shows the diagnosis shortcut', async ({ page }) => {
    await stubLoggedIn(page)
    await stubEnvelopeDefaults(page)
    await page.route(
      (u) => u.pathname === '/api/runs',
      (route) =>
        route.fulfill(
          fulfillJson({
            items: [
              runListItemFixture({ id: 'r-success1', status: 'success', projectName: 'acme-web' }),
              runListItemFixture({ id: 'r-failed1', status: 'failed', projectName: 'billing-svc' }),
            ],
            page: 1,
            total: 2,
          }),
        ),
    )
    await page.goto('/runs')

    await expect(page.getByRole('heading', { name: '运行', exact: true })).toBeVisible()
    await expect(page.getByText('acme-web')).toBeVisible()
    await expect(page.getByText('billing-svc')).toBeVisible()
    // "今天" date group header (fixtures use now()).
    await expect(page.getByText('今天', { exact: true })).toBeVisible()
    // Failed row carries the diagnosis shortcut.
    await expect(page.getByText('查看诊断 →')).toBeVisible()
  })

  test('status filter re-queries the API with the chosen status', async ({ page }) => {
    await stubLoggedIn(page)
    await stubEnvelopeDefaults(page)
    let lastStatus = ''
    await page.route(
      (u) => u.pathname === '/api/runs',
      (route) => {
        lastStatus = new URL(route.request().url()).searchParams.get('status') ?? ''
        return route.fulfill(fulfillJson({ items: [], page: 1, total: 0 }))
      },
    )
    await page.goto('/runs')

    await page.getByRole('group', { name: '运行状态筛选' }).getByRole('button', { name: '失败', exact: true }).click()
    await expect.poll(() => lastStatus).toBe('failed')
    await expect(page.getByText('没有符合当前筛选条件的运行。')).toBeVisible()
  })
})

test.describe('Run detail', () => {
  test('success run shows the steps timeline and completion section', async ({ page }) => {
    await stubLoggedIn(page)
    await stubEnvelopeDefaults(page)
    await page.route(
      (u) => u.pathname === '/api/runs/r-ok',
      (route) => route.fulfill(fulfillJson(runDetailFixture({ id: 'r-ok', status: 'success' }))),
    )
    await page.goto('/runs/r-ok')

    await expect(page.getByRole('heading', { level: 1 })).toContainText('acme-web')
    await expect(page.getByText('流水线完成')).toBeVisible()
    // Step names from the DTO render in the timeline.
    await expect(page.getByText('拉取代码')).toBeVisible()
    await expect(page.getByText('构建', { exact: true })).toBeVisible()
  })

  test('failed run renders the AI diagnosis card: hypothesis, confidence, highlighted evidence', async ({ page }) => {
    await stubLoggedIn(page)
    await stubEnvelopeDefaults(page)
    await page.route(
      (u) => u.pathname === '/api/runs/r-bad',
      (route) =>
        route.fulfill(
          fulfillJson(
            runDetailFixture({
              id: 'r-bad',
              status: 'failed',
              steps: [
                { id: 's1', name: '拉取代码', status: 'success', startedAt: '2026-05-29T08:00:00Z', finishedAt: '2026-05-29T08:00:05Z', durationMs: 5000 },
                { id: 's2', name: '部署', status: 'failed', startedAt: '2026-05-29T08:00:05Z', finishedAt: '2026-05-29T08:00:20Z', durationMs: 15000 },
              ],
              finishedAt: '2026-05-29T08:00:20Z',
              durationMs: 20000,
              diagnosis: diagnosisReadyFixture(),
            }),
          ),
        ),
    )
    await page.goto('/runs/r-bad')

    const panel = page.getByRole('region', { name: 'AI 失败诊断' })
    await expect(panel).toBeVisible()
    await expect(panel.getByText('AI 失败诊断')).toBeVisible()
    await expect(panel.getByText('置信度·高')).toBeVisible()
    // Root-cause hypothesis (left "AI 认为" column).
    await expect(panel.getByRole('region', { name: 'AI 认为' })).toContainText('DB_PASSWORD')
    await expect(panel.getByText('最可能的根因是')).toBeVisible()
    // Fix suggestion + highlighted evidence line (right "原始日志证据" column).
    await expect(panel.getByText('在凭据保险库注入 DB_PASSWORD')).toBeVisible()
    await expect(
      panel.getByRole('region', { name: '原始日志证据' }).getByText(/password authentication failed/),
    ).toBeVisible()
  })

  test('diagnosis 👍 feedback posts and shows the recorded state', async ({ page }) => {
    await stubLoggedIn(page)
    await stubEnvelopeDefaults(page)
    let feedbackBody: unknown = null
    await page.route(
      (u) => u.pathname === '/api/runs/r-bad',
      (route) =>
        route.fulfill(fulfillJson(runDetailFixture({ id: 'r-bad', status: 'failed', diagnosis: diagnosisReadyFixture() }))),
    )
    await page.route(
      (u) => u.pathname === '/api/runs/r-bad/diagnosis/feedback',
      (route) => {
        feedbackBody = route.request().postDataJSON()
        return route.fulfill(fulfillJson({ ok: true }))
      },
    )
    await page.goto('/runs/r-bad')

    await page.getByRole('button', { name: '诊断有帮助' }).click()
    await expect.poll(() => (feedbackBody as { verdict?: string })?.verdict).toBe('up')
    await expect(page.getByText(/已记录你的反馈/)).toBeVisible()
  })

  test('in-progress run shows the step records and a cancel control', async ({ page }) => {
    await stubLoggedIn(page)
    await stubEnvelopeDefaults(page)
    await page.route(
      (u) => u.pathname === '/api/runs/r-run',
      (route) =>
        route.fulfill(
          fulfillJson(
            runDetailFixture({
              id: 'r-run',
              status: 'running',
              steps: [
                { id: 's1', name: '拉取代码', status: 'success', startedAt: '2026-05-29T08:00:00Z', finishedAt: '2026-05-29T08:00:05Z', durationMs: 5000 },
                { id: 's2', name: '构建', status: 'running', startedAt: '2026-05-29T08:00:05Z', finishedAt: null, durationMs: null },
              ],
              finishedAt: null,
              durationMs: null,
            }),
          ),
        ),
    )
    await page.goto('/runs/r-run')

    await expect(page.getByRole('button', { name: /取消运行/ })).toBeVisible()
    await expect(page.getByText('步骤详情')).toBeVisible()
    await expect(page.getByText('拉取代码').first()).toBeVisible()
  })
})
