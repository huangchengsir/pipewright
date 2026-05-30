#!/bin/bash
# Hook (PreToolUse:Bash): Playwright fail blocker
#
# 跟 playwright-timeout-detector.sh (post-hook) 配合:
#   - post-hook 检测 Playwright 失败 → 写 /tmp/.claude_playwright_failed marker
#   - pre-hook (本脚本) 检测 marker 存在 + 当前命令是 Playwright → exit 2 阻断
#   - 自动 clear:跑了 browser-use state / 手动 rm marker
#
# 设计目标:把 AGENTS.md「失败 1 次必须切 browser-use」从自觉变成 hook 强制规则。
#
# 输入: JSON via stdin (PreToolUse 协议)
#   tool_name + tool_input.command

set -e

INPUT=$(cat)
MARKER_FILE="/tmp/.claude_playwright_failed"
MAX_AGE_SECONDS=1800  # 30min 后 marker 失效 (避免误阻断)

TOOL_NAME=$(echo "$INPUT" | python3 -c "import sys, json; d=json.load(sys.stdin); print(d.get('tool_name', ''))" 2>/dev/null || echo "")
if [ "$TOOL_NAME" != "Bash" ]; then
  exit 0
fi

COMMAND=$(echo "$INPUT" | python3 -c "import sys, json; d=json.load(sys.stdin); print(d.get('tool_input', {}).get('command', ''))" 2>/dev/null || echo "")

# 自动清除:跑 browser-use state / inspect spec / 手动 rm marker → 视为已切换思路
if echo "$COMMAND" | grep -qE "browser-use.*state|browser-use.*screenshot|browser-use.*eval|playwright.*test.*debug|DEBUG_inspect|DEBUG_dump"; then
  rm -f "$MARKER_FILE"
  exit 0
fi

# 没 marker → 放行
if [ ! -f "$MARKER_FILE" ]; then
  exit 0
fi

# marker 过期 → 自动清理 + 放行
MARKER_TS=$(grep "^TIMESTAMP=" "$MARKER_FILE" 2>/dev/null | head -1 | cut -d= -f2 || echo "0")
NOW=$(date +%s)
AGE=$((NOW - MARKER_TS))
if [ "$AGE" -gt "$MAX_AGE_SECONDS" ]; then
  rm -f "$MARKER_FILE"
  exit 0
fi

# 命中 Playwright test/run 调用 → 阻断
# 必须是 playwright + test/run 子命令,不能光匹配 "playwright" 否则 show-trace/init-agents/playwright.config.ts 全被误阻断
# 也匹配 .spec.ts 文件路径 (test 子命令省略时的 shortcut),以及 run-test-mcp-server (MCP 走的 path)
if echo "$COMMAND" | grep -qE "(^|[[:space:]])(npx[[:space:]]+)?playwright[[:space:]]+(test|run)([[:space:]]|$)|(^|[[:space:]])\S+\.spec\.ts([[:space:]]|$)|run-test-mcp-server|--reporter[= ]"; then
  PREV_CMD=$(grep "^COMMAND=" "$MARKER_FILE" | cut -d= -f2- | head -c 200)
  PREV_ERR=$(grep "^ERROR=" "$MARKER_FILE" | cut -d= -f2- | head -c 300)
  cat >&2 <<EOF
╔══════════════════════════════════════════════════════════════════════╗
║ ⛔ Playwright 调用被阻断 — 上次失败后未切 browser-use 排查           ║
╠══════════════════════════════════════════════════════════════════════╣
║                                                                      ║
║  上一次失败的命令:                                                    ║
║  ${PREV_CMD}
║                                                                      ║
║  错误片段:                                                            ║
║  ${PREV_ERR}
║                                                                      ║
║  规则: AGENTS.md 硬触发器 (b) — Playwright 失败 1 次,必须先切          ║
║         browser-use 看真实 DOM,禁止改 selector 重跑                  ║
║                                                                      ║
║  解锁方式 (任选其一):                                                  ║
║    A. browser-use --cdp-url http://127.0.0.1:9222 state              ║
║       (跑了 state/screenshot/eval 后 marker 自动清除)                 ║
║    B. 跑 DEBUG_inspect_*.spec.ts 或 DEBUG_dump_*.spec.ts 排查         ║
║    C. 确认已切换思路,手动 rm /tmp/.claude_playwright_failed           ║
║                                                                      ║
║  marker 30 分钟后自动失效。                                            ║
║                                                                      ║
╚══════════════════════════════════════════════════════════════════════╝
EOF
  exit 2
fi

exit 0
