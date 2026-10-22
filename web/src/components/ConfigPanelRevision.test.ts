import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import ConcurrencyPanel from './ConcurrencyPanel.vue'
import { http } from '../api/http'
import { bindConfigRevision } from '../api/configRevision'

vi.mock('../api/http', async () => ({ ...await vi.importActual('../api/http'), http: { get: vi.fn(), put: vi.fn() } }))
const wrappers: VueWrapper[] = []
function deferred<T>() { let resolve!: (value: T) => void; const promise = new Promise<T>(yes => { resolve = yes }); return { promise, resolve } }
function render() { const wrapper = mount(ConcurrencyPanel, { props: { projectId: 'p1' } }); wrappers.push(wrapper); return wrapper }
describe('standalone configuration editor revision', () => {
  beforeEach(() => { vi.clearAllMocks(); vi.mocked(http.get).mockResolvedValue(bindConfigRevision({ maxConcurrent: 1 }, 'cfg-1')) })
  afterEach(() => { for (const wrapper of wrappers.splice(0)) wrapper.unmount() })
  it('uses its read baseline and advances only after its own acknowledged save', async () => {
    const wrapper = render(); await flushPromises()
    await wrapper.get('#conc-input').setValue(2)
    vi.mocked(http.put).mockResolvedValueOnce(bindConfigRevision({ maxConcurrent: 2 }, 'cfg-2'))
    await wrapper.get('.btn-primary').trigger('click'); await flushPromises()
    expect(http.put).toHaveBeenLastCalledWith('/api/projects/p1/concurrency', { maxConcurrent: 2 }, { headers: { 'If-Match': 'cfg-1' } })
    await wrapper.get('#conc-input').setValue(3)
    vi.mocked(http.put).mockResolvedValueOnce(bindConfigRevision({ maxConcurrent: 3 }, 'cfg-3'))
    await wrapper.get('.btn-primary').trigger('click'); await flushPromises()
    expect(http.put).toHaveBeenLastCalledWith('/api/projects/p1/concurrency', { maxConcurrent: 3 }, { headers: { 'If-Match': 'cfg-2' } })
    expect(http.get).toHaveBeenCalledTimes(1)
  })
  it('ignores an old-project read instead of rebinding a new-project form', async () => {
    const old = deferred<{ maxConcurrent: number }>()
    vi.mocked(http.get).mockReturnValueOnce(old.promise)
    const wrapper = render(); await flushPromises()
    vi.mocked(http.get).mockResolvedValueOnce(bindConfigRevision({ maxConcurrent: 2 }, 'cfg-4'))
    await wrapper.setProps({ projectId: 'p2' }); await flushPromises()
    old.resolve(bindConfigRevision({ maxConcurrent: 9 }, 'cfg-9')); await flushPromises()
    expect((wrapper.get('#conc-input').element as HTMLInputElement).value).toBe('2')
    vi.mocked(http.put).mockResolvedValueOnce(bindConfigRevision({ maxConcurrent: 2 }, 'cfg-5'))
    await wrapper.get('.btn-primary').trigger('click'); await flushPromises()
    expect(http.put).toHaveBeenLastCalledWith('/api/projects/p2/concurrency', { maxConcurrent: 2 }, { headers: { 'If-Match': 'cfg-4' } })
  })
})
