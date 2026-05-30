# Story 3.6: 实时运行日志 + 源码读取端点(FR-16 实时侧 / FR-4 预埋)

status: ready-for-dev
epic: 3
基线: master(17 story done;3-1 run-detail DTO/5 态骨架/SSE 事件总线已冻结;3-2 webhook/手动触发;7-2 失败日志落 pipeline_runs.failure_log)
covers: 运行"进行中"态纯黑终端 SSE 实时日志(UX-DR6/7)+ 日志历史回放 + **源码/产物读取端点预埋(供 FR-4=7-4 代码浏览复用)**

## As a / I want / So that
As a 管理员,I want 运行进行中时实时看到逐行日志(纯黑终端)、刷新后能回放完整历史日志,并有一个只读源码读取端点,So that 我能观察运行过程、事后排查,且 Epic 7 代码浏览可直接复用源码端点。

## Acceptance Criteria
1. **Given** 一次运行进行中 **When** 打开运行详情 **Then** SSE 推送逐行日志,纯黑终端实时 tail(随步骤推进滚动),不阻塞页面。
2. **Given** 运行已结束(成功/失败)**When** 刷新/重新打开详情 **Then** 从持久化日志**完整回放历史**(非只在内存),按序呈现。
3. **And** 日志行**出网/持久化前经 secret 脱敏**(复用 1-4 `mask.Masker`,AC-SEC-04;[MASKED] 呈现),日志中绝无明文凭据。
4. **And** 源码读取端点(只读、认证保护)能列目录树 + 读单文件内容(按 ref/path),为 7-4 代码浏览预埋;路径穿越被拒、二进制文件标注不返原始字节流。
5. **And** CI ≤100MB 断言仍绿(日志不全量驻留内存:SSE 有界缓冲 + 落库,大日志分页)。

## ⚠️ 本机约束(亲验须知)
- **无真实构建**(3-3 卡 Docker)→ 桩 runner 现合成失败日志,本期让**桩 runner 逐行 emit 多行真实感日志**(每步若干行 stdout + 失败步 stderr),驱动实时流 + 持久化 + 回放,全链本机可验。真实构建日志在 3-3 落地后自然接入(StepSink.Log 接口不变)。
- **源码端点**用 go-git(复用 2-5 analyze 的浅克隆能力)读 ref 下的树/文件;本机用 file:// 夹具裸仓库验(复用 test-only allowInsecure)。GFW 拦 gitee 时降级:克隆失败 → 端点 200 + 空树 + degraded 说明(不 500)。

## ⛓ 冻结契约
### SSE 扩展(复用 3-1 既有 `GET /api/runs/{id}/events` 单流;新增 log 事件类型,不改既有 step/status 事件形状)
新增事件 `log`:
```
event: log
data: {"seq":42,"ts":"RFC3339","stream":"stdout","stepOrdinal":1,"text":"npm install …(已脱敏)"}
```
- `seq` 单调递增(每 run 内,从 1 起;前端据此去重/排序/断线续传)· `stream` ∈ `stdout|stderr` · `stepOrdinal` 关联步骤(可 -1 表示运行级)· `text` 单行已脱敏文本(无尾换行)。
- **连接即回放**:SSE 建连时先按 seq 升序补发该 run 全部历史 log 事件(让刷新者拿到完整日志),再转入实时;之后实时行随 emit 推送。
- 既有 `step`/`status` 事件**形状不变**(3-1 冻结)。

### GET `/api/runs/{id}/logs`(认证,只读;非 SSE 历史拉取 / 分页)
查询 `?sinceSeq=<int>`(默认 0)→ 200
```json
{ "lines": [ { "seq":1, "ts":"RFC3339", "stream":"stdout", "stepOrdinal":0, "text":"…" } ], "nextSeq": 43, "complete": true }
```
- 返回 `sinceSeq` 之后的行(升序),`nextSeq`=末行 seq+1,`complete`=该 run 是否已终态(前端据此停止轮询)。run 不存在 → 404 `run_not_found`。

### 源码读取端点(只读,认证;**FR-4 预埋,7-4 前端消费;本 story 定义并冻结形状**)
项目维度按 ref 读仓库源码(go-git 浅取;ref 默认项目默认分支)。
- **GET `/api/projects/{id}/source/tree?ref=<ref>&path=<dir>`** → 200
  ```json
  { "ref":"main", "path":"", "entries":[ {"name":"src","path":"src","type":"dir"}, {"name":"go.mod","path":"go.mod","type":"file","size":120} ] }
  ```
- **GET `/api/projects/{id}/source/blob?ref=<ref>&path=<file>`** → 200
  ```json
  { "ref":"main", "path":"go.mod", "size":120, "binary":false, "truncated":false, "content":"module …\n" }
  ```
  - `binary=true` 时 `content` 为空 + 不返原始字节(前端显"二进制文件不可预览");`truncated=true` 当超过单文件上限(如 512KB,防 OOM)。
- 路径规范化拒穿越(`..`、绝对路径、越出仓库根)→ 400 `invalid_path`;文件不存在 → 404 `path_not_found`;项目不存在 → 404 `project_not_found`;克隆失败 → 200 degraded(tree 空 entries + `degraded:true` 说明,不 500)。

> **冻结点:** log 事件(seq/ts/stream/stepOrdinal/text)+ logs 拉取 DTO + source tree/blob DTO 形状定死。3-3 真实构建只经 StepSink.Log 喂行、不改事件形状;7-4 只消费 source 端点、不改其形状。

## 文件切分(零冲突,前后端并行;同一棵树)
**后端(只动 Go):**
- `internal/store/migrations/0013_run_logs.sql`:`run_logs(id, run_id, seq, ts, stream, step_ordinal, text)`,索引 `(run_id, seq)`。seq 每 run 单调(service 维护)。**text 落库前已脱敏**。
- `internal/run/runner.go`+`pool.go`+`bus.go`+`service.go`(改):StepSink 加 `Log(ctx, stream string, stepOrdinal int, line string) error`(runner 报告日志行);`dbStepSink.Log` 脱敏→落 run_logs(分配 seq)→ 经 bus 发 `EventLog`。新增 `EventLog EventKind="log"` + Event 携带 log 负载(seq/ts/stream/stepOrdinal/text)。RunService 加 `AppendLog`/`GetLogs(id, sinceSeq)`。**StubRunner 每步 emit 若干行**(成功步 stdout、失败步追加 stderr,含一个假 secret 验脱敏)。脱敏复用注入的 `mask.Masker`(项目凭据登记;同 7-2 的 registerRunSecrets 债,沿用并标注)。
- `internal/httpapi/runs.go`(改):SSE handler 建连先回放历史 log(GetLogs(id,0))再订阅实时;新增 `GET /api/runs/{id}/logs` handler(分页 DTO)。EventLog → SSE `log` 事件。
- `internal/httpapi/source.go`(新)+`_test.go`:`GET /api/projects/{id}/source/tree|blob` handler——go-git 浅克隆(复用 2-5 analyzer 的 clone 能力或新建只读 reader)读 tree/blob;路径穿越校验;binary 检测(含 NUL 字节)+ 512KB 截断;克隆失败 degraded。SSRF 复用 2-5 validRepoURL + test-only allowInsecure。
- `router.go`+`main.go`(改):挂 `/api/runs/{id}/logs`(auth)+ `/api/projects/{id}/source/{tree,blob}`(auth);source handler 需 project 服务 + vault(取仓库凭据)+ go-git reader 装配。
**前端(只动 `web/`):**
- `web/src/api/runs.ts`(改):run 事件流加 `log` 事件类型;`getRunLogs(id, sinceSeq)`。
- `web/src/components/run/RunTerminal.vue`(新):纯黑终端组件——逐行渲染(stdout/stderr 着色)、自动滚底(用户上滚则暂停自动滚)、seq 去重、行号、命中 [MASKED] 标识。复用 1-6 tokens(--color-term 纯黑、JetBrains Mono)。
- `web/src/views/RunDetail.vue`(改):**"进行中"态填入 RunTerminal(SSE 实时)**;**"成功/失败"态也挂 RunTerminal(历史回放,只读)**。不碰其它 slot 所有权(诊断面板=7-2、骨架=3-1)。

## Dev Notes 护栏
- **AC-SEC-04**:日志行落库/出网/SSE 前一律 `mask.Masker.Scrub`;桩日志含假 secret → run_logs 表/SSE/响应 [MASKED] 无明文(自动化测试断言)。
- **内存(NFR-4)**:SSE 不把全量日志驻留;历史回放从 DB 流式读;单文件 source 读 512KB 截断;大日志 logs 端点分页(sinceSeq)。
- **源码端点只读**:绝不写仓库;路径穿越拒;二进制不返字节流;克隆失败 degraded 不 500。
- **StepSink.Log 接口冻结**:3-3 真实构建只喂行、不改形状;源码端点形状冻结、7-4 只消费。
- 参数化 SQL;无 init 副作用;seq 分配并发安全(service 内单 run 串行或 DB 自增按 run)。

## Tasks
1. [后端] 迁移 0013 + StepSink.Log + EventLog + bus/service AppendLog/GetLogs + StubRunner 逐行 emit(含假 secret)+ 脱敏落库 + 单测(脱敏断言、seq 单调、历史回放)。
2. [后端] SSE 建连回放历史 + 实时 log 事件 + `GET /runs/{id}/logs` 分页 + 单测。
3. [后端] `source.go` tree/blob 端点(go-git 读、路径穿越拒、binary/截断、degraded)+ 路由 + main 装配 + 单测(file:// 夹具)。
4. [前端] runs.ts log 事件 + getRunLogs;`RunTerminal.vue`(着色/自动滚/去重/[MASKED])+ RunDetail 进行中&终态挂载。

## 编排者亲验清单(派活后必跑,别只信 agent 自检)
- gofmt/vet/`go test ./...`/`-race ./internal/run ./internal/httpapi`/build/`make mem-check`。
- **前端必跑 `npm --prefix web run typecheck`(不是 build!)** + build。
- 真二进制:触发运行 → SSE `log` 事件逐行到达(curl -N 看流)· 运行结束后 `GET /runs/{id}/logs` 回放完整历史 · **桩日志假 secret → run_logs 表/SSE/响应 [MASKED] 无明文** · source/tree 列 file:// 夹具树 · source/blob 读文件 · 路径穿越 `../etc/passwd` → 400 · 二进制文件 binary=true 不返字节 · 克隆失败 degraded 不 500 · run 不存在 404 · 未认证 401。
- 真浏览器:运行详情进行中态纯黑终端实时 tail(逐行/着色/自动滚)· 刷新终态页历史日志完整回放 · **截图肉眼审(看截图非正则,selector 精确,等够异步)**。
