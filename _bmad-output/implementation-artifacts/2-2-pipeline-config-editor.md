# Story 2.2: 流水线配置编辑器与编排画布

status: ready-for-dev
epic: 2
prev: 2-1(项目接入)/ 2-3(触发设置)已 done
covers: UX-DR5(画布+抽屉就地编辑、曲线+流光连线)· 部分 FR-9(保存时结构校验,完整引用校验=2-6)

## As a / I want / So that
As a 管理员,I want 一个 4 标签的流水线配置编辑器,其中"流水线编排"以画布 + 右侧抽屉就地编辑阶段/任务,So that 我能可视化地组织构建/部署步骤而不必手写整份 YAML。

## Acceptance Criteria
1. **Given** 进入项目配置 **When** 查看 **Then** 见 4 个 tab:流水线编排 / 变量与缓存 / 触发设置 / 环境与凭据。
2. **Given** 流水线编排 tab **When** 选中一个任务卡 **Then** 右侧抽屉就地展开其配置(**不弹模态、不跳页**),阶段间以**曲线 + 流光**连接(UX-DR5)。
3. **And** 配置以声明式持久化(结构化 spec + 渲染 YAML 文本,解析入库),编辑可**保存为草稿**;保存时做结构校验(阶段/任务名非空、kind 枚举、id 不重复),失败 422 定位到具体项(完整引用存在性校验留 2-6)。
4. **And** 触发设置 tab 复用 2-3 已实现的触发配置(并入为 tab,原 `/projects/:id/triggers` 路由保留向后兼容);变量与缓存 / 环境与凭据 tab 为**占位**(明确标注「2-4 落地」),不在本期实现其表单。

## 范围边界(本期做 / 不做)
- ✅ 做:4-tab 编辑器外壳;流水线编排画布(阶段列 + 任务卡 + 曲线流光连线 + 添加阶段/任务 + 删除)+ 右侧抽屉就地编辑选中任务(名称/类型/摘要/通用配置 KV);结构化 spec 持久化(草稿)+ 渲染 YAML;保存结构校验;触发 tab 复用 2-3。
- ❌ 不做(后续 story):变量与缓存表单 / 环境与凭据表单(=2-4)· AI 生成流水线(=2-5,画布上 AI 按钮可见但本期点了提示"2-5")· 完整引用存在性校验(凭据/服务器/镜像仓库是否存在 = 2-6)· 任务类型的强 schema(本期 config 为自由 KV,2-4/2-6 收紧)· 真正执行(Epic 3/4)。

## ⛓ 冻结 API 契约(前后端两 Agent 各自独立开发再集成 —— 不得私改)
Base:认证保护(`requireAuth`),写方法过 `requireCSRF`(双提交 `X-CSRF-Token`)。错误体沿用既有 `{code,message}`。

### GET `/api/projects/{id}/pipeline`
首次访问无配置 → **惰性生成默认**(种子:源阶段引用项目仓库 + 空的 构建/部署/通知 阶段)并返回。
200 →
```json
{
  "stages": [
    { "id": "stg_src", "name": "流水线源", "kind": "source",
      "jobs": [ { "id":"job_src", "name":"Gitee 源", "type":"git_source", "summary":"main · push/tag/PR", "config": {} } ] },
    { "id": "stg_build", "name": "构建", "kind": "build", "jobs": [] },
    { "id": "stg_deploy", "name": "部署", "kind": "deploy", "jobs": [] },
    { "id": "stg_notify", "name": "通知", "kind": "notify", "jobs": [] }
  ],
  "yaml": "stages:\n  - name: 流水线源\n    ...",
  "status": "draft",
  "updatedAt": "2026-05-30T..."
}
```
- `kind` 枚举:`source | build | deploy | notify | custom`。
- `job.type`:本期自由 token 字符串(如 `git_source|build_image|push_image|deploy_ssh|health_check|notify|custom`),不强约束。
- `job.config`:`object`(自由 KV,本期任意结构;前端抽屉编辑为字符串 KV 即可)。
- `job.summary`:卡片副标题展示文本(可空)。
- `status`:本期恒 `"draft"`(active/发布 = 后续)。
- `yaml`:服务端按 spec 渲染的只读 YAML 文本(供"查看源码"/导出;本期前端可折叠展示,非必须可编辑)。

### PUT `/api/projects/{id}/pipeline`
请求体 = `{ "stages": [ ...同上 stages 形状,不含 id 也可(服务端补全) ] }`(只收 stages;yaml/status 由服务端派生)。
- 服务端:规范化(补 id、trim、去空)→ 结构校验 → 渲染 YAML → 持久化(status=draft)→ 回读返回 200 同 GET DTO。
- 校验失败 422:`invalid_stage`(阶段名空 / kind 非枚举)· `invalid_job`(任务名空 / type 空)· `duplicate_id`(stage/job id 重复)。`MaxBytesReader` 限 256KB。
- 项目不存在 404 `project_not_found`。

> **冻结点:** pipelineDTO 形状(stages[].{id,name,kind,jobs[].{id,name,type,summary,config}} + yaml + status + updatedAt)定死。后续 2-4/2-5/2-6 只在 `job.config` 内填充强 schema、不改外层形状。

## 文件切分(零冲突,前后端并行)
**后端(只动 Go):**
- `internal/pipeline/pipeline.go` + `pipeline_test.go` —— 领域层 Service(Spec/Stage/Job 模型、Get 惰性默认、Save 校验+规范化+YAML 渲染、`internal/store` 经 db 触库)。
- `internal/store/migrations/0008_pipelines.sql` —— `pipeline_configs`(project_id PK·spec_json·spec_yaml·status·created_at·updated_at·FK→projects CASCADE)。
- `internal/httpapi/pipelines.go` + `pipelines_test.go` —— GET/PUT handler + DTO 映射 + 错误映射。
- `internal/httpapi/router.go`(改):加 `WithPipelines(s pipeline.Service)` Option + 挂载 `GET/PUT /api/projects/{id}/pipeline`(GET 过 auth;PUT 过 auth+CSRF)。
- `cmd/devopstool/main.go`(改):装配 `pipeline.New(db)` + `httpapi.New(..., WithPipelines(...))`。
- `go.mod`/`go.sum`:可加 `gopkg.in/yaml.v3`(渲染 YAML;纯 Go,体积小)。**拉依赖需 `dangerouslyDisableSandbox`(goproxy.cn 已配)。** 若不愿加依赖,可手写最小 YAML 序列化(spec 结构简单),二选一。

**前端(只动 `web/`):**
- `web/src/views/ProjectPipeline.vue`(新):4-tab 外壳(流水线编排 / 变量与缓存 / 触发设置 / 环境与凭据)+ 保存草稿按钮 + AI 生成按钮(点击提示"2-5")。
- `web/src/components/pipeline/PipelineCanvas.vue` + `StageColumn.vue` + `JobCard.vue` + `JobDrawer.vue` + `pipeline.css`(新):画布(点阵背景)、阶段列、任务卡、**曲线 SVG 连线 + 流光动效**(prefers-reduced-motion 退化)、抽屉就地编辑(名称/类型/摘要/config KV 增删)、添加/删除阶段与任务。
- `web/src/components/TriggersPanel.vue`(新,从 `ProjectTriggers.vue` 抽出触发表单主体)。
- `web/src/views/ProjectTriggers.vue`(改):瘦身为 `<TriggersPanel>` 包装(保留 `/projects/:id/triggers` 路由向后兼容)。
- `web/src/api/pipeline.ts`(新):`getPipeline(id)` / `savePipeline(id, {stages})`。
- `web/src/router/*`(改):加 `/projects/:id/pipeline` 路由。
- `web/src/views/Projects.vue`(改):项目卡「配置」入口 → `/projects/:id/pipeline`(原指向 triggers)。

> **无共享文件**:main.go/router.go=后端;Projects.vue/router/Triggers=前端。契约靠本文件冻结。

## Dev Notes 护栏(沿用既有约定)
- **领域三层命名**:仅 `internal/store` 触库语句外置?本项目实际是领域包持 `*sql.DB` 经参数化 SQL 直触(见 trigger.go),沿用此风格;参数化、永不拼接。
- **惰性默认**:仿 trigger `createDefault` 的 `INSERT ... ON CONFLICT(project_id) DO NOTHING` + 回读权威行,防并发首访竞态。
- **不抬空载内存**:领域包**无 `init()` 副作用**、无包级重对象(教训:argon2 在 init 跑曾把 RSS 18→82MB)。保存 `make mem-check` ≤100MB。
- **校验在服务端**:前端校验仅体验,服务端权威(422 定位项)。
- **secret 不沾**:本期 spec 不存任何明文 secret(凭据仍走保险库引用,2-4 才落引用字段);config KV 本期纯展示性,勿在此引入明文密钥。
- **冻结骨架所有权**:pipelineDTO 外层形状本 story 定义并冻结,后续只填 `job.config`。

## Tasks
1. [后端] 迁移 0008 + `internal/pipeline` 领域(模型/Get惰性默认/Save校验规范化/YAML 渲染)+ 单测(惰性默认、校验各分支、重复 id、规范化补 id、YAML 往返)。
2. [后端] `httpapi/pipelines.go` GET/PUT + DTO/错误映射 + 单测(401 未认证、403 无 CSRF、404 项目不存在、422 各校验、200 往返);router `WithPipelines` 挂载;main 装配。
3. [前端] `ProjectPipeline.vue` 4-tab 外壳 + 保存草稿;`api/pipeline.ts`。
4. [前端] 画布组件(阶段/任务卡/曲线流光连线/添加删除)+ 抽屉就地编辑。
5. [前端] 抽出 `TriggersPanel.vue`,触发 tab 复用;变量/环境 tab 占位标注 2-4;`Projects.vue` 配置入口改指向 `/pipeline`;路由新增。

## 编排者亲验清单(派活后必跑,别只信 agent 自检)
- `export PATH="$HOME/sdk/go/bin:$PATH"` → gofmt / `go vet ./...` / `go test ./...` / `go test -race ./internal/pipeline ./internal/httpapi` / `CGO_ENABLED=0 go build` / `make mem-check`(RSS ≤100MB)。
- 前端 `vue-tsc` 0 错 + `vite build` 成功。
- **真二进制 API 冒烟**(就绪轮询):GET 惰性默认 4 阶段 + yaml 非空 · PUT 增删阶段/任务往返 · PUT 空阶段名 422 · PUT 重复 id 422 · 无 CSRF 403 · 未认证 401 · 不存在项目 404。
- **真浏览器 E2E**(`.sky-aitest/runs/pipeline-check/`,headless chromium):登录回归 · 项目卡「配置」进 4-tab · 流水线编排画布渲染(4 阶段 + 曲线连线可见)· 点任务卡 → 抽屉就地展开 · 添加阶段/任务 · 保存草稿 → 刷新仍在 · 触发 tab 复用正常 · 变量/环境 tab 占位 · **截图肉眼审 UI 质量**(连线优美、内容铺满、无 AI 模板感)。

## Review Findings (code-review 2026-05-30,9-agent 三层对抗)
**已修 patch:**
- [x] [Patch] pipeline.Get 并发删项目 sql.ErrNoRows → project_not_found(原 500)[pipeline/pipeline.go]
- [x] [Patch] 源阶段不变式:Save 拒无 source 阶段 spec(防空 stages 抹仓库引用)[pipeline/pipeline.go]
- [x] [Patch] JobDrawer objectToKV null 守卫 [web/components/pipeline/JobDrawer.vue]
- [x] [Patch] PipelineCanvas/Vars/Env 双向绑定无限循环(已在实现期修,本轮复核守卫完整)
**deferred:** 并发保存丢更新(乐观锁) · job.config TS/Go 类型不匹配 · 跨 tab 未保存草稿
**Acceptance:无违反**(4-tab/抽屉就地/曲线/spec+YAML/冻结 DTO 全合规)
