export interface RenderGridLayout {
  coverage: 'corners' | 'sides'
  diagonal: 'primary' | 'secondary'
}

export interface RenderGridEngine {
  setLayout(layout: RenderGridLayout): void
  setWaiting(waiting: boolean): void
  react(reaction: 'fillIn' | 'press' | 'error' | 'success'): void
  refresh(): void
  destroy(): void
}

export interface RenderGridOptions {
  container: HTMLElement
  canvas: HTMLCanvasElement
  content?: HTMLElement | null
  protectedElements?: HTMLElement[]
  coverage?: RenderGridLayout['coverage']
  diagonal?: RenderGridLayout['diagonal']
  motion?: boolean
}

export function createRenderGrid(
  options: RenderGridOptions,
): RenderGridEngine | null
