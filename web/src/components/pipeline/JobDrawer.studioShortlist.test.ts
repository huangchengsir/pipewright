import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import JobDrawer from './JobDrawer.vue'
import StudioInstanceParams from './StudioInstanceParams.vue'
import StepBuilder from './StepBuilder.vue'
import { encodeStudioMeta, STUDIO_META_KEY, type PromotedParam } from './studioCompile'
import type { PipelineJob, PipelineStage } from '../../api/pipeline'

const stage: PipelineStage = { id: 's1', name: '构建', kind: 'build', jobs: [] }

function param(p: Partial<PromotedParam> & { key: string }): PromotedParam {
  return { label: p.key, type: 'text', default: '', ...p }
}

/** A templated (studio) node carrying promoted params (dir=text, ver=select). */
function studioJob(params: string): PipelineJob {
  const meta = encodeStudioMeta([
    param({ key: 'dir', label: '目录', type: 'text' }),
    param({ key: 'ver', label: 'Go 版本', type: 'select', options: ['1.22', '1.21'] }),
    param({ key: 'verbose', label: '详细日志', type: 'toggle' }),
  ])
  return {
    id: 'j1',
    name: '自定义构建',
    type: 'templated',
    summary: '',
    config: {
      image: 'golang:{{ver}}',
      commandTemplate: 'cd {{dir}}\ngo build ./...',
      params,
      [STUDIO_META_KEY]: meta,
    },
  }
}

describe('JobDrawer — studio instance shortlist', () => {
  it('renders the typed shortlist (not raw text) by default for a studio node', () => {
    const wrapper = mount(JobDrawer, {
      props: { job: studioJob('dir=web\nver=1.22\nverbose=false'), stage },
    })
    const shortlist = wrapper.findComponent(StudioInstanceParams)
    expect(shortlist.exists()).toBe(true)
    // definitions flow in from parsePromotedParams (label/type/options)
    const defs = shortlist.props('params') as PromotedParam[]
    expect(defs.map((d) => d.key)).toEqual(['dir', 'ver', 'verbose'])
    expect(defs.find((d) => d.key === 'ver')!.type).toBe('select')
    // current values seeded from params text
    expect(shortlist.props('modelValue')).toEqual({ dir: 'web', ver: '1.22', verbose: 'false' })
    // raw command-template textarea + step builder are NOT shown by default
    expect(wrapper.findComponent(StepBuilder).exists()).toBe(false)
    expect(wrapper.find('textarea').exists()).toBe(false)
    // advanced "查看/编辑原始参数" entry is present (collapsed)
    expect(wrapper.text()).toContain('查看/编辑原始参数')
  })

  it('writing a shortlist value flushes params text with only the value changed', async () => {
    const wrapper = mount(JobDrawer, {
      props: { job: studioJob('dir=web\nver=1.22\nverbose=false'), stage },
    })
    const shortlist = wrapper.findComponent(StudioInstanceParams)
    shortlist.vm.$emit('update:modelValue', { dir: 'frontend', ver: '1.21', verbose: 'true' })
    await wrapper.vm.$nextTick()

    const events = wrapper.emitted('update')
    expect(events).toBeTruthy()
    const last = events!.at(-1)![0] as Partial<PipelineJob>
    // params text rewritten (key order preserved), __studio meta + commandTemplate untouched
    expect(last.config!.params).toBe('dir=frontend\nver=1.21\nverbose=true')
    expect(last.config!.commandTemplate).toBe('cd {{dir}}\ngo build ./...')
    expect(last.config![STUDIO_META_KEY]).toBe(studioJob('').config![STUDIO_META_KEY])
  })

  it('expanding 高级 reveals the raw typed form (commandTemplate textarea)', async () => {
    const wrapper = mount(JobDrawer, {
      props: { job: studioJob('dir=web\nver=1.22\nverbose=false'), stage },
    })
    expect(wrapper.find('textarea').exists()).toBe(false)
    const toggle = wrapper.findAll('.advanced-toggle').find((b) => b.text().includes('查看/编辑原始参数'))
    expect(toggle).toBeTruthy()
    await toggle!.trigger('click')
    // raw view now exposes the params / commandTemplate textareas
    expect(wrapper.findAll('textarea').length).toBeGreaterThan(0)
  })

  it('round-trip: reload with edited params re-displays the new values', () => {
    const wrapper = mount(JobDrawer, {
      props: { job: studioJob('dir=web\nver=1.22\nverbose=false'), stage },
    })
    // simulate parent re-feeding the job with edited params (e.g. after save+reload)
    wrapper.setProps({ job: studioJob('dir=app\nver=1.21\nverbose=true') })
    return wrapper.vm.$nextTick().then(() => {
      const shortlist = wrapper.findComponent(StudioInstanceParams)
      expect(shortlist.props('modelValue')).toEqual({ dir: 'app', ver: '1.21', verbose: 'true' })
    })
  })

  it('non-studio node is unaffected (no shortlist, normal typed form)', () => {
    const job: PipelineJob = { id: 'j2', name: '通知', type: 'notify', summary: '', config: {} }
    const wrapper = mount(JobDrawer, { props: { job, stage } })
    expect(wrapper.findComponent(StudioInstanceParams).exists()).toBe(false)
    expect(wrapper.text()).not.toContain('实例参数')
    expect(wrapper.text()).not.toContain('查看/编辑原始参数')
  })

  it('templated node with no promoted params keeps the original raw form (no empty shortlist)', () => {
    const job: PipelineJob = {
      id: 'j3',
      name: '自定义',
      type: 'templated',
      summary: '',
      config: { image: 'node:20', commandTemplate: 'npm run build' },
    }
    const wrapper = mount(JobDrawer, { props: { job, stage } })
    expect(wrapper.findComponent(StudioInstanceParams).exists()).toBe(false)
    // raw typed config section still rendered
    expect(wrapper.text()).toContain('配置')
  })
})
