import {beforeEach,afterEach,it,expect,vi} from 'vitest'
import {mount,flushPromises,type VueWrapper} from '@vue/test-utils'
import {createPinia,setActivePinia} from 'pinia'
import {useSessionStore} from '../stores/session'
import {usePermissionStore} from '../stores/permissions'
import {snapshot} from '../composables/onboardingFixtures.test-helper'
import Onboarding from './Onboarding.vue'
import ContinueOnboarding from '../components/onboarding/ContinueOnboarding.vue'
const m=vi.hoisted(()=>({status:vi.fn(),push:vi.fn(),resolve:vi.fn()}))
vi.mock('../api/onboarding',()=>({getOnboardingStatus:m.status}))
vi.mock('vue-router',()=>({useRouter:()=>({push:m.push,resolve:m.resolve})}))
let w:VueWrapper|undefined
beforeEach(()=>{setActivePinia(createPinia());useSessionStore().setUser({username:'reader',userId:'reader',multiTenant:true,instanceAdmin:false});usePermissionStore().setTeam('team');vi.clearAllMocks();m.status.mockResolvedValue(snapshot());localStorage.clear()})
afterEach(()=>w?.unmount())
it('does not render creation steps or request global snapshot on a direct employee visit',async()=>{w=mount(Onboarding,{global:{stubs:{OnboardingFlow:true}}});await flushPromises();expect(w.find('onboarding-flow-stub').exists()).toBe(false);expect(w.text()).toContain('暂无权限');expect(m.status).not.toHaveBeenCalled()})
it('does not expose the floating continuation entry to employees',async()=>{w=mount(ContinueOnboarding,{global:{stubs:{RouterLink:true}}});await flushPromises();expect(w.find('router-link-stub').exists()).toBe(false);expect(m.status).not.toHaveBeenCalled()})
it('preserves enabled instance administrator setup',async()=>{useSessionStore().setUser({username:'admin',userId:'admin',multiTenant:true,instanceAdmin:true});w=mount(Onboarding,{global:{stubs:{OnboardingFlow:true}}});await flushPromises();expect(w.find('onboarding-flow-stub').exists()).toBe(true);expect(m.status).toHaveBeenCalled()})
