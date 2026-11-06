import { nextTick } from 'vue'
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it } from 'vitest'
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

function mountDrawer() {
  return mount(JobDrawer, {
    attachTo: document.body,
    props: { job, stage },
  })
}

let wrapper: ReturnType<typeof mountDrawer>

function commandEditor(): HTMLDialogElement | null {
  return document.body.querySelector('.command-editor-dialog')
}

function commandTextarea(): HTMLTextAreaElement {
  return document.body.querySelector('.command-editor-dialog__textarea')!
}

function saveButton(): HTMLButtonElement {
  return commandEditor()!.querySelector('.app-btn--primary')!
}

function inlineCommands(): HTMLTextAreaElement {
  return wrapper.get('[data-config-key="commands"] textarea').element as HTMLTextAreaElement
}

async function openEditor(): Promise<void> {
  await wrapper.get('.command-editor-trigger').trigger('click')
  await nextTick()
}

function setCommandDraft(value: string): void {
  const textarea = commandTextarea()
  textarea.value = value
  textarea.dispatchEvent(new Event('input', { bubbles: true }))
}

function pressKey(key: string, init: KeyboardEventInit = {}): void {
  document.activeElement!.dispatchEvent(
    new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true, ...init }),
  )
}

// jsdom does not implement the modal lifecycle.
const dialogPrototype = HTMLDialogElement.prototype
const nativeDialogMethods = {
  showModal: Object.getOwnPropertyDescriptor(dialogPrototype, 'showModal'),
  close: Object.getOwnPropertyDescriptor(dialogPrototype, 'close'),
}

beforeAll(() => {
  Object.defineProperties(dialogPrototype, {
    showModal: {
      configurable: true,
      value(this: HTMLDialogElement) {
        this.setAttribute('open', '')
      },
    },
    close: {
      configurable: true,
      value(this: HTMLDialogElement) {
        this.removeAttribute('open')
      },
    },
  })
})

afterAll(() => {
  for (const [name, descriptor] of Object.entries(nativeDialogMethods)) {
    if (descriptor) Object.defineProperty(dialogPrototype, name, descriptor)
    else Reflect.deleteProperty(dialogPrototype, name)
  }
})

beforeEach(() => {
  wrapper = mountDrawer()
})

afterEach(() => {
  wrapper.unmount()
  document.body.innerHTML = ''
  document.body.style.overflow = ''
})

describe('JobDrawer expanded command editor', () => {
  it('opens a named modal with the current commands and focuses the editor', async () => {
    const triggers = wrapper.findAll('.command-editor-trigger')
    expect(triggers).toHaveLength(1)
    expect(triggers[0].attributes('aria-label')).toBe('放大编辑执行命令')

    await openEditor()

    const dialog = commandEditor()!
    expect(dialog.open).toBe(true)
    const title = document.getElementById(dialog.getAttribute('aria-labelledby')!)
    expect(title?.textContent?.trim()).toBe('编辑执行命令')
    expect(saveButton().textContent?.trim()).toBe('应用到节点')
    expect(commandTextarea().value).toBe(job.config.commands)
    expect(document.activeElement).toBe(commandTextarea())
    expect(document.body.style.overflow).toBe('hidden')
  })

  it('only dismisses an unchanged command draft when the backdrop is clicked', async () => {
    await openEditor()
    const clickBackdrop = async () => {
      const dialog = commandEditor()!
      for (const type of ['pointerdown', 'pointerup']) {
        const event = new MouseEvent(type, { clientX: 1, clientY: 1 })
        Object.assign(event, { pointerId: 1, isPrimary: true })
        dialog.dispatchEvent(event)
      }
      await nextTick()
    }

    setCommandDraft('echo unsaved')
    await clickBackdrop()
    expect(commandTextarea().value).toBe('echo unsaved')
    expect(wrapper.emitted('update')).toBeUndefined()

    setCommandDraft(job.config.commands)
    await clickBackdrop()
    expect(commandEditor()).toBeNull()
  })

  it('discards the draft on Escape and returns focus to the command trigger', async () => {
    const unrelated = document.createElement('button')
    document.body.append(unrelated)
    unrelated.focus()
    document.body.style.overflow = 'scroll'

    await openEditor()
    setCommandDraft('npm run changed')
    pressKey('Escape')
    await nextTick()

    expect(commandEditor()).toBeNull()
    expect(wrapper.emitted('update')).toBeUndefined()
    expect(inlineCommands().value).toBe(job.config.commands)
    expect(document.body.style.overflow).toBe('scroll')
    expect(document.activeElement).toBe(wrapper.get('.command-editor-trigger').element)
  })

  it('wraps Tab in both directions inside the dialog', async () => {
    await openEditor()
    const close = commandEditor()!.querySelector<HTMLButtonElement>('.command-editor-dialog__close')!
    close.focus()
    pressKey('Tab', { shiftKey: true })
    expect(document.activeElement).toBe(saveButton())

    pressKey('Tab')
    expect(document.activeElement).toBe(close)
  })

  it('writes the draft through the existing job update flow when saved', async () => {
    const draft = 'npm ci\nnpm test\nnpm run build'
    await openEditor()
    setCommandDraft(draft)
    saveButton().click()
    await nextTick()

    expect(commandEditor()).toBeNull()
    expect(wrapper.emitted('update')).toEqual([[
      { name: job.name, type: job.type, summary: job.summary, config: { ...job.config, commands: draft } },
    ]])
    expect(inlineCommands().value).toBe(draft)
  })

  it('retains the draft across same-node save responses and preserves the latest fields on apply', async () => {
    await openEditor()
    const draft = 'npm run draft'
    setCommandDraft(draft)

    const latest = {
      ...job,
      name: 'Latest name',
      summary: 'Latest summary',
      config: { ...job.config, image: 'node:22', extraOption: 'latest value' },
    }
    await wrapper.setProps({ job: latest, stage: { ...stage, name: 'Latest stage name' } })

    expect(commandEditor()!.open).toBe(true)
    expect(commandTextarea().value).toBe(draft)
    expect(saveButton().disabled).toBe(false)
    pressKey('Enter', { ctrlKey: true })
    await nextTick()

    expect(commandEditor()).toBeNull()
    expect(wrapper.emitted('update')).toEqual([[
      { name: latest.name, type: latest.type, summary: latest.summary, config: { ...latest.config, commands: draft } },
    ]])
  })

  it('preserves the draft and blocks every apply action after a conflicting save response', async () => {
    await openEditor()
    const draft = 'npm run draft'
    const latest = 'npm run latest'
    setCommandDraft(draft)
    await wrapper.setProps({ job: { ...job, config: { ...job.config, commands: latest } } })

    const dialog = commandEditor()!
    const alert = dialog.querySelector('[role="alert"]')!
    expect(alert.textContent).toContain('命令已更新')
    expect(commandTextarea().getAttribute('aria-describedby')).toBe(alert.id)
    expect(commandTextarea().value).toBe(draft)
    expect(commandTextarea().disabled).toBe(false)
    expect(saveButton().disabled).toBe(true)

    saveButton().click()
    pressKey('Enter', { ctrlKey: true })
    pressKey('Enter', { metaKey: true })
    await nextTick()
    expect(wrapper.emitted('update')).toBeUndefined()
    expect(commandEditor()).toBe(dialog)

    dialog.dispatchEvent(new Event('cancel', { cancelable: true }))
    await nextTick()
    expect(commandEditor()).toBeNull()

    await openEditor()
    expect(commandTextarea().value).toBe(latest)
    expect(commandEditor()!.querySelector('[role="alert"]')).toBeNull()
    expect(saveButton().disabled).toBe(false)
  })

  it('keeps Escape and shortcuts from closing or applying during IME composition', async () => {
    await openEditor()
    setCommandDraft('npm run draft')
    pressKey('Escape', { isComposing: true })
    pressKey('Enter', { ctrlKey: true, isComposing: true })

    commandTextarea().dispatchEvent(new Event('compositionstart'))
    pressKey('Enter', { metaKey: true })
    commandEditor()!.dispatchEvent(new Event('cancel', { cancelable: true }))
    await nextTick()

    expect(commandEditor()!.open).toBe(true)
    expect(commandTextarea().value).toBe('npm run draft')
    expect(wrapper.emitted('update')).toBeUndefined()

    commandTextarea().dispatchEvent(new Event('compositionend'))
    pressKey('Enter', { metaKey: true })
    await nextTick()
    expect(commandEditor()).toBeNull()
    expect(wrapper.emitted('update')).toHaveLength(1)
  })

  it.each([
    { field: 'job ID', props: { job: { ...job, id: 'job-2' } } },
    { field: 'job type', props: { job: { ...job, type: 'custom' } } },
    { field: 'stage ID', props: { stage: { ...stage, id: 'stage-2' } } },
  ])('discards the old draft when the $field changes', async ({ props }) => {
    await openEditor()
    setCommandDraft('draft for the old node')
    await wrapper.setProps(props)

    expect(commandEditor()).toBeNull()
    expect(wrapper.emitted('update')).toBeUndefined()
    await openEditor()
    expect(commandTextarea().value).toBe(job.config.commands)
  })
})
