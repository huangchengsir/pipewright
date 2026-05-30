---
stepsCompleted: [1, 2, 3, 4]
status: 'complete'
completedAt: '2026-05-28'
inputDocuments:
  - '_bmad-output/planning-artifacts/prds/prd-Pipewright-2026-05-27/prd.md'
  - '_bmad-output/planning-artifacts/prds/prd-Pipewright-2026-05-27/addendum.md'
  - '_bmad-output/planning-artifacts/architecture.md'
  - '_bmad-output/planning-artifacts/ux-designs/ux-Pipewright-2026-05-27/DESIGN.md'
  - '_bmad-output/planning-artifacts/ux-designs/ux-Pipewright-2026-05-27/EXPERIENCE.md'
---

# Pipewright - Epic Breakdown

## Overview

本文档把 Pipewright 的 PRD 需求、UX 设计契约与架构决策,拆解为可实现的 epics 与 stories。Pipewright = 轻量、自托管、开源的 CI/CD + 部署编排平台(Go 单二进制双运行,≤100MB,AI 失败诊断为王牌),v1 全程 agentless(SSH),k8s 委托延后。

> **M1 最薄竖切(优先打通,再回填广度):** 跨 epic 取最窄一条端到端路径作为首个里程碑——`1.1 → 2.1 → 2.3(单分支映射) → 3.2 → 3.3(单镜像产物) → 4.1 → 4.2 → 4.4 → 5.1(单渠道)`。先让作者第 1 个真实服务"push→build→deploy→notify"跑通,再把各 epic 的其余 story 横向补齐。避免按能力域纵切导致迟迟没有一次真实成功部署。
>
> **横切验收(贯穿所有 story 的 DoD):** ① 每个 story 完成时 CI 的 ≤100MB 断言仍绿(高危 story 已就地加可测 AC);② 跨 story 复用的接口契约(产物契约 3.4 / `internal/target` SSH 层 4.1 / 源码读取端点 3.6 / run-detail DTO 3.1)在其定义 story 内冻结,后续 story 只消费不修改、变更须回到定义 story;③ AC-SEC-01~04 的验收做成自动化回归测试,而非人肉核对。

## Requirements Inventory

### Functional Requirements

**4.1 代码接入与触发**
- FR-1: Webhook 接入与事件触发 —— 每个项目独立 webhook 端点;事件由流水线配置声明(v1 支持 push/tag/PR-MR);亦支持 Web UI 手动触发(指定分支/commit)。
- FR-2: 分支 → 环境/服务器 映射 —— 维护「分支(支持通配)→ 环境 + 目标服务器集合」映射;一分支可映射多机,不同分支可映射不同环境;未映射分支无部署动作。
- FR-3: 仓库凭据配置 —— 可为项目配置拉取仓库凭据(deploy key / 访问令牌),安全存储,按引用使用;无效/缺失时以明确"凭据错误"失败,且日志/界面无明文。
- FR-4: 代码管理与 diff —— 在触发 commit 上克隆/更新仓库;提供**只读**代码浏览 + diff 视图(本次相对上次成功部署:commit 区间 + 文件级逐行);首次部署提示"无基线";无编辑/提交入口。

**4.2 流水线与隔离构建**
- FR-5: 多版本隔离构建 —— 每次构建在版本钉死的**构建容器**内执行,宿主机零污染、结束即销毁;支持模型 A(自带 Dockerfile,`docker build`)与模型 B(选定工具链版本,无需 Dockerfile)。
- FR-6: 多类型构建产物 —— 产物类型可配置:Docker 镜像 / JAR / dist(静态目录)等,各被部署阶段消费。
- FR-7: 外部镜像仓库集成 —— 平台不自带 registry;镜像推送到用户配置的外部镜像仓库(ACR / Docker Hub / Harbor),凭据按 FR-3 管理;非镜像产物不进 registry。
- FR-8: AI 生成流水线配置 —— 初始化时拉代码,AI 分析仓库(技术栈/版本/结构)推荐流水线配置;支持自然语言补充;结果以预览呈现,可一键全部接受或逐项部分接受。
- FR-9: 流水线配置合法性校验 —— 保存配置(手写或 AI 生成)时执行 schema/必填项/引用存在性校验;失败阻止保存并定位到具体错误项。

**4.3 多服务器部署**
- FR-10: 多产物部署执行(经 SSH)—— 按产物类型:Docker 镜像(目标机拉取 + `docker run`)、JAR(`java -jar` 或注册 systemd,可配)、dist(传输 + nginx 托管,平台可选管理 nginx 站点配置)。
- FR-11: 部署策略(零停机 + 回滚)—— v1 仅普通 Docker/SSH 目标:保留上一版本;"先起新版 → 健康检查通过 → 切换(端口约定)→ 确认后停旧版"实现零停机;失败回滚到上一版本。(k8s 委托延后出 v1)
- FR-12: 部署健康检查 —— 部署后按配置执行健康检查(HTTP 探针 / 端口存活),结果决定切换或回滚;可选、可配置;未配置则启动成功即视为部署成功。
- FR-13: 并行部署与独立结果(非事务性)—— 多流水线/多目标并行,结果独立:一台失败不阻断其余、失败机自行回滚;部分目标失败时整体标记「部分失败」并触发部署失败事件(携带失败目标清单);界面汇总每台状态。

**4.4 服务器与服务管理**
- FR-14: 目标服务器登记与分组 —— 登记目标服务器(host/port + SSH 凭据),校验连通性(SSH + Docker 可用);可按服务器分组(环境/项目)组织与筛选。
- FR-15: 多机状态总览 —— 统一面板查看服务器层(CPU/内存/磁盘)与服务/容器层(运行中服务、状态、端口)。
- FR-16: 日志查看(实时 + 历史)—— 查看服务日志,支持实时 tail 与历史按时间范围回看。
- FR-17: 服务操作 —— 经界面对目标机服务执行重启 / 停止 / 启动 / 重新部署。
- FR-18: 容器内命令执行(交互终端)—— 进入目标机容器执行命令的交互式终端(经 SSH → `docker exec`);安全敏感,受凭据/权限约束并可审计。

**4.5 构建与部署通知**
- FR-19: 多渠道通知 —— 支持企业微信、钉钉、飞书、邮件,以及自定义出站 webhook(向任意 HTTP 端点 POST 通知负载)。
- FR-20: 通知事件与细粒度路由 —— 可通知事件:构建成功/失败、部署成功/失败、回滚发生、健康检查失败;按项目细粒度配置「事件 → 渠道」;未配置路由的事件不发送。
- FR-21: 通知内容自定义 —— 模板 + 变量(项目名/分支/commit/状态/耗时/错误摘要等);未自定义用平台默认模板。

**4.6 AI 运行时运维**
- FR-22: AI 失败分析 —— 构建/部署失败时调用配置的 LLM(+ 精选错误知识库),产出根因假说(因果链)+ 可执行修复建议 + 置信度;"AI 认为"与"原始日志证据"分栏;随失败通知推送;LLM 不可用时降级为"不可用"提示,不阻断结果。
- FR-23: 可配置运行时异常检测 —— 经 SSH 轮询目标服务状态/指标;异常检测规则可按服务/框架配置;检测到异常触发通知告警;轮询间隔可配。
- FR-24: 可配置 AI 提供商 —— LLM 提供商与密钥由用户配置、不写死(Claude / OpenAI / 本地 Ollama);未配置 AI 时优雅禁用,核心 CI/CD 不受影响。
- FR-25: 成功/失败差异对比视图 —— 对失败运行,提供其与上一次成功运行的并排差异(变更的环境变量/配置/commit/文件);无成功基线时提示"无可对比基线"。
- FR-26: 诊断反馈闭环 —— 用户对 AI 根因假说标记"有用/无用"(可附正确根因);反馈持久化、关联该运行与诊断,用于改进后续诊断并积累平台专有诊断数据。

### NonFunctional Requirements

**平台形态与运行**
- NFR-1: 单一 Web UI —— 所有能力经一个干净、直观的 Web 界面操作(对 Jenkins 难用 UX 的直接回应)。
- NFR-2: 双运行模式 —— 平台既可作为 Docker 容器运行,也可本地编译为原生程序直接运行(不依赖 Docker)。
- NFR-3: 技术栈 Go —— 单静态二进制分发。

**性能 / 体积**
- NFR-4: 常驻内存 ≤100MB(对标 Woodpecker,远低于 Jenkins 500MB+);构建在隔离容器内执行、不计入平台常驻、不污染宿主机。须做成 CI 回归断言。

**安全(最关键横切)**
- NFR-5: 认证 —— Web UI 必须登录;v1 单管理员账号(无多用户/RBAC);界面不在网络上裸奔。
- NFR-6: 凭据 —— 仓库/SSH/镜像仓库/AI 密钥等一律加密存储,永不以明文出现在日志或界面。
- NFR-7: 审计日志 —— 敏感操作(部署、回滚、进容器执行命令、改凭据、改服务器、改流水线配置)记录"谁、何时、做了什么"。
- NFR-8: 远程命令面 —— FR-18 容器内执行与各类部署操作受认证约束并纳入审计。
- **AC-SEC-01 凭据存储**:SSH 私钥等凭据必须存入加密保险库,严禁明文落库/落环境变量/落日志。验收:日志目录 grep 私钥为空;DB dump 无 PEM 字段。
- **AC-SEC-02 命令执行**:`docker exec` / SSH 执行必须用 exec 数组(非 shell 字符串拼接),容器/主机经白名单校验。验收:传入 `; id` 返回 4xx 且不执行。
- **AC-SEC-03 审计不可篡改**:每次 SSH/exec 操作同步写入 append-only 外部 sink(远程 syslog / 对象存储);本地 root 删本地日志后,外部记录仍完整。
- **AC-SEC-04 secret 脱敏**:运行日志落盘前对已登记 secret masking(验收:job 中 echo secret,输出为 [MASKED]);架构扩展:**发往外部 LLM 前**亦须脱敏。

**可靠性 & AI**
- NFR-9: 可靠性 —— 部署失败可回滚(FR-11),健康检查门控切换(FR-12),多机部署结果独立、互不连累(FR-13)。
- NFR-10: AI 优雅降级 —— LLM 不可用时 AI 功能优雅降级,不阻断核心 CI/CD(FR-22/24)。

**反指标(不可为功能/速度牺牲)**
- NFR-11(SM-C1): 常驻内存不得为"加功能"突破 ≤100MB(轻量是头号卖点)。
- NFR-12(SM-C2): 不得为"求快/上线"牺牲凭据加密、登录认证、审计日志等基本防护。

### Additional Requirements

_来自架构文档(`architecture.md`)的技术与实现需求,影响 epic/story 拆分与排序。_

- **【起步 · Epic 1 Story 1】Scaffold 最小有意图栈**(不套重型脚手架):Go module + **Chi**(HTTP)+ **modernc.org/sqlite(纯 Go,无 CGO)** + 内嵌 **Vue 3 + Vite** 经 `go:embed`;布局按架构目录树(`cmd/pipewright`、`internal/<领域包>`、`web/`)。**CI 内置"常驻内存 ≤100MB"回归断言**(NFR-4 的硬门)。
- **数据架构**:SQLite via modernc(支持 `CGO_DISABLED` 全静态交叉编译 = 双运行前提);入库实体:项目 / 流水线配置(YAML 文本+解析)/ 运行历史 / 环境快照(支撑 FR-25)/ 诊断反馈(FR-26)/ 凭据(加密)/ 审计;内嵌 SQL 迁移,启动自动应用;仅 `internal/store` 触库,领域包经 repository 接口。
- **认证实现**:会话 cookie + **argon2id** 口令哈希;状态变更接口 **CSRF** 防护。
- **凭据保险库实现**:内嵌加密(NaCl secretbox / age),master key 来自环境变量/文件、**不入库**。
- **API & 通信**:REST(JSON,**字段 camelCase**)做 CRUD;统一错误结构 `{ error: { code, message } }`(永不含 secret/栈);状态变更接口速率限制;**SSE** 单向流(实时日志 tail / 运行状态 / 服务器指标);**WebSocket 仅**用于交互式容器终端(FR-18)。
- **前端架构**:Vue 3 + Vite + Pinia + Vue Router + **Naive UI**;Monaco 只读、懒加载(FR-4/25);构建静态资源 → `go:embed`。
- **Builder 抽象**:模型 A(Dockerfile)/ 模型 B(工具链镜像跑命令);执行位置支持**中控机本地**与**远程构建机(经 SSH 派发)**;运行时优先 **rootless BuildKit/Buildah/kaniko**,不强绑 Docker daemon。
- **Deployer 接口**:v1 仅 SSH 实现(`x/crypto/ssh`);零停机 = **端口约定 + 健康门控**(平台不接管反代;新版起新端口→健康通过→切换转发/符链→停旧版)。
- **任务调度**:**进程内 worker pool** + SQLite 持久化运行/任务状态(无外部队列);多流水线并行、非事务、结果独立(FR-13)。
- **领域事件**:`domain.action` 命名(`build.failed` / `deploy.succeeded` / `run.partial_failed`),驱动通知与 SSE。
- **AI 日志出网脱敏**:发往外部 LLM 前 secret masking(扩展 AC-SEC-04);敏感场景默认/推荐本地 Ollama;架构需呈现"日志 → 脱敏 → LLM"数据流。
- **一致性规范(多 AI agent 唯一事实源)**:DB snake_case / JSON camelCase / Go PascalCase 三层映射(经 struct tag);领域分包 `internal/`;`httpapi` 为唯一对外面(写接口过 auth+CSRF+审计中间件);抽象接口用 `-er`;提交前 gofmt+golangci-lint / eslint+prettier。
- **dogfooding 竖切顺序(实现序,影响 epic 排序)**:① scaffold+≤100MB 断言 → ② 认证+凭据保险库 → ③ 项目+流水线配置 CRUD+校验 → ④ Builder(中控本地)+Gitee webhook→隔离构建 → ⑤ Deployer(SSH)+端口切换/健康门控→部署 1 台 → ⑥ 通知(企业微信/钉钉)端到端打通 → ⑦ 纳管/监控(SSH 轮询)+日志 SSE+容器终端 WS → ⑧ AI 失败分析+diff+反馈闭环 → ⑨ 远程构建机·异常检测·其余渠道/平台横向铺开。

### UX Design Requirements

_来自 UX 契约(`DESIGN.md` + `EXPERIENCE.md`),与 FR 同等一级。每条须可生成带可测验收标准的 story。_

- UX-DR1: **双主题令牌系统** —— 深色为默认、浅色为对等主题(两套 OKLCH 令牌经 `data-theme` 切换);实现 DESIGN.md frontmatter 的 colors/typography(Inter + JetBrains Mono)/rounded/spacing 令牌;右下角常驻「明/暗」切换并记忆选择。
- UX-DR2: **全局壳与导航** —— 62px 左侧 rail(概览/运行/服务器/通知 + 底部设置),当前项电蓝竖条;主内容**铺满宽度**(~1680,与全宽顶栏/tab 左右对齐),过宽表单拆两栏;设置为"左二级导航 + 表单列"。
- UX-DR3: **组件与状态规范库**(`mock-states-components.html`)—— 状态徽标固定六词(成功/失败/进行中/部分失败/已回滚/排队)、按钮四级 + 交互态(hover/loading/disabled)、Toast 4 型(右下堆叠;成功/信息 4s 自动消失、错误手动关闭)、内联 Banner、**加载骨架(非转圈遮罩)**、空状态、错误态、二次确认(破坏性 + 高危 type-to-confirm)、表单控件各态(focus 光环/error)、Tooltip、进度/Spinner。
- UX-DR4: **AI 视觉语义** —— 青色为 AI 专属语义 + **活动脉冲线图标(禁用 ✦ 星芒)**;AI 诊断卡三分(根因假说"假说非结论"/ 证据日志命中行红底 / 置信度环填充 + 高中低)+ 👍/👎 反馈闭环 UI + 设置页诊断反馈统计。
- UX-DR5: **流水线可视化** —— 运行详情"穿珠"线性时间线(统一圆点节点 + ok/bad/skip 状态色环、失败脉冲发光、绿→红渐变段);编排页画布 + **n8n/Dify 风格贝塞尔曲线 + 流光连接器** + 右侧配置抽屉(就地编辑,不跳页)。
- UX-DR6: **终端/日志/代码视觉** —— 纯黑终端(`term`)+ 内阴影 + mac 三灯;日志级别着色/关键词高亮/SSE 标识/跟随尾部;**GitHub 红绿统一 diff**(双行号槽 + `@@` hunk 头 + 可折叠 + 轻量语法着色 + AI"与失败相关"青标);Monaco 只读懒加载。
- UX-DR7: **运行详情 5 态** —— 进行中(脉冲 + SSE 实时日志 + 步骤列表 + 取消)、成功(全绿 + 零停机切换 4 步流程图 + 健康门控核对)、失败+AI诊断、多机部分失败(扇出独立状态卡 + 仅重试失败目标)、全局运行列表(筛选 + 全宽分组表)。
- UX-DR8: **可访问性底线 WCAG 2.2 AA** —— 桌面 Web 全程键盘可达;**状态不单靠颜色**(色 + 圆点 + 文字);实时区 `aria-live`(运行状态/新日志/诊断完成,高频节流);`prefers-reduced-motion` 整体降级;焦点环可见(双主题达 AA);表单错误 `aria-describedby`;等宽字仅用于机器文本。
- UX-DR9: **微文案 / Voice & Tone** —— 直、准、用动词与数字;AI 永远"假说,非结论"语气;状态词汇固定不可换词;凭据一律掩码/handle 呈现;降级/空态/无基线文案明示后果且不制造焦虑。
- UX-DR10: **交互原语** —— 失败行/项目卡/服务行整体可点(失败行直达诊断);就地 drawer 编辑;二次确认分级(可逆直接 / 破坏性确认 / 高危 type-to-confirm);SSE 实时即推 + 跟随尾部;复制 ID/commit/webhook,凭据只复制引用 handle;`Esc` 关最顶层浮层;模态栈 ≤1 层。
- UX-DR11: **首次使用 / 空状态 onboarding** —— 价值三卡 + 3 步引导清单(连 AI → 加服务器 → 建项目,步骤锁依赖,可跳过);各情境空态/无基线/未配健康检查/凭据闲置等提示。
- UX-DR12: **通知配置 UI(继承 + 覆盖模型)** —— 全局渠道(共享连接)+ 全局默认事件路由矩阵 + 内容模板 + 「流水线覆盖」概览;流水线内"结果通知"任务 drawer 的「继承全局默认 ↔ 为本流水线自定义」整体切换 + 事件路由 chip 矩阵。

### FR Coverage Map

- FR-1 → Epic 2:Webhook 端点 + 手动触发入口(实际触发运行的执行在 Epic 3)
- FR-2 → Epic 2:分支(通配)→ 环境/目标服务器 映射表
- FR-3 → Epic 1:凭据加密保险库(后续 epic 按引用消费)
- FR-4 → Epic 7:只读代码浏览 + 代码 diff(后端读取端点在 Epic 3 预埋)
- FR-5 → Epic 3:多版本隔离构建(模型 A/B)
- FR-6 → Epic 3:多类型构建产物
- FR-7 → Epic 3:外部镜像仓库推送
- FR-8 → Epic 2:AI 生成流水线配置
- FR-9 → Epic 2:流水线配置合法性校验
- FR-10 → Epic 4:经 SSH 多产物部署执行
- FR-11 → Epic 4:零停机(端口切换)+ 回滚
- FR-12 → Epic 4:部署健康检查门控
- FR-13 → Epic 4:并行部署、结果独立、部分失败
- FR-14 → Epic 4:目标服务器登记与分组(做成共享 registry,Epic 6 复用)
- FR-15 → Epic 6:多机状态/指标总览
- FR-16 → Epic 6:日志查看(实时 + 历史)
- FR-17 → Epic 6:服务操作(重启/停止/启动/重新部署)
- FR-18 → Epic 6:容器内命令执行(交互终端)
- FR-19 → Epic 5:多渠道通知
- FR-20 → Epic 5:通知事件与细粒度路由(全局默认 + 流水线覆盖)
- FR-21 → Epic 5:通知内容自定义(模板 + 变量)
- FR-22 → Epic 7:AI 失败分析(假说 + 证据 + 置信度)
- FR-23 → Epic 6:可配置运行时异常检测
- FR-24 → Epic 7:可配置 AI 提供商
- FR-25 → Epic 7:成功/失败差异对比视图
- FR-26 → Epic 7:诊断反馈闭环

> NFR / AC-SEC 归属:NFR-1~8 + AC-SEC-01/03/04 主要落地于 Epic 1(地基),其中 **NFR-4 ≤100MB 为每个 epic 的横切 DoD**(非 Epic 1 一次性达标);AC-SEC-02(命令 array 化)落地于 Epic 4(SSH 部署)与 Epic 6(容器终端);AC-SEC-04 secret 脱敏在 Epic 1 建立、Epic 7 扩展(发往 LLM 前)。NFR-9 可靠性 → Epic 4;NFR-10 AI 降级 → Epic 7。UX-DR 按上文各 epic 注记分布。

## Epic List

### Epic 1: 可登录的安全实例(平台地基)
管理员能把单二进制跑起来、登录、把密钥安全存进加密保险库;实例稳定 ≤100MB,具备双主题外壳与组件规范。这是后续一切外部连接的前提。
**FRs covered:** FR-3
**实现注记:** scaffold 最小栈(Chi + modernc/sqlite 纯Go + Vue3+Vite + go:embed)+ **CI ≤100MB 回归断言**;单管理员认证(argon2id + 会话 cookie + CSRF);凭据保险库(NaCl/age,master key 不入库,掩码呈现 + 作用域 + 审计);双主题令牌系统 + 全局壳/导航 + 组件与状态规范库 + onboarding 引导骨架。覆盖 NFR-1~8、AC-SEC-01/03/04、UX-DR1/2/3/9/10/11。
**story 拆分指引(评审吸收):** 建议拆为 scaffold+≤100MB 断言 / auth+vault / **design-shell(收口边界写死:仅 app shell——导航/布局/主题/登录/账户;业务页面归各自 epic)** / login+account 联调。**横切 DoD:每个 epic 完成时 ≤100MB CI 仍绿。**

### Epic 2: 项目与流水线配置(含 AI 生成)
管理员能接入 Gitee 仓库、声明流水线配置(触发、分支→环境/服务器映射、构建/部署步骤),AI 可分析仓库生成配置,保存前校验。项目被"声明"但尚不能跑。
**FRs covered:** FR-1, FR-2, FR-8, FR-9
**实现注记:** 流水线配置 4 tab(编排画布+抽屉 / 变量与缓存 / 触发设置 / 环境与凭据);FR-8 AI 生成可独立 demo("不手写 YAML",预览 + 一键全接受/逐项部分接受);FR-9 服务端权威校验。项目列表页 + 新建项目向导。覆盖 UX-DR5、UX-DR12(配置入口)。

### Epic 3: 隔离构建与产物
push 或手动触发 → 在版本钉死的隔离容器内构建(模型 A/B)、产出多类型产物、推送外部镜像仓库;运行进入"进行中→成功/失败"并实时出日志。
**FRs covered:** FR-5, FR-6, FR-7
**实现注记:** Builder 抽象(模型 A Dockerfile / 模型 B 工具链)、rootless BuildKit/Buildah、worker pool、Gitee webhook 实际触发运行(承接 FR-1)。**产物契约(类型/寻址)在此定死,作为 E3 一等交付物(评审:防 E4 反向倒逼改接口)。** **运行详情页骨架 + 5 态状态机 + run-detail DTO 在此一次定死,后续 E4/E7 只往 slot 填组件、不动骨架(评审:杜绝跨 epic 考古)。** **源码/产物可读取的 store + httpapi 端点在此预埋,供 FR-4(Epic 7)前端复用。** 运行"进行中"态 + 纯黑终端 SSE 日志(UX-DR6/7)。

### Epic 4: 多服务器部署(零停机 + 回滚 + 多机)
登记目标服务器,把产物经 SSH 部署上线;端口切换 + 健康门控实现零停机,失败回滚;多机并行、结果独立、部分失败可见。完成 push→build→deploy 竖切。
**FRs covered:** FR-10, FR-11, FR-12, FR-13, FR-14
**实现注记:** **FR-14 服务器登记做成共享 server registry——`internal/target` 暴露通用 exec/session,别写死成"仅部署用",供 Epic 6 复用(评审)。** 内部线性链显式排序(E4.1 登记 → Deployer(SSH) → 端口切换 → 健康门控 → 回滚)。运行"成功 / 多机部分失败"态 + 全局运行列表(填 Epic 3 定的骨架 slot)。AC-SEC-02(命令 array 化)、NFR-9 可靠性。覆盖 UX-DR7。

### Epic 5: 构建与部署通知
关键事件(构建/部署 成功/失败、回滚、健康检查失败)经多渠道通知,按项目细粒度路由(全局默认 + 流水线覆盖,整体切换),内容可模板化。**至此 push→build→deploy→notify 端到端打通(dogfood M1)。**
**FRs covered:** FR-19, FR-20, FR-21
**实现注记:** Notifier 抽象 + 渠道适配器,订阅领域事件(`build.failed`/`deploy.succeeded`/`run.partial_failed`)。**评审建议:最小单渠道通知(FR-19 一条 webhook/邮件)可在 Epic 3/4 完成时先上,完整多渠道路由+模板在本 epic 收口(有没有 > 好不好)。** 覆盖 UX-DR12(继承+覆盖 UI)。

### Epic 6: 服务器与服务运维
在一个地方纳管多机:状态/指标、服务操作(重启/停止/启动/重新部署)、实时+历史日志、容器交互终端、可配置运行时异常检测与告警。
**FRs covered:** FR-15, FR-16, FR-17, FR-18, FR-23
**实现注记:** 复用 Epic 4 的 `internal/target` SSH 层;五件事弱耦合、可并行;**内部分批交付(先 状态/指标/日志 → 再 容器终端 → 再 异常检测)。** 容器终端 WS + AC-SEC-02 + 进入前审计提示;日志 SSE;异常检测(SSH 轮询,按服务/框架规则)留意与 Epic 7 AI 的接口。

### Epic 7: AI 失败诊断与代码洞察(王牌)
配置 AI 提供商后,失败运行给出根因假说 + 证据 + 置信度、成功/失败差异对比、代码浏览 + diff;👍/👎 反馈闭环积累私有诊断数据;LLM 不可用优雅降级、绝不阻断核心 CI/CD。
**FRs covered:** FR-4, FR-22, FR-24, FR-25, FR-26
**实现注记:** AI Provider 抽象(Claude/OpenAI/Ollama)、知识库、诊断卡(假说"非结论" + 证据日志命中行 + 置信度环)、成功/失败 diff(FR-25)、代码浏览+diff 前端 Monaco(后端读取端点已在 Epic 3 预埋)、反馈闭环(FR-26)、**日志出网脱敏(AC-SEC-04 扩展:发 LLM 前 masking)**、NFR-10 优雅降级。填 run-detail "失败诊断"态(UX-DR4/6)。**护城河注记(评审 Victor/John):反馈飞轮越早转越好——若 dogfood 节奏允许,可把 FR-22 + FR-26 的最小诊断切片随首条 build/deploy 失败路径提前接上(本结构保留 7-epic,该前移作为 story 排期裁量,不改 epic 边界)。**

> **Party Mode 评审吸收摘要(2026-05-28)**:保留 7-epic 结构;在 story 层吸收以下横切建议——① ≤100MB 设为每 epic 横切 DoD;② FR-4 后端读取端点在 Epic 3 预埋、Epic 7 只做前端;③ Epic 3 一次定死运行详情页 5 态骨架/DTO 与产物契约;④ FR-14 做成共享 server registry 供 Epic 6 复用;⑤ Epic 1 按 story 拆分且 design-shell 收口边界写死;⑥ AI 诊断+反馈最小切片可在 story 排期上尽量前移(护城河时间窗口)。

---

## Epic 1: 可登录的安全实例(平台地基)

管理员能把单二进制跑起来、登录、把密钥安全存进加密保险库;实例稳定 ≤100MB,具备双主题外壳与组件规范。这是后续一切外部连接的前提。**横切 DoD:本 epic 及之后每个 epic 完成时,CI 的 ≤100MB 断言仍绿。**

### Story 1.1: 项目脚手架与 ≤100MB 体积门

As a 平台维护者,
I want 一个可编译运行的最小 Go 单二进制骨架(Chi + modernc/sqlite + 内嵌 Vue3),且 CI 断言常驻内存 ≤100MB,
So that 后续一切功能都建立在一个轻量、可双运行、体积受控的地基上。

**Acceptance Criteria:**

**Given** 干净检出 **When** 执行 `make build` **Then** 产出单个静态二进制(CGO_DISABLED)、经 go:embed 内嵌 Vue3 构建产物 **And** 原生直跑与容器运行两种方式都能启动并响应健康检查端点
**Given** 二进制空载运行 **When** CI 测量常驻内存 **Then** ≤100MB,超标则流水线失败(NFR-4 / SM-C1)
**Given** modernc/sqlite(纯 Go) **When** 首次启动 **Then** 自动应用内嵌 SQL 迁移并建库,全程无需 CGO
**And** 目录结构符合架构 `cmd/` + `internal/` + `web/` 布局,三层命名(DB snake_case / JSON camelCase / Go PascalCase)就位

### Story 1.2: 单管理员认证与登录

As a 管理员,
I want 用户名口令登录(argon2id + 会话 cookie)且状态变更接口有 CSRF 防护,
So that Web UI 不在网络上裸奔,只有我能操作实例。

**Acceptance Criteria:**

**Given** 未登录 **When** 访问任意受保护页面/接口 **Then** 重定向到登录页 / 返回 401
**Given** 正确口令 **When** 登录 **Then** 建立 HttpOnly + SameSite 会话 cookie 并进入控制台 **And** 口令以 argon2id 哈希存储、DB 中无明文
**Given** 连续登录失败 5 次 **When** 再次尝试 **Then** 锁定 15 分钟(NFR-5)
**And** 对状态变更接口发起缺失 CSRF token 的请求返回 403;系统中无多用户/RBAC 概念

### Story 1.3: 凭据加密保险库(FR-3)

As a 管理员,
I want 把仓库令牌 / SSH 私钥 / 镜像仓库账号等密钥加密存入保险库、按引用使用、以掩码呈现,
So that 凭据永不以明文出现在日志或界面,后续 epic 可安全引用它们。

**Acceptance Criteria:**

**Given** 新增一条凭据 **When** 保存 **Then** 经 NaCl secretbox / age 加密入库,master key 来自环境变量/文件且不入库 **And** DB dump 中无明文 / 无 PEM 字段(AC-SEC-01)
**Given** 已存凭据 **When** 在界面查看 **Then** 仅显示掩码(如 `sk-••••a91f`)+ 类型 + 作用域 + 最近使用,永不回显明文
**And** 调用方经 handle/引用取用、不接触明文存储;复制按钮复制的是引用而非明文
**And** AC-SEC-01 验收做成**自动化回归测试**:日志目录 grep 私钥为空、DB dump 无 PEM/明文字段

### Story 1.4: 审计地基与 secret 脱敏(AC-SEC-03/04)

As a 管理员,
I want 敏感操作写入 append-only 审计、运行日志落盘前对已登记 secret 脱敏,
So that 谁在何时做了什么可追溯且不可篡改,且密钥不泄进日志。

**Acceptance Criteria:**

**Given** 一次敏感操作(改凭据/改服务器/部署/回滚/进容器等) **When** 发生 **Then** 同步写入本地 append-only 审计(专用不可改表)+ 可选远端 sink **And** 本地删除日志后远端记录仍完整(AC-SEC-03)
**Given** 日志中包含已登记 secret **When** 落盘 **Then** 输出为 [MASKED](AC-SEC-04);测试:job 中 echo secret → 日志为 [MASKED]
**And** 审计日志与应用日志分离存储
**And** AC-SEC-03/04 验收做成**自动化回归测试**:本地删日志后远端 sink 仍完整、job 中 echo secret → 日志为 [MASKED]

### Story 1.5: 应用外壳与双主题

As a 管理员,
I want 一个干净的应用外壳(左侧 rail 导航 + 主区 + 右下角明/暗主题切换),使用双主题 OKLCH 令牌,
So that 所有页面拥有一致、可切换、铺满宽度的视觉骨架。

**Acceptance Criteria:**

**Given** 登录后 **When** 进入控制台 **Then** 见 62px 左侧 rail(概览/运行/服务器/通知 + 底部设置),当前项电蓝竖条标识 **And** 主内容区铺满宽度(~1680 与全宽顶栏对齐)
**Given** 主题切换 **When** 点击右下角明/暗 **Then** 即时切换 data-theme 并记忆选择,深/浅两套令牌均生效(UX-DR1)
**And** colors / typography(Inter + JetBrains Mono)/ rounded / spacing 与 DESIGN.md 契约一致;**本 story 仅含 app shell(导航/布局/主题/路由占位),不含任何业务页面(收口边界)**

### Story 1.6: 组件与状态规范库

As a 前端开发者,
I want 一套可复用的基础组件与状态模式(状态徽标/按钮/Toast/Banner/骨架/空态/错误态/二次确认/表单控件/Tooltip/进度),
So that 后续所有业务页面复用统一、符合规范的组件与交互。

**Acceptance Criteria:**

**Given** 组件库 **When** 检视 **Then** 含 DESIGN.md / states-components 定义的全部组件,颜色一律走令牌,动效仅用 transform/opacity 且遵守 `prefers-reduced-motion`(UX-DR3)
**Given** 状态徽标 **When** 使用 **Then** 固定六词(成功/失败/进行中/部分失败/已回滚/排队),进行中态脉冲,状态不单靠颜色(色 + 圆点 + 文字)
**Given** Toast 与二次确认 **When** 触发 **Then** Toast 右下角堆叠(成功/信息 4s 自动消失、错误手动关闭);高危操作走 type-to-confirm(输入名称匹配前禁用确认)
**And** 焦点环可见、Esc 关闭最顶层浮层、表单错误经 aria-describedby 关联(UX-DR8 基线)

### Story 1.7: 账户设置与首次使用引导

As a 管理员,
I want 账户设置(改口令/会话管理)与首次使用引导(连 AI / 加服务器 / 建项目 3 步),
So that 我能管理自己的账户,且新实例有清晰的从零上手路径。

**Acceptance Criteria:**

**Given** 账户设置 **When** 修改口令 **Then** 校验当前口令、更新为新 argon2id 哈希、使其它会话失效 **And** 可查看并撤销活跃会话
**Given** 全新实例(无项目/服务器/AI) **When** 登录后 **Then** 进入引导:价值三卡 + 3 步清单(连 AI ✓ → 加服务器 → 建项目),步骤间锁依赖,可跳过且随时可在设置重开(UX-DR11)
**And** 已有配置时不强制进引导,落到概览(空态带 CTA)

## Epic 2: 项目与流水线配置(含 AI 生成)

管理员能接入 Gitee 仓库、声明流水线配置(触发、分支→环境/服务器映射、构建/部署步骤),AI 可分析仓库生成配置,保存前校验。项目被"声明"但尚不能跑(执行在 Epic 3)。

### Story 2.1: 项目接入与项目列表

As a 管理员,
I want 创建项目、接入一个 Gitee 仓库(引用保险库中的仓库凭据),并在项目列表查看全部项目,
So that 我有一个被纳管的代码仓库作为后续配置与运行的载体。

**Acceptance Criteria:**

**Given** 新建项目 **When** 填名称 + Gitee 仓库地址 + 选择仓库凭据(引用 Epic 1 保险库) **Then** 项目创建成功,凭据按引用绑定、界面不显明文
**Given** 凭据有效 **When** 平台试克隆仓库 **Then** 私有仓库克隆成功;凭据无效/缺失 → 以明确"凭据错误"失败且不泄明文(FR-3)
**Given** 已有多个项目 **When** 打开项目列表 **Then** 卡片网格显示每个项目的名称/仓库分支/上次运行状态/目标服务器/快捷操作,可搜索/筛选
**And** 项目可重命名/删除(删除走二次确认)

### Story 2.2: 流水线配置编辑器与编排画布

As a 管理员,
I want 一个 4 标签的流水线配置编辑器,其中"流水线编排"以画布 + 右侧抽屉就地编辑阶段/任务,
So that 我能可视化地组织构建/部署步骤而不必手写整份 YAML。

**Acceptance Criteria:**

**Given** 进入项目配置 **When** 查看 **Then** 见 4 个 tab:流水线编排 / 变量与缓存 / 触发设置 / 环境与凭据
**Given** 流水线编排 tab **When** 选中一个任务卡 **Then** 右侧抽屉就地展开其配置(不弹模态、不跳页),阶段间以曲线 + 流光连接(UX-DR5)
**And** 配置以声明式持久化(YAML 文本 + 解析入库),编辑可保存为草稿

### Story 2.3: 触发设置与分支映射(FR-1 / FR-2)

As a 管理员,
I want 为项目配置 webhook 接收端点与签名密钥、勾选触发事件、并维护"分支(通配)→ 环境 + 目标服务器"映射,
So that 指定分支的 push/tag/PR 能触发对的环境与服务器(执行在 Epic 3)。

**Acceptance Criteria:**

**Given** 触发设置 tab **When** 查看 **Then** 显示该项目独立的 webhook 端点 URL + 签名密钥(掩码/复制/重置),事件可勾选 push/tag/PR-MR,手动触发常开(FR-1)
**Given** 分支映射表 **When** 配置 `main`→生产(机A/机B)、`dev`→测试(机C) **Then** 映射持久化,支持通配(如 `release/*`),一分支可映射多机(FR-2)
**And** 未匹配任何映射的分支可选"记录但不部署"或"忽略";引用不存在的服务器在保存时被校验拦截(联动 FR-9)

### Story 2.4: 构建/部署配置(变量·缓存·环境·凭据·镜像仓库)

As a 管理员,
I want 在"变量与缓存""环境与凭据"两个 tab 声明构建模型(A/B)、产物类型、构建变量与缓存、环境定义、引用的凭据与镜像仓库,
So that 流水线拥有完整、可校验的构建与部署声明(实际执行在 Epic 3/4)。

**Acceptance Criteria:**

**Given** 变量与缓存 tab **When** 配置 **Then** 可选构建模型 A(自带 Dockerfile)/ B(工具链版本)、产物类型(镜像/JAR/dist)、构建变量(明文 + 引用保险库的 secret 掩码)、依赖缓存
**Given** 环境与凭据 tab **When** 配置 **Then** 可定义环境(各自目标服务器 + 环境变量)、引用保险库凭据(仓库/部署 SSH/镜像仓库)、绑定外部镜像仓库(FR-7 声明侧)
**And** 所有 secret 均以掩码/引用呈现,绝不明文

### Story 2.5: AI 生成流水线配置(FR-8)

As a 管理员,
I want 初始化项目时让 AI 分析仓库并推荐一份流水线配置,可用自然语言补充、以预览呈现、一键全部接受或逐项部分接受,
So that 我不必手写配置即可快速得到一份合理的流水线。

**Acceptance Criteria:**

**Given** 对一个 Node 22 仓库初始化 **When** AI 分析仓库 **Then** 推荐含 `node:22` 构建的配置,以预览呈现(技术栈/版本/结构判断可见)
**Given** 预览 **When** 用户补充"push main 部署到生产" **Then** 预览实时反映该分支→环境映射
**And** 用户可一键全部接受,或逐项勾选部分接受 → 仅被勾选项写入流水线配置
**And** 未配置 AI 提供商时,该向导优雅提示"AI 不可用,可手动配置",不阻断手动建项目

### Story 2.6: 流水线配置合法性校验(FR-9)

As a 管理员,
I want 保存配置(手写或 AI 生成)时执行服务端权威校验,
So that 非法配置无法保存,且错误能定位到具体项。

**Acceptance Criteria:**

**Given** 配置缺少必填项或引用了不存在的凭据/服务器 **When** 保存 **Then** 阻止保存并定位到具体错误项(服务端权威校验)
**Given** 配置合法 **When** 保存 **Then** 配置入库并可用于触发流水线运行
**And** 前端镜像校验仅为体验,最终以服务端校验为准

## Epic 3: 隔离构建与产物

push 或手动触发 → 在版本钉死的隔离容器内构建(模型 A/B)、产出多类型产物、推送外部镜像仓库;运行进入"进行中→成功/失败"并实时出日志。**本 epic 一次定死:运行详情页 5 态骨架 + run-detail DTO + 产物契约 + 源码/产物读取端点(供后续 epic 只填不重构)。**

### Story 3.1: 运行模型与运行详情页骨架

As a 平台开发者,
I want 一次性定义流水线运行的数据模型、进程内 worker pool、运行详情页 5 态骨架与 run-detail DTO,
So that Epic 4/7 只需往既定 slot 填组件、不必重构运行详情页(杜绝跨 epic 考古)。

**Acceptance Criteria:**

**Given** 一次触发 **When** 创建流水线运行 **Then** 运行记录入库(状态机:排队→进行中→成功/失败/部分失败/已回滚),由进程内 worker pool 调度
**Given** 运行详情页 **When** 实现 **Then** 提供 5 态 slot(进行中/成功/失败诊断/多机部分失败/列表)与**可扩展**的 run-detail DTO——前向声明 `targets[]`(多机,Epic 4 填)与 `diagnosis`(诊断,Epic 7 填)为可选块,后续 epic **只填不扩**
**And** 多流水线并行、非事务、结果独立(FR-13 的调度地基);全局运行列表骨架就位
**And** 确立"骨架所有权"规则:仅本 story 改运行详情骨架与 DTO 形状,Epic 4/7 只往既定 slot 填组件(防多 agent 并发改同文件回归)

### Story 3.2: Webhook 触发与手动触发创建运行(FR-1 执行)

As a 管理员,
I want Gitee 的 push/tag/PR 投递到项目 webhook 端点后、或我在 UI 手动触发后,真正创建一次流水线运行,
So that "push 后自动更新"的链路从触发端被打通。

**Acceptance Criteria:**

**Given** 配置了 push 触发 **When** 投递一个匹配分支的 push 事件 **Then** 数秒内创建一次流水线运行,并按分支映射解析目标环境/服务器
**Given** 未配置/不匹配的事件 **When** 投递 **Then** 不创建运行并记录被忽略的原因
**And** UI 手动触发(指定分支/commit)创建一次等效运行;签名密钥校验失败的 webhook 被拒
**And** webhook 投递**幂等**:同一 delivery id / 同一 commit 的重复投递不创建重复运行(去重),网络重放被安全忽略并记录

### Story 3.3: 多版本隔离构建(FR-5)

As a 管理员,
I want 每次构建在版本钉死的隔离容器内执行、宿主机零污染、结束即销毁,支持模型 A(Dockerfile)与模型 B(工具链),
So that 不同项目的工具链互不冲突也不污染宿主机。

**Acceptance Criteria:**

**Given** 项目 X 配 `node:14`、项目 Y 配 `node:22` **When** 分别构建 **Then** 均成功且互不影响,宿主机未安装任何 node
**Given** 构建结束 **When** 检视 **Then** 构建容器被销毁,宿主机不留存工具链
**And** 模型 A(`docker build`)与模型 B(工具链镜像内跑命令)均可产出可部署产物;构建器优先 rootless(BuildKit/Buildah),不强绑 Docker daemon
**And** 构建进行中(容器构建为内存大户)平台常驻 CI ≤100MB 断言仍绿(NFR-4)

### Story 3.4: 多类型构建产物与产物契约(FR-6)

As a 平台开发者,
I want 构建产物类型可配置(Docker 镜像 / JAR / dist),并把"产物类型 + 寻址方式"定为 build↔deploy 之间的稳定契约,
So that Epic 4 部署可消费产物而不会反向倒逼 Epic 3 改接口。

**Acceptance Criteria:**

**Given** 配置产出 Docker 镜像 / JAR / dist **When** 构建 **Then** 分别产出镜像 / jar 文件 / 静态目录
**Given** 产物 **When** 被下游引用 **Then** 通过既定产物契约(类型 + 寻址)定位,契约文档化为本 epic 一等交付物
**And** 产物类型与后续部署方式一一对应(镜像→run、JAR→运行、dist→静态服务)
**And** **接口冻结**:产物契约(类型 + 寻址)以类型定义/文档固化于本 story,Epic 4 只消费不修改;需变更须回到本 story

### Story 3.5: 推送外部镜像仓库(FR-7)

As a 管理员,
I want 镜像产物推送到我配置的外部镜像仓库(ACR/Docker Hub/Harbor),凭据按引用管理,
So that 平台不必自带 registry 即可分发镜像。

**Acceptance Criteria:**

**Given** 配置 Harbor + 有效凭据 **When** 构建镜像 **Then** 成功推送到该 Harbor,tag 按策略生成
**Given** 凭据错误 / 仓库不可达 **When** 推送 **Then** 以明确错误失败,日志不泄露凭据明文
**And** 非镜像产物(JAR/dist)不进 registry

### Story 3.6: 运行进行中态、实时日志与源码读取端点

As a 管理员,
I want 运行进行中时看到流水线节点脉冲与 SSE 实时构建日志,并由平台预埋"源码/产物可读取"的后端端点,
So that 我能实时跟踪构建,且 Epic 7 的代码浏览只需做前端。

**Acceptance Criteria:**

**Given** 运行进行中 **When** 查看运行详情 **Then** 流水线当前节点脉冲、纯黑终端经 SSE 实时滚动日志、闪烁光标、可取消(UX-DR6/7)
**Given** 一次运行 **When** 完成 **Then** 状态切到成功/失败,日志可历史回看
**And** 平台提供"按 commit/分支读取源码树与文件内容"的 store + httpapi 端点(为 FR-4 预埋),日志落盘前 secret 脱敏(AC-SEC-04)
**And** **接口冻结**:该读取端点契约固化于本 story,Epic 7(FR-4 前端)只消费不改;需变更须回到本 story

## Epic 4: 多服务器部署(零停机 + 回滚 + 多机)

登记目标服务器,把产物经 SSH 部署上线;端口切换 + 健康门控实现零停机,失败回滚;多机并行、结果独立、部分失败可见。完成 push→build→deploy 竖切。

### Story 4.1: 目标服务器登记、分组与共享 SSH 层(FR-14)

As a 管理员,
I want 登记目标服务器(host/port + 选 SSH 凭据)、校验连通性、按分组组织,并把 SSH 连接做成通用层,
So that 部署有可用目标,且 Epic 6 运维可复用同一套 SSH/服务器清单。

**Acceptance Criteria:**

**Given** 添加服务器并填有效 SSH 凭据(引用保险库) **When** 校验 **Then** SSH + Docker 可用则标记可用;凭据/网络错误则标记不可用并提示原因(FR-14)
**Given** 多台服务器 **When** 组织 **Then** 可归入服务器分组(环境/项目)并按组筛选
**And** `internal/target` 暴露通用 exec/session(非"仅部署用"),供 Epic 6 状态/日志/终端复用;命令以 exec 数组执行、目标白名单(AC-SEC-02)
**And** **接口冻结**:`internal/target` 的 exec/session 抽象固化于本 story,Epic 6 只消费不改;AC-SEC-02 验收做成**自动化回归测试**(传入 `; id` 返回 4xx 且不执行)

### Story 4.2: 经 SSH 多产物部署执行(FR-10)

As a 管理员,
I want 平台经 SSH 把构建产物部署到目标服务器并按产物类型启动,
So that push 后服务能真正在对的服务器上更新。

**Acceptance Criteria:**

**Given** 镜像产物 **When** 部署 **Then** 目标机从镜像仓库拉取并 `docker run`,服务可访问
**Given** JAR 产物 **When** 部署 **Then** 按所选方式(`java -jar` 直接运行 / 注册 systemd)在目标机运行
**Given** dist 产物 **When** 部署 **Then** 传输到配置目录由 nginx 托管;启用 nginx 管理时平台写入/更新对应站点配置

### Story 4.3: 部署健康检查门控(FR-12)

As a 管理员,
I want 部署后按配置执行健康检查(HTTP 探针 / 端口存活),并以结果决定后续切换或回滚,
So that 不健康的新版本不会被放到线上。

**Acceptance Criteria:**

**Given** 配置了健康检查 **When** 新版本不健康 **Then** 不切换并触发回滚
**Given** 未配置健康检查 **When** 容器/进程启动成功 **Then** 视为部署成功
**And** 健康检查可选、可配置(探针类型/次数/超时)

### Story 4.4: 零停机端口切换与失败回滚(FR-11)

As a 管理员,
I want 普通 Docker/SSH 目标采用"先起新版→健康通过→端口切换→确认后停旧版"实现零停机,失败回滚到上一版本,
So that 部署对用户无感知,出错也能回到安全态。

**Acceptance Criteria:**

**Given** 普通目标 **When** 新版本通过健康检查 **Then** 切换流量(端口约定)、旧实例优雅下线,全程零停机(平台不接管反代)
**Given** 新版本健康检查失败 **When** 部署 **Then** 保留/恢复旧版本对外可用(回滚)
**And** 默认保留上一版本 1 份用于回滚;k8s 委托不在 v1 范围

### Story 4.5: 多机并行部署、独立结果与部分失败态(FR-13)

As a 管理员,
I want 一次构建并行下发到多台目标、各机结果独立、部分失败时整体可见且失败机自行回滚,
So that 一台出问题不连累其余,且我能一眼看清每台状态。

**Acceptance Criteria:**

**Given** 1 个流水线推 3 台、1 台失败 **When** 部署 **Then** 其余 2 台照常完成、失败机回滚,界面汇总 3 台各自状态(填 Epic 3 的"成功/多机部分失败"slot)
**Given** 部分目标失败 **When** 落定 **Then** 运行整体状态标记「部分失败」并触发部署失败事件(携带失败目标清单,供 Epic 5)
**And** 全局运行列表展示进行中/成功/失败/部分失败/已回滚多态、可筛选
**And** 边界:**全部目标失败** → 整体标记「失败」(非「部分失败」);全部成功 → 「成功」;切换时保证 0 个进行中请求被中断(优雅断流)

### Story 4.6: 最小失败诊断与反馈种子(护城河飞轮前移)

As a 作者(dogfooding 第一个服务),
I want 在第一条 build/deploy 失败路径上,就能看到一段最小的可解释失败输出 + 一个"这次诊断准不准"的反馈按钮,
So that 私有诊断反馈飞轮(护城河时间窗口)从产品最早期就开始转,而非等到 Epic 7。

**Acceptance Criteria:**

**Given** 一次 build 或 deploy 失败、且已通过**本 story 自带的最小内联 AI 提供商配置**(单提供商 + key,仅引用 Epic 1 保险库;无前向依赖,Epic 7 的 7.1 后续将其扩展为完整多提供商管理) **When** 失败发生 **Then** 在运行详情"失败诊断"slot 给出最小根因假说 + 关键证据行(初版可粗糙),语气为"最可能的根因(假说,非结论)";未配置则该 slot 留空、不阻断
**Given** 该诊断 **When** 展示 **Then** 附 👍/👎 反馈按钮,点击即持久化、关联该运行(FR-26 种子,设置页统计可后续补全)
**And** LLM 不可用时降级为"诊断不可用",不阻断失败结果落库与通知(NFR-10)
**And** 本 story 是"最薄切片":成熟诊断(分栏证据/置信度环/成功失败 diff/知识库)仍由 Epic 7(7.2/7.3/7.5)交付,本 story 不重复实现、只先让飞轮转起来
**And** 排期上紧随 M1 竖切(建议 4.4 之后),作为 dogfooding 的早期正反馈

> 说明:本 story 为 Party Mode + Pre-mortem 评审吸收项——把 FR-22/FR-26 最小切片从 Epic 7 前移以启动护城河计时器;不改 7-epic 边界,Epic 7 仍是 AI 能力的完整归属。

## Epic 5: 构建与部署通知

关键事件(构建/部署 成功/失败、回滚、健康检查失败)经多渠道通知,按项目细粒度路由(全局默认 + 流水线覆盖,整体切换),内容可模板化。至此 push→build→deploy→notify 端到端打通(dogfood M1)。

### Story 5.1: 多渠道连接(全局共享)(FR-19)

As a 管理员,
I want 连接企业微信/钉钉/飞书/邮件以及自定义出站 webhook 作为全局共享渠道,并能发送测试,
So that 所有流水线共用同一套已验证的通知连接。

**Acceptance Criteria:**

**Given** 配置钉钉机器人 / 自定义 webhook URL **When** 保存并发送测试 **Then** 对应渠道收到测试消息;签名密钥(如有)用于校验
**Given** 渠道连接 **When** 检视 **Then** 为全局共享(只配一次),流水线不单独连渠道
**And** 渠道密钥经保险库引用、掩码呈现

### Story 5.2: 事件路由与全局默认矩阵(FR-20 全局)

As a 管理员,
I want 配置"哪些事件 → 发到哪些渠道"的全局默认路由,订阅领域事件,
So that 新流水线默认继承一套合理的通知规则。

**Acceptance Criteria:**

**Given** 可路由事件(构建成功/失败、部署成功/失败、回滚、健康检查失败) **When** 在矩阵勾选"失败→钉钉+邮件、成功→仅钉钉" **Then** 失败事件发钉钉与邮件、成功只发钉钉
**Given** 某事件未配置任何渠道 **When** 触发 **Then** 不发送通知
**And** 路由订阅领域事件(`build.failed`/`deploy.succeeded`/`run.partial_failed` 等)
**And** 边界:渠道投递失败(网络/限流/4xx)时重试 N 次并记录失败,不静默丢弃;投递结果可在审计/日志查见

### Story 5.3: 通知内容模板与变量(FR-21)

As a 管理员,
I want 自定义通知内容模板(支持变量),
So that 通知里包含我关心的项目/分支/commit/状态/耗时/AI 根因等字段。

**Acceptance Criteria:**

**Given** 自定义模板引用变量(项目/分支/commit/状态/环境/耗时/ai_root_cause/run_url) **When** 触发通知 **Then** 渲染为对应字段值
**Given** 未自定义模板 **When** 触发 **Then** 使用平台默认模板
**And** 模板标注为"全局默认",可被流水线覆盖

### Story 5.4: 流水线通知覆盖(继承/自定义整体切换)(FR-20 覆盖)

As a 管理员,
I want 在某条流水线的"结果通知"任务里,从"继承全局默认"切换到"为本流水线自定义"路由,
So that 关键流水线能有自己的通知策略(如成功降噪)而不影响其它流水线。

**Acceptance Criteria:**

**Given** 流水线"结果通知"drawer **When** 范围切到"为本流水线自定义" **Then** 整套替换本流水线事件路由(渠道仍取自全局已连接的),非逐事件 merge(UX-DR12)
**Given** 通知页"流水线覆盖"概览 **When** 查看 **Then** 如实标注每条流水线"沿用全局默认"或"自定义路由",并可跳转配置
**And** 切回"继承全局默认"即整体恢复,不残留半套自定义

## Epic 6: 服务器与服务运维

在一个地方纳管多机:状态/指标、服务操作、实时+历史日志、容器交互终端、可配置运行时异常检测与告警。复用 Epic 4 的 `internal/target` SSH 层。

### Story 6.1: 多机状态与指标总览(FR-15)

As a 管理员,
I want 在统一面板查看所管服务器的 CPU/内存/磁盘与其上运行的服务/容器(状态、端口),
So that 不必逐台 SSH 即可掌握全局。

**Acceptance Criteria:**

**Given** 面板 **When** 打开 **Then** 列出每台服务器的 CPU/内存/磁盘使用(经 SSH 轮询)
**Given** 某服务器 **When** 展开 **Then** 列出其上运行的服务/容器、状态与端口
**And** 复用 Epic 4 的 `internal/target` SSH 层,不新建专用连接

### Story 6.2: 服务日志查看(实时 + 历史)(FR-16)

As a 管理员,
I want 查看某服务的日志,支持实时 tail 与按时间范围历史回看,
So that 我能在一处排查服务问题。

**Acceptance Criteria:**

**Given** 选定一个服务 **When** 实时模式 **Then** 经 SSE 实时 tail 日志、级别着色、关键词高亮、跟随尾部可开关
**Given** 历史模式 **When** 选时间范围 **Then** 回看该范围历史日志
**And** 日志中已登记 secret 脱敏呈现
**And** 持续高吞吐日志流式(SSE)下平台常驻 CI ≤100MB 断言仍绿(NFR-4)

### Story 6.3: 服务操作(FR-17)

As a 管理员,
I want 经界面对目标机服务执行重启/停止/启动/重新部署,
So that 我不必逐台 SSH 即可运维服务。

**Acceptance Criteria:**

**Given** 某服务 **When** 重启 **Then** 该服务在目标机重启并反馈结果
**Given** 触发重新部署 **When** 执行 **Then** 等效于重跑该服务的部署阶段(Epic 4)
**And** 停止等破坏性操作走二次确认;操作纳入审计

### Story 6.4: 容器交互终端(FR-18)

As a 管理员,
I want 进入目标机容器执行命令的交互式终端(经 SSH → docker exec),
So that 我能在容器内排查问题。

**Acceptance Criteria:**

**Given** 运行中容器 **When** 打开终端 **Then** 经 WebSocket 建立交互终端,可执行命令并看到输出
**Given** 进入终端前 **When** 悬停入口 **Then** tooltip 明示"经 SSH → docker exec,操作纳入审计"
**And** 命令以 exec 数组执行、目标经白名单校验(AC-SEC-02);传入 `; id` 返回 4xx 且不执行(自动化回归);全程审计
**And** 多并发终端会话(WS)下平台常驻 CI ≤100MB 断言仍绿(NFR-4)

### Story 6.5: 可配置运行时异常检测与告警(FR-23)

As a 管理员,
I want 按服务/框架配置异常检测规则(经 SSH 轮询取数),命中即告警,
So that 服务异常时我能第一时间被通知。

**Acceptance Criteria:**

**Given** 为某服务配置"重启循环"规则 **When** 该服务反复重启 **Then** 产生告警并经配置渠道通知(联动 Epic 5)
**Given** 检测规则 **When** 配置 **Then** 可按服务/框架定制(重启循环/内存/5xx/健康/磁盘等),轮询间隔可配
**And** 告警 feed 展示严重/警告/已恢复,失败项提供"AI 排查"入口(联动 Epic 7);数据源为 SSH 轮询(实时流待 agent,延后)

## Epic 7: AI 失败诊断与代码洞察(王牌)

配置 AI 提供商后,失败运行给出根因假说 + 证据 + 置信度、成功/失败差异对比、代码浏览 + diff;👍/👎 反馈闭环积累私有诊断数据;LLM 不可用优雅降级、绝不阻断核心 CI/CD。**护城河注记:反馈飞轮越早转越好——若 dogfood 节奏允许,可把 7.1+7.2+7.5 的最小切片随首条 build/deploy 失败路径提前接上。**

### Story 7.1: 可配置 AI 提供商(FR-24)

As a 管理员,
I want 配置 LLM 提供商与密钥(Claude/OpenAI/本地 Ollama)、测试连接、选模型、设预算,
So that AI 功能走我自己的 LLM,且密钥不写死在代码。

**Acceptance Criteria:**

**Given** 配置 Claude key(存入保险库) **When** 测试连接 **Then** 连接正常并回显延迟;AI 功能走 Claude
**Given** 切换到本地 Ollama **When** 保存 **Then** AI 功能走本地模型
**And** 未配置 AI 时 AI 功能优雅禁用,核心 CI/CD 不受影响(NFR-10);密钥掩码呈现

### Story 7.2: AI 失败分析(假说 + 证据 + 置信度)(FR-22)

As a 管理员,
I want 构建/部署失败时,平台基于失败日志(+ 知识库)给出根因假说、可执行修复建议与置信度,与原始日志证据分栏呈现,并随失败通知推送,
So that 我不必翻一堆日志即可定位失败原因。

**Acceptance Criteria:**

**Given** 一次依赖缺失导致的构建失败 **When** 查看运行详情 **Then** 给出指向该依赖的根因假说 + 修复建议 + 置信度,并附可展开的日志证据(命中行高亮);失败通知包含该摘要
**Given** 诊断 **When** 呈现 **Then** 语言为"最可能的根因是 X(假说,非结论)",低置信时明示存在其它可能根因;"AI 认为"与"原始日志证据"分栏
**Given** LLM 不可用 **When** 失败发生 **Then** 流水线结果照常记录,诊断降级为"不可用"提示、不阻断结果(NFR-10)
**And** 发往 LLM 的日志在出网前 secret 脱敏(AC-SEC-04 扩展);填 run-detail"失败诊断"slot
**And** 边界:LLM 超时 / 返回不可解析 / 明显低质 → 一律归为"诊断不可用",不当作有效诊断展示;AI 推理期平台常驻 CI ≤100MB 断言仍绿(NFR-4)

### Story 7.3: 成功/失败差异对比视图(FR-25)

As a 管理员,
I want 对一次失败运行,并排查看它与上一次成功运行的差异(环境变量/配置/commit/文件),
So that 我能快速定位"这次和上次差在哪"。

**Acceptance Criteria:**

**Given** 失败运行存在上一次成功基线 **When** 打开差异视图 **Then** 并排列出变更的环境变量/配置/commit/文件,疑似根因项打标
**Given** 无成功基线 **When** 打开 **Then** 提示"无可对比基线",不渲染空表
**And** 差异数据来自入库的运行历史 + 环境快照

### Story 7.4: 代码浏览与 diff(FR-4)

As a 管理员,
I want 在运行/诊断上下文中只读浏览代码、并查看本次相对上次成功部署的 diff(commit 区间 + 文件级逐行),
So that 我能直接看清这次部署改了什么(无 AI 也可用)。

**Acceptance Criteria:**

**Given** 连续两次部署 **When** 打开 diff 视图 **Then** 列出两次间的变更 commit,并可下钻到文件级逐行差异(Monaco 只读,GitHub 红绿)
**Given** 首次部署 **When** 打开 diff **Then** 提示"无基线"
**And** 代码浏览为只读、无编辑/提交入口;前端复用 Epic 3 预埋的源码读取端点,不重做后端
**And** Monaco 懒加载(不进常驻基线);加载后平台常驻 CI ≤100MB 断言仍绿(NFR-4 / D1 体积约束)

### Story 7.5: 诊断反馈闭环(FR-26)

As a 管理员,
I want 对 AI 根因假说标记"有用/无用"(可附正确根因),反馈被记录并改进后续诊断,
So that 诊断越用越准,并积累平台专有的诊断数据(护城河)。

**Acceptance Criteria:**

**Given** 一条根因假说 **When** 点击 👍/👎(可附正确根因) **Then** 反馈持久化、关联该运行与诊断
**Given** 反馈数据 **When** 累积 **Then** 设置页"诊断反馈闭环统计"展示准确率/反馈数/环比,并入知识库供后续检索
**And** 否定反馈纠正的案例可被后续同类失败命中
