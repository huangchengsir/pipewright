import { HttpError } from '../../api/http'
import { listServers } from '../../api/servers'
import { beforeEach, afterEach, describe, it, expect, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import AiOpsPanel from './AiOpsPanel.vue'
import AiOpsComposer from './AiOpsComposer.vue'
import AiOpsSessionList from './AiOpsSessionList.vue'
import { useSessionStore } from '../../stores/session'
import { usePermissionStore } from '../../stores/permissions'
import { useOpsChatStore } from '../../stores/opsChat'
import { access } from '../../api/access'
import { opsApi, createOpsApi } from '../../api/opsChat'
import { t } from '../../i18n'
import { resetOpsAuth } from '../../utils/opsAuth'
const resolveTerminal = vi.hoisted(() => vi.fn(() => ({ href: '/terminal' })))
vi.mock('vue-router',()=>({useRouter:()=>({resolve:resolveTerminal}),onBeforeRouteLeave:vi.fn()}))
vi.mock('../../api/servers',()=>({listServers:vi.fn().mockResolvedValue([])}))
vi.mock('../../api/access',async original=>({...await original<typeof import('../../api/access')>(),access:{decisions:vi.fn()}}))
vi.mock('../../api/opsChat',async original=>{
 const actual=await original<typeof import('../../api/opsChat')>()
 const mocked=Object.fromEntries(Object.keys(actual.opsApi).map(key=>[key,vi.fn()]))
 return {...actual,opsApi:mocked,createOpsApi:vi.fn(()=>mocked),streamOps:vi.fn(()=>new Promise(()=>{}))}
})
const session={id:'chat',title:'Private',revision:1,draft:'',serverIds:[],activeRunId:'',watermark:0,createdAt:'2026-11-13T00:00:00Z',updatedAt:'2026-11-13T00:00:00Z'}
const snapshot={session,runs:[],calls:[],confirmations:[],entries:{entries:[],cursor:'',watermark:0,reset:false},watermark:0}
let wrapper:VueWrapper|undefined
let allowed:Set<string>
beforeEach(()=>{
 setActivePinia(createPinia());vi.clearAllMocks();resetOpsAuth();useOpsChatStore().clear()
 useSessionStore().user={username:'member',userId:'member',multiTenant:true}
 usePermissionStore().setTeam('team-a')
 allowed=new Set(['opschat.read'])
 vi.mocked(access.decisions).mockImplementation(async(team,queries)=>({userId:'member',teamId:team,modeEpoch:1,revision:1,catalogVersion:1,allowed:queries.map(q=>allowed.has(q.action))}))
 vi.mocked(opsApi.capabilities).mockResolvedValue({available:true,modelAvailable:true,provider:{provider:'test',model:'test',configHash:'hash'},limits:{}})
 vi.mocked(opsApi.tools).mockResolvedValue([])
 vi.mocked(opsApi.list).mockResolvedValue({sessions:[session],activeSessionId:'chat',cursor:''})
 vi.mocked(opsApi.snapshot).mockResolvedValue(snapshot)
 vi.mocked(opsApi.activate).mockResolvedValue({ok:true})
})
afterEach(()=>{wrapper?.unmount();wrapper=undefined;resetOpsAuth();vi.restoreAllMocks()})
async function open(){wrapper=mount(AiOpsPanel);await flushPromises();await flushPromises()}
describe('assistant panel and per-call exact DOM controls', () => {
  const control = (key: string) => wrapper!.get(`button[aria-label="${t('opsChat.' + key)}"]`)
  const call = {
    callId: 'selected-call', runId: 'local-run', sessionId: 'chat', serverId: 'server-a', serverName: 'Fixture target',
    toolId: 'docker_logs', args: {}, object: 'fixture-container', status: 'failed' as const, argsHash: 'args',
    targetHash: 'binding', collectedAt: '2026-11-13T00:00:00Z', output: 'bounded local fixture text',
    error: 'fixture failure', truncated: false,
  }
  function addResult() {
    vi.mocked(opsApi.snapshot).mockResolvedValue({
      ...snapshot, calls: [call],
      entries: { ...snapshot.entries, entries: [{ sessionId: 'chat', seq: 1, kind: 'tool_call',
        runId: call.runId, callId: call.callId, createdAt: call.collectedAt }] },
    })
    vi.mocked(listServers).mockResolvedValue([{
      id: 'server-a', name: 'Fixture target', host: 'fixture.invalid', port: 22, user: 'fixture',
      credentialId: 'credential-a', credentialName: 'Fixture credential', createdAt: '', updatedAt: '',
    }])
  }
  async function expandedResult() {
    const details = wrapper!.get(`[data-call-id="${call.callId}"]`)
    ;(details.element as HTMLDetailsElement).open = true
    await details.trigger('toggle')
    return details
  }
  function expectNoAssistantWrite() {
    for (const key of ['turn', 'patch', 'create', 'cancel', 'confirm', 'retry', 'analyze'] as const)
      expect(opsApi[key]).not.toHaveBeenCalled()
  }

  it('opens and closes local history through its header button without a fetch or write', async () => {
    await open()
    const history = control('history')
    expect(history.attributes('aria-expanded')).toBe('false')
    await history.trigger('click')
    expect(history.attributes('aria-expanded')).toBe('true')
    expect(wrapper!.findComponent(AiOpsSessionList).exists()).toBe(true)
    await history.trigger('click')
    expect(history.attributes('aria-expanded')).toBe('false')
    expect(wrapper!.findComponent(AiOpsSessionList).exists()).toBe(false)
    expect(opsApi.list).toHaveBeenCalledOnce()
    expect(opsApi.entries).not.toHaveBeenCalled()
    expectNoAssistantWrite()
  })

  it('collapses the current assistant from its header without stopping or writing a clean session', async () => {
    await open()
    expect(wrapper!.text()).toContain('Private')
    await control('close').trigger('click')
    await flushPromises()
    expect(wrapper!.emitted('collapse')).toEqual([[]])
    expectNoAssistantWrite()
  })

  it('refreshes failed initial restore on the selected team without creating a session', async () => {
    vi.mocked(opsApi.capabilities).mockRejectedValueOnce(new Error('fixture disconnected'))
    await open()
    expect(wrapper!.find('.ops-feedback').exists()).toBe(true)
    expect(opsApi.list).not.toHaveBeenCalled()
    await control('refresh').trigger('click')
    await flushPromises(); await flushPromises()
    expect(opsApi.capabilities).toHaveBeenCalledTimes(2)
    expect(opsApi.list).toHaveBeenCalledExactlyOnceWith('containers')
    expect(opsApi.snapshot).toHaveBeenCalledExactlyOnceWith('chat')
    expect(wrapper!.text()).toContain('Private')
    expect(createOpsApi).toHaveBeenCalledWith(expect.objectContaining({headers:{'X-Pipewright-Team':'team-a'}}))
    expectNoAssistantWrite()
  })

  it('recovers the current session after a read failure without relisting or submitting', async () => {
    await open()
    vi.mocked(opsApi.capabilities).mockRejectedValueOnce(new HttpError(503, { code: 'ops_unavailable', message: 'fixture service unavailable' }, 'fixture service unavailable'))
    const active = wrapper!.vm as unknown as { $: { setupState: { chat: { recover(): Promise<boolean> } } } }
    await active.$.setupState.chat.recover()
    await flushPromises()
    expect(wrapper!.find('.ops-feedback').exists()).toBe(true)
    await control('refresh').trigger('click')
    await flushPromises(); await flushPromises()
    expect(wrapper!.find('.ops-feedback').exists()).toBe(false)
    expect(opsApi.capabilities).toHaveBeenCalledTimes(3)
    expect(opsApi.list).toHaveBeenCalledOnce()
    expect(opsApi.snapshot).toHaveBeenNthCalledWith(2, 'chat')
    expectNoAssistantWrite()
  })

  it('copies the current tool result through its own button without submit access or an API request', async () => {
    addResult()
    await open()
    const details = await expandedResult()
    expect(details.find(`button[aria-label="${t('opsChat.retryFailed')}"]`).exists()).toBe(false)
    expect(details.find(`button[aria-label="${t('opsChat.analyze')}"]`).exists()).toBe(false)
    const writeText = vi.fn().mockResolvedValue(undefined)
    vi.stubGlobal('navigator', Object.assign(Object.create(navigator), { clipboard: { writeText } }))
    try {
      await details.get(`button[aria-label="${t('opsChat.copy')}"]`).trigger('click')
      await flushPromises()
      expect(writeText).toHaveBeenCalledExactlyOnceWith(call.output)
      expectNoAssistantWrite()
      expect(opsApi.snapshot).toHaveBeenCalledOnce()
    } finally { vi.unstubAllGlobals() }
  })

  it.each([['retryFailed', 'retry'], ['analyze', 'analyze']] as const)(
    'dispatches exact %s call ID with submit scope and blocks the retained control after revocation',
    async (label, method) => {
      addResult()
      allowed.add('opschat.submit')
      await open()
      const details = await expandedResult()
      const button = details.get(`button[aria-label="${t('opsChat.' + label)}"]`)
      expect(button.attributes('disabled')).toBeUndefined()
      await button.trigger('click')
      await flushPromises(); await flushPromises()
      const expected = {
        callIds: [call.callId], revision: 1, clientRequestId: expect.any(String),
        ...(method === 'analyze' ? { analyzeResults: true,
          provider: { provider: 'test', model: 'test', configHash: 'hash' } } : {}),
      }
      expect(opsApi[method]).toHaveBeenCalledExactlyOnceWith('chat', expect.objectContaining(expected))
      expect(opsApi[method === 'analyze' ? 'retry' : 'analyze']).not.toHaveBeenCalled()
      usePermissionStore().reject('team-a',{ action: 'opschat.submit', resourceId: 'chat' })
      button.element.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await flushPromises()
      expect(opsApi[method]).toHaveBeenCalledOnce()
      expect(details.find(`button[aria-label="${t('opsChat.' + label)}"]`).exists()).toBe(false)
    },
  )

  it('opens only the exact tool server terminal with both independent permissions and rechecks a retained click', async () => {
    addResult()
    allowed.add('server.terminal')
    await open()
    const details = await expandedResult()
    const terminal = () => details.find(`button[aria-label="${t('opsChat.terminal')}"]`)
    expect(terminal().exists()).toBe(false)
    allowed.add('credential.use')
    usePermissionStore().invalidate()
    await flushPromises(); await flushPromises()
    const spy = vi.spyOn(window, 'open').mockReturnValue(null)
    const button = terminal()
    expect(button.exists()).toBe(true)
    await button.trigger('click')
    expect(resolveTerminal).toHaveBeenCalledExactlyOnceWith({ name: 'server-terminal', params: { id: 'server-a' } })
    expect(spy).toHaveBeenCalledExactlyOnceWith('/terminal', '_blank', 'noopener')
    usePermissionStore().reject('team-a',{ action: 'credential.use', resourceId: 'credential-a' })
    button.element.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await flushPromises()
    expect(terminal().exists()).toBe(false)
    expect(spy).toHaveBeenCalledOnce()
    expectNoAssistantWrite()
  })
})

it('removes the terminal reference via its exact button without saving or submitting', async () => {
  vi.mocked(opsApi.snapshot).mockResolvedValue({ ...snapshot, session: { ...session, serverIds: ['server-a'] } })
  wrapper = mount(AiOpsPanel, { props: { initialServerIds: ['server-a'], terminalEpoch: 'fixture-binding' } })
  await flushPromises(); await flushPromises()
  const api = wrapper.vm as unknown as {
    terminalTarget(): string | null
    attachTerminal(text: string, binding: string): boolean
  }
  expect(api.terminalTarget()).not.toBeNull()
  expect(api.attachTerminal('fixture-only terminal output', api.terminalTarget()!)).toBe(true)
  await flushPromises()
  const reference = wrapper!.get('[data-testid="terminal-reference"]')
  expect(reference.get('pre').text()).toBe('fixture-only terminal output')
  await reference.get('button').trigger('click')
  expect(wrapper!.find('[data-testid="terminal-reference"]').exists()).toBe(false)
  expect(opsApi.patch).not.toHaveBeenCalled()
  expect(opsApi.turn).not.toHaveBeenCalled()
  expect(wrapper!.getComponent(AiOpsComposer).props('contextText')).toBeUndefined()
})
