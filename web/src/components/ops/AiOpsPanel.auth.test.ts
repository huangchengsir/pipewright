import { afterEach, describe, expect, it, vi } from 'vitest'
import { shallowRef } from 'vue'
import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import AiOpsPanel from './AiOpsPanel.vue'
import AiOpsMessageList from './AiOpsMessageList.vue'
import CopyTextDialog from './CopyTextDialog.vue'
import AiOpsComposer from './AiOpsComposer.vue'
import { copyText } from '../../utils/clipboard'
import { resetOpsAuth } from '../../utils/opsAuth'
const panelState = vi.hoisted(() => ({
  stop: vi.fn().mockResolvedValue(true),
  confirm: vi.fn(),
  session: undefined as unknown as {
    value: { id: string; activeRunId: string } | null
  },
}))
vi.mock('../../composables/useConfirm', () => ({
  useConfirm: () => ({ open: panelState.confirm }),
}))
vi.mock('vue-router', () => ({
  useRouter: () => ({ resolve: () => ({ href: '/terminal' }) }),
  onBeforeRouteLeave: vi.fn(),
}))
vi.mock('../../utils/clipboard', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../utils/clipboard')>()),
  copyText: vi.fn(),
}))
vi.mock('../../composables/useOpsChat', () => ({
  useOpsChat: () => {
    const value = {
      state: { sessions: [], currentId: 'chat', entries: [], calls: {} },
      capabilities: shallowRef({ available: true, modelAvailable: false }),
      session: shallowRef(null),
      activeRun: shallowRef(null),
      draft: shallowRef(''),
      draftStatus: shallowRef('saved'),
      busy: shallowRef(false),
      loading: shallowRef(false),
      error: shallowRef(''),
      preview: shallowRef(null),
      pending: shallowRef(null),
      servers: shallowRef([]),
      tools: shallowRef([]),
      confirmations: shallowRef([]),
      historyCursor: shallowRef(''),
      close: vi.fn().mockResolvedValue(true),
      discard: vi.fn(),
      stop: panelState.stop,
    }
    panelState.session = value.session
    return value
  },
}))
let w: VueWrapper | undefined
afterEach(() => {
  w?.unmount()
  w = undefined
  resetOpsAuth()
  vi.clearAllMocks()
})
describe('ops output copies and authentication', () => {
  it('does not cancel a new run that arrived while confirming an old run', async () => {
    let approve!: (value: boolean) => void
    panelState.confirm.mockReturnValueOnce(
      new Promise<boolean>((r) => {
        approve = r
      }),
    )
    w = mount(AiOpsPanel, { attachTo: document.body })
    panelState.session.value = { id: 'chat', activeRunId: 'old' }
    await flushPromises()
    w.getComponent(AiOpsComposer).vm.$emit('stop')
    await flushPromises()
    panelState.session.value = { id: 'chat', activeRunId: 'new' }
    approve(true)
    await flushPromises()
    expect(panelState.stop).not.toHaveBeenCalled()
  })
  it('passes the clicked run ID explicitly after confirmation', async () => {
    panelState.confirm.mockResolvedValueOnce(true)
    w = mount(AiOpsPanel, { attachTo: document.body })
    panelState.session.value = { id: 'chat', activeRunId: 'old' }
    await flushPromises()
    w.getComponent(AiOpsComposer).vm.$emit('stop')
    await flushPromises()
    expect(panelState.stop).toHaveBeenCalledWith('old')
  })
  it('clears an already-open manual copy dialog on authentication loss', async () => {
    vi.mocked(copyText).mockResolvedValueOnce(false)
    w = mount(AiOpsPanel, { attachTo: document.body })
    w.getComponent(AiOpsMessageList).vm.$emit('copy', 'private output')
    await flushPromises()
    expect(w.findComponent(CopyTextDialog).exists()).toBe(true)
    resetOpsAuth()
    await flushPromises()
    expect(w.findComponent(CopyTextDialog).exists()).toBe(false)
    expect(document.body.textContent).not.toContain('private output')
  })
  it('ignores a late clipboard failure after authentication loss', async () => {
    let resolve!: (copied: boolean) => void
    vi.mocked(copyText).mockReturnValueOnce(
      new Promise<boolean>((r) => {
        resolve = r
      }),
    )
    w = mount(AiOpsPanel, { attachTo: document.body })
    w.getComponent(AiOpsMessageList).vm.$emit('copy', 'late private output')
    resetOpsAuth()
    resolve(false)
    await flushPromises()
    expect(w.findComponent(CopyTextDialog).exists()).toBe(false)
    expect(document.body.textContent).not.toContain('late private output')
  })
})
