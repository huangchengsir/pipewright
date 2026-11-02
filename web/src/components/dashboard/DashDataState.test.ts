import { afterEach, describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { SUPPORTED_LOCALES, setLocale } from '../../i18n'
import DashDataState from './DashDataState.vue'
afterEach(() => setLocale('zh-CN'))
describe('localized dashboard failure controls', () => {
 for (const locale of SUPPORTED_LOCALES) {
  it(`${locale.code} renders retry and failure state without unresolved keys`, async () => {
   setLocale(locale.code)
   const w = mount(DashDataState, { props: { state: 'error', updatedAt: 0, pending: false, label: 'Test' } })
   expect(w.find('button').text()).toBeTruthy(); expect(w.text()).not.toMatch(/common\.|runExperience\.|runs\./)
   await w.setProps({ state: 'stale', updatedAt: 1800000000000 }); expect(w.text()).not.toMatch(/common\.|runExperience\.|runs\./)
   await w.setProps({ forbidden: true }); expect(w.text()).not.toMatch(/common\.|runExperience\.|runs\./); w.unmount()
  })
 }
})
