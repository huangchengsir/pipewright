import { mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createRenderGrid, type RenderGridEngine } from '../../lib/renderGrid'
import LoginGridBackground from './LoginGridBackground.vue'

vi.mock('../../lib/renderGrid', () => ({ createRenderGrid: vi.fn() }))
const createGrid = vi.mocked(createRenderGrid)

function mediaQuery(matches: boolean) {
  const listeners = new Set<EventListener>()
  const query = {
    matches,
    addEventListener: (_event: string, listener: EventListener) =>
      listeners.add(listener),
    removeEventListener: (_event: string, listener: EventListener) =>
      listeners.delete(listener),
  }
  return {
    query: query as unknown as MediaQueryList,
    listeners,
    setMatches(value: boolean) {
      query.matches = value
      for (const listener of listeners) listener(new Event('change'))
    },
  }
}

let desktop: ReturnType<typeof mediaQuery>
let reducedMotion: ReturnType<typeof mediaQuery>
let instances: RenderGridEngine[]
let wrapper: VueWrapper | undefined
let content: HTMLDivElement
let footer: HTMLElement

beforeEach(() => {
  desktop = mediaQuery(true)
  reducedMotion = mediaQuery(false)
  instances = []
  content = document.createElement('div')
  footer = document.createElement('footer')
  vi.stubGlobal(
    'matchMedia',
    vi.fn((query: string) =>
      query.includes('min-width') ? desktop.query : reducedMotion.query,
    ),
  )
  createGrid.mockReset().mockImplementation(() => {
    const instance = {
      setLayout: vi.fn(),
      setWaiting: vi.fn(),
      react: vi.fn(),
      refresh: vi.fn(),
      destroy: vi.fn(),
    }
    instances.push(instance)
    return instance
  })
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  vi.unstubAllGlobals()
})

function render() {
  wrapper = mount(LoginGridBackground, {
    props: { content, protectedElements: [footer] },
  })
  return wrapper
}

describe('LoginGridBackground', () => {
  it('uses the extracted corner engine and protects the existing content', () => {
    const view = render()
    expect(createGrid).toHaveBeenCalledOnce()
    expect(createGrid).toHaveBeenCalledWith(
      expect.objectContaining({
        canvas: view.get('canvas').element,
        container: view.get('.showcase-grid').element,
        content,
        protectedElements: [footer],
        coverage: 'corners',
        diagonal: 'secondary',
        motion: true,
      }),
    )
    expect(view.get('.showcase-grid').attributes('aria-hidden')).toBe('true')
  })

  it('renders a static corner pattern when reduced motion is requested', () => {
    reducedMotion.setMatches(true)
    render()
    expect(createGrid).toHaveBeenCalledWith(
      expect.objectContaining({ motion: false }),
    )
  })

  it('does not start or allocate the canvas on phone and tablet widths', () => {
    desktop.setMatches(false)
    const view = render()
    expect(createGrid).not.toHaveBeenCalled()
    expect((view.get('canvas').element as HTMLCanvasElement).width).toBe(0)
  })

  it('destroys the desktop engine below the breakpoint and resumes on desktop', () => {
    const view = render()
    desktop.setMatches(false)
    expect(instances[0]!.destroy).toHaveBeenCalledOnce()
    expect((view.get('canvas').element as HTMLCanvasElement).width).toBe(0)
    desktop.setMatches(true)
    expect(createGrid).toHaveBeenCalledTimes(2)
    expect(instances[1]!.destroy).not.toHaveBeenCalled()
  })

  it('responds to changes in the system motion preference', () => {
    render()
    reducedMotion.setMatches(true)
    expect(instances[0]!.destroy).toHaveBeenCalledOnce()
    expect(createGrid).toHaveBeenLastCalledWith(
      expect.objectContaining({ motion: false }),
    )
    reducedMotion.setMatches(false)
    expect(instances[1]!.destroy).toHaveBeenCalledOnce()
    expect(createGrid).toHaveBeenLastCalledWith(
      expect.objectContaining({ motion: true }),
    )
  })

  it('updates protected elements when the parent template refs become available', async () => {
    const view = render()
    const nextContent = document.createElement('div')
    await view.setProps({ content: nextContent, protectedElements: [] })
    expect(instances[0]!.destroy).toHaveBeenCalledOnce()
    expect(createGrid).toHaveBeenLastCalledWith(
      expect.objectContaining({ content: nextContent, protectedElements: [] }),
    )
  })

  it('cleans up the engine and media listeners when leaving the page', () => {
    render().unmount()
    wrapper = undefined
    expect(instances[0]!.destroy).toHaveBeenCalledOnce()
    expect(desktop.listeners.size).toBe(0)
    expect(reducedMotion.listeners.size).toBe(0)
    desktop.setMatches(false)
    desktop.setMatches(true)
    expect(createGrid).toHaveBeenCalledOnce()
  })

  it('handles an unavailable canvas context without affecting the page', () => {
    createGrid.mockReturnValueOnce(null)
    expect(() => render()).not.toThrow()
    expect(() => desktop.setMatches(false)).not.toThrow()
  })
})
