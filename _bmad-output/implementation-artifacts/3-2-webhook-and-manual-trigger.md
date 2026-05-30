---
baseline_commit: NO_VCS
---

# Story 3.2: Webhook 触发与手动触发创建运行(FR-1 执行)

Status: done

> 后端/前端并行,经冻结契约对接。依赖 3-1(RunService.Create + worker pool)✅review、2-3(trigger 配置:token/密钥/events/分支映射)✅review、2-1(projects)✅review。
> **骨架所有权**:**不改 3-1 的 run-detail DTO 形状**;分支映射解析出的环境/目标服务器作为**运行内部元数据**存储(供 Epic 4 的 targets 消费),不塞进冻结 DTO。桩 runner 仍来自 3-1(真实构建=3-3)。

## Story

As a 管理员,
I want Gitee 的 push/tag/PR 投递到项目 webhook 端点后、或我在 UI 手动触发后,真正创建一次流水线运行,
so that "push 后自动更新"的链路从触发端被打通(本期跑桩 runner,真实构建/部署在 3-3/4-x 接)。

## Acceptance Criteria

1. **AC1 — Webhook 触发创建运行**:配置了 push 触发的项目,投递一个**匹配分支**的 push 事件到其 webhook 端点 → **数秒内创建一次流水线运行**(入 3-1 worker pool 调度),并按 2-3 的分支映射**解析目标环境/服务器**记录到运行(内部元数据,供 4-x)。
2. **AC2 — 未配置/不匹配 → 不创建 + 记录原因**:事件类型未勾选、或分支不匹配任何映射(且未匹配策略=忽略)→ **不创建运行**,记录被忽略的原因(可查)。未匹配但策略=记录 → 记录一次"已记录未部署"(本期可记为被忽略原因的一种,不创建可执行运行)。
3. **AC3 — 手动触发 + 验签拒绝**:UI 手动触发(指定分支、可选 commit)→ 创建一次等效运行(trigger.type=manual, actor=admin);**签名/密钥校验失败的 webhook 被拒**(401/403,不创建运行)。
4. **AC4 — 幂等去重**:同一 delivery id / 同一 (项目, commit, 事件) 的重复投递**不创建重复运行**(去重);网络重放被安全忽略并记录。

## 冻结的触发执行 API 契约(前后端共享,不得擅改)

- **Webhook 接收(公开,无会话;由签名校验)** `POST /api/webhooks/{token}`:
  - 识别 Gitee 头:`X-Gitee-Event`(Push/Tag/Merge Request Hook)、`X-Gitee-Token`(密码模式:明文密钥)或签名模式、`X-Gitee-Timestamp`、`X-Gitee-Delivery`(投递 id,用于去重)。
  - 校验:`token` 路径定位项目;**密钥校验**(密码模式:`X-Gitee-Token` == 解密后的 webhook 密钥,恒定时间比较;签名模式:HMAC-SHA256(timestamp+"\n"+secret) base64 == `X-Gitee-Token`/签名头——按 Gitee 文档实现,二者其一通过即可)。失败 → **401**,不创建。
  - 匹配:事件类型在 events 中勾选 + 分支命中某 branchMapping(支持通配)→ 创建运行(解析该映射的 environment/targetServerIds 存运行内部元数据);否则按 unmatchedPolicy 记录忽略原因。
  - 去重:`X-Gitee-Delivery`(或 项目+commit+event)已处理过 → 不重复创建,记 `ignored:"duplicate"`。
  - 响应:200 `{ "accepted":true, "runId":"..." }` / 200 `{ "accepted":false, "ignored":"event_not_subscribed|no_branch_match|duplicate|unmatched_ignored" }`;401 验签失败。**不要把密钥/明文回显**。
- **手动触发(认证 + CSRF)** `POST /api/projects/{id}/runs` body `{ "branch": string, "commit"? : string }` → 201 创建的 run 对象(3-1 run-detail DTO 形状)。前端据此跳 `/runs/{id}` 看进度。
- **被忽略投递查询(可选,供 AC2 可查)** 记录可经现有审计/日志或一张轻表;前端本期不强制展示。
- 全部认证端点过 1-2 requireAuth + 写方法 requireCSRF;**webhook 接收端点豁免 requireAuth/CSRF**(它是公开入口,靠签名),但要在 CSRF 中间件白名单里排除 `/api/webhooks/`。JSON camelCase;错误体永不含密钥。

## Tasks / Subtasks

- [x] **Task 1 — webhook 接收 + 验签 + 匹配 + 去重**(AC: 1,2,4)
  - [x] `internal/trigger`(或 `internal/webhook`):`Receiver` 处理 Gitee 投递——按 token 取项目 trigger 配置、解密 webhook 密钥(经 vault OpenSecret)、验签(密码模式 + 签名模式,恒定时间比较)、解析事件类型/分支/commit/delivery id、按 events+branchMappings 匹配(通配 glob)、命中则解析 environment/targetServerIds 调 `RunService.Create`、未命中按 unmatchedPolicy 记忽略原因。
  - [x] 去重:`webhook_deliveries` 表(project_id + delivery_id UNIQUE,或 project_id+commit+event)记录已处理;命中则忽略。迁移 `0007_webhook_deliveries.sql`(+ 视需要给 pipeline_runs 加 `resolved_environment`/`resolved_target_server_ids` 内部列,**不改冻结 DTO 输出**)。
  - [x] 分支匹配:通配 `*`(如 `release/*`)glob 实现;一分支命中多服务器即解析多目标。
- [x] **Task 2 — HTTP 端点**(AC: 1,3,4)
  - [x] `POST /api/webhooks/{token}`(**公开**,在 requireAuth/CSRF 白名单外;限请求体大小)。`POST /api/projects/{id}/runs`(认证+CSRF,手动触发,actor=admin)。装配进 main.go(注入 Receiver + RunService + trigger/project/vault)。
- [x] **Task 3 — 测试**(AC: 全部)
  - [x] Go test:密码模式验签通过创建运行 + 错误密钥 401;事件未勾选/分支不匹配 → 不创建 + 记原因;通配分支命中;同 delivery id 重复 → 第二次 ignored:"duplicate" 不重复创建;手动触发创建 run(经真实 RunService+pool 等到 worker 推进);webhook 端点无需 cookie/CSRF 可达;响应/日志无密钥明文。
- [x] **Task 4 — 前端 手动触发**(AC: 3)
  - [x] `web/src/api/runs.ts` 加 `triggerManual(projectId,{branch,commit})`(POST /api/projects/{id}/runs,带 CSRF)。
  - [x] 在**项目卡快捷操作**(`Projects.vue`,「▶ 运行」)和/或 **运行列表**(`Runs.vue`,「手动触发」)加手动触发入口 → **模态**(选/填分支 + 可选 commit)→ 创建成功 → **跳 `/runs/{id}`**(3-1 详情页据 SSE 看 running→success 推进)。
  - [x] webhook 端点 URL/配置已在 2-3 的触发设置页;本期前端只加手动触发 + 跳转。错误态(无分支/创建失败)走规范;走 1-5 令牌/组件。

## Review Findings(2026-05-30 三层对抗评审 · 后端 A 块)

Patch(无歧义可直接修):
- [x] [Review][Patch] **webhook 签名模式无时间戳新鲜度校验 → 重放永久有效**:`X-Gitee-Timestamp` 只进 HMAC 不与当前时间比对,且签名不绑 body,攻击者改 `X-Gitee-Delivery` 即换去重键重放触发部署 → 校验 `|now-timestamp| ≤ ~5min`,缺失/超窗 401 [internal/trigger/receiver.go verifySignature](blind+edge,High)
- [x] [Review][Patch] 去重健壮性三连:① claim+CreateWebhookRun+回填 run_id 非原子,失败回滚 DELETE 后并发同 delivery 可重复创建;② `UPDATE ... SET run_id` 错误被 `_,_=` 吞致去重表与实际运行脱节 → run_id 回填错误不吞;③ 派生去重键 `derived:event:branch:commit` 以 `:` 拼接不转义,分支名含 `:` 可碰撞误判/绕过 + 空 payload(畸形 JSON)派生同键致误去重 → 用定长 hash 或长度前缀分隔 [internal/trigger/receiver.go claim/record/deliveryKey](blind+edge,Medium)
- [x] [Review][Patch] 未匹配(尤其 `record` 策略)投递每次都尝试写 `webhook_deliveries`,改 delivery_id 即可无限灌库且无 TTL 清理 → 对未匹配/record 路径也纳入去重/速率;去重表加保留期清理(清理可作 defer)[internal/trigger/receiver.go record](blind+edge,Medium)

Change Log(2026-05-30 后端 A 块 patch):
- 防重放(verifySignature/Handle):`verifySignature` 改返回 `(mode, ok)`(password/signature);Handle 在验签通过后调 `timestampFresh` —— 签名模式 timestamp 必填且 `|now-ts| ≤ 5min`,密码模式缺失放行/存在则同样校验。`parseEpoch` 自适应解析 Gitee timestamp(>=1e12 按毫秒 `UnixMilli`,否则按秒 `Unix`)。`nowFunc` 变量便于测试注入。新增测试:旧 ts 重放拒(401 不创建)/签名模式缺 ts 拒/毫秒 ts 接受;既有签名模式成功测试改用新鲜 ts。
- 派生去重键(derivedKey):废弃 `"derived:event:branch:commit"` 裸冒号拼接,改 sha256(对每段 `len:value` 长度前缀)→ `"derived:"+hex`,消除分支名含 `:` 的跨段碰撞与空 payload 误碰。新增 derivedKey 碰撞测试。
- claim/create 原子性 + 不吞错:创建失败回滚 DELETE 错误不再 `_,_=`,失败合并进返回错误(防占位残留误判 duplicate);run_id 回填改 `backfillRun`(失败做一次最佳努力重试,但已创建的运行不翻成 500——占位仍在,重投判 duplicate 不重复创建)。
- 未匹配/record 不灌库:统一所有路径(未订阅/未匹配 record|ignore/命中)先走同一 `claim` dedup,同 delivery 幂等(UNIQUE 并发安全),ignored 原因经 `setOutcome` 回填(不吞错+重试);删除旧 `record` 无界 INSERT。新增「未匹配 record 同 delivery 只入一行」测试。

Defer(真实但需真实环境/属协议固有,入 deferred-work.md):
- [x] [Review][Defer] **签名模式 HMAC 构造(secret 既作 key 又拼进 message)未经真实 Gitee 投递验证**,线上对接可能失败 → 抓真实 Gitee webhook 包核对算法 [internal/trigger/receiver.go](auditor+blind,Medium,需真实 Gitee)
- [x] [Review][Defer] 密码模式 `X-Gitee-Token`==明文密钥 是 Gitee 协议固有弱点(任何读到一次请求头/日志者得密钥)+ ConstantTimeCompare 长度短路泄漏长度 → 文档化推荐签名模式,考虑可禁用密码模式 [internal/trigger/receiver.go](blind+edge,Medium)
- [x] [Review][Defer] `globMatch` 的 `*` 跨 `/` 过度匹配(`release/*` 命中 `release/a/b/c`)→ 明确单段语义或 UX 说明;`matchBranch` 取首条命中、顺序敏感无显式优先级 [internal/trigger/receiver.go globMatch/matchBranch](edge,Low)

AC 复核(auditor):3-2 AC1-4 全满足(webhook 创建运行 + 解析目标元数据存内部列**未改冻结 run-detail DTO** + 验签拒绝 401 + 幂等去重 + 手动触发 201);webhook 公开端点正确豁免 auth+CSRF(注册在受保护组外 + MaxBytesReader 限体积)。

## Dev Notes

### 必守护栏
- **webhook 公开端点安全**:在 CSRF/auth 中间件**白名单排除** `/api/webhooks/`;靠签名校验(恒定时间比较),失败 401;限请求体大小(`http.MaxBytesReader`);**绝不**回显/日志密钥明文。密钥经 vault `OpenSecret` 解密(master key 未配置则该项目 webhook 验签不可用 → 明确拒绝,不 panic)。[Source: architecture.md#AC-SEC / Authentication & Security]
- **不改冻结 DTO**:解析出的 environment/targetServerIds 存运行**内部列/元数据**,供 Epic 4 的 `targets[]` 消费;**不动 3-1 的 run-detail DTO 形状**(骨架所有权)。
- **命令/注入**:本期不执行真实命令(桩 runner);webhook payload 解析用 encoding/json,不拼命令。
- **边界/命名/格式**:经 store/repository + vault + RunService;httpapi 唯一对外面;DB snake_case / JSON camelCase / Go PascalCase / `Receiver`/`Service`/`ErrXxx`/RFC3339;错误体永不含 secret。modernc 纯 Go 禁 CGO;沿用 sqlite pragma + 迁移事务。
- **幂等**:delivery id 去重用 UNIQUE 约束 + 冲突即忽略;并发同投递安全(靠 DB 唯一约束)。
- **≤100MB**:勿 init() 跑重活;mem-check 不回退。

### 边界(本期不做)
- ❌ 真实构建/部署(桩 runner;真实=3-3/4-2);❌ targets 多机执行(=4-x,本期只解析记录目标元数据);❌ 实时日志正文(=3-6);❌ AI 诊断(=7-x)。

### 禁止
- ❌ webhook 端点要求会话/CSRF(它是公开签名入口);❌ 密钥明文入日志/响应;❌ 改 3-1 run-detail DTO 形状;❌ 重复投递创建重复运行;❌ CGO sqlite;❌ 后端碰 web/ 或前端碰 Go;❌ init() 跑重活;❌ 回退既有安全修复。

### References
- [Source: epics.md#Epic 3 / Story 3.2]
- [Source: prd.md#FR-1(webhook push/tag/PR 触发 + 手动触发)]
- [Source: architecture.md#Authentication & Security(签名校验)/ 任务调度(创建即入 pool)/ AC-SEC]
- [Source: EXPERIENCE.md#触发设置 / 手动触发(运行列表点手动触发指定分支/commit)/ Key Flow 2(点▶运行→进行中→成功)]
- [Source: 2-3(trigger 配置:token/密钥/events/branchMappings + vault OpenSecret)/ 3-1(RunService.Create + worker pool + run-detail DTO)/ 2-1(projects)]

## Dev Agent Record

### Agent Model Used
claude-sonnet-4-6 (Task 4 前端部分)
claude-opus-4-8[1m] (Task 1-3 后端部分)

### Debug Log References
- vue-tsc --noEmit: 零错误
- vite build: 成功,Runs-*.js 14.46 kB gzip 5.16 kB,Projects-*.js 31.44 kB gzip 8.44 kB

### Completion Notes List

**后端 Task 1-3(opus-4-8;未碰 `web/`):**
- **Receiver(`internal/trigger/receiver.go`)**:按 webhook_token SQL 定位项目触发配置 → `vault.OpenSecret` 解密签名密钥(明文用后 `zero()` 清零)→ 验签(密码模式 `X-Gitee-Token`==明文密钥;签名模式 `base64(HMAC-SHA256(key=secret,msg=timestamp+"\n"+secret))`==头;均 `crypto/subtle.ConstantTimeCompare`,其一通过)→ `encoding/json` 解析 ref(`refs/heads/`、`refs/tags/` 取末段)/commit/delivery → events 订阅判定 → branchMappings 通配匹配(`path.Match` + 自实现 `globMatch` 兜底 `release/*` 跨 `/`)→ 命中前先抢占 `webhook_deliveries`(UNIQUE 冲突=duplicate,并发安全)→ 调 `RunCreator.CreateWebhookRun`,创建失败回滚去重占位。
- **去耦**:`trigger` 不 import `run`;定义 `RunCreator`/`RunRequest`,httpapi 提供 `NewRunCreator(run.Service)` 适配注入 `run.Trigger{Type:webhook,Resolved*,...}`。
- **迁移 0007**:`webhook_deliveries`(project_id+delivery_id UNIQUE,记 event/branch/commit/run_id/outcome 供 AC2 可查)+ `pipeline_runs` 加 `resolved_environment`/`resolved_target_server_ids` 内部列;delivery_id 缺失时回退 `derived:event:branch:commit` 派生键去重。
- **RunService.Create**:`run.Trigger` 增 `ResolvedEnvironment`/`ResolvedTargetServerIDs` 并持久化到内部列;**未改冻结 run-detail DTO 形状**(targets/diagnosis 恒 null,resolved 字段不出现在 JSON——测试与冒烟均断言)。
- **HTTP**:`POST /api/webhooks/{token}` 公开(注册在受保护 `/api` 组之外,豁免 requireAuth/CSRF;`MaxBytesReader` 限 1 MiB;验签失败 401、token 未知 404、命中 200 `{accepted:true,runId}`、否则 200 `{accepted:false,ignored:<枚举>}`)。`POST /api/projects/{id}/runs` 认证+CSRF,actor=admin,缺 branch→400,成功 201。
- **安全**:密钥明文/密文绝不入日志/响应/错误体(冒烟 `grep` 日志 0 命中);master key 未配置 → `ErrUnauthorized` 明确拒绝不 panic(有单测)。
- **真实二进制冒烟全过**:登录→git_token 凭据→`file://` 裸仓库建项目→GET token / reset 明文 / PUT push+`main→prod(srv-a,srv-b)`→① 手动触发 201→worker success;② webhook 正确密钥 accepted+success、错误密钥 401、重复 delivery `ignored:duplicate`(DB 仅 1 行)、不匹配分支 `unmatched_ignored`、签名模式 HMAC accepted;DB dump 确认 resolved 内部列正确落库。
- 校验:`gofmt -l` 空、`go vet` 干净、`go test ./...` + `-race ./internal/...` 全绿、`CGO_ENABLED=0 build` OK、`go mod tidy` 无变更(仅标准库)、`make mem-check` 20MB(≤100MB 未回退)。

**前端 Task 4(sonnet-4-6):**
- `triggerManual(projectId, {branch, commit?})` 已加入 `runs.ts`,直接调用 `http.post` 带 CSRF,返回冻结 RunDetail DTO。
- `Projects.vue` 项目卡操作栏新增「▶ 运行」图标按钮(primary 悬停样式);点击弹出手动触发模态 —— 分支字段预填 `project.defaultBranch`,可选 Commit,触发成功后 `router.push('/runs/' + run.id)`。
- `Runs.vue` 页面头部新增「手动触发」主操作按钮;弹出模态先选项目(懒加载 `listProjects`)再填分支+可选 Commit;选中项目时自动回填 `defaultBranch`。
- 两处模态均实现:字段级错误提示、全局 banner 错误、loading/disabled 态、Esc 关闭、scrim 点击关闭、reduced-motion 降级(@media)、深浅双主题(全走设计令牌)。
- 后端未就绪时 (status === 0 / 404 / 非 2xx) 均有友好 banner,不白屏。

### File List

后端(Task 1-3):
- `internal/store/migrations/0007_webhook_deliveries.sql`(新增:去重表 + pipeline_runs resolved 内部列)
- `internal/trigger/receiver.go`(新增:webhook `Receiver` 验签/解析/匹配/去重)
- `internal/trigger/receiver_test.go`(新增)
- `internal/httpapi/webhooks.go`(新增:webhook + 手动触发 handler + `NewRunCreator` 适配)
- `internal/httpapi/webhooks_test.go`(新增)
- `internal/run/run.go`(改:`Trigger` 增 Resolved* 内部字段)
- `internal/run/service.go`(改:`Create` 持久化 resolved 内部列)
- `internal/httpapi/router.go`(改:`WithWebhooks` Option + 公开 webhook 路由 + 手动触发路由)
- `cmd/devopstool/main.go`(改:装配 Receiver 并注入 `WithWebhooks`)

前端(Task 4):
- `web/src/api/runs.ts` — 新增 `TriggerManualInput` 接口 + `triggerManual` 函数
- `web/src/views/Projects.vue` — 新增 `triggerManual` import、手动触发状态+处理器、卡片「▶ 运行」按钮、手动触发模态 Teleport、相关 CSS
- `web/src/views/Runs.vue` — 新增 `triggerManual`/`listProjects`/`RunDetail` import、手动触发状态+处理器、页头「手动触发」按钮、模态 Teleport、完整表单+modal CSS

## Change Log

- 2026-05-29:创建 Story 3.2 上下文(webhook 触发 + 手动触发创建运行);冻结触发执行 API 契约;webhook 公开签名入口、幂等去重、分支映射解析存运行元数据不改冻结 DTO。Status → in-progress。
- 2026-05-29:Task 4 前端完成。`triggerManual` API 函数、Projects.vue 卡片「▶ 运行」按钮+模态、Runs.vue 「手动触发」按钮+模态全部实现。vue-tsc 零错误,vite build 成功。
- 2026-05-29:后端 Task 1-3 完成——webhook `Receiver`(密码/签名双模式恒定时间验签、Gitee 头/ref/commit 解析、events+通配分支匹配、`webhook_deliveries` UNIQUE 去重)、迁移 0007(去重表 + pipeline_runs resolved 内部列)、公开 `POST /api/webhooks/{token}`(豁免 auth/CSRF、限体积、401/200 契约)+ 认证 `POST /api/projects/{id}/runs`(手动触发 201)、main 装配。未改冻结 run-detail DTO。Go test + race 全绿、`CGO_ENABLED=0` 静态 build、`go mod tidy` 无变更、mem-check 20MB。真实二进制端到端冒烟(手动→success、webhook 验签/去重/匹配/签名模式)全过。Status → review。
