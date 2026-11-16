import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { defineComponent } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import PermissionGate from './PermissionGate.vue'
import AppButton from './ui/AppButton.vue'
import { access } from '../api/access'
import { useSessionStore } from '../stores/session'
import { usePermissionStore } from '../stores/permissions'
vi.mock('../api/access', () => ({ access: { decisions: vi.fn() } }))
let wrappers: ReturnType<typeof mount>[] = []
beforeEach(() => { setActivePinia(createPinia()); vi.clearAllMocks(); useSessionStore().setUser({ username: 'member', userId: 'user', multiTenant: true }) })
afterEach(() => { wrappers.forEach(w => w.unmount()); wrappers = []; vi.useRealTimers() })
const result = (allowed: boolean[]) => ({ userId: 'user', teamId: 'team', modeEpoch: 1, revision: 1, catalogVersion: 1, allowed })
function render(mode = 'hide') {
 const component = defineComponent({ components: { PermissionGate, AppButton }, template: `<PermissionGate team="team" :query="{action:'project.edit',resourceId:'p'}" mode="${mode}"><AppButton class="edit">Edit</AppButton><template #denied="{disabled}"><AppButton class="denied" :disabled="disabled">Edit</AppButton></template></PermissionGate>` })
 const wrapper = mount(component); wrappers.push(wrapper); return wrapper
}
describe('PermissionGate', () => {
 it('hides unknown/loading and denied actions with no extra layout wrapper', async () => {
  let complete!: (value: ReturnType<typeof result>) => void
  vi.mocked(access.decisions).mockReturnValue(new Promise(resolve => { complete = resolve }))
  const wrapper = render(); expect(wrapper.find('button').exists()).toBe(false)
  await flushPromises(); complete(result([true])); await flushPromises()
  expect(wrapper.html()).not.toContain('<div'); expect(wrapper.find('.edit').exists()).toBe(true)
 })
 it('batches shared controls and invalidates immediately on team/identity changes', async () => {
  vi.mocked(access.decisions).mockResolvedValue(result([true]))
  const first = render(), second = render(); await flushPromises()
  expect(access.decisions).toHaveBeenCalledTimes(1); expect(first.find('.edit').exists()).toBe(true); expect(second.find('.edit').exists()).toBe(true)
  usePermissionStore().setTeam('other'); expect(usePermissionStore().allowed('team', {action:'project.edit',resourceId:'p'})).toBe(false)
  useSessionStore().clearSession(); await flushPromises(); expect(first.find('button').exists()).toBe(false)
 })
 it('only renders an explicitly disabled actual control for denied-slot mode', async () => {
  vi.mocked(access.decisions).mockResolvedValue(result([false]))
  const wrapper = render('disabled'); await flushPromises()
  expect(wrapper.find('button').attributes('disabled')).toBeDefined(); expect(wrapper.find('button').attributes('aria-disabled')).toBe('true')
 })
 it('fails closed on malformed responses and request errors', async () => {
  vi.mocked(access.decisions).mockResolvedValue({...result([true]),userId:'another-user'})
  const wrapper=render();await flushPromises();expect(wrapper.find('button').exists()).toBe(false)
  vi.mocked(access.decisions).mockRejectedValue(new Error('offline'));usePermissionStore().invalidate();await flushPromises();expect(wrapper.find('button').exists()).toBe(false)
 })
 it('preserves legacy known actions but rejects unknown actions', async () => {
  useSessionStore().setUser({username:'admin',multiTenant:false});const wrapper=render();await flushPromises()
  expect(wrapper.find('.edit').exists()).toBe(true);expect(access.decisions).not.toHaveBeenCalled();expect(usePermissionStore().allowed('',{action:'future',resourceId:'p'})).toBe(false)
 })
 it('rejects missing mode and network failure without trusting stale user fields', async()=>{
  useSessionStore().setUser({username:'admin'});const wrapper=render();await flushPromises();expect(wrapper.find('button').exists()).toBe(false)
  useSessionStore().setUser({username:'member',userId:'user',multiTenant:true});vi.mocked(access.decisions).mockResolvedValue(result([true]));await flushPromises();expect(wrapper.find('button').exists()).toBe(true)
  useSessionStore().isNetworkError=true;await flushPromises();expect(wrapper.find('button').exists()).toBe(false)
 })
 it('renews before expiry without replacing buttons, then rejects revocation and expired unresolved responses', async()=>{
  vi.useFakeTimers({toFake:['setInterval','clearInterval','Date']});vi.mocked(access.decisions).mockResolvedValue(result([true]))
  const wrapper=render();await flushPromises();const button=wrapper.find('button').element
  await vi.advanceTimersByTimeAsync(3_000);await flushPromises();expect(wrapper.find('button').element).toBe(button)
  await vi.advanceTimersByTimeAsync(3_000);await flushPromises();expect(wrapper.find('button').element).toBe(button)
  vi.mocked(access.decisions).mockResolvedValue({...result([false]),revision:2});await vi.advanceTimersByTimeAsync(3_000);await flushPromises();expect(wrapper.find('button').exists()).toBe(false)
  vi.mocked(access.decisions).mockResolvedValue({...result([true]),revision:3});usePermissionStore().invalidate();await flushPromises();expect(wrapper.find('button').exists()).toBe(true)
  vi.mocked(access.decisions).mockReturnValue(new Promise(()=>{}));await vi.advanceTimersByTimeAsync(3_000);await flushPromises();expect(wrapper.find('button').exists()).toBe(true)
  await vi.advanceTimersByTimeAsync(2_000);await flushPromises();expect(wrapper.find('button').exists()).toBe(false)
 })
 it('discards a delayed response after a team switch',async()=>{
  let complete!:(value:ReturnType<typeof result>)=>void;vi.mocked(access.decisions).mockReturnValue(new Promise(resolve=>{complete=resolve}))
  const wrapper=render();await flushPromises();usePermissionStore().setTeam('another-team');complete(result([true]));await flushPromises();expect(wrapper.find('button').exists()).toBe(false)
 })

 it('retries an overflow control when a bounded cache slot is released',async()=>{
  vi.mocked(access.decisions).mockImplementation(async(_team,queries)=>result(queries.map(()=>true)))
  const permissions=usePermissionStore();const releases=Array.from({length:512},(_,index)=>permissions.retain('team',{action:'project.edit',resourceId:`p-${index}`}))
  const overflow={action:'project.edit',resourceId:'overflow'};const releaseOverflow=permissions.retain('team',overflow);await flushPromises()
  expect(permissions.allowed('team',overflow)).toBe(false);releases[0]?.();await flushPromises();expect(permissions.allowed('team',overflow)).toBe(true)
  releases.slice(1).forEach(release=>release());releaseOverflow()
 })

})

it('requires every resource in a compound action and drops it on any revocation', async () => {
 vi.mocked(access.decisions).mockResolvedValue(result([true, true]))
 const wrapper = mount(defineComponent({ components: { PermissionGate, AppButton }, template: `<PermissionGate team="team" :query="[{action:'mcp.issue',resourceId:'p1'},{action:'mcp.issue',resourceId:'p2'}]"><AppButton>Issue</AppButton></PermissionGate>` })); wrappers.push(wrapper)
 await flushPromises(); expect(wrapper.find('button').exists()).toBe(true)
 vi.mocked(access.decisions).mockResolvedValue(result([true, false])); usePermissionStore().invalidate(); await flushPromises()
 expect(wrapper.find('button').exists()).toBe(false)
})
