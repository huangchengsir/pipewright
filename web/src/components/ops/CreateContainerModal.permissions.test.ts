import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, afterEach, expect, it, vi } from 'vitest'
import CreateContainerModal from './CreateContainerModal.vue'
import { createContainer } from '../../api/containers'
import { suggestCommand } from '../../api/aiOps'
import { access } from '../../api/access'
import { usePermissionStore } from '../../stores/permissions'
import { useSessionStore } from '../../stores/session'

vi.mock('../../api/containers', () => ({ createContainer: vi.fn() }))
vi.mock('../../api/aiOps', () => ({ suggestCommand: vi.fn() }))
vi.mock('../../api/access', () => ({ access: { decisions: vi.fn() } }))
const { success } = vi.hoisted(() => ({ success: vi.fn() }))
vi.mock('../../composables/useToast', () => ({ useToast: () => ({ success }) }))
type Created = Awaited<ReturnType<typeof createContainer>>
const created: Created = { ok: true, containerId: 'fixture-container', error: '', serverId: 'a' }
let w: VueWrapper | undefined
let grants: Set<string>
async function open() {
  w = mount(CreateContainerModal, {props:{servers:[{id:'a',name:'A'},{id:'b',name:'B'}]}})
  await flushPromises()
  return w
}
beforeEach(() => {
  vi.resetAllMocks()
  setActivePinia(createPinia())
  useSessionStore().setUser({username:'member',userId:'u',multiTenant:true})
  usePermissionStore().setTeam('team-a')
  grants = new Set(['container.operate/a'])
  vi.mocked(access.decisions).mockImplementation(async(team, queries) => ({
    teamId: team,userId:'u',modeEpoch:1,revision:1,catalogVersion:1,
    allowed:queries.map(q=>team==='team-a' && grants.has(q.action+'/'+q.resourceId)),
  }))
  vi.mocked(createContainer).mockResolvedValue(created)
})
afterEach(() => {w?.unmount();w=undefined})

it('creates only on the selected authorized server through the real button', async()=>{
  const view=await open()
  await view.get('input[placeholder*="nginx"]').setValue('fixture-image')
  const submit=view.get('.btn--primary')
  await submit.trigger('click')
  await flushPromises()
  expect(createContainer).toHaveBeenCalledExactlyOnceWith('a',expect.objectContaining({image:'fixture-image'}),expect.objectContaining({headers:{'X-Pipewright-Team':'team-a'},signal:expect.any(AbortSignal)}))
  expect(view.emitted('created')).toEqual([['a']])
  expect(view.emitted('close')).toHaveLength(1)
})

it('drops a response after target selection changes and denies the retained submit', async()=>{
  const view=await open()
  await view.get('input[placeholder*="nginx"]').setValue('fixture-image')
  let finish!:(r:Created)=>void
  vi.mocked(createContainer).mockReturnValueOnce(new Promise(r=>{finish=r}))
  const submit=view.get('.btn--primary')
  await submit.trigger('click')
  const options=vi.mocked(createContainer).mock.calls[0]![2]!
  await view.get('select').setValue('b')
  await flushPromises()
  submit.element.dispatchEvent(new MouseEvent('click',{bubbles:true}))
  expect(options.signal?.aborted).toBe(true)
  finish(created)
  await flushPromises()
  expect(createContainer).toHaveBeenCalledTimes(1)
  expect(view.emitted('created')).toBeUndefined()
  expect(view.emitted('close')).toBeUndefined()
  expect(success).not.toHaveBeenCalled()
})

it('uses the independent AI decision and discards a late suggestion after withdrawal', async()=>{
  grants.add('container.diagnose/a')
  const view=await open()
  await view.get('.ai-gen__in').setValue('fixture description')
  let finish!:(value:Awaited<ReturnType<typeof suggestCommand>>)=>void
  vi.mocked(suggestCommand).mockReturnValueOnce(new Promise(r=>{finish=r}))
  await view.get('.ai-gen__btn').trigger('click')
  const options=vi.mocked(suggestCommand).mock.calls[0]![2]!
  usePermissionStore().reject('team-a',{action:'container.diagnose',resourceId:'a'})
  finish({available:true,command:'docker run fixture-image',explanation:'',risk:'safe',reason:'',generatedAt:''})
  await flushPromises()
  expect(options.signal?.aborted).toBe(true)
  expect(view.find('.ai-gen').exists()).toBe(false)
  expect((view.get('input[placeholder*="nginx"]').element as HTMLInputElement).value).toBe('')
  expect(createContainer).not.toHaveBeenCalled()
})

it('dismisses from the backdrop, header and footer without creating a container', async () => {
  const view = await open()
  for (const selector of ['.modal-scrim', '.modal__close', '.btn--ghost']) {
    await view.get(selector).trigger('click')
  }
  expect(view.emitted('close')).toHaveLength(3)
  expect(createContainer).not.toHaveBeenCalled()
  expect(suggestCommand).not.toHaveBeenCalled()
})
