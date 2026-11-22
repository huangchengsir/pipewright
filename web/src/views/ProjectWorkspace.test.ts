import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, disposePinia, setActivePinia, type Pinia } from 'pinia'
import { defineComponent, type PropType } from 'vue'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { assignWorkspaceApplication, listWorkspace, listWorkspaceApplications, type WorkspaceApplication, type WorkspaceCardScope } from '../api/workspace'
import { HttpError } from '../api/http'
import { setLocale } from '../i18n'
import { useSessionStore } from '../stores/session'
import ProjectWorkspace from './ProjectWorkspace.vue'

vi.mock('../api/workspace', () => ({
  assignWorkspaceApplication: vi.fn(), listWorkspace: vi.fn(), listWorkspaceApplications: vi.fn(),
  createBusinessProject: vi.fn(), createWorkspaceProduct: vi.fn(), createWorkspaceApplication: vi.fn(), deleteBusinessProject: vi.fn(),
  deleteWorkspaceProduct: vi.fn(), updateBusinessProject: vi.fn(), updateWorkspaceProduct: vi.fn(),
}))

const application: WorkspaceApplication = {
  id: 'app-1', name: 'Frontend', repoUrl: 'https://repos.example.net/long/complete-repository-url',
  defaultBranch: 'release/full-branch-name', credentialId: '', credentialName: '', pacEnabled: false,
  prStatusEnabled: false, lastRunStatus: '失败', targetServers: [], createdAt: '', updatedAt: '',
  assignmentRevision: 7,
  latestRun: { runId: 'latest', status: 'failed', commit: 'failed-commit', createdAt: '2026-11-20T06:00:00Z' },
  productionDeployment: { runId: 'deployed', environment: 'production', commit: 'production-version', deployedAt: '2026-11-19T06:00:00Z' },
}
const RepositoryCards = defineComponent({
  props: { workspace: { type: Object as PropType<WorkspaceCardScope>, required: true } },
  emits: ['open-application', 'open-product', 'assign-application', 'workspace-changed'],
  setup(_props, { expose }) {
    expose({ openCreateModal: vi.fn(), loadProjects: vi.fn() })
    return {}
  },
  template: '<div data-testid="repository-cards" />',
})
const organization = { businessProjects: [{
  id: 'business-1', name: 'Operations', revision: 3, products: [
    { id: 'prod-1', name: 'Portal', revision: 4, businessProjectId: 'business-1' },
    { id: 'prod-2', name: 'Back office', revision: 5, businessProjectId: 'business-1' },
  ],
}] }
let pinia: Pinia
let router: Router
let wrapper: VueWrapper | undefined
const dialogPrototype = HTMLDialogElement.prototype
const originalShow = Object.getOwnPropertyDescriptor(dialogPrototype, 'showModal')
const originalClose = Object.getOwnPropertyDescriptor(dialogPrototype, 'close')

async function render(url = '/projects'): Promise<VueWrapper> {
  await router.push(url)
  wrapper = mount(ProjectWorkspace, {
    attachTo: document.body,
    global: { plugins: [pinia, router], stubs: { ProjectsContent: RepositoryCards, ProductReleasePanel: true } },
  })
  await flushPromises()
  return wrapper
}

function editor(): HTMLDialogElement {
  return document.querySelector<HTMLDialogElement>('.workspace-editor')!
}

async function confirmEditor(): Promise<void> {
  editor().querySelector<HTMLButtonElement>('button[type=submit]')!.click()
  await flushPromises()
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(listWorkspace).mockReset()
  vi.mocked(listWorkspaceApplications).mockReset()
  vi.mocked(assignWorkspaceApplication).mockReset()
  setLocale('en')
  pinia = createPinia()
  setActivePinia(pinia)
  useSessionStore().setUser({ username: 'admin', multiTenant: false })
  const Placeholder = defineComponent({ template: '<div />' })
  router = createRouter({ history: createMemoryHistory(), routes: [
    { name: 'projects', path: '/projects', component: Placeholder },
    { name: 'project-pipeline', path: '/projects/:id/pipeline', component: Placeholder },
    { name: 'run-detail', path: '/runs/:id', component: Placeholder },
  ] })
  vi.mocked(listWorkspace).mockResolvedValue(organization)
  vi.mocked(listWorkspaceApplications).mockResolvedValue({ items: [application], page: 1, pageSize: 20, total: 1 })
  vi.mocked(assignWorkspaceApplication).mockResolvedValue(undefined)
  Object.defineProperty(dialogPrototype, 'showModal', {
    configurable: true,
    value(this: HTMLDialogElement) { this.open = true },
  })
  Object.defineProperty(dialogPrototype, 'close', {
    configurable: true,
    value(this: HTMLDialogElement) {
      if (!this.open) return
      this.open = false
      this.dispatchEvent(new Event('close'))
    },
  })
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  disposePinia(pinia)
  if (originalShow) Object.defineProperty(dialogPrototype, 'showModal', originalShow)
  else Reflect.deleteProperty(dialogPrototype, 'showModal')
  if (originalClose) Object.defineProperty(dialogPrototype, 'close', originalClose)
  else Reflect.deleteProperty(dialogPrototype, 'close')
  vi.restoreAllMocks()
  setLocale('zh-CN')
})

describe('multi-project workspace', () => {
  it('passes the selected scope to the original card component and returns to all products', async () => {
    const page = await render('/projects?workspaceScope=business&workspaceId=business-1')
    expect(page.getComponent(RepositoryCards).props('workspace')).toMatchObject({ kind: 'business', id: 'business-1' })
    expect(page.findAll('.workspace-product-button').map(button => button.text())).toEqual(['Portal', 'Back office'])
    await page.get('.workspace-product-button').trigger('click')
    await flushPromises()
    expect(page.getComponent(RepositoryCards).props('workspace')).toMatchObject({ kind: 'product', id: 'prod-1', label: 'Operations / Portal' })
    await page.findAll('.workspace-crumb').find(button => button.text() === 'All products')!.trigger('click')
    await flushPromises()
    expect(page.getComponent(RepositoryCards).props('workspace')).toMatchObject({ kind: 'business', id: 'business-1' })
  })

  it('shows full repository and branch and keeps run and production facts distinct', async () => {
    const page = await render()
    page.getComponent(RepositoryCards).vm.$emit('open-application', application)
    await flushPromises()
    expect(page.get('[aria-labelledby=workspace-application-title]').text()).toContain(application.repoUrl)
    expect(page.text()).toContain(application.defaultBranch)
    expect(page.get('.workspace-pipeline-link').attributes('href')).toBe('/projects/app-1/pipeline')
    const buttons = page.findAll('.workspace-detail-tabs button')
    await buttons[1]!.trigger('click')
    expect(page.get('.workspace-fact').text()).toContain('failed-commit')
    expect(page.get('.workspace-fact a').attributes('href')).toBe('/runs/latest')
    await buttons[2]!.trigger('click')
    expect(page.get('.workspace-fact').text()).toContain('production-version')
    expect(page.get('.workspace-fact a').attributes('href')).toBe('/runs/deployed')
  })

  it('loads eligible visible applications for product attach and writes the original assignment revision', async () => {
    const page = await render('/projects?workspaceScope=product&workspaceId=prod-1')
    await page.findAll('button').find(button => button.text() === 'Add an existing application')!.trigger('click')
    await flushPromises()
    expect(editor().open).toBe(true)
    expect(listWorkspaceApplications).toHaveBeenCalledWith(expect.objectContaining({ kind: 'all' }), 1, 20, '', 'all', expect.anything())
    const input = editor().querySelector<HTMLSelectElement>('#workspace-attachment')!
    input.value = 'app-1'
    input.dispatchEvent(new Event('change'))
    await flushPromises()
    await confirmEditor()
    expect(assignWorkspaceApplication).toHaveBeenCalledOnce()
    expect(assignWorkspaceApplication).toHaveBeenCalledWith('app-1', 'prod-1', 7, expect.anything())
    expect(editor().open).toBe(false)
  })

  it('shows a revision conflict and requires a fresh read before trying again', async () => {
    vi.mocked(assignWorkspaceApplication).mockRejectedValueOnce(new HttpError(409, { code: 'workspace_conflict', message: 'stale' }, 'stale'))
    const page = await render()
    page.getComponent(RepositoryCards).vm.$emit('assign-application', application)
    await flushPromises()
    const input = editor().querySelector<HTMLSelectElement>('#workspace-assignment')!
    input.value = 'prod-2'
    input.dispatchEvent(new Event('change'))
    await flushPromises()
    await confirmEditor()
    expect(editor().querySelector('[role=alert]')!.textContent).toContain('Refresh and try again.')
    expect(editor().querySelector('button[type=submit]')).toBeNull()
    Array.from(editor().querySelectorAll('button')).find(button => button.textContent?.trim() === 'Refresh')!.click()
    await flushPromises()
    expect(listWorkspace).toHaveBeenCalledTimes(2)
    expect(assignWorkspaceApplication).toHaveBeenCalledOnce()
  })
})
