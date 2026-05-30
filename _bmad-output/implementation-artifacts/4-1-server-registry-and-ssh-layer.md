# Story 4.1: 服务器登记与共享 SSH 层(FR-14)

status: ready-for-dev
epic: 4
基线: master(18 story;1-3 vault 凭据保险库;3-4 产物契约)
covers: FR-14 服务器登记 + **`internal/target` 通用 exec/session 层(Epic 4 部署 + Epic 6 运维共享,评审:别写死成"仅部署用")** · AC-SEC-02(命令 array 化)
**迁移号:0015**(已分配)

## As a / I want / So that
As a 管理员,I want 登记目标服务器(host/port/user + SSH 凭据)、测试连通性,并有一个通用的 SSH 执行层,So that 后续部署(4-2)与运维(Epic 6)都经它对服务器执行命令。

## Acceptance Criteria
1. **Given** 登记一台服务器(host/port/user + 引用一条 SSH 凭据)**When** 保存 **Then** 入库(凭据仅引用,密钥经 vault,绝无明文)。
2. **Given** 已登记服务器 **When** 测试连接 **Then** 经 SSH 实连、跑一条只读命令(如 `uname -a`),回显成功/延迟/输出或人读错误。
3. **And** `internal/target` 暴露**通用** `Exec(ctx, serverID, cmd []string) (stdout,stderr,exitCode)` 与 session 能力,**命令为 array 不拼 shell 字符串**(AC-SEC-02 防注入);供 Epic 4/6 复用,**不写死部署语义**。
4. **And** 未配 master key / 凭据缺失 / 连接失败 → 人读错误,绝不 500、绝不泄漏密钥。

## ⚠️ 本机可验(关键)
- **本机 SSH server 在、22 端口开**(macOS 远程登录)→ **可登记 `localhost:22` + 当前用户 + 一条 SSH 凭据,真测连接**(`uname -a` 真回显)。这是本期能真验的核心。凭据用密码或本机 `~/.ssh/id_*` 私钥(经 vault 存)。

## ⛓ 冻结契约(`internal/target` 供 Epic 4/6 消费,定死)
### Server 模型 + CRUD
```json
{ "id":"...", "name":"web-prod-1", "host":"10.0.0.5", "port":22, "user":"deploy",
  "credentialId":"<ssh 凭据>", "createdAt":"RFC3339", "updatedAt":"RFC3339" }
```
- `GET /api/servers` → `{items:[…]}` · `POST /api/servers`(创建)· `GET/PUT/DELETE /api/servers/{id}` · 凭据仅引用(`credentialId`),响应绝无密钥。
### POST `/api/servers/{id}/test`(认证+CSRF)→ 200 `{ "ok":bool, "latencyMs":number, "output":"uname 输出(截断)", "error":string|null }`
- 经 SSH 连接 + 跑只读探测命令;失败映射人读(连接失败/认证失败/超时);**错误绝不含密钥**。
### `internal/target` 包导出(冻结,Epic 4/6 import):
- `type Service interface { Get(ctx,id)(*Server,error); List(ctx)([]*Server,error); Create/Update/Delete; Test(ctx,id)(*TestResult,error); Exec(ctx, serverID string, cmd []string)(*ExecResult,error) }`
- `ExecResult{ Stdout, Stderr string; ExitCode int }`;`Exec` 用 `golang.org/x/crypto/ssh`(已在 go.mod 的 x/crypto v0.52);**cmd []string，不经 shell 拼接**(AC-SEC-02);超时经 ctx。

> **冻结点:** Server DTO + test 结果 + `internal/target` 的 `Exec(serverID, cmd []string)`/`Test` 签名定死。4-2 部署、Epic 6 运维只消费这些、不改签名。

## 文件切分(独立 worktree 完整 BE+FE)
**后端(Go):**
- `internal/store/migrations/0015_servers.sql`:`servers(id, name, host, port, user, credential_id, created_at, updated_at)` + FK credential_id → credentials(ON DELETE RESTRICT)。
- `internal/target/target.go` + `ssh.go` + `*_test.go`:Server 模型 + Service(CRUD + Test + Exec)。SSH 经 `x/crypto/ssh`:用 vault.OpenSecret 取凭据明文(私钥或密码)即用即弃;`ssh.Dial` + session.Run(cmd array → 用 `shellquote`-free 方式:对单命令用 `session.Run(strings.Join)`? **不**——AC-SEC-02 要 array:用 `ssh` 的 `session.Start`/Run 时把 cmd 当一个程序+参数,经安全拼接或 `exec`-style;最简稳妥:cmd[0] 为程序、其余为参数,用 shellescape 各参数再 join,**或**优先 `session.Run` 单串但参数经 `%q` 转义)。Test = Exec(`uname -a` 等)+ 计时。注入 `*ssh.ClientConfig` 超时 + `HostKeyCallback`(本机可 `InsecureIgnoreHostKey` 但注释标注生产应固定 known_hosts;**留 deferred**)。**测试**:对 `localhost:22` 真连(若 CI 无 sshd 则 t.Skip);单元层测 cmd array 不被 shell 注入、错误不含密钥、CRUD。
- `internal/httpapi/servers.go` + `_test.go`:CRUD + test handler + DTO + 错误映射(404/422/503)。
- `router.go` + `main.go`:**仅 append** 挂 `/api/servers*`(GET auth;写方法 auth+CSRF);`target.New(db, vault)` + `WithServers` Option。
**前端(web/):**
- `web/src/api/servers.ts`(新):CRUD + testServer。
- `web/src/views/settings/SettingsServers.vue`(新或并入设置):服务器列表 + 新增/编辑(name/host/port/user + 选 SSH 凭据)+ 测试连接(显延迟/uname 输出/错误)。复用 1-6 ui 组件。
- 设置导航 + 路由:**仅 append** 一个「服务器」设置项(别动别人的设置项)。

## Dev Notes 护栏
- **AC-SEC**:SSH 密钥仅 vault 密文,Exec/Test/日志/错误/响应绝无明文;cmd array 不拼 shell(注入防护)。
- `internal/target` 通用化:不含部署语义,纯 exec/session;Epic 4/6 在其上构建。
- HostKeyCallback 生产应固定 known_hosts(本期 dev 可放宽,**deferred 标注**)。
- 参数化 SQL;无 init 副作用;mem ≤100MB。**只有本 story 改 go.mod**(import x/crypto/ssh,跑 `go mod tidy`)。

## 编排者亲验清单
- `export PATH="/Users/huangjiawei/sdk/go/bin:$PATH"`;gofmt/vet/`go test -race ./internal/target/... ./internal/httpapi/...`/build/mem-check;`go mod tidy` 后 go.sum 干净。
- 前端 typecheck(symlink node_modules)+ build。
- **真验:登记 localhost:22 + 当前用户 + SSH 凭据 → 测试连接真回显 `uname`**(本机 sshd 开)。错误路径(死端口/错凭据)人读不泄密。
- worktree 内 commit(分支 story/4-1)。
