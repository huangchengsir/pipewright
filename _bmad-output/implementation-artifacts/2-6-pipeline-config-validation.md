# Story 2.6: 流水线配置合法性校验(FR-9)

status: ready-for-dev
epic: 2
基线: master(13 story done;2-2 spec / 2-4 settings / 2-3 triggers 各自 tab 已有局部校验)
covers: FR-9(schema/必填/引用存在性校验,失败定位到具体项)

## As a / I want / So that
As a 管理员,I want 保存配置(手写或 AI 生成)时执行服务端权威校验,So that 非法配置无法保存,且错误能定位到具体项。

## Acceptance Criteria
1. **Given** 配置缺少必填项或引用了不存在的凭据/服务器 **When** 保存 **Then** 阻止保存并定位到具体错误项(服务端权威校验)。
2. **Given** 配置合法 **When** 保存 **Then** 配置入库并可用于触发流水线运行。
3. **And** 前端镜像校验仅为体验,最终以服务端校验为准。

## 范围边界(本期做 / 不做)
- ✅ 做:**跨 tab 整体就绪度校验**(单 tab 内的局部校验 2-2/2-4/2-3 已做;2-6 补**跨 tab 引用 + 整体完整性**)。一个**只读校验端点**聚合 spec + settings + triggers + 项目凭据,产出结构化 issue 列表(error/warning/info,定位到 tab+字段)。前端校验面板/横幅展示 issues,点击定位到对应 tab。
- ✅ 关键新校验(跨 tab,单 tab 内无法做):分支映射的 `environment` 必须匹配「环境与凭据」中已定义的环境名;构建模型完整性(dockerfile 路径 / toolchain 版本);镜像仓库声明完整性(url+凭据);产物/部署阶段存在性。
- ❌ 不做:目标服务器**存在性**校验(4-1 服务器表未建 → 本期 `targetServerIds` 出 **warning「目标服务器登记将在 4-1 提供」**,非 error)· AI 生成(=2-5)· 改动 2-2/2-4/2-3 各自 tab 的既有保存校验(它们已对、不回退;2-6 是**叠加的整体校验**,不替换)· 真正执行门控(本期校验为只读视图 + 体验,不阻断 3-2 触发——是否用 ready 门控触发留作可选,见下)。

## ⛓ 冻结 API 契约
### GET `/api/projects/{id}/pipeline/validation`(认证保护,只读)
聚合该项目的 pipeline spec(2-2)+ settings(2-4)+ trigger 配置(2-3)+ 项目凭据,跑校验。200 →
```json
{
  "ready": false,
  "issues": [
    { "severity": "error", "code": "environment_undefined", "scope": "triggers",
      "field": "branchMappings[0].environment",
      "message": "分支映射「main」引用的环境「生产」未在「环境与凭据」中定义" },
    { "severity": "warning", "code": "target_servers_pending", "scope": "triggers",
      "field": "branchMappings[0].targetServerIds",
      "message": "目标服务器登记将在 Story 4-1 提供;当前映射的服务器引用暂不校验" },
    { "severity": "warning", "code": "no_deploy_stage", "scope": "canvas",
      "field": "stages", "message": "流水线无部署阶段,触发后仅构建产物" }
  ]
}
```
- `ready` = **无 error 级 issue**(warning/info 不影响 ready)。
- `severity` ∈ `error|warning|info`;`scope` ∈ `canvas|vars|triggers|envs`(前端据此定位 tab);`field` 为定位路径(可空);`code` 稳定枚举(前端可据此本地化);`message` 中文人读。
- 项目不存在 → 404 `project_not_found`。

### 校验规则(本期,error 阻塞 ready / warning 提示)
**canvas(spec):** 源阶段缺失→error `source_stage_missing`(2-2 不变式已基本兜住,此为聚合复述);无 build 且无 deploy 阶段→warning `no_build_or_deploy`;有 deploy 阶段但 settings 无环境定义→warning `deploy_without_environment`。
**vars(build settings):** model=toolchain 但 language/version 空→error `toolchain_incomplete`;model=dockerfile 但 dockerfilePath 空→warning `dockerfile_path_default`(退化为 Dockerfile);secret 构建变量 credentialId 悬挂(凭据已删)→error `credential_missing`;构建变量 key 重复(跨 build 已由 2-4 拦,此为聚合)→（略,2-4 已 error)。
**envs(environments):** 镜像仓库 type 非空但 url 空→error `registry_url_missing`;镜像仓库绑定但无 credentialId→warning `registry_credential_missing`;环境的 secret envVar credentialId 悬挂→error `credential_missing`。
**triggers:** 分支映射 environment 非空但未在 settings.environments 中定义(按 name 匹配)→**error `environment_undefined`**(核心跨 tab 校验);所有触发事件关闭且无分支映射→info `manual_only`;targetServerIds 非空→warning `target_servers_pending`(留 4-1)。
**project:** 项目凭据不存在/不可读→error `project_credential_missing`(项目创建已要求凭据,此为兜底)。

> **冻结点:** validation DTO 形状(ready + issues[].{severity,code,scope,field,message})定死;后续(2-5 AI 生成、4-1 服务器、Epic7)只**新增 code/规则**,不改外层形状。是否用 `ready` 门控 3-2 触发 = 可选增强(本期默认只读视图,不阻断触发;如做,在 manual/webhook 创建前查 ready,error 则 422 `config_not_ready` + issues)。**作者定:本期先只读视图,不门控触发**(避免桩 runner 阶段误伤;真实执行 Epic3/4 再上门控)。

## 文件切分(零冲突,前后端并行;同一棵树,文件不重叠)
**后端(只动 Go):**
- `internal/pipeline/validate.go` + `validate_test.go`:**纯函数** `Validate(in ValidationInput) []Issue`(ValidationInput = 中性值类型:Stages + BuildConfig + Environments + 分支映射〔pattern/environment/targetServerIds〕+ events + 项目凭据存在性 bool)。`Issue{Severity,Code,Scope,Field,Message}`。纯逻辑、易单测(逐规则各一例 + ready 计算)。
- `internal/httpapi/pipeline_validation.go` + `_test.go`:`GET /api/projects/{id}/pipeline/validation` handler——取 pipeline.Service.Get(spec)+ pipeline.SettingsService.Get(settings)+ trigger.Service.Get(分支映射/events)+ vault.Exists(凭据),组装 ValidationInput → `pipeline.Validate` → DTO。错误映射(404/503)。
- `router.go`(改):挂该路由(auth);`main.go`(改):handler 需 pipeline+settings+trigger+vault 四服务(已都在 options,复用;加 `WithPipelineValidation` 或直接在现有 handler 注入)。**注意:聚合 handler 需同时拿到四服务——可在 router 内用已注入的 o.pipelines/o.pipelineSettings/o.triggers/o.vault 组装,无需新 Option。**
**前端(只动 `web/`):**
- `web/src/api/pipelineValidation.ts`(新):`getValidation(id)` → 上述 DTO。
- `web/src/components/pipeline/ValidationPanel.vue`(新):校验结果面板——ready 徽标(复用 1-6 StatusBadge?或自绘)、issue 列表(error 红/warning 琥珀/info 蓝,按 scope 分组),点 issue → emit 跳对应 tab。复用 1-6 `components/ui/*`。
- `web/src/views/ProjectPipeline.vue`(改):顶部加「校验」按钮/就绪徽标 + 挂 ValidationPanel(抽屉或顶部横幅);切 tab / 保存草稿后可重新拉校验。**不碰 canvas/vars/envs/triggers tab 各自逻辑**(只加聚合校验视图)。

> **本机能跑、不卡 Docker**:纯校验逻辑 + 真二进制造数据可验。

## Dev Notes 护栏
- **服务端权威**:前端面板仅展示,校验逻辑全在后端纯函数;前端不重复实现规则(避免漂移)。
- 纯函数 Validate 无 init 副作用、无 DB(DB 读在 handler 层);易 `-race` 单测。`make mem-check` ≤100MB。
- environment 匹配按 name 精确匹配(trim 后);大小写敏感(环境名是标识)。
- 不改既有 tab 保存校验(2-2/2-4/2-3 已对);2-6 是叠加只读视图,**不门控 3-2 触发**(作者定)。
- issue `code` 稳定枚举,前端可据此定位/本地化,别只靠 message 文本。

## Tasks
1. [后端] `internal/pipeline/validate.go` 纯函数 + ValidationInput/Issue 模型 + 全规则 + 单测(逐规则 + ready 计算 + environment 跨 tab 匹配)。
2. [后端] `httpapi/pipeline_validation.go` 聚合 handler(取四服务组装)+ 路由挂载 + main 装配 + 单测(401/404/200 各形态)。
3. [前端] `api/pipelineValidation.ts` + `ValidationPanel.vue` + ProjectPipeline 接入(就绪徽标 + 面板 + 点 issue 跳 tab)。

## 编排者亲验清单(派活后必跑,别只信 agent 自检)
- gofmt/vet/`go test ./...`/`-race ./internal/pipeline ./internal/httpapi`/`CGO_ENABLED=0 build`/`make mem-check`。
- **前端必跑 `npm --prefix web run typecheck`(不是 build!)** + build。
- 真二进制(种子项目+凭据+settings+triggers):GET validation 各形态——合法配置 ready=true 空 issues;分支映射引用未定义环境 → error environment_undefined;toolchain 缺版本 → error;registry url 空 → error;targetServerIds 非空 → warning target_servers_pending;无 deploy 阶段 → warning;未认证 401;不存在项目 404。
- 真浏览器:/pipeline 顶部就绪徽标 + 校验面板渲染 issues(error/warning 着色分组)· 点 issue 跳对应 tab · 改配置后重拉校验 ready 变化 · 截图肉眼审质量。

## Review Findings (code-review 2026-05-30,综合对抗)
**已修 patch:**
- [x] [Patch][中] vault 未配置被误报为一堆 credential_missing+project_credential_missing(ready 恒 false)→ 环境级状态应单独提示:handler 聚合 Get 遇 vault 未配短路为 200+单条 vault_unconfigured warning(原 trigger.Get 掩码失败致 500 也一并修);纯函数加 VaultUnconfigured 抑制悬挂误报 [pipeline_validation.go, validate.go]
**复核通过:** environment 跨 tab 匹配、DTO 契约、降级不 500、前端只展示均通过。
**deferred:** 事件全关但有分支映射无提示 · 镜像仓库 type 空时悬挂凭据漏检
