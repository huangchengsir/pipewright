import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { i18n, setLocale, SUPPORTED_LOCALES } from '../i18n'
import AuditTimeline from './AuditTimeline.vue'
import type { AuditEntry } from '../api/audit'

const api = vi.hoisted(() => ({ list: vi.fn() }))
vi.mock('../api/audit', () => ({ listAudit: api.list }))

let wrapper: VueWrapper | undefined
function entry(action: string, detail: Record<string, unknown> = {}): AuditEntry {
  return { id: action, action, detail, actor: 'admin', targetType: 'credential', targetId: 'synthetic-target-id', timestamp: new Date().toISOString(), ip: '' }
}
async function create(entries: AuditEntry[]) {
  api.list.mockResolvedValue({ entries, nextBefore: null })
  wrapper = mount(AuditTimeline)
  await flushPromises()
  return wrapper
}
beforeEach(() => { vi.clearAllMocks(); setLocale('zh-CN') })
afterEach(() => { wrapper?.unmount(); setLocale('zh-CN') })

describe('current lifecycle audit presentation', () => {
  it('localizes the four real actions in eight locales without refetching audit data', async () => {
    const actions = [
      ['oauth_credential.authorize', 'verbAuthorizeOAuth', 'nounOAuthCredential', { provider: 'gitea' }, 'Gitea'],
      ['oauth_credential.reconnect', 'verbReconnectOAuth', 'nounOAuthCredential', { provider: 'github' }, 'GitHub'],
      ['selfupdate_submit', 'verbSubmitUpgrade', 'nounUpgrade', { target: 'v99.0.2' }, 'v99.0.2'],
      ['selfupdate_resume', 'verbResumeUpgrade', 'nounUpgrade', { target: 'v1.9.6-rc.1' }, 'v1.9.6-rc.1'],
    ] as const
    const w = await create(actions.map(([action, , , detail]) => entry(action, detail)))
    for (const { code } of SUPPORTED_LOCALES) {
      setLocale(code); await flushPromises()
      const rows = w.findAll('.aev')
      actions.forEach(([action, verb, noun, , object], index) => {
        const row = rows[index]!
        expect(row.text()).toContain(i18n.global.t(`misc.audit.${verb}`) + i18n.global.t(`misc.audit.${noun}`))
        expect(row.get('.obj').text()).toBe(object)
        expect(row.text()).not.toContain(action)
        expect(row.text()).not.toContain('synthetic-target-id')
        expect(row.text()).not.toContain('misc.audit.')
      })
    }
    expect(api.list).toHaveBeenCalledTimes(1)
  })

  it.each([
    ['gitee', 'Gitee'], ['github', 'GitHub'], ['gitlab', 'GitLab'], ['gitea', 'Gitea'], ['custom', ''],
  ])('uses only the declared provider enum for %s labels', async (provider, label) => {
    const w = await create([entry('oauth_credential.authorize', { provider })])
    expect(w.get('.obj').text()).toBe(label || i18n.global.t('settingsVault.providerCustom'))
  })

  it('does not promote arbitrary OAuth details or invalid version tags to labels', async () => {
    const w = await create([
      entry('oauth_credential.authorize', { provider: '__proto__', name: 'unexpected-provider-name' }),
      entry('oauth_credential.reconnect', { provider: '<img src=x>', branch: 'unexpected-provider-branch' }),
      entry('selfupdate_submit', { target: 'v99.0.2\nunsafe-label', name: 'unexpected-upgrade-name' }),
      entry('selfupdate_resume', { target: 'https://example.invalid/token', name: 'unexpected-upgrade-name' }),
      { ...entry('selfupdate_resume', { target: 'v99.0.2\n' }), id: 'trailing-newline' },
    ])
    for (const object of w.findAll('.obj')) expect(object.text()).toBe('synthetic-target-id')
    expect(w.text()).not.toContain('unexpected-')
    expect(w.text()).not.toContain('unsafe-label')
    expect(w.find('img').exists()).toBe(false)
  })

  it('retains older known actions and unknown-action object fallbacks', async () => {
    const w = await create([
      entry('credential_create', { name: 'Existing name', provider: 'gitea' }),
      entry('future_action', { name: 'Future name', provider: 'gitea' }),
      entry('other_action', { branch: 'main' }),
    ])
    const rows = w.findAll('.aev')
    expect(rows[0]!.text()).toContain(i18n.global.t('misc.audit.verbCreate') + i18n.global.t('misc.audit.nounCredential'))
    expect(rows[0]!.get('.obj').text()).toBe('Existing name')
    expect(rows[1]!.text()).toContain('future_action')
    expect(rows[1]!.get('.obj').text()).toBe('Future name')
    expect(rows[2]!.get('.obj').text()).toBe('main')
  })
})
