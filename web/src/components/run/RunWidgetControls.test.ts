import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import { access } from '../../api/access'
import type { ParamDef } from '../../api/parameters'
import { getRunDiff, type ArtifactDTO, type DeployTarget, type DiagnosisDTO, type RunDiffDTO } from '../../api/runs'
import { usePermissionStore } from '../../stores/permissions'
import { useSessionStore } from '../../stores/session'
import RunParamsEditor from '../RunParamsEditor.vue'
import TypedRunParams from '../TypedRunParams.vue'
import ArtifactList from './ArtifactList.vue'
import DeployTargets from './DeployTargets.vue'
import DiagnosisPanel from './DiagnosisPanel.vue'
import SuccessFailDiff from './SuccessFailDiff.vue'

vi.mock('../../api/access', () => ({ access: { decisions: vi.fn() } }))
vi.mock('../../api/runs', async (importOriginal) => {
  const original = await importOriginal<typeof import('../../api/runs')>()
  return { ...original, getRunDiff: vi.fn(), diagnoseRun: vi.fn(), submitDiagnosisFeedback: vi.fn() }
})

const cleanup: Array<() => void> = []
const clipboard = vi.fn<(text: string) => Promise<void>>()
let readable: boolean
let previousClipboard: PropertyDescriptor | undefined

beforeEach(() => {
  setActivePinia(createPinia())
  useSessionStore().setUser({ username: 'employee', userId: 'fixture-user', multiTenant: true })
  usePermissionStore().setTeam('fixture-team')
  readable = true
  vi.mocked(access.decisions).mockImplementation(async (team, queries) => ({
    teamId: team,
    userId: 'fixture-user',
    catalogVersion: 1,
    modeEpoch: 1,
    revision: 1,
    allowed: queries.map(q => readable && team === 'fixture-team' && q.resourceId === 'run-1' && q.action === 'run.read'),
  }))
  vi.mocked(getRunDiff).mockReset()
  clipboard.mockReset().mockResolvedValue(undefined)
  previousClipboard = Object.getOwnPropertyDescriptor(navigator, 'clipboard')
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: clipboard } })
})
afterEach(() => {
  cleanup.splice(0).forEach(unmount => unmount())
  if (previousClipboard) Object.defineProperty(navigator, 'clipboard', previousClipboard)
  else Reflect.deleteProperty(navigator, 'clipboard')
})

const artifacts: ArtifactDTO[] = [
  { id: 'a1', type: 'archive', name: 'Synthetic artifact', reference: 'fixture://first', sizeBytes: 4, metadata: { stored: true }, createdAt: '' },
  { id: 'a2', type: 'archive', name: 'Another artifact', reference: 'fixture://second', sizeBytes: 5, metadata: { stored: true }, createdAt: '' },
]
const diagnosis: DiagnosisDTO = {
  status: 'ready', reason: '', hypothesis: 'Current fixture diagnosis', confidence: 'low',
  alternateCauses: ['Alternative fixture cause'], fixSuggestions: [], fixScript: 'echo fixture', evidence: [], generatedAt: '',
}
const failedTarget: DeployTarget = {
  serverId: 'server-failed', serverName: 'Failed fixture target', status: 'failed', message: 'fixture',
  startedAt: '2026-11-15T01:00:00Z', finishedAt: '2026-11-15T01:00:02Z',
}

describe('run evidence controls', () => {
  it('copies only the selected visible artifact reference for a metadata reader', async () => {
    const wrapper = mount(ArtifactList, { props: { runId: 'run-1', artifacts } })
    cleanup.push(() => wrapper.unmount())
    await flushPromises()
    expect(wrapper.find('.download-btn').exists()).toBe(false)
    const references = wrapper.findAll('.reference-row')
    expect(references).toHaveLength(2)
    await references[1].trigger('click')
    await flushPromises()
    expect(clipboard).toHaveBeenCalledExactlyOnceWith('fixture://second')
  })

  it('does not copy a removed or previous-team artifact reference from an old click', async () => {
    const wrapper = mount(ArtifactList, { props: { runId: 'run-1', artifacts } })
    cleanup.push(() => wrapper.unmount())
    await flushPromises()
    const old = wrapper.findAll('.reference-row')[0].element as HTMLButtonElement
    await wrapper.setProps({ artifacts: [artifacts[1]] })
    old.click()
    await nextTick()
    expect(clipboard).not.toHaveBeenCalled()

    const current = wrapper.get<HTMLButtonElement>('.reference-row').element
    usePermissionStore().setTeam('other-team')
    current.click()
    await flushPromises()
    expect(clipboard).not.toHaveBeenCalled()
  })

  it('copies only the visible diagnosis script and refuses clipboard use after read is withdrawn', async () => {
    const wrapper = mount(DiagnosisPanel, { props: { runId: 'run-1', diagnosis } })
    cleanup.push(() => wrapper.unmount())
    await flushPromises()
    const button = wrapper.get<HTMLButtonElement>('.dp-fixscript-copy')
    await button.trigger('click')
    await flushPromises()
    expect(clipboard).toHaveBeenCalledExactlyOnceWith('echo fixture')
    expect(button.classes()).toContain('dp-fixscript-copy--done')

    readable = false
    usePermissionStore().reject('fixture-team', { action: 'run.read', resourceId: 'run-1' })
    button.element.click()
    await flushPromises()
    expect(wrapper.find('.dp-fixscript-copy').exists()).toBe(false)
    expect(clipboard).toHaveBeenCalledTimes(1)
  })

  it('expands and collapses alternate causes without requesting a new diagnosis', async () => {
    const wrapper = mount(DiagnosisPanel, { props: { runId: 'run-1', diagnosis } })
    cleanup.push(() => wrapper.unmount())
    await flushPromises()
    const button = wrapper.get('.dp-alt-toggle')
    expect(button.attributes('aria-expanded')).toBe('false')
    expect(wrapper.find('.dp-alt-list').exists()).toBe(false)
    await button.trigger('click')
    expect(button.attributes('aria-expanded')).toBe('true')
    expect(wrapper.get('.dp-alt-list').text()).toContain('Alternative fixture cause')
    await button.trigger('click')
    expect(wrapper.find('.dp-alt-list').exists()).toBe(false)
    expect(wrapper.emitted('diagnosed')).toBeUndefined()
  })

  it('opens only the selected available redacted patch and makes no additional request when closing', async () => {
    const diff: RunDiffDTO = {
      available: true, reason: '', baselineRunId: 'baseline', baselineCommit: 'aaaaaaaaaa',
      currentCommit: 'bbbbbbbbbb', truncated: false, summary: '',
      files: [
        { path: 'with.txt', status: 'modified', additions: 1, deletions: 1, patch: '-old fixture\n+new fixture', patchTruncated: false },
        { path: 'without.bin', status: 'modified', additions: 0, deletions: 0, patch: '', patchTruncated: false },
      ],
    }
    vi.mocked(getRunDiff).mockResolvedValue(diff)
    const wrapper = mount(SuccessFailDiff, { props: { runId: 'run-1' } })
    cleanup.push(() => wrapper.unmount())
    await flushPromises()
    expect(getRunDiff).toHaveBeenCalledExactlyOnceWith('run-1', expect.objectContaining({
      headers: { 'X-Pipewright-Team': 'fixture-team' }, signal: expect.any(AbortSignal),
    }))
    const rows = wrapper.findAll<HTMLButtonElement>('.sfd-file')
    expect(rows).toHaveLength(2)
    expect(rows[1].element.disabled).toBe(true)
    rows[1].element.click()
    expect(wrapper.find('.sfd-patch').exists()).toBe(false)
    await rows[0].trigger('click')
    expect(rows[0].attributes('aria-expanded')).toBe('true')
    expect(wrapper.get('.sfd-patch').text()).toContain('+new fixture')
    await rows[0].trigger('click')
    expect(wrapper.find('.sfd-patch').exists()).toBe(false)
    expect(getRunDiff).toHaveBeenCalledTimes(1)
  })

  it('emits retry only for failed targets while the permission and availability remain current', async () => {
    const wrapper = mount(DeployTargets, { props: { targets: [failedTarget], retryAllowed: false } })
    cleanup.push(() => wrapper.unmount())
    expect(wrapper.find('.dt-retry-btn').exists()).toBe(false)
    await wrapper.setProps({ retryAllowed: true, retrying: true })
    const button = wrapper.get<HTMLButtonElement>('.dt-retry-btn')
    expect(button.element.disabled).toBe(true)
    button.element.click()
    expect(wrapper.emitted('retry')).toBeUndefined()

    await wrapper.setProps({ retrying: false })
    await button.trigger('click')
    expect(wrapper.emitted('retry')).toHaveLength(1)
    await wrapper.setProps({ retryAllowed: false })
    expect(wrapper.find('.dt-retry-btn').exists()).toBe(false)
    button.element.click()
    expect(wrapper.emitted('retry')).toHaveLength(1)
    await wrapper.setProps({ retryAllowed: true, targets: [{ ...failedTarget, status: 'success' }] })
    expect(wrapper.find('.dt-retry-btn').exists()).toBe(false)
  })
})

describe('run parameter draft controls', () => {
  it('adds a blank parameter without committing it and deletes only the chosen drafted key', async () => {
    const wrapper = mount(RunParamsEditor, { props: { modelValue: { RELEASE: 'test', KEEP: 'true' } } })
    cleanup.push(() => wrapper.unmount())
    await wrapper.get('.params-add').trigger('click')
    expect(wrapper.findAll('.params-row')).toHaveLength(3)
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    const last = wrapper.findAll('.params-row')[2]
    await last.get('.params-input--key').setValue(' EXTRA ')
    await last.get('.params-input--val').setValue('one')
    expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual([{ RELEASE: 'test', KEEP: 'true', EXTRA: 'one' }])

    await wrapper.findAll('.params-del')[0].trigger('click')
    expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual([{ KEEP: 'true', EXTRA: 'one' }])
    const add = wrapper.get<HTMLButtonElement>('.params-add').element
    const remove = wrapper.get<HTMLButtonElement>('.params-del').element
    const changes = wrapper.emitted('update:modelValue')?.length
    await wrapper.setProps({ disabled: true })
    expect(add.disabled && remove.disabled).toBe(true)
    add.click()
    remove.click()
    await nextTick()
    expect(wrapper.findAll('.params-row')).toHaveLength(2)
    expect(wrapper.emitted('update:modelValue')).toHaveLength(changes!)
  })

  it('toggles only the selected typed boolean parameter as a local string value', async () => {
    const defs: ParamDef[] = [
      { key: 'DRY_RUN', label: 'Dry run', type: 'boolean', required: true, default: 'false' },
      { key: 'CACHE', label: 'Cache', type: 'boolean', required: false, default: 'true' },
    ]
    const wrapper = mount(TypedRunParams, { props: { modelValue: {}, defs } })
    cleanup.push(() => wrapper.unmount())
    const dry = wrapper.get('#tp-DRY_RUN')
    expect(dry.attributes('aria-checked')).toBe('false')
    await dry.trigger('click')
    expect(dry.attributes('aria-checked')).toBe('true')
    expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual([{ DRY_RUN: 'true', CACHE: 'true' }])
    await dry.trigger('click')
    expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual([{ DRY_RUN: 'false', CACHE: 'true' }])
  })
})
