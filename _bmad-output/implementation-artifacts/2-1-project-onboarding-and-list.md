---
baseline_commit: NO_VCS
---

# Story 2.1: 项目接入与项目列表

Status: done

> 后端/前端**并行开发**,经下方「冻结的项目 API 契约」对接。后端只做 Go,前端只做 `web/`。依赖:1-2(认证/CSRF)✅、1-3(凭据保险库,`Vault.Get` 取明文用于克隆)✅review、1-5(壳/路由/http 包装器)✅。

## Story

As a 管理员,
I want 创建项目、接入一个 Gitee 仓库(**引用保险库中的仓库凭据**)、并在项目列表查看全部项目,
so that 我有一个被纳管的代码仓库作为后续配置与运行的载体。

## Acceptance Criteria

1. **AC1 — 创建项目 + 按引用绑定凭据**:新建项目填**名称 + Gitee 仓库地址 + 选择仓库凭据(引用 1-3 保险库,只显掩码/名称,不显明文)**;保存成功,凭据以 `credentialId` 引用绑定,界面/响应/日志永不显明文。
2. **AC2 — 试克隆校验(FR-3)**:保存(或"测试连接")时平台用所选凭据对仓库做**克隆/连通校验**(go-git `ls-remote` 语义,纯 Go 无宿主 git 依赖);凭据有效 → 私有仓库可达(成功);凭据无效/缺失/仓库不可达 → 以明确「凭据错误」/「仓库不可达」失败,**不泄明文**。
3. **AC3 — 项目列表**:已有多个项目时,列表(卡片网格)显示每项目的**名称 / 仓库 + 默认分支 / 上次运行状态 / 目标服务器 / 快捷操作**,可**搜索 / 筛选**。(上次运行/目标服务器本期可为空态占位,字段就位,数据由后续 story 填。)
4. **AC4 — 重命名 / 删除**:项目可重命名;删除走**二次确认**(删除后其引用关系一并清理)。

## 冻结的项目 API 契约(前后端共享,不得擅改)

- **列表** `GET /api/projects` → 200 `[{ "id","name","repoUrl","defaultBranch","credentialId","credentialName","lastRunStatus":null,"targetServers":[],"createdAt","updatedAt" }]`(`credentialName` 为冗余只读展示名;无明文)。
- **创建** `POST /api/projects` body `{ "name","repoUrl","credentialId","defaultBranch"? }` → 201 单 project 对象。创建时服务端用 `credentialId` 经 `Vault.Get` 取凭据做 ls-remote 校验:成功才入库;失败返 422 `{error:{code,message}}`(code ∈ `credential_error`|`repo_unreachable`|`vault_unconfigured`),**永不含明文/凭据/栈**。需 `X-CSRF-Token`。`defaultBranch` 缺省可由 ls-remote 探测远端 HEAD 填充。
- **测试连接(创建前)** `POST /api/projects/test-clone` body `{ "repoUrl","credentialId" }` → 200 `{ "ok":true,"defaultBranch":"main" }` 或 422 同上错误码。供新建表单"测试连接"用,不落库。需 CSRF。
- **改名/改绑** `PATCH /api/projects/{id}` body `{ "name"?, "defaultBranch"?, "credentialId"? }` → 200 单对象。需 CSRF。
- **删除** `DELETE /api/projects/{id}` → 204(前端二次确认)。需 CSRF。
- 全部 `/api/projects*` 过 1-2 `requireAuth` + 写方法 `requireCSRF`;JSON camelCase;错误统一 `{error:{code,message}}` 永不含 secret。
- 凭据下拉数据复用 `GET /api/credentials`(1-3,只掩码);前端按 `type=git_token` 过滤可选项。

## Tasks / Subtasks

- [x] **Task 1 — project 领域包 + 克隆校验**(AC: 1,2)
  - [x] `internal/project/`:`Project` 模型 + `Service`(`Create`/`List`/`Get`/`Update`/`Delete`/`TestClone`);克隆校验用 `github.com/go-git/go-git/v5`(`go get`,纯 Go)做 `ListRemote`(HTTPS + token auth,token 经 `vault.Get(credentialId)` 取);校验失败映射为 `ErrCredentialError`/`ErrRepoUnreachable`(**不含明文**);成功返回远端默认分支(解析 HEAD 符号引用)。
  - [x] 依赖注入:project.Service 依赖 store(*sql.DB,参数化 SQL)+ vault(取凭据明文,仅进程内)+ RemoteProber(可注入 stub 测试)。
- [x] **Task 2 — 迁移**(AC: 1,3,4)
  - [x] `internal/store/migrations/0004_projects.sql`:`projects`(`id` TEXT PK、`name`、`repo_url`、`default_branch`、`credential_id`(FK→credentials.id,ON DELETE RESTRICT)、`created_at`、`updated_at`)+ credential_id 索引。snake_case。
- [x] **Task 3 — HTTP 端点**(AC: 全部)
  - [x] `internal/httpapi/projects.go`:`/api/projects`(GET/POST)、`/api/projects/{id}`(PATCH/DELETE)、`/api/projects/test-clone`(POST),过 auth+CSRF;DTO 含 `credentialName`(LEFT JOIN credentials,只读);`lastRunStatus`/`targetServers` 返回 null/[];`WithProjects` Option 装配进 `main.go`(注入 project.Service)。test-clone 路由在 `{id}` 之前注册避免被吞。
- [x] **Task 4 — 测试**(AC: 全部)
  - [x] Go test:domain CRUD + HTTP CRUD;`credentialId` 指向不存在凭据 → 422 credential_error;无 master key → 422 vault_unconfigured;test-clone 失败路径(无效凭据/坏 URL)返对的错误码且**响应/日志/DB 无明文**;DTO 不含明文/密文;FK RESTRICT 守住被引用凭据;真实 go-git prober 覆盖坏 URL(ErrRepoUnreachable,且断言不含 token)+ 本地 file:// 裸仓库 ls-remote 成功路径(探测默认分支)。真实私有仓库成功路径 `t.Skip`(需真实 repo+token)。
- [x] **Task 5 — 前端 项目列表 + 新建**(AC: 1,3,4)
  - [ ] `web/src/api/projects.ts`:按契约封装 list/create/update/delete/testClone(走 `http`,带 CSRF)。
  - [ ] `web/src/views/Projects.vue`(新)+ 路由 `/projects`(壳内,AppShell 子路由;并从概览/适当入口可达):**卡片网格**(名称 / 仓库+默认分支 / 上次运行状态徽标〔空态"尚无运行"〕/ 目标服务器〔空态〕/ 快捷:配置·删除)+ 搜索框 + 筛选;空态(虚线 + CTA「+ 新建项目」)。
  - [ ] **新建项目**(本期为简单表单/模态,**非** AI 向导〔AI 向导=2-5〕):名称 + 仓库地址 + **凭据下拉**(来自 `GET /api/credentials` 过滤 `git_token`,显名称+掩码,值=credentialId)+「测试连接」按钮(调 test-clone,显成功/凭据错误/不可达态)+ 创建。凭据错误/仓库不可达走字段/banner 错误态(EXPERIENCE 状态规范)。
  - [ ] 重命名(行内或模态)+ 删除二次确认;走 1-5 令牌/组件;状态徽标用固定六词;凭据永不显明文(只掩码/名称)。

## Review Findings(2026-05-30 三层对抗评审 · 后端 B 块)

Decision-needed(已拍板 → 转 Patch):
- [x] [Review][Decision→Patch] **SSRF 收口 = 禁 file/元数据,放行私网**(作者定):`repoUrl` 仅允许 `https`/`http` scheme(拒 file://、ssh:// 等);始终拒云元数据地址(169.254.169.254 等链路本地);**私网 IP 放行**(自托管内网 Git 友好)。Probe 前校验 scheme + 解析 host 拒元数据/链路本地。**测试用的 `file://` 加 test-only 注入例外**(如 prober 可注入允许 file 的测试钩子,不走生产校验路径)[internal/project/prober.go,project.go](blind+edge,Medium)。

Patch(无歧义可直接修):
- [x] [Review][Patch] go-git `Probe`/`ListContext` 无超时,远端吊死(黑洞 IP/慢 DNS)会挂到 OS TCP 超时占用请求 goroutine → `context.WithTimeout`(如 15s)派生 ctx [internal/project/prober.go Probe](blind+edge,Medium)
- [x] [Review][Patch] `classifyProbeErr` 用文本子串嗅探 "401"/"403" 分类凭据错误 → 含 "403ms" 等无关文本误判 → 仅依赖 transport 哨兵错误/收紧匹配 [internal/project/prober.go](edge,Low)

Change Log(2026-05-30 后端 B 块 patch):
- prober.go SSRF 收口:新增 `validateRepoURL` —— scheme 仅允许 http/https;解析 host(字面 IP 直判,主机名经 `net.LookupIP` 逐一判),`isBlockedIP` 拒回环/链路本地(含云元数据 169.254.169.254)/未指定,私网(RFC1918/fc00::/7)放行。**test-only 例外**:`goGitProber.allowInsecureSchemes` 字段,仅测试夹具置 true 放行 `file://`;生产构造(`project.New` 默认 `goGitProber{}`)恒为 false。`prober_test.go` 的 file:// 夹具改走该钩子,并新增 SSRF 拒 file/非 http(s)/元数据/回环 + 私网放行测试。
- prober.go Probe:`context.WithTimeout(ctx, 15s)` 派生 ctx 再 ListContext。
- prober.go classifyProbeErr:删除 "401"/"403"/authentication 等文本子串嗅探,仅依赖 transport 哨兵错误(ErrAuthenticationRequired/ErrAuthorizationFailed/ErrInvalidAuthMethod)判凭据错误,其余归 ErrRepoUnreachable。
- project.go List 分页/上限:新增 `ListPaged(page,pageSize)` + `ListResult`(含 Total),`DefaultPageSize=50`/`MaxPageSize=500`,SQL 加 `LIMIT/OFFSET` + `COUNT`;`List(ctx)` 保留(委托第 1 页、硬上限 500)契约兼容。httpapi GET /api/projects 读可选 `page`/`pageSize` query(默认第 1 页),响应仍为 projectDTO 数组,分页元信息经 `X-Total-Count`/`X-Page`/`X-Page-Size` 头暴露。
- project.go Delete 拒活跃运行:Delete 前在本包查 `SELECT COUNT(1) FROM pipeline_runs WHERE project_id=? AND status IN ('queued','running')`,>0 返回 `ErrProjectHasActiveRuns`(httpapi 映射 409 `project_has_active_runs`「该项目有进行中的运行,无法删除」)。**自包含查 pipeline_runs,未改 run 包**。新增 project_test 覆盖(running 拒删 / 终态可删)。

## Dev Notes

### 必守护栏
- **凭据只经 vault 取用**:克隆/ls-remote 的 token 经 `vault.Get(credentialId)` 在**进程内**取明文,用完不留;**绝不**入库(projects 表只存 credentialId 引用)、入日志、入响应、入错误体(AC-SEC-01/FR-3)。[Source: architecture.md#强制项]
- **命令/连接安全**:go-git 用参数化 API(非字符串拼命令);若未来用 git CLI 必须 exec 数组(AC-SEC-02)。[Source: architecture.md#Authentication & Security]
- **边界/命名/格式**:`internal/project` 经 store/repository 触库、经 vault 取凭据,不直接 open DB / 不碰 HTTP;httpapi 唯一对外面;DB snake_case / JSON camelCase / Go PascalCase / `-er`·`Service` / `ErrXxx` / RFC3339;成功返资源对象不套壳;错误体永不含 secret。[Source: architecture.md#Boundaries / Implementation Patterns]
- **纯 Go**:`go-git/v5` 纯 Go,保单二进制零宿主依赖(**不**要求宿主装 git);modernc sqlite 禁 CGO;沿用 1-1 WAL/busy_timeout/MaxOpenConns(1)+迁移事务。
- **≤100MB**:go-git 仅增二进制几 MB,不驻留;**勿在 init() 跑重活**(教训:1-2 dummy-hash);终验 mem-check 不回退。
- **2-1 边界**:本期新建项目是**简单表单**(名称/仓库/凭据/测试连接),**不含 AI 生成向导**(=Story 2-5)、不含流水线配置编辑(=2-2/2-3/2-4);列表的"上次运行/目标服务器"字段就位但数据后续填。

### 环境
- Go 在 `~/sdk/go/bin`(`export PATH`);`go get github.com/go-git/go-git/v5` 需联网 → 加 `dangerouslyDisableSandbox`;GOPROXY=goproxy.cn 已设。npm=npmmirror。master key 见 1-3(测试 vault 路径需 `PIPEWRIGHT_MASTER_KEY`)。

### 禁止
- ❌ 凭据明文入库/日志/响应/错误体;❌ 前端显凭据明文;❌ 本期塞 AI 向导或流水线配置;❌ 字符串拼接执行命令;❌ CGO sqlite;❌ 后端碰 web/ 或前端碰 Go;❌ init() 跑重活;❌ 回退 1-1/1-2/1-3 安全修复。

### References
- [Source: epics.md#Epic 2 / Story 2.1]
- [Source: prd.md#FR-3(凭据引用/私有仓库克隆)/ FR-1]
- [Source: architecture.md#Authentication & Security / Architectural Boundaries / 强制项 / Implementation Patterns]
- [Source: EXPERIENCE.md#IA(项目列表 二级目的地)/ State Patterns(空态/凭据错误/字段错误)/ 新建项目流程]
- [Source: DESIGN.md#Components(卡片/状态徽标/模态/输入/空态)/ mock-projects.html · mock-new-project.html(结构参考)]
- [Source: 1-3(凭据 API + Vault.Get)/ 1-5(http 包装器、壳、路由)]

## Dev Agent Record

### Agent Model Used
claude-sonnet-4-6 (前端 Task 5)
claude-opus-4-8 (后端 Task 1-4)

### Debug Log References
- `vue-tsc --noEmit` → 0 errors
- `vite build` → success (2.08s), Projects chunk 26.91 kB / gzip 7.59 kB
- (后端)`gofmt -l cmd internal embed.go` → 空(干净)
- (后端)`go vet . ./cmd/... ./internal/...` → 干净
- (后端)`go test . ./cmd/... ./internal/...` → 全绿(httpapi/project/store/vault 全 ok)
- (后端)`CGO_ENABLED=0 go build ./cmd/pipewright` → OK
- (后端)`go mod tidy` → OK(新增 go-git/v5 v5.19.1 及其传递依赖)
- (后端)`make mem-check` → resident 20016 KB(19 MB),≤100 MB,未回退(go-git 仅增二进制,不驻留)
- (后端)真实二进制冒烟:test-clone 坏 URL→422 repo_unreachable;创建指向不存在凭据→422 credential_error;file:// 裸仓库创建→201 且 defaultBranch 由远端 HEAD 探测为 production;GET 列表 DTO 含 credentialName/lastRunStatus:null/targetServers:[];PATCH 200 / DELETE 204 / 再 DELETE 404;`grep` 整库(db/wal/shm)+ 日志 → 0 处明文 token

### Completion Notes List
- **Task 5 完成(前端)**:
  - `web/src/api/projects.ts`:按契约封装 list/create/update/delete/testClone,类型与字段完全对齐(RunStatus 固定六词,Project 含 credentialName 只读展示、lastRunStatus/targetServers 本期可 null/[])。CSRF 通过 http 模块自动附加。
  - `web/src/views/Projects.vue`:卡片网格(名称/仓库+分支等宽/状态徽标/目标服务器/凭据掩码引用/快捷操作)+ 搜索框 + 状态筛选分段控件;空态(虚线+图标+CTA)+搜索空态+加载骨架;骨架用 shimmer,不用转圈遮罩。
  - **新建项目模态**:名称+仓库地址+凭据下拉(仅 git_token,显名称+掩码,值=credentialId)+默认分支(可选)+测试连接按钮(三态:ok/凭据错误/不可达)+创建。凭据永不显明文。
  - **重命名模态** + **删除二次确认模态**:按设计规范实现(破坏性操作确认弹窗)。
  - **状态徽标**:固定六词(成功/失败/进行中/部分失败/已回滚/排队中)+ 语义令牌颜色 + 进行中脉冲动效。
  - 双主题(dark/light)通过令牌变量成立;hover/focus/active 状态经设计;prefers-reduced-motion 降级。
  - 不可达时优雅错误态+重试,不白屏。后端未就绪时 testClone/create 的 code 映射凭据错误/仓库不可达明确文案。
  - 路由 `/projects` 注册为 AppShell 壳内子路由;AppShell rail 新增「项目」导航项(GitBranch 图标)。
- **Task 1-4 完成(后端)**:
  - **克隆校验做法**:go-git v5 `Remote.ListContext`(= `git ls-remote` 语义,纯 Go,**不要求宿主装 git**),全程 `memory.NewStorage()` 内存进行不落盘;HTTPS + `http.BasicAuth{Username:"git", Password:token}` 参数化鉴权(**绝不拼命令字符串**);token 经 `vault.Get(credentialId)` 进程内取,Probe 后即清引用,**绝不**入库/日志/响应/错误体。成功路径解析 HEAD 符号引用得远端默认分支(`defaultBranch` 缺省时回填)。
  - **错误映射**:鉴权类哨兵错误(`transport.ErrAuthenticationRequired/ErrAuthorizationFailed/ErrInvalidAuthMethod`)+ 401/403 文本嗅探 → `ErrCredentialError`;其余(DNS/连接/不存在)→ `ErrRepoUnreachable`。底层错误**不 %w 外泄**(避免含敏感细节)。凭据不存在→`ErrCredentialNotFound`,vault 未配置→`ErrVaultUnconfigured`。
  - **HTTP**:`writeProjectError` 把领域错误映射契约错误码(credential_error|repo_unreachable|vault_unconfigured 全 422,project_not_found 404,invalid_project 400);成功返资源对象不套壳;DTO camelCase。
  - **FK RESTRICT**:被项目引用的凭据删除被阻止(测试验证);创建/改绑遇外键失败竞态归 `ErrCredentialNotFound`。
  - **改绑凭据重做校验**:`Update` 带 `credentialId` 时用新凭据对仓库重做 ls-remote 校验(测试验证 prober 被再次调用且收到新明文)。

### File List
- `web/src/api/projects.ts` (新建)
- `web/src/views/Projects.vue` (新建)
- `web/src/router/index.ts` (修改:添加 Projects 路由)
- `web/src/layouts/AppShell.vue` (修改:添加「项目」rail 导航项)
- `internal/project/project.go` (新建:Project 模型 + Service + 领域错误)
- `internal/project/prober.go` (新建:go-git ListRemote 实现 + 错误分类 + 默认分支解析)
- `internal/project/project_test.go` (新建:domain CRUD/校验/FK/test-clone 测试,stub prober)
- `internal/project/prober_test.go` (新建:真实 go-git prober 坏 URL + 本地裸仓库成功路径)
- `internal/store/migrations/0004_projects.sql` (新建:projects 表 + FK RESTRICT + 索引)
- `internal/httpapi/projects.go` (新建:DTO + 5 端点 handler + 错误映射)
- `internal/httpapi/projects_test.go` (新建:HTTP CRUD/422 路径/CSRF/auth/无明文断言)
- `internal/httpapi/router.go` (修改:WithProjects Option + 挂载 /api/projects* 路由)
- `cmd/pipewright/main.go` (修改:装配 project.Service 并注入)
- `go.mod` / `go.sum` (修改:新增 go-git/v5 依赖)

## Change Log

- 2026-05-29:创建 Story 2.1 上下文(项目接入与列表),前后端并行;冻结项目 API 契约;克隆校验定为 go-git 纯 Go ls-remote。Status → in-progress。
- 2026-05-29:Task 5 前端完成(claude-sonnet-4-6):projects API 模块 + Projects.vue + 路由 + AppShell 导航项。vue-tsc 0 error,vite build 成功。
- 2026-05-29:Task 1-4 后端完成(claude-opus-4-8):project 领域包(go-git v5 纯 Go ls-remote 克隆校验,token 仅进程内、绝不外泄)+ 0004 迁移(FK RESTRICT)+ 5 个 HTTP 端点(冻结契约,credential_error/repo_unreachable/vault_unconfigured 422)+ 域/HTTP 测试。gofmt/vet/test/build/mod-tidy 全过;mem-check 19MB 未回退;真实二进制冒烟全绿且整库+日志无明文。Status → review。
