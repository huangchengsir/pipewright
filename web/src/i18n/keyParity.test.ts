/**
 * keyParity.test.ts — 回归:所有 8 个 locale 的 key 集合必须完全一致。
 *
 * zh-CN 是产品默认语言、词条的唯一真源(source of truth)。任何一种语言漏译 / 多译一个
 * key,都会让用户在该语言下看到回退文案(或界面里出现裸 key)。本测试以 zh-CN 的完整
 * key 路径集合为基准,逐一比对其余 7 种语言:缺失或多出都判红。
 *
 * 这里只比对「key 结构」(点路径集合),不校验译文内容 —— 译文质量交由人工 / 评审。
 * 基础文件(`locales/<lang>.ts`)与按页面拆分的命名空间(`locales/<lang>/<page>.ts`)
 * 都经 `import.meta.glob` 合并后比对,与运行时实际加载的消息树一致。
 */
import { describe, it, expect } from 'vitest'
import type { LocaleCode } from './index'

const SUPPORTED: LocaleCode[] = ['zh-CN', 'zh-TW', 'en', 'ja', 'ko', 'es', 'fr', 'de']

// 基础文件:locales/<lang>.ts(顶层消息,如 nav / login / dashboard…)。
const baseModules = import.meta.glob<{ default: Record<string, unknown> }>('./locales/*.ts', {
  eager: true,
})
// 按页面拆分的命名空间:locales/<lang>/<page>.ts(文件名即顶层命名空间)。
const pageModules = import.meta.glob<{ default: Record<string, unknown> }>('./locales/*/*.ts', {
  eager: true,
})

/** 组装某语言的完整消息树(基础 + 各命名空间),复刻 i18n/index.ts 的合并逻辑。 */
function messagesFor(lang: LocaleCode): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  for (const [path, mod] of Object.entries(baseModules)) {
    const m = path.match(/\.\/locales\/([^/]+)\.ts$/)
    if (m && m[1] === lang) Object.assign(out, mod.default)
  }
  for (const [path, mod] of Object.entries(pageModules)) {
    const m = path.match(/\.\/locales\/([^/]+)\/([^/]+)\.ts$/)
    if (m && m[1] === lang) out[m[2]] = mod.default
  }
  return out
}

/** 递归收集所有 string 叶子的点路径(排序后便于稳定 diff)。 */
function collectKeys(obj: unknown, prefix = ''): string[] {
  if (typeof obj === 'string') return [prefix]
  if (obj && typeof obj === 'object') {
    return Object.entries(obj as Record<string, unknown>).flatMap(([k, v]) =>
      collectKeys(v, prefix ? `${prefix}.${k}` : k),
    )
  }
  return []
}

describe('i18n key parity (all 8 locales share an identical key set)', () => {
  const reference = new Set(collectKeys(messagesFor('zh-CN')))

  for (const lang of SUPPORTED) {
    if (lang === 'zh-CN') continue
    it(`${lang} has the same keys as zh-CN`, () => {
      const keys = new Set(collectKeys(messagesFor(lang)))
      const missing = [...reference].filter((k) => !keys.has(k)).sort()
      const extra = [...keys].filter((k) => !reference.has(k)).sort()
      expect({ missing, extra }).toEqual({ missing: [], extra: [] })
    })
  }
})
