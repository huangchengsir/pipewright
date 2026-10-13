import { defineComponent, h, reactive } from 'vue'
import { mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { usePipelineDraftSave, type PipelineDraftParticipant } from './usePipelineDraftSave'

vi.mock('vue-router', () => ({ onBeforeRouteLeave: vi.fn(), onBeforeRouteUpdate: vi.fn() }))

let wrapper: VueWrapper | undefined
function setup(parts: PipelineDraftParticipant[]) {
  let save!: ReturnType<typeof usePipelineDraftSave>
  wrapper = mount(defineComponent({
    setup() {
      save = usePipelineDraftSave({ participants: () => parts, confirmDiscard: async () => false })
      return () => h('div')
    },
  }))
  return save
}

function participant(scope: string) {
  const part = reactive({
    scopes: [scope], ready: true, dirty: true, saving: false,
    save: vi.fn<() => Promise<boolean>>(),
  })
  part.save.mockImplementation(async () => { part.dirty = false; return true })
  return part
}

afterEach(() => { wrapper?.unmount(); wrapper = undefined })

describe('usePipelineDraftSave outcomes', () => {
  it.each(['false', 'throw'])('records acknowledged scopes when a later participant returns %s', async (failure) => {
    const canvas = participant('canvas')
    const settings = participant('vars')
    const triggers = participant('triggers')
    const error = new Error('rejected')
    settings.save.mockImplementationOnce(async () => {
      if (failure === 'throw') throw error
      return false
    })
    const save = setup([canvas, settings, triggers])
    if (failure === 'throw') await expect(save.save()).rejects.toBe(error)
    else expect(await save.save()).toBe(false)
    expect(save.outcome.value).toEqual({ state: 'partial', completedScopes: ['canvas'], failedScopes: ['vars'] })
    expect(save.busy.value).toBe(false)
    expect(settings.dirty).toBe(true)
    expect(triggers.save).not.toHaveBeenCalled()
    expect(await save.save()).toBe(true)
    expect(canvas.save).toHaveBeenCalledTimes(1)
    expect(save.outcome.value).toEqual({ state: 'saved', completedScopes: ['vars', 'triggers'], failedScopes: [] })
  })

  it('does not infer partial success from scopes being named', async () => {
    const first = participant('canvas')
    first.save.mockResolvedValueOnce(false)
    const save = setup([first])
    expect(await save.save()).toBe(false)
    expect(save.outcome.value).toEqual({ state: 'error', completedScopes: [], failedScopes: ['canvas'] })
  })

  it('captures scopes at write time before acknowledgement clears dirty flags', async () => {
    const canvas = participant('canvas')
    let release!: () => void
    canvas.save.mockImplementationOnce(() => new Promise<boolean>((resolve) => {
      release = () => { canvas.dirty = false; resolve(true) }
    }))
    const build = reactive({ dirty: true })
    const env = reactive({ dirty: false })
    const settings: PipelineDraftParticipant = {
      get scopes() { return [...(build.dirty ? ['vars'] : []), ...(env.dirty ? ['envs'] : [])] },
      ready: true, dirty: true, saving: false,
      save: async () => { build.dirty = false; env.dirty = false; return true },
    }
    const last = participant('triggers')
    last.save.mockResolvedValueOnce(false)
    const save = setup([canvas, settings, last])
    const result = save.save()
    env.dirty = true
    release()
    expect(await result).toBe(false)
    expect(save.outcome.value).toEqual({
      state: 'partial', completedScopes: ['canvas', 'vars', 'envs'], failedScopes: ['triggers'],
    })
  })

  it('keeps newer edits pending without reporting a failure or accepting duplicate saves', async () => {
    const canvas = participant('canvas')
    let release!: () => void
    canvas.save.mockImplementationOnce(() => new Promise<boolean>((resolve) => { release = () => resolve(true) }))
    const save = setup([canvas])
    const result = save.save()
    expect(save.busy.value).toBe(true)
    expect(await save.save()).toBe(false)
    release()
    expect(await result).toBe(false)
    expect(canvas.save).toHaveBeenCalledTimes(1)
    expect(save.outcome.value).toEqual({ state: 'dirty', completedScopes: ['canvas'], failedScopes: [] })
    expect(save.dirty.value).toBe(true)
    expect(save.busy.value).toBe(false)
  })

  it('leaves an initially clean participant pending when it changes during another save', async () => {
    const canvas = participant('canvas')
    const preview = participant('preview')
    preview.dirty = false
    let release!: () => void
    canvas.save.mockImplementationOnce(() => new Promise<boolean>((resolve) => {
      release = () => { canvas.dirty = false; resolve(true) }
    }))
    const save = setup([canvas, preview])
    const result = save.save()
    preview.dirty = true
    release()
    expect(await result).toBe(false)
    expect(save.outcome.value?.state).toBe('dirty')
    expect(preview.save).not.toHaveBeenCalled()
    expect(save.dirty.value).toBe(true)
  })

  it('records validation failure before any writes and rejects unavailable configuration', async () => {
    const canvas = participant('canvas')
    const trigger = { ...participant('triggers'), validate: () => false }
    const save = setup([canvas, trigger])
    expect(await save.save()).toBe(false)
    expect(canvas.save).not.toHaveBeenCalled()
    expect(save.outcome.value).toEqual({ state: 'error', completedScopes: [], failedScopes: ['triggers'] })
    canvas.ready = false
    expect(save.ready.value).toBe(false)
    expect(await save.save()).toBe(false)
    expect(save.outcome.value?.state).toBe('unavailable')
    save.resetOutcome()
    expect(save.outcome.value).toBeNull()
  })
})
