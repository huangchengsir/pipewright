import { afterEach, beforeEach, describe, it, expect, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import { useSessionStore } from '../../stores/session'
import { usePermissionStore } from '../../stores/permissions'
import { access } from '../../api/access'
import { aiApply, aiGenerate, type AIGenerateResponse } from '../../api/aiGenerate'
import { t } from '../../i18n'
import AIGenerateWizard from './AIGenerateWizard.vue'

vi.mock('../../api/access', () => ({ access: { decisions: vi.fn() } }))
vi.mock('../../api/aiGenerate', () => ({ aiGenerate: vi.fn(), aiApply: vi.fn() }))

const noWireProposal: AIGenerateResponse['proposal'] = null
let wrapper: VueWrapper | undefined
async function settle() { for (let i = 0; i < 6; i++) await flushPromises() }
beforeEach(() => {
  vi.resetAllMocks()
  setActivePinia(createPinia())
  useSessionStore().setUser({ username: 'member', userId: 'u', multiTenant: true })
  usePermissionStore().setTeam('t')
  vi.mocked(access.decisions).mockImplementation(async (team, queries) => ({
    userId: 'u', teamId: team, modeEpoch: 1, revision: 1, catalogVersion: 1,
    allowed: queries.map(query => team === 't' && query.action === 'pipeline.edit' && query.resourceId === 'p'),
  }))
})
afterEach(() => { wrapper?.unmount(); wrapper = undefined })

describe('AI generation wizard exact local controls', () => {
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

  const button = (selector: string, key: string) =>
    wrapper!.findAll(selector).find(element => element.text() === t('pipelinePanels.' + key))!
  async function showWizard(useRouter = false) {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/', component: { template: '<p>Fixture home</p>' } },
        { path: '/settings/ai', component: { template: '<p>Fixture AI settings</p>' } },
      ],
    })
    if (useRouter) { await router.push('/'); await router.isReady() }
    wrapper = mount(AIGenerateWizard, { props: { projectId: 'p' },
      global: useRouter ? { plugins: [router] } : { stubs: { RouterLink: true } } })
    await settle()
    return router
  }
  async function result(available: boolean) {
    vi.mocked(aiGenerate).mockResolvedValueOnce({
      available, reason: 'fixture result without proposal',
      analysis: { cloned: true, language: 'go', languageVersion: '', buildTool: '',
        hasDockerfile: false, artifactHint: '', signals: [] },
      proposal: noWireProposal,
    })
    await button('.wiz-footer button', 'wizGenerateConfig').trigger('click')
    await settle()
  }

  it.each(['backdrop','header','idle-cancel'] as const)(
    'closes the idle wizard via %s without generation or apply',
    async control => {
      await showWizard()
      await wrapper!.get('.wiz-panel').trigger('click')
      expect(wrapper!.emitted('close')).toBeUndefined()
      const selectors = { backdrop: '.wiz-backdrop', header: '.wiz-close' }
      const close = control === 'idle-cancel' ? button('.wiz-footer button', 'wizCancel') : wrapper!.get(selectors[control])
      await close.trigger('click')
      expect(wrapper!.emitted('close')).toEqual([[]])
      expect(aiGenerate).not.toHaveBeenCalled()
      expect(aiApply).not.toHaveBeenCalled()
    },
  )

  it('navigates to AI settings from the unavailable link and closes without applying', async () => {
    const router = await showWizard(true)
    await result(false)
    const link = wrapper!.get('.unavail-actions a')
    expect(link.attributes('href')).toBe('/settings/ai')
    await link.trigger('click')
    await settle()
    expect(router.currentRoute.value.fullPath).toBe('/settings/ai')
    expect(wrapper!.emitted('close')).toEqual([[]])
    expect(aiGenerate).toHaveBeenCalledOnce()
    expect(aiApply).not.toHaveBeenCalled()
  })

  it.each([
    ['unavailable-manual', false, '.unavail-actions button', 'wizManualConfig'],
    ['unavailable-footer', false, '.wiz-footer button', 'wizClose'],
    ['available-without-proposal', true, '.wiz-footer button', 'wizClose'],
  ] as const)('closes the %s result through the specified button without applying', async (_, available, selector, key) => {
    await showWizard()
    await result(available)
    await button(selector, key).trigger('click')
    expect(wrapper!.emitted('close')).toEqual([[]])
    expect(aiGenerate).toHaveBeenCalledOnce()
    expect(aiApply).not.toHaveBeenCalled()
  })

  it('keeps both generating footer buttons inert while the one model request is pending', async () => {
    await showWizard()
    let complete!: (value: AIGenerateResponse) => void
    vi.mocked(aiGenerate).mockReturnValueOnce(new Promise(resolve => { complete = resolve }))
    await button('.wiz-footer button', 'wizGenerateConfig').trigger('click')
    const cancel = button('.wiz-footer button', 'wizCancel')
    const loading = button('.wiz-footer button', 'wizAnalyzing')
    for (const control of [cancel, loading]) {
      expect(control.attributes('disabled')).toBeDefined()
      control.element.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    }
    expect(loading.attributes('aria-busy')).toBe('true')
    expect(wrapper!.emitted('close')).toBeUndefined()
    expect(aiGenerate).toHaveBeenCalledOnce()
    expect(aiApply).not.toHaveBeenCalled()
    complete({available: false, reason: 'fixture', analysis: {
      cloned: false, language: '', languageVersion: '', buildTool: '', hasDockerfile: false, artifactHint: '', signals: [],
    }, proposal: noWireProposal})
    await settle()
  })

  it('selects and clears the entire proposal via its exact shortlist button without applying', async () => {
    await showWizard()
    vi.mocked(aiGenerate).mockResolvedValueOnce(proposal)
    await button('.wiz-footer button', 'wizGenerateConfig').trigger('click')
    await settle()
    const toggle = wrapper!.get('.proposal-header-actions .sel-btn')
    const inputs = () => wrapper!.findAll('.proposal-section input[type="checkbox"]')
    expect(inputs()).toHaveLength(4)
    expect(inputs().every(input => (input.element as HTMLInputElement).checked)).toBe(true)
    await toggle.trigger('click')
    expect(inputs().every(input => !(input.element as HTMLInputElement).checked)).toBe(true)
    expect(button('.wiz-footer button', 'wizApplySelected').attributes('disabled')).toBeDefined()
    await toggle.trigger('click')
    expect(inputs().every(input => (input.element as HTMLInputElement).checked)).toBe(true)
    expect(button('.wiz-footer button', 'wizApplySelected').attributes('disabled')).toBeUndefined()
    expect(aiGenerate).toHaveBeenCalledOnce()
    expect(aiApply).not.toHaveBeenCalled()
  })
})
