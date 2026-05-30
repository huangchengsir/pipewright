#!/usr/bin/env node
/**
 * Open Q#7 实验 harness —— 4 臂递增对照 + 盲评 LLM-judge。
 *
 * 臂:A 原始粘贴(ChatGPT 基线) / B FR-22 平台 prompt / C B+FR-25 diff / D C+知识库。
 * 底座 4 臂同模型同 temp=0(控制);judge 用另一更强模型,盲评(剥标签+打乱)。
 *
 * 真跑:
 *   node harness/run.mjs --dataset dataset/cases.json \
 *     --provider ollama --base http://localhost:11434 --model qwen2.5-coder:7b \
 *     --judge-provider ollama --judge-base http://localhost:11434 --judge-model qwen2.5-coder:14b
 * 离线 dry-run(只验装置,不验假设):
 *   node harness/run.mjs --stub
 *
 * ⚠️ --stub 的任何得分都是占位,不得当作差异化证据(见 protocol.md §8)。
 */

import { readFile, writeFile, mkdir } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const __dirname = dirname(fileURLToPath(import.meta.url))
const ROOT = resolve(__dirname, '..')

// ---- args ----
const args = process.argv.slice(2)
const flag = (k, d) => { const i = args.indexOf(k); return i >= 0 && i + 1 < args.length ? args[i + 1] : d }
const has = (k) => args.includes(k)
const STUB = has('--stub')
const DATASET = flag('--dataset', STUB ? `${ROOT}/dataset/seed-cases.json` : `${ROOT}/dataset/cases.json`)
const OUT = flag('--out', `${ROOT}/results`)

const gen = {
  provider: flag('--provider', STUB ? 'stub' : 'ollama'),
  base: flag('--base', 'http://localhost:11434'),
  model: flag('--model', 'qwen2.5-coder:7b'),
  key: flag('--key', process.env.LLM_API_KEY || ''),
}
const judge = {
  provider: flag('--judge-provider', STUB ? 'stub' : gen.provider),
  base: flag('--judge-base', gen.base),
  model: flag('--judge-model', STUB ? 'stub' : gen.model),
  key: flag('--judge-key', gen.key),
}

// ---- LLM 调用抽象 ----
async function callLLM(cfg, system, user) {
  if (cfg.provider === 'stub') return stubLLM(system, user)
  const ctrl = new AbortController()
  const t = setTimeout(() => ctrl.abort(), 60000)
  try {
    if (cfg.provider === 'ollama') {
      const r = await fetch(`${cfg.base}/api/chat`, {
        method: 'POST', signal: ctrl.signal,
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ model: cfg.model, stream: false, options: { temperature: 0 },
          messages: [{ role: 'system', content: system }, { role: 'user', content: user }] }),
      })
      if (!r.ok) throw new Error(`ollama ${r.status}`)
      return (await r.json()).message?.content ?? ''
    }
    if (cfg.provider === 'openai') {
      const r = await fetch(`${cfg.base}/v1/chat/completions`, {
        method: 'POST', signal: ctrl.signal,
        headers: { 'content-type': 'application/json', authorization: `Bearer ${cfg.key}` },
        body: JSON.stringify({ model: cfg.model, temperature: 0,
          messages: [{ role: 'system', content: system }, { role: 'user', content: user }] }),
      })
      if (!r.ok) throw new Error(`openai ${r.status}`)
      return (await r.json()).choices?.[0]?.message?.content ?? ''
    }
    if (cfg.provider === 'claude') {
      const r = await fetch(`${cfg.base}/v1/messages`, {
        method: 'POST', signal: ctrl.signal,
        headers: { 'content-type': 'application/json', 'x-api-key': cfg.key, 'anthropic-version': '2023-06-01' },
        body: JSON.stringify({ model: cfg.model, max_tokens: 1024, temperature: 0, system,
          messages: [{ role: 'user', content: user }] }),
      })
      if (!r.ok) throw new Error(`claude ${r.status}`)
      return (await r.json()).content?.[0]?.text ?? ''
    }
    throw new Error(`unknown provider ${cfg.provider}`)
  } finally { clearTimeout(t) }
}

// ---- 确定性 stub(dry-run):据 ground-truth 给"够好"答案,但故意让 A 弱一档以演示装置 ----
let stubMod = null
async function loadStub() { stubMod = await import('./stub-llm.mjs') }
function stubLLM(system, user) { return stubMod.respond(system, user) }

// ---- prompt 构造 ----
// 臂 A:原始粘贴(ChatGPT 基线)——无结构、无额外上下文。
function promptA(c) {
  return { system: '你是一个有帮助的助手。',
    user: `${c.failureLog}\n\n这次 CI/CD 运行失败了。哪里出了问题,我该怎么修?` }
}

// 臂 B:FR-22 平台 prompt —— 忠实复刻 internal/ai/diagnose.go buildDiagnosePrompt。
function promptB(c, { diff = false, kb = false } = {}) {
  let b = ''
  b += '你是 CI/CD 失败分析助手。请根据下面一次流水线运行的**失败日志**,给出最可能的根因「假说」、修复建议与置信度。\n'
  b += '重要:你的结论是**假说,不是定论**;措辞须用「最可能的根因是 X(假说,非结论)」这类表述。\n'
  b += '当置信度不高(medium/low)时,必须在 alternateCauses 列出其它可能根因。\n\n'
  if (c.stack) b += `## 项目技术栈\n${c.stack}\n\n`
  if (c.step) b += `## 失败步骤\n${c.step}\n\n`
  if (diff && c.diffSummary) b += `## 自上次成功运行的变更(diff,FR-25)\n${c.diffSummary}\n\n`
  if (kb && Array.isArray(c.kbCases) && c.kbCases.length) {
    b += '## 知识库:过往同类失败与修复(FR-22 精选知识库)\n'
    for (const k of c.kbCases) b += `- 症状:${k.symptom} / 根因:${k.rootCause} / 修复:${k.fix}\n`
    b += '\n'
  }
  b += '## 失败日志(已脱敏;[MASKED] 为被屏蔽的敏感信息,行首为行号)\n'
  c.failureLog.split('\n').forEach((ln, i) => { b += `${i + 1}: ${ln}\n` })
  b += `
## 常见失败模式参考(仅供参考,以实际日志为准)
- 依赖缺失 / 版本不存在 / peer 冲突
- lockfile 未提交或与 manifest 不一致
- 认证 / 权限 / 速率限制失败
- 编译 / 测试失败
- 网络 / 超时 / registry 不可达
- 资源不足(OOM、磁盘满)

## 输出要求
只输出一个 JSON 对象(不要解释文字、不要 markdown 代码块):
{
  "hypothesis": "最可能的根因是 ...(假说,非结论)",
  "confidence": "high|medium|low",
  "alternateCauses": ["其它可能根因"],
  "fixSuggestions": ["可执行的修复建议"],
  "evidenceLines": [12, 13]
}
`
  return { system: '你是 CI/CD 失败分析助手。', user: b }
}

const ARMS = {
  A: (c) => promptA(c),
  B: (c) => promptB(c, {}),
  C: (c) => promptB(c, { diff: true }),
  D: (c) => promptB(c, { diff: true, kb: true }),
}

// ---- 盲评 judge ----
function judgePrompt(c, diagnosisText) {
  const gt = c.groundTruth
  const user = `你是严格的诊断质量裁判。下面是一次 CI/CD 失败的**标准答案(ground-truth)**与**一份待评诊断**。
请只依据标准答案对待评诊断打分,严格、不放水。

## 标准答案
- 真实根因:${gt.rootCause}
- 决定性日志/diff 行号:${(gt.decisiveLines || []).join(', ')}
- 正确修复:${gt.correctFix}

## 待评诊断(来源已匿名,不要猜它来自哪种方法)
${diagnosisText}

## 打分(只输出一个 JSON,不要解释)
{
  "rootCause": 0|1|2,   // 2=精确命中真实根因, 1=相邻/部分, 0=错或漏
  "fix": 0|1|2,         // 2=正确且具体可直接执行, 1=合理但含糊/不全, 0=错
  "evidence": 0|1,      // 是否引用了真正决定性的行(或等价指出关键证据)
  "penalty": 0           // 若编造了不存在的根因/文件/行则为 -1, 否则 0
}`
  return { system: '你是严格的诊断质量裁判,只输出 JSON。', user }
}

function parseJSON(text) {
  if (!text) return null
  let t = text.trim()
  const fence = t.match(/```(?:json)?\s*([\s\S]*?)```/i)
  if (fence) t = fence[1].trim()
  const s = t.indexOf('{'), e = t.lastIndexOf('}')
  if (s >= 0 && e > s) t = t.slice(s, e + 1)
  try { return JSON.parse(t) } catch { return null }
}

const composite = (s) => (s.rootCause * 2) + (s.fix * 2) + s.evidence + s.penalty

// ---- 主流程 ----
async function main() {
  if (gen.provider === 'stub' || judge.provider === 'stub') await loadStub()
  const ds = JSON.parse(await readFile(DATASET, 'utf8'))
  const cases = ds.cases || []
  if (!cases.length) { console.error(`数据集为空:${DATASET}`); process.exit(1) }

  console.log(`[实验] 数据集=${DATASET}(${cases.length} 案) · 底座=${gen.provider}/${gen.model} · 裁判=${judge.provider}/${judge.model}${STUB ? ' · STUB DRY-RUN(不验假设)' : ''}`)

  const rows = [] // {caseId, difficulty, arm, score, composite, output}
  for (const c of cases) {
    // 生成 4 臂(顺序随后打乱给 judge,judge 不知臂别)。
    const armKeys = ['A', 'B', 'C', 'D']
    const outputs = {}
    for (const a of armKeys) {
      const { system, user } = ARMS[a](c)
      let text = ''
      try { text = await callLLM(gen, system, user) } catch (e) { text = `__GEN_ERROR__ ${e.message}` }
      outputs[a] = text
    }
    // 盲评:打乱顺序后逐臂判。
    const order = shuffle([...armKeys], c.id)
    for (const a of order) {
      const { system, user } = judgePrompt(c, outputs[a])
      let raw = ''
      try { raw = await callLLM(judge, system, user) } catch (e) { raw = '' }
      const parsed = parseJSON(raw) || { rootCause: 0, fix: 0, evidence: 0, penalty: 0, _unscored: true }
      const score = {
        rootCause: clamp(parsed.rootCause, 0, 2), fix: clamp(parsed.fix, 0, 2),
        evidence: clamp(parsed.evidence, 0, 1), penalty: clamp(parsed.penalty, -1, 0),
      }
      rows.push({ caseId: c.id, difficulty: c.difficulty, arm: a, score, composite: composite(score), output: outputs[a] })
    }
    console.log(`  · ${c.id}(${c.difficulty}) 完成`)
  }

  const report = aggregate(cases, rows)
  await mkdir(OUT, { recursive: true })
  const stamp = STUB ? 'stub-dryrun' : 'run'
  await writeFile(`${OUT}/${stamp}-raw.json`, JSON.stringify({ gen, judge, rows }, null, 2))
  await writeFile(`${OUT}/${stamp}-report.md`, report)
  console.log(`\n[报告] ${OUT}/${stamp}-report.md`)
  console.log(report.split('\n').slice(0, 40).join('\n'))
}

function clamp(v, lo, hi) { v = Number(v); if (!Number.isFinite(v)) return lo; return Math.max(lo, Math.min(hi, Math.round(v))) }
function shuffle(arr, seed) { // 确定性打乱(seed=caseId)
  let h = 0; for (const ch of String(seed)) h = (h * 31 + ch.charCodeAt(0)) >>> 0
  for (let i = arr.length - 1; i > 0; i--) { h = (h * 1103515245 + 12345) >>> 0; const j = h % (i + 1);[arr[i], arr[j]] = [arr[j], arr[i]] }
  return arr
}

function aggregate(cases, rows) {
  const arms = ['A', 'B', 'C', 'D']
  const byArm = Object.fromEntries(arms.map(a => [a, rows.filter(r => r.arm === a)]))
  const mean = (xs) => xs.length ? xs.reduce((s, x) => s + x, 0) / xs.length : 0
  const armMean = Object.fromEntries(arms.map(a => [a, mean(byArm[a].map(r => r.composite))]))
  const armRC = Object.fromEntries(arms.map(a => [a, mean(byArm[a].map(r => r.score.rootCause))]))

  // 配对差值 + A-未答对子集胜率
  const byCase = {}
  for (const r of rows) { (byCase[r.caseId] ||= {})[r.arm] = r }
  const ids = Object.keys(byCase)
  const delta = (x, y) => mean(ids.map(id => byCase[id][x].composite - byCase[id][y].composite))
  const upliftPct = (x) => armMean.A ? ((armMean[x] - armMean.A) / armMean.A) * 100 : NaN
  const aWrong = ids.filter(id => byCase[id].A.score.rootCause < 2)
  const winOnAwrong = (x) => aWrong.length ? aWrong.filter(id => byCase[id][x].score.rootCause === 2).length / aWrong.length * 100 : NaN
  const signTest = (x) => { // 简单符号检验(配对,x vs A)
    let w = 0, l = 0, t = 0
    for (const id of ids) { const d = byCase[id][x].composite - byCase[id].A.composite; if (d > 0) w++; else if (d < 0) l++; else t++ }
    return { w, l, t }
  }

  const best = ['C', 'D'].reduce((b, a) => armMean[a] > armMean[b] ? a : b, 'C')
  const stC = signTest('C'), stD = signTest('D')

  let md = `# Open Q#7 实验报告${STUB ? '(STUB DRY-RUN —— 仅验装置,得分不作数)' : ''}\n\n`
  md += `- 数据集:${cases.length} 案 · 底座 ${gen.provider}/${gen.model} · 裁判 ${judge.provider}/${judge.model}\n`
  md += STUB ? `- ⚠️ **STUB 模式:下列任何数字均为占位,严禁当作差异化证据(protocol.md §8)。**\n` : ''
  md += `\n## 各臂均值\n\n| 臂 | 复合分均值(0–11) | 根因准确率均值(0–2) |\n|----|----|----|\n`
  for (const a of arms) md += `| ${a} ${({ A: '原始粘贴', B: 'FR-22 平台', C: '+FR-25 diff', D: '+知识库' })[a]} | ${armMean[a].toFixed(2)} | ${armRC[a].toFixed(2)} |\n`

  md += `\n## 关键差值(隔离价值来源)\n\n`
  md += `| 对比 | 含义 | 复合分差 | 提升% | A-未答对子集胜率 | 符号检验(胜/负/平) |\n|----|----|----|----|----|----|\n`
  md += `| C−A | 装配上下文 vs 裸粘贴 | ${delta('C', 'A').toFixed(2)} | ${upliftPct('C').toFixed(1)}% | ${winOnAwrong('C').toFixed(0)}% | ${stC.w}/${stC.l}/${stC.t} |\n`
  md += `| D−A | 全装置 vs 裸粘贴 | ${delta('D', 'A').toFixed(2)} | ${upliftPct('D').toFixed(1)}% | ${winOnAwrong('D').toFixed(0)}% | ${stD.w}/${stD.l}/${stD.t} |\n`
  md += `| C−B | **diff 边际(gate 7-3)** | ${delta('C', 'B').toFixed(2)} | — | — | — |\n`
  md += `| D−C | **KB 边际(gate 7-5)** | ${delta('D', 'C').toFixed(2)} | — | — | — |\n`

  // 决策(对照 protocol §6 阈值;n<20 时只给方向,不下最终结论)
  md += `\n## 决策(对照 protocol §6 预注册阈值)\n\n`
  const n = ids.length
  const passUplift = upliftPct(best) >= 25
  const passWin = winOnAwrong(best) >= 60
  const diffMarginal = delta('C', 'B') > 0.5
  if (n < 20) {
    md += `> ⚠️ **n=${n} < 20,统计不充分,以下仅为方向性观察,非最终 GO/NO-GO。** 真跑须用作者 ~20 条真实日志。\n\n`
  }
  md += `- 最佳平台臂:**${best}**(复合分均值 ${armMean[best].toFixed(2)} vs A ${armMean.A.toFixed(2)})\n`
  md += `- 提升 ≥25%? ${passUplift ? '✅' : '❌'}(${upliftPct(best).toFixed(1)}%)\n`
  md += `- A-未答对子集胜率 ≥60%? ${passWin ? '✅' : '❌'}(${winOnAwrong(best).toFixed(0)}%)\n`
  md += `- diff 有边际(C−B>0.5,证明 7-3 值得)? ${diffMarginal ? '✅' : '❌'}(${delta('C', 'B').toFixed(2)})\n\n`
  let verdict = 'NO-GO / 重定差异点'
  if (passUplift && passWin && diffMarginal) verdict = 'GO(按计划建 7-3/7-5)'
  else if (passUplift && passWin) verdict = 'PARTIAL(KB 有用、diff 边际弱 → 重排序)'
  md += `**方向性判定:${STUB ? '(stub 占位)' : ''}${verdict}**\n`

  md += `\n## 逐案明细\n\n| 案 | 难度 | A | B | C | D |\n|----|----|----|----|----|----|\n`
  for (const id of ids) md += `| ${id} | ${byCase[id].A.difficulty} | ${byCase[id].A.composite} | ${byCase[id].B.composite} | ${byCase[id].C.composite} | ${byCase[id].D.composite} |\n`
  return md
}

main().catch(e => { console.error(e); process.exit(1) })
