# Story 5.1: 通知渠道(FR-19)

status: ready-for-dev
epic: 5
基线: master(18 story;1-3 vault;1-6 组件库)
covers: FR-19 多渠道通知 —— **渠道模型 + Notifier 抽象在此定死**(5-2 路由 / 5-3 模板 / 5-4 覆盖消费)。本期实现 **webhook + email** 两渠道,企业微信/钉钉/飞书脚手架占位。
**迁移号:0016**(已分配)

## As a / I want / So that
As a 管理员,I want 配置通知渠道(自定义 webhook、邮件等)、测试发送,So that 后续构建/部署事件能经这些渠道通知我。

## Acceptance Criteria
1. **Given** 配置一条 webhook 渠道(URL)或 email 渠道(SMTP)**When** 保存 **Then** 入库(敏感字段如 SMTP 密码经 vault 加密,绝无明文)。
2. **Given** 已配置渠道 **When** 测试发送 **Then** 发一条测试通知,回显成功/延迟或人读错误(webhook 非 2xx / SMTP 失败 → 人读)。
3. **And** Notifier 抽象 `Send(ctx, channel, payload)` 定死,供 5-2 事件路由消费;未配渠道不发送、不报错。
4. **And** webhook URL 经 SSRF 收口(复用既有 validURL:拒云元数据 169.254/链路本地;允许私网自托管);敏感字段绝不在响应/日志明文。

## ⚠️ 本机可验
- **webhook**:指向本地起的接收器(`nc -l` 或 python `http.server`)→ 真收到 POST。**email**:指向本地 stub SMTP(python `aiosmtpd` 或 `python -m smtpd`)→ 真投递。无需外网。

## ⛓ 冻结契约(Notifier + 渠道模型,定死)
### Channel 模型 + CRUD
```json
{ "id":"...", "name":"运维群 webhook", "type":"webhook", "enabled":true,
  "config":{ "url":"https://..." },                 // webhook
  "createdAt":"RFC3339", "updatedAt":"RFC3339" }
```
- `type` ∈ **`webhook` | `email` | `wecom` | `dingtalk` | `feishu`**(枚举冻结;本期实现 webhook+email,其余 type 保存合法但 Send 返回 `not_implemented` 人读提示)。
- `config` per-type:webhook=`{url}` · email=`{smtpHost,smtpPort,from,to,username,secretRef?}`(SMTP 密码 write-only,经 vault;响应仅 `hasPassword:bool`)。**敏感字段 write-only + 掩码**,仿 7-1 apiKey。
- `GET /api/notifications/channels` → `{items:[…]}` · `POST` 创建 · `GET/PUT/DELETE /{id}`。
### POST `/api/notifications/channels/{id}/test`(认证+CSRF)→ 200 `{ "ok":bool, "latencyMs":number, "detail":string, "error":string|null }`
- 发测试 payload;webhook=POST JSON、email=SMTP 发信。失败人读;**绝无明文敏感字段**。
### `internal/notify` 导出(冻结):
- `type Notifier interface { Send(ctx, ch *Channel, payload Payload) error }` · `Payload{ Title, Body string; Fields map[string]string }`(5-3 模板渲染后传入)。
- `type Service interface { CRUD + Test(ctx,id)(*TestResult,error) + SendVia(ctx, channelID string, payload Payload) error }`。

> **冻结点:** Channel DTO + type 枚举 + Notifier.Send + Payload 形状定死。5-2 路由按 channelID 调 SendVia;5-3 模板产 Payload;5-4 覆盖不改这些。

## 文件切分(独立 worktree 完整 BE+FE)
**后端(Go):**
- `internal/store/migrations/0016_notification_channels.sql`:`notification_channels(id, name, type, enabled, config_json, secret_ciphertext BLOB, created_at, updated_at)`(secret 仅密文)。
- `internal/notify/notify.go` + `webhook.go` + `email.go` + `*_test.go`:Channel 模型 + Service(CRUD〔secret write-only 保留语义,仿 7-1〕+ Test + SendVia)+ Notifier 实现(webhook=注入 *http.Client POST JSON〔SSRF 收口、MaxBytes、超时〕;email=`net/smtp` 发信)。未实现 type → `not_implemented` 人读。
- `internal/httpapi/notifications.go` + `_test.go`:CRUD + test handler + DTO + 错误映射;webhook URL SSRF 校验(自包含一份或复用模式,拒元数据/链路本地)。
- `router.go` + `main.go`:**仅 append** 挂 `/api/notifications/channels*`(GET auth;写 auth+CSRF);`notify.New(db, vault, httpClient)` + `WithNotifications` Option。
**前端(web/):**
- `web/src/api/notifications.ts`(新):CRUD + testChannel。
- `web/src/views/settings/SettingsNotifications.vue`(新):渠道列表 + 新增/编辑(type 选 webhook/email + per-type 字段;email 密码 write-only 掩码)+ 测试发送(显结果)。复用 1-6 ui。
- 设置导航 + 路由:**仅 append** 一个「通知」设置项(别动别人的)。

## Dev Notes 护栏
- **AC-SEC**:SMTP 密码等仅 vault 密文,响应/日志/错误绝无明文;webhook SSRF 收口。
- Notifier/Payload 定死供 5-2~5-4 消费;未配/未启用渠道不发、不报错(优雅)。
- 参数化 SQL;无 init 副作用;mem ≤100MB;**不改 go.mod**(net/smtp + net/http 均 stdlib)。

## 编排者亲验清单
- `export PATH="/Users/huangjiawei/sdk/go/bin:$PATH"`;gofmt/vet/`go test -race ./internal/notify/... ./internal/httpapi/...`/build/mem-check。
- 前端 typecheck(symlink node_modules)+ build。
- **真验:webhook 指本地 `python3 -m http.server`/`nc -l` → 测试发送真收到 POST;email 指本地 stub SMTP → 真投递**;裸 DB 无明文 SMTP 密码;SSRF 拒 169.254;404/401/403。
- worktree 内 commit(分支 story/5-1)。
