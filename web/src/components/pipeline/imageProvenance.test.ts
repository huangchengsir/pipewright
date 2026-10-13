import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import type { PipelineJob, PipelineStage } from '../../api/pipeline'
import { imageSourceChoices, projectImageName, registryRepository, resolveImageSource } from './imageProvenance'
import { MATRIX_MAX_AXES, MATRIX_MAX_CELLS } from './stageSettings'
import ImageProvenanceSummary from './ImageProvenanceSummary.vue'
import JobDrawer from './JobDrawer.vue'

function job(id: string, type = 'build_image', config: Record<string, string> = {}): PipelineJob {
  return { id, name: id, type, config, summary: '' }
}
function stage(id: string, jobs: PipelineJob[], needs: string[] = []): PipelineStage {
  return { id, name: id, kind: 'build', jobs, needs }
}
const build = stage('build', [job('image')])
const push = stage('push', [job('publish', 'push_image', { registry: 'http://registry.test/team/', imageName: 'service', tag: '${DATE:yyyyMMddHHmmss}-${COMMIT_SHA}' })], ['build'])
const deployJob = job('deploy', 'deploy_ssh', { artifactType: 'image' })
const deploy = stage('deploy', [deployJob], ['push'])

describe('configuration-time image provenance', () => {
  it('does not hide a build that may already be published by an environment', () => {
    const dep = { ...deploy, needs: ['build', 'push'] }
    expect(resolveImageSource([build, push, dep], dep, deployJob, true).status).toBe('ambiguous')
    expect(resolveImageSource([build, push, dep], dep, deployJob, false).source?.job.id).toBe('publish')
    const explicit = { ...deployJob, config: { artifactSource: JSON.stringify({ stageId: 'push', jobId: 'publish', declarationIndex: 0 }) } }
    expect(resolveImageSource([build, push, dep], dep, explicit, true).source?.job.id).toBe('publish')
  })
  it('deduplicates converging push chains by their actual build origin', () => {
    const second = stage('second', [job('publish2', 'push_image')], ['build'])
    const third = stage('third', [job('publish3', 'push_image')], ['push', 'second'])
    const dep = { ...deploy, needs: ['build', 'third'] }
    expect(resolveImageSource([build, push, second, third, dep], dep, deployJob).source?.job.id).toBe('publish3')
    expect(resolveImageSource([build, push, second, third, dep], dep, deployJob, true).status).toBe('ambiguous')
  })
  it('passes environment auto-push uncertainty through the drawer', async () => {
    const dep = { ...deploy, needs: ['build', 'push'] }
    const wrapper = mount(JobDrawer, { props: { job: deployJob, stage: dep, stages: [build, push, dep], environmentAutoPushPossible: true } })
    try {
      expect(wrapper.findComponent(ImageProvenanceSummary).exists()).toBe(false)
      await wrapper.setProps({ environmentAutoPushPossible: false })
      expect(wrapper.findComponent(ImageProvenanceSummary).exists()).toBe(true)
    } finally { wrapper.unmount() }
  })
  it('resolves the dependency push node, not an unrelated or old image', () => {
    const result = resolveImageSource([build, stage('unrelated', [job('other')]), push, deploy], deploy, deployJob)
    expect(result.source?.job.id).toBe('publish')
    expect(JSON.parse(result.source!.selector)).toEqual({ stageId: 'push', jobId: 'publish', declarationIndex: 0 })
    expect(result.source?.job.config.tag).toBe('${DATE:yyyyMMddHHmmss}-${COMMIT_SHA}')
  })
  it('reports ambiguous dependency outputs instead of picking the first', () => {
    const two = stage('two', [job('one'), job('two')])
    const dep = { ...deploy, needs: ['two'] }
    expect(resolveImageSource([two, dep], dep, deployJob).status).toBe('ambiguous')
  })
  it('ignores non-image build outputs', () => {
    const jar = stage('jar', [job('jar', 'build_image', { artifactType: 'jar' })])
    const dep = { ...deploy, needs: ['jar'] }
    expect(resolveImageSource([jar, dep], dep, deployJob).status).toBe('missing')
  })
  it('permits explicit ancestor selection but excludes parallel and downstream stages', () => {
    const unrelated = stage('other', [job('parallel')])
    const downstream = stage('later', [job('future')], ['deploy'])
    const choices = imageSourceChoices([build, push, unrelated, deploy, downstream], deploy, deployJob)
    expect(choices.map((source) => source.job.id)).toEqual(['image', 'publish'])
    const explicit = { ...deployJob, config: { artifactType: 'image', artifactSource: choices[0].selector } }
    expect(resolveImageSource([build, push, deploy], deploy, explicit).source?.job.id).toBe('image')
  })
  it.each(['{}', 'null', '[]', 'bad-json', '{"stageId":"push","jobId":"gone","declarationIndex":0}', '{"stageId":"push","jobId":"publish","declarationIndex":1}', '{"stageId":"push","jobId":"publish","declarationIndex":0,"extra":true}'])('preserves and flags invalid source %s', (artifactSource) => {
    expect(resolveImageSource([build, push, deploy], deploy, { ...deployJob, config: { artifactSource } }).status).toBe('unavailable')
  })
  it('accepts equivalent selector JSON independent of key order', () => {
    const artifactSource = '{ "jobId": "publish", "declarationIndex": 0, "stageId": "push" }'
    expect(resolveImageSource([push, deploy], deploy, { ...deployJob, config: { artifactSource } }).status).toBe('selected')
  })
  it('hides a same-stage build output consumed by push', () => {
    const image = job('image')
    const publish = { ...job('publish', 'push_image'), needs: ['image'] }
    const target = { ...deployJob, needs: ['publish'] }
    const all = stage('all', [image, publish, target])
    expect(imageSourceChoices([all], all, target).map((source) => source.job.id)).toEqual(['publish'])
    expect(resolveImageSource([all], all, target).source?.job.id).toBe('publish')
  })
  it('does not pick a same-stage unrelated job when job dependencies exist', () => {
    const target = { ...deployJob, needs: ['empty'] }
    const all = stage('all', [job('image'), job('empty', 'script'), target])
    expect(resolveImageSource([all], all, target).status).toBe('missing')
  })
  it('excludes parallel same-stage jobs even when no job declares needs', () => {
    const all = stage('all', [job('image'), deployJob])
    expect(imageSourceChoices([all], all, deployJob)).toEqual([])
    expect(resolveImageSource([all], all, deployJob).status).toBe('missing')
    const explicit = { ...deployJob, config: {
      artifactType: 'image', artifactSource: JSON.stringify({ stageId: 'all', jobId: 'image', declarationIndex: 0 }),
    } }
    expect(resolveImageSource([all], all, explicit).status).toBe('unavailable')
  })
  it.each(['', ' '])('permits no-needs images only in the legacy grouped path with ID %s', (id) => {
    const all = stage('all', [job('image'), job(id, 'script'), deployJob])
    expect(resolveImageSource([all], all, deployJob).source?.job.id).toBe('image')
  })
  it('uses DAG scheduling if any job has needs even when other job IDs are missing', () => {
    const afterBuild = { ...job('after', 'script'), needs: ['image'] }
    const all = stage('all', [job('image'), afterBuild, job('', 'script'), deployJob])
    expect(resolveImageSource([all], all, deployJob).status).toBe('missing')
  })
  it('permits transitive same-stage dependencies in a job DAG', () => {
    const afterBuild = { ...job('after', 'script'), needs: ['image'] }
    const target = { ...deployJob, needs: ['after'] }
    const all = stage('all', [job('image'), afterBuild, target])
    expect(resolveImageSource([all], all, target).source?.job.id).toBe('image')
  })
  it('shadows the local build if both push and its source stage are dependencies', () => {
    const dep = { ...deploy, needs: ['build', 'push'] }
    expect(resolveImageSource([build, push, dep], dep, deployJob).source?.job.id).toBe('publish')
  })
  it('does not shadow an upstream build when push consumes its own same-stage image', () => {
    const local = job('local')
    const publish = { ...job('publish-local', 'push_image'), needs: ['local'] }
    const ownPush = stage('own-push', [local, publish], ['build'])
    const dep = { ...deploy, needs: ['build', 'own-push'] }
    expect(resolveImageSource([build, ownPush, dep], dep, deployJob).status).toBe('ambiguous')
    expect(imageSourceChoices([build, ownPush, dep], dep, deployJob).map((source) => source.job.id)).toEqual(['image', 'publish-local'])
  })
  it('does not infer a unique push input from only the deployment dependency scope', () => {
    const other = stage('other', [job('other-image')])
    const uncertainPush = { ...push, needs: ['build', 'other'] }
    const dep = { ...deploy, needs: ['build', 'push'] }
    expect(resolveImageSource([build, other, uncertainPush, dep], dep, deployJob).status).toBe('ambiguous')
  })
  it('does not fall back to an upstream build when push has an unusable same-stage build', () => {
    const fileBuild = job('file', 'build_image', { artifactType: 'jar' })
    const unusablePush = { ...push, jobs: [fileBuild, job('publish', 'push_image')] }
    const dep = { ...deploy, needs: ['build', 'push'] }
    expect(resolveImageSource([build, unusablePush, dep], dep, deployJob).status).toBe('ambiguous')
  })
  it('traces a unique upstream push chain back to its actual build', () => {
    const secondPush = stage('second-push', [job('republish', 'push_image')], ['push'])
    const dep = { ...deploy, needs: ['build', 'second-push'] }
    expect(resolveImageSource([build, push, secondPush, dep], dep, deployJob).source?.job.id).toBe('republish')
  })
  it('terminates without guessing push provenance through cycles', () => {
    const cyclicPush = { ...push, needs: ['second-push'] }
    const secondPush = stage('second-push', [job('republish', 'push_image')], ['push'])
    const dep = { ...deploy, needs: ['build', 'push'] }
    expect(resolveImageSource([build, cyclicPush, secondPush, dep], dep, deployJob).status).toBe('ambiguous')
  })
  it('offers explicit upstream choices in legacy linear graphs without claiming backend auto resolution', () => {
    const legacy = [build, { ...push, needs: [] }, { ...deploy, needs: [] }]
    expect(imageSourceChoices(legacy, legacy[2], deployJob)).toHaveLength(2)
    expect(resolveImageSource(legacy, legacy[2], deployJob).status).toBe('missing')
  })
  it('terminates with malformed cycles', () => {
    const cyclic = { ...push, needs: ['deploy', 'build'] }
    expect(imageSourceChoices([build, cyclic, deploy], deploy, deployJob).length).toBeGreaterThan(0)
  })
  it('projects each matrix image into real cell stage and job selectors', () => {
    const matrix = { ...build, matrix: { arch: ['amd64', 'arm64'] } }
    const dep = { ...deploy, needs: ['build'] }
    const choices = imageSourceChoices([matrix, dep], dep, deployJob)
    expect(choices.map((source) => JSON.parse(source.selector))).toEqual([
      { stageId: 'build__arch-amd64', jobId: 'image__arch-amd64', declarationIndex: 0 },
      { stageId: 'build__arch-arm64', jobId: 'image__arch-arm64', declarationIndex: 0 },
    ])
    expect(choices.map((source) => source.inspectJobId)).toEqual(['image', 'image'])
    expect(resolveImageSource([matrix, dep], dep, deployJob).status).toBe('ambiguous')
    const explicit = { ...deployJob, config: { artifactType: 'image', artifactSource: choices[1].selector } }
    expect(resolveImageSource([matrix, dep], dep, explicit).source?.job.id).toBe('image__arch-arm64')
    expect(resolveImageSource([matrix, dep], dep, { ...deployJob, config: {
      artifactSource: JSON.stringify({ stageId: 'build', jobId: 'image', declarationIndex: 0 }),
    } }).status).toBe('unavailable')
    expect(matrix.jobs[0].id).toBe('image')
    expect(dep.needs).toEqual(['build'])
  })
  it('matches backend axis ordering, normalization and rune-based cell suffixes', () => {
    const matrix = { ...build, matrix: { ' z ': [' a/b ', 'a/b'], A: ['\u{1f600}'] } }
    const dep = { ...deploy, needs: ['build'] }
    const result = resolveImageSource([matrix, dep], dep, deployJob)
    expect(JSON.parse(result.source!.selector)).toEqual({ stageId: 'build__A--_z-a-b', jobId: 'image__A--_z-a-b', declarationIndex: 0 })
    expect(result.source?.stage.name).toBe('build [A=\u{1f600}, z=a/b]')
  })
  it('projects valid prototype-like matrix axes without losing their cell identity', () => {
    const matrix = { ...build, matrix: Object.fromEntries([['__proto__', ['v1']], ['constructor', ['v2']]]) }
    const dep = { ...deploy, needs: ['build'] }
    expect(resolveImageSource([matrix, dep], dep, deployJob).source?.stage.id).toBe('build____proto__-v1_constructor-v2')
  })
  it('keeps declaration job IDs distinct from IDs that resemble another projected cell job', () => {
    const target = { ...deployJob, id: 'image__arch-amd64', needs: ['image'] }
    const matrix = { ...stage('all', [job('image'), target]), matrix: { arch: ['amd64'] } }
    // The executor preserves needs verbatim; it does not turn this into a valid cell edge.
    expect(resolveImageSource([matrix], matrix, target).status).toBe('missing')
  })
  it('does not fabricate stable matrix selectors for declarations without source IDs', () => {
    const matrix = { ...build, jobs: [job('')], matrix: { arch: ['amd64'] } }
    const dep = { ...deploy, needs: ['build'] }
    expect(resolveImageSource([matrix, dep], dep, deployJob).status).toBe('missing')
  })
  it('projects at most the existing matrix cell limit', () => {
    const matrix = { ...build, matrix: { arch: Array.from({ length: MATRIX_MAX_CELLS }, (_, i) => `v${i}`) } }
    const dep = { ...deploy, needs: ['build'] }
    expect(imageSourceChoices([matrix, dep], dep, deployJob)).toHaveLength(MATRIX_MAX_CELLS)
    const oversized = { ...matrix, matrix: { arch: [...matrix.matrix.arch, 'over-limit'] } }
    expect(imageSourceChoices([oversized, dep], dep, deployJob)).toEqual([])
    expect(resolveImageSource([oversized, dep], dep, deployJob).status).toBe('missing')
  })
  it.each([
    { arch: [] },
    { 'invalid-axis': ['x'] },
    { arch: ['a/b', 'a?b'] },
    Object.fromEntries(Array.from({ length: MATRIX_MAX_AXES + 1 }, (_, i) => [`axis${i}`, ['x']])),
  ])('does not claim a source for an invalid matrix %j', (matrix) => {
    const dep = { ...deploy, needs: ['build'] }
    expect(resolveImageSource([{ ...build, matrix }, dep], dep, deployJob).status).toBe('missing')
  })
  it('does not guess a deploy cell even when an explicit upstream source is configured', () => {
    const matrixDeploy = { ...deploy, needs: ['build'], matrix: { arch: ['amd64', 'arm64'] } }
    const explicit = { ...deployJob, config: {
      artifactSource: JSON.stringify({ stageId: 'build', jobId: 'image', declarationIndex: 0 }),
    } }
    expect(imageSourceChoices([build, matrixDeploy], matrixDeploy, deployJob)).toEqual([])
    expect(resolveImageSource([build, matrixDeploy], matrixDeploy, deployJob).status).toBe('ambiguous')
    expect(resolveImageSource([build, matrixDeploy], matrixDeploy, explicit).status).toBe('ambiguous')
    const cellContext = { ...matrixDeploy, id: 'deploy__arch-arm64', matrix: undefined }
    expect(resolveImageSource([build, matrixDeploy], cellContext, deployJob).source?.job.id).toBe('image')
  })
  it('resolves a deploy matrix with exactly one configured cell', () => {
    const matrixDeploy = { ...deploy, needs: ['build'], matrix: { arch: ['amd64'] } }
    expect(resolveImageSource([build, matrixDeploy], matrixDeploy, deployJob).source?.job.id).toBe('image')
  })
  it('does not invent rewritten job dependencies in a matrix cell', () => {
    const target = { ...deployJob, needs: ['image'] }
    const matrix = { ...stage('all', [job('image'), target]), matrix: { arch: ['amd64'] } }
    expect(resolveImageSource([matrix], matrix, target).status).toBe('missing')
  })
  it('navigates an explicitly selected matrix source to the original canvas job', async () => {
    const matrix = { ...build, matrix: { arch: ['amd64', 'arm64'] } }
    const dep = { ...deploy, needs: ['build'] }
    const choices = imageSourceChoices([matrix, dep], dep, deployJob)
    const explicit = { ...deployJob, config: { artifactType: 'image', artifactSource: choices[1].selector } }
    const wrapper = mount(JobDrawer, { props: { job: explicit, stage: { ...dep, jobs: [explicit] }, stages: [matrix, dep] } })
    try {
      const summary = wrapper.findComponent(ImageProvenanceSummary)
      expect(summary.text()).toContain('arch=arm64')
      await summary.find('button').trigger('click')
      expect(wrapper.emitted('inspect-source')?.[0]).toEqual(['image'])
      expect((wrapper.emitted('update')?.[0]?.[0] as PipelineJob).config.artifactSource).toBe(choices[1].selector)
    } finally { wrapper.unmount() }
  })
  it('normalizes the project default name like the builder', () => {
    expect(projectImageName(' Gitea OAuth 完整链路验收 ')).toBe('gitea-oauth')
    expect(projectImageName('中文')).toBe('app')
  })
  it.each(['http://user:secret@registry.test/team', 'ftp://registry.test/team', 'registry.test/team?secret=x', 'registry.test//team', 'registry.test/Bad Name'])('does not display a resolved registry for unsafe input %s', (raw) => {
    expect(registryRepository(raw)).toBeNull()
  })
  it('displays the registry authority and namespace without the scheme', () => {
    expect(registryRepository('http://127.0.0.1:18093/acceptance-194/')).toBe('127.0.0.1:18093/acceptance-194')
  })
  it.each([
    ['http://REGISTRY.example:80/team/', 'registry.example:80/team'],
    ['https://registry.example:443/team', 'registry.example:443/team'],
    ['registry.example:443/team', 'registry.example:443/team'],
    ['https://registry:443/team', 'registry:443/team'],
    ['http://registry.example:00080/team', 'registry.example:00080/team'],
    ['https://[2001:db8::1]:443/team', '[2001:db8::1]:443/team'],
    ['http://127.1:80/team', '127.1:80/team'],
  ])('preserves the raw registry authority of %s', (raw, expected) => {
    expect(registryRepository(raw)).toBe(expected)
  })
  it.each([
    'registry.example/team/../other', 'registry.example/team/./other',
    'registry.example/./team', 'registry.example/../team',
    'registry.example/team/.', 'registry.example/team/..',
    'registry.example/team/%2e%2e/other', 'http://registry.example:/team',
    'registry.example:0/team', 'registry.example:65536/team',
    'registry.example:abc/team', 'registry.example:1.5/team',
    `registry.example/${'a'.repeat(256)}`, 'registry.example/team\x00',
  ])('rejects raw invalid registry paths or authorities in %s', (raw) => {
    expect(registryRepository(raw)).toBeNull()
  })
})
