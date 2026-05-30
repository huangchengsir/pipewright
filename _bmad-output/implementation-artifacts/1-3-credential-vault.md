---
baseline_commit: NO_VCS
---

# Story 1.3: 凭据加密保险库(FR-3 / AC-SEC-01)

Status: done

> 本 story 后端/前端**并行开发**,经下方「冻结的凭据 API 契约」对接。后端只做 Go(`internal/`、迁移、`go.mod`),前端只做 `web/`,互不越界。1-2(认证/会话/CSRF 中间件)、1-5(设置外壳 + SettingsVault 占位)已 done,本 story 在其上建。

## Story

As a 管理员,
I want 把仓库令牌 / SSH 私钥 / 镜像仓库账号等密钥**加密**存入保险库、**按引用**使用、以**掩码**呈现,
so that 凭据永不以明文出现在日志或界面,后续 epic(2-1 克隆仓库、4-x SSH 部署、3-5 推镜像)可安全引用它们。

## Acceptance Criteria

1. **AC1 — 加密入库,master key 不入库**:新增凭据保存时经 **NaCl secretbox**(`golang.org/x/crypto/nacl/secretbox`)加密入库;master key 来自**环境变量/文件**(`DEVOPSTOOL_MASTER_KEY` base64-32B 或 `DEVOPSTOOL_MASTER_KEY_FILE`),**绝不入库**。DB dump 中无明文 / 无 PEM 字段(AC-SEC-01)。未配置 master key 时:保险库写/读操作返回明确错误「保险库未配置 master key」,但平台仍正常启动(/healthz 200),不 panic。
2. **AC2 — 仅掩码呈现,永不回显明文**:界面/列表只显示**掩码值**(如 Git 令牌 `ghp_••••a91f`、SSH 私钥 `ssh-ed25519 ••••Qz1k`)+ 类型 + 作用域 + 最近使用时间;**任何接口/响应/日志永不回显明文**(连创建时也不回显)。掩码由服务端计算。
3. **AC3 — 按引用取用,复制引用非明文**:平台内部调用方经 `Vault.Get(id)` 接口取明文(仅服务端进程内、用于克隆/SSH/推镜像);对外 REST 只暴露掩码 + 元数据。前端「复制」按钮复制的是**引用 handle(凭据 id/name)**,不是明文。
4. **AC4 — AC-SEC-01 自动化回归测试**:做成自动化测试:① 创建含 PEM 私钥的凭据后,`SELECT *` 整库 dump **grep 不到明文/PEM 头**(`-----BEGIN`);② 加解密往返正确(加密存、`Vault.Get` 取回 == 原文);③ 错误 master key 无法解密(认证标签校验失败)。

## 冻结的凭据 API 契约(前后端共享,不得擅改)

- **类型枚举** `type` ∈ `git_token` | `ssh_key` | `registry`(DB 存 snake_case 字串;JSON camelCase 字段)。
- **列表** `GET /api/credentials` → 200 `[{ "id": "...", "name": "...", "type": "git_token", "scope": "...", "maskedValue": "ghp_••••a91f", "lastUsedAt": "RFC3339|null", "createdAt": "RFC3339" }, ...]`。**无明文/无密文**。
- **创建** `POST /api/credentials` body `{ "name": string, "type": "git_token|ssh_key|registry", "scope": string, "secret": string }` → 201 同上单对象(**不回显 secret**)。写操作 → 需 `X-CSRF-Token`。`secret` 缺失/类型非法 → 400 `{error:{code,message}}`。
- **改名/改作用域** `PATCH /api/credentials/{id}` body `{ "name"?: string, "scope"?: string, "secret"?: string }`(给 `secret` 即轮换密钥)→ 200 单对象(不回显明文)。需 CSRF。
- **删除** `DELETE /api/credentials/{id}` → 204(前端走二次确认)。需 CSRF。
- 错误统一 `{ "error": { "code": "...", "message": "..." } }`,**永不含明文/密文/master key/内部栈**;凭据错误用 code 如 `vault_unconfigured` / `credential_not_found` / `invalid_credential`。
- 全部 `/api/credentials*` 受 1-2 的 `requireAuth` + 写方法 `requireCSRF` 保护。

## Tasks / Subtasks

- [x] **Task 1 — vault 领域包(加密)**(AC: 1,3,4)
  - [x] `internal/vault/`:`crypto.go`(NaCl secretbox 封装:`Seal(plaintext)→ciphertext`、`Open(ciphertext)→plaintext`,随机 24B nonce 前置;master key 32B)、`masker.go`(按类型生成掩码:git_token 末 4 位、ssh_key 取算法前缀+末 4、registry 用户名可见+密码掩码)、`vault.go`(`Vault` 接口:`Create`/`List`/`Get(id)→plaintext`/`Update`/`Delete`;`Get` 更新 last_used_at)。
  - [x] master key 加载:从 `internal/config`(`DEVOPSTOOL_MASTER_KEY` base64-32B 或 `_FILE` 路径读取);缺失时 Vault 进入「未配置」态,操作返回 `ErrVaultUnconfigured`,不 panic。
- [x] **Task 2 — 迁移 + 存储**(AC: 1)
  - [x] `internal/store/migrations/0003_credentials.sql`:`credentials`(`id` TEXT PK(uuid 或 时间序)、`name`、`type`、`scope`、`ciphertext` BLOB、`nonce` BLOB(或合并进 ciphertext)、`masked_value`、`last_used_at` NULL、`created_at`、`updated_at`)。**snake_case**;**明文/PEM 绝不入任何列**;只存密文 + 掩码。
- [x] **Task 3 — HTTP 端点**(AC: 2,3)
  - [x] `internal/httpapi`:挂 `/api/credentials`(GET/POST)、`/api/credentials/{id}`(PATCH/DELETE),过 requireAuth + requireCSRF;DTO 只输出契约字段(掩码,无密文/明文);装配进 `main.go`(注入 vault,依赖 store + master key)。
- [x] **Task 4 — AC-SEC-01 自动化回归测试**(AC: 4)
  - [x] Go test:创建含 `-----BEGIN OPENSSH PRIVATE KEY-----...` 的 ssh_key 凭据 → 整库 dump(遍历所有表所有 TEXT/BLOB 列)grep 不到 `-----BEGIN` 与明文片段;加解密往返 == 原文;错误 master key Open 失败;掩码不含明文尾部以外的内容;List/响应无密文字段。
- [x] **Task 5 — 前端 SettingsVault**(AC: 2,3)
  - [x] `web/src/views/settings/SettingsVault.vue`:凭据列表(卡片/行:类型图标 + 名称 + 掩码值 + 作用域 + 最近使用 + 操作)、空态、`+ 添加凭据` 按钮;`api/credentials.ts` 走 1-5 的 `http`(自动带 CSRF)。
  - [x] 添加/编辑凭据**模态**(DESIGN 模态规范:图标+标题、字段网格、底部次/主按钮):name + type 选择 + scope + secret 输入(secret 为 password 输入,提交后清空、永不回显);删除走二次确认。
  - [x] 「复制」复制引用(id)非明文;掩码值不可点开明文;审计时间线区**本期占位**(审计 = Story 1-4)。
  - [x] 走 1-5 既有令牌 / 组件风格;`maskedValue` 用等宽 JetBrains Mono 呈现。

## Review Findings(2026-05-30 第二轮三层对抗评审 · 后端 A 块)

Patch(无歧义可直接修):
- [x] [Review][Patch] **短 secret 掩码泄露过多明文**:`maskGitToken` 取首个 `_` 前缀全暴露 + 末 4 位 → 短 token 如 `ab_cdef`→`ab_••••cdef`(=明文);`maskSSHKey` 对极短密钥 `••••ab12` 全暴露 → 加**最小长度门槛**(secret 短于阈值则全打点,不暴露尾部/前缀);前缀只暴露已知白名单(`ghp_`/`github_pat_`/`gho_`)[internal/vault/masker.go maskGitToken/maskSSHKey](blind+edge,High)
- [x] [Review][Patch] `LoadMasterKey` base64 解码出的明文 key 切片(`decoded`)用后未清零 → GC 前明文副本残留(可被 core dump/swap 捕获),与他处 `zero()` 纪律不一致 → 返回前 `for i := range decoded { decoded[i]=0 }` [internal/config/config.go LoadMasterKey](blind,Low)

Dismissed:`vault_unconfigured` 用 503 而架构状态码白名单未列 503(两份 spec 张力,实现跟随 story 契约,语义正确)· master key 仅 `StdEncoding`(不容 url-safe;文档约束即可)。

Change Log(2026-05-30 后端 A 块 patch):
- masker.go:加 `maskMinLen=12` 门槛,短于阈值的 git_token/ssh_key 全打点(不暴露任何前缀/尾部);git_token 前缀只暴露白名单 `github_pat_`/`ghp_`/`gho_`,非白名单前缀不暴露;ssh_key 以密钥体(去标点字母数字)长度判定门槛。新增覆盖测试(短 token/非白名单前缀/github_pat_/短 ssh key)。既有 TestMaskGitToken/TestMaskSSHKey/AC-SEC-01 仍绿。
- config.go LoadMasterKey:`copy(key, decoded)` 后清零 `decoded` 明文副本。新增 config_test.go(解码/无配置/坏 base64 不回显/长度错误)。

AC 复核(auditor):1-3 AC1-4 全满足(NaCl 加密 + master key 不入库 + DB 无明文 + 掩码 + 按引用 + AC-SEC-01 整库回归测试齐备)。

## Dev Notes

### 必守护栏
- **加密**:`golang.org/x/crypto/nacl/secretbox`(已在 1-2 引入 x/crypto,无新增重依赖);随机 nonce(`crypto/rand`)。master key 32B,base64 解码;**绝不**把 key/明文/密文写日志或错误体。[Source: architecture.md#Authentication & Security / AC-SEC-01]
- **边界**:`internal/vault` 经 store/repository 触库,不直接 open DB;`internal/httpapi` 唯一对外面;`Vault.Get` 仅供进程内领域调用(后续 2-1/4-x)。[Source: architecture.md#Architectural Boundaries / 强制项「任何外部连接的凭据只经 vault 取用」]
- **命名/格式**:DB snake_case、JSON camelCase、Go PascalCase、接口 `-er`/`Vault`、`ErrXxx`、时间 RFC3339;成功返回资源对象不套壳;错误体永不含 secret。[Source: architecture.md#Implementation Patterns]
- **纯 Go SQLite**(modernc,禁 CGO);沿用 1-1 的 WAL/busy_timeout/MaxOpenConns(1) + 迁移事务化。BLOB 列存密文。
- **≤100MB**:不引重依赖;终验 `make mem-check` 不回退(注意:**勿在包 init() 跑重计算**,教训见 1-2 dummy-hash 致 RSS 飙升)。

### 环境(本机受限)
- Go 在 `~/sdk/go/bin`(`export PATH`);GOPROXY/GOSUMDB 已持久化;拉依赖加 `dangerouslyDisableSandbox`。npm registry=npmmirror。
- 开发期 master key:可在 `.env`/启动 env 设 `DEVOPSTOOL_MASTER_KEY=$(head -c32 /dev/urandom | base64)`;文档化但**不提交真实 key**。

### 禁止
- ❌ 明文/PEM/master key 入库、入日志、入响应、入错误体;❌ 任何接口回显明文(连创建响应);❌ 前端复制明文;❌ CGO sqlite;❌ 后端碰 `web/` / 前端碰 Go;❌ 在 init() 跑重加密;❌ 回退 1-1/1-2 既有安全修复。

### References
- [Source: epics.md#Epic 1 / Story 1.3]
- [Source: architecture.md#Authentication & Security(NaCl secretbox/age,master key env/文件不入库)/ Architectural Boundaries / 强制项 / 目录树 internal/vault]
- [Source: EXPERIENCE.md#Component Patterns(凭据/密钥字段:永不明文,掩码+类型+作用域,复制引用)/ 设置·凭据保险库]
- [Source: DESIGN.md#Components(模态/输入/表单规范)/ mock-settings-vault.html(结构参考)]
- [Source: 1-2(requireAuth/requireCSRF 中间件、x/crypto 已引)/ 1-5(http 包装器带 CSRF、设置外壳、SettingsVault 占位)]

## Dev Agent Record

### Agent Model Used

- claude-sonnet-4-6 (前端实现,Task 5 only)
- claude-opus-4-8[1m] (后端实现,Task 1-4)

### Debug Log References

- vue-tsc --noEmit: 零错误
- vite build: ✓ built in 1.93s,SettingsVault JS 18.07 kB gzip 5.88 kB,CSS 14.32 kB gzip 2.84 kB
- (后端) `gofmt -l cmd internal embed.go` 空;`go vet . ./cmd/... ./internal/...` 干净
- (后端) `go test . ./cmd/... ./internal/...` 全绿(vault/httpapi/store 均 ok)
- (后端) `CGO_ENABLED=0 go build ./cmd/devopstool` 成功;`make mem-check` resident 18 MB(≤100 MB,无 init 重计算回退)
- (后端) AC-SEC-01:`TestACSEC01_NoPlaintextInDB` 遍历整库所有表所有列,grep 不到 `-----BEGIN`/明文 marker — PASS
- (后端) 真实二进制冒烟:login→POST /api/credentials(ssh_key+PEM)→GET 见掩码 `••••…`,无明文;raw .db/.db-wal/.db-shm grep 无 `-----BEGIN`/明文

### Completion Notes List

**后端(Task 1-4):**
- **AC1**:NaCl secretbox(`golang.org/x/crypto/nacl/secretbox`)加密;每次 `seal` 用 `crypto/rand` 生成 24B 随机 nonce 前置于密文;master key 32B 来自 `DEVOPSTOOL_MASTER_KEY`(base64-32B)或 `DEVOPSTOOL_MASTER_KEY_FILE`,**绝不入库**。master key 缺失/无效 → Vault 未配置态,操作返回 `ErrVaultUnconfigured`,平台仍启动(/healthz 200,实测无 panic)。
- **AC2**:`credentials` 表只存密文 BLOB + 服务端算出的 `masked_value` + 元数据;DTO 只输出契约字段,创建/列表/更新响应**均不回显 secret/密文**。掩码服务端计算:git_token 保留前缀+末4、ssh_key 算法前缀+末4(私钥 PEM 跳过 `-----` 标点,只暴露 body 字母数字)、registry 用户名可见+密码点掩码。
- **AC3**:`Vault.Get(id)` 解密返回明文**仅供进程内领域调用**(后续 2-1/4-x),并更新 `last_used_at`;REST 永不暴露明文。
- **AC4**:`TestACSEC01_NoPlaintextInDB` 创建含 PEM 私钥凭据后遍历整库 dump,grep 不到 `-----BEGIN`/明文 marker;`TestSealOpenRoundTrip` 往返==原文;`TestOpenWrongKeyFails` 错误 key 解密失败(Poly1305 认证标签);掩码测试断言不泄漏密钥体。
- **契约遵守**:端点/方法/字段名(camelCase)/状态码(200/201/204/400/403/404/503)/类型枚举(git_token|ssh_key|registry)/错误码(vault_unconfigured/credential_not_found/invalid_credential)均按冻结契约;全部 `/api/credentials*` 经 requireAuth + 写方法 requireCSRF。
- **安全**:master key/明文/密文从不入日志/错误体/响应;`ErrDecrypt` 与内部错误一律映射为通用文案,不泄漏细节。
- **装配**:`cmd/devopstool/main.go` 已加载 master key 并经 `httpapi.WithVault` 挂载路由;真实二进制冒烟通过(本要点是 1-2 教训的对应修复)。
- **内存**:加密只在实际请求时分配,无包 init() 重计算;mem-check resident 18 MB 不回退。
- **签名变更**:`httpapi.New` 由 `New(webFS, authn ...Authenticator)` 改为 `New(webFS, authn Authenticator, opts ...Option)`(新增 `WithVault` Option);已同步更新既有测试调用。`google/uuid` 由 indirect 提升为 direct 依赖(`go mod tidy`)。

- **AC2 遵守**:maskedValue 只显示服务端计算的掩码(JetBrains Mono 等宽,user-select:none),`type="password"` 输入框在提交完成/模态关闭时立即清空 `form.secret`,任何地方不渲染明文。
- **AC3 遵守**:「复制」按钮复制 `credential.id`(引用 handle)而非 secret;掩码值不可交互展开。
- **冻结契约遵守**:`Credential` 接口字段与契约精确对齐(`id/name/type/scope/maskedValue/lastUsedAt/createdAt`);`type ∈ 'git_token'|'ssh_key'|'registry'`。
- **后端未就绪优雅降级**:网络错误(status 0)、`vault_unconfigured` 错误码均映射到中文错误 banner + ↻ 重试按钮,不白屏。
- **设计反 AI 味处理**:图标全部手写 SVG 几何细线(无星芒);类型图标用三套不同语义色(cyan/green/amber);idle 凭据走水平渐变背景而非单纯变色;分段控件带阴影激活态区别于 Naive UI 默认;掩码值行上方的凭据名称文字阶梯使用了 bold+mono 双字族对比;删除模态背景用 `62%` 暗化 scrim 而非纯黑。
- **审计时间线**:本期用占位面板,明标「审计日志 — 将在 Story 1.4 实现」。
- **双主题**:全部颜色走 CSS 自定义属性令牌,不含任何硬编码色值(除 `#fff` 白色用于 primary 按钮文字)。
- **reduced-motion**:末尾 `@media (prefers-reduced-motion: reduce)` 块将所有动效降级为瞬时。
- **无 Naive UI 组件**:此页全部用原生 HTML + Vue refs 实现,保持与 Login.vue 相同的 scoped CSS 模式。

### File List

- `web/src/api/credentials.ts` — 新建:凭据 API 封装(list/create/update/delete),类型定义对齐冻结契约
- `web/src/views/settings/SettingsVault.vue` — 改写占位为完整实现

**后端(Task 1-4):**

- `internal/vault/crypto.go` — 新建:NaCl secretbox 封装(seal/open,随机 24B nonce 前置,ErrDecrypt)
- `internal/vault/masker.go` — 新建:按类型服务端掩码(git_token/ssh_key/registry)
- `internal/vault/vault.go` — 新建:`Vault` 接口 + store 支撑实现(Create/List/Get/Update/Delete;Get 更新 last_used_at;未配置态)
- `internal/vault/crypto_*`/`masker_test.go`/`vault_test.go` — 新建:单元 + AC-SEC-01 整库回归测试
- `internal/store/migrations/0003_credentials.sql` — 新建:`credentials` 表(密文 BLOB + 掩码 + 元数据,无明文/PEM 列)
- `internal/config/config.go` — 改:新增 `LoadMasterKey()`(env/文件,base64-32B,缺失返回 ErrNoMasterKey)+ `MasterKeyLen`
- `internal/httpapi/credentials.go` — 新建:`/api/credentials*` 4 个 handler + DTO + 错误映射
- `internal/httpapi/credentials_test.go` — 新建:HTTP 端点测试(创建/列表无明文、CSRF、Auth、PATCH/DELETE、未配置 503)
- `internal/httpapi/router.go` — 改:`New` 签名加 `Option`/`WithVault`,挂载凭据路由
- `internal/httpapi/router_test.go` — 改:`New(testWebFS())` → `New(testWebFS(), nil)`(适配新签名)
- `cmd/devopstool/main.go` — 改:加载 master key,装配 vault 并 `WithVault` 挂载
- `go.mod`/`go.sum` — 改:`google/uuid` 提升为 direct 依赖

## Change Log

- 2026-05-29:创建 Story 1.3 上下文(凭据加密保险库),前后端并行;冻结凭据 API 契约。Status → in-progress。
- 2026-05-29:前端 Task 5 完成。实现 credentials.ts API 模块 + SettingsVault.vue 完整 UI(列表/骨架/空态/添加-编辑模态/删除二次确认/复制引用/掩码展示/审计占位/双主题/reduced-motion)。vue-tsc 零错误,vite build 成功。
- 2026-05-29:后端 Task 1-4 完成。internal/vault(secretbox 加密/掩码/Vault 接口)+ 0003 迁移 + config master key 加载 + /api/credentials* 端点 + 装配 main.go;AC-SEC-01 整库 dump 回归绿,加解密往返/错误 key 失败覆盖;gofmt/vet/test 全过,mem-check 18 MB 不回退,真实二进制冒烟通过。Status → review。
