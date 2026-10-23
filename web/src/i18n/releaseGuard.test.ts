import { describe, expect, it } from 'vitest'
import { compile } from '@intlify/core-base'
import english from './locales/en/releaseGuard'
import englishFacts from './locales/en/releaseGuardFacts'
import englishAnalysis from './locales/en/releaseGuardAnalysis'
import englishInvestigation from './locales/en/releaseGuardInvestigation'

const modules = import.meta.glob<{ default: Record<string, unknown> }>(
  './locales/*/releaseGuard.ts',
  { eager: true },
)
const factModules = import.meta.glob<{ default: Record<string, unknown> }>(
  './locales/*/releaseGuardFacts.ts',
  { eager: true },
)
const analysisModules = import.meta.glob<{ default: Record<string, unknown> }>(
  './locales/*/releaseGuardAnalysis.ts',
  { eager: true },
)
const investigationModules = import.meta.glob<{
  default: Record<string, unknown>
}>('./locales/*/releaseGuardInvestigation.ts', { eager: true })
function leaves(value: unknown, prefix = ''): Array<[string, string]> {
  return typeof value === 'string'
    ? [[prefix, value]]
    : Object.entries(value as Record<string, unknown>).flatMap(([key, item]) =>
        leaves(item, prefix ? `${prefix}.${key}` : key),
      )
}
describe('release guard locales', () => {
  it('provides matching, compilable local messages in all eight supported languages', () => {
    for (const [group, reference] of [
      [modules, english],
      [factModules, englishFacts],
      [analysisModules, englishAnalysis],
      [investigationModules, englishInvestigation],
    ] as const) {
      const keys = leaves(reference)
        .map(([key]) => key)
        .sort()
      expect(
        leaves(reference).some(([, text]) => /[\u3400-\u9fff]/.test(text)),
      ).toBe(false)
      expect(Object.keys(group)).toHaveLength(8)
      for (const [path, module] of Object.entries(group)) {
        expect(
          leaves(module.default)
            .map(([key]) => key)
            .sort(),
          path,
        ).toEqual(keys)
        for (const [key, value] of leaves(module.default))
          expect(
            () => compile(value, { key, locale: path }),
            `${path}:${key}`,
          ).not.toThrow()
      }
    }
    expect(
      leaves(english).some(([, text]) => /[\u3400-\u9fff]/.test(text)),
    ).toBe(false)
  })
})
