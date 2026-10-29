import { HttpError } from '../../api/http'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import SettingsSystem from './SettingsSystem.vue'
import { i18n, SUPPORTED_LOCALES } from '../../i18n'
const mocks = vi.hoisted(() => ({ current: vi.fn(), get: vi.fn(), start: vi.fn(), resume: vi.fn(), version: vi.fn(), check: vi.fn() }))
vi.mock('../../api/version', () => ({ getCurrentUpdateTask: mocks.current, getUpdateTask: mocks.get, startUpdateTask: mocks.start, resumeUpdateTask: mocks.resume, getVersion: mocks.version, checkUpdate: mocks.check }))
vi.mock('../../api/retention', () => ({ getRetentionConfig: vi.fn(async () => ({ enabled: false, keepPerProject: 0, maxAgeDays: 0 })), setRetentionConfig: vi.fn() }))
const wrappers: VueWrapper[] = []
describe('system upgrade task presentation', () => {
 beforeEach(() => { vi.resetAllMocks(); sessionStorage.clear(); mocks.version.mockResolvedValue({ version: '1.0.0', commit: 'test', platform: 'linux/amd64', date: '2026-10-27T00:00:00Z', goVersion: 'go1.26' }); mocks.current.mockResolvedValue({ id: 'task', requestId: 'request', repository: 'owner/repo', target: '2.0.0', from: '1.0.0', platform: 'linux_amd64', asset: 'fixed-package', sha256: 'a'.repeat(64), state: 'ready_blocked', bytes: 100, total: 100, blockers: [{ kind: 'guards', count: 1 }], updatedAt: '2026-10-27T00:00:00Z' }) })
 afterEach(() => { wrappers.splice(0).forEach(w => w.unmount()); i18n.global.locale.value = 'zh-CN'; vi.useRealTimers(); vi.restoreAllMocks() })
 it('keeps task status visible without a fresh update check', async () => { const w = mount(SettingsSystem); wrappers.push(w); await flushPromises(); expect(w.get('[data-testid="upgrade-task"]').text()).toContain('2.0.0'); expect(mocks.check).not.toHaveBeenCalled(); expect(mocks.start).not.toHaveBeenCalled(); expect(mocks.resume).not.toHaveBeenCalled() })
 it('translates all eight locales without resubmitting or losing the task', async () => { const w = mount(SettingsSystem); wrappers.push(w); await flushPromises(); const seen = new Set<string>(); for (const { code } of SUPPORTED_LOCALES) { i18n.global.locale.value = code; await flushPromises(); const text = w.get('[data-testid="upgrade-task"]').text(); expect(text).not.toContain('settingsSystem.'); expect(text).toContain('2.0.0'); seen.add(w.get('#upgrade-task-title').text()) } expect(seen.size).toBe(8); expect(mocks.current).toHaveBeenCalledTimes(1); expect(mocks.start).not.toHaveBeenCalled(); expect(mocks.resume).not.toHaveBeenCalled() })
 it('keeps original submission recovery available alongside unrelated history', async () => {
  const requestId = 'a'.repeat(32)
  sessionStorage.setItem('pipewright.update.pending', JSON.stringify({ requestId, target: '2.0.0' }))
  mocks.current.mockResolvedValue({ id: 'old', requestId: 'old', state: 'completed', from: '1.0.0', target: '1.5.0', bytes: 0, total: 0 })
  const w = mount(SettingsSystem); wrappers.push(w); await flushPromises()
  const recover = w.findAll('button').find(b => b.text() === i18n.global.t('settingsSystem.taskOriginalRequest'))
  expect(recover).toBeDefined()
  expect(mocks.start).not.toHaveBeenCalled()
  mocks.start.mockRejectedValue(new Error('still unreachable'))
  await recover!.trigger('click'); await flushPromises()
  expect(mocks.start).toHaveBeenCalledWith({ requestId, target: '2.0.0' })
 })

 it('shows a translated definitive rejection without stranding pending state', async () => {
  mocks.current.mockResolvedValue(null)
  mocks.check.mockResolvedValue({ current: '1.0.0', latest: '2.0.0', updateAvailable: true })
  mocks.start.mockRejectedValue(new HttpError(409, { code: 'update_conflict', message: 'raw server message' }, 'raw server message'))
  const w = mount(SettingsSystem); wrappers.push(w); await flushPromises()
  await w.findAll('button').find(b => b.text().includes(i18n.global.t('settingsSystem.checkUpdate')))!.trigger('click')
  await flushPromises()
  await w.findAll('button').find(b => b.text().includes(i18n.global.t('settingsSystem.updateNow')))!.trigger('click')
  await flushPromises()
  expect(w.text()).toContain(i18n.global.t('settingsSystem.errUpdateStatus', { status: 409 }))
  expect(w.text()).not.toContain('raw server message')
 })

 it.each(['start', 'resume'] as const)('makes original %s recovery clickable while accepted is busy', async action => {
  const accepted = () => ({ id: 'task', requestId: JSON.parse(sessionStorage.getItem('pipewright.update.pending') || '{}').requestId || 'server-request', target: '2.0.0', from: '1.0.0', state: 'accepted', bytes: 0, total: 0 })
  mocks.current.mockResolvedValueOnce(action === 'start' ? null : { ...accepted(), state: 'interrupted' }).mockImplementation(async () => accepted())
  mocks.get.mockImplementation(async () => accepted())
  mocks.check.mockResolvedValue({ current: '1.0.0', latest: '2.0.0', updateAvailable: true })
  if (action === 'start') mocks.start.mockRejectedValueOnce(new HttpError(503, null, 'unconfirmed'))
  else mocks.resume.mockRejectedValueOnce(new HttpError(503, null, 'unconfirmed'))
  const w = mount(SettingsSystem); wrappers.push(w); await flushPromises()
  if (action === 'start') {
   await w.findAll('button').find(b => b.text().includes(i18n.global.t('settingsSystem.checkUpdate')))!.trigger('click'); await flushPromises()
   await w.findAll('button').find(b => b.text().includes(i18n.global.t('settingsSystem.updateNow')))!.trigger('click')
  } else await w.findAll('button').find(b => b.text() === i18n.global.t('settingsSystem.taskResume'))!.trigger('click')
  await flushPromises()
  const original = JSON.parse(sessionStorage.getItem('pipewright.update.pending')!)
  const recover = w.findAll('button').find(b => b.text() === i18n.global.t('settingsSystem.taskOriginalRequest'))
  expect(recover).toBeDefined()
  expect((recover!.element as HTMLButtonElement).disabled).toBe(false)
  expect(action === 'start' ? mocks.start : mocks.resume).toHaveBeenCalledTimes(1)
  if (action === 'start') mocks.start.mockResolvedValueOnce({ status: 'accepted', task: accepted() })
  else mocks.resume.mockResolvedValueOnce(accepted())
  await recover!.trigger('click'); await flushPromises()
  if (action === 'start') expect(mocks.start.mock.calls[1][0]).toEqual({ requestId: original.requestId, target: original.target })
  else expect(mocks.resume.mock.calls[1]).toEqual([original.taskId, original.resumeId])
 })
 it('offers an explicit same-request check for a server accepted task without a browser pending record', async () => {
  mocks.current.mockResolvedValue({ id: 'task', requestId: 'server-request', target: '2.0.0', from: '1.0.0', state: 'accepted', bytes: 0, total: 0 })
  mocks.start.mockResolvedValue({ status: 'accepted', task: { id: 'task', requestId: 'server-request', target: '2.0.0', from: '1.0.0', state: 'accepted' } })
  const w = mount(SettingsSystem); wrappers.push(w); await flushPromises()
  const recover = w.findAll('button').find(b => b.text() === i18n.global.t('settingsSystem.taskOriginalRequest'))
  expect(recover).toBeDefined(); expect((recover!.element as HTMLButtonElement).disabled).toBe(false)
  expect(mocks.start).not.toHaveBeenCalled()
  await recover!.trigger('click'); await flushPromises()
  expect(mocks.start).toHaveBeenCalledOnce()
  expect(mocks.start).toHaveBeenCalledWith({ requestId: 'server-request', target: '2.0.0' })
 })

 it.each(['start', 'resume'] as const)('keeps status refresh reachable during an in-flight %s without stranding its writer', async action => {
  let resolve!: (value: any) => void
  const response = new Promise<any>(yes => { resolve = yes })
  const accepted = () => ({ id: 'task', requestId: JSON.parse(sessionStorage.getItem('pipewright.update.pending') || '{}').requestId || 'server-request', target: '2.0.0', from: '1.0.0', state: 'accepted', bytes: 0, total: 0 })
  mocks.current.mockResolvedValueOnce(action === 'start' ? null : { ...accepted(), state: 'interrupted' }).mockImplementation(async () => accepted())
  mocks.get.mockImplementation(async () => accepted())
  mocks.check.mockResolvedValue({ current: '1.0.0', latest: '2.0.0', updateAvailable: true })
  if (action === 'start') mocks.start.mockReturnValueOnce(response); else mocks.resume.mockReturnValueOnce(response)
  const w = mount(SettingsSystem); wrappers.push(w); await flushPromises()
  if (action === 'start') {
   await w.findAll('button').find(b => b.text().includes(i18n.global.t('settingsSystem.checkUpdate')))!.trigger('click'); await flushPromises()
   await w.findAll('button').find(b => b.text().includes(i18n.global.t('settingsSystem.updateNow')))!.trigger('click')
  } else await w.findAll('button').find(b => b.text() === i18n.global.t('settingsSystem.taskResume'))!.trigger('click')
  await flushPromises()
  const recheck = w.findAll('button').find(b => b.text() === i18n.global.t('settingsSystem.taskRecheck'))
  expect(recheck).toBeDefined(); expect((recheck!.element as HTMLButtonElement).disabled).toBe(false)
  await recheck!.trigger('click'); await flushPromises()
  resolve(action === 'start' ? { status: 'accepted', task: accepted() } : accepted())
  await flushPromises()
  const recover = w.findAll('button').find(b => b.text() === i18n.global.t('settingsSystem.taskOriginalRequest'))
  expect(recover).toBeDefined(); expect((recover!.element as HTMLButtonElement).disabled).toBe(false)
  expect(action === 'start' ? mocks.start : mocks.resume).toHaveBeenCalledTimes(1)
 })
 it('collapses a completed download into history without active controls or background checks', async () => {
  mocks.current.mockResolvedValue({ id: 'completed', requestId: 'done', state: 'completed', from: '1.0.0', target: '2.0.0', bytes: 100, total: 100, updatedAt: '2026-10-29T00:00:00Z', repository: 'owner/repo', platform: 'linux_amd64', asset: 'package', sha256: 'a'.repeat(64) })
  const w = mount(SettingsSystem); wrappers.push(w); await flushPromises()
  const history = w.get('[data-testid="upgrade-history"]')
  expect(history.element.tagName).toBe('DETAILS'); expect(history.attributes('open')).toBeUndefined()
  expect(w.get('.sys-completion-status').text()).toBe('')
  expect(w.find('[data-testid="upgrade-task"]').exists()).toBe(false)
  expect(w.find('[role="progressbar"]').exists()).toBe(false)
  expect(history.get('summary').text()).toContain('1.0.0 → 2.0.0')
  expect(mocks.version).toHaveBeenCalledTimes(1); expect(mocks.check).not.toHaveBeenCalled(); expect(mocks.start).not.toHaveBeenCalled(); expect(mocks.resume).not.toHaveBeenCalled()
  for (const { code } of SUPPORTED_LOCALES) { i18n.global.locale.value = code; await flushPromises(); expect(history.text()).not.toContain('settingsSystem.'); expect(history.get('summary').text()).toContain('2.0.0') }
 })
 it.each([
  ['accepted', 'download'], ['checking', 'download'], ['downloading', 'download'],
  ['verifying', 'verify'], ['extracting', 'verify'], ['installing', 'install'], ['restarting', 'restart'],
 ])('uses the actual %s phase without treating downloaded bytes as completion', async (state, stage) => {
  mocks.current.mockResolvedValue({ id: 'task', requestId: 'req', state, from: '1.0.0', target: '2.0.0', bytes: 100, total: 100 })
  const w = mount(SettingsSystem); wrappers.push(w); await flushPromises()
  expect(w.get('[aria-current="step"]').attributes('data-stage')).toBe(stage)
  expect(w.find('[role="progressbar"]').exists()).toBe(state === 'downloading')
  expect(w.find('[data-testid="upgrade-history"]').exists()).toBe(false)
  expect(mocks.start).not.toHaveBeenCalled(); expect(mocks.resume).not.toHaveBeenCalled()
 })
 it.each([
  [50, 100, '50'], [120, 100, '100'], [10, 0, undefined], [10, -2, undefined], [NaN, 100, undefined], [Infinity, 100, undefined], [-1, 100, undefined], [10, Infinity, undefined],
 ])('keeps download progress finite and honest for %s / %s', async (bytes, total, percentage) => {
  mocks.current.mockResolvedValue({ id: 'task', state: 'downloading', from: '1.0.0', target: '2.0.0', bytes, total })
  const w = mount(SettingsSystem); wrappers.push(w); await flushPromises()
  expect(w.get('[role="progressbar"]').attributes('aria-valuenow')).toBe(percentage)
  expect(w.get('.sys-download').text()).not.toMatch(/NaN|Infinity|-1/)
 })
 it('reads the backend version once after the same observed active task completes and ignores an older read', async () => {
  vi.useFakeTimers()
  let initialRead!: (value: object) => void
  mocks.version.mockReturnValueOnce(new Promise(resolve => { initialRead = resolve })).mockResolvedValueOnce({ version: '2.0.0', commit: 'new', date: '', platform: 'linux/amd64', goVersion: 'go1.26' })
  const task = { id: 'task', requestId: 'req', from: '1.0.0', target: '2.0.0', bytes: 100, total: 100 }
  mocks.current.mockResolvedValueOnce({ ...task, state: 'restarting' }).mockResolvedValue({ ...task, state: 'completed' })
  const w = mount(SettingsSystem); wrappers.push(w); await flushPromises()
  await vi.advanceTimersByTimeAsync(1500); await flushPromises()
  expect(mocks.version).toHaveBeenCalledTimes(2); expect(w.get('.sys-version-num').text()).toBe('2.0.0')
  expect(w.get('.sys-completion-status[role=status]').text()).toBe(i18n.global.t('settingsSystem.taskStates.completed'))
  initialRead({ version: '1.0.0' }); await flushPromises(); expect(w.get('.sys-version-num').text()).toBe('2.0.0')
  await vi.advanceTimersByTimeAsync(3000); expect(mocks.version).toHaveBeenCalledTimes(2)
  expect(mocks.check).not.toHaveBeenCalled(); expect(mocks.start).not.toHaveBeenCalled(); expect(mocks.resume).not.toHaveBeenCalled()
 })
 it('preserves an explicit status error when the bounded completion version read fails', async () => {
  vi.useFakeTimers(); mocks.version.mockRejectedValueOnce(new Error('unreachable')).mockRejectedValueOnce(new Error('still unreachable'))
  const task = { id: 'task', from: '1.0.0', target: '2.0.0', bytes: 100, total: 100 }
  mocks.current.mockResolvedValueOnce({ ...task, state: 'installing' }).mockResolvedValue({ ...task, state: 'completed' })
  const w = mount(SettingsSystem); wrappers.push(w); await flushPromises(); await vi.advanceTimersByTimeAsync(1500); await flushPromises()
  expect(w.get('[role="alert"]').text()).toBe(i18n.global.t('settingsSystem.errReadVersion'))
  expect(mocks.version).toHaveBeenCalledTimes(2); expect(mocks.start).not.toHaveBeenCalled()
 })

})
