import { describe, expect, it } from 'vitest'
import { compile } from '@intlify/core-base'
import english from './locales/en/releaseGuard'
import englishFacts from './locales/en/releaseGuardFacts'
import englishAnalysis from './locales/en/releaseGuardAnalysis'
import englishInvestigation from './locales/en/releaseGuardInvestigation'
import englishPresentation from './locales/en/releaseGuardPresentation'

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
const presentationModules = import.meta.glob<{
  default: Record<string, unknown>
}>('./locales/*/releaseGuardPresentation.ts', { eager: true })
function leaves(value: unknown, prefix = ''): Array<[string, string]> {
  return typeof value === 'string'
    ? [[prefix, value]]
    : Object.entries(value as Record<string, unknown>).flatMap(([key, item]) =>
        leaves(item, prefix ? `${prefix}.${key}` : key),
      )
}
describe('release guard locales', () => {
  it('describes source and deployment provenance without mislabeling it as a missing container', () => {
    for (const key of [
      'resolvedSourceUnavailable',
      'sourceRunUnavailable',
      'sourceRevisionConflict',
      'sourceRevisionNotConfirmed',
      'currentDeploymentIdentityUnavailable',
      'deploymentHistoryUnavailable',
    ] as const) {
      const text = englishPresentation.reasons[key]
      expect(text).toMatch(/source|commit|deployment/i)
      expect(text).not.toMatch(/container/i)
      for (const [locale, messages] of Object.entries(presentationModules)) {
        const reasons = messages.default.reasons as Record<string, unknown>
        expect(typeof reasons[key], `${locale}:${key}`).toBe('string')
        expect(String(reasons[key]).length, `${locale}:${key}`).toBeGreaterThan(
          0,
        )
      }
    }
  })
  it('provides matching, compilable local messages in all eight supported languages', () => {
    for (const [group, reference] of [
      [modules, english],
      [factModules, englishFacts],
      [analysisModules, englishAnalysis],
      [investigationModules, englishInvestigation],
      [presentationModules, englishPresentation],
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
