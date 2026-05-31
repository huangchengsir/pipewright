import { describe, it, expect } from 'vitest'
import {
  JOB_TYPE_SPECS,
  JOB_TYPE_OPTIONS,
  PICKABLE_TYPES,
  getJobTypeSpec,
  jobTypeLabel,
  jobTypeAccent,
  groupedJobTypes,
  schemaKeys,
  splitConfig,
} from './jobConfigSchema'

describe('jobConfigSchema', () => {
  it('exposes a typed spec for every dropdown option', () => {
    for (const opt of JOB_TYPE_OPTIONS) {
      const spec = getJobTypeSpec(opt.value)
      expect(spec, `spec for ${opt.value}`).not.toBeNull()
      expect(spec!.fields.length).toBeGreaterThan(0)
      expect(opt.label).toContain(spec!.label)
    }
  })

  it('returns null for an unknown type and falls back to the raw token label', () => {
    expect(getJobTypeSpec('totally_unknown')).toBeNull()
    expect(jobTypeLabel('totally_unknown')).toBe('totally_unknown')
    expect(jobTypeLabel('build_image')).toBe('构建')
  })

  it('every spec has a valid accent and category', () => {
    const accents = ['cyan', 'primary', 'green', 'amber', 'red', 'neutral']
    for (const spec of Object.values(JOB_TYPE_SPECS)) {
      expect(accents, `${spec.type} accent`).toContain(spec.accent)
      expect(typeof spec.category).toBe('string')
    }
  })

  describe('groupedJobTypes', () => {
    it('covers every pickable type exactly once across groups', () => {
      const grouped = groupedJobTypes()
      const flat = grouped.flatMap((g) => g.specs.map((s) => s.type))
      expect(flat.slice().sort()).toEqual([...PICKABLE_TYPES].sort())
      expect(new Set(flat).size).toBe(PICKABLE_TYPES.length)
    })

    it('omits empty groups and keeps display order', () => {
      const grouped = groupedJobTypes()
      for (const g of grouped) expect(g.specs.length).toBeGreaterThan(0)
      // source group comes before custom group
      const ids = grouped.map((g) => g.id)
      expect(ids.indexOf('source')).toBeLessThan(ids.indexOf('custom'))
    })

    it('never lists the custom alias as a pickable type', () => {
      expect(PICKABLE_TYPES).not.toContain('custom')
    })
  })

  it('jobTypeAccent falls back to neutral for unknown types', () => {
    expect(jobTypeAccent('build_image')).toBe('primary')
    expect(jobTypeAccent('totally_unknown')).toBe('neutral')
  })

  it('every field key is unique within a type', () => {
    for (const spec of Object.values(JOB_TYPE_SPECS)) {
      const keys = spec.fields.map((f) => f.key)
      expect(new Set(keys).size, `duplicate key in ${spec.type}`).toBe(keys.length)
    }
  })

  it('select/credential fields are well-formed', () => {
    for (const spec of Object.values(JOB_TYPE_SPECS)) {
      for (const f of spec.fields) {
        if (f.kind === 'select') {
          expect(f.options && f.options.length > 0, `${spec.type}.${f.key} needs options`).toBe(
            true,
          )
        }
        if (f.kind === 'credential') {
          expect(typeof f.credentialType === 'string' || f.credentialType === undefined).toBe(true)
        }
      }
    }
  })

  describe('build_image conditional fields', () => {
    const fields = JOB_TYPE_SPECS.build_image.fields

    function visible(config: Record<string, string>): string[] {
      return fields.filter((f) => !f.when || f.when(config)).map((f) => f.key)
    }

    it('shows Dockerfile fields by default (no model set)', () => {
      const keys = visible({})
      expect(keys).toContain('dockerfilePath')
      expect(keys).toContain('context')
      expect(keys).not.toContain('toolchainLanguage')
    })

    it('shows toolchain fields when model = toolchain', () => {
      const keys = visible({ buildModel: 'toolchain' })
      expect(keys).toContain('toolchainLanguage')
      expect(keys).toContain('buildCommand')
      expect(keys).not.toContain('dockerfilePath')
    })
  })

  describe('schemaKeys / splitConfig', () => {
    it('owns its declared keys', () => {
      const keys = schemaKeys('push_image')
      expect(keys.has('registry')).toBe(true)
      expect(keys.has('tag')).toBe(true)
      expect(keys.has('nonexistent')).toBe(false)
    })

    it('returns an empty key set for unknown types', () => {
      expect(schemaKeys('unknown').size).toBe(0)
    })

    it('splits owned keys out and keeps unknown keys as extras', () => {
      const config = { registry: 'r', tag: 'v1', myCustomFlag: 'x', another: 'y' }
      const { extras } = splitConfig('push_image', config)
      const extraKeys = extras.map(([k]) => k)
      expect(extraKeys).toEqual(['myCustomFlag', 'another'])
    })

    it('treats every key as an extra for an unknown type', () => {
      const config = { a: '1', b: '2' }
      const { extras } = splitConfig('unknown', config)
      expect(extras.map(([k]) => k)).toEqual(['a', 'b'])
    })
  })
})
