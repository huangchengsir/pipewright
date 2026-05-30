---
stepsCompleted: [1, 2, 3, 4, 5, 6, 7, 8]
lastStep: 8
status: 'complete'
completedAt: '2026-05-27'
inputDocuments:
  - '_bmad-output/planning-artifacts/prds/prd-devopsTool-2026-05-27/prd.md'
  - '_bmad-output/planning-artifacts/prds/prd-devopsTool-2026-05-27/addendum.md'
  - '_bmad-output/planning-artifacts/briefs/brief-devopsTool-2026-05-27/brief.md'
  - '_bmad-output/planning-artifacts/briefs/brief-devopsTool-2026-05-27/addendum.md'
workflowType: 'architecture'
project_name: 'devopsTool'
user_name: ''
date: '2026-05-27'
---

# Architecture Decision Document

_This document builds collaboratively through step-by-step discovery. Sections are appended as we work through each architectural decision together._

## Project Context Analysis

### Requirements Overview

**Functional Requirements:** 26 条 FR,6 个 Feature,映射到约 9 个架构子系统:
Webhook 网关 · 流水线引擎 · 容器隔离构建执行器 · 外部 registry 适配 ·
部署编排器(SSH;k8s 委托延后)· 纳管与监控 · 通知分发 · AI 诊断服务 · 凭据保险库;
统一收口于 Web UI + API,横贯审计层。

**Non-Functional Requirements:**
- 性能/体积:常驻内存 ≤100MB(Go 单静态二进制);构建在容器内,不计入平台内存。
- 双运行模式:Docker 容器运行 / 本地编译原生运行(平台二进制原生;构建仍需容器运行时)。
- 安全(第一约束):凭据加密保险库、命令 array 化防注入、审计 append-only 外部 sink、
  secret masking(含发往 LLM 前)、单管理员认证(无 RBAC)。详见 AC-SEC-01~04。
- 可靠性:部署回滚、健康检查门控、多机部署结果独立(非事务)。
- AI:提供商可配置(Claude/OpenAI/Ollama)、不可用时优雅降级。

**Scale & Complexity:**
- Primary domain: 后端平台(Go)+ Web UI,基础设施编排密集
- Complexity level: 中-高
- Estimated architectural components: ~9 个子系统

### Technical Constraints & Dependencies
- 语言/分发:Go,单静态二进制,双运行模式。
- 外部依赖:容器运行时(构建/容器部署)、外部镜像仓库(ACR/Docker Hub/Harbor)、
  目标机 SSH、LLM 提供商、凭据保险库。
- v1 全程 agentless(SSH);常驻 Go agent 延后至安全评审里程碑。
- 不自训模型:AI 采用现成 LLM + 精选知识库。
- **k8s 委托延后出 v1**(client-go 体积威胁 ≤100MB + 复杂度);v1 部署仅 SSH 普通机。

### Cross-Cutting Concerns Identified
1. 安全(贯穿凭据/命令执行/审计/日志/日志出网,最高优先)。
2. 双运行模式 × 构建隔离的语义边界(原生模式仍需容器运行时)。
3. 部署抽象:`Deployer` 接口,v1 仅 SSH 普通机实现。
4. AI 集成:LLM 提供商抽象、优雅降级、知识库与诊断反馈闭环数据、日志脱敏出网。
5. 可观测与审计:SSH 轮询采集 + append-only 审计 sink。
6. 体积预算:重依赖威胁 ≤100MB(见 D1)。

### 关键架构假设与待决风险(进入技术决策前须解决)
按「置信度 × 影响」评分;🔴 = 必须先决策。

- 🔴 A2 「本地编译原生」语义:平台二进制可原生运行,但**隔离构建始终需宿主机有容器运行时**
      (不必是 Docker daemon —— rootless BuildKit/buildah/kaniko/podman 更轻更安全)。
- 🔴 B3 普通机"零停机切换流量"缺机制:需定平台是否管理一层反代(自带/对接 nginx)或端口约定。
- 🔴 C2 日志出网脱敏:发往外部 LLM 前须 secret masking(扩展 AC-SEC-04);敏感场景默认/推荐本地 Ollama。
- 🔴 D1 体积预算威胁:client-go / 内嵌 VSCode 等是体积大户,可能破 ≤100MB;
      需体积预算硬门 + 重依赖按需/外置 + 把 100MB 做成 CI 回归断言。(已据此将 k8s 延后)
- 🟠 A1 隔离构建对容器运行时的依赖(具体运行时选型待决)
- 🟠 A3 构建发生位置(中控机 / 目标机 / 独立 runner)待定
- 🟡 C1 现成 LLM + 知识库诊断是否"足够好"(已挂 PRD Open Q#7,开建前验证)
- 🟡 C3 diff/反馈闭环依赖持久化的运行历史 + 环境快照(存储模型待定)

### 初步架构决策方向(ADR 草案,供后续决策步骤定稿)
- ADR-1 构建运行时:隔离构建依赖宿主机容器运行时,优先 rootless(BuildKit/buildah/kaniko),
        不强绑 Docker daemon。双运行模式 = 平台二进制可原生运行,但构建始终需容器运行时。【倾向已定】
- ADR-2 部署抽象与 k8s 范围:定义 `Deployer` 接口;**v1 仅实现 SSH 普通机**;k8s 委托延后。【已定】
- ADR-3 零停机切换机制:普通机需流量切换点 —— 选项 ① 平台自带轻量反代 ② 对接既有 nginx
        ③ 端口约定 + 健康门控。【待决,决策步骤定稿】
- ADR-4 AI 日志出网:发 LLM 前 secret masking;敏感默认本地 Ollama;架构画出"日志→脱敏→LLM"数据流。【倾向已定】
- ADR-5 体积预算:"≤100MB 常驻"做成 CI 回归断言;重依赖(内嵌 VSCode 等)按需/外置加载。【倾向已定】

> **✅ 已回写 PRD(2026-05-27)**:k8s 委托延后已同步进 FR-11/§4.3/§5/§6.2/§9。

## Starter Template Evaluation

### Primary Technology Domain
单 Go 二进制内嵌 SPA + 内嵌 SQLite(PocketBase 式自托管单体架构)。后端平台(Go)+ 内嵌 Web UI(Vue 3),基础设施编排密集。

### Starter Options Considered
- 重型全栈 starter(T3/RedwoodJS 等):❌ 属 JS 全栈生态,与 Go 单二进制不符。
- Go 全栈模板:多为示例级、绑定特定取向,与"有意图最小栈"取向冲突。
- **结论:不套重型脚手架,采用有意图的最小栈**(契合轻量 / 反模板 / 单二进制 / ≤100MB)。架构范例参考 PocketBase(SvelteKit+Go 单二进制,2026 生产可行)。

### Selected Foundation:最小有意图栈(无重型 starter)
**Rationale:** 重型 starter 会绑入大量用不上的依赖,与 ≤100MB、轻量、反模板取向冲突;最小栈最可控,也最契合单二进制 + go:embed 形态。

**技术基座决策:**
- **语言/运行时:** Go(单静态二进制;双运行模式 = 平台二进制可原生运行,构建需容器运行时)。
- **HTTP 框架:** Chi(基于 net/http、最轻、最小内存、GC 友好;契合 ≤100MB 与 clean architecture)。
- **持久化:** SQLite(关系型,支撑运行历史 / diff / 诊断反馈 / 审计的查询;单文件,契合单二进制)。备选 bbolt(纯 KV,关系化不足)。
- **前端:** Vue 3 + Vite,构建为静态资源,经 `go:embed` 嵌入二进制(嵌入产物仅数 MB)。
- **代码浏览(FR-4):** Monaco 编辑器前端懒加载(只读),**不用 code-server**(体积过大,违背 D1)。
- **构建隔离运行时:** 宿主机容器运行时,优先 rootless(BuildKit/buildah/kaniko),不强绑 Docker daemon(见 ADR-1)。

**初始化建议:** 不跑某个 CLI,而是 scaffold 上述最小布局(Go module + chi + sqlite + 内嵌 Vue/Vite),作为**第一个实现 story**;CI 中加入"二进制常驻内存 ≤100MB"回归断言(见 ADR-5)。

> 版本以实现时 `go.mod` / `package.json` 锁定为准(本步未硬编码版本号)。

## Core Architectural Decisions

### Decision Priority Analysis
- **Critical(阻塞实现):** 数据驱动 modernc/sqlite · 凭据保险库 · 认证 · Builder/Deployer 抽象 · 端口切换+健康门控
- **Important(塑造架构):** SSE/WS 实时分层 · 进程内任务调度 · 通知/AI Provider 抽象 · 审计 append-only
- **Deferred(v1 后):** k8s 委托 · 常驻 Go agent(内网穿透/实时监控)· 多用户/RBAC

### Data Architecture
- SQLite via **modernc.org/sqlite(纯 Go,无 CGO)**,支持 CGO_DISABLED 全静态交叉编译(双运行模式前提)。
- 入库实体:项目 · 流水线配置(YAML 文本+解析)· 运行历史 · 环境快照(支撑 FR-25 diff)· 诊断反馈(FR-26)· 凭据(加密)· 审计。
- 迁移:内嵌 SQL 迁移,启动时自动应用。

### Authentication & Security
- 单管理员认证:会话 cookie + argon2id 口令哈希;状态变更接口 CSRF 防护。无多用户/RBAC。
- 凭据保险库(AC-SEC-01):内嵌加密(NaCl secretbox / age),master key 来自环境变量/文件、不入库;明文永不入日志/界面。
- 命令执行(AC-SEC-02):SSH/docker exec 一律 exec 数组,目标白名单校验。
- 审计(AC-SEC-03):**本地 append-only**(专用不可改表/文件)+ 可选远端 sink(syslog/对象存储)。
- 日志脱敏(AC-SEC-04):落盘前 + **发往 LLM 前** 均做 secret masking。

### API & Communication Patterns
- REST(JSON)做 CRUD;统一错误结构;速率限制(状态变更接口)。
- **SSE** 单向流:实时日志 tail、运行状态、服务器指标(FR-15/16/23)。
- **WebSocket** 仅用于交互式容器终端(FR-18)。

### Frontend Architecture
- Vue 3 + Vite + Pinia(状态)+ Vue Router;组件库 Naive UI(轻、现代、契合"干净直观")。
- Monaco 只读、懒加载(FR-4);SSE 客户端订阅实时流。
- 构建为静态资源 → `go:embed` 嵌入二进制。

### Infrastructure & Deployment
- **Builder 抽象**:模型 A(Dockerfile)+ 模型 B(选定工具链镜像跑命令);执行位置支持**中控机本地** 与 **远程构建机(经 SSH 派发,Jenkins 式)**;运行时优先 rootless BuildKit/Buildah,Docker 在则可用。
- **Deployer 接口**:v1 仅 SSH 实现(`x/crypto/ssh`);镜像→拉+run、JAR→java -jar/systemd、dist→nginx。
- **零停机(普通机)**:端口约定 + 健康门控 —— 新版起新端口 → 健康通过 → 切换转发/符链 → 停旧版;失败回滚。平台不接管反代。
- **任务调度**:进程内 worker pool + SQLite 持久化运行/任务状态(无外部队列);多流水线并行、非事务、结果独立(FR-13)。
- **通知 / AI**:Notifier 与 AI Provider 各自抽象 + 适配器(渠道:企业微信/钉钉/飞书/邮件/自定义webhook;Provider:Claude/OpenAI/Ollama)。
- **体积**:CI 内置"常驻内存 ≤100MB"回归断言;重依赖(Monaco 等)前端侧懒加载。

### Decision Impact Analysis
**Implementation Sequence(也即 dogfooding 竖切顺序):**
1. Scaffold 最小布局 + ≤100MB CI 断言(第一个 story)
2. 认证 + 凭据保险库(安全地基)
3. 项目 + 流水线配置 CRUD + 校验(FR-9)
4. Builder(中控本地)+ Gitee webhook 触发 → 隔离构建
5. Deployer(SSH)+ 端口切换/健康门控 → 部署到 1 台
6. 通知(企业微信/钉钉)→ 端到端竖切打通
7. 纳管/监控(SSH 轮询)+ 日志 SSE + 容器终端 WS
8. AI 失败分析 + diff(FR-25)+ 反馈闭环(FR-26)
9. 远程构建机 · 异常检测 · 其余渠道/平台横向铺开

**Cross-Component Dependencies:** 凭据保险库 → 所有外部连接;Builder 产物类型 → Deployer 方式;运行历史/环境快照 → diff/反馈;SSE/WS 基建 → 监控/日志/终端。

## Implementation Patterns & Consistency Rules

### 命名规范
- **数据库(SQLite):** 表名 snake_case 复数(`projects` `pipeline_runs` `target_servers`);列 snake_case(`created_at`);外键 `project_id`;索引 `idx_pipeline_runs_project_id`。
- **API:** REST 复数名词(`/api/projects`、`/api/projects/{id}/runs`);路径参数 `{id}`;**JSON 字段统一 camelCase**(对齐 TS 前端);HTTP 状态码语义化(200/201/400/401/403/404/409/422/429/500)。
- **Go 代码:** 包名小写单词;导出 PascalCase;文件名小写(`pipeline.go`);抽象接口用 `-er`(`Builder`/`Deployer`/`Notifier`/`Provider`);错误变量 `ErrXxx`。
- **Vue/TS:** 组件 PascalCase(`ServerCard.vue`);组合式函数 `useXxx`;Pinia store `useXxxStore`;变量 camelCase。

### 结构规范
- **Go:** `cmd/devopstool/main.go` 入口;领域包在 `internal/` 下按领域分(`pipeline` `build` `deploy` `server` `notify` `ai` `auth` `vault` `audit` `store`);测试就近 `_test.go`;迁移在 `internal/store/migrations/`。
- **前端:** `web/`,内部按领域 `src/{views,components,composables,stores,api}`;构建产物 `go:embed`。

### 格式规范
- **成功响应**:直接返回资源对象/数组(不套 `{data}` 壳)。
- **错误响应**:统一 `{ "error": { "code": "...", "message": "..." } }`;**永不含 secret/内部栈**。
- **时间**:JSON 一律 ISO 8601(RFC3339)字符串。
- **JSON**:camelCase;布尔 true/false。

### 通信规范
- **领域事件**(驱动通知/SSE):命名 `domain.action`(`build.failed` `deploy.succeeded` `run.partial_failed`);结构化负载。
- **SSE**:事件名与领域事件一致;**WebSocket** 仅容器终端,协议单独定义。
- **状态(Pinia)**:按领域分 store;action 动词 camelCase;不可变式更新。

### 过程规范
- **错误处理**:Go 用 `fmt.Errorf("...: %w", err)` 包裹;集中式 HTTP error mapper;用户态错误不泄内部。前端全局错误处理 + toast,区分用户态/系统态。
- **校验**:**服务端权威**(FR-9 配置校验);前端镜像校验仅为体验。
- **日志**:结构化 `slog` + 级别;secret masking;**审计与应用日志分离**。
- **加载态**:前端按请求在 store 维护;长任务走 SSE。

### 强制项(所有 AI agent MUST)
- 提交前 `gofmt` + `golangci-lint`;前端 `eslint` + `prettier`。
- DB snake_case / JSON camelCase / Go PascalCase 三层映射经 struct tag,不得混用。
- 任何外部连接的凭据只经 vault 取用,严禁明文/硬编码/入日志。
- 本《一致性规范》是 AI agent 的唯一事实源,冲突时以此为准。

### 反模式(禁止)
- JSON 里混用 snake_case/camelCase;响应套多层 `{data:{data}}` 壳;
- 用字符串拼接执行 SSH/exec 命令(违反 AC-SEC-02);
- 把 secret echo 进构建日志或错误消息。

## Project Structure & Boundaries

### Complete Project Directory Structure
```
devopstool/
├── README.md  ·  LICENSE  ·  go.mod  ·  go.sum
├── Makefile                      # build / test / lint / embed-frontend / ≤100MB 断言
├── Dockerfile                    # 双运行模式之一(容器运行)
├── .github/workflows/ci.yml      # gofmt·golangci-lint·eslint·test·≤100MB 回归断言
├── cmd/devopstool/main.go        # 入口:加载配置 → 装配依赖 → 起 HTTP
├── embed.go                      # //go:embed web/dist
├── internal/
│   ├── httpapi/                  # chi 路由·中间件(auth/CSRF/限流)·error mapper·SSE·WS
│   │   ├── router.go · middleware.go · sse.go · terminal_ws.go(FR-18)
│   ├── auth/                     # 单管理员·会话·argon2id·CSRF
│   ├── vault/                    # 凭据加密保险库(AC-SEC-01)
│   ├── secret/                   # secret masking(AC-SEC-04;被 logs/ai 复用)
│   ├── audit/                    # append-only 审计(AC-SEC-03)
│   ├── project/                  # 项目 + 流水线配置 CRUD/校验(FR-9)
│   ├── trigger/                  # webhook 网关 + 分支映射(FR-1,2)
│   ├── pipeline/                 # 运行编排·worker pool·运行/任务状态(FR-13)
│   ├── build/                    # Builder 抽象 + 本地/远程SSH构建机实现·rootless(FR-5~7)
│   ├── deploy/                   # Deployer 接口 + SSH 实现·端口切换/健康门控/回滚(FR-10~12)
│   ├── target/                   # 目标服务器登记/分组/SSH(FR-14)
│   ├── monitor/                  # SSH 轮询状态/指标 + 异常检测(FR-15,23)
│   ├── logs/                     # 日志采集/tail(FR-16)
│   ├── notify/                   # Notifier 抽象 + 渠道适配器(FR-19~21)
│   ├── ai/                       # Provider 抽象·知识库·失败分析·diff·反馈(FR-22~26)
│   ├── store/                    # SQLite(modernc)·repository·migrations/
│   └── config/                   # 平台配置(master key 来源等)
└── web/                          # Vue3 + Vite
    ├── package.json · vite.config.ts · index.html
    └── src/{main.ts, router/, stores/(Pinia 按域), api/(REST+SSE), composables/,
             views/(项目/运行/服务器/通知/设置), components/code/(Monaco 懒加载只读+diff FR-4,25)}
    └── dist/                     # 构建产物 → go:embed
```

### Architectural Boundaries
- **API 边界**:`internal/httpapi` 是唯一对外面;领域包不直接碰 HTTP。所有写接口过 auth+CSRF+审计中间件。
- **组件边界**:前端只经 `web/src/api` 与后端通信(REST + SSE);WS 仅终端。
- **服务边界(领域)**:领域包间经接口调用,不互相导入实现;`vault`/`secret`/`audit`/`store` 为被复用的横切基座。
- **数据边界**:仅 `internal/store` 触达 SQLite;其它包经 repository 接口取数,不写裸 SQL。

### Requirements → Structure 映射
- 触发/接入(FR-1~4)→ `trigger` + `project` + `web/.../code`(只读浏览/diff)
- 构建(FR-5~9)→ `build` + `project`(配置校验)+ `store`(镜像仓库凭据经 `vault`)
- 部署(FR-10~13)→ `deploy` + `target` + `pipeline`
- 管理(FR-14~18)→ `target` + `monitor` + `logs` + `httpapi/terminal_ws`
- 通知(FR-19~21)→ `notify`(订阅 `pipeline` 领域事件)
- AI(FR-22~26)→ `ai`(消费 `logs` + `store` 运行历史/环境快照,经 `secret` 脱敏)
- 安全(AC-SEC-01~04)→ `vault` + `secret` + `audit` + `auth`(横切)

### 数据流
webhook → `trigger` → `pipeline` 建 run → `build`(隔离容器,产物)→ `deploy`(SSH 推送+端口切换)→ 领域事件 → `notify` + SSE 推前端;失败 → `ai` 读日志(脱敏后)→ 诊断写 `store` → 前端展示 + 反馈回写。

### 开发/构建/部署工作流
- **开发**:前端 `vite dev` 代理到 Go API;后端 `go run`。
- **构建**:`make build` = 前端 `vite build` → `web/dist` → `go build`(CGO_DISABLED 静态)→ 单二进制 → ≤100MB 断言。
- **分发(双运行)**:① 原生二进制直跑;② 该二进制塞进最小镜像运行。

## Architecture Validation Results

### Coherence Validation ✅
- **决策兼容**:Go + Chi + SQLite(modernc 纯 Go)+ Vue/Vite + go:embed 单二进制 —— 互相兼容;modernc 驱动支撑 CGO_DISABLED 静态交叉编译,正是双运行模式前提,无矛盾。
- **模式一致**:DB snake_case / JSON camelCase / Go PascalCase 三层映射与栈一致;Notifier/Builder/Deployer/Provider 抽象与领域包结构对齐。
- **结构对齐**:`internal/` 领域分包 + `httpapi` 单一对外面 + `store` 单一触库,边界自洽。

### Requirements Coverage Validation
- **功能需求(26 条 FR)**:全部有架构归属。⚠️ **FR-11 的 k8s 委托延后** → v1 仅 SSH 普通机覆盖,k8s 留待后续(见 Gap)。
- **非功能需求**:≤100MB(CI 断言 + 轻量栈)· 双运行 · 安全 AC-SEC-01~04 · 可靠性(回滚/健康门控/独立结果)· AI 提供商可配 + 降级 —— 均覆盖。

### Implementation Readiness Validation ✅
- 决策完整(含选型理由,ADR-3 已拍板端口+健康门控);结构完整(目录树 + 边界 + FR→目录映射 + 数据流);模式完整(命名/结构/格式/通信/过程 + 强制项 + 反模式)。

### Gap Analysis Results
- **Critical(阻塞)**:无。
- **Important(需跟进,不阻塞)**:
  1. k8s 委托延后 与 PRD(FR-11/§5/§6)冲突 → 架构定稿后必须 `bmad-prd` update 回写。
  2. UX 文档缺失 → 前端界面/交互细节待 `bmad-ux`;架构已给前端技术约束。
  3. AI 诊断差异化验证(PRD Open Q#7)→ 开建前最小实验(前置)。
- **Minor**:端口切换转发/符链实现、远程构建机 SSH 派发协议、异常检测规则模型 —— 留实现阶段。

### Architecture Completeness Checklist
**Requirements Analysis:** [x] 上下文分析 · [x] 规模复杂度 · [x] 技术约束 · [x] 横切关注
**Architectural Decisions:** [x] 关键决策含理由 · [x] 技术栈完整 · [x] 集成模式 · [x] 性能(≤100MB)
**Implementation Patterns:** [x] 命名 · [x] 结构 · [x] 通信 · [x] 过程
**Project Structure:** [x] 目录结构 · [x] 组件边界 · [x] 集成点 · [x] 需求→结构映射

### Architecture Readiness Assessment
- **Overall Status:** READY WITH MINOR GAPS(16 项清单全 [x]、无 Critical Gap;3 项 Important 跟进:PRD 回写 k8s、UX 待做、开建前 AI 验证实验)
- **Confidence Level:** high
- **Key Strengths:** 单二进制双运行的栈高度自洽;安全从设计期落到 AC;一致性规范专为多 AI agent 并行而设;dogfooding 竖切顺序清晰。
- **Areas for Future Enhancement:** k8s 委托 · 常驻 Go agent(内网穿透/实时监控)· 多用户 RBAC。

### Implementation Handoff
- **AI Agent Guidelines:** 严格遵循本文档决策与一致性规范;尊重目录边界;一切架构问题以本文档为准。
- **First Implementation Priority:** scaffold 最小布局(Go module + chi + modernc/sqlite + 内嵌 Vue/Vite)+ CI 的 ≤100MB 断言。
