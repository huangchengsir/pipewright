import {beforeEach,afterEach,it,expect,vi} from 'vitest'
import {mount,flushPromises,type VueWrapper} from '@vue/test-utils'
import {createPinia,setActivePinia} from 'pinia'
import {useSessionStore} from '../stores/session'
import {usePermissionStore} from '../stores/permissions'
import {useNavigationStore} from '../stores/navigation'
import {HttpError} from '../api/http'
import Dashboard from './Dashboard.vue'
const m=vi.hoisted(()=>({decisions:vi.fn(),teams:vi.fn(),navigation:vi.fn(),section:vi.fn(),push:vi.fn()}))
vi.mock('../api/access',()=>({access:{decisions:m.decisions,teams:m.teams,navigation:m.navigation}}))
vi.mock('../api/dashboard',()=>({getDashboardSection:m.section}))
vi.mock('vue-router',()=>({useRouter:()=>({push:m.push})}))
let w:VueWrapper|undefined,create=false,pages:string[]=[]
async function settle(){for(let i=0;i<5;i++)await flushPromises()}
async function render(){await useNavigationStore().refresh();w=mount(Dashboard,{global:{stubs:{DoraMetricCard:true}}});await settle()}
beforeEach(()=>{setActivePinia(createPinia());useSessionStore().setUser({username:'employee',userId:'u1',multiTenant:true});usePermissionStore().setTeam('a');vi.clearAllMocks();create=false;pages=['page.dashboard'];m.teams.mockResolvedValue([{id:'a',name:'Team'}]);m.navigation.mockImplementation(async()=>({userId:'u1',teamId:'a',modeEpoch:1,revision:1,catalogVersion:1,pages}));m.decisions.mockImplementation(async(team:string,qs:{action:string}[])=>({userId:'u1',teamId:team,modeEpoch:1,revision:1,catalogVersion:1,allowed:qs.map(q=>create&&q.action==='project.create')}));m.section.mockImplementation(async(section:string)=>section==='recent'||section==='running'?{items:[],total:0,page:1}:section==='dora'||section==='diagnosis'?null:[])})
afterEach(()=>{w?.unmount();w=undefined})
it('hides create and disallowed navigation, disables KPI navigation',async()=>{await render();expect(w!.find('.dash-primary').exists()).toBe(false);expect(w!.find('.alerts .card-link').exists()).toBe(false);for(const button of w!.findAll('.kpi'))expect(button.attributes('disabled')).toBeDefined();expect(w!.find('.runs .card-link').exists()).toBe(false);expect(w!.find('.srv .card-link').exists()).toBe(false);expect(w!.find('.env .card-link').exists()).toBe(false)})
it('requires both project create action and project menu, then revokes button',async()=>{pages.push('page.projects');await render();expect(w!.find('.dash-primary').exists()).toBe(false);create=true;usePermissionStore().invalidate();await settle();await w!.get('.dash-primary').trigger('click');expect(m.push).toHaveBeenCalledWith('/projects');create=false;usePermissionStore().invalidate();await settle();expect(w!.find('.dash-primary').exists()).toBe(false)})
it('exposes anomaly navigation only with its actual menu and successful section',async()=>{pages.push('page.anomalies');await render();await w!.get('.alerts .card-link').trigger('click');expect(m.push).toHaveBeenCalledWith('/anomaly');m.section.mockRejectedValue(new HttpError(403,null,'denied'));await w!.get('.dash-ghost').trigger('click');await settle();expect(w!.find('.alerts .card-link').exists()).toBe(false)})
it('collapses forbidden sections into one neutral notice without retry failures',async()=>{m.section.mockRejectedValue(new HttpError(403,null,'denied'));await render();expect(w!.findAll('.dash-access-notice')).toHaveLength(1);expect(w!.text()).toContain('暂无权限');expect(w!.findAll('[role="alert"]')).toHaveLength(0);expect(w!.findAll('.data-notice button')).toHaveLength(0);expect(w!.findAll('.kpi')).toHaveLength(0);expect(w!.find('.alerts').exists()).toBe(false)})
it('keeps transport failures independently retryable',async()=>{m.section.mockImplementation(async(section:string)=>{if(section==='alerts')throw new Error('offline');return section==='recent'||section==='running'?{items:[],total:0,page:1}:section==='dora'||section==='diagnosis'?null:[]});await render();expect(w!.get('.bento-alerts .dash-data').attributes('data-state')).toBe('error');m.section.mockClear();await w!.get('.bento-alerts .data-notice button').trigger('click');await settle();expect(m.section.mock.calls.map(c=>c[0])).toEqual(['alerts'])})

it('removes revoked navigation and blocks a captured KPI click',async()=>{pages.push('page.projects');await render();const project=w!.findAll('.kpi')[0]!;expect(project.attributes('disabled')).toBeUndefined();pages=['page.dashboard'];await useNavigationStore().refresh(true);await settle();await project.trigger('click');expect(m.push).not.toHaveBeenCalled();expect(project.attributes('disabled')).toBeDefined()})


it('opens allowed project and DORA links without bypassing later menu revocation', async () => {
  pages.push('page.projects', 'page.dora')
  await render()
  const project = w!.get('.kpi-row .kpi')
  expect(project.attributes('disabled')).toBeUndefined()
  await project.trigger('click')
  const dora = w!.get('.bento-dora .card-link')
  await dora.trigger('click')
  expect(m.push.mock.calls).toEqual([['/projects'], ['/metrics/dora']])
  pages = ['page.dashboard']
  await useNavigationStore().refresh(true)
  await settle()
  expect(w!.find('.bento-dora .card-link').exists()).toBe(false)
  await project.trigger('click')
  await dora.trigger('click')
  expect(m.push).toHaveBeenCalledTimes(2)
})
