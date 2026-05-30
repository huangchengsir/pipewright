# Story 5.3: 通知内容自定义(模板 + 变量)(FR-21)

status: ready-for-dev
epic: 5
基线: master(24 story;5-1 notify Payload{Title,Body,Fields} done;5-2 NotifyHook 构 EventPayload 默认文案 done)
covers: FR-21 通知内容自定义 —— 模板 + 变量(项目名/分支/commit/状态/耗时/错误摘要等);未自定义用平台默认模板。
**迁移号:0019**

## As a / I want / So that
As a 管理员,I want 自定义通知内容(标题/正文模板 + 变量占位),未自定义时用合理默认,So that 通知信息符合我的习惯(如带上项目、分支、耗时、失败摘要)。

## Acceptance Criteria
1. **Given** 配置一个通知模板(标题/正文含 `{{project}}`/`{{branch}}`/`{{status}}` 等占位)**When** 事件触发(5-2 路由)**Then** 渲染该模板为 Payload 发出(占位被真实值替换)。
2. **Given** 未配置模板 **When** 事件触发 **Then** 用平台默认模板(= 5-2 现有 EventPayload 默认文案),行为不变。
3. **And** 模板按事件类型(可选按渠道)配置;未知占位渲染为空串或保留原文(不报错);渲染**绝不注入/不执行**(纯文本替换,无模板引擎 RCE 面)。
4. **And** 变量集冻结:`project`/`branch`/`commit`/`status`/`event`/`durationMs`/`runId`/`errorSummary` 等;`errorSummary` 经脱敏(若含已登记 secret 复用 mask 尽力,绝无明文)。

## ⚠️ 本机真验
- 配模板「构建失败:{{project}}@{{branch}} 挂了({{durationMs}}ms)」+ 5-2 的 build_failed→本地 webhook 路由 → 触发失败 run → **本地收到的 POST body 含渲染后的真实值**;未配模板→收到默认文案。

## ⛓ 冻结契约(模板模型 + 变量集 + 渲染,定死)
### 变量集(冻结;渲染上下文)
`{{project}}` `{{branch}}` `{{commit}}`(短)`{{status}}` `{{event}}` `{{durationMs}}` `{{runId}}` `{{errorSummary}}`。占位语法 `{{name}}`(纯文本替换,未知占位 → 空串)。
### Template 模型 + CRUD
```json
{ "id":"...", "event":"build_failed", "channelId":null,
  "titleTemplate":"构建失败:{{project}}", "bodyTemplate":"{{branch}}@{{commit}} 失败,耗时 {{durationMs}}ms\n{{errorSummary}}",
  "createdAt":"RFC3339" }
```
- `event` ∈ 5-2 冻结的 6 事件枚举;`channelId` 可空(空=该事件所有渠道通用;非空=仅该渠道)。
- `GET /api/notifications/templates` → `{items:[…]}` · `POST` 创建 · `PUT/DELETE /{id}`。
### 渲染(冻结,供 5-2 NotifyHook 消费)
- `notify.RenderPayload(ctx, event, channelID string, vars TemplateVars) Payload`——查最具体匹配模板(优先 channelId 精确 > 通用 > 平台默认)→ 纯文本替换占位 → Payload{Title,Body,Fields}。**无匹配 → 平台默认**(= 现 EventPayload)。
- 5-2 的 NotifyHook 改为:构 TemplateVars → 经 `RenderPayload` 产 Payload → SendVia(替换原硬编码默认文案路径;**默认行为不变**)。

> **冻结点:** Template 模型 + 变量集 + RenderPayload 签名定死。占位为纯文本替换(无 RCE);5-4 流水线级覆盖在此基础加 projectId 维度,不改形状。

## 文件切分(独立 worktree 完整 BE+FE)
**后端(Go):**
- `internal/store/migrations/0019_notification_templates.sql`:`notification_templates(id, project_id, event, channel_id, title_template, body_template, created_at)`(project_id 可空=全局,留 5-4;channel_id 可空=该事件通用;FK channel_id → notification_channels ON DELETE CASCADE)。
- `internal/notify/template.go` + `*_test.go`:Template 模型 + CRUD(进 notify.Service)+ `RenderPayload`(查最具体匹配 → 纯文本 `{{x}}` 替换 → Payload;未匹配回平台默认;`errorSummary` 经 mask 尽力脱敏)。**纯字符串替换,绝不用可执行模板引擎**(无 text/template 注入面;或用 text/template 但仅 `.Field` 无函数——你定,务必无 RCE)。
- `internal/httpapi/notify_hook.go`(改):构 TemplateVars(从 run 元数据 + 状态)→ 经 `RenderPayload` 产 Payload(替换原硬编码 EventPayload 默认;**默认路径行为不变**)。
- `internal/httpapi/notifications.go`(改):template CRUD handler + DTO。`router.go` **仅 append** 挂 `/api/notifications/templates*`(GET auth;写 auth+CSRF)。
**前端(web/):**
- `web/src/api/notifications.ts`(改):template CRUD。
- `web/src/views/settings/SettingsNotifications.vue`(改):加「通知模板」区(event/渠道 选择 + 标题/正文模板编辑 + 可用变量提示 + 预览可选)。**不动渠道/路由既有区**。

## Dev Notes 护栏
- **无 RCE**:占位渲染为纯文本替换(或 text/template 仅字段访问);用户模板**绝不**当代码执行。
- **脱敏**:errorSummary 经 mask 尽力(绝无明文 secret);渲染输出不泄密。
- 未配模板 → 平台默认(行为向后兼容 5-2)。匹配优先级:channelId 精确 > 通用 > 默认。
- 参数化 SQL;无 init 副作用;mem ≤100MB。

## 编排者亲验清单
- `export PATH="/Users/huangjiawei/sdk/go/bin:$PATH"`;gofmt/vet/`go test -race ./internal/notify/... ./internal/httpapi/...`/build/mem-check。
- 前端 typecheck(symlink node_modules)+ build。
- **真验:配 build_failed 模板含 {{project}}/{{branch}}/{{durationMs}} → 触发失败 run → 本地 webhook 收到渲染后真实值;未配→默认文案;{{未知}}→空串不报错;errorSummary 无明文密钥**;401/403。
- worktree 内 commit(分支 story/5-3)。
