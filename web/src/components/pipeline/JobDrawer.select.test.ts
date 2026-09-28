import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import AppSelect from '../ui/AppSelect.vue'
import JobDrawer from './JobDrawer.vue'
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
