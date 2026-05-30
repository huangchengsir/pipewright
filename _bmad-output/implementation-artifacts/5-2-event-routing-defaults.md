# Story 5.2: 通知事件路由与默认(FR-20)

status: ready-for-dev
epic: 5
基线: master(21 story;5-1 `internal/notify`(Notifier.Send / Service.SendVia / Payload)done;run 终态事件可观察)
covers: FR-20 通知事件 + 细粒度路由 —— 「事件 → 渠道」映射;未配置路由的事件不发送。本期:路由引擎 + 全局默认。每流水线覆盖=5-4、模板=5-3。
**迁移号:0018**

## As a / I want / So that
As a 管理员,I want 配置「哪些事件经哪些渠道通知」(构建成功/失败、部署成功/失败、回滚、健康检查失败),且未配置的事件不打扰我,So that 我只在关心的事件上收到通知。

## Acceptance Criteria
1. **Given** 配置「构建失败 → webhook 渠道X」**When** 一次运行失败 **Then** 经渠道X 发出通知(经 5-1 `SendVia`),内容含项目/分支/状态/耗时等。
2. **Given** 某事件未配置任何路由 **When** 该事件发生 **Then** **不发送**(FR-20:未配置不发)。
3. **And** 路由按事件类型 + 渠道映射;支持全局默认(项目维度覆盖留 5-4 在此基础加 projectId 维度)。
4. **And** 通知发送为 **best-effort**:发送失败仅记日志,**绝不阻断 run 终态 / 不 500**(NFR-10 精神)。

## ⚠️ 本机真验
- 配「构建失败 → 本地 webhook 渠道(5-1 建,指 httptest/`python3 -m http.server`)」→ 触发一个失败 run → **本地真收到 POST**(含事件 payload)。未配路由的事件→不发(断言无请求)。

## ⛓ 冻结契约(路由模型 + 事件枚举 + hook,定死)
### 事件枚举(冻结)
`event` ∈ **`build_succeeded` | `build_failed` | `deploy_succeeded` | `deploy_failed` | `rollback` | `health_check_failed`**。
- run 终态 → 事件映射:success→build_succeeded;failed→build_failed;partial_failed/有部署→deploy_failed;rolled_back→rollback。(本期以 run 终态驱动;部署/健康事件随 4-x 细化,event 枚举不变。)
### Route 模型 + CRUD
```json
{ "id":"...", "event":"build_failed", "channelId":"<5-1 渠道>", "enabled":true, "createdAt":"RFC3339" }
```
- `GET /api/notifications/routes` → `{items:[…]}` · `POST` 创建 · `DELETE /{id}` · (PUT 可选)。一个事件可映射多渠道(多条 route)。
- 本期全局默认(无 projectId);5-4 加 `projectId` 维度覆盖(契约预留:route 可带可空 `projectId`,本期恒空=全局)。
### 路由引擎(冻结,供 hook 调用)
- `internal/notify` 加 `Router`:`RouteEvent(ctx, event string, payload Payload) error`——查该 event 的所有 enabled route → 对每个 channelId 调 `SendVia`(未配 route → 不发,返回 nil)。best-effort:单渠道失败记日志续发其余。
- **worker pool 注入 NotifyHook**(仿 7-2 DiagnoseHook:`func(ctx, runID, finalStatus)`;run 包不 import notify;main 装配钩子内「run 终态→event→构 Payload→Router.RouteEvent」)。

> **冻结点:** event 枚举 + Route 模型 + Router.RouteEvent + NotifyHook 签名定死。5-3 模板产 Payload、5-4 加 projectId 维度,均不改这些。

## 文件切分(独立 worktree 完整 BE+FE)
**后端(Go):**
- `internal/store/migrations/0018_notification_routes.sql`:`notification_routes(id, project_id, event, channel_id, enabled, created_at)`(project_id 可空=全局;FK channel_id → notification_channels ON DELETE CASCADE)+ 索引 `(event)`。
- `internal/notify/router.go` + `*_test.go`:`Router` + `RouteEvent`(查 route → SendVia 每渠道;未配不发;best-effort)。route CRUD 进 `notify.Service`(List/Create/Delete Route)。**默认 Payload 构造**(项目名/分支/commit/状态/耗时;模板=5-3 后接管,本期内置默认文案)。
- `internal/run/pool.go`(改):加 `WithNotifyHook(fn func(ctx, runID, finalStatus string))` + run 终态后调用(仿 diagnoseHook;**best-effort,独立 goroutine + recover,不阻断终态**)。**注意:本 story 是唯一改 pool.go 的(避免与 4-2 冲突)。**
- `internal/httpapi/notifications.go`(改)+ 路由 CRUD handler;`router.go`+`main.go`:**仅 append** 挂 `/api/notifications/routes*`;装配 NotifyHook(main 内「终态→event→Payload→Router.RouteEvent」注入 pool)。
**前端(web/):**
- `web/src/api/notifications.ts`:route CRUD。
- `web/src/views/settings/SettingsNotifications.vue`(改):加「事件路由」区(event → 渠道 映射表 + 增删 + 启用开关)。复用既有渠道列表页,**仅在该页加区块,不动渠道 CRUD**。

## Dev Notes 护栏
- **未配路由不发**(FR-20 硬性);**best-effort 不阻断 run 终态**(独立 goroutine+recover,仿 diagnoseHook 信号量精神可选)。
- run 包不 import notify(NotifyHook 注入解耦);Payload 内容经 5-1 Payload 形状;**本 story 是唯一改 pool.go 者**。
- 参数化 SQL;无 init 副作用;mem ≤100MB。

## 编排者亲验清单
- `export PATH="/Users/huangjiawei/sdk/go/bin:$PATH"`;gofmt/vet/`go test -race ./internal/notify/... ./internal/run/... ./internal/httpapi/...`/build/mem-check。
- 前端 typecheck(symlink node_modules)+ build。
- **真验:配「build_failed→本地 webhook」→ 触发失败 run → 本地真收到 POST(payload 含项目/分支/状态);未配事件不发(无请求);发送失败不阻断 run 终态**;401/403。
- worktree 内 commit(分支 story/5-2)。
