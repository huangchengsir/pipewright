# Story 7.2: AI 失败分析(假说 + 证据 + 置信度)(FR-22)

status: done
epic: 7
基线: master(16 story;7-1 AI provider done → 消费 ai.Service;3-1 run-detail DTO 有冻结 `diagnosis` null slot;1-4 mask.Masker 脱敏)
covers: FR-22(失败日志→LLM→根因假说+修复建议+置信度,AI/证据分栏,优雅降级)· NFR-10 · AC-SEC-04 扩展(出网前脱敏)

## As a / I want / So that
As a 管理员,I want 构建/部署失败时平台基于失败日志给出根因假说、修复建议与置信度,与原始日志证据分栏呈现,So that 我不必翻日志即可定位失败原因。

## Acceptance Criteria
1. **Given** 一次失败运行 **When** 查看运行详情 **Then** 给出根因假说 + 修复建议 + 置信度,附可展开日志证据(命中行高亮);填 run-detail「失败诊断」slot。
2. **Given** 诊断 **When** 呈现 **Then** 语言为「最可能的根因是 X(假说,非结论)」,低置信明示存在其它可能根因;「AI 认为」与「原始日志证据」分栏。
3. **Given** LLM 不可用/超时/不可解析/明显低质 **When** 失败发生 **Then** 结果照常记录,诊断降级为「不可用」提示,**不阻断结果**(NFR-10);一律不当有效诊断展示。
4. **And** 发往 LLM 的日志**出网前 secret 脱敏**(复用 1-4 `mask.Masker`,AC-SEC-04 扩展);CI ≤100MB 断言仍绿(NFR-4)。

## ⚠️ 前置缺口与本机约束(亲验须知)
- **无真实失败日志**:3-3 真实构建卡 Docker、3-6 实时日志 backlog → 系统当前**无运行日志文本**。本期**最小自带失败日志捕获**:桩 runner 失败路径**合成一段真实感错误日志**(如 `npm ERR! missing dependency`),持久化到 run,供诊断。诚实标注「桩日志」,3-3/3-6 落地后换真实日志。
- **LLM 被 GFW 拦/无 Ollama** → 真诊断在本机失败=正常;亲验用**本地 stub LLM server**(回结构化 JSON 诊断)+ 验降级路径(未配/超时/不可解析→不可用)。

## 范围边界(本期做 / 不做)
- ✅ 做:run 加**失败日志捕获**(桩 runner 合成)+ `ai.Service.Diagnose`(脱敏→LLM chat→解析→**质量门控**:超时/不可解析/低质→不可用)+ 诊断持久化 + **填冻结 run-detail diagnosis slot** + `POST /api/runs/{id}/diagnose`(显式重诊断)+ worker 失败时**best-effort 自动诊断**(注入 hook 解耦,不阻断)+ 前端 RunDetail 诊断面板(AI 认为/日志证据 分栏,置信度,命中行高亮)。
- ❌ 不做:真实构建日志(=3-3/3-6)· 失败通知推送(=Epic5,通知未建;本期诊断已生成即可,推送留 Epic5)· 知识库精选(FR-22 提「+知识库」,本期 prompt 内置少量通用错误模式提示即可,正式知识库后续)· 成功/失败 diff(=7-3)· 诊断反馈闭环(=7-5)。

## ⛓ 冻结契约
### run-detail diagnosis slot(本 story 定义并冻结该子形状;3-1 留的 `diagnosis` null 块)
`GET /api/runs/{id}` 的 `diagnosis` 字段(失败且已诊断时非 null;否则 null)→
```json
"diagnosis": {
  "status": "ready",          // ready | unavailable | pending
  "reason": "",               // status≠ready 时的人读原因(LLM 不可用/超时/解析失败/低质;绝无密钥)
  "hypothesis": "最可能的根因是缺少依赖 foo(假说,非结论)",
  "confidence": "high",       // high | medium | low
  "alternateCauses": ["也可能是 lockfile 未提交"],   // 低置信时非空
  "fixSuggestions": ["在 package.json 声明 foo 并提交 lockfile"],
  "evidence": [ { "line": 12, "text": "npm ERR! missing: foo@^1.0", "highlight": true } ],
  "generatedAt": "RFC3339"
}
```
- `status=unavailable` + reason 当 AI 未配/超时/不可解析/低质;此时 hypothesis/fixSuggestions 等为空。**前端据 status 分支**:ready 显诊断,unavailable 显「诊断不可用」+ reason + 重诊断按钮。
- evidence.line 指失败日志行号,highlight=命中行。**evidence 取自脱敏后的日志**(绝无明文 secret)。
### POST `/api/runs/{id}/diagnose`(认证+CSRF)
触发(重)诊断:取该 run 失败日志 → mask 脱敏 → ai.Diagnose → 持久化 → 返回上述 diagnosis 对象。
- run 非失败态 → 422 `run_not_failed`;无失败日志 → diagnosis.status=unavailable reason「无失败日志」;AI 未配 → status=unavailable reason「AI 未配置」;run 不存在 404。**任何 LLM 失败均 200 + status=unavailable,绝不 500、绝无密钥。**

> **冻结点:** diagnosis 子 DTO(status/reason/hypothesis/confidence/alternateCauses/fixSuggestions/evidence/generatedAt)定死;7-5 反馈闭环、7-3 diff 只新增、不改此形状。run-detail 外层 DTO 不变(只把 diagnosis null→填值)。

## 文件切分(零冲突,前后端并行;同一棵树)
**后端(只动 Go):**
- `internal/store/migrations/0012_run_diagnosis.sql`:`pipeline_runs` 加 `failure_log TEXT`(脱敏前原始桩日志)+ `diagnosis_json TEXT`(持久化诊断,空=未诊断)。或独立 `run_diagnoses` 表,你定更干净的。
- `internal/run/run.go`+`runner.go`+`pool.go`(改):StepSink 加 `SetFailureLog(ctx, log string)`(runner 失败时报告日志);RunService 加 `SetFailureLog`/`GetFailureLog`/`SaveDiagnosis`/`GetDiagnosis`;Run 加 `FailureLog`/`Diagnosis` 字段(Diagnosis 为 `*Diagnosis` 领域类型或 raw json)。**StubRunner 失败路径合成真实感错误日志**(多行,含一个可被 mask 的假 secret 以验脱敏)。worker 在 run→failed 后调**注入的 best-effort 诊断 hook**(`func(ctx, runID)`,nil 则跳过;**run 包不 import ai**,经 main 注入,仿 RunCreator 解耦)。
- `internal/ai/diagnose.go`+`diagnose_test.go`:`ai.Service` 加 `Diagnose(ctx, DiagnoseInput{FailureLog, StepName, ProjectName}) (*Diagnosis, error)`。**先 mask 脱敏**(注入 `mask.Masker` 或在 handler 层脱敏后传入——你定;务必出网前脱敏)→ 构 prompt(失败日志 + 少量通用错误模式提示 + 期望 JSON schema)→ LLM chat(复用 7-1/2-5 的 per-provider chat)→ 解析 + **质量门控**(空 hypothesis/解析失败/超时 → status=unavailable)。未配 → status=unavailable reason。`Diagnosis` 模型对齐契约。stub LLM 单测 + 降级各路。
- `internal/httpapi/runs.go`(改)+`run_diagnose.go`(新)+`_test.go`:`POST /api/runs/{id}/diagnose` handler;run-detail DTO `diagnosis` 由 nil 改为读持久化诊断(无则 nil)。错误映射。
- `router.go`+`main.go`(改):挂路由 + 装配 best-effort 诊断 hook(main 把 `func(ctx,runID){ 取日志→脱敏→ai.Diagnose→保存 }` 注入 worker pool)。
**前端(只动 `web/`):**
- `web/src/api/runs.ts`(改):run-detail 类型加 diagnosis;`diagnoseRun(id)` POST。
- `web/src/components/run/DiagnosisPanel.vue`(新或并入 RunDetail):**「AI 认为」分栏**(hypothesis + confidence 徽标 + alternateCauses〔低置信〕+ fixSuggestions 列表)与**「原始日志证据」分栏**(evidence 行,命中行高亮);status=unavailable 显「诊断不可用」+ reason + 「重新诊断」按钮(调 diagnoseRun)。复用 1-6 `components/ui/*`。
- `web/src/views/RunDetail.vue`(改):**填 3-1 留的「失败诊断」slot**(失败态渲染 DiagnosisPanel);**不碰其它态/slot 所有权**。

## Dev Notes 护栏
- **AC-SEC-04 出网前脱敏铁律**:发往 LLM 的日志**必须先过 `mask.Masker`**(把已登记 secret → [MASKED]);prompt/日志/错误/evidence/响应**绝无明文 secret/密钥**(自动化测试:桩日志含假 secret → 诊断响应/evidence 为 [MASKED])。
- **优雅降级**:LLM 不可用/超时(注入 client 超时)/不可解析/空 hypothesis/明显低质 → **status=unavailable**,**绝不 500、绝不阻断 run 结果**;worker 自动诊断失败仅记日志不影响 run 终态。
- **run 包不 import ai**(经 main 注入 hook 解耦,仿 trigger RunCreator)。诊断措辞「假说,非结论」+ 低置信明示其它可能(prompt 约束)。
- 桩失败日志诚实标注;diagnosis 子 DTO 形状冻结。无 init 副作用;`make mem-check` ≤100MB(AI 推理在注入 client、不驻留)。参数化 SQL。

## Tasks
1. [后端] 迁移 0012 + run 失败日志捕获(StepSink/RunService/StubRunner 合成)+ 诊断持久化 + worker best-effort hook(注入解耦)。
2. [后端] `ai.Service.Diagnose`(脱敏→LLM→解析→质量门控+降级)+ 单测(stub LLM ready;未配/超时/不可解析→unavailable;脱敏断言无明文)。
3. [后端] `POST /api/runs/{id}/diagnose` + run-detail diagnosis 填值 + 路由 + main 装配 hook + 单测。
4. [前端] runs.ts diagnosis 类型 + diagnoseRun;`DiagnosisPanel.vue`(AI/证据分栏+置信度+高亮+不可用态)+ RunDetail 填失败诊断 slot。

## 编排者亲验清单(派活后必跑,别只信 agent 自检)
- gofmt/vet/`go test ./...`/`-race ./internal/run ./internal/ai ./internal/httpapi`/build/mem-check。
- **前端必跑 `npm --prefix web run typecheck`(不是 build!)** + build。
- 真二进制:造一个**失败 run**(桩 runner FailAt + 合成日志)→ AI 未配 → diagnose status=unavailable reason「AI 未配置」不阻断 · 配**本地 stub LLM** → diagnose status=ready(hypothesis/confidence/fixSuggestions/evidence)· **桩日志含假 secret → 诊断响应/evidence/DB/日志 [MASKED] 无明文** · run 非失败 → 422 · 不存在 404 · 401/403 · GET run-detail diagnosis 填值 · stub 返不可解析 → status=unavailable 不 500。
- 真浏览器:失败 run 详情页渲染诊断面板(AI 认为/日志证据 分栏、置信度徽标、命中行高亮)· unavailable 态 + 重新诊断按钮 · **截图肉眼审(看截图非正则,selector 精确,等够异步)**。

## Review Findings (code-review 2026-05-30,Blind/Edge/Acceptance 三层对抗)

综合裁决:Auditor 确认 4 条 AC 全满足、冻结 DTO 形状对齐、核心护栏(run 不 import ai / 出网前脱敏 / 降级不 500 不阻断终态 / 参数化 SQL / 桩日志诚实标注)到位,**无阻断性 AC 违反**。两条 agent 高危被读真代码证伪(见 dismissed)。

**待修 patch:**(均已修 + 加回归测试 TestDiagnoseRunPersistUnavailableFlag + 全 `-race` 绿)
- [x] [Review][Patch][Major] 自动诊断钩子无条件 persist `unavailable` 诊断,污染「null=未诊断」干净态 + 无谓 DB 写/LLM 调:取消复用 StatusFailed(run.go:27/36)→ 用户取消的 run 也触发钩子;AI 未配时**每个失败/取消 run 都落一条 unavailable**,run-detail 恒非 null 显「诊断不可用」而非干净「触发诊断」。修:`diagnoseRun` 加 `persistUnavailable bool`——自动钩子传 false(仅 persist ready),显式端点传 true(用户主动请求,回显结果可留)。[internal/httpapi/run_diagnose.go:98,148]
- [x] [Review][Patch][Major] 自动诊断 goroutine 无并发上限:`runDiagnoseHook` 每个失败 run 起一独立 goroutine(脱离 worker pool 的 4 并发),AI 配置时批量失败 → 无界并发打 LLM + 多份日志/解密 key 同时驻留(NFR-4 内存 + provider 限流风险)。修:加有界信号量(try-acquire,满则跳过 + 记日志,best-effort 语义不变,用户仍可手动诊断)。[internal/run/pool.go:240]

**复核通过(读真代码证伪/已防御):** DiagnosisPanel 仅在 `v-else-if run.status==='failed'` 块内渲染(非无条件,不会对成功 run 出 422)· secret 跨截断边界泄漏不存在(Scrub 先于截断,secret 已 [MASKED] 再切)· chat 错误路径有 LimitReader(4096)+ 8s client 超时(无独立超时担忧化解)· Stop 阻塞被 p.ctx 取消 + Stop 自带 deadline 化解(wg 纳入为有意设计)· confidenceLabel 由后端 normalizeConfidence 保证枚举(TS union 穷尽)· apiKey="" 仅注释瑕疵(Go string 不可变,无实际泄漏)。

**deferred(见 deferred-work.md):** registerRunSecrets 仅对桩假 secret 有效(真实日志凭据登记留 3-3/3-6,生产前必修)· failure_log 明文列(留 3-6 落盘前脱敏)· chat 成功响应体无大小上限(7-1/2-5 既存,跨切面 AI client 加固)· decodeDiagnosis 坏 JSON 静默(nil,nil)+ derr 死守卫(有意容错,观测性瑕疵)· parseDiagnosis 无「提取首尾大括号」容错(质量门控设计,增强项)· 自动 vs 显式诊断对 diagnosis_json 双写竞态(last-writer-wins,均有效;patch1 已减少自动写)· humanizeDiagnoseErr 可回显自身 endpoint host(管理员自有,无 apiKey)· pending 态前端死代码(后端不产 pending,预留流式)· 自动诊断完成后页面需刷新才见(SSE 收尾刷新增强)· 取消的 run 仍渲染诊断面板(取消=failed 的产品语义)。
