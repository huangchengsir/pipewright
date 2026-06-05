/**
 * AI ops-terminal assistant API (运维终端 P1 · AI moat).
 *
 * POST /api/ai/command  → CommandSuggestResponse   中文描述 → 命令卡
 * POST /api/ai/explain  → ExplainCommandResponse   解释一条命令 + 风险等级
 *
 * 两者总是返回 200:AI 未配/未启用 → `available=false` + 人读 `reason`(前端提示去设置配 AI);
 * LLM 调用/解析失败 → `available=true` + `reason`(命令/解释为空)。命令的最终 `risk` 由后端
 * 确定性规则复核(rm -rf / mkfs / dd / 关机重启等无条件 danger),前端据此拦截直接执行。
 */

import { http } from './http'

/** 命令风险等级(对齐命令卡左色条:safe=cyan / write=amber / danger=red)。 */
export type CommandRisk = 'safe' | 'write' | 'danger'

/** 终端会话上下文(全部可空;辅助生成更贴合的命令)。 */
export interface CommandContext {
  os?: string
  shell?: string
  container?: string
  cwd?: string
}

export interface CommandSuggestResponse {
  /** false = AI 未配置(前端提示去设置配);其余字段为空。 */
  available: boolean
  command: string
  explanation: string
  risk: CommandRisk
  /** 风险/注意点,或降级原因(人读;绝无密钥)。 */
  reason: string
  generatedAt: string
}

export interface ExplainCommandResponse {
  available: boolean
  explanation: string
  risk: CommandRisk
  reason: string
  generatedAt: string
}

export interface CompleteCommandResponse {
  /** false = AI 未配置(前端退化为本地常用命令字典兜底)。 */
  available: boolean
  /** 补全后的完整命令(以 partial 原样开头);无补全时为空。 */
  completion: string
}

/** 中文描述 → 单条命令 + 解释 + 风险等级。 */
export async function suggestCommand(
  nl: string,
  context: CommandContext = {},
): Promise<CommandSuggestResponse> {
  return http.post<CommandSuggestResponse>('/api/ai/command', { nl, context })
}

/** 解释一条命令(+ 确定性风险等级)。 */
export async function explainCommand(
  command: string,
  context: CommandContext = {},
): Promise<ExplainCommandResponse> {
  return http.post<ExplainCommandResponse>('/api/ai/explain', { command, context })
}

/** 据已输入前缀补全为完整命令(P2 智能补全;补全以前缀原样开头)。 */
export async function completeCommand(
  partial: string,
  context: CommandContext = {},
  signal?: AbortSignal,
): Promise<CompleteCommandResponse> {
  return http.post<CompleteCommandResponse>('/api/ai/complete', { partial, context }, { signal })
}
