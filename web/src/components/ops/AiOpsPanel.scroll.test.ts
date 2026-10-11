import { afterEach, describe, expect, it, vi } from 'vitest'
import { nextTick, reactive, shallowRef } from 'vue'
import { mount, type VueWrapper } from '@vue/test-utils'
import type { Entry } from '../../api/opsChat'
import AiOpsPanel from './AiOpsPanel.vue'
import AiOpsComposer from './AiOpsComposer.vue'
import AiOpsMessageList from './AiOpsMessageList.vue'

const chatMock = vi.hoisted(() => ({
  send: vi.fn(),
  history: vi.fn(),
  state: undefined as unknown as { optimistic: Entry[] },
}))
vi.mock('vue-router', () => ({
  useRouter: () => ({ resolve: () => ({ href: '/terminal' }) }),
  onBeforeRouteLeave: vi.fn(),
}))
vi.mock('../../composables/useOpsChat', () => ({
  useOpsChat: () => {
    const state = reactive({
      sessions: [],
      currentId: 'chat',
      entries: [],
      calls: {},
      optimistic: [] as Entry[],
    })
    chatMock.state = state
    return {
      state,
      capabilities: shallowRef({ available: true, modelAvailable: true }),
      session: shallowRef({ id: 'chat', serverIds: ['server'] }),
      activeRun: shallowRef(null),
      draft: shallowRef('hello'),
      draftStatus: shallowRef('saved'),
      busy: shallowRef(false),
      loading: shallowRef(false),
      error: shallowRef(''),
      pending: shallowRef(null),
      servers: shallowRef([]),
      tools: shallowRef([]),
      confirmations: shallowRef([]),
      historyCursor: shallowRef('older'),
      send: chatMock.send,
      history: chatMock.history,
    }
  },
}))

let wrapper: VueWrapper | undefined
afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  vi.clearAllMocks()
})

function setupPanel() {
  wrapper = mount(AiOpsPanel, {
    attachTo: document.body,
    global: {
      stubs: {
        AiOpsComposer: true,
        AiOpsMessageList: true,
        AiOpsConfirmation: true,
        AiOpsServerPicker: true,
        AiOpsSessionList: true,
      },
    },
  })
  const viewport = wrapper.get('[data-testid="ops-messages-scroll"]')
    .element as HTMLElement
  const layout = { height: 1000 }
  Object.defineProperties(viewport, {
    scrollHeight: { get: () => layout.height },
    clientHeight: { get: () => 200 },
  })
  return { viewport, layout }
}

describe('AiOpsPanel scroll integration', () => {
  it('scrolls the own message into view without waiting for its request', async () => {
    const { viewport, layout } = setupPanel()
    await nextTick()
    await nextTick()
    viewport.scrollTop = 100
    viewport.dispatchEvent(new Event('scroll'))
    let complete!: (value: boolean) => void
    chatMock.send.mockImplementationOnce(() => {
      chatMock.state.optimistic.push({
        sessionId: 'chat',
        seq: -1,
        kind: 'user',
        text: 'hello',
        requestId: 'own-message',
        createdAt: '',
      } as Entry)
      layout.height = 1200
      return new Promise<boolean>((resolve) => {
        complete = resolve
      })
    })
    wrapper!.getComponent(AiOpsComposer).vm.$emit('send')
    await nextTick()
    await nextTick()
    expect(chatMock.send).toHaveBeenCalledOnce()
    expect(viewport.scrollTop).toBe(1000)
    complete(true)
  })

  it('wraps the older-message entry point in offset preservation', async () => {
    const { viewport, layout } = setupPanel()
    await nextTick()
    await nextTick()
    viewport.scrollTop = 200
    viewport.dispatchEvent(new Event('scroll'))
    chatMock.history.mockImplementationOnce(async () => {
      layout.height += 300
    })
    wrapper!.getComponent(AiOpsMessageList).vm.$emit('history')
    await nextTick()
    await nextTick()
    expect(chatMock.history).toHaveBeenCalledOnce()
    expect(viewport.scrollTop).toBe(500)
  })
})
