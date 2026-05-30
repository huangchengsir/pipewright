# Deferred Work

## Deferred from: code review of story 1-1-project-scaffold (2026-05-29)

- **完整 CSP 策略** — `internal/httpapi/router.go`:仅加了 nosniff/缓存头(若采纳 patch),完整 Content-Security-Policy 留到安全专项 story。
- **配置校验(Addr/DBPath 合法性)** — `internal/config/config.go`:非法监听地址/不可写 DB 路径目前在 ListenAndServe/Open 处晚失败;单管理员低风险,后续加启动期校验。
- **迁移 checksum/缺口/重复版本检测** — `internal/store/store.go`:当前仅 1 条空迁移;待迁移增多后加内容哈希校验 + 顺序/重复检测,防已应用迁移被改动后静默偏移。
- **前端 eslint/prettier 配置文件 + 复原 golangci-lint** — `web/`、CI:被仓库 `config-protection` 钩子拦截无法创建配置文件,`npm run lint`/`format` 暂不可用;依赖已装,留维护者添加配置或临时关钩子。架构"强制项"原列 golangci-lint,现以 gofmt+vet 替代(已披露)。
- **容器运行验证(AC1 另一半)** — `Dockerfile`:本机未装 Docker(用户决定开发期不装);发布镜像前需 `docker build` + `docker run` 验证 `/healthz`。
- **请求体大小限制 + 限流 + chi Timeout 中间件** — `internal/httpapi`:暂无 POST 端点;待出现写接口时加 `middleware.RequestSize`/`Timeout` 与限流。
- **serveIndex 条件请求/ETag** — `internal/httpapi/router.go`:SPA shell 用 `http.ServeContent` 提供 ETag/Last-Modified/Range 的条件 GET。
- **CI 加 `-race`、`on:push` 分支过滤** — `.github/workflows/ci.yml`:待出现并发后加竞态检测;按需收敛触发分支以省成本。
- **Dockerfile `GOSUMDB=off`** — 因本网络 sum 校验端点不可用而临时关闭;go.sum 已提交仍校验完整性,网络恢复后复原 GOSUMDB。

## Deferred from: code review of story-1.2 + story-1.5 (2026-05-29)

- **锁定计数 DB 持久化**(`internal/auth/lockout.go`):锁定计数/截止时间为进程内存态,重启清零可绕过 NFR-5。story Dev Notes 已显式将其延后为可选;自托管单二进制重启成本低,后续持久化到 SQLite(已有表)。
- **`New(webFS, authn ...)` 变参可选依赖**(`internal/httpapi/router.go`):nil 时静默全 401/503,排障难。改为必填构造器 + 测试专用构造器。设计小异味,非阻塞。
- **`Bootstrap` count+insert 非原子**(`internal/auth/service.go`):双实例指向同库并发首启会撞主键致一实例崩退。单二进制场景极不可能,后续 `INSERT OR IGNORE` 消除。

## Deferred from: code review of 1-3/2-1/2-3/3-1/3-2 backend (2026-05-30)

- **worker pool 并发 4 vs SetMaxOpenConns(1)**(`internal/run/pool.go`):真实 runner 每步多次写库,4 worker 争唯一连接 + busy_timeout 5s 易 SQLITE_BUSY。**3-3 真实 runner 落地时**重估(降并发 / 给运行单独写连接 / 串行化写)。当前桩 runner 快,无现网 bug。
- **resolved_target_server_ids 读路径**(`internal/run`):本期只写不读(有意,供 Epic 4)。**Epic 4** 消费时做容错反序列化(空串/坏 JSON→[])。
- **webhook 签名模式 HMAC 构造需真实 Gitee 验证**(`internal/trigger/receiver.go`):secret 既作 key 又入 message,未经真实 Gitee 投递抓包核对,线上签名模式可能对接失败。发布前抓包核对 Gitee 文档算法。
- **密码模式 webhook 密钥弱点**(`internal/trigger/receiver.go`):`X-Gitee-Token`==明文密钥是 Gitee 协议固有(读到一次请求头/日志即得密钥);ConstantTimeCompare 长度短路泄漏长度。文档化推荐签名模式,考虑提供"仅签名模式"开关。
- **globMatch `*` 跨 `/` 过度匹配 + matchBranch 首条命中无优先级**(`internal/trigger/receiver.go`):`release/*` 命中 `release/a/b/c`。明确单段语义或 UX 说明 + 显式映射优先级。
- **webhook_deliveries 去重表无 TTL/清理**:长期运行无界增长。加保留期清理任务。
- **删除项目不取消在跑 run / SSRF 私网策略 / 真实 runner 并发**等见各 story Review Findings 与本轮 patch。

## Deferred from: code review of 1-4/1-6/1-7/2-2/2-4 (2026-05-30, 9-agent 三层对抗)
**1-7 账户/会话:**
- 会话无绝对生命周期上限(仅滑动过期)— 失窃 token 持续使用则永不过期。如需硬上限按 created_at 加 absolute cap。
- SessionID 16-hex(64bit)前缀碰撞未处理(撤销 break-first / 列表同 id)— 概率极低,留观察。
- 并发改密 + 撤销当前会话无原子性(各自独立 DELETE)— 单管理员低危。
- `internal/auth` 缺领域层单测(ChangePassword/会话仅 httpapi 集成覆盖)— 补 session_test.go。
**1-4 审计:**
- audit Masker 在生产路径从未 RegisterSecret → detail 脱敏兜底空跑(当前各 handler 只放元数据,无泄漏;未来 handler 往 detail 放含 secret 字段才触发)。需在那时注册或保持人工纪律。
**2-2/2-4 流水线:**
- 并发保存同项目 = 静默丢更新(无乐观锁/etag)— 单管理员低危;多 tab 需 If-Match。
- maskRegistry 有冒号路径暴露冒号前整段(按 spec「用户名可见」设计;若前段敏感则泄漏)。
- targetServerIds 不去重(留 4-1 服务器存在性校验一起做)。
- 跨 build/env 作用域同名变量静默并存(覆盖语义未定义)。
- 跨 tab 编辑切走保存丢未保存草稿(无 dirty 拦截/提示)。
- registry type 非空但 url 空可存(留 2-6 完整校验 FR-9)。
- job.config 前端 Record<string,string> vs 后端 map[string]any(非字符串值会被强转;当前写者均字符串,latent)。
**1-6 组件库:**
- Toast 无堆叠上限(error 永不自动消失,可撑满视口)。
- ProgressBar value=NaN → "NaN%"/aria-valuenow=NaN(上游 total=0 时)。
- ConfirmDialog 焦点还原到可能已卸载元素 / 焦点陷阱无可聚焦元素兜底。
- AppTooltip 触屏不可达(仅 hover/focus)。
- FormField 单错误(不支持多条校验错误)。

## Deferred from: 2-5 AI 生成流水线配置 (2026-05-30)
- TriggersPanel apply 后不自动刷新(写入后端已成功,但面板自持加载,需切 tab 才见新分支映射)。修:给 TriggersPanel 加 expose({reload}) 经 templateRef 调,或 ProjectPipeline 加 triggersKey ref 触发重渲染。
- 2-5 强 schema 校验交由各服务 Save + 2-6 校验兜底(本期不做)。
