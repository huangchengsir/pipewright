---
baseline_commit: NO_VCS
---

# Story 1.1: 项目脚手架与 ≤100MB 体积门

Status: done

<!-- Note: Validation is optional. Run validate-create-story for quality check before dev-story. -->

## Story

As a 平台维护者(单管理员 / 作者本人),
I want 一个可编译运行的最小 Go 单二进制骨架(Chi + modernc/sqlite + 内嵌 Vue3,go:embed),且 CI 断言常驻内存 ≤100MB、可双运行(原生 / Docker),
so that 后续一切功能都建立在一个轻量、体积受控、双运行的地基上,且 ≤100MB 红线从第一天起就被自动守住。

## Acceptance Criteria

1. **AC1 — 可构建、双运行、内嵌前端、健康端点**:干净检出执行 `make build` 产出**单个静态二进制**(`CGO_DISABLED`),经 `go:embed` 内嵌 Vue3 构建产物;**原生直跑**与**容器运行**两种方式都能启动并对 `GET /healthz` 返回 200。
2. **AC2 — ≤100MB 体积门(CI 回归断言)**:二进制空载运行时常驻内存 ≤100MB;CI 中存在该断言,超标则流水线**失败**(NFR-4 / SM-C1)。
3. **AC3 — 纯 Go SQLite + 迁移**:使用 `modernc.org/sqlite`(纯 Go,无 CGO);首次启动自动应用内嵌 SQL 迁移并建库(仅迁移基建 + `schema_migrations` 跟踪表,**不建任何领域表**),全程无需 CGO。
4. **AC4 — 目录与命名规范基线**:目录结构符合架构 `cmd/` + `internal/` + `web/` 布局;三层命名映射(DB snake_case / JSON camelCase / Go PascalCase)的工具与约定就位(gofmt + golangci-lint;eslint + prettier);`go build` 全静态可交叉编译。

## Tasks / Subtasks

- [x] **Task 1 — Go module 与结构骨架**(AC: 1, 4)
  - [x] `go mod init github.com/huangchengsir/pipewright`;go.mod 锁定 go 1.26.3
  - [x] 建最小结构:`cmd/pipewright/main.go`、`embed.go`(`//go:embed all:web/dist`)、`internal/httpapi/`、`internal/store/`、`internal/config/`
  - [x] 未建任何多余领域包(auth/vault/pipeline… 留各自 story 按需创建)
- [x] **Task 2 — HTTP server(Chi)+ 健康端点 + SPA 托管**(AC: 1)
  - [x] `internal/httpapi/router.go`:Chi 路由;`GET /healthz` → 200 `{"status":"ok"}`(camelCase JSON)
  - [x] 经 `embed.go` 用 `go:embed` 托管 `web/dist`(SPA fallback → index.html);httpapi 为唯一对外面
- [x] **Task 3 — SQLite(modernc)+ 启动迁移**(AC: 3)
  - [x] `internal/store`:`modernc.org/sqlite` 打开库(CGO_DISABLED 可静态);仅 store 触库
  - [x] `internal/store/migrations/0001_baseline.sql`:内嵌、启动自动应用;建 `schema_migrations`;不建领域表(测试断言)
  - [~] repository 接口骨架:**延后**——当前 store 仅暴露 `*sql.DB`,repository 接口待首个有领域表的 story 引入(无 AC 要求,不阻塞)
- [x] **Task 4 — Vue3 + Vite 前端骨架 + 构建产物**(AC: 1, 4)
  - [x] `web/`:Vite 6 + Vue 3 占位应用(Naive UI/Pinia/Router 留 Story 1.5/1.6)
  - [x] `vite build` → `web/dist`(已内嵌验证);prettier 配置 **被仓库 config-protection 钩子拦截**,eslint/prettier 依赖已装、配置文件留维护者(见完成说明)
- [x] **Task 5 — 双运行模式**(AC: 1)
  - [x] 原生:`CGO_ENABLED=0 go build` 产静态二进制(Mach-O arm64,15MB),直跑响应 `/healthz`
  - [~] 容器:`Dockerfile` 已写(node 构建前端 + golang 静态 + distroless);**`docker run` 验证延后**(本机未装 Docker,用户决定开发期不装)
- [x] **Task 6 — ≤100MB 体积门(CI 断言)**(AC: 2)
  - [x] `scripts/mem-check.sh`:测空载 RSS 并断言 ≤100MB(实测 ~18MB),超标非零退出
  - [x] 接入 `Makefile`(`make mem-check`)与 `.github/workflows/ci.yml`(mem-budget job)
- [x] **Task 7 — Makefile / CI / 规范工具链**(AC: 1, 2, 4)
  - [x] `Makefile`:`build` / `test` / `vet` / `fmt` / `fmt-check` / `embed-frontend` / `mem-check` / `dev` / `run` / `clean`
  - [x] `.github/workflows/ci.yml`:gofmt 检查 + `go vet` + `go test` + 静态构建 + 前端构建 + ≤100MB;**golangci-lint 不引入**(配置文件被钩子拦,改用内置 gofmt+vet)
  - [x] 三层命名约定落地(DB snake_case / JSON camelCase / Go PascalCase);Go 命令限定 `. ./cmd/... ./internal/...` 避免扫 node_modules

### Review Findings(2026-05-29 三层对抗评审:Blind / Edge / Acceptance)

**Acceptance 结论:Approve-with-deferrals(无 AC-blocking)。** 以下为待处理项。

Patch(代码可直接修,无歧义):
- [x] [Review][Patch] HTTP 服务无超时(slowloris)+ 无优雅停机 + ErrServerClosed 未处理 [cmd/pipewright/main.go](blind+edge,High/Critical)
- [x] [Review][Patch] 迁移 apply+record 非事务,崩溃/多语句部分失败会半迁移且不记录 → 包进 `sql.Tx` [internal/store/store.go:migrate](blind+edge,High)
- [x] [Review][Patch] SQLite 未设 busy_timeout/WAL/foreign_keys、未 `SetMaxOpenConns(1)` → 并发即 SQLITE_BUSY [internal/store/store.go:Open](blind+edge,High)
- [x] [Review][Patch] SPA fallback 对缺失带扩展名资源/接口误路由返回 `200 text/html`(应 404);顺带加 `X-Content-Type-Options: nosniff` + 资源缓存头 [internal/httpapi/router.go:spaHandler](blind+edge,Medium)
- [x] [Review][Patch] `mem-check.sh`:`mktemp ).db` 路径与实际创建文件不一致(泄漏临时文件);健康探测 40 次失败后未硬失败(仅靠空 RSS 兜底)[scripts/mem-check.sh](edge,Medium)
- [x] [Review][Patch] 空/错误 `web/dist` 编译通过但运行期白屏 → 启动时校验内嵌 index.html 存在则 fail-fast;并打印解析后的 DB 绝对路径(防相对路径 CWD 漂移)[embed.go/cmd/pipewright/main.go](edge,Medium)
- [x] [Review][Patch] vite `base: './'` 与 SPA 深路由 fallback 组合下,二级深链相对资源解析错位 → 单二进制服务于根,改 `base: '/'` 并重建 dist [web/vite.config.ts](blind+edge,Low)

Defer(真实但非现在做,已记入 deferred-work.md):
- [x] [Review][Defer] 完整 CSP 策略(留安全专项)[internal/httpapi/router.go] — 设计决策,后续安全 story
- [x] [Review][Defer] 配置校验(Addr/DBPath 合法性)[internal/config/config.go] — 单管理员,失败可见,低风险
- [x] [Review][Defer] 迁移 checksum/缺口/重复版本检测 [internal/store/store.go] — 当前仅 1 条空迁移
- [x] [Review][Defer] 前端 eslint/prettier 配置文件 + 复原 golangci-lint [web/] — 被仓库 config-protection 钩子拦,`npm run lint` 暂不可用,留维护者
- [x] [Review][Defer] 容器运行验证(AC1 另一半)[Dockerfile] — 本机无 Docker(用户决定),发镜像前再验
- [x] [Review][Defer] 请求体大小限制 + 限流 + chi Timeout 中间件 [internal/httpapi] — 暂无 POST 端点
- [x] [Review][Defer] serveIndex 条件请求/ETag [internal/httpapi/router.go] — 低优
- [x] [Review][Defer] CI 加 `-race`、`on:push` 分支过滤 [.github/workflows/ci.yml] — 暂无并发/成本项
- [x] [Review][Defer] Dockerfile `GOSUMDB=off`:网络恢复后复原(go.sum 已提交保完整性)[Dockerfile]

Dismissed(噪声/误报/已覆盖,共 5):路径穿越(stdlib 清洗 + embed.FS 拒 `..`,已安全)· writeJSON 编码错误(静态 map 不可达)· CI 占位 dist 掩盖真实内嵌(mem-budget job 已用真实产物覆盖)· repository 接口延后(合理,无 AC 要求)· getenv 空串当未设(刻意设计)。

## Dev Notes

### Technical Requirements(必须遵守)

- **语言/分发**:Go,单**静态**二进制;双运行模式 = 平台二进制可原生运行,构建在容器内执行(本 story 不涉及构建执行,只搭平台骨架)。[Source: architecture.md#8.1 / Selected Foundation]
- **HTTP**:Chi(`github.com/go-chi/chi/v5`)—— 基于 net/http、最轻、GC 友好。[Source: architecture.md#技术基座决策 / API & Communication Patterns]
- **持久化**:**`modernc.org/sqlite`(纯 Go,无 CGO)** —— 这是双运行/全静态交叉编译的前提。**严禁使用 `mattn/go-sqlite3`(需 CGO)**。[Source: architecture.md#Data Architecture]
- **前端嵌入**:Vue3 + Vite 构建为静态资源 → `go:embed` 嵌入二进制(嵌入产物仅数 MB)。[Source: architecture.md#Frontend Architecture]
- **单二进制 = 单次部署(显式约束)**:Pipewright 自身**不做前后端分离部署**——前端在 build 时即嵌入,运行时由同一个二进制的 `internal/httpapi` 在**同一端口**同时提供 SPA 静态资源 + REST/SSE/WS API。**禁止**把平台自身拆成独立前端服务器 / 独立 API 进程 / 给自身 SPA 配 nginx。部署形态二选一:① 原生二进制直跑;② 同一二进制塞进最小镜像。`vite dev` 代理到 `go run` 仅用于**本地开发**,非部署形态。
- **体积红线**:常驻内存 ≤100MB,**做成 CI 回归断言**(ADR-5);构建在隔离容器内执行、不计入平台常驻。[Source: architecture.md#Core Architectural Decisions / ADR-5;epics.md#Overview 横切验收 / NFR-4]

### Architecture Compliance(护栏)

- **目录树(本 story 落地其结构骨架)**[Source: architecture.md#Complete Project Directory Structure]:
  ```
  pipewright/
  ├── go.mod · go.sum · Makefile · Dockerfile
  ├── .github/workflows/ci.yml      # gofmt·golangci-lint·eslint·test·≤100MB 断言
  ├── cmd/pipewright/main.go        # 入口:加载配置 → 装配依赖 → 起 HTTP
  ├── embed.go                      # //go:embed web/dist
  ├── internal/
  │   ├── httpapi/                  # chi 路由·中间件·SSE·WS(本 story 仅 router + health)
  │   ├── store/                    # SQLite(modernc)·repository·migrations/
  │   └── config/                   # 平台配置(master key 来源等;本 story 占位)
  └── web/                          # Vue3 + Vite;dist → go:embed
  ```
- **边界**:`internal/httpapi` 是唯一对外面,领域包不直接碰 HTTP;**仅 `internal/store` 触达 SQLite**,其它包经 repository 接口。[Source: architecture.md#Architectural Boundaries]
- **命名**:DB snake_case(`schema_migrations`、`created_at`)/ JSON **camelCase**(经 struct tag)/ Go PascalCase 导出;抽象接口用 `-er` 后缀;错误变量 `ErrXxx`。[Source: architecture.md#Implementation Patterns & Consistency Rules]
- **响应格式**:成功直接返回资源对象(不套 `{data}` 壳);错误统一 `{ "error": { "code": "...", "message": "..." } }`、**永不含 secret/内部栈**;时间 ISO 8601(RFC3339)。[Source: architecture.md#格式规范]
- **构建工作流**:`make build` = 前端 `vite build` → `web/dist` → `go build`(`CGO_DISABLED` 静态)→ 单二进制 → ≤100MB 断言。[Source: architecture.md#开发/构建/部署工作流]

### Library / Framework Requirements(版本)

- **以实现时 `go.mod` / `package.json` 锁定为准**——架构未硬编码版本号。开建时拉**最新稳定版**并 pin。[Source: architecture.md#技术基座决策 备注]
- 建议(开建前自行核验最新稳定版):Go 最新稳定工具链;`go-chi/chi/v5`;`modernc.org/sqlite` 最新;Vue 3.5+、Vite 6+;lint:`golangci-lint`、`eslint` + `prettier`。
- **不引入**:重型 starter、`code-server`/内嵌 VSCode(违反 D1 体积);CGO SQLite 驱动。[Source: architecture.md#Starter Template Evaluation / D1]

### Testing Requirements

- Go:标准 `testing`;至少覆盖 `GET /healthz` 返回 200、迁移可成功应用(空库 → schema_migrations 就位)。
- **≤100MB 断言**作为 CI 必过项(测试或脚本测 RSS)。
- 前端:`vite build` 成功 + eslint/prettier 通过即可(占位页)。
- 提交前:`gofmt` + `golangci-lint`;前端 `eslint` + `prettier`。[Source: architecture.md#强制项]
- 注:AC-SEC 自动化回归、领域测试在后续 story(1.3/1.4 等)建立;本 story 只立**测试 + CI 骨架**。

### Anti-patterns / 禁止(本 story 关键雷区)

- ❌ 用 `mattn/go-sqlite3`(CGO)——会破坏全静态/双运行。**必须 modernc.org/sqlite**。
- ❌ 一次性创建全部领域包/领域表("Epic 1 建全表"反模式);**只建 scaffold + 迁移基建 + health**。
- ❌ JSON 里混用 snake_case/camelCase;响应套多层 `{data:{data}}` 壳。
- ❌ 把 ≤100MB 断言留到以后——本 story 就要让它在 CI 里红/绿。

### Project Structure Notes

- 本 story 落地的是**结构骨架**:`cmd` + `internal/{httpapi,store,config}` + `web` + `embed.go` + 构建/CI 文件。其余领域包(auth/vault/project/trigger/pipeline/build/deploy/target/monitor/logs/notify/ai/secret/audit)由各自 story 首次需要时创建。
- `.github/workflows/ci.yml`:Pipewright **自身仓库**用 GitHub Actions 跑 CI(开源在 GitHub);这与产品**面向用户的首发代码平台 Gitee**(FR-1)是两回事,不冲突。
- 无前序 story(本 epic 第一个);当前工作目录非 git 仓库,实现时按需 `git init`。

### References

- [Source: epics.md#Epic 1 / Story 1.1 + Overview 横切验收 + M1 最薄竖切]
- [Source: architecture.md#Selected Foundation:最小有意图栈 / 技术基座决策]
- [Source: architecture.md#Data Architecture(modernc/sqlite 纯 Go 无 CGO)]
- [Source: architecture.md#Frontend Architecture(Vue3+Vite+go:embed)]
- [Source: architecture.md#Core Architectural Decisions / ADR-5(≤100MB CI 断言)]
- [Source: architecture.md#Complete Project Directory Structure / Architectural Boundaries]
- [Source: architecture.md#Implementation Patterns & Consistency Rules(三层命名/强制项/反模式)]
- [Source: prd.md#NFR §8.2 体积(≤100MB)/ §8.1 双运行 / SM-C1]
- [Source: EXPERIENCE.md#Foundation(技术基座一致性:Vue3 + go:embed + 双运行无差异)]

## Dev Agent Record

### Agent Model Used

claude-opus-4-7 (1M context) · bmad-dev-story

### Debug Log References

- 环境:本机原无 Go;经阿里云镜像(`mirrors.aliyun.com/golang`)装 **go1.26.3 darwin/arm64** 到 `~/sdk/go`(go.dev/google.cn 在本网络不可达,系统 curl LibreSSL 过旧)。
- Go 模块代理:`GOPROXY=https://goproxy.cn,direct`、`GOSUMDB=off`、`GOTOOLCHAIN=local`(外网受限)。npm registry=`registry.npmmirror.com`。
- 关键验证(均通过):
  - `go vet . ./cmd/... ./internal/...` 干净;`go test` 全绿(httpapi 3 例 + store 2 例)。
  - 静态构建 `CGO_ENABLED=0 go build` → Mach-O arm64 单二进制 15MB。
  - 原生运行:`/healthz`→`{"status":"ok"}`;`/`→200 且引用内嵌 hash 资源;深路由 `/runs/128`→200(SPA fallback);内嵌资源 200。
  - `vite build`(vite 6.4.2):dist 含 index.html + hash 化 JS/CSS;Go 重建后内嵌真实产物并正确服务。
  - `make mem-check`:空载 RSS ~18MB ≤ 100MB(NFR-4)✅。

### Completion Notes List

- Ultimate context engine analysis completed - comprehensive developer guide created.
- **已实现并验证**:Go 单二进制脚手架(Chi + modernc/sqlite 纯 Go 无 CGO + 内嵌 Vue3/Vite,go:embed)、`/healthz`、SPA 托管+fallback、SQLite 启动迁移(`schema_migrations`,不建领域表,幂等,含测试)、原生静态构建与运行、≤100MB 体积门脚本 + Makefile + CI。
- **AC 状态**:AC1 原生构建+运行✅(容器运行见下延后);AC2 ≤100MB✅(实测~18MB,CI 断言就位);AC3✅(modernc 纯 Go、迁移、无领域表,测试覆盖);AC4 目录/三层命名/gofmt✅(前端 lint 配置见下受阻)。
- **⚠️ 延后/受阻(需评审知悉,非谎报完成)**:
  1. **容器运行未验证**:`Dockerfile` 已交付,但本机未装 Docker(用户决定开发期不装),`docker run` 半个 AC1 **延后**到需要发布镜像时验证。
  2. **前端 eslint/prettier 配置文件未落地**:仓库的 `config-protection` 钩子拦截创建 `eslint.config.js` / `.prettierrc.json`。eslint/prettier **依赖已装**,配置文件留维护者添加(或临时关钩子);Go 侧用内置 gofmt(无需配置)已满足。CI 因此用 gofmt+vet 而非 golangci-lint。
  3. repository 接口骨架延后至首个有领域表的 story(无 AC 要求)。
- **环境约定**(供后续 story / 复现):Go 在 `~/sdk/go/bin`(需加入 PATH);GOPROXY/GOSUMDB/GOTOOLCHAIN 已 `go env -w` 持久化;npm 用 npmmirror。

### File List

新增:
- `go.mod`, `go.sum`
- `embed.go`
- `cmd/pipewright/main.go`
- `internal/config/config.go`
- `internal/store/store.go`, `internal/store/store_test.go`, `internal/store/migrations/0001_baseline.sql`
- `internal/httpapi/router.go`, `internal/httpapi/router_test.go`
- `web/package.json`, `web/package-lock.json`, `web/vite.config.ts`, `web/index.html`, `web/tsconfig.json`, `web/env.d.ts`
- `web/src/main.ts`, `web/src/App.vue`
- `web/dist/*`(vite 构建产物,go:embed)
- `Makefile`, `Dockerfile`, `.gitignore`
- `scripts/mem-check.sh`
- `.github/workflows/ci.yml`

(未提交 git;`web/node_modules/` 已 gitignore)

## Change Log

- 2026-05-28:实现 Story 1.1 项目脚手架——Go 单二进制(Chi + modernc/sqlite + 内嵌 Vue3)、健康端点、SQLite 启动迁移、原生双运行、≤100MB CI 体积门、Makefile/CI。容器运行与前端 lint 配置因环境/钩子延后(见完成说明)。Status → review。
- 2026-05-29:代码评审(Blind/Edge/Acceptance 三层)修复 7 项 patch——HTTP 超时+优雅停机、迁移事务化、SQLite busy_timeout/WAL/FK+MaxOpenConns(1)、SPA 缺失资源 404+nosniff/缓存头、mem-check.sh mktemp/就绪硬失败、启动校验内嵌 index.html+打印 DB 绝对路径、vite base '/'。9 项 defer 入 deferred-work.md,5 项 dismissed。全量 test/vet/gofmt/mem-check 复绿。Status → done。
