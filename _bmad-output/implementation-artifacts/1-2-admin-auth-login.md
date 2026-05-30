---
baseline_commit: NO_VCS
---

# Story 1.2: 单管理员认证与登录(后端)

Status: done

> 本 story 与 **Story 1.5(前端外壳+登录视图)并行开发**。本 story **只做 Go 后端**(`internal/`、`cmd/`、迁移、`go.mod`),**绝不修改 `web/` 下任何文件**。前后端经下方「冻结的 Auth API 契约」对接。

## Story

As a 管理员,
I want 用户名口令登录(argon2id + 服务端会话 cookie)且状态变更接口有 CSRF 防护、连续失败锁定,
so that Web UI 不在网络上裸奔,只有我能操作实例。

## Acceptance Criteria

1. **AC1 — 未认证拦截**:未登录访问任意受保护 `/api/*`(除登录/会话/healthz)返回 **401** JSON;HTML/SPA 路由不在后端硬重定向,由前端依据 401/会话探测跳登录(契约见下)。
2. **AC2 — 登录与会话**:正确口令登录建立 **HttpOnly + SameSite=Lax + Secure(生产)** 会话 cookie 并返回当前用户;口令以 **argon2id** 哈希存储,**DB 中无明文口令**。
3. **AC3 — 失败锁定(NFR-5)**:连续登录失败 **5 次** → 锁定 **15 分钟**,锁定期内返回 **429** 并提示剩余时间;成功登录或锁定到期后计数重置。
4. **AC4 — CSRF 防护**:对状态变更接口(POST/PUT/PATCH/DELETE)缺失/不匹配 CSRF token 的请求返回 **403**;系统中**无多用户 / 无 RBAC** 概念(永远单一 admin)。

## 冻结的 Auth API 契约(前后端共享,不得擅改;改动需同步通知 1.5)

- **会话探测** `GET /api/auth/session` → 200 `{ "username": "admin" }`(已登录) / **401** `{ "error": { "code": "unauthorized", "message": "..." } }`(未登录)。**此端点本身不要求登录**(它就是用来探测的)。
- **登录** `POST /api/auth/login` body `{ "username": string, "password": string }` →
  - 200 `{ "username": "admin" }` + `Set-Cookie: devops_session=...; HttpOnly; SameSite=Lax; Path=/`(生产追加 `Secure`)+ 同时下发可读 CSRF cookie(见下)。
  - 401 `{ "error": { "code": "invalid_credentials", "message": "用户名或口令错误" } }`。
  - 429 `{ "error": { "code": "locked_out", "message": "登录失败次数过多,请 N 分钟后重试" } }`。
  - 登录端点**豁免 CSRF 校验**(此时尚无会话);但仍受锁定限制。
- **登出** `POST /api/auth/logout` → **204**,清除会话 cookie(`Max-Age=0`)。属状态变更 → **需** CSRF header。
- **CSRF(双提交 cookie 模式,零额外重依赖)**:服务端在登录成功 + 会话有效时下发**可读**(非 HttpOnly)cookie `devops_csrf=<token>`;前端在所有写请求(POST/PUT/PATCH/DELETE)带请求头 `X-CSRF-Token: <同值>`;服务端比对 cookie 与 header,缺失/不等 → 403。`GET/HEAD/OPTIONS` 不校验。token 用 `crypto/rand` 生成、与会话绑定。
- **未认证写请求** 仍先过认证(401)再过 CSRF;两者顺序:认证中间件 → CSRF 中间件。
- JSON 一律 camelCase;错误统一 `{ "error": { "code": "...", "message": "..." } }`,**永不含口令/hash/内部栈**。

## Tasks / Subtasks

- [x] **Task 1 — auth 领域包**(AC: 2,3)
  - [ ] 新建 `internal/auth/`:`password.go`(argon2id 哈希/校验,用 `golang.org/x/crypto/argon2`;参数 time=1, memory=64MB, threads=4, keyLen=32, 16B salt;编码为 PHC 字串 `$argon2id$v=19$m=...,t=...,p=...$salt$hash`)、`session.go`(服务端会话:`crypto/rand` 32B token、SQLite 持久化、过期/最近活跃)、`lockout.go`(失败计数器,可内存 + mutex;注释说明 DB 持久化为后续可选)。
  - [ ] `service.go`/`-er` 接口:`Authenticator`(`Login`/`Verify`/`Logout`),依赖经构造函数注入 `*sql.DB`(经 store)或 repository。
- [x] **Task 2 — 迁移 + 管理员引导**(AC: 2)
  - [ ] `internal/store/migrations/0002_auth.sql`:`admin_user`(单行:`id`、`username`、`password_hash`、`created_at`、`updated_at`)、`sessions`(`token` PK、`created_at`、`expires_at`、`last_seen_at`)。**snake_case**。
  - [ ] 首次启动引导:从配置(`internal/config`)读 `DEVOPSTOOL_ADMIN_USERNAME`(默认 `admin`)+ `DEVOPSTOOL_ADMIN_PASSWORD`;若 `admin_user` 空且提供了口令 → argon2id 哈希入库。已存在则忽略 env(口令改在 1.7)。无口令且无 admin 时**明确日志提示**如何设置(不 panic 阻断 healthz)。
- [x] **Task 3 — HTTP 端点 + 中间件**(AC: 1,4)
  - [ ] `internal/httpapi`:挂载 `/api/auth/login`、`/api/auth/logout`、`/api/auth/session`(契约见上)。
  - [ ] `RequireAuth` 中间件:校验会话 cookie → 注入用户到 context;未过 → 401 JSON。包裹除 auth/healthz 外的 `/api/*`。
  - [ ] `CSRF` 中间件:双提交校验(见契约);仅对写方法生效,登录端点豁免。
  - [ ] `cmd/devopstool/main.go`:装配 auth service + 中间件;复用 1.1 的优雅停机/超时,不回退既有安全修复。
- [x] **Task 4 — 测试(Go httptest,无需前端)**(AC: 全部)
  - [ ] argon2id:哈希可校验、错误口令拒绝、**DB dump/查询中无明文口令**(查 `admin_user.password_hash` 以 `$argon2id$` 开头且 != 明文)。
  - [ ] 登录成功设置 cookie + 返回 username;错误口令 401;未登录访问受保护端点 401。
  - [ ] 锁定:连续 5 次失败 → 第 6 次 429;成功后计数重置(可用可注入的时钟以测 15 分钟到期)。
  - [ ] CSRF:带会话但缺 `X-CSRF-Token` 的 POST → 403;带正确 header → 通过(用一个临时受保护写端点或 logout 验证)。
- [x] **Task 5 — 自验 + 收尾**
  - [ ] `gofmt`、`go vet . ./cmd/... ./internal/...`、`go test ./...` 全绿;`go build`(用现有 `web/dist`,**不重建前端**)。
  - [ ] `make mem-check` 仍 ≤100MB(新增会话表 + 内存计数器开销可忽略,确认不回退)。
  - [ ] 更新本文件 Dev Agent Record(实现说明 / File List / AC 状态);Status → review。

## Review Findings(2026-05-29 三层对抗评审:Blind / Edge / Acceptance)

Decision-needed(已拍板 → 转 Patch):
- [x] [Review][Decision→Patch] 会话过期策略 = **滑动空闲超时**(作者定):每次 `Verify` 把 `expires_at` 前移(空闲 7 天才过期),让 `last_seen_at` 真正生效 [internal/auth/session.go]。

Patch(无歧义可直接修):
- [x] [Review][Patch] 用户名不匹配时跳过 argon2 校验留下时序枚举差异 → 失配也跑一次 dummy hash 使两路径等时 [internal/auth/service.go](blind,High)
- [x] [Review][Patch] 锁定 `Check()` 与 `RecordFailure()` 之间 TOCTOU,并发可绕过阈值 → 检查+记失败原子化 [internal/auth/lockout.go,service.go](edge,Medium)
- [x] [Review][Patch] CSRF 仅比对 header==cookie,未与服务端 session 绑定(`Session.CSRFToken` 形同虚设),子域/MITM 写 cookie 可绕过 → `requireCSRF` 改比对已认证 session 的 csrf_token [internal/httpapi/router.go](blind,Medium)
- [x] [Review][Patch] 登录/会话响应硬编码 `"username":"admin"`,与可配 `DEVOPSTOOL_ADMIN_USERNAME` 不符 → 回显真实存储用户名 [internal/httpapi/router.go](blind+edge+auditor,Medium)
- [x] [Review][Patch] `decodeHash` 忽略 PHC 中 m/t/p/version 用硬编码常量重算,参数升级后旧哈希失配锁死管理员 → 从 PHC 解析参数校验 [internal/auth/password.go](blind+edge,Medium)
- [x] [Review][Patch] `requireAuth` 把 `Verify` 的非 NotFound 错误(DB 故障)也当 401「会话过期」掩盖真实故障 → 区分 ErrSessionNotFound(401)与其它(500) [internal/httpapi/router.go](blind,Low)
- [x] [Review][Patch] `Get` 中 `time.Parse` 错误被静默丢弃 → 返回/记错而非吞 [internal/auth/session.go](blind+edge+auditor,Low)
- [x] [Review][Patch] 过期会话仅惰性删除,sessions 表无界增长 → 启动时(+可选定时)`DELETE WHERE expires_at<now` [internal/auth/session.go](blind+edge,Low)
- [x] [Review][Patch] 登录端点(匿名可达写口)无请求体大小限制 → `http.MaxBytesReader` [internal/httpapi/router.go](edge,Low)
- [x] [Review][Patch] 测试尾部 `var _ *url.URL` 掩盖未用 import → 删除多余 import 与占位 [internal/httpapi/auth_test.go](blind,Low)

Defer(真实但非现在做,入 deferred-work.md):
- [x] [Review][Defer] 锁定计数 DB 持久化(进程重启清零绕过 NFR-5)[internal/auth/lockout.go] — story Dev Notes 已显式延后为可选,自托管重启成本低,记录在案
- [x] [Review][Defer] `New(webFS, authn ...)` 变参可选依赖,nil 时静默全 401/503 → 改必填构造器 [internal/httpapi/router.go] — 设计小异味,后续
- [x] [Review][Defer] `Bootstrap` count+insert 非原子,双实例同库并发首启会崩 [internal/auth/service.go] — 单二进制场景极不可能,`INSERT OR IGNORE` 后续

Dismissed(误报/有意取舍,共 2):`makeSessionHandler` 复用 requireAuth —— 误报:会话探测端点必须自身返回 401/200,无法被 requireAuth 拦在 401(其会拦掉 200 分支),重发 csrf cookie 合理 · CSRF cookie 非 HttpOnly 经 document.cookie 读 —— 双提交模式的有意取舍,diff 已注释说明。

## Dev Notes

### 必守护栏(承自 1.1 与架构)
- **纯 Go SQLite**:继续用 `modernc.org/sqlite`,**禁 CGO**;沿用 1.1 已设的 `busy_timeout/WAL/foreign_keys` + `SetMaxOpenConns(1)`。新表走内嵌迁移(事务化,1.1 已改 Tx)。[Source: architecture.md#Data Architecture;1-1 评审]
- **边界**:`internal/httpapi` 是唯一对外面;仅 `internal/store` 触 SQLite,auth 经 store/repository 取数据,不直接 open DB。[Source: architecture.md#Architectural Boundaries]
- **依赖最小化**:只新增 `golang.org/x/crypto`(argon2)。**CSRF 用手写双提交**(`crypto/hmac`/`crypto/rand`),不引入 gorilla/csrf 等重依赖,守 ≤100MB 与轻量立场。[Source: architecture.md#Starter Template / D1]
- **命名/格式**:DB snake_case;JSON camelCase;Go 导出 PascalCase;接口 `-er`;错误变量 `ErrXxx`;时间 RFC3339;错误体永不含 secret。[Source: architecture.md#Implementation Patterns]
- **网络**:本机受限,拉 `x/crypto` 需走 `GOPROXY=https://goproxy.cn`(已 `go env -w`)+ `dangerouslyDisableSandbox`(沙箱拦网络)。Go 在 `~/sdk/go/bin`,先 `export PATH="$HOME/sdk/go/bin:$PATH"`。

### 安全要点
- argon2id 参数取业界稳妥默认(见 Task 1);salt 每口令随机;**恒定时间比较**(`crypto/subtle`)防时序侧信道。
- 会话 cookie:`HttpOnly`、`SameSite=Lax`、`Path=/`;`Secure` 在检测到 TLS/生产时加(开发 http 下不加以免本地登录失败)。会话有合理过期(如 7d)+ 滑动续期可选。
- 锁定按"实例级单 admin"建模:一个全局失败计数 + 锁定截止时间即可(单用户无需按 IP/用户分桶);并发安全(mutex)。
- **不要**把口令/hash/会话 token 写进任何日志。AC-SEC 完整脱敏在 1.4,本 story 先不泄即可。

### 禁止
- ❌ 明文口令入库或入日志;❌ 引入多用户/角色/RBAC 概念或表;❌ CGO sqlite;❌ 修改 `web/`(前端归 1.5);❌ 回退 1.1 已落地的 HTTP 超时/优雅停机/迁移事务/SQLite pragma。

### References
- [Source: epics.md#Epic 1 / Story 1.2]
- [Source: architecture.md#Authentication & Security(会话 cookie + argon2id + CSRF;无 RBAC)]
- [Source: EXPERIENCE.md#Foundation / 壳外屏(登录)]
- [Source: prd.md#NFR-5(失败锁定)]
- [Source: 1-1-project-scaffold.md(护栏与已修复项,勿回退)]

## Dev Agent Record

### Agent Model Used

claude-sonnet via Agent(后端成员,并行)+ claude-opus-4-8 编排者(集成修复与端到端验证)。

### Debug Log References

- 实现成员在最终装配前遇 API 500 中断;编排者接手完成 `cmd/devopstool/main.go` 的 auth 装配 + admin 引导(原 `main.go` 仍是脚手架签名 `httpapi.New(webFS)`,致运行期 auth 半残:login 503),并修复随之而来的两处测试演进:`internal/store/store_test.go`(1.1 的"仅 schema_migrations / 恰好 1 迁移"守卫断言需让位于 1.2 新增的 0002 迁移与领域表)+ 一处 `go vet`(auth_test.go 先用 resp 后查错)。
- 端到端(运行内嵌前端的真实静态二进制,`/tmp` 实测)契约逐项验证(见下),非仅单测。

### Completion Notes List

- **已实现**:`internal/auth`(argon2id 口令哈希 PHC、服务端 SQLite 会话、失败锁定计数器、`Authenticator` 服务 + `Bootstrap` 首启引导)、迁移 `0002_auth.sql`(`admin_user`/`sessions`)、`internal/httpapi` 三端点(login/logout/session)+ `requireAuth`/`requireCSRF`(双提交)中间件、`internal/config` 读 admin env、`cmd/devopstool/main.go` 装配。
- **AC 状态(端到端实测)**:AC1 未登录受保护端点 401 ✅;AC2 登录 200 + 下发 `devops_session`(HttpOnly+SameSite=Lax)与可读 `devops_csrf` cookie、`password_hash` = `$argon2id$v=19$m=65536,t=1,p=4$…`、DB 无明文 ✅;AC3 连续 5 次失败后第 6 次(含正确口令)429 ✅;AC4 缺 `X-CSRF-Token` 的 logout 403、带则 204,无多用户/RBAC ✅。
- **验证命令真实结果**:`gofmt -l`(空)、`go vet ./...`(干净)、`go test ./...`(httpapi + store 全绿)、`CGO_ENABLED=0 go build`(静态二进制 15MB)、`make mem-check`(RSS ~18MB ≤100MB)。
- **延后**(非阻塞,留评审/后续):锁定计数器为内存态(重启重置,单管理员实例可接受;DB 持久化为后续可选);会话固定 7d 过期未做滑动续期;`Secure` cookie 依生产判定(开发 http 不加)。
- 依赖只新增 `golang.org/x/crypto`(argon2);CSRF 手写双提交,未引重依赖;`go mod tidy` 已执行。

### File List

新增:
- `internal/auth/password.go`、`internal/auth/session.go`、`internal/auth/lockout.go`、`internal/auth/service.go`
- `internal/store/migrations/0002_auth.sql`
- `internal/httpapi/auth_test.go`

修改:
- `internal/httpapi/router.go`(auth 端点 + 中间件)
- `cmd/devopstool/main.go`(装配 auth service + admin 引导)
- `internal/config/config.go`(AdminUsername/AdminPassword env)
- `internal/store/store_test.go`(守卫断言随 0002 迁移演进)
- `go.mod` / `go.sum`(golang.org/x/crypto 转直接依赖)

## Change Log

- 2026-05-29:创建 Story 1.2 上下文(后端单管理员认证),与 1.5 并行;冻结 Auth API 契约。Status → in-progress。
- 2026-05-29:实现后端认证(argon2id + 服务端会话 + 双提交 CSRF + 失败锁定 + 首启引导);编排者补完 main.go 装配并端到端验证契约(login/session/CSRF/lockout 全过)+ 演进 store 守卫测试 + 修 vet。gofmt/vet/test/build/mem-check 全绿。Status → review。
- 2026-05-29:`bmad-code-review` 三层对抗评审 → 11 后端 patch + 1 decision(会话=滑动空闲超时)全部应用:dummy-hash 等时防用户名枚举、锁定 Guard 原子化防 TOCTOU、CSRF 绑定服务端 session.CSRFToken、回显真实用户名、decodeHash 解析 PHC 参数、requireAuth 区分 401/500、time.Parse 不吞错、启动清理过期会话、登录体限流、删多余 import、会话滑动续期。**编排者验证又抓修 1 回归**:dummy-hash 原在 init() 跑 argon2 致空载 RSS 18MB→82MB → 改静态 PHC 常量,RSS 回落 18MB。3 defer 入 deferred-work.md。gofmt/vet/test/build/mem-check 全绿。Status → done。
