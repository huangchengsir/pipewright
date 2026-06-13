/**
 * 把当前 i18n 语言映射到 naive-ui 的 locale + dateLocale,供 NConfigProvider 绑定。
 * 管的是 naive 组件自带文案(分页、日期选择器、上传等),与应用词条同步切换。
 */
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  zhCN, dateZhCN,
  zhTW, dateZhTW,
  enUS, dateEnUS,
  jaJP, dateJaJP,
  koKR, dateKoKR,
  esAR, dateEsAR,
  frFR, dateFrFR,
  deDE, dateDeDE,
} from 'naive-ui'
import type { LocaleCode } from '../i18n'

const LOCALE_MAP = {
  'zh-CN': { locale: zhCN, date: dateZhCN },
  'zh-TW': { locale: zhTW, date: dateZhTW },
  en: { locale: enUS, date: dateEnUS },
  ja: { locale: jaJP, date: dateJaJP },
  ko: { locale: koKR, date: dateKoKR },
  es: { locale: esAR, date: dateEsAR },
  fr: { locale: frFR, date: dateFrFR },
  de: { locale: deDE, date: dateDeDE },
} as const

export function useNaiveLocale() {
  const { locale } = useI18n({ useScope: 'global' })

  const entry = computed(() => LOCALE_MAP[locale.value as LocaleCode] ?? LOCALE_MAP['zh-CN'])
  const naiveLocale = computed(() => entry.value.locale)
  const naiveDateLocale = computed(() => entry.value.date)

  return { naiveLocale, naiveDateLocale }
}
