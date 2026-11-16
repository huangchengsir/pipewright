import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { useSessionStore } from '../stores/session'
import { usePermissionStore } from '../stores/permissions'
import { useNavigationStore } from '../stores/navigation'
import { HttpError } from '../api/http'
import Dashboard from './Dashboard.vue'

const api=vi.hoisted(()=>({ decisions:vi.fn(), teams:vi.fn(), navigation:vi.fn(), section:vi.fn(), push:vi.fn() }))
vi.mock('../api/access',()=>({access:{decisions:api.decisions,teams:api.teams,navigation:api.navigation}}))
vi.mock('../api/dashboard',()=>({getDashboardSection:api.section}))
vi.mock('vue-router',()=>({useRouter:()=>({push:api.push})}))

let wrapper: VueWrapper | undefined
let pinia: ReturnType<typeof createPinia>
let pages: string[]
const run = (id: string, name: string) => ({
  id, projectId: 'p', projectName: name, status: 'success',
  createdAt: '2026-11-13T00:00:00Z', durationMs: 100, trigger: { branch: 'main' },
})
const sections: Record<string, unknown> = {
  recent: { items: [run('run-a','Project A'),run('run-b','Project B')], total: 2, page: 1 },
  running: { items: [], total: 0, page: 1 },
  diagnosis: { totalFeedback: 4, thumbsUp: 3, thumbsDown: 1, accuracy: 0.75 },
  dora: null,
  environments: [{ projectId: 'p', projectName: 'Project A', environment: 'staging', active: null }],
  servers: [{ id: 'fixture-server', name: 'Fixture server', host: 'fixture.invalid' }],
}
const sectionData = (key: string) => sections[key] === undefined ? [] : sections[key]
async function settle() { for(let i=0; i<4; i++) await flushPromises() }
async function open(page: string) {
  pages.push(page)
  await useNavigationStore().refresh()
  wrapper = mount(Dashboard,{ global: { plugins: [pinia], stubs: { DoraMetricCard:true } } })
  await settle()
  return wrapper
}
beforeEach(() => {
  vi.resetAllMocks()
  pinia=createPinia()
  setActivePinia(pinia)
  useSessionStore().setUser({username:'employee',userId:'employee',multiTenant:true})
  usePermissionStore().setTeam('team-a')
  pages=['page.dashboard']
  api.teams.mockResolvedValue([{id:'team-a',name:'Team A'}])
  api.navigation.mockImplementation(async()=>({
    userId:'employee',teamId:'team-a',modeEpoch:1,revision:1,catalogVersion:1,pages:[...pages],
  }))
  api.decisions.mockImplementation(async (team:string,queries: unknown[]) => ({
    userId:'employee',teamId:team,modeEpoch:1,revision:1,catalogVersion:1,allowed:queries.map(()=>false),
  }))
  api.section.mockImplementation(async (key:string)=>sectionData(key))
})
afterEach(() => { wrapper?.unmount(); wrapper=undefined })

it.each([
  ['.bento-alerts .diag-row .card-link','page.runs','/settings/diagnosis-stats'],
  ['.bento-env .env .card-link','page.environments','/environments'],
  ['.bento-runs .runs .card-head .card-link','page.runs','/runs'],
  ['.bento-srv .srv .card-link','page.servers','/server-status'],
] as const)('opens %s only for the current destination grant and does not refetch the overview', async (selector,page,path) => {
  const view = await open(page)
  const retained = view.get(selector)
  for (const [, options] of api.section.mock.calls)
    expect(new Headers(options.headers).get('X-Pipewright-Team')).toBe('team-a')
  api.section.mockClear()
  await retained.trigger('click')
  expect(api.push).toHaveBeenCalledExactlyOnceWith(path)
  expect(api.section).not.toHaveBeenCalled()

  pages=['page.dashboard']
  await useNavigationStore().refresh(true)
  await settle()
  expect(view.find(selector).exists()).toBe(false)
  retained.element.dispatchEvent(new MouseEvent('click',{bubbles:true}))
  expect(api.push).toHaveBeenCalledOnce()
})

it('routes only the chosen recent-run row and blocks that retained row after its menu revocation',async()=>{
  const view = await open('page.runs')
  const rows=view.findAll('.bento-runs button.run-row')
  expect(rows).toHaveLength(2)
  const selected=rows[1]!
  expect(selected.text()).toContain('Project B')
  await selected.trigger('click')
  expect(api.push).toHaveBeenCalledExactlyOnceWith('/runs/run-b')
  pages=['page.dashboard']
  await useNavigationStore().refresh(true)
  await settle()
  expect(selected.attributes('disabled')).toBeDefined()
  selected.element.dispatchEvent(new MouseEvent('click',{bubbles:true}))
  expect(api.push).toHaveBeenCalledOnce()
})

it('keeps diagnosis navigation unavailable when its section is forbidden even with the runs menu', async () => {
  api.section.mockImplementation(async (key: string) => {
    if (key==='diagnosis') throw new HttpError(403,{code:'forbidden',message:'denied'},'denied')
    return sectionData(key)
  })
  const view=await open('page.runs')
  expect(view.find('.bento-alerts .diag-row .card-link').exists()).toBe(false)
  expect(view.find('.bento-runs .card-link').exists()).toBe(true)
  expect(api.push).not.toHaveBeenCalled()
})
