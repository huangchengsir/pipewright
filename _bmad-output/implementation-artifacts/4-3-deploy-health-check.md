# Story 4.3: 部署健康门控(FR-12 健康检查侧)

status: ready-for-dev
epic: 4
基线: master(24 story;4-2 SSH 部署执行 done,Deployer 经 target.Exec 部署 + deploy_targets 每机状态)
covers: 部署后健康检查 —— 部署完成后探测服务是否真正健康,不健康则该机 status=failed(为 4-4 零停机切换/回滚门控铺路)
**迁移号:无**(健康检查结果并入 4-2 既有 deploy_targets;配置经 deploy 请求传入)

## As a / I want / So that
As a 管理员,I want 部署到目标机后自动做健康检查(HTTP 探测或命令探测),只有探测通过才算该机部署成功,So that 部署不是"命令跑完就算成功",而是"服务真正起来了"。

## Acceptance Criteria
1. **Given** 部署配置含健康检查(HTTP url / 命令)**When** 部署命令执行成功后 **Then** 经 SSH 在目标机执行健康探测(HTTP:`curl -fsS <url>`;命令:跑给定 cmd 看退出码),支持重试 N 次 + 间隔 + 超时。
2. **Given** 健康探测通过 **When** 记录 **Then** 该机 status=success(message 含"健康检查通过")。
3. **And** 健康探测在重试耗尽后仍失败 → 该机 **status=failed** + message 人读("健康检查失败:…"),整体 run 据此置 partial_failed/failed(复用 4-2 的 overallStatus)。
4. **And** 未配置健康检查 → 跳过(部署命令成功即 success,向后兼容 4-2 行为);探测命令/输出绝无明文密钥;命令 array 化(AC-SEC-02)。

## ⚠️ 本机真验
- 4-2 已证 localhost 真部署 → 配 `healthCheck:{type:command, command:["true"]}` → 通过;`["false"]` → 失败重试耗尽→failed;HTTP type 指向本机起的 `python3 -m http.server`(curl 200)→通过,死端口→失败。全本机可验。

## ⛓ 冻结契约(扩展 4-2 deploy 请求;不改 targets 子 DTO 形状)
### deploy 请求扩展(4-2 的 `POST /api/runs/{id}/deploy` 的 `deployConfig` 加 healthCheck)
```json
"deployConfig": {
  "healthCheck": {
    "type": "http" | "command" | "none",
    "url": "http://localhost:8080/healthz",   // type=http
    "command": ["curl","-fsS","http://..."],   // type=command(array,AC-SEC-02)
    "retries": 5, "intervalSeconds": 3, "timeoutSeconds": 5
  }
}
```
- `type=none`/缺省 → 不做健康检查(4-2 行为)。`retries` 默认 3、`interval` 默认 3s、`timeout` 默认 5s(各有上限防滥用:retries≤20、timeout≤60s)。
- 健康检查**在部署命令成功之后**跑;每机独立;结果并入该机的 deploy_target.status(success/failed)+ message。**targets 子 DTO 形状不变**(4-2 冻结),仅 status/message 内容受健康结果影响。

> **冻结点:** healthCheck 配置子结构(type/url/command/retries/intervalSeconds/timeoutSeconds)定死;4-4 零停机切换在健康门控通过后才切流量、失败触发回滚,消费本结果不改形状。

## 文件切分(独立 worktree 完整 BE+FE)
**后端(Go):**
- `internal/deploy`(改,append 不破坏 4-2):加 `healthcheck.go`——`runHealthCheck(ctx, serverID, hc HealthCheck) error`(经注入的 target.Exec:http type 构造 `curl -fsS --max-time T <url>` array;command type 直接跑 cmd array;重试循环 + 间隔 + ctx 超时)。`Deployer.deployOne` 在部署命令成功后调用;失败 → 该机 failed + message。HealthCheck 模型 + 默认值/上限校验。
- `internal/httpapi/deploy.go`(改):deploy 请求 DTO 加 `healthCheck`(解析进 deployConfig 传给 Deployer)。
- **不改** target/run 的冻结签名;不新增路由(复用 4-2 的 deploy 端点)。
**前端(web/):**
- `web/src/api/runs.ts`(改):deployRun 请求加 healthCheck 可选字段类型。
- `web/src/components/run/DeployTargets.vue` 或部署入口(改):部署触发表单加「健康检查」配置(type 选 none/http/command + url/command + retries/interval/timeout);targets 卡 message 已展示健康结果(无需新组件)。
- 不动 4-2 的 targets 渲染 slot 所有权。

## Dev Notes 护栏
- **AC-SEC-02**:健康探测命令 array 化经 target.Exec,不拼 shell;输出/message 绝无明文密钥(curl 失败 stderr 截断纳入,无凭据)。
- 向后兼容:无 healthCheck → 完全等同 4-2(成功命令即 success)。retries/timeout 有上限防滥用 + DoS 目标机。
- 健康结果并入 deploy_target(不新增表/不改 targets DTO);为 4-4 回滚门控提供"健康/不健康"信号。
- 参数化 SQL(复用 4-2);无 init 副作用;mem ≤100MB。

## 编排者亲验清单
- `export PATH="/Users/huangjiawei/sdk/go/bin:$PATH"`;gofmt/vet/`go test -race ./internal/deploy/... ./internal/httpapi/...`/build/mem-check。
- 前端 typecheck(symlink node_modules)+ build。
- **真验:localhost 部署 + healthCheck command=["true"]→success;["false"]→重试耗尽 failed;http type 指本机 http.server→200 通过,死端口→failed**;无 healthCheck→等同 4-2;message 无密钥。
- worktree 内 commit(分支 story/4-3)。
