import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import JobDrawer from './JobDrawer.vue'
import StepBuilder from './StepBuilder.vue'
import type { PipelineJob, PipelineStage } from '../../api/pipeline'

const stage: PipelineStage = { id: 's1', name: '构建', kind: 'build', jobs: [] }

// 步骤构建器适用于带预置语义的脚本类节点(前端/后端构建/模板),不再用于「自定义脚本」。
function builderJob(config: Record<string, string>): PipelineJob {
  return { id: 'j1', name: '前端构建', type: 'build_frontend', summary: '', config }
}

describe('JobDrawer + StepBuilder integration', () => {
  it('shows the step builder by default for a fresh build node', () => {
    const wrapper = mount(JobDrawer, {
      props: { job: builderJob({ image: 'node:20' }), stage },
    })
    expect(wrapper.findComponent(StepBuilder).exists()).toBe(true)
    // the view switch is present with two tabs
    expect(wrapper.findAll('.view-tab').length).toBe(2)
  })

  // 「自定义脚本」(script/custom)节点天然写命令文本:默认原始参数、无可视化步骤切换。
  it('uses raw view (no step builder, no view switch) for a 自定义脚本 node', () => {
    const job: PipelineJob = { id: 'js', name: '自定义脚本', type: 'script', summary: '', config: { image: 'node:20' } }
    const wrapper = mount(JobDrawer, { props: { job, stage } })
    expect(wrapper.findComponent(StepBuilder).exists()).toBe(false)
    expect(wrapper.findAll('.view-tab').length).toBe(0)
  })

  it('compiles builder steps into commands/artifactPath via emit("update")', async () => {
    const job = builderJob({ image: 'node:20', commands: 'npm ci', artifactPath: '' })
    const wrapper = mount(JobDrawer, { props: { job, stage } })

    const builder = wrapper.findComponent(StepBuilder)
    // simulate the builder emitting a compiled fragment (env + cd + cmd, plus artifact)
    builder.vm.$emit('update', {
      commands: "export CI='true'\ncd 'frontend'\nnpm run build",
      artifactPath: 'frontend/dist',
    })
    await wrapper.vm.$nextTick()

    const events = wrapper.emitted('update')
    expect(events).toBeTruthy()
    const last = events!.at(-1)![0] as Partial<PipelineJob>
    expect(last.config!.commands).toBe("export CI='true'\ncd 'frontend'\nnpm run build")
    expect(last.config!.artifactPath).toBe('frontend/dist')
    // node-level field preserved
    expect(last.config!.image).toBe('node:20')
  })

  it('reparses an existing config back into ordered step blocks on open', () => {
    const job = builderJob({
      image: 'node:20',
      commands: "export NODE_ENV='production'\ncd 'frontend'\nnpm ci\nnpm run build",
      artifactPath: 'frontend/dist',
    })
    const wrapper = mount(JobDrawer, { props: { job, stage } })
    const builder = wrapper.findComponent(StepBuilder)
    // 4 command-derived steps + 1 artifact step = 5 step rows
    expect(builder.findAll('.sb-step').length).toBe(5)
    // first step is the env block (compiled from export line)
    expect(builder.findAll('.sb-kind')[0].text()).toBe('设环境变量')
    expect(builder.findAll('.sb-kind')[1].text()).toBe('切目录')
  })

  it('defaults to raw view for a templated node (commandTemplate present)', () => {
    const job: PipelineJob = {
      id: 'j2',
      name: '自定义',
      type: 'templated',
      summary: '',
      config: { image: 'node:20', commandTemplate: 'cd {{dir}}\nnpm run build', params: 'dir=frontend' },
    }
    const wrapper = mount(JobDrawer, { props: { job, stage } })
    // step builder hidden because config uses the template render path
    expect(wrapper.findComponent(StepBuilder).exists()).toBe(false)
  })

  it('does not show the step builder for a non-script node (e.g. notify)', () => {
    const job: PipelineJob = { id: 'j3', name: '通知', type: 'notify', summary: '', config: {} }
    const wrapper = mount(JobDrawer, { props: { job, stage } })
    expect(wrapper.findComponent(StepBuilder).exists()).toBe(false)
    expect(wrapper.findAll('.view-tab').length).toBe(0)
  })
})
