import { describe, it, expect } from 'vitest'
import {
  type PromotedParam,
  type StudioModel,
  type StudioStep,
  type StudioStepKind,
  STUDIO_META_KEY,
  STEP_CATALOG,
  catalogItem,
  makeStep,
  stepDefaults,
  paramsToText,
  parseParamsText,
  encodeStudioMeta,
  compileSteps,
  parsePromotedParams,
  parseStudioConfig,
  compileStudioConfig,
  isStudioNode,
  emptyStudioModel,
  promotedValues,
  applyPromotedValues,
} from './studioCompile'

function param(p: Partial<PromotedParam> & { key: string }): PromotedParam {
  return { label: p.key, type: 'text', default: '', ...p }
}

let id = 0
function step(kind: StudioStepKind, fields: Record<string, string>): StudioStep {
  return { id: ++id, kind, fields }
}

describe('studioCompile', () => {
  describe('paramsToText / parseParamsText', () => {
    it('compiles key=default lines, skips empty keys', () => {
      const params = [param({ key: 'dir', default: 'web' }), param({ key: '', default: 'x' }), param({ key: 'env', default: 'prod' })]
      expect(paramsToText(params)).toBe('dir=web\nenv=prod')
    })

    it('parses lines back, skips blanks and # comments', () => {
      const got = parseParamsText('dir=web\n\n# comment\nenv=prod')
      expect(got).toEqual([
        { key: 'dir', label: 'dir', type: 'text', default: 'web' },
        { key: 'env', label: 'env', type: 'text', default: 'prod' },
      ])
    })

    it('default may contain = (only first = splits)', () => {
      expect(parseParamsText('flags=-a=1 -b=2')).toEqual([
        { key: 'flags', label: 'flags', type: 'text', default: '-a=1 -b=2' },
      ])
    })
  })

  describe('catalog / step factory', () => {
    it('catalogItem resolves known kinds and falls back for unknown', () => {
      expect(catalogItem('command').label).toBe('运行命令')
      expect(catalogItem('xxx' as StudioStepKind)).toEqual({ kind: 'xxx', label: 'xxx', color: '#868e96' })
    })
    it('makeStep seeds defaults with a fresh id', () => {
      const a = makeStep('test')
      const b = makeStep('test')
      expect(a.kind).toBe('test')
      expect(a.fields).toEqual(stepDefaults('test'))
      expect(a.id).not.toBe(b.id)
    })
    it('catalog covers every kind exactly once', () => {
      const kinds = STEP_CATALOG.flatMap((g) => g.items.map((i) => i.kind))
      expect(new Set(kinds).size).toBe(kinds.length)
      expect(kinds).toContain('healthcheck')
    })
  })

  describe('compileSteps', () => {
    it('compiles a frontend-build pipeline into commandTemplate + artifacts + gate keys', () => {
      const steps = [
        step('env', { envKey: 'NODE_ENV', envValue: '{{env}}' }),
        step('workDir', { dir: '{{dir}}' }),
        step('install', { command: 'npm ci' }),
        step('command', { command: 'npm run build' }),
        step('test', { command: 'npm test', reportPath: '{{dir}}/junit.xml', minCov: '80' }),
        step('artifact', { artifact: '{{dir}}/dist' }),
      ]
      const c = compileSteps(steps)
      expect(c.commandTemplate).toBe('export NODE_ENV={{env}}\ncd {{dir}}\nnpm ci\nnpm run build\nnpm test')
      expect(c.artifactPath).toBe('{{dir}}/dist')
      expect(c.extraKeys).toEqual({ testReport: 'junit', reportPath: '{{dir}}/junit.xml', gateMinCoverage: '80' })
    })

    it('wraps control-flow kinds (condition / retry / timeout / sleep / healthcheck)', () => {
      const c = compileSteps([
        step('condition', { condition: 'test -f package.json' }),
        step('retry', { command: 'flaky', count: '5', delay: '3' }),
        step('timeout', { command: 'slow', secs: '30' }),
        step('sleep', { secs: '5' }),
        step('healthcheck', { url: 'http://x/health', delay: '2' }),
      ])
      expect(c.commandTemplate.split('\n')).toEqual([
        'if ! ( test -f package.json ); then echo "条件不满足,跳过"; exit 0; fi',
        'for i in $(seq 5); do flaky && break || sleep 3; done',
        'timeout 30 slow',
        'sleep 5',
        'until curl -fsS http://x/health >/dev/null; do sleep 2; done',
      ])
    })

    it('quotes echo text with special chars and skips empty fields', () => {
      const c = compileSteps([step('echo', { command: 'hello world' }), step('command', { command: '' })])
      expect(c.commandTemplate).toBe("echo 'hello world'")
    })
  })

  describe('compileStudioConfig → templated config', () => {
    it('maps steps → commandTemplate/artifactPath, params → text, stores __studio v2', () => {
      const model: StudioModel = {
        image: 'node:{{node}}',
        params: [
          param({ key: 'node', label: '构建镜像', type: 'select', default: '20', options: ['20', '18'] }),
          param({ key: 'dir', label: '目录', default: 'web' }),
        ],
        steps: [step('workDir', { dir: '{{dir}}' }), step('install', { command: 'npm ci' }), step('artifact', { artifact: '{{dir}}/dist' })],
        meta: { icon: '🔧', category: '构建与制品', summary: '构建 {{dir}}' },
      }
      const cfg = compileStudioConfig(model)
      expect(cfg.image).toBe('node:{{node}}')
      expect(cfg.commandTemplate).toBe('cd {{dir}}\nnpm ci')
      expect(cfg.artifactPath).toBe('{{dir}}/dist')
      expect(cfg.params).toBe('node=20\ndir=web')
      const meta = JSON.parse(cfg[STUDIO_META_KEY])
      expect(meta.v).toBe(2)
      expect(meta.params).toEqual([
        { key: 'node', label: '构建镜像', type: 'select', options: ['20', '18'] },
        { key: 'dir', label: '目录', type: 'text' },
      ])
      expect(meta.steps).toEqual([
        { kind: 'workDir', fields: { dir: '{{dir}}' } },
        { kind: 'install', fields: { command: 'npm ci' } },
        { kind: 'artifact', fields: { artifact: '{{dir}}/dist' } },
      ])
      expect(meta.meta).toEqual({ icon: '🔧', category: '构建与制品', summary: '构建 {{dir}}' })
    })

    it('spreads gate keys into config and omits empty fields', () => {
      const cfg = compileStudioConfig({
        image: '  ',
        params: [],
        steps: [step('test', { command: 'go test ./...', reportPath: 'r.xml', minCov: '70' })],
        meta: { icon: '🔧', category: '自定义', summary: '' },
      })
      expect(cfg.commandTemplate).toBe('go test ./...')
      expect(cfg.image).toBeUndefined()
      expect(cfg.testReport).toBe('junit')
      expect(cfg.reportPath).toBe('r.xml')
      expect(cfg.gateMinCoverage).toBe('70')
      // steps present → __studio written even with no params / default meta.
      expect(cfg[STUDIO_META_KEY]).toBeDefined()
    })

    it('writes no __studio for an entirely blank model', () => {
      const cfg = compileStudioConfig(emptyStudioModel())
      expect(cfg[STUDIO_META_KEY]).toBeUndefined()
      expect(cfg).toEqual({})
    })
  })

  describe('round-trip compile → parse', () => {
    it('preserves image, params (type/label/options), structured steps, and meta', () => {
      const model: StudioModel = {
        image: 'golang:{{ver}}',
        params: [
          param({ key: 'ver', label: 'Go 版本', type: 'select', default: '1.22', options: ['1.22', '1.21'] }),
          param({ key: 'flags', label: '编译参数', default: '-trimpath' }),
        ],
        steps: [step('command', { command: 'go build {{flags}} ./...' }), step('artifact', { artifact: 'bin/app' })],
        meta: { icon: '🐹', category: '后端构建', summary: '用 Go {{ver}} 编译' },
      }
      const back = parseStudioConfig(compileStudioConfig(model))
      expect(back.image).toBe('golang:{{ver}}')
      expect(back.params).toEqual(model.params)
      expect(back.meta).toEqual(model.meta)
      expect(back.steps.map((s) => ({ kind: s.kind, fields: s.fields }))).toEqual([
        { kind: 'command', fields: { command: 'go build {{flags}} ./...' } },
        { kind: 'artifact', fields: { artifact: 'bin/app' } },
      ])
    })
  })

  describe('parsePromotedParams fallback (no/broken __studio)', () => {
    it('falls back to params text with type=text when __studio missing', () => {
      expect(parsePromotedParams({ params: 'dir=web\nenv=prod' })).toEqual([
        { key: 'dir', label: 'dir', type: 'text', default: 'web' },
        { key: 'env', label: 'env', type: 'text', default: 'prod' },
      ])
    })

    it('ignores corrupt __studio JSON, still recovers params from text', () => {
      const got = parsePromotedParams({ params: 'dir=web', [STUDIO_META_KEY]: '{not json' })
      expect(got).toEqual([{ key: 'dir', label: 'dir', type: 'text', default: 'web' }])
    })

    it('meta only enriches keys present in params text (no phantom params)', () => {
      const meta = encodeStudioMeta([param({ key: 'gone', label: 'X', type: 'number', default: '1' })])
      expect(parsePromotedParams({ params: 'dir=web', [STUDIO_META_KEY]: meta })).toEqual([
        { key: 'dir', label: 'dir', type: 'text', default: 'web' },
      ])
    })
  })

  describe('parseStudioConfig compat (no structured steps in __studio)', () => {
    it('reverse-builds a single command step from legacy commandTemplate + artifact step', () => {
      const m = parseStudioConfig({ commandTemplate: 'make build', artifactPath: 'out/bin' })
      expect(m.steps.map((s) => ({ kind: s.kind, fields: s.fields }))).toEqual([
        { kind: 'command', fields: { command: 'make build' } },
        { kind: 'artifact', fields: { artifact: 'out/bin' } },
      ])
    })
    it('reads legacy commands when commandTemplate absent', () => {
      expect(parseStudioConfig({ commands: 'make build' }).steps[0].fields.command).toBe('make build')
    })
    it('defaults meta when __studio missing', () => {
      expect(parseStudioConfig({ commandTemplate: 'echo hi' }).meta).toEqual({ icon: '🔧', category: '自定义', summary: '' })
    })
  })

  describe('isStudioNode', () => {
    it('true for templated nodeType', () => {
      expect(isStudioNode('templated', {})).toBe(true)
    })
    it('true for any node carrying __studio', () => {
      expect(isStudioNode('script', { [STUDIO_META_KEY]: '{"v":2,"params":[]}' })).toBe(true)
    })
    it('false for plain non-studio nodes', () => {
      expect(isStudioNode('build_image', { dockerfile: 'Dockerfile' })).toBe(false)
    })
  })

  it('emptyStudioModel is a blank studio with default meta', () => {
    expect(emptyStudioModel()).toEqual({ image: '', params: [], steps: [], meta: { icon: '🔧', category: '自定义', summary: '' } })
  })

  describe('instance shortlist values (promotedValues / applyPromotedValues)', () => {
    it('reads current values keyed by param key from params text', () => {
      const meta = encodeStudioMeta([
        param({ key: 'node', label: '镜像', type: 'select', options: ['20', '18'] }),
        param({ key: 'flag', label: '开关', type: 'toggle' }),
      ])
      const config = { params: 'node=18\nflag=true', [STUDIO_META_KEY]: meta }
      expect(promotedValues(config)).toEqual({ node: '18', flag: 'true' })
    })

    it('reads values even without __studio (type=text fallback)', () => {
      expect(promotedValues({ params: 'dir=web\nenv=prod' })).toEqual({ dir: 'web', env: 'prod' })
    })

    it('writes edited values back into params text, preserving key order', () => {
      const params = [
        param({ key: 'node', label: '镜像', type: 'select', default: '20', options: ['20', '18'] }),
        param({ key: 'dir', label: '目录', default: 'web' }),
      ]
      const next = applyPromotedValues(params, { node: '18', dir: 'frontend' })
      expect(next).toBe('node=18\ndir=frontend')
    })

    it('missing key in values keeps its existing default', () => {
      const params = [param({ key: 'a', default: '1' }), param({ key: 'b', default: '2' })]
      expect(applyPromotedValues(params, { a: '9' })).toBe('a=9\nb=2')
    })

    it('round-trips: read → edit → write → re-read across all types', () => {
      const meta = encodeStudioMeta([
        param({ key: 'ver', label: 'Go', type: 'select', options: ['1.22', '1.21'] }),
        param({ key: 'count', label: '并发', type: 'number' }),
        param({ key: 'verbose', label: '详细', type: 'toggle' }),
        param({ key: 'note', label: '备注', type: 'text' }),
      ])
      const config = {
        params: 'ver=1.22\ncount=4\nverbose=false\nnote=hi',
        [STUDIO_META_KEY]: meta,
      }
      const defs = parsePromotedParams(config)
      const edited = { ver: '1.21', count: '8', verbose: 'true', note: 'bye' }
      const newParams = applyPromotedValues(defs, edited)
      const back = promotedValues({ params: newParams, [STUDIO_META_KEY]: meta })
      expect(back).toEqual(edited)
      expect(parsePromotedParams({ params: newParams, [STUDIO_META_KEY]: meta }).map((p) => ({
        key: p.key,
        label: p.label,
        type: p.type,
        options: p.options,
      }))).toEqual([
        { key: 'ver', label: 'Go', type: 'select', options: ['1.22', '1.21'] },
        { key: 'count', label: '并发', type: 'number', options: undefined },
        { key: 'verbose', label: '详细', type: 'toggle', options: undefined },
        { key: 'note', label: '备注', type: 'text', options: undefined },
      ])
    })
  })
})
