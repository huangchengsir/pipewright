# Story 7.3: 成功/失败差异对比(FR-25)

status: ready-for-dev
epic: 7
基线: master(27 story;3-6 source 端点〔go-git 读 tree/blob〕done;7-2 AI 诊断 done;run 模型有 trigger.Commit/Branch)
covers: FR-25 成功/失败 diff —— 对比本次失败运行与「上一次成功运行」的代码/提交差异,作为诊断的关键信号(Open Q#7 命门:diff 是平台相对「粘 ChatGPT」的独有上下文)。
**迁移号:无**(diff 实时算自 git,不落库)

## As a / I want / So that
As a 管理员,I want 一次失败运行能和「上一次成功运行」对比出改了什么(提交/文件差异),So that 我能快速看出「昨天还好今天就挂」是哪个改动引入的。

## Acceptance Criteria
1. **Given** 一次失败运行(有 commit)+ 该项目/分支存在更早的成功运行 **When** 查看差异 **Then** 给出 baseline(上次成功的 commit)→ current(本次 commit)的文件级 diff(路径 + 增删行数 + 状态 added/modified/deleted)。
2. **Given** 无更早成功运行(首次就失败 / 无 baseline)**When** 查看 **Then** 优雅提示「无可对比的成功基线」,不报错。
3. **And** diff 经 go-git 算(复用 3-6 source 的克隆能力);克隆失败 / commit 不可达 → degraded 人读不 500。
4. **And** diff 内容脱敏尽力(路径/diff 不含明文 secret);大 diff 截断(文件数/行数上限防 OOM)。

## ⚠️ 本机真验
- file:// 夹具裸仓库含两个 commit(baseline 改一行、current 再改)→ 造两个 run(baseline=success/current=failed,trigger.Commit 指对应 sha)→ GET diff → 真返回文件级差异。复用 3-6 的 test-only allowInsecure。

## ⛓ 冻结契约
### GET `/api/runs/{id}/diff`(认证,只读)→ 200
```json
{ "available": true, "reason": "",
  "baselineRunId": "...", "baselineCommit": "abc123", "currentCommit": "def456",
  "files": [ { "path":"package.json", "status":"modified", "additions":3, "deletions":1 } ],
  "truncated": false, "summary": "2 文件改动(+5/-2);最可疑:package.json 依赖变更" }
```
- `available=false` + `reason` 当无 baseline 成功运行 / 本次 run 无 commit / 克隆失败(degraded)。**绝不 500。**
- `status` ∈ `added|modified|deleted|renamed`;`files` 超上限(如 200 文件)→ truncated=true;`summary` 人读(可含简单启发式「最可疑」提示)。
- run 不存在 404。baseline 选取:同 project + 同 trigger.Branch 的、created_at 早于本次的、最近一条 status=success 的 run。

> **冻结点:** diff DTO(available/reason/baselineRunId/baselineCommit/currentCommit/files[{path,status,additions,deletions}]/truncated/summary)定死。7-2 诊断可消费此 diff 作上下文(后续增强),7-4 代码浏览可复用 diff 数据,均不改形状。

## 文件切分(独立 worktree 完整 BE+FE)
**后端(Go):**
- `internal/run`(改 append):`LastSuccessfulRun(ctx, projectID, branch string, before time.Time) (*Run, error)`(查 baseline;无→ErrNotFound 或 nil)。
- `internal/ai/diff.go`(新)+`*_test.go`:`RunDiffer`——给两个 (repoURL, commit) 经 go-git 浅取两 commit 的 tree → 算文件级 diff(go-git 的 `object.Tree.Diff` 或 patch)→ 文件状态 + 增删行数 + 截断 + 简单 summary 启发式。复用 3-6/2-5 的克隆能力 + SSRF 收口 + test-only allowInsecure。脱敏尽力。或放 `internal/run`/独立包——你定,**别 import 形成环**。
- `internal/httpapi/run_diff.go`(新)+`_test.go`:`GET /api/runs/{id}/diff` handler——取本次 run(commit)+ LastSuccessfulRun(baseline)+ 项目 repoURL/凭据 → RunDiffer → DTO。无 baseline/克隆失败→available:false degraded。404 映射。
- `router.go`+`main.go`:**仅 append** 挂 diff 路由(auth);装配 RunDiffer(go-git reader,复用 source reader 模式)+ project/vault 取仓库凭据。
**前端(web/):**
- `web/src/api/runs.ts`(改 append):`getRunDiff(id)` → diff DTO。
- `web/src/components/run/SuccessFailDiff.vue`(新):diff 卡——baseline→current commit + 文件列表(status 着色 + ±行数条)+ summary + truncated 提示 + available:false 友好态。
- `web/src/views/RunDetail.vue`(改):**失败态**挂 SuccessFailDiff(在诊断面板附近,作为「证据」补充)。**不动 DiagnosisPanel(7-2)/终端(3-6)/产物(3-4)/部署(4-2)slot 所有权**,只 append 一个 diff 区块。

## Dev Notes 护栏
- **优雅**:无 baseline/克隆失败/无 commit → available:false 人读,绝不 500。大 diff 截断(文件数 + 单文件行数上限)防 OOM。
- 脱敏尽力(diff/路径不含明文 secret);go-git 浅取限体。
- baseline 选取语义明确(同项目+同分支+更早+最近成功)。diff DTO 冻结。
- 参数化 SQL;无 init 副作用;mem ≤100MB。

## 编排者亲验清单
- `export PATH="/Users/huangjiawei/sdk/go/bin:$PATH"`;gofmt/vet/`go test -race ./internal/run/... ./internal/ai/... ./internal/httpapi/...`/build/mem-check。
- 前端 typecheck(symlink node_modules)+ build。
- **真验:file:// 夹具两 commit + baseline成功/current失败 run → GET diff 返回文件级差异;无 baseline→available:false;克隆失败→degraded 不 500;run 不存在 404**;401。
- worktree 内 commit(分支 story/7-3)。
