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

# 容器 / 接收器资源(B/C/D);唯一命名,cleanup 兜底,绝不留垃圾。
CONTAINERS=()           # 起的 sshd 容器名
NOTIFY_PID=""           # 本地 HTTP 接收器 PID
DOCKER_BIN=""           # 探测到的 docker 可执行(空=无 docker)
CENTOS_IMG="pw-e2e-centos-ssh:test"

cleanup(){
  [ -n "${SRV_PID:-}" ] && kill "$SRV_PID" 2>/dev/null
  [ -n "${NOTIFY_PID:-}" ] && kill "$NOTIFY_PID" 2>/dev/null
  if [ -n "$DOCKER_BIN" ]; then
    for c in "${CONTAINERS[@]:-}"; do [ -n "$c" ] && "$DOCKER_BIN" rm -f "$c" >/dev/null 2>&1; done
  fi
  rm -rf "$TMP"
}
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

# ─── 容器 e2e(B 服务器 / C 多机部署 / D 通知):可控开关,无 docker 自动降级 ────────────
# 探测 docker:无则跳过 B/C 的容器部分(D 的本地接收器仍可起,不依赖容器)。
PW_SERVERS=0; PW_SSH_PORT=""; PW_SSH_CRED=""
PW_DEPLOY=0;  PW_DEPLOY_RUN="fs-deployrun"
PW_NOTIFY=0;  PW_NOTIFY_URL=""

for p in /usr/local/bin/docker /opt/homebrew/bin/docker /usr/bin/docker; do
  [ -x "$p" ] && { DOCKER_BIN="$p"; break; }
done
[ -z "$DOCKER_BIN" ] && command -v docker >/dev/null 2>&1 && DOCKER_BIN="$(command -v docker)"
if [ -n "$DOCKER_BIN" ] && ! "$DOCKER_BIN" info >/dev/null 2>&1; then
  echo "  ⚠️ docker 守护未就绪 → 跳过容器 e2e(B/C)"; DOCKER_BIN=""
fi

# wait_sshd <host> <port> <priv-key-file>:真 SSH 握手轮询直至可认证登录(root 免密)。
wait_sshd(){
  local host="$1" port="$2" key="$3" i
  for i in $(seq 1 60); do
    if ssh -i "$key" -p "$port" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
         -o ConnectTimeout=3 -o BatchMode=yes -o LogLevel=ERROR \
         "root@${host}" 'uname -a' >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  return 1
}

if [ -n "$DOCKER_BIN" ]; then
  echo "== 5. 起真 CentOS+sshd 容器(B/C 共享:真 SSH 目标)=="
  # 仅测试用 SSH key 对:私钥经 API 存 vault(ssh_key 凭据),公钥注入容器 authorized_keys。
  KEYDIR="${TMP}/ssh"; mkdir -p "$KEYDIR"
  ssh-keygen -t ed25519 -N '' -C 'pw-e2e' -f "${KEYDIR}/id" >/dev/null 2>&1
  PUBKEY="$(cat "${KEYDIR}/id.pub")"

  # 预装 sshd 的 CentOS 镜像(已存则复用;否则现 build)。
  if ! "$DOCKER_BIN" image inspect "$CENTOS_IMG" >/dev/null 2>&1; then
    echo "  · 构建 $CENTOS_IMG(首次,约 1~2 分钟)…"
    "$DOCKER_BIN" build -t "$CENTOS_IMG" -f "${TMP}/centos.dockerfile" - <<'DF' >/dev/null 2>&1 || true
FROM quay.io/centos/centos:stream9
RUN dnf install -y openssh-server openssh-clients >/dev/null 2>&1 && ssh-keygen -A && \
    sed -i 's/^#\?PermitRootLogin.*/PermitRootLogin yes/' /etc/ssh/sshd_config && \
    sed -i 's/^#\?PubkeyAuthentication.*/PubkeyAuthentication yes/' /etc/ssh/sshd_config && \
    mkdir -p /root/.ssh && chmod 700 /root/.ssh
CMD ["/usr/sbin/sshd","-D","-e"]
DF
  fi
  if ! "$DOCKER_BIN" image inspect "$CENTOS_IMG" >/dev/null 2>&1; then
    echo "  ⚠️ CentOS sshd 镜像不可用(网络/构建失败)→ 跳过容器 e2e(B/C)"; DOCKER_BIN=""
  fi
fi

if [ -n "$DOCKER_BIN" ]; then
  # 起 3 台容器(C 多机用 3 台;B 复用第 1 台);注入 authorized_keys 后起 sshd。
  STAMP="$(date +%s)"
  declare -a SSH_PORTS=()
  for n in 1 2 3; do
    cname="pw-fs-ssh-${STAMP}-${n}-$$"
    "$DOCKER_BIN" rm -f "$cname" >/dev/null 2>&1 || true
    cid=$("$DOCKER_BIN" run -d --name "$cname" -p 0:22 "$CENTOS_IMG" \
            sh -c "printf '%s\n' '${PUBKEY}' > /root/.ssh/authorized_keys && chmod 600 /root/.ssh/authorized_keys && exec /usr/sbin/sshd -D -e" 2>/dev/null) || true
    if [ -z "$cid" ]; then echo "  ⚠️ 容器 $n 启动失败 → 降级"; DOCKER_BIN=""; break; fi
    CONTAINERS+=("$cname")
    pline=$("$DOCKER_BIN" port "$cname" 22/tcp 2>/dev/null | head -1)
    port="${pline##*:}"
    [ -z "$port" ] && { echo "  ⚠️ 容器 $n 取端口失败 → 降级"; DOCKER_BIN=""; break; }
    SSH_PORTS+=("$port")
  done
fi

if [ -n "$DOCKER_BIN" ] && [ "${#SSH_PORTS[@]}" -ge 2 ]; then
  # 等首台 sshd 就绪(dnf 镜像已预装,启动快;给足 60s)。
  if wait_sshd 127.0.0.1 "${SSH_PORTS[0]}" "${KEYDIR}/id"; then
    # vault 存 ssh_key 凭据(私钥 PEM 经 secret 加密落库;B 的 spec 经 UI 选它)。
    SSH_CRED_NAME="fs-ssh-key"
    PRIV_JSON="$(python3 -c 'import json,sys; print(json.dumps(open(sys.argv[1]).read()))' "${KEYDIR}/id")"
    cred_resp=$(curl -s -b "$JAR" -X POST "${BASE}/api/credentials" -H 'Content-Type: application/json' \
      -H "X-CSRF-Token: ${CSRF}" \
      -d "{\"name\":\"${SSH_CRED_NAME}\",\"type\":\"ssh_key\",\"scope\":\"prod\",\"secret\":${PRIV_JSON}}")
    SSH_CRED_ID="$(printf '%s' "$cred_resp" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')"
    if [ -n "$SSH_CRED_ID" ]; then
      PW_SERVERS=1; PW_SSH_PORT="${SSH_PORTS[0]}"; PW_SSH_CRED="$SSH_CRED_NAME"
      echo "  ✅ SSH 凭据「${SSH_CRED_NAME}」就绪;B 容器端口 ${SSH_PORTS[0]}"

      # C 多机部署:把全部容器经 API 登记为 server + seed 成功 run + dist 产物。
      sids=()
      idx=0
      for port in "${SSH_PORTS[@]}"; do
        idx=$((idx+1))
        sresp=$(curl -s -b "$JAR" -X POST "${BASE}/api/servers" -H 'Content-Type: application/json' \
          -H "X-CSRF-Token: ${CSRF}" \
          -d "{\"name\":\"deploy-node-${idx}\",\"host\":\"127.0.0.1\",\"port\":${port},\"user\":\"root\",\"credentialId\":\"${SSH_CRED_ID}\"}")
        sid="$(printf '%s' "$sresp" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')"
        [ -n "$sid" ] && sids+=("$sid")
      done
      if [ "${#sids[@]}" -ge 2 ]; then
        DNOW="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
        sqlite3 "$DB" "PRAGMA busy_timeout=5000; INSERT INTO pipeline_runs(id,project_id,status,trigger_branch,created_at,started_at,finished_at) VALUES('${PW_DEPLOY_RUN}','fs-proj','success','main','${DNOW}','${DNOW}','${DNOW}');" >/dev/null \
          && sqlite3 "$DB" "PRAGMA busy_timeout=5000; INSERT INTO run_steps(id,run_id,name,status,ordinal,started_at,finished_at) VALUES('fs-ds1','${PW_DEPLOY_RUN}','拉取源码','success',0,'${DNOW}','${DNOW}'),('fs-ds2','${PW_DEPLOY_RUN}','构建','success',1,'${DNOW}','${DNOW}');" >/dev/null \
          && sqlite3 "$DB" "PRAGMA busy_timeout=5000; INSERT INTO run_artifacts(id,run_id,type,name,reference,size_bytes,metadata_json,created_at) VALUES('fs-art1','${PW_DEPLOY_RUN}','dist','门户站前端','dist/portal-1.0.0.tar.gz',2048,'{\"stub\":true}','${DNOW}');" >/dev/null \
          && { PW_DEPLOY=1; echo "  ✅ C 多机部署就绪:${#sids[@]} 台目标 + 成功 run「${PW_DEPLOY_RUN}」+ dist 产物"; } \
          || echo "  ⚠️ seed 部署 run/产物失败 → 跳过 C"
      else
        echo "  ⚠️ 登记目标服务器不足 2 台 → 跳过 C"
      fi
    else
      echo "  ⚠️ 存 SSH 凭据失败($cred_resp)→ 跳过 B/C"
    fi
  else
    echo "  ⚠️ 容器 sshd 未就绪(超时)→ 跳过 B/C"
  fi
else
  echo "  ℹ️ 无 docker 或容器不足 → 跳过 B(服务器)/C(多机部署),A/D 不受影响"
fi

# ─── D 通知:本地 HTTP 接收器(记录请求,回 200)→ webhook 渠道指向它 ─────────────────
echo "== 6. 起本地 webhook 接收器(D 通知)=="
NOTIFY_PORT=18099
python3 -c "
import http.server, socketserver, sys
class H(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        ln = int(self.headers.get('Content-Length', 0) or 0)
        self.rfile.read(ln)
        self.send_response(200); self.send_header('Content-Type','application/json')
        self.end_headers(); self.wfile.write(b'{\"ok\":true}')
    def do_GET(self):
        self.send_response(200); self.end_headers(); self.wfile.write(b'ok')
    def log_message(self, *a): pass
with socketserver.TCPServer(('127.0.0.1', ${NOTIFY_PORT}), H) as s:
    s.serve_forever()
" >/dev/null 2>&1 &
NOTIFY_PID=$!
nok=0; for _ in $(seq 1 30); do curl -fsS "http://127.0.0.1:${NOTIFY_PORT}/" >/dev/null 2>&1 && { nok=1; break; }; sleep 0.2; done
if [ "$nok" = 1 ]; then
  PW_NOTIFY=1; PW_NOTIFY_URL="http://127.0.0.1:${NOTIFY_PORT}/hook"
  echo "  ✅ 接收器就绪 → ${PW_NOTIFY_URL}"
else
  echo "  ⚠️ 接收器未就绪 → 跳过 D"
fi

echo "== 7. Playwright 真 UI 打真后端(不 mock;A 始终跑,B/C/D 视环境)=="
( cd web && \
  PW_BASE_URL="$BASE" PW_ADMIN_PW="$ADMIN_PW" PW_AI="$PW_AI" \
  PW_SERVERS="$PW_SERVERS" PW_SSH_PORT="$PW_SSH_PORT" PW_SSH_CRED="$PW_SSH_CRED" \
  PW_DEPLOY="$PW_DEPLOY" PW_DEPLOY_RUN="$PW_DEPLOY_RUN" \
  PW_NOTIFY="$PW_NOTIFY" PW_NOTIFY_URL="$PW_NOTIFY_URL" \
  npx playwright test --config playwright.real.config.ts )
RC=$?

echo ""
[ "$RC" = 0 ] && echo "✅ 全栈真 e2e 通过(真前端触发 → 真后端 → 真 SSE → UI 断言)" || echo "❌ 全栈真 e2e 失败(见上)"
exit "$RC"
