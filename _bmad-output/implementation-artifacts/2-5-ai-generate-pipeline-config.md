# Story 2.5: AI 生成流水线配置(FR-8)

status: ready-for-dev
epic: 2(收尾)
基线: master(15 story;7-1 AI provider done → 本期消费 ai.Service;2-2 spec/2-4 settings/2-3 trigger 写入复用)
covers: FR-8(AI 分析仓库推荐配置、NL 补充、预览、全/部分接受;未配 AI 优雅降级)

## As a / I want / So that
As a 管理员,I want 初始化项目时让 AI 分析仓库并推荐一份流水线配置,可用自然语言补充、以预览呈现、一键全部接受或逐项部分接受,So that 我不必手写配置即可快速得到一份合理的流水线。

## Acceptance Criteria
1. **Given** 对一个 Node 22 仓库 **When** AI 分析仓库 **Then** 推荐含 `node:22` 构建的配置,以预览呈现(技术栈/版本/结构判断可见)。
2. **Given** 预览 **When** 用户补充"push main 部署到生产" **Then** 预览反映该分支→环境映射(重新生成)。
3. **And** 用户可一键全部接受,或逐项勾选部分接受 → 仅被勾选项写入流水线配置。
4. **And** 未配置 AI 提供商时,向导优雅提示"AI 不可用,可手动配置",不阻断手动建项目(NFR-10)。

## ⚠️ 本机双外部依赖约束(亲验须知)
- **拉仓库**(go-git clone 读 manifest)与 **调 LLM**(chat completion)在本机都跑不通(GFW 拦 gitee/github/anthropic/openai;Ollama 未装)。**亲验用**:① 本地 file:// 夹具裸仓库(含 package.json,go-git clone 离线可读;复用 prober 的 test-only allowInsecure SSRF 钩子)验 clone+分析;② 本地 stub LLM server(回固定结构 JSON)验生成+解析。**clone 失败须优雅降级**(degraded:仅凭 repoUrl+NL 让 LLM 推断,不 500)——这既是真实健壮性,也让真实失败路径可验。

## 范围边界(本期做 / 不做)
- ✅ 做:仓库浅克隆 + manifest 技术栈/版本检测(node/go/java/python + Dockerfile)· `ai.Service.Generate`(构 prompt→调 LLM chat→解析结构化 JSON 提案)· 预览端点(返回分析+提案,不落库)· 应用端点(全/部分接受→写 spec/settings/triggers 复用既有服务)· 未配 AI 优雅降级 · clone 失败降级 · 前端 AI 向导(生成→预览逐项勾选→NL 补充重生成→全/部分接受)。
- ❌ 不做:真实外网 e2e(stub 验)· 流式输出 · 多轮对话记忆(每次无状态生成)· AI 生成 settings 的 secret 引用(提案只给非 secret 声明,凭据由用户手动绑;避免 AI 编造 credentialId)· 强 schema 校验(应用后由 2-6 校验兜底)。

## ⛓ 冻结 API 契约
Base:认证保护,写过 CSRF,`MaxBytesReader` 限体。错误体 `{"error":{"code,message}}`。
### POST `/api/projects/{id}/pipeline/ai-generate`
请求 `{ "nlSupplement": "可选自然语言补充" }` → 200
```json
{
  "available": true,
  "reason": "",
  "analysis": { "cloned": true, "language": "node", "languageVersion": "22",
                "buildTool": "npm", "hasDockerfile": false, "artifactHint": "image",
                "signals": ["package.json", "engines.node=22"] },
  "proposal": {
    "stages": [ { "id":"p_s1", "name":"流水线源", "kind":"source", "jobs":[...] },
                { "id":"p_s2", "name":"构建", "kind":"build",
                  "jobs":[ { "id":"p_j1", "name":"隔离构建", "type":"build_image", "summary":"node:22 容器构建" } ] },
                { "id":"p_s3", "name":"部署", "kind":"deploy", "jobs":[...] } ],
    "build": { "model":"toolchain", "toolchain":{"language":"node","version":"22"}, "artifactType":"image",
               "dockerfilePath":"" },
    "branchMappings": [ { "id":"p_m1", "branchPattern":"main", "environment":"生产" } ],
    "rationale": "检测到 Node 22(package.json engines),推荐工具链构建 node:22 + main→生产 部署。"
  }
}
```
- `available=false` 当 AI 未配置/未启用 → `reason`="AI 提供商未配置,可在 设置·AI 提供商 配置;或手动编辑流水线"。**不阻断**(HTTP 200,available=false)。
- `analysis.cloned=false` 当 clone 失败(网络/不可达)→ 降级:analysis 仅含 cloned=false + degraded 说明,LLM 仅凭 repoUrl+NL 推断(proposal 仍尽力给)。
- LLM 调用失败(网络/解析失败)→ 200 `available=true` 但带 `reason`="AI 生成失败:<人读错误>" + 空/部分 proposal;**绝不 500、绝不含密钥**。
- 每个 stage/job/branchMapping 带 `id`(`p_` 前缀,供前端逐项勾选)。proposal 形状对齐 2-2 spec stages + 2-4 build + 2-3 branchMappings 的**子集**(便于 apply 映射)。

### POST `/api/projects/{id}/pipeline/ai-apply`
请求 `{ "proposal": {…同上 proposal…}, "selections": { "stageIds": ["p_s2","p_s3"], "build": true, "branchMappingIds": ["p_m1"] } }` → 200
- 仅写被勾选项:stageIds → 合入 pipeline spec(**始终保留/补源阶段**,满足 2-2 源阶段不变式)· build=true → 写 pipeline_settings · branchMappingIds → 合入 trigger 分支映射(去重、复用既有)。经既有 `pipeline.Save`/`SettingsService.Save`/`trigger.Save`(各自校验仍生效;失败 422 定位)。
- 返回 `{ "applied": { "spec": bool, "settings": bool, "triggers": bool } }` + 不报错的提示。
- 项目不存在 404;校验失败 422(透传既有错误码)。
- **应用 settings 时 secret 引用为空**(AI 不编造 credentialId);用户后续在「环境与凭据」手动绑。

> **冻结点:** generate(available/reason/analysis/proposal)+ apply(selections/applied)形状定死。proposal 子结构对齐既有 spec/settings/trigger,**apply 只映射不新增字段**。

## 文件切分(零冲突,前后端并行;同一棵树)
**后端(只动 Go):**
- `internal/ai/analyze.go` + `analyze_test.go`:`RepoAnalyzer`——go-git 浅克隆(`CloneContext` 内存 storer,Depth:1,SingleBranch)+ 读 manifest(package.json/go.mod/pom.xml/build.gradle/requirements.txt/pyproject.toml/Dockerfile)→ `RepoAnalysis`。**复用 prober 的 SSRF 校验 + test-only allowInsecure 钩子**(file:// 夹具)。clone 失败 → cloned=false 降级(不报致命)。token 经 vault 取、用完即弃。
- `internal/ai/generate.go` + `generate_test.go`:`ai.Service` 加 `Generate(ctx, GenerateInput{RepoAnalysis, NL}) (*Proposal, error)`——构 prompt(含分析 + NL + 期望 JSON schema)→ 调 LLM chat(claude `POST /v1/messages`、openai `POST /v1/chat/completions`、ollama `POST /api/chat`,用注入 client)→ 解析 JSON(容错:剥 markdown fence)→ Proposal。未配置/未启用 → ErrAINotConfigured;调用/解析失败 → 人读错误(绝无密钥)。stub LLM 单测三 provider。
- `internal/httpapi/ai_generate.go` + `_test.go`:generate + apply 两 handler。generate 编排(分析→Generate→DTO,各失败降级)。apply 映射 selections → 调 pipeline/settings/trigger 服务。错误映射。
- `router.go`(改):挂两路由(auth+CSRF);`main.go`(改):ai-generate handler 需 analyzer(go-git)+ ai.Service + pipeline/settings/trigger 服务装配(复用已注入)。
**前端(只动 `web/`):**
- `web/src/api/aiGenerate.ts`(新):`aiGenerate(id, {nlSupplement})` / `aiApply(id, {proposal, selections})`。
- `web/src/components/pipeline/AIGenerateWizard.vue`(新):向导(模态/抽屉)——「AI 生成流水线」触发 → 生成中态 → 分析卡(技术栈/版本/结构)+ 提案预览(stages/build/branchMappings 逐项**勾选**)+ NL 补充输入(重新生成)+「全部接受」/「应用所选」。available=false 显友好引导(去 设置·AI)。复用 1-6 `components/ui/*`。
- `web/src/views/ProjectPipeline.vue`(改):**把「AI 生成流水线」按钮的 toast 占位换成打开 AIGenerateWizard**;apply 成功后重拉 pipeline/settings/trigger 刷新画布。**不碰各 tab 编辑逻辑**。

## Dev Notes 护栏
- **AC-SEC**:apiKey/token 仅进程内取用即弃,prompt/日志/错误/响应**绝无明文密钥**;LLM 错误人读化。clone 用完清内存。
- **优雅降级两路**:AI 未配 → available=false 不阻断;clone 失败 → cloned=false 仅凭 NL+url。**均不 500**。
- AI 不编造 credentialId(settings 提案 secret 引用留空,用户手动绑)。
- apply 复用既有 Save(校验/源阶段不变式/凭据校验仍生效);AI 提案可能不合法 → apply 透传 422 由前端提示 + 2-6 校验兜底。
- 无 init 副作用;clone Depth:1 限体防大仓库 OOM;`make mem-check` ≤100MB。参数化 SQL。
- **复用 prober SSRF 收口**:生产仅 http/https、拒元数据/环回;test-only 钩子放 file:// 夹具。

## Tasks
1. [后端] `internal/ai/analyze.go`(go-git 浅克隆 + manifest 检测 + 降级)+ 单测(file:// 夹具 node/go 仓库 → 正确 language/version;clone 失败 → cloned=false)。
2. [后端] `ai.Service.Generate`(prompt + LLM chat 三 provider + JSON 解析容错)+ 单测(stub LLM → 解析 Proposal;未配 ErrAINotConfigured;解析失败人读错误无密钥)。
3. [后端] `httpapi/ai_generate.go` generate+apply 编排 + 路由 + main 装配 + 单测(available=false / 降级 / apply 全选/部分选 → 写对应服务 / 401/403/404)。
4. [前端] `api/aiGenerate.ts` + `AIGenerateWizard.vue` + ProjectPipeline 接通(按钮开向导 / 预览勾选 / NL 重生成 / 应用刷新画布 / available=false 引导)。

## 编排者亲验清单(派活后必跑,别只信 agent 自检)
- gofmt/vet/`go test ./...`/`-race ./internal/ai ./internal/httpapi`/build/mem-check。
- **前端必跑 `npm --prefix web run typecheck`(不是 build!)** + build。
- 真二进制:**未配 AI → ai-generate available=false 友好 reason,手动建项目不受阻** · 配置指向**本地 stub LLM** + **本地 file:// node 夹具仓库**(test-only 放行)→ generate 返回 analysis(language=node/version=22)+ proposal(node:22 build) · NL 补充重生成反映 main→生产 · apply 全选 → spec+settings+triggers 写入(GET 各端点确认) · apply 部分选 → 仅勾选项写入 · clone 失败(不可达 url)→ cloned=false 降级不 500 · **响应/日志/DB 无明文 key** · 401/403/404。
- 真浏览器:ProjectPipeline「AI 生成流水线」开向导 · 生成中态 · 分析卡 + 提案预览勾选 · NL 输入重生成 · 全部/部分接受 → 画布刷新 · 未配 AI 友好引导 · **截图肉眼审质量**(看截图,别只信正则;selector 精确)。

## Review Findings (code-review 2026-05-30,综合对抗)
**已修 patch:**
- [x] [Patch][Major] LLM 失败降级时前端崩溃:后端返 available=true+proposal=null,向导 result 块未守 proposal → 解引用 null 白屏 → 加 v-if="response.proposal" 守卫(含 footer 应用按钮)[AIGenerateWizard.vue]
**复核通过:** 编排者已修的 apply 合并阶段重复 bug 逐路径正确;无 key 泄漏/clone SSRF/OOM 边界/两路降级均通过。
**deferred:** selectAll();apply() 同 tick 读 ref 时序脆弱(当前可用)
