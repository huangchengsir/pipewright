#!/usr/bin/env bash
# scripts/e2e/fullstack.sh —— 真·全栈 e2e 编排(#真前端触发、打真后端)。
#
# 构建真前端 → 编译进真二进制(go:embed)→ 起真后端 → seed 一个项目 → 用 Playwright(真
# chromium)对真二进制**不 mock** 地跑 web/e2e-real/*.spec.ts:真 UI 登录 → 从 UI 手动触发运行
# → 真 worker pool 执行 + 真 SSE 推回 → UI 上断言「成功」。跑完清理。
#
# 需要:go、node(用 homebrew v18.19+,这里强制 /opt/homebrew/bin 优先)、Playwright chromium(已缓存)。
# 跑法:bash scripts/e2e/fullstack.sh
set -u
export PATH="/opt/homebrew/bin:/usr/local/bin:${HOME}/sdk/go/bin:${PATH}"

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"
PORT=18088
BASE="http://127.0.0.1:${PORT}"
TMP="$(mktemp -d)"
DB="${TMP}/pw.db"
BIN="${TMP}/pipewright"
JAR="${TMP}/jar.txt"
ADMIN_PW="fs-e2e-admin-9z"
MASTER_KEY="$(head -c 32 /dev/urandom | base64)"

cleanup(){ [ -n "${SRV_PID:-}" ] && kill "$SRV_PID" 2>/dev/null; rm -rf "$TMP"; }
trap cleanup EXIT

echo "== 1. 构建真前端(go:embed)+ 真二进制 =="
( cd web && npm run build >/dev/null 2>&1 ) || { echo "前端构建失败"; exit 1; }
go build -o "$BIN" ./cmd/pipewright || { echo "二进制构建失败"; exit 1; }
echo "  ✅ 二进制 $(du -h "$BIN" | cut -f1)(含真前端 SPA)"

echo "== 2. 起真后端 =="
PIPEWRIGHT_ADDR=":${PORT}" PIPEWRIGHT_DB="$DB" PIPEWRIGHT_MASTER_KEY="$MASTER_KEY" \
  PIPEWRIGHT_ADMIN_PASSWORD="$ADMIN_PW" PIPEWRIGHT_BUILDER=stub \
  "$BIN" >"${TMP}/server.log" 2>&1 &
SRV_PID=$!
up=0; for _ in $(seq 1 50); do curl -fsS "${BASE}/healthz" >/dev/null 2>&1 && { up=1; break; }; sleep 0.2; done
[ "$up" = 1 ] || { echo "二进制未就绪"; cat "${TMP}/server.log"; exit 1; }
echo "  ✅ 真后端就绪 + 服务真 SPA"

echo "== 3. seed 一个项目(绕 GFW 探仓库;时间戳用 RFC3339 否则列表 500)=="
curl -fsS -c "$JAR" -X POST "${BASE}/api/auth/login" -H 'Content-Type: application/json' \
  -d "{\"username\":\"admin\",\"password\":\"${ADMIN_PW}\"}" >/dev/null || { echo "登录失败"; exit 1; }
CSRF="$(awk '/pipewright_csrf/{print $7}' "$JAR" | tail -1)"
CRED="$(curl -fsS -b "$JAR" -X POST "${BASE}/api/credentials" -H 'Content-Type: application/json' \
  -H "X-CSRF-Token: ${CSRF}" -d '{"name":"fs-git","type":"git_token","secret":"ghp_fs_e2e_tok"}' \
  | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')"
[ -n "$CRED" ] || { echo "建凭据失败"; exit 1; }
sqlite3 "$DB" "PRAGMA busy_timeout=5000; INSERT INTO projects(id,name,repo_url,default_branch,credential_id,created_at,updated_at) VALUES('fs-proj','门户站 portal','https://example.com/portal.git','main','${CRED}',strftime('%Y-%m-%dT%H:%M:%SZ','now'),strftime('%Y-%m-%dT%H:%M:%SZ','now'));" >/dev/null \
  || { echo "seed 项目失败"; exit 1; }
echo "  ✅ 项目「门户站 portal」就绪"

echo "== 3b. seed 一个失败 run(给「触发诊断」旗舰流程;failure_log 含项目凭据明文以验脱敏)=="
NOW="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
FAILLOG="#8 ERROR: process \"/bin/sh -c pip install requests==99.99.99-x\" did not complete successfully: exit code 1' || char(10) || 'pip config set global.index-url https://ci:ghp_fs_e2e_tok@pypi.internal/simple"
sqlite3 "$DB" "PRAGMA busy_timeout=5000; INSERT INTO pipeline_runs(id,project_id,status,trigger_branch,created_at,started_at,finished_at,failure_log) VALUES('fs-failrun','fs-proj','failed','main','${NOW}','${NOW}','${NOW}','${FAILLOG}');" >/dev/null || { echo "seed 失败 run 失败"; exit 1; }
sqlite3 "$DB" "PRAGMA busy_timeout=5000; INSERT INTO run_steps(id,run_id,name,status,ordinal) VALUES('fs-s1','fs-failrun','拉取源码','success',0),('fs-s2','fs-failrun','构建','failed',1);" >/dev/null || true
echo "  ✅ 失败 run「fs-failrun」就绪"

# AI 配置(DeepSeek):仅当 DEEPSEEK_API_KEY 提供时,经真 API 配 → 启用 AI 诊断 e2e。key 不落库明文(vault 加密)。
PW_AI=0
if [ -n "${DEEPSEEK_API_KEY:-}" ]; then
  ai_code=$(curl -s -o /dev/null -w '%{http_code}' -b "$JAR" -X PUT "${BASE}/api/settings/ai" \
    -H 'Content-Type: application/json' -H "X-CSRF-Token: ${CSRF}" \
    -d "{\"provider\":\"openai\",\"baseUrl\":\"https://api.deepseek.com\",\"model\":\"deepseek-chat\",\"apiKey\":\"${DEEPSEEK_API_KEY}\",\"enabled\":true}")
  [ "$ai_code" = 200 ] && { PW_AI=1; echo "  ✅ AI=DeepSeek 已配(启用诊断 e2e)"; } || echo "  ⚠️ AI 配置返回 $ai_code,跳过诊断 e2e"
else
  echo "  ℹ️ 未设 DEEPSEEK_API_KEY → 跳过 AI 诊断 e2e(其余照跑)"
fi

echo "== 4. Playwright 真 UI 打真后端(不 mock)=="
( cd web && PW_BASE_URL="$BASE" PW_ADMIN_PW="$ADMIN_PW" PW_AI="$PW_AI" npx playwright test --config playwright.real.config.ts )
RC=$?

echo ""
[ "$RC" = 0 ] && echo "✅ 全栈真 e2e 通过(真前端触发 → 真后端 → 真 SSE → UI 断言)" || echo "❌ 全栈真 e2e 失败(见上)"
exit "$RC"
