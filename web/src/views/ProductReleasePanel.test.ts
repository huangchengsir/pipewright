import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import ProductReleasePanel from './ProductReleasePanel.vue'
import { HttpError } from '../api/http'
import {
  advanceRelease, confirmRelease, getReleaseOptions, listProductReleases,
  previewRelease, reconcileRelease, type ReleaseBatch, type ReleasePlan,
} from '../api/productReleases'
import { setLocale } from '../i18n'

vi.mock('../api/productReleases', () => ({
  advanceRelease: vi.fn(), confirmRelease: vi.fn(), getReleaseOptions: vi.fn(),
  listProductReleases: vi.fn(), previewRelease: vi.fn(), reconcileRelease: vi.fn(),
}))
vi.mock('../composables/useAuthorizedRequestScope', () => ({
  useIdentityRequestScope: () => ({
    active: () => true,
    run: (work: () => Promise<unknown>) => work(),
    requestOptions: {},
  }),
}))

const sha = 'b'.repeat(40)
const snapshotRef = '806aeb60-cb07-4d75-863e-7517a0158d49'
const plan: ReleasePlan = { productId: 'shop', items: [{
  applicationId: 'web', environment: 'staging', commitSha: sha, targetIds: ['web-1'],
  configDigest: 'a'.repeat(64), snapshotRef,
}] }
const batch: ReleaseBatch = {
  id: 'batch', productId: 'shop', plan, revision: 1, state: 'pending',
  items: [{ applicationId: 'web', state: 'pending' }], createdAt: 1795230000000, updatedAt: 1795230000000,
}
const running: ReleaseBatch = {
  ...batch, state: 'running', revision: 3, items: [{ applicationId: 'web', state: 'running', runId: 'run-1' }],
}
let wrapper: VueWrapper | undefined
const dialogPrototype = HTMLDialogElement.prototype
const originalShow = Object.getOwnPropertyDescriptor(dialogPrototype, 'showModal')
const originalClose = Object.getOwnPropertyDescriptor(dialogPrototype, 'close')

async function render() {
  const panel = mount(ProductReleasePanel, { attachTo: document.body, props: { productId: 'shop', productName: 'Shop' }, global: { stubs: { RouterLink: true } } })
  wrapper = panel
  await flushPromises()
  return panel
}

beforeEach(() => {
  vi.resetAllMocks()
  setLocale('en')
  vi.mocked(getReleaseOptions).mockResolvedValue({ applications: [
    { applicationId: 'web', name: 'Web', defaultBranch: 'main', environments: [
      { name: 'staging', targetIds: ['web-1'], targets: [{ id: 'web-1', name: 'Staging web' }] },
    ] },
    { applicationId: 'internal', name: 'Internal', defaultBranch: 'main', canRelease: false, environments: [] },
  ] })
  vi.mocked(listProductReleases).mockResolvedValue({ items: [] })
  vi.mocked(previewRelease).mockResolvedValue(plan)
  vi.mocked(confirmRelease).mockResolvedValue(batch)
  vi.mocked(advanceRelease).mockResolvedValue(running)
  Object.defineProperty(dialogPrototype, 'showModal', { configurable: true, value(this: HTMLDialogElement) { this.open = true } })
  Object.defineProperty(dialogPrototype, 'close', { configurable: true, value(this: HTMLDialogElement) { this.open = false } })
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  if (originalShow) Object.defineProperty(dialogPrototype, 'showModal', originalShow)
  else Reflect.deleteProperty(dialogPrototype, 'showModal')
  if (originalClose) Object.defineProperty(dialogPrototype, 'close', originalClose)
  else Reflect.deleteProperty(dialogPrototype, 'close')
  setLocale('zh-CN')
})

describe('product release', () => {
  it('shows the pinned plan and starts one item only after an explicit confirmation', async () => {
    const panel = await render()
    panel.vm.open()
    await flushPromises()
    const modal = panel.get('dialog')
    const [web, denied] = modal.findAll('input[type=checkbox]')
    expect(denied!.attributes('disabled')).toBeDefined()
    expect(modal.get('button[type=submit]').attributes('disabled')).toBeDefined()
    await web!.setValue(true)
    await modal.get('form').trigger('submit')
    await flushPromises()
    expect(previewRelease).toHaveBeenCalledWith('shop', [{ applicationId: 'web', branch: 'main', environment: 'staging', commitSha: undefined }], {})
    expect(modal.text()).toContain(sha)
    expect(modal.text()).toContain('Staging web')
    expect(advanceRelease).not.toHaveBeenCalled()
    await modal.get('form').trigger('submit')
    await flushPromises()
    expect(confirmRelease).toHaveBeenCalledExactlyOnceWith(snapshotRef, plan, {})
    expect(advanceRelease).toHaveBeenCalledExactlyOnceWith('batch', 1, {})
    expect(panel.get('.product-release__details').text()).toContain('Running')
    expect(panel.get('.product-release__details').text()).not.toContain('Start next application')
  })

  it('uses the same request ID if the confirmation response is lost', async () => {
    vi.mocked(confirmRelease).mockRejectedValueOnce(new HttpError(0, null, 'offline'))
    const panel = await render()
    panel.vm.open()
    await flushPromises()
    const modal = panel.get('dialog')
    await modal.get('input[type=checkbox]').setValue(true)
    await modal.get('form').trigger('submit')
    await flushPromises()
    await modal.get('form').trigger('submit')
    await flushPromises()
    expect(modal.attributes('open')).toBeDefined()
    expect(advanceRelease).not.toHaveBeenCalled()
    await modal.get('form').trigger('submit')
    await flushPromises()
    expect(vi.mocked(confirmRelease).mock.calls.map(call => call[0])).toEqual([snapshotRef, snapshotRef])
    expect(advanceRelease).toHaveBeenCalledOnce()
  })

  it('never launches the next application when a retried confirmation returns an existing batch', async () => {
    const continued = { ...batch, revision: 4 }
    vi.mocked(confirmRelease).mockResolvedValue(continued)
    const panel = await render()
    panel.vm.open()
    await flushPromises()
    const modal = panel.get('dialog')
    await modal.get('input[type=checkbox]').setValue(true)
    await modal.get('form').trigger('submit')
    await flushPromises()
    await modal.get('form').trigger('submit')
    await flushPromises()
    expect(advanceRelease).not.toHaveBeenCalled()
    expect(panel.get('.product-release__details button').text()).toBe('Start next application')
  })
  it('keeps selected applications while paging through available options', async () => {
    const stage = { name: 'staging', targetIds: ['server'] }
    vi.mocked(getReleaseOptions).mockImplementation(async (_product, page) => page === 1 ? {
      applications: [{ applicationId: 'web', name: 'Web', defaultBranch: 'main', environments: [stage] }], hasMore: true,
    } : {
      applications: [{ applicationId: 'api', name: 'API', defaultBranch: 'main', environments: [stage] }], hasMore: false,
    })
    const panel = await render()
    panel.vm.open()
    await flushPromises()
    const modal = panel.get('dialog')
    await modal.get('input[type=checkbox]').setValue(true)
    await modal.findAll('nav button').at(-1)!.trigger('click')
    await flushPromises()
    expect(getReleaseOptions).toHaveBeenLastCalledWith('shop', 2, {})
    await modal.get('input[type=checkbox]').setValue(true)
    await modal.get('form').trigger('submit')
    await flushPromises()
    expect(vi.mocked(previewRelease).mock.calls[0]?.[1].map(item => item.applicationId)).toEqual(['web', 'api'])
  })
  it('opens a published batch on the next history page without dispatching it', async () => {
    vi.mocked(listProductReleases)
      .mockResolvedValueOnce({ items: [], hasMore: true })
      .mockResolvedValueOnce({ items: [running], hasMore: false })
    const panel = await render()
    const next = panel.findAll('nav button').at(-1)!
    await next.trigger('click')
    await flushPromises()
    expect(listProductReleases).toHaveBeenLastCalledWith('shop', 2, {})
    await panel.get('.product-release__record').trigger('click')
    expect(panel.get('.product-release__details').text()).toContain('Running')
    expect(advanceRelease).not.toHaveBeenCalled()
  })
  it('only reconciles an unknown result and never dispatches it', async () => {
    const unknown = { ...running, state: 'needs_reconciliation', items: [{ applicationId: 'web', state: 'unknown', runId: 'run-1' }] }
    vi.mocked(listProductReleases).mockResolvedValue({ items: [unknown] })
    vi.mocked(reconcileRelease).mockResolvedValue({ ...unknown, revision: 4 })
    const panel = await render()
    await panel.get('.product-release__record').trigger('click')
    expect(panel.text()).toContain('Check the original run')
    await panel.get('.product-release__details button').trigger('click')
    await flushPromises()
    expect(reconcileRelease).toHaveBeenCalledExactlyOnceWith('batch', 3, {})
    expect(advanceRelease).not.toHaveBeenCalled()
  })
})
