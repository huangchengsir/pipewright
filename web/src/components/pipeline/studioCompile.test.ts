import { describe, it, expect } from 'vitest'
import {
  type PromotedParam,
  type StudioModel,
  STUDIO_META_KEY,
  paramsToText,
  parseParamsText,
  encodeStudioMeta,
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

  describe('compileStudioConfig → templated config', () => {
    it('maps steps.commands → commandTemplate, params → params text, stores __studio', () => {
      const model: StudioModel = {
        image: 'node:{{node}}',
        params: [
          param({ key: 'node', label: '构建镜像', type: 'select', default: '20', options: ['20', '18'] }),
          param({ key: 'dir', label: '目录', default: 'web' }),
        ],
        stepConfig: { commands: 'cd {{dir}}\nnpm ci', artifactPath: '{{dir}}/dist' },
      }
      const cfg = compileStudioConfig(model)
      expect(cfg.image).toBe('node:{{node}}')
      expect(cfg.commandTemplate).toBe('cd {{dir}}\nnpm ci')
      expect(cfg.artifactPath).toBe('{{dir}}/dist')
      expect(cfg.params).toBe('node=20\ndir=web')
      // __studio carries label/type/options (not defaults).
      const meta = JSON.parse(cfg[STUDIO_META_KEY])
      expect(meta.v).toBe(1)
      expect(meta.params).toEqual([
        { key: 'node', label: '构建镜像', type: 'select', options: ['20', '18'] },
        { key: 'dir', label: '目录', type: 'text' },
      ])
    })

    it('omits empty fields and __studio when no params', () => {
      const cfg = compileStudioConfig({ image: '  ', params: [], stepConfig: { commands: 'echo hi', artifactPath: '' } })
      expect(cfg).toEqual({ commandTemplate: 'echo hi' })
      expect(cfg[STUDIO_META_KEY]).toBeUndefined()
    })
  })

  describe('round-trip compile → parse', () => {
    it('preserves image, params (incl. type/label/options), and steps', () => {
      const model: StudioModel = {
        image: 'golang:{{ver}}',
        params: [
          param({ key: 'ver', label: 'Go 版本', type: 'select', default: '1.22', options: ['1.22', '1.21'] }),
          param({ key: 'flags', label: '编译参数', default: '-trimpath' }),
        ],
        stepConfig: { commands: 'go build {{flags}} ./...', artifactPath: 'bin/app' },
      }
      const back = parseStudioConfig(compileStudioConfig(model))
      expect(back.image).toBe('golang:{{ver}}')
      expect(back.stepConfig).toEqual({ commands: 'go build {{flags}} ./...', artifactPath: 'bin/app' })
      expect(back.params).toEqual(model.params)
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
      // params text has only "dir"; meta references "gone" → ignored.
      expect(parsePromotedParams({ params: 'dir=web', [STUDIO_META_KEY]: meta })).toEqual([
        { key: 'dir', label: 'dir', type: 'text', default: 'web' },
      ])
    })
  })

  describe('parseStudioConfig compat', () => {
    it('reads legacy commands when commandTemplate absent', () => {
      expect(parseStudioConfig({ commands: 'make build' }).stepConfig.commands).toBe('make build')
    })
    it('prefers commandTemplate over commands', () => {
      expect(parseStudioConfig({ commandTemplate: 'a', commands: 'b' }).stepConfig.commands).toBe('a')
    })
  })

  describe('isStudioNode', () => {
    it('true for templated nodeType', () => {
      expect(isStudioNode('templated', {})).toBe(true)
    })
    it('true for any node carrying __studio', () => {
      expect(isStudioNode('script', { [STUDIO_META_KEY]: '{"v":1,"params":[]}' })).toBe(true)
    })
    it('false for plain non-studio nodes', () => {
      expect(isStudioNode('build_image', { dockerfile: 'Dockerfile' })).toBe(false)
    })
  })

  it('emptyStudioModel is a blank studio', () => {
    expect(emptyStudioModel()).toEqual({ image: '', params: [], stepConfig: { commands: '', artifactPath: '' } })
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
      // re-read from the new params text + same __studio meta
      const back = promotedValues({ params: newParams, [STUDIO_META_KEY]: meta })
      expect(back).toEqual(edited)
      // __studio definitions untouched by the value edit
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
