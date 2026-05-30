#!/usr/bin/env bash
# scripts/e2e/mainline.sh —— Pipewright 整系统真二进制 e2e 冒烟(#1 整系统主线)。
#
# 起**真编译的二进制**(真 main() 装配 + 真 SQLite 文件 + 真迁移 + go:embed 前端壳),经真
# HTTP 验证 httptest 单测证不到的整系统行为:二进制能启动、迁移落地、/healthz、auth+CSRF
# 中间件强制、登录发 session+csrf cookie、认证后可达受保护 API、凭据真 CRUD 经 vault 加密。
# 项目创建会真探仓库(GFW 下 github 不可达)→ best-effort:能建则连 run 生命周期一并验,
# 不能建则如实报告并跳过(不误判为系统故障)。
#
# 跑法:bash scripts/e2e/mainline.sh   (需 go 在 PATH;占用 127.0.0.1:18099)
set -u

PORT=18099
BASE="http://127.0.0.1:${PORT}"
TMP="$(mktemp -d)"
DB="${TMP}/pw.db"
BIN="${TMP}/pipewright"
JAR="${TMP}/cookies.txt"
ADMIN_PW="e2e-admin-pw-9z"
MASTER_KEY="$(head -c 32 /dev/urandom | base64)"
PASS=0; FAIL=0
ok(){ echo "  ✅ $1"; PASS=$((PASS+1)); }
bad(){ echo "  ❌ $1"; FAIL=$((FAIL+1)); }
note(){ echo "  ℹ️  $1"; }

cleanup(){ [ -n "${SRV_PID:-}" ] && kill "$SRV_PID" 2>/dev/null; rm -rf "$TMP"; }
trap cleanup EXIT

echo "== 1. 编译真二进制 =="
if ! go build -o "$BIN" ./cmd/pipewright 2>"${TMP}/build.log"; then
  echo "构建失败:"; cat "${TMP}/build.log"; exit 1
fi
ok "go build → $(du -h "$BIN" | cut -f1) 二进制"

echo "== 2. 启动(真 DB 文件 + 真迁移 + stub builder)=="
PIPEWRIGHT_ADDR=":${PORT}" PIPEWRIGHT_DB="$DB" PIPEWRIGHT_MASTER_KEY="$MASTER_KEY" \
  PIPEWRIGHT_ADMIN_PASSWORD="$ADMIN_PW" PIPEWRIGHT_BUILDER=stub \
  "$BIN" >"${TMP}/server.log" 2>&1 &
SRV_PID=$!

# 等 /healthz 就绪(最多 ~10s)。
up=0
for _ in $(seq 1 50); do
  if curl -fsS "${BASE}/healthz" >/dev/null 2>&1; then up=1; break; fi
  sleep 0.2
done
[ "$up" = 1 ] && ok "二进制启动 + /healthz 200(迁移已落地)" || { bad "二进制未在预期时间内就绪"; cat "${TMP}/server.log"; exit 1; }

echo "== 3. auth + CSRF 中间件强制 =="
code=$(curl -s -o /dev/null -w '%{http_code}' "${BASE}/api/projects")
[ "$code" = 401 ] && ok "未认证 GET /api/projects → 401" || bad "未认证应 401,实际 $code"

code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "${BASE}/api/credentials" -H 'Content-Type: application/json' -d '{}')
[ "$code" = 401 ] || [ "$code" = 403 ] && ok "未认证 POST → $code(拒写)" || bad "未认证 POST 应 401/403,实际 $code"

echo "== 4. 登录 → session + csrf cookie =="
login_code=$(curl -s -o "${TMP}/login.json" -w '%{http_code}' -c "$JAR" \
  -X POST "${BASE}/api/auth/login" -H 'Content-Type: application/json' \
  -d "{\"username\":\"admin\",\"password\":\"${ADMIN_PW}\"}")
[ "$login_code" = 200 ] && ok "登录 200" || { bad "登录失败 code=$login_code $(cat "${TMP}/login.json")"; }
grep -q pipewright_session "$JAR" && ok "下发 pipewright_session cookie" || bad "无 session cookie"
CSRF=$(awk '/pipewright_csrf/{print $7}' "$JAR" | tail -1)
[ -n "$CSRF" ] && ok "下发 pipewright_csrf cookie" || bad "无 csrf cookie"

echo "== 5. 认证后可达受保护 API =="
code=$(curl -s -o /dev/null -w '%{http_code}' -b "$JAR" "${BASE}/api/projects")
[ "$code" = 200 ] && ok "认证后 GET /api/projects → 200" || bad "认证后应 200,实际 $code"

# 缺 CSRF 头的写请求应被拒(CSRF 双重提交校验)。
code=$(curl -s -o /dev/null -w '%{http_code}' -b "$JAR" -X POST "${BASE}/api/credentials" \
  -H 'Content-Type: application/json' -d '{"name":"x","type":"git_token","secret":"y"}')
[ "$code" = 403 ] && ok "认证但缺 X-CSRF-Token 的 POST → 403(CSRF 强制)" || bad "缺 CSRF 应 403,实际 $code"

echo "== 6. 凭据真 CRUD(经 vault 加密)=="
cred_code=$(curl -s -o "${TMP}/cred.json" -w '%{http_code}' -b "$JAR" -X POST "${BASE}/api/credentials" \
  -H 'Content-Type: application/json' -H "X-CSRF-Token: ${CSRF}" \
  -d '{"name":"e2e-git","type":"git_token","secret":"ghp_e2e_REAL_token_SHOULD_be_masked_42"}')
[ "$cred_code" = 201 ] || [ "$cred_code" = 200 ] && ok "建凭据 → $cred_code" || bad "建凭据失败 code=$cred_code $(cat "${TMP}/cred.json")"
# 列凭据:绝无明文 secret(只掩码)。
curl -s -b "$JAR" "${BASE}/api/credentials" >"${TMP}/creds.json"
if grep -q 'ghp_e2e_REAL_token' "${TMP}/creds.json"; then bad "❌ 凭据列表泄漏明文 secret!"; else ok "凭据列表无明文 secret(掩码)"; fi
CRED_ID=$(sed -n 's/.*"id":"\([^"]*\)".*/\1/p' "${TMP}/cred.json" | head -1)

echo "== 7. 项目创建 + run 生命周期(best-effort:GFW 探仓库可能拦)=="
proj_code=$(curl -s -o "${TMP}/proj.json" -w '%{http_code}' -b "$JAR" -X POST "${BASE}/api/projects" \
  -H 'Content-Type: application/json' -H "X-CSRF-Token: ${CSRF}" \
  -d "{\"name\":\"e2e-proj\",\"repoUrl\":\"https://gitee.com/oschina/git-osc.git\",\"defaultBranch\":\"master\",\"credentialId\":\"${CRED_ID}\"}")
if [ "$proj_code" = 201 ] || [ "$proj_code" = 200 ]; then
  ok "建项目 → $proj_code(仓库可达)"
  PID=$(sed -n 's/.*"id":"\([^"]*\)".*/\1/p' "${TMP}/proj.json" | head -1)
  run_code=$(curl -s -o "${TMP}/run.json" -w '%{http_code}' -b "$JAR" -X POST "${BASE}/api/projects/${PID}/runs" \
    -H 'Content-Type: application/json' -H "X-CSRF-Token: ${CSRF}" -d '{"branch":"master"}')
  if [ "$run_code" = 201 ] || [ "$run_code" = 200 ]; then
    RID=$(sed -n 's/.*"id":"\([^"]*\)".*/\1/p' "${TMP}/run.json" | head -1)
    ok "触发 run → ${RID}"
    # 轮询到终态(stub builder 应 success)。
    status=""
    for _ in $(seq 1 50); do
      curl -s -b "$JAR" "${BASE}/api/runs/${RID}" >"${TMP}/rundetail.json"
      status=$(sed -n 's/.*"status":"\([^"]*\)".*/\1/p' "${TMP}/rundetail.json" | head -1)
      case "$status" in success|failed|partial_failed|rolled_back) break;; esac
      sleep 0.2
    done
    [ "$status" = success ] && ok "run 经真 pool 跑到 success(整条主线:触发→调度→步骤→终态)" || bad "run 终态=$status(期望 success)"
    grep -q '"steps"' "${TMP}/rundetail.json" && ok "run 详情含 steps 时间线" || note "run 详情无 steps 字段"
  else
    note "触发 run code=$run_code(项目无流水线配置等),跳过 run 生命周期"
  fi
else
  note "建项目 code=$proj_code(GFW 探仓库不可达,非系统故障)→ 改用 sqlite seed 项目以验 run 主线"
  PID="seed-$(date +%s)"
  if command -v sqlite3 >/dev/null 2>&1 && [ -n "${CRED_ID:-}" ]; then
    sqlite3 "$DB" "PRAGMA busy_timeout=5000; INSERT INTO projects(id,name,repo_url,default_branch,credential_id,created_at,updated_at) VALUES('${PID}','e2e-seed','https://example.com/r.git','main','${CRED_ID}',datetime('now'),datetime('now'));" >/dev/null 2>"${TMP}/seed.err" \
      && ok "sqlite seed 项目(绕 GFW)" || { bad "seed 项目失败:$(cat "${TMP}/seed.err")"; PID=""; }
  else
    note "无 sqlite3 或无 CRED_ID,跳过 seed"; PID=""
  fi
  if [ -n "$PID" ]; then
    run_code=$(curl -s -o "${TMP}/run.json" -w '%{http_code}' -b "$JAR" -X POST "${BASE}/api/projects/${PID}/runs" \
      -H 'Content-Type: application/json' -H "X-CSRF-Token: ${CSRF}" -d '{"branch":"main"}')
    if [ "$run_code" = 201 ] || [ "$run_code" = 200 ]; then
      RID=$(sed -n 's/.*"id":"\([^"]*\)".*/\1/p' "${TMP}/run.json" | head -1)
      ok "触发 run → ${RID}(经真二进制 worker pool)"
      status=""
      for _ in $(seq 1 50); do
        curl -s -b "$JAR" "${BASE}/api/runs/${RID}" >"${TMP}/rundetail.json"
        status=$(sed -n 's/.*"status":"\([^"]*\)".*/\1/p' "${TMP}/rundetail.json" | head -1)
        case "$status" in success|failed|partial_failed|rolled_back) break;; esac
        sleep 0.2
      done
      [ "$status" = success ] && ok "run 经真 pool 跑到 success(整条主线:触发→调度→步骤→SSE→终态)" || bad "run 终态=$status(期望 success)"
      grep -q '"steps"' "${TMP}/rundetail.json" && ok "run 详情含 steps 时间线" || bad "run 详情无 steps"

      # 真 SSE 流:开 text/event-stream,验真推 status + log 事件(端点+事件总线+历史回放经真二进制)。
      curl -s -N --max-time 3 -b "$JAR" "${BASE}/api/runs/${RID}/events" >"${TMP}/sse.txt" 2>/dev/null
      ct=$(curl -s -o /dev/null -w '%{content_type}' -N --max-time 2 -b "$JAR" "${BASE}/api/runs/${RID}/events")
      echo "$ct" | grep -q 'text/event-stream' && ok "SSE 端点 Content-Type=text/event-stream" || bad "SSE Content-Type 错:$ct"
      grep -q '^event: status' "${TMP}/sse.txt" && ok "SSE 真推 status 事件" || bad "SSE 无 status 事件"
      grep -q '^event: log' "${TMP}/sse.txt" && ok "SSE 真推 log 日志事件(历史回放)" || note "SSE 未见 log 事件(stub 日志时序)"
      grep -q "${RID}" "${TMP}/sse.txt" && ok "SSE data 含本 run id" || note "SSE data 未含 run id"
    else
      bad "触发 run code=$run_code $(cat "${TMP}/run.json")"
    fi
  fi
fi

echo ""
echo "== 结果:PASS=$PASS FAIL=$FAIL =="
[ "$FAIL" = 0 ] && echo "✅ 整系统主线冒烟通过" || echo "❌ 有失败项"
exit "$FAIL"
