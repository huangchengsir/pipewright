---
baseline_commit: NO_VCS
---

# Story 2.3: 触发设置与分支映射(FR-1 / FR-2)— M1 最小前向兼容

Status: done

> 作者拍板:做**最小前向兼容**版。本期实现 webhook 端点+密钥+事件开关+分支模式(通配)+未匹配策略 + 分支→环境/服务器**目标列建模占位**;**服务器存在性校验延后到 4-1 落地**(目标服务器表此刻不存在)。作为**独立项目配置页** `/projects/:id/triggers`(Story 2-2 后续把它并入 4-tab 编辑器)。解锁 3-2 webhook 触发,不返工。
> 后端/前端并行,经冻结契约对接。依赖 1-2/1-3/1-5/2-1 ✅。webhook **接收**端点(收 Gitee 推送创建运行)是 **3-2**,本期只做**配置**。

## Story

As a 管理员,
I want 为项目配置 webhook 接收端点 URL 与签名密钥、勾选触发事件、并维护「分支(通配)→ 环境 + 目标服务器」映射,
so that 指定分支的 push/tag/PR 能在 Epic 3 触发对的环境与服务器(执行在 3-2)。

## Acceptance Criteria

1. **AC1 — Webhook 端点 + 签名密钥(FR-1)**:触发设置页显示该项目**独立**的 webhook 接收端点 URL(形如 `/api/webhooks/{token}`)+ 签名密钥;密钥**掩码呈现**,可**复制**(供粘贴到 Gitee;复制取真实值)、可**重置**(重置后旧值失效、新值仅在重置响应里回显一次);密钥**加密入库**(复用 vault secretbox / master key),DB dump 无明文。
2. **AC2 — 触发事件**:可勾选 `push` / `tag` / `PR-MR`;**手动触发常开**(不可关,展示为常开态)。配置持久化。
3. **AC3 — 分支映射(FR-2)**:分支映射表 `分支模式 → 环境 + 目标服务器`;支持**通配**(`main`、`release/*`、`dev`);一分支模式可映射**多个**目标服务器;持久化。**本期**:环境为名称(文本),目标服务器为**引用 id 列表**(目标服务器表尚不存在,列建模占位、可空);**服务器存在性校验留 TODO,4-1 落地后启用**(联动 FR-9)。
4. **AC4 — 未匹配策略**:未匹配任何映射的分支可选「记录但不部署」或「忽略」,持久化。

## 冻结的触发配置 API 契约(前后端共享,不得擅改)

- **读取** `GET /api/projects/{id}/trigger` → 200 `{ "webhookUrl":"/api/webhooks/<token>", "webhookSecretMasked":"whsec_••••a91f", "events":{"push":true,"tag":false,"pullRequest":false}, "branchMappings":[{"id","branchPattern":"main","environment":"生产","targetServerIds":[]}], "unmatchedPolicy":"record"|"ignore" }`(secret 仅掩码;首次访问无配置则返回默认:随机 token、生成密钥、events 全 false 但 manual 隐含常开、空映射、unmatchedPolicy=record)。
- **重置密钥** `POST /api/projects/{id}/trigger/secret/reset` → 200 `{ "webhookSecret":"<完整明文,仅此一次>", "webhookSecretMasked":"..." }`(重新生成并加密入库,旧值失效)。需 CSRF。
- **保存配置** `PUT /api/projects/{id}/trigger` body `{ "events":{push,tag,pullRequest}, "branchMappings":[{branchPattern,environment,targetServerIds}], "unmatchedPolicy" }` → 200 更新后对象(掩码)。需 CSRF。校验:branchPattern 非空且为合法通配;`targetServerIds` 本期不校验存在性(TODO 标注,4-1 后启用);非法 → 400/422 `{error:{code,message}}` 定位到项。
- 全部过 1-2 requireAuth + 写方法 requireCSRF;JSON camelCase;错误体永不含 secret 明文。
- **复制密钥**:UI 的"复制"复制的是**重置时回显的明文**(用户需粘贴到 Gitee);常态只存掩码态,要拿明文须重置。(避免设单独 reveal 端点泄露面。)

## Tasks / Subtasks

- [x] **Task 1 — trigger 领域 + 迁移**(AC: 全部)
  - [x] `internal/store/migrations/0005_pipeline_triggers.sql`:`pipeline_triggers`(`project_id` PK/FK→projects ON DELETE CASCADE、`webhook_token` UNIQUE、`webhook_secret_ciphertext` BLOB、`events_json` TEXT、`branch_mappings_json` TEXT、`unmatched_policy` TEXT、`created_at`、`updated_at`)。snake_case;**密钥只存密文**。
  - [x] `internal/trigger/trigger.go`:`Service`(Get/Save/ResetSecret);webhook_secret 用 **vault secretbox 加密**(复用 master key;新增导出 `vault.SealSecret/OpenSecret`),master key 未配置则读取/重置降级为 `ErrVaultUnconfigured`,不 panic。token + 密钥用 crypto/rand。branchMappings/events 以 JSON 存。
- [x] **Task 2 — HTTP 端点**(AC: 全部)
  - [x] `internal/httpapi/triggers.go`:`GET /api/projects/{id}/trigger`、`PUT /api/projects/{id}/trigger`、`POST /api/projects/{id}/trigger/secret/reset`,过 requireAuth + 写方法 requireCSRF;DTO 掩码;经 `WithTriggers` Option 装配进 main.go。首次 GET 无配置 → 惰性生成默认(token+密钥)并返回。
- [x] **Task 3 — 校验**(AC: 3)
  - [x] branchPattern 合法性(非空、无空白、通配语法 `*`、拒纯 `*`/`**`);`targetServerIds` 存在性校验**留 TODO 注释 + 占位函数 `validateTargetServerIDs`**(4-1 后接 target_servers,本期不拦存在性);非法 branchPattern → 422 `{error:{code,message}}`;events 结构合法。
- [x] **Task 4 — 测试**
  - [x] Go test:默认惰性生成、Save 往返、ResetSecret 旧值失效+新值加密入库、整库 dump 无密钥明文(AC-SEC)、非法 branchPattern 422、CSRF/auth 拦截、密钥 GET 只掩码不回显明文、删项目级联删 trigger、vault 未配置降级错误。
- [x] **Task 5 — 前端 触发设置页**(AC: 全部)
  - [x] `web/src/api/triggers.ts`:getTrigger/saveTrigger/resetSecret(走 http,带 CSRF)。
  - [x] `web/src/views/ProjectTriggers.vue`(新)+ 路由 `/projects/:id/triggers`(壳内;项目卡「配置」入口接通):Webhook 区(端点 URL 只读+复制、签名密钥掩码+重置二次确认+新值一次性回显可复制)、事件区(push/tag/PR 开关,手动触发常开禁用态)、分支映射表(分支模式+环境+目标服务器占位"4-1 后启用"+增删)、未匹配策略分段、保存。
  - [x] 字段错误(非法分支模式)走 EXPERIENCE 状态规范;走 1-5 令牌/组件;密钥**常态只掩码**;reduced-motion 降级。

## Review Findings(2026-05-30 第二轮三层对抗评审 · 后端 A 块)

Patch(无歧义可直接修):
- [x] [Review][Patch] 触发配置惰性默认 `Get` 在无行时无条件 `INSERT`,两个并发首访同项目 → 第二个撞 project_id 主键唯一冲突 → 走 default 分支返 `insert default config` → HTTP 500 → 改 `INSERT ... ON CONFLICT DO NOTHING` 后重新 load,或唯一冲突重试 load [internal/trigger/trigger.go Get/createDefault](edge,Medium)

Change Log(2026-05-30 后端 A 块 patch):
- trigger.go createDefault:`INSERT ... ON CONFLICT(project_id) DO NOTHING`;据 `RowsAffected()==0`(竞态败者)回读权威行(`s.load`)返回,保证返回的 token/掩码与库内一致(并发下另一协程已写入的随机 token 不被覆盖/不脱节),消除并发首访 500。pipeline_triggers `project_id` 为 PK,ON CONFLICT 目标成立。

AC 复核(auditor):2-3 AC1-4 全满足(webhook 端点 + 密钥 vault 加密入库只掩码 + reset 一次性回显 + 事件/手动常开 + 分支映射通配 + targetServerIds 占位 + 未匹配策略 + 级联删);非法 branchPattern 422 符合契约。webhook 验签相关发现见 [[3-2-webhook-and-manual-trigger]]。

## Dev Notes

### 必守护栏
- **密钥加密入库**:webhook 签名密钥经 vault secretbox(master key)加密,DB 无明文(AC-SEC-01 同理);常态只掩码,复制取重置时一次性回显值。**绝不**入日志/常态响应。
- **边界/命名/格式**:trigger 经 store/repository + vault;httpapi 唯一对外面;DB snake_case / JSON camelCase / Go PascalCase / `Service`/`ErrXxx`/RFC3339;成功返资源对象;错误体永不含 secret。沿用 1-1 sqlite pragma + 迁移事务。modernc 纯 Go 禁 CGO。
- **前向兼容占位**:`targetServerIds` 列/字段就位但本期不校验存在性(留 TODO,4-1 接);环境为文本名(2-4 落地后可升级为环境引用)。**不要**因为依赖未建就跳过这些字段——建模占位以免 3-2/4-x 返工。
- **2-3 边界**:只做触发**配置**;webhook **接收**端点(验签 + 创建运行)= Story 3-2,本期不实现接收逻辑。作为独立页 `/projects/:id/triggers`,2-2 后并入 tab。
- **≤100MB**:勿 init() 跑重活;mem-check 不回退。

### 环境
- Go 在 `~/sdk/go/bin`(`export PATH`);拉依赖加 `dangerouslyDisableSandbox`。npm=npmmirror。测试 vault/密钥路径需 `DEVOPSTOOL_MASTER_KEY`。

### 禁止
- ❌ 密钥明文入库/日志/常态响应;❌ 本期实现 webhook 接收/验签(=3-2);❌ 塞流水线画布(=2-2)或构建/部署配置(=2-4);❌ CGO sqlite;❌ 后端碰 web/ 或前端碰 Go;❌ init() 跑重活;❌ 回退既有安全修复。

### References
- [Source: epics.md#Epic 2 / Story 2.3]
- [Source: prd.md#FR-1(webhook 触发)/ FR-2(分支→环境/服务器映射,通配,一对多)/ FR-9(保存校验)]
- [Source: architecture.md#Authentication & Security / Implementation Patterns / 强制项]
- [Source: EXPERIENCE.md#流水线配置·4标签(触发设置)/ State Patterns(字段错误)]
- [Source: DESIGN.md#Components(输入/开关/分段/表格/掩码字段/二次确认)/ mock-pipeline-triggers.html(结构参考)]
- [Source: 1-3(vault secretbox 加密复用)/ 2-1(projects 表 + 项目卡「配置」入口)/ 1-5(http 包装器、壳、路由)]

## Dev Agent Record

### Agent Model Used

claude-sonnet-4-6 (Task 5 — 前端)
claude-opus-4-8 (Task 1–4 — 后端,纯 Go)

### Debug Log References

- vue-tsc --noEmit: 零错误
- vite build: ✓ 2.16s, ProjectTriggers chunk 22.07 kB / 6.79 kB gzip
- 后端:`gofmt -l`(空)、`go vet . ./cmd/... ./internal/...`(干净)、`go test ./...`(全绿)、`CGO_ENABLED=0 go build ./cmd/devopstool`(ok)、`go mod tidy`(无变更)、`make mem-check` = 20 MB(≤100 MB,未回退)。
- 真实二进制冒烟:登录 → 建 git_token 凭据 → file:// 裸仓建项目 → `GET /trigger`(惰性默认,`whsec_••••3760` 掩码)→ `PUT /trigger`(events+映射,id 自动补全,policy=ignore)→ 非法 branchPattern → 422 `invalid_branch_pattern` → `POST /secret/reset`(一次性回显 `whsec_52fb…d18e`,新掩码 `whsec_••••d18e`)→ GET 仅掩码 → 整库三文件(db/-wal/-shm)grep 无密钥明文/密钥体,而 `webhook_secret_ciphertext` 为 94B BLOB(密钥已加密入库)。

### Completion Notes List

- `web/src/api/triggers.ts`: getTrigger/saveTrigger/resetSecret,类型完整对齐冻结契约(TriggerConfig / SaveTriggerInput / SecretResetResult / BranchMapping / UnmatchedPolicy);走 http 带自动 CSRF。
- `web/src/views/ProjectTriggers.vue`: 新建独立页;Webhook 区(只读 URL + 复制、掩码密钥 + 重置二次确认 + 一次性明文回显 + 复制)、事件区(push/tag/PR 开关 + 手动常开禁用态)、分支映射表(增删行 + 字段级 branchPattern 校验 + 目标服务器占位)、未匹配策略分段控件、保存按钮;双主题成立;prefers-reduced-motion 降级;骨架屏加载态;错误态可重试;密钥常态只掩码,明文仅 revealedSecret 变量持有直到用户关闭回显。
- `web/src/router/index.ts`: 添加 `/projects/:id/triggers` 路由(AppShell 壳内,requiresAuth 继承)。
- `web/src/views/Projects.vue`: 项目卡「配置」按钮从 disabled 改为 @click 导航到 project-triggers 路由。
- targetServerIds 列建模占位(本期前端展示为虚线占位框 + 说明文字"目标服务器将在 Story 4-1 后启用"),字段已在 API 类型中就位。

后端(Task 1–4):
- 迁移 `0005_pipeline_triggers.sql`:每项目一行(`project_id` PK/FK→projects **ON DELETE CASCADE**,删项目级联删触发设置,已测试断言)、`webhook_token` UNIQUE、`webhook_secret_ciphertext` BLOB(仅密文)、`events_json`/`branch_mappings_json` TEXT、`unmatched_policy`、时间戳;snake_case。
- vault 加密复用:在 `vault.Vault` 接口新增导出方法 `SealSecret([]byte)([]byte,error)` / `OpenSecret([]byte)([]byte,error)`,内部直接复用既有未导出 `seal`/`open`(NaCl secretbox + master key,nonce 前置)。trigger 包不持有 master key,只经 vault 接口加解密 webhook 签名密钥;master key 未配置 → 两方法返 `ErrVaultUnconfigured`,trigger 层映射为领域 `ErrVaultUnconfigured`(读取/重置降级,平台不 panic)。
- `internal/trigger/trigger.go`:`Service` Get/Save/ResetSecret。Get 首访惰性生成默认(crypto/rand token + 生成并加密的 `whsec_` 签名密钥 + events 全 false + 空映射 + policy=record),幂等(二次 Get 不换 token)。掩码服务端即算(解密→`maskSecret`→明文清零),常态只暴露掩码。ResetSecret 重新生成+加密,旧密文被覆盖(旧值失效),完整明文仅在返回值出现一次。校验:`normalizePolicy`(record|ignore)、`validateBranchPattern`(非空、无空白、拒纯 `*`/`**`、允许 `release/*` 等通配)、`validateTargetServerIDs`(**占位**:仅非空结构校验,存在性留 TODO Story 4-1)。
- `internal/httpapi/triggers.go`:三端点 + `triggerDTO`(camelCase,`webhookUrl=/api/webhooks/<token>`、`webhookSecretMasked`,reset 才回 `webhookSecret` 完整明文一次);`writeTriggerError` 映射 422/404,错误体永不含密钥;经 `WithTriggers` Option 装配,挂在已有 requireAuth + requireCSRF 的 `/api` 路由组下。
- main.go 装配 `trigger.New(st.DB, credVault)` 并经 `httpapi.WithTriggers` 注入。
- 测试:`internal/trigger/trigger_test.go`(惰性默认/幂等、Save 往返、非法 branchPattern、合法通配、非法 policy、Reset 旧值失效+新值可解密、**AC-SEC 整库三文件无明文**、删项目级联删、vault 未配置降级)+ `internal/httpapi/triggers_test.go`(GET 掩码不回显、PUT+Reset 端到端、422 错误体、无会话 401、无 CSRF 403)。

### File List

- web/src/api/triggers.ts (新建)
- web/src/views/ProjectTriggers.vue (新建)
- web/src/router/index.ts (修改:添加 project-triggers 路由)
- web/src/views/Projects.vue (修改:配置按钮接上 goToTriggers)
- internal/store/migrations/0005_pipeline_triggers.sql (新建)
- internal/trigger/trigger.go (新建)
- internal/trigger/trigger_test.go (新建)
- internal/httpapi/triggers.go (新建)
- internal/httpapi/triggers_test.go (新建)
- internal/vault/vault.go (修改:新增 SealSecret/OpenSecret 导出方法)
- internal/httpapi/router.go (修改:options.triggers + WithTriggers + 三路由)
- cmd/devopstool/main.go (修改:装配 trigger 服务)

## Change Log

- 2026-05-29:创建 Story 2.3 上下文(触发设置与分支映射,M1 最小前向兼容);冻结触发配置 API 契约;密钥复用 vault secretbox 加密;服务器校验占位留 4-1。Status → in-progress。
- 2026-05-29:Task 5 前端完成。triggers.ts API 模块 + ProjectTriggers.vue 全区段 + 路由接入 + Projects.vue 配置按钮接通。vue-tsc 零错误,vite build 成功。
- 2026-05-29:Task 1–4 后端完成(纯 Go)。迁移 0005 pipeline_triggers(ON DELETE CASCADE);trigger 领域 Service(Get/Save/ResetSecret),webhook 签名密钥复用 vault secretbox(新增 SealSecret/OpenSecret 导出方法)加密入库、常态只掩码、reset 一次性回显;三 HTTP 端点过 auth+CSRF、契约掩码语义;branchPattern 通配校验 + targetServerIds 存在性占位(留 4-1);Go 测试覆盖 AC-SEC(整库无密钥明文)+ 旧值失效 + 级联删 + auth/CSRF。gofmt/vet/test/build/mod tidy/mem-check(20MB)全通过;真实二进制端到端冒烟通过。Status → review。
