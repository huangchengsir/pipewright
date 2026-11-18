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

it.each(['scrim', 'header', 'footer', 'escape'])('closes the current container diagnosis via %s without another request', async control => {
  const view = await open()
  expect(view.text()).toContain('A scoped finding')
  await view.get('.modal').trigger('click')
  expect(view.emitted('close')).toBeUndefined()
  const selectors: Record<string, string> = {
    scrim: '.modal-scrim', header: '.modal__close', footer: '.modal__foot .btn--primary',
  }
  await view.get('.modal').trigger('pointerdown')
  await view.get('dialog').trigger('click')
  expect(view.emitted('close')).toBeUndefined()
  if (control === 'scrim') await view.get('dialog').trigger('pointerdown')
  if (control === 'escape') await view.get('dialog').trigger('cancel')
  else await view.get(selectors[control]!).trigger('click')
  expect(view.emitted('close')).toEqual([[]])
  expect(diagnoseContainer).toHaveBeenCalledOnce()
})
