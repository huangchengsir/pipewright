# Story 3.4: 构建产物与产物契约(FR-6)

status: ready-for-dev
epic: 3
基线: master(18 story;3-1 run-detail DTO 有 Targets/Diagnosis null slot 模式;3-6 StepSink 已扩展)
covers: FR-6 多类型构建产物 —— **产物契约(类型/寻址)在此定死,作为 E3 一等交付物**(Epic 4 部署消费,防反向倒逼改接口)
**迁移号:0014**(已分配,勿用别的)

## As a / I want / So that
As a 管理员,I want 运行成功后看到它产出的构建产物(类型 + 寻址 + 大小),且产物有稳定契约,So that 部署阶段(Epic 4)能按类型寻址消费产物。

## Acceptance Criteria
1. **Given** 一次成功运行 **When** 查看运行详情 **Then** 列出产物:类型(镜像/JAR/dist/archive)+ 名称 + 寻址引用 + 大小,填 run-detail 冻结 `artifacts` slot。
2. **Given** 产物契约 **When** Epic 4 部署消费 **Then** 按 `type` + `reference` 寻址,无需了解构建内部。
3. **And** 无真实构建(3-3 卡 Docker)→ 桩 runner 成功时合成 1~2 个桩产物(诚实标注桩);3-3 落地后真实产物经同一 `EmitArtifact` 接口接入,**契约不变**。

## ⛓ 冻结契约(E3 一等交付物,定死)
### Artifact 模型 + run-detail `artifacts` slot
`GET /api/runs/{id}` 外层 DTO 加 `artifacts` 字段(成功/有产物时为数组,否则 `[]`):
```json
"artifacts": [
  { "id":"...", "type":"image", "name":"shop-api",
    "reference":"registry.example.com/acme/shop-api:a1b2c3d", "sizeBytes":48210432,
    "metadata":{"digest":"sha256:…"}, "createdAt":"RFC3339" }
]
```
- `type` ∈ **`image` | `jar` | `dist` | `archive`**(枚举冻结;后续新增类型只加枚举不改形状)。
- `reference` = **类型寻址**(image: `repo:tag` 或本地 image id;jar: 路径/URL;dist: 目录/tarball 寻址;archive: 路径)。Epic 4 部署按 (type, reference) 消费。
- `metadata` 自由 KV(digest/path 等);`sizeBytes` 整数(未知用 0)。
### GET `/api/runs/{id}/artifacts`(认证,只读)→ `{ "artifacts": [ …同上… ] }`;run 不存在 404 `run_not_found`。

> **冻结点:** Artifact DTO(id/type/name/reference/sizeBytes/metadata/createdAt)+ type 枚举定死。3-3 真实构建只经 `EmitArtifact` 喂、不改形状;4-x 部署只按 (type,reference) 消费。run-detail 外层 DTO 仅新增 `artifacts` slot(仿 Targets/Diagnosis)。

## 文件切分(同一棵树前后端;本 story 在独立 worktree 内完整实现 BE+FE)
**后端(Go):**
- `internal/store/migrations/0014_run_artifacts.sql`:`run_artifacts(id, run_id, type, name, reference, size_bytes, metadata_json, created_at)` + 索引 `(run_id)`。
- `internal/run/run.go`/`service.go`/`runner.go`/`pool.go`:`Artifact` 领域模型;StepSink 加 `EmitArtifact(ctx, Artifact) error`(runner 报告产物);RunService `AddArtifact`/`ListArtifacts(runID)`;dbStepSink.EmitArtifact 落 run_artifacts + (可选)发事件。**StubRunner 成功路径 emit 1~2 桩产物**(如 type=image reference=`local/stub-<proj>:<commit>` + type=dist;metadata 标 `"stub":true`)。
- `internal/httpapi/runs.go` + `run_artifacts.go`(新):run-detail DTO 填 `artifacts` slot(读 ListArtifacts);`GET /api/runs/{id}/artifacts` handler。
- `router.go` + `main.go`:**仅 append** 挂 `/api/runs/{id}/artifacts`(auth)。
**前端(web/):**
- `web/src/api/runs.ts`:run-detail 类型加 `artifacts: ArtifactDTO[]`;`getRunArtifacts(id)`。
- `web/src/components/run/ArtifactList.vue`(新):产物卡列表(type 徽标〔image/jar/dist/archive 各色〕+ name + reference〔可复制〕+ 大小人读)。
- `web/src/views/RunDetail.vue`:**成功态**渲染 ArtifactList(填 3-1 骨架,不扰其它 slot:终端=3-6、诊断=7-2)。

## Dev Notes 护栏
- 产物契约是 E3 一等交付物:type 枚举 + reference 寻址语义定死,Epic 4 据此消费。
- 桩产物诚实标注(metadata.stub=true);参数化 SQL;无 init 副作用;`make mem-check` ≤100MB。
- run-detail 外层只加 slot,不改既有字段(3-1 冻结)。

## 编排者亲验清单
- `export PATH="/Users/huangjiawei/sdk/go/bin:$PATH"`;gofmt/vet/`go test -race ./internal/run/... ./internal/httpapi/...`/`CGO_ENABLED=0 build`/mem-check。
- 前端 `npm --prefix web run typecheck`(symlink 主仓 node_modules 进 worktree:`ln -s /Users/huangjiawei/huangjiawei/devopsTool/web/node_modules web/node_modules`)+ build。
- 单测:成功 run → ListArtifacts 非空;GET artifacts 形态;run-detail 填 slot;404。
- worktree 内 `git add` + commit(分支名 story/3-4)。
