---
baseline_commit: NO_VCS
---

# Story 3.1: 运行模型与运行详情页骨架(+ 冻结 run-detail DTO)

Status: done

> 本 story 是 Epic 3 地基。**骨架所有权规则:仅本 story 定义/修改运行详情骨架与 run-detail DTO 形状;Epic 4(targets 多机)/Epic 7(diagnosis)只往既定 slot 填、不改形状。** 后端/前端并行,经冻结 DTO 对接。依赖 1-2/1-5/2-1 ✅。
> 边界:本 story **不做真实构建/部署**(=3-3/4-x),worker pool 跑**桩 runner**(占位步骤→成功)以打通状态机;**不做真实触发**(=3-2);**不做日志内容流**(=3-6,本期 SSE 只推状态/步骤事件)。

## Story

As a 平台开发者,
I want 一次性定义流水线运行数据模型、进程内 worker pool、运行详情页 5 态骨架与可扩展 run-detail DTO,
so that Epic 4/7 只需往既定 slot 填组件、不必重构运行详情页(杜绝跨 epic 考古)。

## Acceptance Criteria

1. **AC1 — 运行模型 + 状态机 + worker pool**:创建流水线运行 → 记录入库,状态机 `queued→running→success|failed|partial_failed|rolled_back`;由**进程内 worker pool**(goroutine 池 + SQLite 持久化运行/步骤状态)调度;**多流水线并行、非事务、结果独立**(FR-13 调度地基)。worker 经可插拔 `Runner` 接口执行;本期桩 runner 跑占位步骤(可模拟成功/失败)使状态机可被测试与观察。
2. **AC2 — 5 态 slot + 可扩展 DTO(只填不扩)**:运行详情页提供 5 态 slot(进行中 / 成功 / 失败诊断 / 多机部分失败 / 列表);**run-detail DTO 前向声明** `targets`(多机,Epic 4 填,默认 null)与 `diagnosis`(诊断,Epic 7 填,默认 null)为**可选块**;形状冻结见下契约。
3. **AC3 — 运行列表骨架**:全局运行列表骨架就位(按日期分组的全宽表/卡,状态徽标、项目/分支/触发、耗时、分页占位),可点进详情;空态。
4. **AC4 — 状态实时(SSE 骨架)**:进行中运行经 SSE 推送**状态/步骤**变化(worker pool 驱动),进行中态据此推进节点;**日志内容流不在本期**(3-6)。无 SSE 时可降级轮询 `GET /api/runs/{id}`,不阻断。

## 冻结的 run-detail DTO 契约(跨 Epic 共享;Epic 4/7 只填 null 块、不得改形状)

```jsonc
// GET /api/runs/{id}
{
  "id": "...", "projectId": "...", "projectName": "acme",
  "status": "queued|running|success|failed|partial_failed|rolled_back",
  "trigger": { "type": "webhook|manual", "branch": "main", "commit": "d52e8a0", "actor": "admin|gitee" },
  "steps": [ { "id":"...","name":"拉取源码","status":"pending|running|success|failed|skipped",
              "startedAt":null,"finishedAt":null,"durationMs":null } ],   // 穿珠时间线节点
  "createdAt":"RFC3339","startedAt":null,"finishedAt":null,"durationMs":null,
  "targets": null,      // Epic 4 填:[{ "server":"生产-1","status":"...","steps":[...],"rolledBackTo":null }]
  "diagnosis": null     // Epic 7 填:{ "rootCause":"...","confidence":0.0,"evidence":[...],"feedback":null }
}
```
- **列表** `GET /api/runs?projectId=&status=&page=` → 200 `{ "items":[{ "id","projectId","projectName","status","trigger","createdAt","durationMs" }], "page","total" }`(列表行精简,不含 steps/targets/diagnosis)。
- **取消** `POST /api/runs/{id}/cancel` → 200 更新后 run(进行中→可取消;终态不可)。需 CSRF。
- **状态流** `GET /api/runs/{id}/events`(SSE)→ 推 `event: status`(run 状态)与 `event: step`(步骤状态)JSON;**仅状态,不含日志正文**(3-6)。
- 全部过 1-2 requireAuth(SSE 也要认证);写方法过 requireCSRF;JSON camelCase;时间 RFC3339;成功返资源对象。
- **内部创建运行**:`RunService.Create(projectId, trigger)` 供 3-2(触发)调用;本 story 可加最小受保护入口或仅内部 + 测试用,真实触发=3-2。

## Tasks / Subtasks

- [x] **Task 1 — 运行模型 + 迁移**(AC: 1,2)
  - [x] `internal/store/migrations/0006_runs.sql`:`pipeline_runs`(id PK、project_id FK、status、trigger_type、trigger_branch、trigger_commit、trigger_actor、created_at、started_at NULL、finished_at NULL)+ `run_steps`(id、run_id FK CASCADE、name、status、ordinal、started_at、finished_at)。snake_case;索引 `idx_pipeline_runs_project_id`、`idx_run_steps_run_id`。
- [x] **Task 2 — worker pool + Runner + 状态机**(AC: 1)
  - [x] `internal/run/`:`RunService`(Create/Get/List/Cancel)、状态机(合法转移校验)、`WorkerPool`(进程内 goroutine 池,从 queued 取、转 running、跑 Runner、按结果转终态;并发上限可配;多 run 独立、单 run 失败不连累他者)、`Runner` 接口(`Run(ctx, *Run, stepSink) error`)。本期 `StubRunner`(顺序跑几个占位步骤、每步置 success,可注入失败/取消;真实构建=3-3 换实现)。状态/步骤变更持久化 + 经事件总线推 SSE。
  - [x] 取消:context 取消传播到 Runner;进行中→cancel→标记(复用 `failed` 终态:queued 取消直接 failed;running 取消经 context 传播,runner 观察后落 failed)。
- [x] **Task 3 — HTTP + SSE**(AC: 2,3,4)
  - [x] `internal/httpapi`:`GET /api/runs`(列表+筛选+分页)、`GET /api/runs/{id}`(run-detail DTO,targets/diagnosis=null)、`POST /api/runs/{id}/cancel`、`GET /api/runs/{id}/events`(SSE 状态/步骤);装配进 main.go(注入 RunService + 启动 worker pool;优雅停机时停 pool)。DTO 严格按冻结形状。
- [x] **Task 4 — 测试**(AC: 1,2)
  - [x] Go test:Create→worker 调度→success 全流程;并发多 run 独立(一个失败不影响另一个);非法状态转移被拒;取消进行中;DTO 形状(含 targets/diagnosis=null 字段存在);列表筛选/分页;SSE 推送状态(httptest 读 status 事件);auth/CSRF。worker pool 测试用注入的快速 StubRunner/可控 runner(无 sleep)。
- [x] **Task 5 — 前端 运行详情 5 态骨架 + 列表**(AC: 2,3,4)
  - [x] `web/src/api/runs.ts`:listRuns/getRun/cancelRun + SSE 订阅(EventSource → 断线降级轮询)。类型严格按冻结 DTO(targets?/diagnosis? 可选)。
  - [x] `web/src/views/Runs.vue`(改实现):全局运行列表(按日期分组全宽表,状态徽标六词、项目/分支/触发/耗时、行可点进、失败行红渐变+查看诊断、进行中流光、分页占位、空态、骨架)。
  - [x] `web/src/views/RunDetail.vue`(新)+ 路由 `/runs/:id`:5 态骨架按 status 渲染,slot 具名区块 + 注释标明归属(3-6 日志 / Epic4 零停机·targets / Epic7 诊断),Epic4/7 只填不改骨架;SSE 订阅推进 + 取消按钮。
  - [x] 状态徽标固定六词 + 进行中脉冲;走 1-5 令牌/组件;reduced-motion 降级;SSE 断线降级轮询。

## Review Findings(2026-05-30 三层对抗评审 · 后端 B 块:run/project)

Patch(无歧义可直接修):
- [x] [Review][Patch] **SSE 先取快照(svc.Get)后订阅(Subscribe),中间事件丢失 + 终态在窗口内发生则连接永久挂起**(注释与代码相反)→ 先 Subscribe 再取快照,快照后若已终态立即收流 [internal/httpapi/runs.go makeRunEventsHandler](blind+edge,High)
- [x] [Review][Patch] **`Enqueue` 队列满/Stop 后退化为阻塞发送 `p.queue<-id`,会永久阻塞 Create→HTTP handler**(停机或突发 >256 创建)→ `select{case queue<-id; case <-p.ctx.Done(): 返回错误/丢弃}` [internal/run/pool.go Enqueue](blind+edge,High)
- [x] [Review][Patch] 慢/不读的 SSE 订阅者使终态 status 事件被非阻塞 bus 丢弃 → 连接/goroutine 泄漏到心跳超时 → 终态事件保证投递,或 SSE 加最大存活 + 周期性 `svc.Get` 兜底查终态收流 [internal/run/bus.go,httpapi/runs.go](blind+edge,Medium)
- [x] [Review][Patch] `Cancel` 在 queued→failed 转移撞上 worker 已转 running 时把 `ErrInvalidTransition` 透传 → HTTP 500;且命中句柄路径返回可能仍 running 的旧快照而非对终态返 `ErrNotCancelable` → 命中后重查终态、映射合理错误,勿透传 ErrInvalidTransition [internal/run/service.go Cancel](blind+edge,Medium)
- [x] [Review][Patch] worker panic recover 硬编码 `transition(running→failed, requireFrom=false)`:若 panic 在转 running 前,会把 queued 强制改 failed(无 started_at,语义破坏)→ 改"幂等置 failed(若非终态)"按当前状态合法转移 [internal/run/pool.go recover](blind+edge,Medium)
- [x] [Review][Patch] `dbStepSink.StepDone` 失败被调用方 `_ =` 忽略 → 出现"run=success 但某 step 永远 running"不一致 → StepDone 失败传播为运行失败,或落终态时校正悬挂步骤 [internal/run/pool.go,runner](edge,Medium)
- [x] [Review][Patch] 删除项目级联删其 pipeline_runs/run_steps,但**不取消仍在 worker 中执行的 run** → **已修(在 2-1 project.go)**:Delete 前查 `pipeline_runs WHERE status IN (queued,running)`,>0 返回 `ErrProjectHasActiveRuns`(409),拒删而非静默级联 [internal/project/project.go Delete](edge,Medium)
- [x] [Review][Patch] 分页 `page` 非数字静默回退第 1 页(无 400)、无上界 → 极大 page 致 `(page-1)*size` OFFSET 溢出为负 → 校验 page(非法 400 或钳制 + 上界) [internal/httpapi/runs.go,run/service.go List](blind+edge,Low)
- [x] [Review][Patch] `projects` List 无分页/上限,全量返回 → 项目多时无界查询 → **已修(在 2-1 project.go)**:`ListPaged` + `DefaultPageSize=50`/`MaxPageSize=500` + LIMIT/OFFSET/COUNT,`List` 委托第 1 页契约兼容 [internal/project/project.go List](edge,Medium)

Defer(真实但留后续 epic / 需真实环境,入 deferred-work.md):
- [x] [Review][Defer] worker pool 默认并发 4 与 `SetMaxOpenConns(1)` 冲突:真实(非桩)runner 每步多次写库,4 worker 争唯一连接 + busy_timeout 5s 易 SQLITE_BUSY → **3-3 真实 runner 落地时**重估(降并发/给运行单独写连接/串行化写)[internal/run/pool.go](edge,Medium)
- [x] [Review][Defer] `resolved_target_server_ids` 只写不读(本期有意,供 Epic 4)→ **Epic 4** 读取时做容错反序列化(空/坏 JSON→[])[internal/run/service.go](edge)

Dismissed:`dbStepSink` StepDone 用 background ctx 与 StepRunning 用 runCtx 不一致(有意:失败步骤须持久化,已注释)· 并发取消同 run 返回非终态快照(异步语义,spec 允许取消复用 failed)· status 筛选大小写敏感(契约友好度)。

## Dev Notes

### 必守护栏
- **调度地基(FR-13)**:进程内 worker pool + SQLite 持久化,无外部队列;多 run 并行、非事务、结果独立——这是后续多机/部分失败的根。[Source: architecture.md#任务调度]
- **骨架所有权 / 只填不扩**:run-detail DTO 与运行详情骨架**仅本 story 定形**;`targets`/`diagnosis` 前向声明为 null 可选块,Epic 4/7 填值不改形状、不改骨架文件结构(防多 agent 并发改同文件回归)。[Source: epics.md#Story 3.1 + 跨 story 接口冻结]
- **边界/命名/格式**:`internal/run` 经 store 触库、不碰 HTTP;httpapi 唯一对外面;DB snake_case / JSON camelCase / Go PascalCase / `Runner`(-er)/`Service`/`ErrXxx`/RFC3339;成功返资源对象;SSE 用标准 `text/event-stream`。沿用 1-1 sqlite pragma + 迁移事务;modernc 纯 Go 禁 CGO。
- **SSE + worker 并发**:注意 `SetMaxOpenConns(1)` 下 SSE 长连接不可占着 DB 连接轮询;状态推送走内存事件总线(worker 发布、SSE 订阅),DB 只在状态变更时短写。worker pool 优雅停机随 HTTP server。
- **≤100MB**:worker pool 池大小有界;勿 init() 跑重活;mem-check 不回退。

### 边界(本期不做)
- ❌ 真实构建/部署执行(桩 runner 占位;真实=3-3/4-2);❌ 真实触发创建(=3-2);❌ 日志内容流(=3-6,本期 SSE 只状态/步骤);❌ targets 多机内容(=4-x)/diagnosis 内容(=7-x)——只留 slot + DTO null 块。

### 禁止
- ❌ 改 DTO 形状的"扩"(只可填 null 块);❌ CGO sqlite;❌ 后端碰 web/ 或前端碰 Go;❌ SSE 长连接占用唯一 DB 连接轮询;❌ init() 跑重活;❌ 回退既有安全修复。

### References
- [Source: epics.md#Epic 3 / Story 3.1 + Overview 跨 story 接口冻结(run-detail DTO 3.1)]
- [Source: architecture.md#任务调度(进程内 worker pool + SQLite,无外部队列;多流水线并行非事务结果独立)/ API & Communication(SSE)]
- [Source: EXPERIENCE.md#单次运行·5 态 / Component Patterns(流水线骨架图 skewer/状态徽标/日志终端)/ Interaction(实时即推 SSE)]
- [Source: DESIGN.md#Components(pipeline-node/连线/状态 pill/终端/骨架)/ mock-run-live·run-success·run-diagnosis·run-multi·runs(结构参考)]
- [Source: 1-5(壳/路由/http)/ 2-1(projects)/ 2-3(trigger 将在 3-2 创建 run)]

## Dev Agent Record

### Agent Model Used
- claude-sonnet-4-6 (Task 5 — 前端侧)
- claude-opus-4-8[1m] (Task 1-4 — 后端:运行模型/worker pool/状态机/HTTP+SSE/测试)

### Debug Log References
- vue-tsc --noEmit: 零错误
- vite build: 成功,RunDetail-BA1-Ops5.js 18.81 kB gzip 5.06 kB
- (后端)gofmt -l cmd internal: 空;go vet . ./cmd/... ./internal/...: 干净
- (后端)go test . ./cmd/... ./internal/...: 全绿;go test -race ./internal/run ./internal/httpapi: 全绿
- (后端)CGO_ENABLED=0 go build ./cmd/pipewright: 成功;go mod tidy: go.mod/go.sum 无变化
- (后端)make mem-check: resident 20MB ≤ 100MB,OK
- (后端)真实二进制冒烟:登录 → DB 播种 queued run → GET /api/runs(列表)/GET /api/runs/{id}(targets/diagnosis=null 且 key 存在)/GET /api/runs/{id}/events(读到 `event: status` 帧)/未认证 SSE = 401

### Completion Notes List
- Task 5 完成:web/src/api/runs.ts + web/src/views/Runs.vue(改) + web/src/views/RunDetail.vue(新) + 路由 /runs/:id
- SSE 订阅就位(EventSource → polling 降级),5 态骨架按 status 渲染
- slot 占位区块均有具名注释标明归属(Story 3-6 / Epic 4 / Epic 7)
- 冻结 DTO 严格遵守:targets? / diagnosis? 可选,形状不变
- (后端 Task 1)迁移 0006_runs.sql:pipeline_runs + run_steps,snake_case + FK CASCADE + 两索引。
- (后端 Task 2)`internal/run`:Service(Create/Get/List/Cancel)+ 状态机(allowedTransitions 表 + CAS 式 transition 持久化)+ WorkerPool(进程内 goroutine 池,有界并发默认 4,内存 channel 队列非轮询 DB,每 run 独立 goroutine + recover 隔离 panic/失败)+ Runner 接口 + StubRunner(占位三步,可注入 FailAt/取消,无 sleep)。状态/步骤变更持久化 + 经内存事件总线发布。取消复用 failed 终态:queued→直接 failed;running→context 取消传播,runner 观察后 failed。
- (后端 Task 3)`internal/httpapi/runs.go`:GET /api/runs(筛选 projectId/status + 分页)、GET /api/runs/{id}(冻结 run-detail DTO,targets/diagnosis 输出 null)、POST /api/runs/{id}/cancel(过 CSRF)、GET /api/runs/{id}/events(SSE text/event-stream,推 status/step 事件、不含日志正文,经事件总线订阅不占 DB 连接,15s 心跳保活,终态推完即收流)。全过 requireAuth。装配进 main.go:注入 RunService + WithRuns(pool 作 SSE 订阅源),启动 pool,优雅停机先 srv.Shutdown 再 pool.Stop;WriteTimeout 置 0 以容 SSE 长连接。
- (后端)冻结 DTO 严格遵守:字段名 camelCase、targets/diagnosis 为 `*any` 恒输出 null 且 key 始终存在;列表行精简(无 steps/targets/diagnosis)。
- (后端)RunService.Create(projectId, trigger) 已就位供 3-2 调用;入库后经注入的 enqueue 回调推入 pool 队列(无 pool 时仅入库)。

### File List
- web/src/api/runs.ts (新建)
- web/src/views/Runs.vue (改实现)
- web/src/views/RunDetail.vue (新建)
- web/src/router/index.ts (加 /runs/:id 路由)
- internal/store/migrations/0006_runs.sql (新建)
- internal/run/run.go (新建:模型 + 状态机)
- internal/run/bus.go (新建:内存事件总线)
- internal/run/runner.go (新建:Runner 接口 + StubRunner + StepSink)
- internal/run/service.go (新建:Service Create/Get/List/Cancel + transition)
- internal/run/pool.go (新建:WorkerPool + dbStepSink)
- internal/run/run_test.go (新建:领域 + pool 测试)
- internal/httpapi/runs.go (新建:DTO + handlers + SSE)
- internal/httpapi/runs_test.go (新建:HTTP/SSE/auth/CSRF 测试)
- internal/httpapi/router.go (改:WithRuns Option + /api/runs* 路由)
- cmd/pipewright/main.go (改:装配 RunService + pool + 优雅停机 + WriteTimeout=0)

## Change Log

- 2026-05-29:创建 Story 3.1 上下文(运行模型 + worker pool + 5 态骨架 + 冻结 run-detail DTO);定桩 runner、SSE 只状态、targets/diagnosis 前向声明只填不扩。Status → in-progress。
- 2026-05-29:Task 5 前端完成 — runs API / Runs.vue / RunDetail.vue / 路由;vue-tsc 零错误,vite build 成功。
- 2026-05-29:Task 1-4 后端完成 — 迁移 0006 + internal/run(模型/状态机/worker pool/事件总线/StubRunner)+ internal/httpapi/runs(列表/详情/取消/SSE)+ main.go 装配。fmt/vet/test(含 -race)/build/mod tidy/mem-check 全过;真实二进制冒烟通过。Status → review。
- 2026-05-30:应用运行子系统评审 7 patch(仅改 internal/run/* + internal/httpapi/runs.go):
  1. SSE 改为**先 Subscribe 再 svc.Get 快照**,消除快照-订阅窗口漏事件;快照已终态则发完初始帧即收流。
  2. `Enqueue` 永不阻塞:队列满→`ErrQueueFull`、池停机→`ErrPoolStopped`;`enqueue` 回调签名改 `func(string) error`,`Create` 入队失败删孤儿 queued 记录并失败返回。
  3. SSE 终态兜底:每次 15s 心跳时主动 `svc.Get` 查状态,已终态→补发终态帧收流(慢/不读订阅者被非阻塞 bus 丢终态时不再永久挂);bus 非阻塞语义不变。
  4. `Cancel` 不再透传 `ErrInvalidTransition`:queued→failed 撞 worker 竞态时重查当前态,运行中→走运行中取消,终态→`ErrNotCancelable`(409)。
  5. worker panic recover 改 `service.failToTerminal`:读当前状态,非终态才按合法转移幂等置 failed(不再硬编码 running→failed 破坏 queued 语义)。
  6. 新增 `dbStepSink.reconcile`:run 落终态后按 run_id 校正所有非终态步骤(run 失败→failed / 成功→skipped)并补发 step 事件,杜绝"run=终态但 step 永远 running";panic/加载失败路径亦调用。
  7. 分页校验:HTTP 层 page 非数字/<1/>上界(100000)→ 400;`service.List` 额外钳制 page 上界防 OFFSET 溢出。
  新增导出 helper `IsStepTerminal`;新增测试 TestEnqueueNonBlocking/TestEnqueueAfterStop/TestReconcileHangingSteps(run)、TestRunListInvalidPage/TestSSETerminalSnapshotEndsStream(httpapi)。fmt/vet/test(含 -race ./internal/run ./internal/httpapi)/CGO_ENABLED=0 build/mem-check 全过。**注:删项目取消活跃 run 一条归并行 project 成员,run 包未改 project.go。**
