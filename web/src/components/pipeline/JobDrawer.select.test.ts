import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import AppSelect from '../ui/AppSelect.vue'
import JobDrawer from './JobDrawer.vue'
import ImageProvenanceSummary from './ImageProvenanceSummary.vue'
import ProjectSourceSummary from './ProjectSourceSummary.vue'
import type { Project } from '../../api/projects'
import type { PipelineJob, PipelineStage } from '../../api/pipeline'

const stage: PipelineStage = { id: 'stage-1', name: '部署', kind: 'deploy', jobs: [] }
const job: PipelineJob = {
  id: 'job-1',
  name: 'SSH 部署',
  type: 'deploy_ssh',
  summary: '',
  config: { artifactType: '' },
}

describe('JobDrawer deployment select', () => {
  it('distinguishes default cache inheritance from explicit empty node paths', async () => {
    const scriptJob = { ...job, type: 'script', config: { image: 'node:20', commands: 'npm ci' } }
    const wrapper = mount(JobDrawer, { props: { job: scriptJob, stage } })
    try {
      const field = wrapper.find('[data-config-key="cachePaths"]')
      const toggle = field.find('input[type="checkbox"]')
      expect((toggle.element as HTMLInputElement).checked).toBe(true)
      expect(field.find('textarea').attributes('disabled')).toBeDefined()
      await toggle.setValue(false)
      const explicit = (wrapper.emitted('update')!.at(-1)![0] as PipelineJob).config
      expect(Object.hasOwn(explicit, 'cachePaths')).toBe(true)
      expect(explicit.cachePaths).toBe('')
      await toggle.setValue(true)
      expect(Object.hasOwn((wrapper.emitted('update')!.at(-1)![0] as PipelineJob).config, 'cachePaths')).toBe(false)
    } finally { wrapper.unmount() }
  })
  it('shows actual project checkout bindings instead of editable ignored source parameters', () => {
    const sourceJob = { ...job, type: 'git_source', config: { repoUrl: 'https://ignored.test/repo.git', credentialId: 'ignored-credential' } }
    const project: Project = { id: 'project', name: 'Actual project', repoUrl: 'https://actual.test/repo.git', defaultBranch: 'main', credentialId: 'actual', credentialName: 'Actual OAuth credential', pacEnabled: false, prStatusEnabled: false, lastRunStatus: null, targetServers: [], createdAt: '', updatedAt: '' }
    const wrapper = mount(JobDrawer, { props: { job: sourceJob, stage, project }, global: { stubs: { RouterLink: { props: ['to'], template: '<a><slot /></a>' } } } })
    try {
      const binding = wrapper.findComponent(ProjectSourceSummary)
      expect(binding.text()).toContain('https://actual.test/repo.git')
      expect(binding.text()).toContain('Actual OAuth credential')
      expect(binding.text()).not.toContain('ignored.test')
      expect(wrapper.find('[data-config-key="repoUrl"]').exists()).toBe(false)
      expect(wrapper.emitted('update')).toBeUndefined()
    } finally { wrapper.unmount() }
  })

  it('exposes the current image source, tag template and source navigation without a previous run', async () => {
    const publish: PipelineStage = { id: 'publish', name: '推送到 Harbor', kind: 'build', jobs: [{
      id: 'push', name: '推送日期版本镜像', type: 'push_image', summary: '',
      config: { registry: 'http://127.0.0.1:18093/acceptance-194/', imageName: 'gitea-service', tag: 'oauth-${DATE:yyyyMMddHHmmss}-${COMMIT_SHA}' },
    }] }
    const imageJob = { ...job, config: { artifactType: 'image', serverId: 'missing-server' } }
    const dep = { ...stage, jobs: [imageJob], needs: ['publish'] }
    const wrapper = mount(JobDrawer, { props: { job: imageJob, stage: dep, stages: [publish, dep], servers: [] } })
    try {
      expect(wrapper.find('[data-config-key="artifactSource"]').exists()).toBe(true)
      const summary = wrapper.findComponent(ImageProvenanceSummary)
      expect(summary.exists()).toBe(true)
      expect(summary.text()).toContain('推送日期版本镜像')
      expect(summary.text()).toContain('127.0.0.1:18093/acceptance-194')
      expect(summary.text()).toContain('oauth-${DATE:yyyyMMddHHmmss}-${COMMIT_SHA}')
      expect(wrapper.find('[data-config-key="serverId"]').text()).toContain('不可用')
      await summary.find('button').trigger('click')
      expect(wrapper.emitted('inspect-source')?.[0]).toEqual(['push'])
    } finally { wrapper.unmount() }
  })

  it('keeps an unavailable credential reference visible and does not erase it', () => {
    const pushJob = { ...job, type: 'push_image', config: { credentialId: 'deleted-credential' } }
    const wrapper = mount(JobDrawer, { props: { job: pushJob, stage, credentials: [] } })
    try {
      const field = wrapper.find('[data-config-key="credentialId"]')
      expect(field.text()).toContain('不可用')
      expect(field.findComponent(AppSelect).props('modelValue')).toBe('deleted-credential')
      expect(wrapper.emitted('update')).toBeUndefined()
    } finally { wrapper.unmount() }
  })

  it('writes a custom-menu selection into the job config', async () => {
    const wrapper = mount(JobDrawer, { attachTo: document.body, props: { job, stage } })
    try {
      const artifactSelect = wrapper.findAllComponents(AppSelect).find((select) =>
        select.props('options').some((option: { value: string }) => option.value === 'archive'),
      )
      expect(artifactSelect).toBeDefined()
      await artifactSelect!.find('[role="combobox"]').trigger('click')
      const options = document.body.querySelectorAll('.app-select__menu--portal [role="option"]')
      expect(options).toHaveLength(5)
      ;(options[1] as HTMLButtonElement).click()
      await wrapper.vm.$nextTick()

      const updates = wrapper.emitted('update')
      expect(updates).toBeTruthy()
      expect((updates!.at(-1)![0] as Partial<PipelineJob>).config?.artifactType).toBe('image')
    } finally {
      wrapper.unmount()
    }
  })

  it('binds a declaration identity instead of a previous run artifact ID', async () => {
    const buildStage: PipelineStage = {
      id: 'build-stage', name: '构建', kind: 'build', jobs: [{
        id: 'frontend', name: '前端', type: 'build_frontend', summary: '',
        config: { artifacts: JSON.stringify([{ path: 'dist', name: 'Web UI' }]) },
      }],
    }
    const wrapper = mount(JobDrawer, {
      attachTo: document.body,
      props: { job, stage, stages: [buildStage, stage] },
    })
    try {
      const source = wrapper.findAllComponents(AppSelect).find((select) =>
        select.props('options').some((option: { label: string }) => option.label.includes('Web UI')),
      )
      expect(source).toBeDefined()
      await source!.find('[role="combobox"]').trigger('click')
      ;(document.body.querySelectorAll('.app-select__menu--portal [role="option"]')[1] as HTMLButtonElement).click()
      await wrapper.vm.$nextTick()

      const updates = wrapper.emitted('update')
      const config = (updates!.at(-1)![0] as Partial<PipelineJob>).config!
      expect(JSON.parse(config.artifactSource)).toEqual({ stageId: 'build-stage', jobId: 'frontend', declarationIndex: 0 })
    } finally {
      wrapper.unmount()
    }
  })
})
