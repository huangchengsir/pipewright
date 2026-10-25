import { beforeEach, describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { i18n, setLocale } from '../i18n'
import type { GuardEvidence } from '../api/releaseGuard'
import GuardEvidenceCard from '../components/run/GuardEvidenceCard.vue'
import {
  guardDate,
  guardTimelineTitle,
  guardPresentationText,
  presentGuardEvidence,
} from './guardEvidencePresentation'

function evidence(
  value: Record<string, unknown>,
  overrides: Partial<GuardEvidence> = {},
): GuardEvidence {
  return {
    id: 'e-1',
    sequence: 1,
    targetId: 'target-1',
    kind: 'runtime',
    eventAt: '2026-10-25T11:40:04Z',
    collectedAt: '2026-10-25T11:40:19Z',
    reason: '',
    truncated: false,
    summary: 'typed_runtime_sample',
    value,
    ...overrides,
  }
}
const sample = {
  outcome: 'ok',
  running: false,
  oomKilled: false,
  exitCode: 0,
  restartCount: 0,
  health: '',
  configId: `sha256:${'a'.repeat(64)}`,
}
const present = (item: GuardEvidence) =>
  presentGuardEvidence(item, i18n.global.t, 'en')

describe('typed guard evidence presentation', () => {
  beforeEach(() => setLocale('en'))
  it('keeps observed false and zero and reads the actual configId field', () => {
    const result = present(evidence(sample))
    expect(result.title).toBe('Container not running at sampling time')
    expect(result.facts.map((item) => item.value)).toEqual([
      'Not running',
      '0',
      'No',
      '0',
      'Not configured; business health unknown',
      'Recorded; see technical details',
    ])
    expect(result.tone).toBe('danger')
  })
  it('makes a confirmed Docker health failure prominent even while the container is running', () => {
    const result = present(
      evidence({ ...sample, running: true, health: 'unhealthy' }),
    )
    expect(result.title).toBe('Docker health check failed at sampling time')
    expect(result.tone).toBe('danger')
    expect(
      result.facts.find((item) => item.label === 'Running state')?.value,
    ).toBe('Running')
    expect(
      result.facts.find((item) => item.label === 'Out-of-memory termination')
        ?.value,
    ).toBe('No')
    const unavailable = present(
      evidence({
        ...sample,
        running: true,
        outcome: 'unavailable',
        health: 'unhealthy',
      }),
    )
    expect(unavailable.tone).toBe('warning')
    expect(unavailable.facts.every((item) => item.value === 'Unknown')).toBe(
      true,
    )
  })
  it('retains verified container facts when only image metadata was truncated', () => {
    const item = evidence(
      { ...sample, reason: 'image_identity_truncated', truncated: true },
      { reason: 'image_identity_truncated', truncated: true },
    )
    const result = present(item)
    expect(result.title).toBe('Container not running at sampling time')
    expect(result.facts.map((fact) => fact.value).slice(0, 4)).toEqual([
      'Not running',
      '0',
      'No',
      '0',
    ])
    expect(result.notices).toContain(
      i18n.global.t('releaseGuardPresentation.runtimeImageTruncated'),
    )
    const wrapper = mount(GuardEvidenceCard, {
      props: { evidence: item, targetName: 'Web / app' },
    })
    expect(
      wrapper
        .findAll('.evidence-notice')
        .some((notice) =>
          notice.text().includes('Image metadata was truncated'),
        ),
    ).toBe(true)
    expect(wrapper.get('details pre').text()).toContain(
      'image_identity_truncated',
    )
    wrapper.unmount()
  })
  it.each(['unavailable', 'ok'])(
    'never trusts a truncated container inspection even if outcome is %s',
    (outcome) => {
      const result = present(
        evidence(
          {
            ...sample,
            outcome,
            reason: 'inspection_truncated',
            truncated: true,
          },
          { reason: 'inspection_truncated', truncated: true },
        ),
      )
      expect(result.facts.every((fact) => fact.value === 'Unknown')).toBe(true)
      expect(result.tone).toBe('warning')
    },
  )
  it('preserves captured runtime facts but warns on changed or malformed target bindings', () => {
    const changed = present(
      evidence({
        ...sample,
        running: true,
        targetChanged: true,
        reason: 'target_configuration_changed',
      }),
    )
    expect(changed.tone).toBe('warning')
    expect(changed.facts.map((fact) => fact.value).slice(0, 4)).toEqual([
      'Running',
      '0',
      'No',
      '0',
    ])
    expect(changed.notices).toContain(
      i18n.global.t('releaseGuardPresentation.targetBindingChanged'),
    )
    const unchanged = present(
      evidence({ ...sample, running: true, targetChanged: false }),
    )
    expect(unchanged.tone).toBe('neutral')
    expect(unchanged.notices).toEqual([])
    for (const targetChanged of ['true', 'false', 1, null]) {
      const malformed = present(
        evidence({ ...sample, running: true, targetChanged }),
      )
      expect(malformed.tone).toBe('warning')
      expect(malformed.facts[0].value).toBe('Running')
      expect(malformed.notices).toContain(
        i18n.global.t('releaseGuardPresentation.targetBindingUnknown'),
      )
      expect(malformed.notices).not.toContain(
        i18n.global.t('releaseGuardPresentation.targetBindingChanged'),
      )
    }
  })
  it.each(['logs_unavailable', 'log_scope_invalid'])(
    'does not treat %s and its default false/empty values as a collected log window',
    (reason) => {
      const result = present(
        evidence(
          {
            text: '',
            gap: false,
            from: '2026-10-25T11:40:00Z',
            until: '2026-10-25T11:41:00Z',
          },
          { kind: 'logs', reason },
        ),
      )
      expect(result.title).toBe('No windowed logs obtained')
      expect(result.tone).toBe('warning')
      expect(
        result.facts.find(
          (fact) => fact.label === 'Log collection schedule gap',
        )?.value,
      ).toBe('Unknown')
      expect(
        result.facts.find((fact) => fact.label === 'Collected log lines')
          ?.value,
      ).toBe('Unknown')
    },
  )
  it.each(['logs_window_partial', 'untrusted_windowed_logs'])(
    'marks partial or truncated log windows explicitly for %s',
    (reason) => {
      const result = present(
        evidence(
          { text: 'retained line', gap: false },
          { kind: 'logs', reason, truncated: true },
        ),
      )
      expect(result.title).toBe('Only partial windowed logs retained')
      expect(result.tone).toBe('warning')
      expect(result.notices).toContain(
        i18n.global.t('releaseGuardPresentation.logsPartialScope'),
      )
      expect(
        result.facts.find((fact) => fact.label === 'Collected log lines')
          ?.value,
      ).toBe('1')
    },
  )
  it('distinguishes a collected empty log window from unknown collection status', () => {
    const values = { text: '', gap: false }
    const empty = present(
      evidence(values, { kind: 'logs', reason: 'untrusted_windowed_logs' }),
    )
    expect(empty.title).toBe('Log window collected; no lines returned')
    expect(empty.tone).toBe('neutral')
    expect(
      empty.facts.find((fact) => fact.label === 'Collected log lines')?.value,
    ).toBe('0')
    expect(
      empty.facts.find((fact) => fact.label === 'Log collection schedule gap')
        ?.value,
    ).toBe('No')
    const unknown = present(
      evidence(values, { kind: 'logs', reason: 'future_reason' }),
    )
    expect(unknown.tone).toBe('warning')
    expect(
      unknown.facts.find((fact) => fact.label === 'Collected log lines')?.value,
    ).toBe('Unknown')
  })
  it('explains known cache, restart and log-gap reasons while keeping a generic fallback for unknown codes', () => {
    for (const code of [
      'source_cache_busy',
      'source_cache_missing',
      'source_cache_unreadable',
      'base_commit_not_cached',
      'current_commit_not_cached',
      'source_tree_unavailable',
      'log_time_gap',
      'service_restarted',
      'resolved_source_unavailable',
      'source_run_unavailable',
      'source_revision_conflict',
      'source_revision_not_confirmed',
      'current_deployment_identity_unavailable',
      'deployment_history_unavailable',
    ]) {
      const text = guardPresentationText(code, i18n.global.t)
      expect(text).not.toBe(code)
      expect(text).not.toBe(
        i18n.global.t('releaseGuardPresentation.unconfirmed'),
      )
      expect(text).not.toContain('releaseGuard')
      expect(text).not.toMatch(/container/i)
    }
    expect(guardPresentationText('unknown_future_code', i18n.global.t)).toBe(
      i18n.global.t('releaseGuardPresentation.unconfirmed'),
    )
  })
  it('translates known timeline stages without assigning a verification result to completion', () => {
    expect(guardTimelineTitle('runtime_sample', i18n.global.t)).toBe(
      'Runtime sample',
    )
    expect(guardTimelineTitle('finished', i18n.global.t)).toBe(
      'Observation task ended',
    )
    expect(guardTimelineTitle('logs_requested', i18n.global.t)).toBe(
      'Bounded log collection requested',
    )
    expect(
      guardTimelineTitle('baseline_already_failing', i18n.global.t),
    ).toContain('already recorded a runtime failure')
    expect(guardTimelineTitle('future_internal_kind', i18n.global.t)).toBe(
      i18n.global.t('releaseGuardPresentation.unconfirmed'),
    )
  })
  it('does not infer health from collection success, missing fields or a fabricated nested imageIdentity', () => {
    const result = present(
      evidence({ outcome: 'ok', imageIdentity: { digest: sample.configId } }),
    )
    expect(result.title).toBe('This evidence cannot be interpreted yet')
    expect(result.facts.every((item) => item.value === 'Unknown')).toBe(true)
  })
  it.each(['unavailable', 'target_changed', 'future_outcome', undefined])(
    'never interprets Go zero defaults for outcome %s',
    (outcome) => {
      const result = present(evidence({ ...sample, outcome }))
      expect(result.facts.every((item) => item.value === 'Unknown')).toBe(true)
      expect(result.tone).toBe('warning')
    },
  )
  it('describes strictly confirmed absence while leaving its zero runtime fields unknown', () => {
    const result = present(
      evidence({
        ...sample,
        outcome: 'not_found',
        reason: 'container_not_found',
      }),
    )
    expect(result.title).toBe('Container confirmed absent at sampling time')
    expect(result.tone).toBe('danger')
    expect(result.facts.every((item) => item.value === 'Unknown')).toBe(true)
    expect(
      present(
        evidence({ ...sample, outcome: 'not_found', reason: 'future_reason' }),
      ).title,
    ).toBe('No valid runtime sample obtained')
  })
  it.each([
    {
      running: 'false',
      oomKilled: 'false',
      exitCode: '0',
      restartCount: '0',
      health: 'future',
      configId: false,
    },
    {
      running: null,
      oomKilled: 0,
      exitCode: -1,
      restartCount: -1,
      health: false,
      configId: 'sha256:short',
    },
    {
      running: 1,
      oomKilled: null,
      exitCode: 256,
      restartCount: Infinity,
      health: null,
      configId: undefined,
    },
  ])(
    'rejects malformed field types, bounds and unknown health enums',
    (invalid) => {
      const result = present(evidence({ ...sample, ...invalid }))
      expect(result.facts.every((item) => item.value === 'Unknown')).toBe(true)
      expect(result.tone).toBe('warning')
    },
  )
  it('requires a valid expected image type and never calls a recorded expectation a match', () => {
    const result = present(
      evidence(
        {
          digest: sample.configId,
          digestKind: 'config_id',
          identitySource: 'script_pre_activation',
        },
        { kind: 'expected_image' },
      ),
    )
    expect(result.title).toBe('Expected image identity recorded')
    expect(JSON.stringify(result)).not.toContain('matched')
    expect(
      present(
        evidence(
          { digest: sample.configId, digestKind: 'future' },
          { kind: 'expected_image' },
        ),
      ).tone,
    ).toBe('warning')
  })
  it('keeps unknown evidence types generic and retains complete escaped technical values', () => {
    const malicious = '<img src=x onerror=alert(1)>'
    const wrapper = mount(GuardEvidenceCard, {
      props: {
        evidence: evidence(
          { text: malicious },
          { kind: 'future_kind', summary: malicious },
        ),
        targetName: 'Web / app',
      },
    })
    expect(wrapper.get('h3').text()).toBe(
      'This evidence cannot be interpreted yet',
    )
    expect(wrapper.get('pre').text()).toContain(malicious)
    expect(wrapper.get('pre').text()).toContain('future_kind')
    expect(wrapper.find('img').exists()).toBe(false)
    expect(wrapper.get('details').attributes('open')).toBeUndefined()
    wrapper.unmount()
  })
  it('shows escaped log text with real window fields and preserves the complete log', () => {
    const text = '<script>bad()</script>' + 'a'.repeat(800)
    const wrapper = mount(GuardEvidenceCard, {
      props: {
        evidence: evidence(
          {
            text,
            from: '2026-10-25T11:40:00Z',
            until: '2026-10-25T11:41:00Z',
            gap: false,
          },
          { kind: 'logs', reason: 'untrusted_windowed_logs', truncated: true },
        ),
        targetName: 'Web / app',
      },
    })
    expect(wrapper.get('.evidence-log pre').text()).toHaveLength(600)
    expect(wrapper.get('details pre').text()).toContain(text)
    expect(wrapper.find('script').exists()).toBe(false)
    expect(wrapper.get('.evidence-notice').text()).toContain('truncated')
    expect(wrapper.get('.evidence-facts').text()).toContain('No')
    wrapper.unmount()
  })
  it('formats valid dates by locale and rejects non-date values', () => {
    expect(guardDate('0', 'en', i18n.global.t)).toBe('Unknown')
    expect(guardDate(false, 'en', i18n.global.t)).toBe('Unknown')
    expect(guardDate('2026-10-25T11:40:00Z', 'de', i18n.global.t)).toContain(
      '25.10.2026',
    )
  })
  it('renders local evidence UI in all eight languages without exposing type codes', () => {
    for (const language of [
      'zh-CN',
      'zh-TW',
      'en',
      'ja',
      'ko',
      'es',
      'fr',
      'de',
    ] as const) {
      setLocale(language)
      const wrapper = mount(GuardEvidenceCard, {
        props: { evidence: evidence(sample), targetName: '用户目标' },
      })
      expect(wrapper.get('h3').text()).toBe(
        i18n.global.t('releaseGuardPresentation.titles.exited'),
      )
      expect(wrapper.get('header').text()).toContain('用户目标')
      expect(wrapper.get('header').text()).not.toContain('typed_runtime_sample')
      expect(wrapper.get('.evidence-facts').text()).not.toContain(
        'releaseGuard',
      )
      expect(wrapper.get('.evidence-facts').text()).toContain(
        new Intl.NumberFormat(language).format(0),
      )
      expect(wrapper.get('summary').text()).toBe(
        i18n.global.t('releaseGuardPresentation.technicalDetails'),
      )
      wrapper.unmount()
    }
  })
})
