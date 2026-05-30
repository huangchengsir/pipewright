# Story 1.7: 账户设置与首次使用引导

status: ready-for-dev
epic: 1
基线: master(含 1-6 组件库,**复用 components/ui/***)
covers: 账户设置(改口令/会话管理)· 首次使用引导(UX-DR11)

## As a / I want / So that
As a 管理员,I want 账户设置(改口令/会话管理)与首次使用引导(连 AI / 加服务器 / 建项目 3 步),So that 我能管理自己的账户,且新实例有清晰的从零上手路径。

## Acceptance Criteria
1. **Given** 账户设置 **When** 修改口令 **Then** 校验当前口令、更新为新 argon2id 哈希、**使其它会话失效**(当前会话保留);**And** 可查看并撤销活跃会话。
2. **Given** 全新实例(无项目) **When** 登录后 **Then** 进入引导:价值三卡 + 3 步清单(连 AI → 加服务器 → 建项目),步骤间锁依赖,可跳过且随时可在设置重开(UX-DR11)。
3. **And** 已有配置时不强制进引导,落到概览(空态带 CTA)。

## 范围边界(本期做 / 不做)
- ✅ 做:**账户设置全功能**(改口令 + 会话列表/撤销);**引导外壳**(价值三卡 + 3 步清单 + 跳过/重开);Overview 空态 CTA。
- ⚠️ 现实约束:引导的「连 AI」依赖 **7-1 AI 提供商**(未建)、「加服务器」依赖 **4-1 服务器登记**(未建)。本期这两步**前向声明**:显示为"未配置",点击跳到对应设置页(AI 设置已有占位 / 服务器页占位),**不阻断**;唯「建项目」步真实可用(检测项目存在)。引导完成度 = 仅「建项目」真实判定,其余步标"即将可用/前往配置"。
- ❌ 不做:AI 提供商真实连通(=7-1)· 服务器真实登记(=4-1)· 多用户/RBAC(v1 单管理员)。

## ⛓ 冻结 API 契约
Base:认证保护,写方法过 CSRF。错误体沿用 `{"error":{"code,message}}`。

### POST `/api/account/password`
请求 `{ "currentPassword": "...", "newPassword": "..." }` → 204。
- 校验当前口令(`VerifyPassword`);错→401 `invalid_current_password`。
- newPassword 规则(至少 8 位,非空):不满足→422 `weak_password`。
- 更新 `admin_user.password_hash` 为新 argon2id;**删除除当前会话外的所有会话**(当前 token 保留,用户不掉线)。
- 与当前会话相同口令也允许(不强制不同)。

### GET `/api/account/sessions`
→ 200 `{ "sessions": [ { "id":"<token 的 sha256 前缀,非原 token>", "createdAt":"RFC3339", "lastSeenAt":"RFC3339", "expiresAt":"RFC3339", "current": true } ] }`
- **绝不回传原始 session token**;`id` = token 的 sha256 hex 前 16 位(用于撤销定位)。`current` 标识本请求所属会话。

### DELETE `/api/account/sessions/{id}`
→ 204。按 `id`(sha256 前缀)定位并删除该会话;不存在→404 `session_not_found`;**撤销当前会话**亦允许(返回 204,前端随后 401 跳登录)。

> 引导状态**前端派生**:`hasProject` 由 `GET /api/projects` 列表长度判定;`hasAI`/`hasServer` 本期恒 false(7-1/4-1 未建,UI 标"即将可用")。跳过/已读用 localStorage(`onboarding_dismissed`)持久化;设置页「重新引导」清该 flag。**不引入引导专用后端端点**(避免为未建功能造端点)。

## 文件切分(零冲突,前后端;本 story 自成一对 BE+FE)
**后端(只动 Go):**
- `internal/auth/service.go`(改/加):`ChangePassword(current, new, currentToken string) error`(校验当前→更新 hash→删其它会话);`ListSessions(currentToken string) ([]SessionInfo, error)`;`RevokeSession(id string) error`。复用 `VerifyPassword`/`HashPassword`(0002 已有 argon2id PHC)。`SessionStore` 加 `List`/`DeleteByIDPrefix`/`DeleteOthers(keepToken)`(sha256 前缀定位,**不存原 token 之外可定位字段→用 token 自身 sha256 即时算**)。
- `internal/store/migrations/0010_account.sql`(**可选**):若要展示会话 UA/IP,给 `sessions` 加 `user_agent`/`ip` 列;否则本期不加迁移,仅用既有 created/last_seen/expires 列(Login 时已写)。**优先不加迁移**(UA/IP 是 nice-to-have,会话表已够列出+撤销)。
- `internal/httpapi/account.go` + `account_test.go`:三端点 + DTO + 错误映射。接 `requireAuth`+`requireCSRF`(写)。从请求 cookie 拿 currentToken 判定 `current`/保留当前会话。
- `router.go`(改):挂 `/api/account/password`(POST)、`/api/account/sessions`(GET)、`/api/account/sessions/{id}`(DELETE)。**改口令/撤销会话是敏感操作 → 接 1-4 的 `audit.Record`**(action 加 `password_change`/`session_revoke`;若 1-4 的 Action 枚举未含,在 audit 包补这两个枚举值——这是对 1-4 冻结契约的"只增不改语义"扩展,允许)。
- `main.go`(改):account handler 装配(传 auth.Service + recorder)。

**前端(只动 `web/`):**
- `web/src/api/account.ts`(新):`changePassword`/`listSessions`/`revokeSession`。
- `web/src/views/settings/SettingsAccount.vue`(改):占位换成真实——身份显示 + 改口令表单(当前/新/确认,**复用 1-6 `FormField`/`AppButton`**,422/401 定位)+ 活跃会话列表(当前标识 + 撤销按钮,**复用 `ConfirmDialog` 二次确认**)+ 「重新引导」按钮(清 localStorage flag)。
- `web/src/components/onboarding/OnboardingFlow.vue`(新):价值三卡 + 3 步清单(连 AI/加服务器=即将可用前向链接,建项目=真实 CTA→/projects),可跳过(写 localStorage)。参考 `mockups/mock-onboarding.html`。
- `web/src/views/Overview.vue`(改):占位换成空态——无项目时显示引导 CTA / 价值卡;有项目落概览空态(本期概览数据 = 后续,先空态带 CTA)。
- 引导触发:登录后若 `!hasProject && !localStorage.onboarding_dismissed` → 进引导(overlay 或 /onboarding 路由)。`web/src/router`(改,**仅加 /onboarding 路由**,与并行 story 边界:2-4 不碰 router)。

> **与 2-4 并行的边界**:1-7 后端碰 router.go/main.go/internal/auth + account.go;2-4 后端碰 router.go/main.go/internal/pipeline + pipelines handler。**两者都改 router.go/main.go → 各自 worktree 提交,编排者 git merge 三方合并**(增量在不同区域,git 自动合;冲突编排者解)。前端:1-7 动 SettingsAccount/Overview/onboarding/router;2-4 动 ProjectPipeline/pipeline 组件——**不重叠**。

## Dev Notes 护栏
- 改口令安全:argon2id(复用既有参数,别在 init 跑 argon2 抬内存——见教训);删其它会话用参数化 `DELETE ... WHERE token != ?`。
- 会话 id 用 token 的 sha256 前缀,**绝不回传原 token**。
- 复用 1-6 `components/ui/*`(FormField/AppButton/ConfirmDialog/EmptyState/StatusBadge),别重造。
- 引导步骤锁依赖:建项目前置可后,AI/服务器步因功能未建标灰"即将可用",不阻断跳过。
- `make mem-check` ≤100MB。

## 编排者亲验清单
- gofmt/vet/`go test ./...`/`-race ./internal/auth ./internal/httpapi`/build/mem-check。
- **前端必跑 `npm --prefix web run typecheck`(不是 build!)** + `npm --prefix web run build`。
- 真二进制:改口令(错当前→401 · 弱新→422 · 成功 204 + 旧其它会话失效 + 当前不掉线)· `GET /api/account/sessions` 不含原 token + current 标识 · 撤销会话 204 · 改口令入审计无明文。
- 真浏览器:全新库登录→引导(三卡+3步,建项目 CTA,AI/服务器"即将可用")· 跳过→概览空态 · 账户设置改口令表单 + 会话列表 + 撤销二次确认 + 重新引导 · 截图肉眼审质量(复用 1-6 组件、无模板感)。
