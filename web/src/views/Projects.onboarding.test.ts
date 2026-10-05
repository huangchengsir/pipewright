import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { reactive } from 'vue'
import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import Projects from './Projects.vue'
import { listProjects, updateProject, createProject, type Project } from '../api/projects'
import { listCredentials } from '../api/credentials'
import { listRefs, listCommits } from '../api/refs'
import { triggerManual } from '../api/runs'
import { getParameters } from '../api/parameters'
import { dismissOnboarding, resetOnboarding } from '../composables/useOnboarding'
const navigation = vi.hoisted(() => ({ replace: vi.fn(), push: vi.fn() }))
const route = reactive<{ query: Record<string, string> }>({ query: {} })
vi.mock('vue-router', () => ({ useRouter: () => navigation, useRoute: () => route }))
vi.mock('../api/projects', () => ({ listProjects: vi.fn(), createProject: vi.fn(), updateProject: vi.fn(), deleteProject: vi.fn(), testClone: vi.fn() }))
vi.mock('../api/credentials', () => ({ listCredentials: vi.fn(), createCredential: vi.fn() }))
vi.mock('../api/refs', () => ({ listRefs: vi.fn(), listCommits: vi.fn() }))
vi.mock('../api/runs', () => ({ triggerManual: vi.fn() }))
vi.mock('../api/parameters', () => ({ getParameters: vi.fn(), validateParamValues: () => null }))
const p: Project = { id: 'a', name: 'A', repoUrl: 'https://internal.example/repo', defaultBranch: 'main', credentialId: '', credentialName: '',
  pacEnabled: false, prStatusEnabled: false, lastRunStatus: null, targetServers: [], createdAt: '2026-10-05', updatedAt: '2026-10-05' }
let wrapper: VueWrapper | undefined
const stubs = { RouterLink: { props: ['to'], template: '<a :href="to"><slot /></a>' }, Teleport: true }
beforeEach(() => {
  vi.clearAllMocks(); localStorage.clear(); resetOnboarding(); route.query = {}
  vi.mocked(listProjects).mockResolvedValue([p])
  vi.mocked(listCredentials).mockResolvedValue([{ id: 'c', name: 'credential', type: 'git_token', scope: '', username: '', maskedValue: 'masked', createdAt: 'now', lastUsedAt: null }])
  vi.mocked(getParameters).mockResolvedValue([])
  vi.mocked(listRefs).mockResolvedValue({ branches: [], tags: [] })
  vi.mocked(listCommits).mockResolvedValue([])
  vi.mocked(updateProject).mockResolvedValue(p)
})
afterEach(() => { wrapper?.unmount(); wrapper = undefined })
async function open(query: Record<string, string>) {
  route.query = query
  wrapper = mount(Projects, { global: { stubs } })
  await flushPromises()
  return wrapper
}
describe('onboarding project navigation only opens existing dialogs', () => {
  it('keeps the create form intact when clicking its backdrop', async () => {
    const view = await open({ onboardingCreate: '1' })
    await view.get('#proj-name').setValue('Keep this draft')
    await view.get('#proj-repo').setValue('https://internal.example/draft.git')
    await view.get('[role="dialog"]').trigger('click')
    expect(view.find('[role="dialog"]').exists()).toBe(true)
    expect((view.get('#proj-name').element as HTMLInputElement).value).toBe('Keep this draft')
    expect((view.get('#proj-repo').element as HTMLInputElement).value).toBe('https://internal.example/draft.git')
    expect(createProject).not.toHaveBeenCalled()
  })
  it.each(['cancel', 'close', 'escape'])('still closes create through explicit %s', async control => {
    const view = await open({ onboardingCreate: '1' })
    if (control === 'escape') await view.get('[role="dialog"]').trigger('keydown', { key: 'Escape' })
    else if (control === 'close') await view.get('.modal-close').trigger('click')
    else await view.get('.modal-footer .btn-secondary').trigger('click')
    expect(view.find('[role="dialog"]').exists()).toBe(false)
    expect(createProject).not.toHaveBeenCalled()
  })
  it('consumes run query preserving other parameters without refs, commits or task submission', async () => {
    const view = await open({ onboardingRun: 'a', keep: 'yes' })
    expect(view.find('[role="dialog"]').exists()).toBe(true)
    expect(navigation.replace).toHaveBeenCalledWith({ query: { keep: 'yes' } })
    const branch = view.findAll('input').find(i => i.attributes('list')?.includes('branch'))
    expect(branch).toBeDefined()
    await branch!.setValue('other')
    expect(listRefs).not.toHaveBeenCalled(); expect(listCommits).not.toHaveBeenCalled(); expect(triggerManual).not.toHaveBeenCalled()
    expect(getParameters).toHaveBeenCalledWith('a')
  })
  it('does not open or submit an invalid project id', async () => {
    const view = await open({ onboardingRun: 'deleted' })
    expect(view.find('[role="dialog"]').exists()).toBe(false)
    expect(triggerManual).not.toHaveBeenCalled()
  })
  it('honors query changes after projects load', async () => {
    const view = await open({})
    route.query = { onboardingCreate: '1' }; await flushPromises()
    expect(view.find('[role="dialog"]').exists()).toBe(true)
    expect(createProject).not.toHaveBeenCalled()
  })
  it('ordinary rename sends name only', async () => {
    const view = await open({})
    const rename = view.findAll('button').find(b => b.attributes('aria-label')?.includes('重命名'))
    expect(rename).toBeDefined()
    await rename!.trigger('click')
    await view.get('#rename-input').setValue('renamed')
    await view.get('.modal-form').trigger('submit'); await flushPromises()
    expect(updateProject).toHaveBeenCalledWith('a', { name: 'renamed' })
  })
  it('repair sends explicit credential only on Save and reports missing credential beside selector', async () => {
    const view = await open({ onboardingEdit: 'a' })
    expect(updateProject).not.toHaveBeenCalled()
    await view.get('.modal-form').trigger('submit')
    expect(updateProject).not.toHaveBeenCalled()
    expect(view.get('#onboarding-repair-error').text()).not.toBe('')
    expect(view.get('#rename-input').attributes('aria-invalid')).toBeUndefined()
    await view.get('#onboarding-repair-credential').trigger('click')
    await view.get('[role="option"]').trigger('click')
    await view.get('.modal-form').trigger('submit'); await flushPromises()
    expect(updateProject).toHaveBeenCalledWith('a', { name: 'A', credentialId: 'c' })
  })
  it('empty credentials cannot submit repair', async () => {
    vi.mocked(listCredentials).mockResolvedValue([])
    const view = await open({ onboardingEdit: 'a' })
    await view.get('.modal-form').trigger('submit'); await flushPromises()
    expect(updateProject).not.toHaveBeenCalled()
  })
})
