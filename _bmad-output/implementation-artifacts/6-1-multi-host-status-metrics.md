# Story 6.1: 多机状态总览(服务器层指标)(FR-15)

status: ready-for-dev
epic: 6
基线: master(24 story;4-1 internal/target 通用 SSH Exec/ExecStream done;6-2 server_logs 已示范 SSH 取数 + 安全校验)
covers: FR-15 多机状态总览(服务器层 CPU/内存/磁盘)—— 经 SSH 轮询取服务器指标。容器/服务层(运行中服务、端口)随 6-3 细化;本期聚焦服务器层资源指标。
**迁移号:无**(指标实时取自服务器不落库)

## As a / I want / So that
As a 管理员,I want 在一个面板看所有已登记服务器的 CPU/内存/磁盘使用,So that 我不必逐台 SSH 登录就能掌握资源状况。

## Acceptance Criteria
1. **Given** 已登记服务器 **When** 查看指标 **Then** 经 SSH 跑只读命令取 CPU 负载 / 内存 used/total / 磁盘 used/total,解析为结构化指标返回。
2. **Given** 多台服务器 **When** 总览 **Then** 可逐台或批量取指标;某台不可达 → 该台标 unreachable + 人读错误,**不连累其它台**(每台独立)。
3. **And** 取数命令**只读、array 化、白名单**(AC-SEC-02:固定命令如 `cat /proc/loadavg`、`free -b`、`df -B1 /`,不接受用户输入拼命令);跨平台尽力(Linux 优先,macOS/不支持→该指标 null + 标注)。
4. **And** SSH/解析失败 → 该台 status=unreachable/error + 人读,不 500;指标无敏感信息。

## ⚠️ 本机真验
- 6-2 已证 localhost SSH 取数 → 登记 localhost → 取指标:`/proc/loadavg`(macOS 无 → loadavg 用 `uptime` 解析或标 null)、`free`/`df`(macOS 无 free → 用 `vm_stat`/`df` 或标 null + 注明"该平台部分指标不可用")。**至少磁盘 `df -B1 /` 跨平台可真回显**,验证管线端到端。

## ⛓ 冻结契约
### GET `/api/servers/{id}/metrics`(认证,只读)→ 200
```json
{ "serverId":"...", "reachable":true, "error":"",
  "cpu": { "loadavg1":0.42, "cores":8 },
  "memory": { "usedBytes":4123456789, "totalBytes":17179869184 },
  "disk": { "path":"/", "usedBytes":..., "totalBytes":... },
  "collectedAt":"RFC3339" }
```
- 不可达/失败 → `reachable:false` + `error` 人读,指标字段为 null(不 500)。各指标解析失败单独 null + 不影响其它。
### GET `/api/servers/metrics`(认证,只读;批量)→ `{ "items":[ {…同上,每台…} ] }`
- 逐台并行取(有界并发)、各自独立;某台失败仅该台 reachable:false。

> **冻结点:** metrics DTO(serverId/reachable/error/cpu/memory/disk/collectedAt)+ 字段形状定死。6-3 服务操作、6-5 异常检测复用 internal/target,不改本契约;容器/服务层指标后续作为新字段 append。

## 文件切分(独立 worktree 完整 BE+FE)
**后端(Go):**
- `internal/target`(改,**仅 append 不动既有签名**):可加 `metrics.go` helper 或留在 httpapi 层——**你定**:用既有 `Exec(serverID, []string)` 跑固定只读命令即可,**无需改 target 接口**(优先不动 4-1 冻结)。
- `internal/httpapi/server_metrics.go`(新)+`_test.go`:`GET /api/servers/{id}/metrics` + `/api/servers/metrics`(批量)。固定白名单命令(`cat /proc/loadavg`/`nproc`、`free -b`、`df -B1 /`)经 target.Exec;解析输出为结构化指标(健壮容错:解析失败该指标 null);跨平台尽力(命令不存在→null + 不报错)。不可达→reachable:false 不 500。批量并行有界。
- `router.go`+`main.go`:**仅 append** 挂两路由(auth,只读);复用既有 targetSvc(4-1 已装配,无需新服务)。
**前端(web/):**
- `web/src/api/servers.ts`(改,append):`getServerMetrics(id)` + `getAllServerMetrics()`。
- `web/src/views/ServerStatus.vue` 或 `web/src/components/ops/MetricsOverview.vue`(新):多机状态总览(每台卡:名称 + reachable 徽标 + CPU 负载/核数 + 内存 used/total 进度条 + 磁盘 used/total 进度条;不可达灰显 + 错误)。复用 1-6 ui(进度条/徽标)。
- 路由 + 入口:**仅 append** 一个「服务器状态」总览页/入口(别动 4-1 SettingsServers CRUD、6-2 日志入口)。

## Dev Notes 护栏
- **AC-SEC-02**:取数命令**固定白名单、不接受任何用户输入拼接**(纯静态命令 array);无注入面。指标无敏感信息。
- 每台独立:某台不可达不连累总览;批量并行有界(信号量,防 N 台同时 SSH 打爆)。
- 不改 4-1 target 冻结签名(优先用既有 Exec);跨平台 best-effort(指标 null 而非报错)。
- 解析健壮(空/格式异常→null);无 init 副作用;mem ≤100MB。

## 编排者亲验清单
- `export PATH="/Users/huangjiawei/sdk/go/bin:$PATH"`;gofmt/vet/`go test -race ./internal/target/... ./internal/httpapi/...`/build/mem-check。
- 前端 typecheck(symlink node_modules)+ build。
- **真验:登记 localhost → GET metrics → 至少磁盘 df 真回显 used/total;不可达服务器(死端口)→ reachable:false 人读不 500;批量端点逐台独立**;401。指标解析容错(某指标命令不存在→null 不崩)。
- worktree 内 commit(分支 story/6-1)。
