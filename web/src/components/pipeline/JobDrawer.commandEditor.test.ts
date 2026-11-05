import { nextTick } from 'vue'
import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import JobDrawer from './JobDrawer.vue'
import type { PipelineJob, PipelineStage } from '../../api/pipeline'

const stage: PipelineStage = {
  id: 'stage-1',
  name: '构建',
  kind: 'build',
  jobs: [],
}
const job: PipelineJob = {
  id: 'job-1',
  name: '自定义脚本',
  type: 'script',
  summary: '',
  config: { image: 'node:20', commands: 'npm ci\nnpm run build' },
}

function commandEditor(): HTMLElement | null {
  return document.body.querySelector('.command-editor-dialog')
}

function commandTextarea(): HTMLTextAreaElement {
  return document.body.querySelector(
    '.command-editor-dialog__textarea',
  ) as HTMLTextAreaElement
}

function setCommandDraft(value: string): void {
  const textarea = commandTextarea()
  textarea.value = value
  textarea.dispatchEvent(new Event('input', { bubbles: true }))
}

describe('JobDrawer expanded command editor', () => {
  it('opens from the commands field with the current value and focuses the editor', async () => {
    const wrapper = mount(JobDrawer, {
      attachTo: document.body,
      props: { job, stage },
    })
    try {
      const triggers = wrapper.findAll('.command-editor-trigger')
      expect(triggers).toHaveLength(1)
      expect(triggers[0].attributes('aria-label')).toBe('放大编辑执行命令')

      await triggers[0].trigger('click')
      await nextTick()

      expect(commandEditor()).not.toBeNull()
      expect(commandTextarea().value).toBe('npm ci\nnpm run build')
      expect(document.activeElement).toBe(commandTextarea())
      expect(document.body.style.overflow).toBe('hidden')
    } finally {
      wrapper.unmount()
      document.body.innerHTML = ''
    }
  })

  it('discards the draft when Escape closes the editor', async () => {
    const wrapper = mount(JobDrawer, {
      attachTo: document.body,
      props: { job, stage },
    })
    try {
      await wrapper.get('.command-editor-trigger').trigger('click')
      setCommandDraft('npm run changed')
      commandEditor()!.dispatchEvent(
        new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }),
      )
      await nextTick()

      expect(commandEditor()).toBeNull()
      expect(wrapper.emitted('update')).toBeUndefined()
      expect(
        (
          wrapper.get('[data-config-key="commands"] textarea')
            .element as HTMLTextAreaElement
        ).value,
      ).toBe('npm ci\nnpm run build')
      expect(document.body.style.overflow).toBe('')
    } finally {
      wrapper.unmount()
      document.body.innerHTML = ''
    }
  })

  it('writes the draft through the existing job update flow when saved', async () => {
    const wrapper = mount(JobDrawer, {
      attachTo: document.body,
      props: { job, stage },
    })
    try {
      await wrapper.get('.command-editor-trigger').trigger('click')
      setCommandDraft('npm ci\nnpm test\nnpm run build')
      const buttons = Array.from(
        document.body.querySelectorAll<HTMLButtonElement>(
          '.command-editor-dialog .app-btn',
        ),
      )
      buttons.find((button) => button.textContent?.trim() === '保存')!.click()
      await nextTick()

      expect(commandEditor()).toBeNull()
      const update = wrapper
        .emitted('update')!
        .at(-1)![0] as Partial<PipelineJob>
      expect(update.config?.commands).toBe('npm ci\nnpm test\nnpm run build')
      expect(update.config?.image).toBe('node:20')
      expect(
        (
          wrapper.get('[data-config-key="commands"] textarea')
            .element as HTMLTextAreaElement
        ).value,
      ).toBe('npm ci\nnpm test\nnpm run build')
    } finally {
      wrapper.unmount()
      document.body.innerHTML = ''
    }
  })
})
