import {afterEach,beforeEach,describe,it,expect,vi} from 'vitest'
import {createPinia,setActivePinia} from 'pinia'
import {mount,flushPromises,type VueWrapper} from '@vue/test-utils'
import {useSessionStore} from '../../stores/session'
import {usePermissionStore} from '../../stores/permissions'
import {access} from '../../api/access'
import {listTemplates,applyTemplate,createTemplate} from '../../api/templates'
import {previewPacConfig} from '../../api/projects'
import {importPipeline} from '../../api/pipeline'
import {aiApply,aiGenerate,type AIApplyResponse,type AIGenerateResponse} from '../../api/aiGenerate'
import TemplatePickerModal from './TemplatePickerModal.vue'
import PacPreviewModal from './PacPreviewModal.vue'
import YamlImportModal from './YamlImportModal.vue'
import AIGenerateWizard from './AIGenerateWizard.vue'
vi.mock('../../api/access',()=>({access:{decisions:vi.fn()}}))
vi.mock('../../api/templates',()=>({listTemplates:vi.fn(),applyTemplate:vi.fn(),createTemplate:vi.fn()}))
vi.mock('../../api/projects',()=>({previewPacConfig:vi.fn()}))
vi.mock('../../api/pipeline',()=>({importPipeline:vi.fn()}))
vi.mock('../../api/aiGenerate',()=>({aiGenerate:vi.fn(),aiApply:vi.fn()}))
let wrapper:VueWrapper|undefined
let allowed:Set<string>
const dto={projectId:'p',stages:[],yaml:'',status:'draft',updatedAt:''}
const source={found:true,valid:true,ref:'main',file:'.pipewright.yml',error:'',stageCount:1,stages:[{name:'private-stage',kind:'build',jobCount:1}]}
async function settle(){for(let i=0;i<6;i++)await flushPromises()}
function revoke(){allowed.clear();usePermissionStore().invalidate()}
beforeEach(()=>{vi.resetAllMocks();setActivePinia(createPinia());useSessionStore().setUser({username:'member',userId:'u',multiTenant:true});usePermissionStore().setTeam('t');allowed=new Set(['pipeline.read:p','pipeline.edit:p','template.read:tpl','template.create:']);vi.mocked(access.decisions).mockImplementation(async(team,qs)=>({userId:'u',teamId:team,modeEpoch:1,revision:1,catalogVersion:1,allowed:qs.map(q=>allowed.has(q.action+':'+q.resourceId))}));vi.mocked(listTemplates).mockResolvedValue([{id:'tpl',name:'Template',description:'',stageCount:1,createdAt:'',updatedAt:''}]);vi.mocked(applyTemplate).mockResolvedValue(dto);vi.mocked(previewPacConfig).mockResolvedValue(source);vi.mocked(importPipeline).mockResolvedValue(dto)})
afterEach(()=>{wrapper?.unmount();wrapper=undefined})
describe('pipeline modal permissions and request identity',()=>{
 it('loads templates after asynchronous permission resolution and applies with fixed team',async()=>{wrapper=mount(TemplatePickerModal,{props:{projectId:'p',stages:[],beforeReplace:async()=>true}});await settle();expect(listTemplates).toHaveBeenCalledOnce();await wrapper.get('.btn-primary').trigger('click');await settle();expect(applyTemplate).toHaveBeenCalledExactlyOnceWith('p','tpl',undefined,expect.objectContaining({headers:{'X-Pipewright-Team':'t'},signal:expect.any(AbortSignal)}))})
 it('does not apply after edit permission is withdrawn while confirming',async()=>{let confirm!:(v:boolean)=>void;wrapper=mount(TemplatePickerModal,{props:{projectId:'p',stages:[],beforeReplace:()=>new Promise(r=>{confirm=r})}});await settle();await wrapper.get('.btn-primary').trigger('click');allowed.delete('pipeline.edit:p');usePermissionStore().invalidate();await settle();confirm(true);await settle();expect(applyTemplate).not.toHaveBeenCalled()})
 it('requires the selected template read permission, independently of project edit',async()=>{allowed.delete('template.read:tpl');wrapper=mount(TemplatePickerModal,{props:{projectId:'p',stages:[],beforeReplace:async()=>true}});await settle();for(const button of wrapper.findAll('.btn-primary'))await button.trigger('click');await settle();expect(applyTemplate).not.toHaveBeenCalled()})
 it('does not expose or fetch templates without pipeline read',async()=>{allowed.clear();wrapper=mount(TemplatePickerModal,{props:{projectId:'p',stages:[]}});await settle();expect(listTemplates).not.toHaveBeenCalled();expect(wrapper.find('[role="dialog"]').exists()).toBe(false)})
 it('starts PAC read only after authorization and hides a late result after team change',async()=>{let finish!:(v:typeof source)=>void;vi.mocked(previewPacConfig).mockImplementation(()=>new Promise(r=>{finish=r}));wrapper=mount(PacPreviewModal,{props:{projectId:'p',defaultBranch:'main'}});await settle();expect(previewPacConfig).toHaveBeenCalledExactlyOnceWith('p','main',expect.objectContaining({headers:{'X-Pipewright-Team':'t'}}));const options=vi.mocked(previewPacConfig).mock.calls[0][2]!;usePermissionStore().setTeam('other');finish(source);await settle();expect(options.signal?.aborted).toBe(true);expect(wrapper.text()).not.toContain('private-stage')})
 it('revokes PAC preview and prevents another fetch',async()=>{wrapper=mount(PacPreviewModal,{props:{projectId:'p',defaultBranch:'main'}});await settle();expect(wrapper.text()).toContain('private-stage');revoke();await settle();expect(wrapper.find('[role="dialog"]').exists()).toBe(false);expect(previewPacConfig).toHaveBeenCalledOnce()})
 it('blocks YAML import after edit is withdrawn during confirmation',async()=>{let confirm!:(v:boolean)=>void;wrapper=mount(YamlImportModal,{props:{projectId:'p',beforeReplace:()=>new Promise(r=>{confirm=r})}});await settle();await wrapper.get('textarea').setValue('version: 1');await wrapper.findAll('button').find(b=>b.text()==='导入并保存')!.trigger('click');revoke();await settle();confirm(true);await settle();expect(importPipeline).not.toHaveBeenCalled()})
 it('prevents YAML and AI invocation when only read is granted',async()=>{allowed.delete('pipeline.edit:p');wrapper=mount(YamlImportModal,{props:{projectId:'p'}});await settle();expect(wrapper.find('textarea').exists()).toBe(false);wrapper.unmount();wrapper=mount(AIGenerateWizard,{props:{projectId:'p'},global:{stubs:{RouterLink:true}}});await settle();expect(wrapper.find('.wiz-footer').exists()).toBe(false);expect(aiGenerate).not.toHaveBeenCalled();expect(importPipeline).not.toHaveBeenCalled()})
 it('discards a late model response after logout',async()=>{let finish!:(v:any)=>void;vi.mocked(aiGenerate).mockImplementation(()=>new Promise(r=>{finish=r}));wrapper=mount(AIGenerateWizard,{props:{projectId:'p'},global:{stubs:{RouterLink:true}}});await settle();await wrapper.findAll('.wiz-footer button').find(b=>b.text().includes('生成配置'))!.trigger('click');await settle();expect(aiGenerate).toHaveBeenCalledOnce();useSessionStore().clearSession();finish({available:true,reason:'private-model-result',analysis:{signals:[]},proposal:null});await settle();expect(wrapper.text()).not.toContain('private-model-result');expect(wrapper.find('.wiz-footer').exists()).toBe(false)})
})

describe('pipeline modal exact DOM actions', () => {
  const scopedOptions = () => expect.objectContaining({
    headers: { 'X-Pipewright-Team': 't' },
    signal: expect.any(AbortSignal),
  })
  const button = (selector: string, label: string) => wrapper!.findAll(selector).find(item => item.text() === label)!
  const proposal: AIGenerateResponse = {
    available: true, reason: '',
    analysis: { cloned: true, language: 'go', languageVersion: '', buildTool: '',
      hasDockerfile: false, artifactHint: '', signals: [] },
    proposal: {
      stages: [
        { id: 'stage-a', name: 'Stage A', kind: 'build', jobs: [] },
        { id: 'stage-b', name: 'Stage B', kind: 'build', jobs: [] },
      ],
      build: { model: 'dockerfile', toolchain: { language: 'go', version: '' }, artifactType: 'image', dockerfilePath: 'Dockerfile' },
      branchMappings: [{ id: 'mapping-a', branchPattern: 'main', environment: 'staging' }],
      rationale: '',
    },
  }

  async function mountAi() {
    vi.mocked(aiGenerate).mockResolvedValue(proposal)
    vi.mocked(aiApply).mockResolvedValue({ applied: { spec: true, settings: true, triggers: true } })
    wrapper = mount(AIGenerateWizard, { props: { projectId: 'p', baseRevision: 'cfg-1' }, global: { stubs: { RouterLink: true } } })
    await settle()
    await button('.wiz-footer button', '生成配置').trigger('click')
    await settle()
  }

  it('clicks PAC fetch with the selected branch after initial failure and blocks the retained button after read is revoked', async () => {
    vi.mocked(previewPacConfig).mockRejectedValueOnce(new Error('transient'))
    wrapper = mount(PacPreviewModal, { props: { projectId: 'p', defaultBranch: 'main' } })
    await settle()
    expect(wrapper.get('[role="alert"]').text()).not.toBe('')
    const fetch = wrapper.get('.pp-btn--primary')
    await wrapper.get('#pp-ref').setValue('release/v2.4')
    await fetch.trigger('click')
    await settle()
    expect(previewPacConfig).toHaveBeenNthCalledWith(1, 'p', 'main', scopedOptions())
    expect(previewPacConfig).toHaveBeenNthCalledWith(2, 'p', 'release/v2.4', scopedOptions())
    expect(wrapper.get('.pp-stage-name').text()).toBe('private-stage')
    allowed.delete('pipeline.read:p')
    usePermissionStore().invalidate()
    await settle()
    await fetch.trigger('click')
    await settle()
    expect(previewPacConfig).toHaveBeenCalledTimes(2)
    expect(wrapper.find('[role="dialog"]').exists()).toBe(false)
  })

  it.each([
    ['预览校验', false], ['导入到画布', true],
  ] as const)('clicks YAML %s with scoped non-persistent parse and accepted draft replacement', async (label, closes) => {
    const beforeReplace = vi.fn().mockResolvedValue(true)
    wrapper = mount(YamlImportModal, { props: { projectId:'p', baseRevision:'cfg-1', beforeReplace } })
    await settle()
    await wrapper.get('textarea').setValue('version: 1')
    await button('.yi-btn', label).trigger('click')
    await settle()
    expect(importPipeline).toHaveBeenCalledExactlyOnceWith('p', 'version: 1', false, undefined, scopedOptions())
    expect(beforeReplace).toHaveBeenCalledOnce()
    expect(wrapper.emitted('preview')).toEqual([[dto]])
    expect(wrapper.emitted('saved')).toBeUndefined()
    expect(!!wrapper.emitted('close')).toBe(closes)
  })

  it.each(['预览校验','导入到画布'])('blocks YAML %s replacement after edit is withdrawn during confirmation', async label => {
    let complete!: (result: boolean) => void
    const beforeReplace = vi.fn(() => new Promise<boolean>(resolve => { complete = resolve }))
    wrapper = mount(YamlImportModal, { props: { projectId:'p', beforeReplace } })
    await settle()
    await wrapper.get('textarea').setValue('version: 1')
    const retained = button('.yi-btn', label)
    await retained.trigger('click')
    await settle()
    expect(beforeReplace).toHaveBeenCalledOnce()
    allowed.delete('pipeline.edit:p')
    usePermissionStore().invalidate()
    await settle()
    complete(true)
    await retained.trigger('click')
    await settle()
    expect(importPipeline).toHaveBeenCalledOnce()
    expect(wrapper.emitted('preview')).toBeUndefined()
    expect(wrapper.emitted('saved')).toBeUndefined()
  })

  it.each([
    ['应用所选', ['stage-b']], ['全部接受', ['stage-a','stage-b']],
  ] as const)('clicks AI %s with the exact current proposal, selected scope and revision', async (label, stages) => {
    await mountAi()
    await wrapper!.get('input[aria-label$="Stage A"]').setValue(false)
    await button('.wiz-footer button', label).trigger('click')
    await settle()
    expect(aiGenerate).toHaveBeenCalledExactlyOnceWith('p', { nlSupplement: undefined }, scopedOptions())
    expect(aiApply).toHaveBeenCalledExactlyOnceWith('p', {
      proposal: proposal.proposal,
      selections: { stageIds: stages, build: true, branchMappingIds: ['mapping-a'] },
    }, 'cfg-1', scopedOptions())
    expect(wrapper!.emitted('applied')).toHaveLength(1)
  })

  it.each(['应用所选','全部接受'])('rejects the retained AI %s from the prior proposal after clicking regenerate', async label => {
    await mountAi()
    const retained = button('.wiz-footer button', label)
    await button('.wiz-footer button', '重新生成').trigger('click')
    await settle()
    expect(wrapper!.find('.footer-right').exists()).toBe(false)
    await retained.trigger('click')
    await settle()
    expect(aiApply).not.toHaveBeenCalled()
    await button('.wiz-footer button', '生成配置').trigger('click')
    await settle()
    expect(aiGenerate).toHaveBeenCalledTimes(2)
    expect(wrapper!.get('.proposal-section').text()).toContain('Stage A')
  })

  it('blocks retained AI regenerate and apply controls after pipeline edit revocation', async () => {
    await mountAi()
    const apply = button('.wiz-footer button','应用所选')
    const regenerate = button('.wiz-footer button','重新生成')
    allowed.delete('pipeline.edit:p')
    usePermissionStore().invalidate()
    await settle()
    await regenerate.trigger('click')
    await apply.trigger('click')
    await settle()
    expect(aiGenerate).toHaveBeenCalledOnce()
    expect(aiApply).not.toHaveBeenCalled()
    expect(wrapper!.find('.wiz-panel').exists()).toBe(false)
  })

  it('keeps AI regenerate disabled while an authorized apply is pending', async () => {
    await mountAi()
    let finish!: (result: AIApplyResponse) => void
    vi.mocked(aiApply).mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    await button('.wiz-footer button','应用所选').trigger('click')
    await settle()
    expect(aiApply).toHaveBeenCalledOnce()
    const regenerate = button('.wiz-footer button','重新生成')
    expect(regenerate.attributes('disabled')).toBeDefined()
    await regenerate.trigger('click')
    await settle()
    expect(wrapper!.find('.footer-right').exists()).toBe(true)
    expect(aiGenerate).toHaveBeenCalledOnce()
    finish({ applied: { spec: true, settings: true, triggers: true } })
    await settle()
    expect(wrapper!.emitted('applied')).toHaveLength(1)
  })
})
