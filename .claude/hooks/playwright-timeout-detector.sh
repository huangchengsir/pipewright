#!/bin/bash
# Hook (PostToolUse:Bash): Playwright timeout detector
# 检测 Playwright spec/test 失败,在 /tmp 写一个 marker 文件供 pre-hook 阻断后续猜测调用。
#
# 与 playwright-fail-blocker.sh (PreToolUse) 配合,实现「失败 1 次必须切 browser-use」硬规则。
#
# 输入: JSON via stdin (hook 协议)
# 字段: tool_response.output 是 Bash 命令合并的 stdout+stderr

set -e

INPUT=$(cat)
MARKER_FILE="/tmp/.claude_playwright_failed"

TOOL_NAME=$(echo "$INPUT" | python3 -c "import sys, json; d=json.load(sys.stdin); print(d.get('tool_name', ''))" 2>/dev/null || echo "")
if [ "$TOOL_NAME" != "Bash" ]; then
  exit 0
fi

OUTPUT=$(echo "$INPUT" | python3 -c "import sys, json; d=json.load(sys.stdin); print(d.get('tool_response', {}).get('output', ''))" 2>/dev/null || echo "")
COMMAND=$(echo "$INPUT" | python3 -c "import sys, json; d=json.load(sys.stdin); print(d.get('tool_input', {}).get('command', ''))" 2>/dev/null || echo "")

# 命中 Playwright 失败模式
if echo "$OUTPUT" | grep -qE "TimeoutError.*locator|Timeout.*exceeded.*waiting for locator|element\(s\) not found|element is not visible|intercepts pointer events|locator\.click: Timeout"; then
  # 写 marker (Unix epoch + 命令 + 错误摘要)
  TS=$(date +%s)
  ERR_SNIPPET=$(echo "$OUTPUT" | grep -oE "TimeoutError.*|element.*not found|element is not visible" | head -3 | tr '\n' '|' | head -c 500)
  cat > "$MARKER_FILE" <<EOF
TIMESTAMP=$TS
COMMAND=$(echo "$COMMAND" | head -c 300)
ERROR=$ERR_SNIPPET
EOF
  cat >&2 <<'EOF'
╔══════════════════════════════════════════════════════════════════╗
║ ⛔ Playwright 失败 — AGENTS.md 硬触发器 (b) 命中                 ║
╠══════════════════════════════════════════════════════════════════╣
║                                                                  ║
║  下次 npx playwright 调用将被阻断,直到你做以下任一操作:           ║
║    A. 跑一次 browser-use state 看真实 DOM (推荐)                  ║
║       browser-use --cdp-url http://127.0.0.1:9222 state          ║
║    B. 手动清除 marker (确认已切换思路,不是猜 selector 重跑)        ║
║       rm /tmp/.claude_playwright_failed                          ║
║                                                                  ║
║  禁止: 改 selector 直接重跑 spec                                  ║
║  推荐: 看完 state → 修 helper → 再跑 spec                        ║
║                                                                  ║
║  完整 SOP: AGENTS.md「硬性触发器」节                              ║
║                                                                  ║
╚══════════════════════════════════════════════════════════════════╝
EOF
fi

exit 0
