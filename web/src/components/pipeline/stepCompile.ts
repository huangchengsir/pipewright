/**
 * stepCompile — 可视化「步骤构建器」与现有可执行 config 的双向纯函数转换。
 *
 * 设计契约(绝不改后端执行语义):
 * 脚本类节点(script/custom/build_frontend/build_backend/templated)在后端经
 * `scriptStepFromJob` 读取这些 config 键执行:
 *   - `image`        运行镜像(节点级,非步骤)
 *   - `commands`     多行命令字符串,按 `\n` 拆成逐行命令,在容器内拼成一个
 *                    `set -e` 单脚本经 `sh -c` 执行(见 internal/build/dag_stage_exec.go:25)
 *   - `workDir`      步骤起始工作目录(节点级)
 *   - `artifactPath` 多行产物路径(每行一条 glob)
 * 后端**不读** `env` 这类键 —— 所以「设环境变量」步骤被编译成 `export K=V` 命令行;
 * 「切目录」步骤被编译成 `cd DIR` 命令行。因为所有命令同处一个 shell 脚本,
 * `export` / `cd` 对后续命令生效,顺序语义被忠实保留,且零后端改动。
 *
 * 编译(steps → config):把有序步骤列表展开为 `commands` 多行串,产物路径汇入
 * `artifactPath`;`image`/`workDir` 作为节点级字段单独保留(由调用方合并)。
 *
 * 反解析(config → steps):把 `commands` 按行拆开,逐行识别成「设环境变量」/「切目录」/
 * 「运行命令」三类步骤(`artifactPath` 的每一行还原成「上传产物」步骤)。
 * 反解析允许有损(注释、奇异语法仍归类为「运行命令」原样保留),绝不丢用户数据。
 */

// ─── 步骤模型 ──────────────────────────────────────────────────────────────────

export type StepKind = 'command' | 'env' | 'workDir' | 'artifact'

export interface StepBlock {
  /** 稳定的本地标识(列表 key / 拖拽用),不进 config */
  id: string
  kind: StepKind
  /** command:整段命令(可多行) */
  command?: string
  /** env:环境变量名 */
  envKey?: string
  /** env:环境变量值 */
  envValue?: string
  /** workDir:目标目录 */
  dir?: string
  /** artifact:产物路径(glob) */
  artifact?: string
}

/** 编译产出:展开后的 config 片段(只含步骤拥有的键)。 */
export interface CompiledSteps {
  commands: string
  artifactPath: string
}

let _seq = 0
/** 生成稳定的步骤 id(仅前端,不进 config)。 */
export function nextStepId(): string {
  _seq += 1
  return `step-${_seq}-${Math.random().toString(36).slice(2, 7)}`
}

// ─── 编译:steps → config ───────────────────────────────────────────────────────

/** POSIX shell 单引号转义:把值安全包进 '...'(内部单引号 → '\''). */
export function shellQuote(value: string): string {
  return `'${value.replace(/'/g, `'\\''`)}'`
}

/**
 * 把一个步骤编译成 0 条或多条命令行。
 * - command:原样多行(空步骤跳过)
 * - env:`export K=V`(K 合法才发;值做单引号转义)
 * - workDir:`cd DIR`(DIR 做单引号转义)
 * - artifact:不产生命令(进 artifactPath)
 */
export function stepToCommandLines(step: StepBlock): string[] {
  switch (step.kind) {
    case 'command': {
      const raw = (step.command ?? '').replace(/\r/g, '')
      return raw.split('\n').filter((l) => l.trim() !== '')
    }
    case 'env': {
      const key = (step.envKey ?? '').trim()
      if (!key) return []
      return [`export ${key}=${shellQuote(step.envValue ?? '')}`]
    }
    case 'workDir': {
      const dir = (step.dir ?? '').trim()
      if (!dir) return []
      return [`cd ${shellQuote(dir)}`]
    }
    case 'artifact':
      return []
    default:
      return []
  }
}

/** 把有序步骤列表编译成可执行 config 片段(commands 多行 + artifactPath 多行)。 */
export function compileSteps(steps: readonly StepBlock[]): CompiledSteps {
  const cmdLines: string[] = []
  const artifacts: string[] = []
  for (const step of steps) {
    if (step.kind === 'artifact') {
      const p = (step.artifact ?? '').trim()
      if (p) artifacts.push(p)
      continue
    }
    cmdLines.push(...stepToCommandLines(step))
  }
  return {
    commands: cmdLines.join('\n'),
    artifactPath: artifacts.join('\n'),
  }
}

// ─── 反解析:config → steps ─────────────────────────────────────────────────────

const ENV_LINE = /^export\s+([A-Za-z_][A-Za-z0-9_]*)=(.*)$/
const CD_LINE = /^cd\s+(.+)$/

/**
 * 反单引号转义:把 shellQuote 产出的 '...'(内含 '\'' 续接)还原为原始字符串。
 * 逐字符状态机:引号内原样吃,引号外只认 `\'`(转义单引号)= 字面 `'`,其余在引号外的
 * 字符按字面吃(容错)。非单引号开头的 token 原样返回(有损但不丢字符)。
 */
export function shellUnquote(token: string): string {
  const t = token.trim()
  if (!t.startsWith("'")) return t
  let out = ''
  let i = 0
  let inQuote = false
  while (i < t.length) {
    const ch = t[i]
    if (inQuote) {
      if (ch === "'") {
        inQuote = false
        i += 1
      } else {
        out += ch
        i += 1
      }
      continue
    }
    // 引号外
    if (ch === "'") {
      inQuote = true
      i += 1
    } else if (ch === '\\' && i + 1 < t.length && t[i + 1] === "'") {
      // shellQuote 用 '\'' 表达一个字面单引号
      out += "'"
      i += 2
    } else {
      out += ch
      i += 1
    }
  }
  return out
}

/**
 * 把一行命令分类成步骤:
 * - `export K=V`  → env(值反转义)
 * - `cd DIR`      → workDir(DIR 反转义)
 * - 其它          → command(原样,有损归类不丢内容)
 */
export function lineToStep(line: string): StepBlock {
  const trimmed = line.trim()
  const env = ENV_LINE.exec(trimmed)
  if (env) {
    return { id: nextStepId(), kind: 'env', envKey: env[1], envValue: shellUnquote(env[2]) }
  }
  const cd = CD_LINE.exec(trimmed)
  if (cd) {
    return { id: nextStepId(), kind: 'workDir', dir: shellUnquote(cd[1]) }
  }
  return { id: nextStepId(), kind: 'command', command: line }
}

/**
 * 从 config 反解析出步骤列表。`commands` 逐行分类;`artifactPath` 每行还原成上传产物步骤
 * (追加在末尾,顺序无关)。空 → 空列表(由调用方决定是否兜底)。
 */
export function parseSteps(config: Record<string, string>): StepBlock[] {
  const steps: StepBlock[] = []
  const commands = config.commands ?? ''
  for (const line of commands.replace(/\r/g, '').split('\n')) {
    if (line.trim() === '') continue
    steps.push(lineToStep(line))
  }
  const artifactPath = config.artifactPath ?? ''
  for (const line of artifactPath.replace(/\r/g, '').split('\n')) {
    const p = line.trim()
    if (p) steps.push({ id: nextStepId(), kind: 'artifact', artifact: p })
  }
  return steps
}

/**
 * 判断该类型的 config 能否被步骤构建器无歧义地处理(模板节点用 commandTemplate/params,
 * 步骤构建器不覆盖那套渲染语义 → 让它们走原始视图,避免误编译丢数据)。
 */
export function configUsesTemplate(config: Record<string, string>): boolean {
  return Boolean((config.commandTemplate ?? '').trim() || (config.params ?? '').trim())
}

/** 步骤类型的展示元信息(标签 + accent token 名),供 UI 复用。 */
export const STEP_KIND_META: Record<StepKind, { label: string; accent: string }> = {
  command: { label: '运行命令', accent: 'primary' },
  env: { label: '设环境变量', accent: 'cyan' },
  workDir: { label: '切目录', accent: 'amber' },
  artifact: { label: '上传产物', accent: 'green' },
}
