/**
 * i18n 接线 + 语言侦测。
 *
 * 默认语言取自浏览器(`navigator.languages`),用户手动切换后写 localStorage 覆盖,
 * 下次启动优先用存储值。任何一项不可用都安静回退到 zh-CN(产品默认语言)。
 *
 * 组件内用 `useI18n()`;纯函数 / api 层(无 setup 上下文)用本模块导出的 `t`。
 */
import { createI18n } from 'vue-i18n'
import zhCN from './locales/zh-CN'
import en from './locales/en'

export type LocaleCode = 'zh-CN' | 'en'

export const SUPPORTED_LOCALES: ReadonlyArray<{ code: LocaleCode; label: string }> = [
  { code: 'zh-CN', label: '简体中文' },
  { code: 'en', label: 'English' },
]

const STORAGE_KEY = 'pipewright-locale'
const DEFAULT_LOCALE: LocaleCode = 'zh-CN'

function isSupported(code: string): code is LocaleCode {
  return SUPPORTED_LOCALES.some((l) => l.code === code)
}

/** 把任意 BCP-47 标签粗匹配到受支持语言(zh* → zh-CN,en* → en)。 */
function matchLocale(tag: string): LocaleCode | null {
  const lower = tag.toLowerCase()
  if (lower.startsWith('zh')) return 'zh-CN'
  if (lower.startsWith('en')) return 'en'
  return null
}

/**
 * 侦测初始语言:存储覆盖 > 浏览器偏好 > 默认。
 * localStorage 不可用(隐私模式 / 安全策略)时安静跳过。
 */
export function detectLocale(): LocaleCode {
  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    if (stored && isSupported(stored)) return stored
  } catch {
    // ignore — private mode / SecurityError
  }
  const prefs = typeof navigator !== 'undefined' ? navigator.languages ?? [navigator.language] : []
  for (const tag of prefs) {
    const m = tag && matchLocale(tag)
    if (m) return m
  }
  return DEFAULT_LOCALE
}

export const i18n = createI18n({
  legacy: false,
  locale: detectLocale(),
  fallbackLocale: DEFAULT_LOCALE,
  messages: { 'zh-CN': zhCN, en },
})

function applyDocumentLang(code: LocaleCode): void {
  if (typeof document !== 'undefined') document.documentElement.lang = code
}

// 初始即同步 <html lang>(无障碍 / 搜索引擎)。
applyDocumentLang(i18n.global.locale.value as LocaleCode)

/** 切换当前语言:更新 vue-i18n、持久化、同步 <html lang>。 */
export function setLocale(code: LocaleCode): void {
  i18n.global.locale.value = code
  applyDocumentLang(code)
  try {
    localStorage.setItem(STORAGE_KEY, code)
  } catch {
    // ignore — degrade to in-memory only
  }
}

export function currentLocale(): LocaleCode {
  return i18n.global.locale.value as LocaleCode
}

/** 供纯函数 / api 层使用的全局翻译器(组件内请用 `useI18n()`)。 */
export const t = i18n.global.t
