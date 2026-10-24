import { describe, expect, it } from 'vitest'
import { releaseGuardText } from './releaseGuardText'
import { t } from '../i18n'

describe('release guard fact descriptions', () => {
  it('distinguishes deployment completion from deployment-time health failures', () => {
    expect(releaseGuardText('deployment_completion_pending', t)).toContain('等待本次部署的最终回执')
    expect(releaseGuardText('deployment_completed', t)).toContain('本次部署成功的最终回执')
    expect(releaseGuardText('deployment_outcome_unavailable', t)).toContain('结果尚不能核实')
    for (const code of ['deployment_health_gate_failed', 'recorded_deployment_health_gate_failure']) {
      expect(releaseGuardText(code, t)).toContain('持续 Docker 健康状态分别判断')
    }
    expect(releaseGuardText('deployment_health_gate', t)).toBe('部署时健康门控')
    expect(releaseGuardText('deployment_evidence_unavailable', t)).toContain('失败结论保留，不引用未保存的证据')
    expect(t('releaseGuard.conditions.deployment_completion')).toBe('部署最终结果')
    expect(t('releaseGuard.conditions.deployment_health_gate')).toBe('部署时健康门控')
  })
})
