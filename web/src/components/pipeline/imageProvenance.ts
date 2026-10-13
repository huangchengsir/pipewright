import type { PipelineJob, PipelineStage } from '../../api/pipeline'
import { hasAnyNeeds } from './stageDeps'
import { isValidAxisName, MATRIX_MAX_AXES, MATRIX_MAX_CELLS, matrixCellCount } from './stageSettings'

export interface ImageSource {
  stage: PipelineStage
  job: PipelineJob
  inspectJobId: string
  selector: string
}

function isImageJob(job: PipelineJob): boolean {
  return job.type.trim() === 'push_image' || (job.type.trim() === 'build_image' && (!job.config.artifactType?.trim() || job.config.artifactType.trim() === 'image'))
}

interface ProjectedStage {
  stage: PipelineStage
  originalStageId: string
  jobSuffix: string
  inspectJobIds: Map<string, string>
}

function matrixCells(matrix: PipelineStage['matrix']): Record<string, string>[] | null {
  const normalized: Record<string, string[]> = Object.create(null)
  for (const [rawAxis, rawValues] of Object.entries(matrix ?? {})) {
    const axis = rawAxis.trim()
    const values = [...new Set(rawValues.map((value) => value.trim()).filter(Boolean))]
    if (!axis && !values.length) continue
    if (!isValidAxisName(axis) || !values.length || Object.hasOwn(normalized, axis)) return null
    normalized[axis] = values
  }
  const axes = Object.keys(normalized).sort()
  if (axes.length > MATRIX_MAX_AXES || matrixCellCount(normalized) > MATRIX_MAX_CELLS) return null
  let cells: Record<string, string>[] = [{}]
  for (const axis of axes) cells = cells.flatMap((cell) => normalized[axis].map((value) => ({ ...cell, [axis]: value })))
  return cells
}

function cellSuffix(cell: Record<string, string>): string {
  const sanitize = (value: string) => value.trim().replace(/[^A-Za-z0-9_.-]/gu, '-') || 'x'
  return Object.keys(cell).sort().map((axis) => `${sanitize(axis)}-${sanitize(cell[axis])}`).join('_')
}

/** Project identities exactly as dagrun does, without mutating canvas declarations. */
function projectStages(stages: readonly PipelineStage[]): ProjectedStage[] | null {
  const groups = new Map<string, ProjectedStage[]>()
  for (const stage of stages) {
    if (groups.has(stage.id)) return null
    const cells = matrixCells(stage.matrix)
    if (!cells) return null
    groups.set(stage.id, cells.map((cell) => {
      const suffix = cellSuffix(cell)
      const jobSuffix = suffix ? `__${suffix}` : ''
      const jobs = stage.jobs.map((job) => ({ ...job, id: job.id + jobSuffix, type: job.type.trim() }))
      return {
        stage: {
          ...stage, id: stage.id + jobSuffix, matrix: undefined, jobs,
          name: suffix ? `${stage.name} [${Object.keys(cell).sort().map((axis) => `${axis}=${cell[axis]}`).join(', ')}]` : stage.name,
        },
        originalStageId: stage.id, jobSuffix,
        inspectJobIds: new Map(jobs.map((job, index) => [job.id, stage.jobs[index].id])),
      }
    }))
  }
  const projected = [...groups.values()].flat()
  if (new Set(projected.map(({ stage }) => stage.id)).size !== projected.length) return null
  for (const projection of projected) {
    projection.stage.needs = (projection.stage.needs ?? []).flatMap((id) => groups.get(id)?.map(({ stage }) => stage.id) ?? [id])
  }
  return projected
}

type SourceContext = {
  status: 'ready'
  stages: ProjectedStage[]
  stage: PipelineStage
  job: PipelineJob
} | { status: 'missing' | 'ambiguous' }

function sourceContext(stages: readonly PipelineStage[], stage: PipelineStage, job: PipelineJob): SourceContext {
  const projected = projectStages(stages.map((candidate) => candidate.id === stage.id ? stage : candidate))
  if (!projected) return { status: 'missing' }
  const targets = projected.filter((candidate) => candidate.originalStageId === stage.id || candidate.stage.id === stage.id)
  // A matrix declaration with multiple deploy cells has no single runtime context.
  if (targets.length !== 1) return { status: targets.length ? 'ambiguous' : 'missing' }
  const target = targets[0]
  const runtimeContext = stage.id !== target.originalStageId
  const jobId = runtimeContext && target.inspectJobIds.has(job.id) ? job.id : job.id + target.jobSuffix
  return { status: 'ready', stages: projected, stage: target.stage, job: { ...job, id: jobId } }
}

function hasJobDAG(stage: PipelineStage): boolean {
  return stage.jobs.some((job) => !!job.needs?.length)
    || (stage.jobs.length > 1 && stage.jobs.every((job) => !!job.id.trim()))
}

function jobAncestors(stage: PipelineStage, job: PipelineJob): Set<string> {
  const seen = new Set<string>()
  const pending = [...(job.needs ?? [])]
  while (pending.length) {
    const id = pending.pop()!
    if (seen.has(id)) continue
    seen.add(id)
    pending.push(...(stage.jobs.find((candidate) => candidate.id === id)?.needs ?? []))
  }
  return seen
}

function consumedBuilds(stage: PipelineStage): Set<string> {
  const consumed = new Set<string>()
  for (const job of stage.jobs.filter((candidate) => candidate.type === 'push_image')) {
    const ancestors = jobAncestors(stage, job)
    for (const build of stage.jobs.filter((candidate) => candidate.type === 'build_image' && isImageJob(candidate))) {
      if (!job.needs?.length || ancestors.has(build.id)) consumed.add(build.id)
    }
  }
  return consumed
}

function sources(stages: readonly ProjectedStage[]): ImageSource[] {
  return stages.flatMap(({ stage, originalStageId, inspectJobIds }) => {
    const hidden = consumedBuilds(stage)
    return stage.jobs.filter((job) => originalStageId.trim() && inspectJobIds.get(job.id)?.trim() && isImageJob(job) && !hidden.has(job.id)).map((job) => ({
      stage, job, inspectJobId: inspectJobIds.get(job.id)!,
      selector: JSON.stringify({ stageId: stage.id, jobId: job.id, declarationIndex: 0 }),
    }))
  })
}

function choicesInContext({ stages: projections, stage, job }: Extract<SourceContext, { status: 'ready' }>): ImageSource[] {
  const stages = projections.map((projection) => projection.stage)
  const ancestors = jobAncestors(stage, job)
  const upstream = new Set<string>()
  if (!hasAnyNeeds(stages)) {
    const index = stages.findIndex((candidate) => candidate.id === stage.id)
    for (const candidate of stages.slice(0, Math.max(0, index))) upstream.add(candidate.id)
  } else {
    const pending = [...(stage.needs ?? [])]
    while (pending.length) {
      const id = pending.pop()!
      if (upstream.has(id)) continue
      upstream.add(id)
      pending.push(...(stages.find((candidate) => candidate.id === id)?.needs ?? []))
    }
  }
  return sources(projections).filter((source) => upstream.has(source.stage.id)
    || (source.stage.id === stage.id && source.job.id !== job.id && (!hasJobDAG(stage) || ancestors.has(source.job.id))))
}

/** Explicit choices must finish before the deploy; auto selection uses direct dependencies only. */
export function imageSourceChoices(stages: readonly PipelineStage[], stage: PipelineStage, job: PipelineJob): ImageSource[] {
  const context = sourceContext(stages, stage, job)
  return context.status === 'ready' ? choicesInContext(context) : []
}

function pushBuildSelector(source: ImageSource, outputs: readonly ImageSource[], visited = new Set<string>()): string | null {
  if (visited.has(source.selector)) return null
  visited.add(source.selector)
  const ancestors = jobAncestors(source.stage, source.job)
  const localBuilds = source.stage.jobs.filter((job) => job.type.trim() === 'build_image' && isImageJob(job)
    && (!source.job.needs?.length || ancestors.has(job.id)))
  if (localBuilds.length === 1) {
    return JSON.stringify({ stageId: source.stage.id, jobId: localBuilds[0].id, declarationIndex: 0 })
  }
  // The executor never falls back upstream when a same-stage build exists but is unusable.
  if (localBuilds.length || source.stage.jobs.some((job) => job.type.trim() === 'build_image')) return null
  const upstream = outputs.filter((output) => (source.stage.needs ?? []).includes(output.stage.id))
  const origins = upstream.map((output) => output.job.type.trim() === 'build_image'
    ? output.selector : pushBuildSelector(output, outputs, new Set(visited)))
  if (!origins.length || origins.some((origin) => origin === null)) return null
  const unique = new Set(origins)
  return unique.size === 1 ? origins[0] : null
}

function selectorMatches(raw: string, source: ImageSource): boolean {
  try {
    const value = JSON.parse(raw) as Record<string, unknown> | null
    return !!value && !Array.isArray(value)
      && Object.keys(value).length === 3
      && value.stageId === source.stage.id && value.jobId === source.job.id && value.declarationIndex === 0
  } catch { return false }
}

export function resolveImageSource(stages: readonly PipelineStage[], stage: PipelineStage, job: PipelineJob, environmentAutoPushPossible = false): {
  status: 'selected' | 'missing' | 'ambiguous' | 'unavailable'
  source?: ImageSource
} {
  const context = sourceContext(stages, stage, job)
  if (context.status !== 'ready') return { status: context.status }
  const choices = choicesInContext(context)
  if (job.config.artifactSource) {
    const matches = choices.filter((source) => selectorMatches(job.config.artifactSource, source))
    return matches.length === 1 ? { status: 'selected', source: matches[0] } : { status: 'unavailable' }
  }
  // BuildGraph's legacy linear scheduling does not rewrite stage.Needs passed to the executor.
  const candidates = choices.filter((source) => (context.stage.needs ?? []).includes(source.stage.id)
    || source.stage.id === context.stage.id)
  // A published output replaces its local build output when both are in the dependency scope.
  const shadowed = new Set<string>()
  const outputs = sources(context.stages)
  for (const source of candidates.filter((candidate) => candidate.job.type === 'push_image')) {
    const build = pushBuildSelector(source, outputs)
    // Cross-stage builds may already be published by the selected environment;
    // the executor only shadows unpublished outputs, not those remote images.
    if (build && !environmentAutoPushPossible) shadowed.add(build)
  }
  const eligible = candidates.filter((source) => !shadowed.has(source.selector))
  if (eligible.length === 1) return { status: 'selected', source: eligible[0] }
  return { status: eligible.length ? 'ambiguous' : 'missing' }
}

export function projectImageName(name: string): string {
  return name.trim().toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '') || 'app'
}

/** Reject unsafe/invalid URLs rather than echoing userinfo or claiming a resolved registry. */
export function registryRepository(raw: string): string | null {
  if (!raw || /[\s\x00-\x1f\x7f\\?#%@]/.test(raw)) return null
  try {
    const address = raw.includes('://') ? raw : `https://${raw}`
    const parts = /^(https?):\/\/([^/]+)(\/.*)?$/.exec(address)
    if (!parts) return null
    const [, , authority, rawPath = ''] = parts
    if (rawPath.includes('//') || rawPath.split('/').some((part) => part === '.' || part === '..')) return null
    const hostParts = /^(\[[^\]]+\]|[^:]+)(?::([0-9]+))?$/.exec(authority)
    if (!hostParts) return null
    const [, hostname, port] = hostParts
    if (port && (Number(port) < 1 || Number(port) > 65535)) return null
    const url = new URL(address)
    if (url.username || url.password || !url.hostname) return null
    if (!hostname.includes('.') && !hostname.includes(':') && hostname.toLowerCase() !== 'localhost' && !port) return null
    if (hostname.length > 253) return null
    if (!hostname.includes(':') && !hostname.split('.').every((part) => part.length <= 63 && /^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/i.test(part))) return null
    const path = rawPath.replace(/^\/|\/$/g, '')
    if (path.length > 255 || (path && !path.split('/').every((part) => /^[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*$/.test(part)))) return null
    return authority.toLowerCase() + (path ? `/${path}` : '')
  } catch { return null }
}
