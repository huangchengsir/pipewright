#!/bin/bash
# Hook (PreToolUse:Bash): browser-use 裸调提示
#
# 检测 browser-use click / input / type / scroll / keys 操作 (非 state / screenshot / eval),
# 如果不是通过 scripts/tools/browser_use_recorder.js 包装,提示用 /explore skill。
#
# 不阻断 (exit 0),只 stderr 提示。
#
# 输入: JSON via stdin (PreToolUse 协议)

set -e

INPUT=$(cat)

TOOL_NAME=$(echo "$INPUT" | python3 -c "import sys, json; d=json.load(sys.stdin); print(d.get('tool_name', ''))" 2>/dev/null || echo "")
if [ "$TOOL_NAME" != "Bash" ]; then
  exit 0
fi

COMMAND=$(echo "$INPUT" | python3 -c "import sys, json; d=json.load(sys.stdin); print(d.get('tool_input', {}).get('command', ''))" 2>/dev/null || echo "")

# 已通过 recorder 包装 → 放行
if echo "$COMMAND" | grep -q "browser_use_recorder\|run_explore_bridge"; then
  exit 0
fi

# 只读类操作 (state/screenshot/eval/get/extract) → 放行 (排查/dump 用,不需要记录)
if echo "$COMMAND" | grep -qE "browser-use[[:space:]]+([^|]*[[:space:]])?(state|screenshot|eval|get|extract|close|--help)"; then
  exit 0
fi

# 操作类 (click/input/type/scroll/keys/open/back/wait) 没走 recorder → 提示
if echo "$COMMAND" | grep -qE "browser-use[[:space:]]+([^|]*[[:space:]])?(click|input|type|scroll|keys|open|back|wait)"; then
  cat >&2 <<'EOF'
╔══════════════════════════════════════════════════════════════════════╗
║ ⚠️  裸调 browser-use 操作命令,建议改用 /explore                       ║
╠══════════════════════════════════════════════════════════════════════╣
║                                                                      ║
║  当前命令是 browser-use 操作类 (click/input/type/scroll/keys),       ║
║  没通过 recorder 包装 — 探索证据不会沉淀,draft spec 不会自动生成。     ║
║                                                                      ║
║  推荐替换为:                                                          ║
║    node scripts/tools/browser_use_recorder.js \\                     ║
║      --run-id <RUN_ID> --case-id <CASE_ID> \\                        ║
║      --headed --cdp-url http://127.0.0.1:9222 <subcommand> ...       ║
║                                                                      ║
║  或一条命令完整探索:                                                   ║
║    node scripts/tools/run_explore_bridge.js \\                       ║
║      --system <system> --goal "<...>" --run-id <...> --case-id <...> ║
║                                                                      ║
║  完整 SOP: skills/explore.md                                          ║
║                                                                      ║
║  排查/dump 场景 (state/screenshot/eval/get) 可以裸调,本提示自动豁免。 ║
║                                                                      ║
╚══════════════════════════════════════════════════════════════════════╝
EOF
fi

exit 0
