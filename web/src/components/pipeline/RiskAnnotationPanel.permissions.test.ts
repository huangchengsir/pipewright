import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import Panel from './RiskAnnotationPanel.vue'
import { access } from '../../api/access'
import { http, HttpError } from '../../api/http'
import type { AnalyzeRisksResponse } from '../../api/aiRisk'
import { usePermissionStore } from '../../stores/permissions'
import { useSessionStore } from '../../stores/session'

vi.mock('../../api/access', () => ({ access: { decisions: vi.fn() } }))
vi.mock('../../api/http', async original => ({
  ...await original<typeof import('../../api/http')>(),
  http: { post: vi.fn() },
}))

let wrapper: VueWrapper | undefined
let grants: Set<string>
const actor = { username:'member', userId:'user-1', multiTenant: true }
const fakeFinding = (title: string): AnalyzeRisksResponse => ({
  findings: [{ title, level:'high', stepName:'test', line:1, why:'synthetic', suggestion:'', source:'rule' }],
  aiEnhanced: false, aiReason:'', generatedAt:'2026-11-15T09:00:00Z',
})
const endpoint = (id: string) => '/api/projects/' + id + '/pipeline/analyze-risks'
const options = (team: string) => expect.objectContaining({
  headers: { 'X-Pipewright-Team': team }, signal: expect.any(AbortSignal), throwOnUnauthorized: true,
})
const requestFor = (id: string) => vi.mocked(http.post).mock.calls.find(([url])=>url===endpoint(id))?.[2]
async function settle() { for(let i=0;i<5;i++)await flushPromises() }
async function render() {
  const view=mount(Panel,{props:{projectId:'project-a'}})
  wrapper=view
  await settle()
  return view
}

beforeEach(()=>{
  vi.resetAllMocks()
  setActivePinia(createPinia())
  useSessionStore().setUser(actor)
  usePermissionStore().setTeam('team-a')
  grants=new Set(['team-a:pipeline.edit:project-a'])
  vi.mocked(access.decisions).mockImplementation(async (team, queries)=>({
    userId: actor.userId, teamId: team, modeEpoch:1, revision:1, catalogVersion:1,
    allowed: queries.map(q=>grants.has(team+':'+q.action+':'+q.resourceId)),
  }))
  vi.mocked(http.post).mockResolvedValue(fakeFinding('fixture result'))
})
afterEach(()=>{wrapper?.unmount();wrapper=undefined})

describe('risk scan control permissions',()=>{
  it('hides the actual risk scan button with pipeline read only and never sends analysis',async()=>{
    grants=new Set(['team-a:pipeline.read:project-a','team-a:pipeline.edit:project-b'])
    const view=await render()
    expect(view.find('.rap-actions button').exists()).toBe(false)
    expect(http.post).not.toHaveBeenCalled()
  })

  it('clicks scan and rescan using the exact pipeline editor decision and captured team',async()=>{
    const view=await render()
    await view.get('.rap-actions button').trigger('click')
    await settle()
    expect(view.get('.rap-list').text()).toContain('fixture result')
    expect(http.post).toHaveBeenNthCalledWith(1,endpoint('project-a'),undefined,options('team-a'))
    vi.mocked(http.post).mockResolvedValueOnce(fakeFinding('current rescan'))
    await view.get('.rap-actions button').trigger('click')
    await settle()
    expect(view.get('.rap-list').text()).toContain('current rescan')
    expect(http.post).toHaveBeenNthCalledWith(2,endpoint('project-a'),undefined,options('team-a'))
    expect(http.post).toHaveBeenCalledTimes(2)
  })

  it('aborts a pending scan and blocks a retained button after pipeline edit revocation',async()=>{
    let complete!: (value: AnalyzeRisksResponse)=>void
    vi.mocked(http.post).mockImplementationOnce(()=>new Promise(resolve=>{complete=resolve}))
    const view=await render()
    const retained=view.get('.rap-actions button')
    await retained.trigger('click')
    await settle()
    expect(http.post).toHaveBeenCalledOnce()
    grants.clear()
    usePermissionStore().invalidate()
    await settle()
    expect(requestFor('project-a')?.signal?.aborted).toBe(true)
    complete(fakeFinding('revoked result'))
    await retained.trigger('click')
    await settle()
    expect(view.text()).not.toContain('revoked result')
    expect(view.find('.rap-actions button').exists()).toBe(false)
    expect(http.post).toHaveBeenCalledOnce()
  })

  it('aborts the previous project immediately and never applies its late scan to the selected project',async()=>{
    let complete!: (value: AnalyzeRisksResponse)=>void
    vi.mocked(http.post).mockImplementationOnce(()=>new Promise(resolve=>{complete=resolve}))
    const view=await render()
    await view.get('.rap-actions button').trigger('click')
    await settle()
    await view.setProps({projectId:'project-b'})
    await settle()
    expect(requestFor('project-a')?.signal?.aborted).toBe(true)
    complete(fakeFinding('previous project result'))
    await settle()
    expect(view.text()).not.toContain('previous project result')
    expect(view.find('.rap-actions button').exists()).toBe(false)
    grants.add('team-a:pipeline.edit:project-b')
    usePermissionStore().invalidate()
    await settle()
    await view.get('.rap-actions button').trigger('click')
    await settle()
    expect(http.post).toHaveBeenNthCalledWith(2,endpoint('project-b'),undefined,options('team-a'))
  })

  it('aborts the old team scope even for the same project and scans only from its current button',async()=>{
    let complete!: (value: AnalyzeRisksResponse)=>void
    vi.mocked(http.post).mockImplementationOnce(()=>new Promise(resolve=>{complete=resolve}))
    grants.add('team-b:pipeline.edit:project-a')
    const view=await render()
    const retained=view.get('.rap-actions button')
    await retained.trigger('click')
    await settle()
    usePermissionStore().setTeam('team-b')
    await settle()
    expect(requestFor('project-a')?.signal?.aborted).toBe(true)
    complete(fakeFinding('old team result'))
    await retained.trigger('click')
    await settle()
    expect(view.text()).not.toContain('old team result')
    expect(http.post).toHaveBeenCalledOnce()
    await view.get('.rap-actions button').trigger('click')
    await settle()
    expect(http.post).toHaveBeenNthCalledWith(2,endpoint('project-a'),undefined,options('team-b'))
    expect(view.get('.rap-list').text()).toContain('fixture result')
  })

  it('clears existing results and refuses stale cached permissions on a forbidden response',async()=>{
    const view=await render()
    await view.get('.rap-actions button').trigger('click')
    await settle()
    expect(view.get('.rap-list').text()).toContain('fixture result')
    const retained=view.get('.rap-actions button')
    vi.mocked(http.post).mockRejectedValueOnce(new HttpError(403,{code:'forbidden',message:'private detail'},'Denied'))
    await retained.trigger('click')
    await settle()
    await retained.trigger('click')
    await settle()
    expect(view.find('.rap-actions button').exists()).toBe(false)
    expect(view.text()).not.toContain('fixture result')
    expect(view.text()).not.toContain('private detail')
    expect(http.post).toHaveBeenCalledTimes(2)
  })

  it('preserves standalone administrator scan and clean result without tenant permission requests',async()=>{
    useSessionStore().setUser({username:'admin',multiTenant:false})
    vi.mocked(http.post).mockResolvedValueOnce({...fakeFinding('unused'),findings:[]})
    const view=await render()
    await view.get('.rap-actions button').trigger('click')
    await settle()
    expect(http.post).toHaveBeenCalledExactlyOnceWith(endpoint('project-a'),undefined,expect.objectContaining({signal:expect.any(AbortSignal),headers:undefined}))
    expect(access.decisions).not.toHaveBeenCalled()
    expect(view.find('.rap-clean').exists()).toBe(true)
  })
})
