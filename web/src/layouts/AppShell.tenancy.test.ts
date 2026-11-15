import {mount,flushPromises} from '@vue/test-utils'
import {createPinia,setActivePinia} from 'pinia'
import {createRouter,createMemoryHistory} from 'vue-router'
import {beforeEach,afterEach,it,expect,vi} from 'vitest'
import Shell from './AppShell.vue'
import AppSelect from '../components/ui/AppSelect.vue'
import {access} from '../api/access'
import { logout } from '../api/auth'
import { useConfirm } from '../composables/useConfirm'
import {useSessionStore} from '../stores/session'
import {useNavigationStore} from '../stores/navigation'
import {usePermissionStore} from '../stores/permissions'
vi.mock('../api/access',()=>({access:{teams:vi.fn(),navigation:vi.fn()}}))
vi.mock('../api/auth', async original => ({
  ...await original<typeof import('../api/auth')>(),
  logout: vi.fn(),
}))
let wrappers:ReturnType<typeof mount>[]=[]
beforeEach(()=>{setActivePinia(createPinia());vi.clearAllMocks();useSessionStore().setUser({username:'member',userId:'u',multiTenant:true});vi.mocked(access.teams).mockResolvedValue([{id:'a',name:'Team A',revision:1},{id:'b',name:'Team B',revision:1}]);vi.mocked(access.navigation).mockImplementation(async team=>({userId:'u',teamId:team,catalogVersion:1,modeEpoch:1,revision:1,pages:team==='a'?['page.projects']:['page.runs']}))})
afterEach(()=>wrappers.splice(0).forEach(w=>w.unmount()))
async function view(){const n=useNavigationStore();await n.refresh();const router=createRouter({history:createMemoryHistory(),routes:[{path:'/projects',component:{template:'<p>private project content</p>'}},{path:'/runs',component:{template:'<p>run content</p>'}},{path:'/settings/account',component:{template:'<p>account content</p>'}},{path:'/:pathMatch(.*)*',component:{template:'<div />'}}]});await router.push('/projects');await router.isReady();const w=mount(Shell,{global:{plugins:[router],stubs:{ThemeToggle:true,ContinueOnboarding:true}}});wrappers.push(w);await flushPromises();return{w,router,n}}
it('filters rail entries and preserves an explicit account destination',async()=>{const{w}=await view();expect(w.find('a[href="/projects"]').exists()).toBe(true);expect(w.find('a[href="/runs"]').exists()).toBe(false);expect(w.find('a[href="/settings/notifications"]').exists()).toBe(false);expect(w.find('a[href="/settings/account"]').exists()).toBe(true);expect(w.text()).toContain('private project content')})
it('honors a rejected draft navigation before changing global team',async()=>{const{w,router}=await view();const stop=router.beforeEach(to=>to.path==='/settings/account'?false:true);w.getComponent(AppSelect).vm.$emit('update:modelValue','b');await flushPromises();expect(usePermissionStore().activeTeam).toBe('a');expect(w.text()).toContain('private project content');stop();w.getComponent(AppSelect).vm.$emit('update:modelValue','b');await flushPromises();expect(usePermissionStore().activeTeam).toBe('b');expect(router.currentRoute.value.path).toBe('/settings/account');expect(w.text()).not.toContain('private project content');expect(w.find('a[href="/runs"]').exists()).toBe(true)})
it('allows switching again from account and clears current page after menu revocation',async()=>{const{w,router,n}=await view();await router.push('/settings/account');w.getComponent(AppSelect).vm.$emit('update:modelValue','b');await flushPromises();expect(usePermissionStore().activeTeam).toBe('b');await router.push('/runs');expect(w.text()).toContain('run content');vi.mocked(access.navigation).mockResolvedValue({userId:'u',teamId:'b',catalogVersion:1,modeEpoch:1,revision:2,pages:[]});await n.refresh(true);await flushPromises();expect(w.text()).not.toContain('run content');expect(router.currentRoute.value.path).toBe('/settings/account')})

it('keeps the current session on cancel and clears it after logout confirmation', async () => {
  vi.mocked(logout).mockResolvedValue(undefined)
  const { w, router } = await view()
  const button = w.get('button.nav-item--logout')
  const confirm = useConfirm()
  await button.trigger('click')
  expect(confirm.pending.value).not.toBeNull()
  confirm._resolve(false)
  await flushPromises()
  expect(logout).not.toHaveBeenCalled()
  expect(useSessionStore().user?.username).toBe('member')
  await button.trigger('click')
  confirm._resolve(true)
  await flushPromises()
  expect(logout).toHaveBeenCalledOnce()
  expect(useSessionStore().user).toBeNull()
  expect(router.currentRoute.value.path).toBe('/login')
})
