/**
 * 把一条 `docker run ...` 命令解析为新增容器表单的结构化字段。
 * 用于「AI 生成配置」:AI 给出 docker run 命令 → 自动填表。尽力解析,无法识别的 flag 跳过。
 */
import type { RestartPolicy } from '../api/containers'

export interface ParsedRun {
  image: string
  name: string
  ports: string[]
  env: string[]
  volumes: string[]
  restart?: RestartPolicy
  command: string
}

/** 不带值的布尔 flag(出现即开,不吞下一个 token)。 */
const BOOL_FLAGS = new Set([
  '-d', '--detach', '--rm', '-i', '-t', '-it', '-ti', '--privileged', '--init',
  '--read-only', '-q', '--quiet', '--no-healthcheck', '--interactive', '--tty',
])

const RESTART_VALUES = new Set<RestartPolicy>(['no', 'always', 'unless-stopped', 'on-failure'])

/** 按空白分词,尊重单/双引号。 */
function tokenize(s: string): string[] {
  const out: string[] = []
  let cur = ''
  let quote = ''
  for (const ch of s) {
    if (quote) {
      if (ch === quote) quote = ''
      else cur += ch
    } else if (ch === '"' || ch === "'") {
      quote = ch
    } else if (/\s/.test(ch)) {
      if (cur) {
        out.push(cur)
        cur = ''
      }
    } else {
      cur += ch
    }
  }
  if (cur) out.push(cur)
  return out
}

/** 取一个 flag 的值:支持 `--name x` 与 `--name=x` 两种写法。 */
function flagValue(tok: string, next: string | undefined): { value: string; consumedNext: boolean } {
  const eq = tok.indexOf('=')
  if (eq >= 0) return { value: tok.slice(eq + 1), consumedNext: false }
  return { value: next ?? '', consumedNext: true }
}

/**
 * 解析 docker run 命令。返回 null 表示不是可识别的 docker run。
 */
export function parseDockerRun(cmd: string): ParsedRun | null {
  const toks = tokenize(cmd.trim())
  // 跳到 "docker run" 之后(容忍前面有 sudo)。
  let i = 0
  while (i < toks.length && toks[i] !== 'docker') i++
  if (i + 1 >= toks.length || toks[i] !== 'docker' || toks[i + 1] !== 'run') return null
  i += 2

  const res: ParsedRun = { image: '', name: '', ports: [], env: [], volumes: [], command: '' }

  // 解析 flags 直到遇到第一个非 flag(= 镜像)。
  while (i < toks.length) {
    const t = toks[i]
    if (!t.startsWith('-')) break // 镜像
    const key = t.includes('=') ? t.slice(0, t.indexOf('=')) : t
    if (BOOL_FLAGS.has(key)) {
      i++
      continue
    }
    const { value, consumedNext } = flagValue(t, toks[i + 1])
    switch (key) {
      case '--name':
        res.name = value
        break
      case '-p':
      case '--publish':
        if (value) res.ports.push(value)
        break
      case '-e':
      case '--env':
        if (value) res.env.push(value)
        break
      case '-v':
      case '--volume':
        if (value) res.volumes.push(value)
        break
      case '--restart':
        if (RESTART_VALUES.has(value as RestartPolicy)) res.restart = value as RestartPolicy
        break
      default:
        break // 其它 flag(--network/-w 等)暂不映射,但仍吞掉其值避免误判为镜像
    }
    i += consumedNext ? 2 : 1
  }

  if (i >= toks.length) return null // 没有镜像
  res.image = toks[i]
  i++
  res.command = toks.slice(i).join(' ')
  return res
}
