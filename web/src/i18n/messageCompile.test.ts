/**
 * messageCompile.test.ts — 回归:每个 locale 的每条消息都必须能被 @intlify 消息编译器
 * 成功编译。用 `@intlify/core-base` 的 `compile`(vue-i18n 运行时内部所用、默认**抛错**的
 * 路径)逐条编译,忠实复现浏览器:任何 message-format 语法错都会抛 → 测试红。
 *
 * 背景:i18n 改造把含字面 `{{project}}`/`{{参数}}` 的展示提示搬进词典后,`{{` 被当成
 * 嵌套占位符(@intlify CompileError code 9 = NOT_ALLOW_NEST_PLACEHOLDER)→ 渲染该提示时
 * 抛 SyntaxError,构建(script)节点抽屉整个打不开。修法:把字面占位包成 vue-i18n 字面
 * 插值 `{'{{X}}'}`(`{{` 落入字面、不再被当嵌套占位 → 不抛,且原样渲染)。
 *
 * 注意:不能用 vue-i18n 的 `t()` 在测试里验证 —— 测试跑的是 dev 构建,编译错会被「恢复」
 * 吞掉(不抛),无法复现生产崩溃;`core.compile` 默认抛错,才是可靠 oracle。
 */
import { describe, it, expect } from 'vitest'
import { compile, createMessageContext } from '@intlify/core-base'
import zhCN from './locales/zh-CN'
import zhTW from './locales/zh-TW'
import en from './locales/en'
import ja from './locales/ja'
import ko from './locales/ko'
import es from './locales/es'
import fr from './locales/fr'
import de from './locales/de'

const LOCALES: Record<string, unknown> = { 'zh-CN': zhCN, 'zh-TW': zhTW, en, ja, ko, es, fr, de }

/** 递归收集所有 string 叶子的点路径。 */
function collectKeys(obj: unknown, prefix = ''): [string, string][] {
  if (typeof obj === 'string') return [[prefix, obj]]
  if (obj && typeof obj === 'object') {
    return Object.entries(obj as Record<string, unknown>).flatMap(([k, v]) =>
      collectKeys(v, prefix ? `${prefix}.${k}` : k),
    )
  }
  return []
}

describe('i18n message compilation (production-faithful)', () => {
  for (const [code, messages] of Object.entries(LOCALES)) {
    it(`every ${code} message compiles without a message-format error`, () => {
      const failures: string[] = []
      for (const [key, src] of collectKeys(messages)) {
        try {
          compile(src, { key, locale: code })
        } catch (e) {
          const code9 = (e as { code?: number }).code
          failures.push(`${key} (code=${code9}): ${JSON.stringify(src).slice(0, 60)}`)
        }
      }
      expect(failures, `unparseable ${code} messages`).toEqual([])
    })
  }

  it('escaped Pipewright placeholders render verbatim (not nested-parsed)', () => {
    const render = (src: string): string => {
      const msg = compile(src, { key: 'render', locale: 'zh-CN' })
      return msg(createMessageContext({})) as string
    }
    const pj = (zhCN as unknown as { pipelineJob: Record<string, string> }).pipelineJob
    // 这几条曾因字面 `{{…}}` 触发 code-9 崩溃;转义后应原样渲染出双花括号。
    expect(render(pj.fieldCommandTemplateHint)).toContain('{{参数}}')
    expect(render(pj.fieldTitleTemplateHint)).toContain('{{project}}')
    // ${VAR} 占位同样原样渲染(此前被当成空命名占位 → 渲染成空)。
    expect(render(pj.fieldTagHint)).toContain('${COMMIT_SHA}')
  })
})
