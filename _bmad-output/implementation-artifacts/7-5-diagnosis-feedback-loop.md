# Story 7.5: 诊断反馈闭环(FR-26)

status: ready-for-dev
epic: 7
基线: master(27 story;7-2 AI 诊断 done〔DiagnosisPanel + run-detail diagnosis slot〕)
covers: FR-26 诊断反馈闭环 —— 每条诊断 👍/👎(可附正确根因);反馈持久化、关联运行与诊断,汇入设置页统计(准确率/反馈数/环比),并为知识库种子。
**迁移号:0020**

## As a / I want / So that
As a 管理员,I want 对 AI 诊断点 👍/👎(👎 可附正确根因),并在设置页看到诊断准确率统计,So that 诊断质量被持续度量、错误案例可沉淀(知识库种子),AI 越用越准。

## Acceptance Criteria
1. **Given** 一条 ready 诊断 **When** 点 👍 或 👎(👎 可填正确根因文本)**Then** 反馈持久化,关联该 runId + 诊断快照;同一 run 可改判(覆盖最新反馈)。
2. **Given** 累积反馈 **When** 看设置页「诊断反馈闭环统计」**Then** 展示准确率(👍/总)、反馈总数、环比(可选简单实现:本周 vs 上周或最近 N 条趋势)。
3. **And** 👎 附的正确根因作为知识库种子存储(本期仅持久化 + 统计;检索接入留后续/与 Open Q#7 实验对接)。
4. **And** 未配 AI / 无诊断 → 无反馈入口(优雅);反馈文本脱敏尽力(绝无明文 secret);未认证不可提交。

## ⚠️ 本机真验
- 造失败 run + stub 诊断(7-2 路径)→ 前端点 👍/👎 + 填根因 → POST 反馈 → 持久化;再 GET 统计 → 准确率/计数更新。全本机可验(无需外网)。

## ⛓ 冻结契约
### POST `/api/runs/{id}/diagnosis/feedback`(认证+CSRF)
请求 `{ "verdict":"up"|"down", "correctRootCause":"可选,仅 down 有意义" }` → 200 `{ "ok":true }`
- run 不存在 404;run 无诊断 → 422 `no_diagnosis`;同 run 重复提交 = 覆盖(upsert by runId)。correctRootCause 脱敏 + 长度上限。
### GET `/api/settings/diagnosis-stats`(认证,只读)→ 200
```json
{ "totalFeedback": 42, "thumbsUp": 30, "thumbsDown": 12, "accuracy": 0.714,
  "recentTrend": [ {"period":"...", "accuracy":0.7, "count":10} ],
  "recentCorrections": [ {"runId":"...", "correctRootCause":"...(脱敏)", "at":"RFC3339"} ] }
```
- 无反馈 → 全 0 / 空数组 / accuracy:null(不报错)。recentCorrections 为 👎 附根因的最近若干条(知识库种子可视化)。

> **冻结点:** feedback 请求(verdict/correctRootCause)+ stats DTO(totalFeedback/thumbsUp/thumbsDown/accuracy/recentTrend/recentCorrections)定死。知识库检索接入(后续)只消费这些数据,不改形状。

## 文件切分(独立 worktree 完整 BE+FE)
**后端(Go):**
- `internal/store/migrations/0020_diagnosis_feedback.sql`:`diagnosis_feedback(id, run_id UNIQUE, verdict, correct_root_cause, diagnosis_snapshot, created_at, updated_at)`(run_id 唯一→upsert;FK run_id → pipeline_runs ON DELETE CASCADE)。
- `internal/run`(或新 `internal/feedback`):`Feedback` 模型 + `SaveFeedback(upsert)` / `GetStats()`(准确率/计数/最近趋势/最近 corrections)。**run 包内或独立包你定**;脱敏 correctRootCause(复用 mask 尽力或在 handler 层)。
- `internal/httpapi/diagnosis_feedback.go`(新)+`_test.go`:POST feedback + GET stats handler + DTO。run 无诊断→422;脱敏;upsert。
- `router.go`+`main.go`:**仅 append** 挂 `POST /api/runs/{id}/diagnosis/feedback`(auth+CSRF)+ `GET /api/settings/diagnosis-stats`(auth);装配 feedback 服务(复用 runSvc 或新 svc + vault/mask)。
**前端(web/):**
- `web/src/api/runs.ts`(改 append):`submitDiagnosisFeedback(id, {verdict, correctRootCause})`;`web/src/api/settings.ts` 或既有:`getDiagnosisStats()`。
- `web/src/components/run/DiagnosisPanel.vue`(改):ready 态加 👍/👎 按钮(👎 展开正确根因输入);提交后反馈态(谢谢/已记录)。**只在该组件内 append 反馈 UI,不动诊断渲染主体(7-2 所有权)**。
- `web/src/views/settings/SettingsDiagnosisStats.vue`(新)或并入设置:统计卡(准确率环/计数/趋势 + 最近 corrections 列表)。设置导航 **仅 append** 一项。

## Dev Notes 护栏
- **脱敏**:correctRootCause 经 mask 尽力(绝无明文 secret);长度上限防滥用。
- upsert by run_id(同 run 改判覆盖);run 无诊断→422 不创建空反馈。
- 未配 AI/无诊断→前端无反馈入口(优雅,不报错)。
- 知识库检索本期不做(仅持久化 + 统计 + corrections 可视化;与 Open Q#7 实验数据对接留后续)。
- 参数化 SQL;无 init 副作用;mem ≤100MB。

## 编排者亲验清单
- `export PATH="/Users/huangjiawei/sdk/go/bin:$PATH"`;gofmt/vet/`go test -race ./internal/run/... ./internal/httpapi/...`/build/mem-check。
- 前端 typecheck(symlink node_modules)+ build。
- **真验:失败 run + 诊断 → POST 👍 → GET stats accuracy 更新;POST 👎+根因 → recentCorrections 出现(脱敏);同 run 再 POST→覆盖不新增;run 无诊断→422;裸 DB correctRootCause 无明文 secret**;404/401/403。
- 真浏览器:DiagnosisPanel 👍/👎 + 根因输入 + 反馈态;设置统计页渲染。**截图肉眼审**。
- worktree 内 commit(分支 story/7-5)。
