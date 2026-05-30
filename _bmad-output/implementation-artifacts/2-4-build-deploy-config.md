# Story 2.4: 构建/部署配置(变量·缓存·环境·凭据·镜像仓库)

status: ready-for-dev
epic: 2
基线: master(含 2-2 流水线编辑器 + pipeline_configs;**填 2-2 占位的两 tab**)
covers: FR-5(构建模型 A/B 声明侧)· FR-6(产物类型)· FR-7(外部镜像仓库声明侧)· 部分 FR-9(引用存在性校验)

## As a / I want / So that
As a 管理员,I want 在"变量与缓存""环境与凭据"两个 tab 声明构建模型(A/B)、产物类型、构建变量与缓存、环境定义、引用的凭据与镜像仓库,So that 流水线拥有完整、可校验的构建与部署声明(实际执行在 Epic 3/4)。

## Acceptance Criteria
1. **Given** 变量与缓存 tab **When** 配置 **Then** 可选构建模型 A(自带 Dockerfile)/ B(工具链版本)、产物类型(镜像/JAR/dist)、构建变量(明文 + 引用保险库的 secret 掩码)、依赖缓存。
2. **Given** 环境与凭据 tab **When** 配置 **Then** 可定义环境(各自目标服务器 + 环境变量)、引用保险库凭据(仓库/部署 SSH/镜像仓库)、绑定外部镜像仓库(FR-7 声明侧)。
3. **And** 所有 secret 均以掩码/引用呈现,**绝不明文**。

## 范围边界(本期做 / 不做)
- ✅ 做:两 tab 的**声明式配置 + 持久化 + 服务端校验**(含引用保险库凭据的存在性校验=部分 FR-9)。构建模型 A/B、产物类型、构建变量(明文值 + secret 引用 credentialId)、依赖缓存声明、环境定义(名称 + 目标服务器引用占位 + 环境变量〔明文+secret 引用〕)、镜像仓库绑定(类型 + 地址 + 凭据引用)。
- ❌ 不做:真实构建/部署执行(Epic 3/4)· 目标服务器**存在性**校验(4-1 服务器表未建,**占位:校验非空,存在性留 4-1**,仿 trigger 的 targetServerIds 处理)· AI 生成(=2-5)· 流水线编排 spec 的改动(2-2 已冻结,本期**不碰 pipeline_configs 的 spec**,只加独立 settings 表)。

## ⛓ 冻结 API 契约(独立于 2-2 的 pipeline spec)
Base:认证保护,写过 CSRF,`MaxBytesReader` 256KB。错误体 `{"error":{"code,message}}`。

### GET `/api/projects/{id}/pipeline/settings`
首次无配置 → 惰性默认(空构建变量/默认构建模型 A/产物 image/空环境)。200 →
```json
{
  "build": {
    "model": "dockerfile",            // dockerfile(A) | toolchain(B)
    "dockerfilePath": "Dockerfile",   // model=dockerfile 时
    "toolchain": { "language": "node", "version": "22" },  // model=toolchain 时
    "artifactType": "image",          // image | jar | dist
    "vars": [ { "id":"...", "key":"NODE_ENV", "secret": false, "value": "production" },
              { "id":"...", "key":"NPM_TOKEN", "secret": true, "credentialId": "<vault id>", "maskedValue": "••••_zzz" } ],
    "cache": { "enabled": true, "paths": ["node_modules", ".npm"] }
  },
  "environments": [
    { "id":"...", "name":"生产", "targetServerIds": [],
      "envVars": [ { "id":"...", "key":"API_URL", "secret": false, "value":"https://..." },
                   { "id":"...", "key":"DB_PASS", "secret": true, "credentialId":"<vault id>", "maskedValue":"••••" } ],
      "imageRegistry": { "type":"harbor", "url":"harbor.acme.com", "credentialId":"<vault id>", "maskedCredential":"••••" } }
  ],
  "updatedAt": "RFC3339"
}
```
### PUT `/api/projects/{id}/pipeline/settings`
请求体 = `{ build, environments }`(同上形状,secret 项只传 `{key,secret:true,credentialId}`,不传明文/掩码)。服务端:规范化(补 id、trim)→ 校验 → 持久化 → 回读返回 200。
- 校验:`build.model∈{dockerfile,toolchain}`、`artifactType∈{image,jar,dist}`、var/envVar key 非空且不重复(同作用域内)、**secret 项的 credentialId 必须存在于保险库(`vault.Get`,不存在→422 `credential_not_found`)**、imageRegistry.type∈{harbor,acr,dockerhub,custom}。失败 422 定位项(`invalid_build`/`invalid_var`/`invalid_environment`/`credential_not_found`)。
- targetServerIds:**本期仅校验字符串非空,不校验存在性**(留 4-1,仿 trigger)。
- 项目不存在→404;vault 未配置且有 secret 引用→422 `vault_unconfigured`。
- **绝不存明文 secret**:secret var/envVar 只存 credentialId 引用;响应回掩码(经 `vault` 取 maskedValue)。

> **冻结点:** settings DTO 外层形状(build/environments + 各字段)定死;Epic3/4 执行侧只**消费**不改形状。

## 文件切分(零冲突,前后端;本 story 自成一对 BE+FE)
**后端(只动 Go):**
- `internal/store/migrations/0010_pipeline_settings.sql`:`pipeline_settings(project_id PK, build_json, environments_json, created_at, updated_at, FK→projects CASCADE)`。**只存引用(credentialId)+ 明文非 secret 值,绝无明文 secret**。
- `internal/pipeline/settings.go` + `settings_test.go`(**同包 pipeline,别动 pipeline.go 的 spec 逻辑**):`SettingsService`(Get 惰性默认 / Save 校验规范化 + 经注入的 `vault` 校验 credentialId 存在性 + 回算掩码)。模型 `BuildConfig`/`BuildVar`/`Environment`/`EnvVar`/`ImageRegistry`/`Settings`。
- `internal/httpapi/pipeline_settings.go` + `_test.go`:GET/PUT handler + DTO + 错误映射。
- `router.go`(改):挂 `GET/PUT /api/projects/{id}/pipeline/settings`(GET auth;PUT auth+CSRF)。注意与既有 `/projects/{id}/pipeline` 路由共存、注册顺序别被吞。
- `main.go`(改):`pipeline.NewSettingsService(db, vault)` 装配 + `WithPipelineSettings` Option。
- **改凭据引用是敏感声明 → 可选接 1-4 audit**(`pipeline_config_change`,若枚举无则在 audit 包补,允许只增)。
**前端(只动 `web/`):**
- `web/src/api/pipelineSettings.ts`(新):`getSettings`/`saveSettings`。
- `web/src/views/ProjectPipeline.vue`(改):把 `vars`/`envs` 两 tab 的占位换成真实面板(挂下面两组件);**不碰 canvas/triggers tab 逻辑**。
- `web/src/components/pipeline/VarsCacheTab.vue`(新):构建模型 A/B 切换 + 产物类型 + 构建变量 KV(明文 / secret 选保险库凭据下拉显掩码)+ 依赖缓存。参考 `mockups/mock-pipeline-vars.html`。
- `web/src/components/pipeline/EnvCredsTab.vue`(新):环境列表(名称 + 目标服务器占位"4-1 后启用" + 环境变量 KV)+ 镜像仓库绑定(类型+地址+凭据下拉)。参考 `mockups/mock-pipeline-env.html`。
- 复用 1-6 `components/ui/*`(FormField/AppButton/StatusBadge/EmptyState)+ 既有 `api/credentials.ts`(凭据下拉)。**不碰 web/src/router**(两 tab 在既有 /pipeline 路由内)。

> **与 1-7 并行边界**:见 1-7 文件;2-4 后端碰 router.go/main.go(与 1-7 都碰)→ worktree 提交 + 编排者 git merge。前端 2-4 只动 ProjectPipeline + pipeline 组件 + pipelineSettings.ts,与 1-7 不重叠。

## Dev Notes 护栏
- **AC-SEC**:secret var/envVar/镜像仓库凭据**只存 credentialId 引用**,DB/响应/日志绝无明文。掩码经 `vault` 取(仿 credentials handler)。自动化测试:建含 secret 引用的 settings → 裸 DB grep 无明文。
- credentialId 存在性校验经 `vault.Get`(422 不泄明文);vault 未配置且有 secret 引用 → 422 `vault_unconfigured` 不 panic(仿 trigger)。
- 惰性默认仿 trigger/pipeline 的 ON CONFLICT 回读。无 init 副作用。参数化 SQL。`make mem-check` ≤100MB。
- **别碰 2-2 的 pipeline_configs / spec / canvas**(冻结);settings 是独立表/端点。

## 编排者亲验清单
- gofmt/vet/`go test ./...`/`-race ./internal/pipeline ./internal/httpapi`/build/mem-check。
- **前端必跑 `npm --prefix web run typecheck`(不是 build!)** + build。
- 真二进制(种子项目+凭据):GET 惰性默认 · PUT 构建模型 A/B 切换往返 · 含 secret 引用 var 往返(响应掩码,**裸 DB 无明文**)· 引用不存在凭据→422 · 坏 model/artifactType→422 · 无 CSRF 403 · 未认证 401。
- 真浏览器:/pipeline → 变量与缓存 tab(模型切换/产物/变量含 secret 凭据下拉掩码/缓存)· 环境与凭据 tab(环境/目标服务器占位/环境变量/镜像仓库凭据下拉)· 保存往返 · DOM 无明文 · 截图肉眼审质量。

## Review Findings (code-review 2026-05-30,9-agent 三层对抗)
**已修 patch:**
- [x] [Patch][D1] vault.Delete 在用守卫:被 pipeline_settings/项目 FK 引用的凭据 → 409 credential_in_use [vault/vault.go]
- [x] [Patch] 加 vault.Exists 仅查存在,ensureCredentialExists 不再解密/刷 last_used_at [vault/vault.go, pipeline/settings.go]
- [x] [Patch] maskRegistry 无冒号纯 token 掩码泄明文(实现期修 + 本轮确认)[vault/masker.go]
**deferred:** targetServerIds 去重(→4-1) · registry url 空可存(→2-6) · 跨作用域变量同名 · maskRegistry 冒号路径前段暴露
**Acceptance:无违反**(secret 仅 credentialId 引用、settings DTO 冻结、vault 校验存在性全合规)
