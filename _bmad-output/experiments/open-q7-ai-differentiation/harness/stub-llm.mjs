/**
 * 确定性 stub LLM(仅 dry-run 验装置)。
 *
 * 诚实边界:
 *  - 生成 stub 只读 prompt 内的上下文(看不到 ground-truth)。上下文越多(diff/KB 块)
 *    它能引用的线索越多 → 答得越"对"。这机械地复刻了"更多装配上下文→更好诊断"的机制,
 *    但**不是真 LLM 推理**,任何得分仅演示流水线,不构成差异化证据(protocol.md §8)。
 *  - 裁判 stub 按 ground-truth 关键词与诊断文本的重合度打分(确定性)。
 */

export function respond(system, user) {
  if (user.includes('## 标准答案') && user.includes('## 打分')) return judge(user)
  return generate(user)
}

// 抽取拉丁/数字/引号标识符 token(≥3 长),用于关键词重合。
function keywords(s) {
  const out = new Set()
  for (const m of s.matchAll(/[A-Za-z_][A-Za-z0-9_.\-]{2,}/g)) out.add(m[0].toLowerCase())
  for (const m of s.matchAll(/\d{3,}/g)) out.add(m[0]) // 如 429、5432
  return out
}

// ---- 生成臂 ----
function generate(user) {
  const hasSchema = user.includes('"hypothesis"')
  const diffBlock = section(user, '## 自上次成功运行的变更')
  const kbBlock = section(user, '## 知识库')
  // 日志里第一条 ERROR/error 行作为浅层线索(A/B 都只有这个)。
  const logErr = (user.split('\n').find(l => /error|ERROR|ERR!|FAILURE|undefined|Cannot|ECONNREFUSED|ResolutionImpossible|429/.test(l)) || '').trim()

  // 线索强度递增:A 只有 logErr;C 额外有 diff;D 额外有 KB。
  const clues = []
  if (logErr) clues.push(logErr.slice(0, 160))
  if (diffBlock) clues.push('变更线索:' + oneLine(diffBlock).slice(0, 200))
  if (kbBlock) clues.push('知识库线索:' + oneLine(kbBlock).slice(0, 200))

  if (!hasSchema) {
    // 臂 A:自由文本、笼统(只凭日志)。
    return `看起来构建失败了。从日志看,关键报错是:${logErr || '未能定位明确报错'}。\n建议先检查相关依赖/配置是否正确,必要时重试或查看完整日志。`
  }
  // 臂 B/C/D:结构化 JSON;上下文越多 hypothesis/fix 引用的线索越多。
  const hypothesis = `最可能的根因是:${clues.join(';')}(假说,非结论)`
  const fixes = []
  if (diffBlock) fixes.push('针对上次成功以来的变更回退/对齐:' + oneLine(diffBlock).slice(0, 160))
  else if (logErr) fixes.push('依据报错处理:' + logErr.slice(0, 160))
  if (kbBlock) fixes.push('参考知识库同类案例的修复路径')
  if (!fixes.length) fixes.push('检查依赖与配置后重试')
  const conf = diffBlock ? 'high' : 'low'
  return JSON.stringify({
    hypothesis, confidence: conf,
    alternateCauses: conf === 'low' ? ['也可能是环境/网络偶发', '配置缺失'] : [],
    fixSuggestions: fixes, evidenceLines: [1],
  })
}

// ---- 裁判臂:按 ground-truth 关键词重合度打分 ----
function judge(user) {
  const rootCause = afterLabel(user, '真实根因:')
  const correctFix = afterLabel(user, '正确修复:')
  const diag = section(user, '## 待评诊断') || ''
  const dk = keywords(diag)
  const overlap = (ref) => { const rk = [...keywords(ref)]; if (!rk.length) return 0; return rk.filter(k => dk.has(k)).length / rk.length }

  const rcOv = overlap(rootCause)
  const fxOv = overlap(correctFix)
  const rootCauseScore = rcOv >= 0.5 ? 2 : rcOv >= 0.2 ? 1 : 0
  const fixScore = fxOv >= 0.4 ? 2 : fxOv >= 0.15 ? 1 : 0
  const evidence = rcOv >= 0.2 ? 1 : 0
  return JSON.stringify({ rootCause: rootCauseScore, fix: fixScore, evidence, penalty: 0 })
}

// ---- helpers ----
function section(text, header) {
  const i = text.indexOf(header)
  if (i < 0) return ''
  const rest = text.slice(i + header.length)
  const next = rest.search(/\n## /)
  return (next < 0 ? rest : rest.slice(0, next)).trim()
}
function afterLabel(text, label) {
  const i = text.indexOf(label)
  if (i < 0) return ''
  const rest = text.slice(i + label.length)
  const nl = rest.indexOf('\n')
  return (nl < 0 ? rest : rest.slice(0, nl)).trim()
}
function oneLine(s) { return s.replace(/\s+/g, ' ').trim() }
