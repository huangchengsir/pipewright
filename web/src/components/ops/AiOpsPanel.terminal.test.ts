import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { shallowRef } from 'vue'
import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import AiOpsPanel from './AiOpsPanel.vue'
import AiOpsComposer from './AiOpsComposer.vue'
import { resetOpsAuth } from '../../utils/opsAuth'
const m = vi.hoisted(() => ({
  chat: undefined as unknown as ReturnType<typeof mockChat>,
}))
function mockChat() {
  return {
    state: {
      sessions: [],
      currentId: 'chat',
      entries: [],
      calls: {},
      optimistic: [],
    },
    capabilities: shallowRef({
      available: true,
      modelAvailable: true,
      provider: { provider: 'fake', model: 'fixture', configHash: 'hash' },
    }),
    session: shallowRef<{
      id: string
      serverIds: string[]
      activeRunId?: string
    } | null>({ id: 'chat', serverIds: ['a'] }),
    activeRun: shallowRef(null),
    draft: shallowRef('existing question'),
    draftStatus: shallowRef('saved'),
    busy: shallowRef(false),
    loading: shallowRef(false),
    error: shallowRef(''),
    pending: shallowRef<unknown>(null),
    servers: shallowRef([]),
    tools: shallowRef([]),
    confirmations: shallowRef([]),
    historyCursor: shallowRef(''),
    close: vi.fn().mockResolvedValue(true),
    send: vi.fn().mockResolvedValue(true),
    editDraft: vi.fn(),
    discard: vi.fn(),
    stop: vi.fn(),
  }
}
vi.mock('vue-router', () => ({
  useRouter: () => ({ resolve: () => ({ href: '/terminal' }) }),
  onBeforeRouteLeave: vi.fn(),
}))
vi.mock('../../composables/useOpsChat', () => ({ useOpsChat: () => m.chat }))
let view: VueWrapper | undefined
interface Exposed {
  terminalTarget: () => string | null
  attachTerminal: (text: string, binding: string) => boolean
  prepareTerminal: (epoch: string, auth: number) => Promise<string | null>
}
const expose = () => view!.vm as unknown as Exposed
beforeEach(() => {
  m.chat = mockChat()
})
afterEach(() => {
  view?.unmount()
  view = undefined
  resetOpsAuth()
  vi.useRealTimers()
})
function open() {
  view = mount(AiOpsPanel, {
    props: { initialServerIds: ['a'], terminalEpoch: 'gen1' },
  })
  return expose()
}
describe('terminal memory attachment scope', () => {
  it('attaches without saving draft or submitting and sends only after composer click', async () => {
    const api = open()
    const binding = api.terminalTarget()!
    expect(binding).not.toBeNull()
    expect(api.attachTerminal('sanitized output', binding)).toBe(true)
    await flushPromises()
    expect(m.chat.editDraft).not.toHaveBeenCalled()
    expect(m.chat.send).not.toHaveBeenCalled()
    expect(view!.find('[data-testid="terminal-reference"]').exists()).toBe(true)
    view!.getComponent(AiOpsComposer).vm.$emit('send')
    await flushPromises()
    expect(m.chat.send).toHaveBeenCalledWith(
      expect.objectContaining({
        text: 'sanitized output',
        valid: expect.any(Function),
      }),
    )
    expect(view!.find('[data-testid="terminal-reference"]').exists()).toBe(
      false,
    )
  })
  it.each(['target', 'session', 'provider', 'connection', 'auth'])(
    'discards a reference on %s change',
    async (kind) => {
      const api = open()
      api.attachTerminal('old output', api.terminalTarget()!)
      await flushPromises()
      if (kind === 'target')
        m.chat.session.value = { id: 'chat', serverIds: ['b'] }
      else if (kind === 'session')
        m.chat.session.value = { id: 'other', serverIds: ['a'] }
      else if (kind === 'provider')
        m.chat.capabilities.value = {
          ...m.chat.capabilities.value,
          provider: { provider: 'fake', model: 'different', configHash: 'new' },
        }
      else if (kind === 'connection')
        await view!.setProps({ terminalEpoch: 'gen2' })
      else resetOpsAuth()
      await flushPromises()
      expect(view!.find('[data-testid="terminal-reference"]').exists()).toBe(
        false,
      )
      expect(m.chat.send).not.toHaveBeenCalled()
    },
  )
  it('does not accept another target or UTF8 overflow', () => {
    const api = open()
    expect(api.attachTerminal('old output', 'wrong binding')).toBe(false)
    expect(api.attachTerminal('中'.repeat(2800), api.terminalTarget()!)).toBe(
      false,
    )
    m.chat.session.value = { id: 'chat', serverIds: ['b'] }
    expect(api.terminalTarget()).toBeNull()
  })
  it('waits for first assistant restore, then returns the same connection binding', async () => {
    vi.useFakeTimers()
    m.chat.loading.value = true
    m.chat.session.value = null
    const api = open()
    const { opsAuthEpoch } = await import('../../utils/opsAuth')
    const ready = api.prepareTerminal('gen1', opsAuthEpoch())
    m.chat.session.value = { id: 'new', serverIds: ['a'] }
    m.chat.loading.value = false
    await flushPromises()
    expect(await ready).toBe(api.terminalTarget())
  })
  it('cancels initialization wait on unmount', async () => {
    m.chat.loading.value = true
    m.chat.session.value = null
    const api = open()
    const { opsAuthEpoch } = await import('../../utils/opsAuth')
    const ready = api.prepareTerminal('gen1', opsAuthEpoch())
    view!.unmount()
    view = undefined
    expect(await ready).toBeNull()
  })
  it.each(['busy', 'pending', 'activeRun'])(
    'wakes prepareTerminal when %s clears after restore/select',
    async (kind) => {
      vi.useFakeTimers()
      m.chat.loading.value = true
      m.chat.busy.value = true
      m.chat.session.value = null
      const api = open()
      const { opsAuthEpoch } = await import('../../utils/opsAuth')
      let settled = false
      const ready = api
        .prepareTerminal('gen1', opsAuthEpoch())
        .then((target) => {
          settled = true
          return target
        })
      m.chat.session.value = {
        id: 'new',
        serverIds: ['a'],
        activeRunId: kind === 'activeRun' ? 'old-run' : '',
      }
      m.chat.pending.value =
        kind === 'pending' ? { requestId: 'waiting' } : null
      m.chat.loading.value = false
      if (kind !== 'busy') m.chat.busy.value = false
      await flushPromises()
      expect(settled).toBe(false)
      if (kind === 'busy') m.chat.busy.value = false
      else if (kind === 'pending') m.chat.pending.value = null
      else
        m.chat.session.value = { id: 'new', serverIds: ['a'], activeRunId: '' }
      await flushPromises()
      expect(await ready).toBe(api.terminalTarget())
      expect(settled).toBe(true)
    },
  )
})
