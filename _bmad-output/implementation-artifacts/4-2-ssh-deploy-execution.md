# Story 4.2: SSH 部署执行(FR-10)

status: ready-for-dev
epic: 4
基线: master(21 story;4-1 `internal/target` SSH 层 done;3-4 产物契约 done;3-1 run-detail `targets` null slot 待填)
covers: FR-10 多产物部署执行(经 SSH)—— 把产物部署到目标服务器,**填 3-1 冻结的 run-detail `targets` slot**。本期:基础部署执行 + 每机结果记录。零停机切换/回滚=4-4、多机扇出=4-5。
**迁移号:0017**

## As a / I want / So that
As a 管理员,I want 把一次成功运行的产物经 SSH 部署到目标服务器并看到每台机的部署结果,So that 产物能真正上线,且部署过程可观察。

## Acceptance Criteria
1. **Given** 一个产物(type+reference)+ 目标服务器 **When** 触发部署 **Then** 经 `internal/target.Exec` 在目标机执行该产物类型的部署命令(**命令 array 化,AC-SEC-02**),记录每机结果(成功/失败 + 输出/错误)。
2. **Given** 部署执行 **When** 查看运行详情 **Then** 填 run-detail `targets` slot:每台目标机一张结果(serverId/status/message/起止时间)。
3. **And** 部署失败 → 该机 status=failed + 人读 message(不阻断其它机;多机扇出细节留 4-5);命令/输出绝无明文密钥。
4. **And** 无 Docker 的本机 → **dist 类型部署可真验**(传文件 + 跑一条命令到 localhost);image 类型部署命令构造正确即可(目标无 docker 则 status=failed 人读,合理)。

## ⚠️ 本机真验
- 4-1 已证 localhost:22 真连可用 → 登记 localhost 服务器 + dist 产物,**真部署**(scp-like 传输经 SSH + 跑部署命令,如建目录/解包/软链)。image/jar 命令构造对即可(localhost 无 docker→failed 合理)。

## ⛓ 冻结契约(填 3-1 run-detail `targets` slot;4-4/4-5 消费)
### run-detail `targets`(本 story 定义并冻结该子形状;3-1 留的 null 块)
`GET /api/runs/{id}` 的 `targets` 字段(部署过 → 数组;否则 null):
```json
"targets": [
  { "serverId":"...", "serverName":"web-prod-1", "status":"success",
    "message":"dist 部署完成 → /var/www/shop", "startedAt":"RFC3339", "finishedAt":"RFC3339"|null }
]
```
- `status` ∈ **`pending` | `deploying` | `success` | `failed` | `rolled_back`**(枚举冻结;4-4 回滚置 rolled_back,4-5 扇出多条)。
- `message` 人读(成功摘要 / 失败原因,**绝无明文密钥**)。
### POST `/api/runs/{id}/deploy`(认证+CSRF)
请求 `{ "artifactId":"<该 run 的产物>", "serverIds":["..."], "deployConfig":{...可选} }` → 200 返回填好的 `targets` 数组(或 202 + 异步,作者定:**本期同步执行返回最终 targets**,简单可验)。
- run 非成功态 / 无该产物 → 422 人读;服务器不存在 → 422;run 不存在 404。**部署执行失败不 500**:每机 status=failed 记录,整体 200。
- 部署后 run 终态:全成功保持/置 success;有失败 → `partial_failed`(已是合法终态);全失败 → failed。

> **冻结点:** targets 子 DTO(serverId/serverName/status/message/startedAt/finishedAt)+ status 枚举 + deploy 端点定死。4-4 回滚、4-5 多机只新增/置状态,不改形状。

## 文件切分(独立 worktree 完整 BE+FE)
**后端(Go):**
- `internal/store/migrations/0017_deploy_targets.sql`:`deploy_targets(id, run_id, server_id, server_name, status, message, started_at, finished_at)` + 索引 `(run_id)`。
- `internal/deploy/deploy.go` + `*_test.go`:`Deployer`——按产物 type 构造部署命令(**cmd []string**:dist=mkdir+传输+解包/软链;jar=放置+`java -jar`/systemd(可选);image=`docker pull`+`docker run`)→ 经注入的 `target.Service.Exec` 执行 → 每机结果。`Service`:`Deploy(ctx, DeployInput{RunID, Artifact, ServerIDs, Config}) ([]TargetResult, error)`;持久化 deploy_targets;**run 包不 import deploy**(deploy import run 取产物 + 更新终态,或经注入)。**传输用 SSH**(`cat > file` / base64 经 Exec,或 sftp 子系统;dist 小文件够用)。
- `internal/run`:加 `DeployTarget` 模型 + `SaveDeployTargets`/`ListDeployTargets(runID)`(run-detail 读)。
- `internal/httpapi/runs.go`(改):run-detail DTO 填 `targets` slot(读 ListDeployTargets;无则 nil)。`deploy.go`(新):`POST /api/runs/{id}/deploy` handler(取 run+产物+服务器→deploy.Deploy→更新 run 终态→返回 targets)。
- `router.go` + `main.go`:**仅 append** 挂 deploy 路由;`deploy.New(targetSvc, runSvc)` + Option。
**前端(web/):**
- `web/src/api/runs.ts`:run-detail 加 `targets`;`deployRun(id, {artifactId, serverIds})`。
- `web/src/components/run/DeployTargets.vue`(新):多机部署结果卡(每机 status 徽标 + serverName + message + 耗时)。
- `web/src/views/RunDetail.vue`:**成功态**加「部署」入口(选服务器 + 触发 deployRun)+ 渲染 DeployTargets 填 `targets` slot。不扰终端(3-6)/诊断(7-2)/产物(3-4)slot。

## Dev Notes 护栏
- **AC-SEC-02**:部署命令 cmd array 化(经 target.Exec,不拼 shell);SSH 密钥经 vault(target 层已管);输出/错误/message 绝无明文密钥。
- **优雅**:部署失败每机记录、不 500、不连累其它机;run 终态按结果置 success/partial_failed/failed。
- targets 子 DTO 冻结;参数化 SQL;无 init 副作用;mem ≤100MB。零停机/回滚=4-4 不在本期。

## 编排者亲验清单
- `export PATH="/Users/huangjiawei/sdk/go/bin:$PATH"`;gofmt/vet/`go test -race ./internal/deploy/... ./internal/run/... ./internal/httpapi/...`/build/mem-check。
- 前端 typecheck(symlink node_modules)+ build。
- **真验:登记 localhost 服务器 + dist 产物 → POST deploy → 真在 localhost 执行部署命令、targets 回填 success**;image 产物→localhost 无 docker→failed 人读;非成功 run 422;裸输出无密钥;404/401/403。
- worktree 内 commit(分支 story/4-2)。
