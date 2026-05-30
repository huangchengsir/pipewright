# Story 6.2: 服务日志查看(实时 + 历史,经 SSH)(FR-16)

status: ready-for-dev
epic: 6
基线: master(21 story;4-1 `internal/target` SSH Exec/session done)
covers: FR-16 日志查看(实时 tail + 历史)—— 经 SSH 在目标服务器取服务日志。复用 4-1 `internal/target`(评审:server registry 通用层)。
**迁移号:无(日志实时取自服务器,不落库)**

## As a / I want / So that
As a 管理员,I want 经界面查看目标服务器上某服务的日志(历史按行数/时间范围回看 + 实时 tail),So that 我排查问题不必 SSH 登录每台机。

## Acceptance Criteria
1. **Given** 一台已登记服务器 + 一个日志源(journald unit / 文件路径 / docker 容器)**When** 查看历史日志 **Then** 经 SSH 跑安全受限的日志命令(如 `journalctl -u <unit> -n <N>` / `tail -n <N> <path>`),返回最近 N 行。
2. **Given** 实时查看 **When** 开启 tail **Then** 经 SSH 流式 `tail -f`/`journalctl -f`,SSE 推送新行(纯黑终端,复用 3-6 RunTerminal 风格)。
3. **And** 日志源/参数**受限校验**(AC-SEC-02:unit 名/路径白名单化或严格校验,**绝不让任意命令注入**);命令 array 化经 `target.Exec`/session。
4. **And** 连接失败/命令失败 → 人读错误,不 500;输出脱敏(若含已登记 secret,复用 mask 尽力)。

## ⚠️ 本机真验
- 4-1 已证 localhost SSH 可用 → 登记 localhost,**真取本机日志**:`tail -n 50 <某可读文件>`(如 /var/log/system.log 或临时造的 log 文件)真回显;实时 tail 一个 `tail -f` 临时文件真流式。journald/docker 源 localhost 无则人读错误(合理)。

## ⛓ 冻结契约
### GET `/api/servers/{id}/logs`(认证,只读;历史)
查询 `?source=<journald|file|docker>&target=<unit名/路径/容器>&lines=<N≤2000>` → 200
```json
{ "serverId":"...", "source":"file", "target":"/var/log/app.log", "lines":[ {"text":"…","ts":null} ], "truncated":false }
```
- `source` ∈ **`journald` | `file` | `docker`**;`target` 经**严格校验**(file: 绝对路径无 `..`/无 shell 元字符;journald unit: `[\w.@-]+`;docker: 容器名 `[\w.-]+`)→ 非法 400 `invalid_log_target`。命令按 source 构造 cmd []string(`journalctl -u X -n N` / `tail -n N path` / `docker logs --tail N X`)。
- 服务器不存在 404;SSH/命令失败 → 200 + 空 lines + `error` 人读(或 502?**作者定:200 + error 字段**,不 500)。
### GET `/api/servers/{id}/logs/stream`(认证,SSE;实时 tail)
同 source/target 校验;SSE `event: logline` `data:{"text":"…"}`;经 SSH session 跑 `tail -f`/`journalctl -f`/`docker logs -f`,逐行推送。客户端断开 → 关 SSH session(不泄漏)。

> **冻结点:** logs DTO + source 枚举 + target 校验规则 + SSE logline 事件定死。6-1 状态/6-3 操作复用 `internal/target`,不改本契约。

## 文件切分(独立 worktree 完整 BE+FE)
**后端(Go):**
- `internal/target`(改,**仅 append 不动既有**):加 `ExecStream(ctx, serverID, cmd []string) (io.ReadCloser, error)`(SSH session 流式 stdout,供实时 tail;ctx 取消关 session)。**或**新 helper;别改既有 Exec/Test/CRUD 签名(4-1 冻结)。
- `internal/httpapi/server_logs.go`(新)+`_test.go`:历史 + SSE stream handler;**source/target 严格校验**(白名单正则);按 source 构造 cmd array;错误人读不 500。
- `router.go`+`main.go`:**仅 append** 挂 `/api/servers/{id}/logs` + `/logs/stream`(auth);复用既有 `targetSvc`(无需新服务,4-1 已装配)。
**前端(web/):**
- `web/src/api/servers.ts`(改):`getServerLogs(id, {source,target,lines})` + SSE 订阅 helper。
- `web/src/components/ops/ServiceLogViewer.vue`(新):source 选择(journald/file/docker)+ target 输入 + 历史拉取 + 实时 tail 开关(纯黑终端,复用 3-6 RunTerminal 风格或抽取共用)。
- 路由 + 入口:服务器详情/运维区加「日志」入口(**仅 append**,别动 4-1 的 SettingsServers CRUD)。

## Dev Notes 护栏
- **AC-SEC-02 核心**:source/target 严格白名单校验,命令 array 化经 target,**绝不让任意 shell 注入**(这是 FR-16 的安全要害);输出尽力脱敏。
- 实时 tail 的 SSH session 客户端断开必关(防泄漏/挂死);SSE 有界。
- 不改 4-1 `internal/target` 既有签名(只 append ExecStream);无 init 副作用;mem ≤100MB。

## 编排者亲验清单
- `export PATH="/Users/huangjiawei/sdk/go/bin:$PATH"`;gofmt/vet/`go test -race ./internal/target/... ./internal/httpapi/...`/build/mem-check。
- 前端 typecheck(symlink node_modules)+ build。
- **真验:登记 localhost → GET logs source=file target=临时 log → 真回显最近 N 行;实时 stream tail -f 临时文件 → SSE 真流式;非法 target(`../`/`;rm`)→ 400;SSH 失败人读不 500**;401。
- worktree 内 commit(分支 story/6-2)。
