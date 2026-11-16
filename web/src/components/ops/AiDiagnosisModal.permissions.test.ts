import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, afterEach, expect, it, vi } from 'vitest'
import AiDiagnosisModal from './AiDiagnosisModal.vue'
import { diagnoseContainer, type ContainerDiagnosis } from '../../api/containers'
import { access } from '../../api/access'
import { usePermissionStore } from '../../stores/permissions'
import { useSessionStore } from '../../stores/session'

vi.mock('../../api/access',()=>({access:{decisions:vi.fn()}}))
vi.mock('../../api/containers',()=>({diagnoseContainer:vi.fn()}))
const { success, error }=vi.hoisted(()=>({success:vi.fn(),error:vi.fn()}))
vi.mock('../../composables/useToast',()=>({useToast:()=>({success,error})}))
let w: VueWrapper | undefined
const diag:ContainerDiagnosis={
  status:'ready',reason:'',hypothesis:'A scoped finding',confidence:'high',
  fixSuggestions:[],alternateCauses:[],evidence:[],fixScript:'echo fixture',generatedAt:'',
}
beforeEach(()=>{
  vi.resetAllMocks()
  setActivePinia(createPinia())
  useSessionStore().setUser({username:'member',userId:'u',multiTenant:true})
  usePermissionStore().setTeam('a')
  vi.mocked(access.decisions).mockImplementation(async(team, queries)=>({
    userId:'u',teamId:team,modeEpoch:1,revision:1,catalogVersion:1,
    allowed:queries.map(q=>team==='a'&&q.action==='container.diagnose'&&q.resourceId==='server'),
  }))
  vi.mocked(diagnoseContainer).mockResolvedValue(diag)
})
afterEach(()=>{w?.unmount();w=undefined;vi.restoreAllMocks()})
async function open() {
  w=mount(AiDiagnosisModal,{props:{serverId:'server',containerName:'app'}})
  await flushPromises()
  return w
}

it('reruns the selected diagnosis through its button and aborts on action revocation',async()=>{
  const view=await open()
  const rerun=view.get('.btn--ghost')
  let finish!:(v:ContainerDiagnosis)=>void
  vi.mocked(diagnoseContainer).mockReturnValueOnce(new Promise(r=>{finish=r}))
  await rerun.trigger('click')
  expect(diagnoseContainer).toHaveBeenLastCalledWith('server','app',expect.objectContaining({headers:{'X-Pipewright-Team':'a'},signal:expect.any(AbortSignal)}))
  const options=vi.mocked(diagnoseContainer).mock.calls.at(-1)![2]!
  usePermissionStore().reject('a',{action:'container.diagnose',resourceId:'server'})
  rerun.element.dispatchEvent(new MouseEvent('click',{bubbles:true}))
  finish(diag)
  await flushPromises()
  expect(options.signal?.aborted).toBe(true)
  expect(diagnoseContainer).toHaveBeenCalledTimes(2)
  expect(view.text()).not.toContain('A scoped finding')
})

it('copies only the current diagnosis and suppresses late feedback after withdrawal',async()=>{
  const view=await open()
  let finish!:()=>void
  const writeText=vi.fn().mockResolvedValueOnce(undefined).mockImplementationOnce(()=>new Promise<void>(r=>{finish=r}))
  vi.stubGlobal('navigator', Object.assign(Object.create(navigator),{clipboard:{writeText}}))
  try {
    const copy=view.get('.copy')
    await copy.trigger('click'); await flushPromises()
    expect(writeText).toHaveBeenCalledWith('echo fixture')
    expect(success).toHaveBeenCalledOnce()
    await copy.trigger('click')
    usePermissionStore().reject('a',{action:'container.diagnose',resourceId:'server'})
    copy.element.dispatchEvent(new MouseEvent('click',{bubbles:true}))
    finish()
    await flushPromises()
    expect(writeText).toHaveBeenCalledTimes(2)
    expect(success).toHaveBeenCalledOnce()
    expect(error).not.toHaveBeenCalled()
  } finally {
    vi.unstubAllGlobals()
  }
})
