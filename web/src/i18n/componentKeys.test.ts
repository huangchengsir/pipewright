/**
 * componentKeys.test.ts — 回归:组件里每个静态 `t('namespace.path')` key 都必须在 zh-CN 真存在。
 *
 * 背景:keyParity.test.ts 只比对 8 个 locale 彼此的 key 集合是否一致,**不**校验「组件实际
 * 引用的 key 是否存在于 locale 文件里」。于是会漏一类 bug:某功能的组件全写好了,却忘了建对应
 * 的 locale 文件(或漏了某个 key)—— 8 个 locale 都缺,parity 仍全绿,但页面渲染出裸 key
 * (如 `previewEnvs.board.title`)。本测试正面根治这类缺口:
 *
 *   扫描 src/ 下所有 .vue 的静态 `t('a.b.c')` / `t("a.b.c")` 调用 → 逐一断言能在 zh-CN
 *   合并后的消息树里走通点路径并解析到一个 string。zh-CN 是产品默认语言、词条唯一真源;
 *   配合 keyParity(8 locale key 集一致)即可保证任意语言下该 key 都有译文。
 *
 * 仅扫描「字面量字符串 key」;动态拼接 key(模板字符串 / 变量)无法静态校验,见下方 DYNAMIC_KEYS
 * 白名单(每条都已核实确为运行期拼接,且其各分支字面后缀在 zh-CN 中均存在)。
 */
import { describe, it, expect } from 'vitest'

// 基础文件:locales/zh-CN.ts(顶层命名空间,如 nav / login / dashboard…)。
const baseModules = import.meta.glob<{ default: Record<string, unknown> }>('./locales/zh-CN.ts', {
  eager: true,
})
// 按页面拆分的命名空间:locales/zh-CN/<page>.ts(文件名即顶层命名空间)。
const pageModules = import.meta.glob<{ default: Record<string, unknown> }>('./locales/zh-CN/*.ts', {
  eager: true,
})
// 待扫描的组件源码(import.meta.glob 以 raw 形式读入字符串)。
const vueSources = import.meta.glob('../**/*.vue', {
  eager: true,
  query: '?raw',
  import: 'default',
}) as Record<string, string>

/** 组装 zh-CN 完整消息树,复刻 i18n/index.ts 的合并逻辑。 */
function buildZhCN(): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  for (const mod of Object.values(baseModules)) Object.assign(out, mod.default)
  for (const [path, mod] of Object.entries(pageModules)) {
    const m = path.match(/\/zh-CN\/([^/]+)\.ts$/)
    if (m) out[m[1]] = mod.default
  }
  return out
}

/** 按点路径在消息树里下钻,命中 string 叶子返回 true。 */
function resolves(tree: Record<string, unknown>, dotted: string): boolean {
  let node: unknown = tree
  for (const seg of dotted.split('.')) {
    if (node == null || typeof node !== 'object') return false
    node = (node as Record<string, unknown>)[seg]
  }
  return typeof node === 'string'
}

/**
 * 动态 key 白名单:这些前缀对应运行期拼接的 t() 调用(模板字符串),无法被字面量正则捕获,
 * 因此也不会被本测试扫到 —— 列在此处仅作文档,声明「我们知道它们存在且故意不静态校验」。
 * 经核实其所有可能的具体 key(各枚举后缀)在 zh-CN 中均已就位:
 *   - reverseProxy.adv.lbPolicy_<policy>  (RouteAdvancedSettings.vue:`lbPolicy_${p}`,
 *     p ∈ round_robin|least_conn|random|first,四个具体 key 均存在)
 */
const DYNAMIC_KEY_PREFIXES: readonly string[] = ['reverseProxy.adv.lbPolicy_']

// 只匹配字面量字符串 key:t('a.b') / t("a.b.c");跳过模板字符串(反引号)与变量。
const T_CALL = /\bt\(\s*['"]([a-zA-Z][\w-]*(?:\.[a-zA-Z0-9_-]+)+)['"]/g

describe('component i18n keys resolve in zh-CN (source of truth)', () => {
  const zhCN = buildZhCN()

  // 收集 (组件文件 → key) 引用;按 key 去重以减少噪音,但保留首个引用文件用于报错定位。
  type Ref = { file: string; key: string }
  const refs: Ref[] = []
  for (const [file, src] of Object.entries(vueSources)) {
    if (typeof src !== 'string') continue
    let m: RegExpExecArray | null
    T_CALL.lastIndex = 0
    while ((m = T_CALL.exec(src)) !== null) {
      const key = m[1]
      if (DYNAMIC_KEY_PREFIXES.some((p) => key.startsWith(p))) continue
      refs.push({ file: file.replace(/^.*\/src\//, 'src/'), key })
    }
  }

  it('found a non-trivial number of static t() keys to guard', () => {
    // 防回归:若扫描器静默失效(0 命中),本测试本身就失去意义。
    expect(refs.length).toBeGreaterThan(100)
  })

  it('every static t() key referenced by a component exists in zh-CN', () => {
    const missing = refs
      .filter((r) => !resolves(zhCN, r.key))
      .map((r) => `${r.file} → ${r.key}`)
      // 去重并排序,稳定 diff。
      .filter((v, i, a) => a.indexOf(v) === i)
      .sort()
    expect(missing).toEqual([])
  })
})
