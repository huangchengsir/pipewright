/**
 * 把当前 i18n 语言映射到 naive-ui 的 locale + dateLocale,供 NConfigProvider 绑定。
 * 管的是 naive 组件自带文案(分页、日期选择器、上传等),与应用词条同步切换。
 */
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { zhCN, dateZhCN, enUS, dateEnUS } from 'naive-ui'

export function useNaiveLocale() {
  const { locale } = useI18n({ useScope: 'global' })

  const naiveLocale = computed(() => (locale.value === 'en' ? enUS : zhCN))
  const naiveDateLocale = computed(() => (locale.value === 'en' ? dateEnUS : dateZhCN))

  return { naiveLocale, naiveDateLocale }
}
