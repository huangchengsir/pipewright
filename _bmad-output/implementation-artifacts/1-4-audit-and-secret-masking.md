# Story 1.4: 审计地基与 secret 脱敏(AC-SEC-03/04)

status: ready-for-dev(等下一波扇出)
epic: 1
独立于:2-2(无依赖,可并行)
covers: NFR-7 审计 · AC-SEC-03(审计不可篡改 + 远端 sink)· AC-SEC-04(secret 脱敏)

## As a / I want / So that
As a 管理员,I want 敏感操作写入 append-only 审计、运行日志落盘前对已登记 secret 脱敏,So that 谁在何时做了什么可追溯且不可篡改,且密钥不泄进日志。

## Acceptance Criteria
1. 敏感操作发生 → 同步写入本地 append-only 审计(专用不可改表)+ 可选远端 sink;本地删本地日志后远端记录仍完整(AC-SEC-03)。
2. 日志含已登记 secret → 落盘输出 `[MASKED]`;测试:job 中 echo secret → 日志为 `[MASKED]`(AC-SEC-04)。
3. 审计日志与应用日志分离存储。
4. AC-SEC-03/04 做成**自动化回归测试**(本地删日志后远端 sink 仍完整 · echo secret → [MASKED])。

## 范围边界(本期做 / 不做)
- ✅ 做:`internal/audit` 审计地基(append-only 表 + Recorder + 可选远端 sink)· `internal/mask` secret 脱敏工具(注册 secret → 行扫描替换 [MASKED],含短串/边界处理)· 把 audit.Record **接入已存在的敏感操作**(凭据增删改、trigger secret reset、项目增删改、手动触发运行;登录可选)· 审计 REST 查询端点 · FE 审计日志时间线视图(填 `SettingsVault.vue` 的 1-4 占位)· AC-SEC-03/04 自动化回归测试。
- ❌ 不做:部署/回滚/进容器 exec 的审计接入(这些操作 = Epic 4/6 尚不存在,**留 TODO + 在其落地 story 接入**)· 运行日志落盘管线本身(=3-6;本期 mask 工具独立可测,3-6 落地时接入其写日志路径,留接入点)。

## ⛓ 冻结契约(供 FE/未来 story 消费)
### 审计写入(领域,内部)
`audit.Recorder.Record(ctx, Entry)`;`Entry{Actor, Action, TargetType, TargetID, Detail map[string]any, IP}`。
- `Action` 枚举(snake):`credential_create|credential_update|credential_delete|trigger_secret_reset|project_create|project_update|project_delete|run_trigger_manual`(后续按需扩,**只增不改语义**)。
- **Detail 绝不含明文 secret/密文/master key**(写入前过 mask)。
### 审计查询 REST
`GET /api/audit?limit=&before=&action=&targetType=`(认证保护)→ 200
```json
{ "entries": [ { "id":"...", "timestamp":"RFC3339", "actor":"admin", "action":"credential_create",
  "targetType":"credential", "targetId":"...", "detail":{...}, "ip":"..." } ], "nextBefore": "..."|null }
```
- 分页:`before`=游标(上一页最后一条 timestamp/id),`nextBefore` 为 null 表示到底。append-only,**只读**,无 PATCH/DELETE 端点。
### secret 脱敏工具(独立可测)
`mask.NewMasker()` → `RegisterSecret(s string)` + `Scrub(line string) string`(把所有已登记 secret 子串替换为 `[MASKED]`;空/过短 secret 不登记避免误伤;多 secret 多次出现全替换)。

## 文件切分(零冲突,前后端并行;本 story 自成一对 BE+FE)
**后端(只动 Go):**
- `internal/store/migrations/0009_audit.sql`:`audit_log` 表(id·timestamp·actor·action·target_type·target_id·detail_json·ip·created_at)。**append-only**:应用层只 INSERT/SELECT;可加 SQLite trigger 阻止 UPDATE/DELETE(`BEFORE UPDATE/DELETE ... RAISE(ABORT)`)硬化不可篡改。
- `internal/audit/audit.go` + `audit_test.go`:Recorder 接口 + 本地 DB 实现 + 可选远端 sink(env 配 `DEVOPSTOOL_AUDIT_SINK`,如本地第二文件路径或 HTTP append;sink 失败不阻断主操作,但记录降级)。AC-SEC-03 测试:写 N 条 → 删本地表/文件 → 远端 sink 仍有 N 条。
- `internal/mask/mask.go` + `mask_test.go`:Masker(RegisterSecret/Scrub)。AC-SEC-04 测试:注册 secret,Scrub("echo "+secret) → 含 `[MASKED]` 不含明文;短串不误伤;多次出现全替换。
- `internal/httpapi/audit.go` + `audit_test.go`:`GET /api/audit` 列表(认证 + 分页 + 过滤)。
- 接入点(改既有 handler,**仅在写成功后追加一行 Record 调用**,不改其逻辑):`httpapi/credentials.go`、`httpapi/triggers.go`(reset)、`httpapi/projects.go`、`httpapi/webhooks.go`(manual run)。**注意:这些文件 2-2 后端不碰,但 router.go/main.go 漏斗 2-2 会改 → 本 story 走独立 worktree,集成时我合并 router/main。**
- `router.go`(改):挂 `GET /api/audit`;`main.go`(改):构造 `audit.New(db, sink)` + `mask` + 注入需要审计的 handler 依赖。
**前端(只动 `web/`):**
- `web/src/api/audit.ts`(新):`listAudit(params)`。
- `web/src/views/settings/SettingsVault.vue`(改):把「审计日志 — 将在 Story 1.4 实现」占位换成**真实审计时间线**(参考 `mockups/mock-settings-vault.html` 的审计时间线:actor/action/target/time,级别着色,分页加载更多)。
- 或抽 `web/src/components/AuditTimeline.vue` 复用。路由无需新增(嵌在保险库设置内)。

## Dev Notes 护栏
- **不抬空载内存**:audit/mask 包无 init 副作用、无包级重对象。`make mem-check` ≤100MB。
- **append-only 真不可改**:优先 SQLite trigger 硬拦 UPDATE/DELETE(自动化测试断言 UPDATE 被 ABORT)。
- **Detail 先 mask 再落库**:任何写审计前对 detail 值过 Masker,防把密钥写进 detail_json。
- **sink 失败不阻断**:远端 sink 不可达只降级记录,绝不让审计失败阻断核心操作(但要可观测)。
- **接入只追加不改逻辑**:接入既有 handler 时,审计调用放在业务成功之后,失败不回滚业务。

## 编排者亲验清单
- gofmt/vet/`go test ./...`/`-race ./internal/audit ./internal/mask ./internal/httpapi`/`CGO_ENABLED=0 build`/`make mem-check`。
- **AC-SEC-03/04 回归测试必须真跑过**(删本地 sink 后远端完整 · echo secret→[MASKED])。
- 真二进制:做一次改凭据 → `GET /api/audit` 见 `credential_create` 一行,detail 无明文 · DB `audit_log` UPDATE 被 trigger 拒。
- 真浏览器:保险库设置页审计时间线渲染真实条目(非占位)+ 截图审 UI。
