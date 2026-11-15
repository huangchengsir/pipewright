import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { access } from '../../api/access'
import { previewPacConfig } from '../../api/projects'
import { importPipeline } from '../../api/pipeline'
import { getPreviewConfig, listPreviewProviders, setPreviewConfig, type PreviewConfig } from '../../api/previewEnvs'
import { useSessionStore } from '../../stores/session'
import { usePermissionStore } from '../../stores/permissions'
import PacPreviewModal from './PacPreviewModal.vue'
import YamlImportModal from './YamlImportModal.vue'
import ProjectPreviewConfig from './ProjectPreviewConfig.vue'

vi.mock('../../api/access',()=>({access:{decisions:vi.fn()}}))
vi.mock('../../api/projects',()=>({previewPacConfig:vi.fn()}))
vi.mock('../../api/pipeline',()=>({importPipeline:vi.fn()}))
vi.mock('../../api/previewEnvs',()=>({getPreviewConfig:vi.fn(), listPreviewProviders:vi.fn(), setPreviewConfig:vi.fn()}))
let pinia: ReturnType<typeof createPinia>
let wrapper: VueWrapper | undefined
let allowed: Set<string>
const projectId='fixture-project'
const config: PreviewConfig = { projectId, enabled: false, dnsProviderId: '', baseDomain: 'fixture.invalid' }
const expectedOptions = () => expect.objectContaining({headers:{'X-Pipewright-Team':'team-a'},signal:expect.any(AbortSignal)})
async function settle(){for(let i=0;i<4;i++)await flushPromises()}
beforeEach(()=>{
  vi.resetAllMocks()
  pinia=createPinia();setActivePinia(pinia)
  useSessionStore().setUser({username:'employee',userId:'u',multiTenant:true})
  usePermissionStore().setTeam('team-a')
  allowed=new Set(['pipeline.read','pipeline.edit'])
  vi.mocked(access.decisions).mockImplementation(async(team,queries)=>({
    userId:'u',teamId:team,modeEpoch:1,revision:1,catalogVersion:1,
    allowed:queries.map(q=>q.resourceId===projectId&&allowed.has(q.action)),
  }))
  vi.mocked(previewPacConfig).mockResolvedValue({found:true,valid:true,ref:'fixture-main',file:'.pipewright.yml',error:'',stageCount:1,stages:[{name:'Read-only fixture stage',kind:'build',jobCount:1}]})
  vi.mocked(getPreviewConfig).mockResolvedValue(config)
  vi.mocked(listPreviewProviders).mockResolvedValue([])
})
afterEach(()=>{wrapper?.unmount();wrapper=undefined})

it.each([['header','.pp-head .pp-close'],['footer','.pp-foot button.pp-btn']] as const)(
  'closes the current repository preview via %s without another fetch or change',
  async (_,selector)=>{
    wrapper=mount(PacPreviewModal,{props:{projectId,defaultBranch:'fixture-main'},global:{plugins:[pinia]}})
    await settle()
    expect(wrapper.text()).toContain('Read-only fixture stage')
    await wrapper.get(selector).trigger('click')
    expect(wrapper.emitted('close')).toEqual([[]])
    expect(previewPacConfig).toHaveBeenCalledExactlyOnceWith(projectId,'fixture-main',expectedOptions())
    expect(importPipeline).not.toHaveBeenCalled()
  },
)

it('closes the YAML editor from its header without importing its local text', async()=>{
  wrapper=mount(YamlImportModal,{props:{projectId},global:{plugins:[pinia]}})
  await settle()
  await wrapper.get('textarea').setValue('version: 1')
  await wrapper.get('.yi-head .yi-close').trigger('click')
  expect(wrapper.emitted('close')).toEqual([[]])
  expect(wrapper.emitted('preview')).toBeUndefined()
  expect(wrapper.emitted('saved')).toBeUndefined()
  expect(importPipeline).not.toHaveBeenCalled()
})

it('loads the YAML starter into the local textarea without previewing, importing or closing',async()=>{
  wrapper=mount(YamlImportModal,{props:{projectId},global:{plugins:[pinia]}})
  await settle()
  const input=wrapper.get('textarea')
  await input.setValue('preexisting draft')
  await wrapper.get('button.yi-starter').trigger('click')
  const actual=(input.element as HTMLTextAreaElement).value
  expect(actual).toMatch(/^version: 1\nstages:/)
  expect(actual).toContain('type: git_source')
  expect(actual).not.toContain('preexisting draft')
  expect(importPipeline).not.toHaveBeenCalled()
  expect(wrapper.emitted('preview')).toBeUndefined()
  expect(wrapper.emitted('saved')).toBeUndefined()
  expect(wrapper.emitted('close')).toBeUndefined()
})

it('retries the selected project preview configuration and rejects a retained retry after read revocation',async()=>{
  const get=vi.mocked(getPreviewConfig)
  get.mockRejectedValueOnce(new Error('fixture offline'))
  wrapper=mount(ProjectPreviewConfig,{props:{projectId},global:{plugins:[pinia],stubs:{AppSelect:true}}})
  await settle()
  const old=wrapper.get('button.pvc__retry')
  let finish!:(cfg:PreviewConfig)=>void
  get.mockReturnValueOnce(new Promise(resolve=>{finish=resolve}))
  await old.trigger('click')
  await settle()
  expect(get).toHaveBeenCalledTimes(2)
  expect(get).toHaveBeenNthCalledWith(2,projectId,expectedOptions())
  expect(listPreviewProviders).toHaveBeenNthCalledWith(2,projectId,expectedOptions())
  const signal=get.mock.calls[1]![1]!.signal!
  usePermissionStore().reject('team-a',{action:'pipeline.read',resourceId:projectId})
  old.element.dispatchEvent(new MouseEvent('click',{bubbles:true}))
  finish(config)
  await settle()
  expect(signal.aborted).toBe(true)
  expect(get).toHaveBeenCalledTimes(2)
  expect((wrapper.get('.pvc__in').element as HTMLInputElement).value).toBe('')
  expect(setPreviewConfig).not.toHaveBeenCalled()
})
