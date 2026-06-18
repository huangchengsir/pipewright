// Package httpapi is the single outward-facing surface of the platform.
// Domain packages never touch HTTP directly; they are wired in here.
package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/huangchengsir/pipewright/internal/ai"
	"github.com/huangchengsir/pipewright/internal/anomaly"
	"github.com/huangchengsir/pipewright/internal/approval"
	"github.com/huangchengsir/pipewright/internal/artifactstore"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/chain"
	"github.com/huangchengsir/pipewright/internal/cron"
	"github.com/huangchengsir/pipewright/internal/deploy"
	"github.com/huangchengsir/pipewright/internal/dnsprovider"
	"github.com/huangchengsir/pipewright/internal/environments"
	"github.com/huangchengsir/pipewright/internal/i18n"
	"github.com/huangchengsir/pipewright/internal/library"
	"github.com/huangchengsir/pipewright/internal/metrics"
	"github.com/huangchengsir/pipewright/internal/notify"
	"github.com/huangchengsir/pipewright/internal/oauth"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/promotion"
	"github.com/huangchengsir/pipewright/internal/proxy"
	"github.com/huangchengsir/pipewright/internal/retention"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/runner"
	"github.com/huangchengsir/pipewright/internal/target"
	"github.com/huangchengsir/pipewright/internal/trigger"
	"github.com/huangchengsir/pipewright/internal/vault"
	"github.com/huangchengsir/pipewright/internal/version"
)

const (
	cookieSession = "pipewright_session"
	cookieCsrf    = "pipewright_csrf"
	headerCsrf    = "X-CSRF-Token"
)

// contextKey 用于 context 值键,避免冲突。
type contextKey int

const contextKeySession contextKey = 1

// Option 配置 New 构建的路由(如注入凭据保险库)。
type Option func(*options)

type options struct {
	vault            vault.Vault
	projects         project.Service
	triggers         trigger.Service
	cron             cron.Service
	chain            chain.Service
	concurrency      run.ConcurrencyService
	parameters       run.ParameterService
	pipelines        pipeline.Service
	pipelineSettings pipeline.SettingsService
	runs             run.Service
	runSub           runSubscriber
	feedback         run.FeedbackService
	secretSource     *RunSecretSource
	receiver         *trigger.Receiver
	audit            audit.Recorder
	account          accountService
	aiSettings       ai.Service
	aiAnalyzer       ai.RepoAnalyzer
	runDiffer        ai.RunDiffer
	sourceReader     SourceReader
	refsLister       RefsLister
	runnerConfig     runner.Service
	servers          target.Service
	notifications    notify.Service
	deployer         deploy.Service
	anomaly          anomaly.Service
	anomalyInterval  int // 后台定时检测间隔(秒);0=已关闭。供前端显示节奏。
	anomalyCooldown  int // 同条件告警去重冷却窗口(秒)。
	metricsHistory   metrics.Service
	oauth            oauth.Service
	approvalCoord    *approval.Coordinator
	approvalStore    *approval.Store
	approvalSigner   *approval.Signer
	promotionStore   *promotion.Store
	environments     *environments.Service
	doraMetrics      run.MetricsService
	templates        library.TemplateService
	varGroups        library.VarGroupService
	customNodes      library.CustomNodeService
	artifactStore    *artifactstore.Store
	retention        *retention.Service
	proxy            proxy.Service
	dnsProviders     dnsprovider.Service
	previewEnvs      PreviewService
}

// WithArtifactStore 注入制品库(Story 8-16):挂载产物下载端点
// GET /api/runs/{id}/artifacts/{artifactId}/download(按 reference 句柄取真字节)。nil → 端点 503。
func WithArtifactStore(s *artifactstore.Store) Option {
	return func(o *options) { o.artifactStore = s }
}

// WithApprovals 注入审批门协调器 + 持久层(Story 8-4),挂载 /api/runs/{id}/{approve,reject,approvals} 路由。
// 不传则审批端点返回 503。
func WithApprovals(coord *approval.Coordinator, store *approval.Store) Option {
	return func(o *options) {
		o.approvalCoord = coord
		o.approvalStore = store
	}
}

// WithApprovalLinks 注入审批链接签名器(「从通知直接审批」),挂载**公开**端点(豁免 requireAuth +
// CSRF,token 即认证):GET /approvals(确认页,无副作用)+ POST /approvals/act(解析门)。
// signer 为 nil / 禁用(无 master key)时,公开页提示「链接不可用」、不解析任何门。
func WithApprovalLinks(signer *approval.Signer) Option {
	return func(o *options) { o.approvalSigner = signer }
}

// WithPromotion 注入环境晋级流持久层(Story 8-7),挂载环境链配置 / 晋级触发 / 晋级历史路由。
// 晋级审批门复用既有 WithApprovals 装配的协调器 + 持久层(approve/reject 端点共用)。
// 不传则晋级端点返回 503。
func WithPromotion(store *promotion.Store) Option {
	return func(o *options) { o.promotionStore = store }
}

// WithEnvironments 注入「环境一等公民」只读聚合服务(对标 GitLab environments),挂载
// 按环境部署历史时间线 + 单环境历史 + 一键回滚路由。聚合纯查询既有表(零迁移);回滚执行复用
// 既有 deploy.Service(经 WithDeploy 注入)。GET 过 auth;rollback 为写方法,过 auth + CSRF + 审计。
// 不传则相关端点返回 503(服务未初始化);未注入 deploy 时回滚端点 503。
func WithEnvironments(s *environments.Service) Option {
	return func(o *options) { o.environments = s }
}

// WithDoraMetrics 注入 DORA 指标只读聚合服务(FR-8-15),挂载 GET /api/metrics/dora 路由
// (认证 + 只读;projectId 可选过滤 + window 时间窗口)。对既有运行数据做纯聚合,无新表、无副作用。
// 不传则该端点返回 503(服务未初始化)。
func WithDoraMetrics(s run.MetricsService) Option {
	return func(o *options) { o.doraMetrics = s }
}

// WithTemplates 注入流水线模板服务(FR-8-13 复用基座),挂载 /api/templates* 与
// POST /api/projects/{id}/pipeline/apply-template 路由。GET 过 auth;写方法过 auth + CSRF + 审计。
// 不传则相关端点返回 503(服务未初始化)。
func WithTemplates(s library.TemplateService) Option {
	return func(o *options) { o.templates = s }
}

// WithVariableGroups 注入变量组服务(FR-8-13 复用基座),挂载 /api/variable-groups* 路由。
// GET 过 auth;POST/PUT/DELETE 过 auth + CSRF + 审计。secret 变量只存/回 credentialId + 掩码,绝无明文。
// 不传则相关端点返回 503(服务未初始化)。
func WithVariableGroups(s library.VarGroupService) Option {
	return func(o *options) { o.varGroups = s }
}

// WithCustomNodes 注入自定义节点服务(复用库 Tier 2),挂载 /api/custom-nodes* 路由。
// GET 过 auth;POST/PUT/DELETE 过 auth + CSRF + 审计。config 为自由 KV 快照,不含明文 secret。
// 不传则相关端点返回 503(服务未初始化)。
func WithCustomNodes(s library.CustomNodeService) Option {
	return func(o *options) { o.customNodes = s }
}

// WithVault 注入凭据保险库,挂载 /api/credentials* 路由。
// 不传则保险库相关端点不可用(返回 vault_unconfigured)。
func WithVault(v vault.Vault) Option {
	return func(o *options) { o.vault = v }
}

// WithProjects 注入项目服务,挂载 /api/projects* 路由。
// 不传则项目相关端点返回 503(服务未初始化)。
func WithProjects(s project.Service) Option {
	return func(o *options) { o.projects = s }
}

// WithCron 注入定时触发配置服务,挂载 /api/projects/{id}/cron 路由(Story 8-6)。
func WithCron(s cron.Service) Option {
	return func(o *options) { o.cron = s }
}

// WithChain 注入流水线串联配置服务,挂载 /api/projects/{id}/chain 路由(FR-8-11)。
// 不传则该端点返回 503(服务未初始化)。GET 过 auth;PUT 过 auth + CSRF。
func WithChain(s chain.Service) Option {
	return func(o *options) { o.chain = s }
}

// WithConcurrency 注入项目级并发上限配置服务,挂载 /api/projects/{id}/concurrency 路由(FR-8-10)。
// 不传则该端点返回 503(服务未初始化)。GET 过 auth;PUT 过 auth + CSRF。
func WithConcurrency(s run.ConcurrencyService) Option {
	return func(o *options) { o.concurrency = s }
}

// WithParameters 注入项目级类型化运行参数定义服务,挂载 /api/projects/{id}/parameters 路由(P0 typed params)。
// 不传则该端点返回 503;手动触发亦据此校验提供的参数(无定义则透传,向后兼容)。
func WithParameters(s run.ParameterService) Option {
	return func(o *options) { o.parameters = s }
}

// WithTriggers 注入触发配置服务,挂载 /api/projects/{id}/trigger* 路由。
// 不传则触发相关端点返回 503(服务未初始化)。
func WithTriggers(s trigger.Service) Option {
	return func(o *options) { o.triggers = s }
}

// WithPipelines 注入流水线配置服务,挂载 /api/projects/{id}/pipeline 路由(Story 2.2)。
// 不传则流水线相关端点返回 503(服务未初始化)。
func WithPipelines(s pipeline.Service) Option {
	return func(o *options) { o.pipelines = s }
}

// WithPipelineSettings 注入构建/部署配置服务,挂载 /api/projects/{id}/pipeline/settings 路由(Story 2.4)。
// 独立于 2-2 的 pipeline spec;不传则该端点返回 503(服务未初始化)。
func WithPipelineSettings(s pipeline.SettingsService) Option {
	return func(o *options) { o.pipelineSettings = s }
}

// WithRuns 注入运行服务与事件订阅源(worker pool),挂载 /api/runs* 路由(含 SSE)。
// sub 用于 SSE 订阅运行状态/步骤事件(不轮询 DB);为 nil 时 events 端点返回 503。
// 不传则运行相关端点返回 503(服务未初始化)。
func WithRuns(s run.Service, sub runSubscriber) Option {
	return func(o *options) {
		o.runs = s
		o.runSub = sub
	}
}

// WithDiagnosisFeedback 注入诊断反馈服务(Story 7.5;FR-26),挂载
// POST /api/runs/{id}/diagnosis/feedback(认证 + CSRF)+ GET /api/settings/diagnosis-stats(认证)。
// 对 ready 诊断 👍/👎(👎 可附正确根因,脱敏 + 长度上限);反馈 upsert by runID、汇入统计(知识库种子)。
// run 无诊断 → 422 no_diagnosis;不传则相关端点返回 503(服务未初始化)。
func WithDiagnosisFeedback(s run.FeedbackService) Option {
	return func(o *options) { o.feedback = s }
}

// WithSecretSource 注入 run 凭据解析器(红线修):诊断/反馈端点据此构建登记了该 run 真实凭据的
// Masker,出网/落库前脱敏(项目/registry/secret 变量明文 → [MASKED])。不传则降级为只登记桩 secret。
func WithSecretSource(s *RunSecretSource) Option {
	return func(o *options) { o.secretSource = s }
}

// WithWebhooks 注入 webhook 接收器(Story 3.2),挂载**公开**端点 POST /api/webhooks/{token}
// (在 requireAuth/CSRF 白名单外;靠签名校验)。不传则该端点返回 503。
func WithWebhooks(rc *trigger.Receiver) Option {
	return func(o *options) { o.receiver = rc }
}

// WithAudit 注入审计 Recorder(Story 1.4),挂载 GET /api/audit 查询端点,并令既有
// 敏感操作 handler 在业务成功后追加审计行。不传则 /api/audit 返回 503,且写操作不审计。
func WithAudit(rec audit.Recorder) Option {
	return func(o *options) { o.audit = rec }
}

// WithAccount 注入账户设置服务(Story 1.7),挂载 /api/account/* 路由
// (改口令 / 会话列表 / 撤销会话)。不传则相关端点返回 503。
func WithAccount(s accountService) Option {
	return func(o *options) { o.account = s }
}

// WithAISettings 注入可配置 AI 提供商服务(Story 7.1),挂载 /api/settings/ai* 路由
// (GET auth;PUT/test auth + CSRF)。不传则相关端点返回 503;AI 未配置/未启用时
// 下游优雅禁用,核心 CI/CD 完全不依赖此服务(NFR-10)。
func WithAISettings(s ai.Service) Option {
	return func(o *options) { o.aiSettings = s }
}

// WithAIGenerate 注入仓库分析器(go-git 浅克隆),挂载 /api/projects/{id}/pipeline/ai-generate
// 与 ai-apply 路由(Story 2.5)。generate 复用已注入的 ai.Service + projects + vault;
// apply 复用 pipelines + pipelineSettings + triggers。analyzer 为 nil 时 generate 返回 503。
func WithAIGenerate(a ai.RepoAnalyzer) Option {
	return func(o *options) { o.aiAnalyzer = a }
}

// WithRunDiff 注入成功/失败差异计算器(go-git 克隆 + tree diff),挂载 GET /api/runs/{id}/diff
// 路由(Story 7.3;FR-25)。复用已注入的 runs(取本次 run + LastSuccessfulRun)+ projects + vault
// (取 repoURL/凭据)。不传则 diff 端点返回 503(服务未初始化)。
func WithRunDiff(d ai.RunDiffer) Option {
	return func(o *options) { o.runDiffer = d }
}

// WithSource 注入只读源码读取器(go-git 浅克隆),挂载 /api/projects/{id}/source/{tree,blob}
// 路由(Story 3.6;FR-4 预埋,7-4 前端消费)。复用已注入的 projects + vault 取仓库凭据。
// 不传则 source 端点返回 503(服务未初始化)。
func WithSource(reader SourceReader) Option {
	return func(o *options) { o.sourceReader = reader }
}

// WithRefs 注入「列仓库分支/tag」能力(代码管理区 repocache;Story 8-18)。nil → 端点 503。
func WithRefs(lister RefsLister) Option {
	return func(o *options) { o.refsLister = lister }
}

// WithRunnerConfig 注入项目「远程构建 runner」配置服务(FR-8-14 续)。nil → 端点 503。
func WithRunnerConfig(svc runner.Service) Option {
	return func(o *options) { o.runnerConfig = svc }
}

// WithServers 注入目标服务器服务(Story 4.1;internal/target 通用 SSH 层),挂载
// /api/servers* 路由(GET auth;写方法 + test auth + CSRF)。SSH 私钥/口令仅经 vault
// 密文,响应/错误绝无明文。不传则相关端点返回 503(服务未初始化)。
func WithServers(s target.Service) Option {
	return func(o *options) { o.servers = s }
}

// WithDeploy 注入部署执行服务(Story 4.2;internal/deploy,FR-10),挂载
// POST /api/runs/{id}/deploy 路由(认证 + CSRF;本期同步执行返回最终 targets)。
// 经 target.Exec 把产物按 type 部署到目标机(命令 array 化,AC-SEC-02);输出/message 绝无明文密钥。
// 不传则该端点返回 503(服务未初始化)。
func WithDeploy(s deploy.Service) Option {
	return func(o *options) { o.deployer = s }
}

// WithNotifications 注入通知渠道服务(Story 5.1;FR-19),挂载 /api/notifications/channels* 路由
// (GET auth;POST/PUT/DELETE/test auth + CSRF)。敏感字段(SMTP 密码)经 vault 加密入库、响应仅
// hasPassword。不传则相关端点返回 503(服务未初始化)。
func WithNotifications(s notify.Service) Option {
	return func(o *options) { o.notifications = s }
}

// WithRetention 注入运行数据保留服务,挂载 /api/retention/config 路由(GET auth;PUT auth + CSRF)。
// 不传则相关端点返回 503。
func WithRetention(s *retention.Service) Option {
	return func(o *options) { o.retention = s }
}

// WithProxy 注入自动 HTTPS + 域名反向代理服务(R1),挂载 /api/proxy/routes* 路由
// (GET auth;POST/DELETE auth + CSRF + 审计)。经已注入的 target.Service 在目标主机上编排 Caddy。
// 不传则相关端点返回 503(服务未初始化)。
func WithProxy(s proxy.Service) Option {
	return func(o *options) { o.proxy = s }
}

// WithDNSProviders 注入 DNS 提供商集成层服务(R3 E3.1–E3.4),挂载 /api/dns/providers* 与
// POST /api/proxy/subdomains 路由(GET auth;POST/DELETE/verify auth + CSRF + 审计)。
// DNS token 经已注入的 vault 加密入库、apply 时即用即弃,响应/审计/日志绝无明文 token。
// 不传则相关端点返回 503(服务未初始化)。
func WithDNSProviders(s dnsprovider.Service) Option {
	return func(o *options) { o.dnsProviders = s }
}

// WithPreviewEnvs 注入 Per-PR 预览环境服务(R4 E4.1),挂载 /api/preview-envs* 与
// /api/projects/{id}/preview-config 路由(GET auth;reclaim/config-PUT auth + CSRF + 审计)。
// 不传则相关端点返回 503(服务未初始化)。
func WithPreviewEnvs(s PreviewService) Option {
	return func(o *options) { o.previewEnvs = s }
}

// WithAnomaly 注入可配置异常检测服务(Story 6.5;FR-23),挂载 /api/anomaly/* 路由
// (GET rules/alerts auth;POST rules/check + DELETE rules/{id} auth + CSRF)。检测复用 6-1
// 指标采集(metricsCollector 适配 collectServerMetrics);不可达/指标 null 的服务器跳过(不误报)。
// 不传则相关端点返回 503(服务未初始化)。
func WithAnomaly(s anomaly.Service) Option {
	return func(o *options) { o.anomaly = s }
}

// WithAnomalyConfig 注入后台定时检测的节奏参数(秒),经 GET /api/anomaly/config 暴露给前端显示
// 「每 N 秒自动检测一次」。intervalSec=0 表示定时检测已关闭(仅手动)。
func WithAnomalyConfig(intervalSec, cooldownSec int) Option {
	return func(o *options) {
		o.anomalyInterval = intervalSec
		o.anomalyCooldown = cooldownSec
	}
}

// WithMetricsHistory 注入服务器指标时序历史服务,挂载 GET /api/metrics/history(auth):
// 供异常检测页画 CPU/内存/磁盘随时间走势折线。不传则该端点返回 503。
func WithMetricsHistory(s metrics.Service) Option {
	return func(o *options) { o.metricsHistory = s }
}

// WithOAuth 注入多 provider OAuth 凭据接入服务,挂载 /api/oauth/* 路由:
//   - GET  /api/oauth/apps(auth):列已配 OAuth 应用(secret 掩码)。
//   - PUT  /api/oauth/apps/{provider}(auth+CSRF):upsert 应用配置(clientSecret 只写)。
//   - GET  /api/oauth/{provider}/authorize(auth):生成 state → 302 跳 provider 授权页。
//   - GET  /api/oauth/{provider}/callback(auth;豁免 CSRF——GET 回跳,靠 state 校验):
//     验 state → Exchange → 把 access_token 经 vault.Create 存成 git_token 凭据 → 302 跳回。
//
// 复用已注入的 vault(o.vault)存 token 凭据。client_secret/access_token 经 vault 密文,
// 响应/错误/日志绝无明文。不传则相关端点返回 503。
func WithOAuth(s oauth.Service) Option {
	return func(o *options) { o.oauth = s }
}

// New 构建 HTTP 处理器:健康端点 + Auth API + (可选)凭据 API + 内嵌 SPA 静态托管。
// webFS 是已嵌入的前端构建产物(根为 web/dist)。
// authn 可选;为 nil 时认证中间件一律返回 401(仅便于测试静态服务)。
// 额外配置(如凭据保险库)经 Option 注入,见 WithVault。
func New(webFS fs.FS, authn auth.Authenticator, opts ...Option) http.Handler {
	var o options
	for _, opt := range opts {
		opt(&o)
	}

	// 更新检查器:进程级单例,内含 TTL 缓存(跨请求复用,避免反复点击打爆 GitHub 限流)。
	updateChecker := version.NewChecker()
	// 自更新串行闸:同一时刻仅允许一个更新进行。
	updateInflight := newUpdateGate()

	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	// 解析请求 locale 并包装 writer,使 writeError 能按 UI 语言本地化错误消息
	// (包装器转发 Flush/Hijack/Unwrap,不影响 SSE 日志流与 WS 终端)。
	r.Use(localeMiddleware)

	r.Get("/healthz", handleHealthz)
	// /version 公开只读:暴露构建版本元数据,供升级检查与运维探针使用(非敏感)。
	r.Get("/version", handleVersion)

	svc := authn

	// Auth 端点:登录/登出/会话探测(见冻结契约)。
	r.Route("/api/auth", func(ar chi.Router) {
		ar.Post("/login", makeLoginHandler(svc))
		ar.Get("/session", makeSessionHandler(svc))
		// 登出属写操作:先认证后 CSRF。
		ar.Post("/logout", func(w http.ResponseWriter, r *http.Request) {
			requireAuth(svc, requireCSRF(makeLogoutHandler(svc))).ServeHTTP(w, r)
		})
	})

	// Webhook 接收(公开入口,Story 3.2):豁免 requireAuth + CSRF,靠签名校验。
	// 必须在受保护 /api 组之外注册;限请求体大小在 handler 内(MaxBytesReader)。
	r.Post("/api/webhooks/{token}", makeWebhookHandler(o.receiver))

	// 从通知直接审批(公开入口,SECURITY-SENSITIVE):豁免 requireAuth + CSRF——token(HMAC+过期+
	// 常量时间比较)即认证。GET 仅渲染确认页(无副作用,防 IM 预取自动放行);POST 才解析门。
	// signer 为 nil/禁用时页面提示不可用、不解析任何门。
	r.Get("/approvals", makeApprovalPageHandler(o.approvalSigner, o.approvalCoord))
	r.Post("/approvals/act", makeApprovalActHandler(o.approvalSigner, o.approvalCoord, o.audit))

	// 受保护 API 端点(除 /api/auth 与 /api/webhooks 外):requireAuth + 写方法 requireCSRF。
	r.Route("/api", func(ar chi.Router) {
		ar.Use(func(next http.Handler) http.Handler {
			return requireAuth(svc, next)
		})
		ar.Use(func(next http.Handler) http.Handler {
			return requireCSRF(next)
		})

		// 审计 Recorder(Story 1.4):传给既有敏感操作 handler,业务成功后追加审计行。
		// 为 nil 时 handler 跳过审计(不阻断业务)。
		aud := o.audit

		// 检查更新:鉴权只读,查 GitHub 最新发布并与当前版本比对(GET 免 CSRF)。
		ar.Get("/version/check", makeCheckUpdateHandler(updateChecker))
		// 一键自动更新:鉴权 + CSRF(写操作);binary 自替换+重启,docker 返回升级命令。
		ar.Post("/version/update", makeSelfUpdateHandler(updateChecker, updateInflight))

		// 审计查询(Story 1.4):只读 + 认证保护 + 分页 + 过滤。
		ar.Get("/audit", makeListAuditHandler(aud))

		// 凭据保险库(Story 1.3)。v 为 nil 时 handler 返回 vault_unconfigured。
		v := o.vault
		ar.Get("/credentials", makeListCredentialsHandler(v))
		ar.Post("/credentials", makeCreateCredentialHandler(v, aud))
		ar.Patch("/credentials/{id}", makeUpdateCredentialHandler(v, aud))
		ar.Delete("/credentials/{id}", makeDeleteCredentialHandler(v, aud))
		// 查看明文(POST + auth + CSRF;每次留 credential_reveal 审计)。
		ar.Post("/credentials/{id}/reveal", makeRevealCredentialHandler(v, aud))

		// 项目接入与列表(Story 2.1)。p 为 nil 时 handler 返回 503。
		// test-clone 须在 {id} 路由之前注册,否则被 /projects/{id} 吞掉。
		p := o.projects
		ar.Get("/projects", makeListProjectsHandler(p))
		ar.Post("/projects", makeCreateProjectHandler(p, aud))
		ar.Post("/projects/test-clone", makeTestCloneHandler(p))
		ar.Patch("/projects/{id}", makeUpdateProjectHandler(p, aud))
		ar.Delete("/projects/{id}", makeDeleteProjectHandler(p, aud))

		// 触发设置与分支映射(Story 2.3)。t 为 nil 时 handler 返回 503。
		// secret/reset 须在通用 trigger 路由之外单独注册;均过 auth + 写方法 CSRF。
		t := o.triggers
		ar.Get("/projects/{id}/trigger", makeGetTriggerHandler(t))
		ar.Put("/projects/{id}/trigger", makeSaveTriggerHandler(t))
		ar.Post("/projects/{id}/trigger/secret/reset", makeResetTriggerSecretHandler(t, aud))

		// 定时(cron)触发配置(Story 8-6)。o.cron 为 nil 时 handler 返回 503。GET 过 auth;PUT 过 auth + CSRF。
		ar.Get("/projects/{id}/cron", makeGetCronHandler(o.cron))
		ar.Put("/projects/{id}/cron", makeSaveCronHandler(o.cron))

		// 项目级并发上限(FR-8-10)。o.concurrency 为 nil 时 handler 返回 503。GET 过 auth;PUT 过 auth + CSRF。
		ar.Get("/projects/{id}/concurrency", makeGetConcurrencyHandler(o.concurrency))
		ar.Put("/projects/{id}/concurrency", makeSaveConcurrencyHandler(o.concurrency))

		// 项目级类型化运行参数定义(P0 typed params)。o.parameters 为 nil → handler 返回 503。GET 过 auth;PUT 过 auth + CSRF。
		ar.Get("/projects/{id}/parameters", makeGetParametersHandler(o.parameters))
		ar.Put("/projects/{id}/parameters", makeSaveParametersHandler(o.parameters))

		// 流水线串联(FR-8-11):上游成功后触发下游流水线。o.chain 为 nil → handler 返回 503。
		// GET 过 auth;PUT 为写方法,过 auth + CSRF。
		ar.Get("/projects/{id}/chain", makeGetChainHandler(o.chain))
		ar.Put("/projects/{id}/chain", makeSaveChainHandler(o.chain))

		// 环境晋级流配置(Story 8-7 / FR-8-7):有序环境链 + 逐环境作用域变量/密钥。
		// /environments、/promotions 比 /projects/{id} 多一段,不会被吞。
		// GET 过 auth;PUT 为写方法,过 auth + CSRF。o.promotionStore 为 nil → 503。
		ar.Get("/projects/{id}/environments", makeGetEnvironmentsHandler(o.promotionStore))
		ar.Put("/projects/{id}/environments", makeSaveEnvironmentsHandler(o.promotionStore, aud))
		ar.Get("/projects/{id}/promotions", makeListProjectPromotionsHandler(o.promotionStore))

		// 环境一等公民(对标 GitLab environments):按环境聚合部署历史 + 一键回滚到上一次成功部署。
		// 比 /projects/{id}/environments 多一段(/deployments、/{env}/...),chi 精确匹配不会被吞。
		// 纯查询既有表(零迁移);回滚执行复用 o.deployer(经 WithDeploy 注入)。
		// GET 过 auth;rollback 为写方法,过 auth + CSRF + 审计。o.environments 为 nil → 503。
		ar.Get("/projects/{id}/environments/deployments", makeListEnvironmentDeploymentsHandler(o.environments))
		ar.Get("/projects/{id}/environments/{env}/history", makeEnvironmentHistoryHandler(o.environments))
		ar.Post("/projects/{id}/environments/{env}/rollback", makeRollbackEnvironmentHandler(o.environments, o.deployer, o.runs, aud))

		// 流水线编排配置(Story 2.2)。pl 为 nil 时 handler 返回 503。
		// 与 /projects/{id}/trigger 同在 {id} 路由组内并存(不会被 /projects/{id} 吞)。
		// GET 过 auth;PUT 为写方法,过 auth + CSRF。
		pl := o.pipelines
		ar.Get("/projects/{id}/pipeline", makeGetPipelineHandler(pl))
		ar.Put("/projects/{id}/pipeline", makeSavePipelineHandler(pl))

		// 流水线即代码导入/预览(FR-8-12):POST 收 .pipewright.yml 文档,解析+校验为同一套 spec。
		// /pipeline/import 比 /pipeline 多一段,不会被吞;写方法,过 auth + CSRF。
		ar.Post("/projects/{id}/pipeline/import", makeImportPipelineHandler(pl))

		// 构建/部署配置(Story 2.4):独立表/端点,与 /pipeline(2-2 spec)各自路由;
		// /pipeline/settings 比 /pipeline 多一段,不会被吞。GET 过 auth;PUT 写方法过 auth + CSRF。
		ps := o.pipelineSettings
		ar.Get("/projects/{id}/pipeline/settings", makeGetPipelineSettingsHandler(ps))
		ar.Put("/projects/{id}/pipeline/settings", makeSavePipelineSettingsHandler(ps))

		// 流水线配置合法性校验(Story 2.6):只读聚合视图 + 认证。
		// 聚合 spec(2-2)+ settings(2-4)+ trigger(2-3)+ 项目凭据,跑跨 tab 校验。
		// 复用已注入的 o.pipelines/o.pipelineSettings/o.triggers/o.projects/o.vault,无需新 Option。
		// /pipeline/validation 比 /pipeline 多一段,不会被吞;GET 过 auth。
		ar.Get("/projects/{id}/pipeline/validation",
			makeGetPipelineValidationHandler(pl, ps, t, p, v))

		// 运行模型 + 列表 + 详情 + 取消 + SSE 状态流(Story 3.1)。rs 为 nil 时返回 503。
		// SSE events 为 GET,豁免 CSRF;cancel 为写方法,过 CSRF。均过 requireAuth。
		rs := o.runs
		ar.Get("/runs", makeListRunsHandler(rs))
		ar.Get("/runs/{id}", makeGetRunHandler(rs))
		ar.Get("/runs/{id}/events", makeRunEventsHandler(rs, o.runSub))
		// 历史日志拉取 / 分页(Story 3.6):只读 + 认证;sinceSeq 分页;complete=终态。
		ar.Get("/runs/{id}/logs", makeRunLogsHandler(rs))
		// 构建产物契约(Story 3.4 / FR-6):只读 + 认证;按 (type, reference) 供 Epic 4 消费。
		ar.Get("/runs/{id}/artifacts", makeRunArtifactsHandler(rs))
		// 产物下载(Story 8-16 续):按 reference 句柄从制品库取真字节流下载。o.artifactStore 为 nil → 503。
		ar.Get("/runs/{id}/artifacts/{artifactId}/download", makeArtifactDownloadHandler(rs, o.artifactStore))
		// 测试报告 + 质量门禁(Story 8-6 / FR-8-6):只读 + 认证;通过/失败/跳过 + 覆盖率 + 门禁裁决。
		ar.Get("/runs/{id}/test-report", makeRunTestReportHandler(rs))
		ar.Post("/runs/{id}/cancel", makeCancelRunHandler(rs))
		// 人工审批门(Story 8-4):批准/拒绝某运行的审批门阶段 + 列审批记录。
		// approve/reject 为写方法,过 auth + CSRF;coord/store 为 nil → 503。
		ar.Post("/runs/{id}/approve", makeApprovalDecisionHandler(o.approvalCoord, o.approvalStore, aud, true))
		ar.Post("/runs/{id}/reject", makeApprovalDecisionHandler(o.approvalCoord, o.approvalStore, aud, false))
		ar.Get("/runs/{id}/approvals", makeListApprovalsHandler(o.approvalStore))
		// 环境晋级流(Story 8-7 / FR-8-7):把成功运行晋级到下一环境(gated 复用审批门内核)+ 列晋级历史。
		// promote 为写方法,过 auth + CSRF;o.promotionStore 为 nil → 503。
		ar.Post("/runs/{id}/promote", makePromoteRunHandler(o.promotionStore, rs, o.approvalCoord, o.approvalStore, o.vault, aud))
		ar.Get("/runs/{id}/promotions", makeListRunPromotionsHandler(o.promotionStore))
		// SSH 部署执行(Story 4.2 / FR-10):认证 + CSRF(写方法)。dep 为 nil → 503。
		// 取 run + 产物 + 服务器 → deploy.Deploy(逐机经 SSH 执行部署命令,array 不拼 shell)
		// → 据结果更新 run 终态 → 返回填好的 targets。部署执行失败不 500(每机 status=failed)。
		ar.Post("/runs/{id}/deploy", makeDeployRunHandler(o.deployer, rs))
		// 仅重试失败目标(Story 4.5 / FR-13):认证 + CSRF(写方法)。dep 为 nil → 503。
		// 取 run 当前 failed/rolled_back 目标 → 复用产物 + 配置并行重跑 → 逐目标 upsert(成功机不动)
		// → 重算 run 终态 → 返回全量最新 targets。run 非失败 / 无失败目标 → 422;不存在 → 404。
		// 重试执行失败不 500(被重试目标 status=failed)。比 /deploy 多一段,不会被吞。
		ar.Post("/runs/{id}/deploy/retry", makeRetryDeployHandler(o.deployer, rs))
		// 交互式分批部署(P0 · 对标云效 firstBatchPause):续发暂停中的其余 pending / 中止保留旧版本。
		// 认证 + CSRF。比 /deploy 多一段,不会被吞。dep 为 nil → 503。
		ar.Post("/runs/{id}/deploy/continue", makeContinueDeployHandler(o.deployer, rs))
		ar.Post("/runs/{id}/deploy/abort", makeAbortDeployHandler(o.deployer, rs))

		// AI 失败诊断(Story 7.2):显式(重)诊断。认证 + CSRF(写方法)。
		// 取 run 失败日志 → 脱敏 → ai.Diagnose → 持久化 → 返回 diagnosis 子 DTO。
		// 非失败 → 422 run_not_failed;不存在 → 404;任何 LLM 失败 → 200 + status=unavailable
		// (绝不 500、绝无密钥)。复用已注入 rs + aiSettings(o.aiSettings),无新顶层依赖。
		ar.Post("/runs/{id}/diagnose", makeDiagnoseRunHandler(rs, o.aiSettings, o.secretSource))

		// 成功/失败差异对比(Story 7.3;FR-25):只读 + 认证。取本次 run(commit)+ LastSuccessfulRun
		// (同项目+同分支+更早+最近成功)+ 项目 repoURL/凭据 → RunDiffer(go-git 克隆 + tree diff)→ DTO。
		// 无 baseline / 无 commit / 克隆失败 / commit 不可达 → 200 available:false degraded(绝不 500);
		// run 不存在 → 404。复用已注入 rs + p + v + 注入的 RunDiffer。differ 为 nil → 503。
		ar.Get("/runs/{id}/diff", makeRunDiffHandler(runDiffDeps{
			runs:     rs,
			projects: p,
			vault:    v,
			differ:   o.runDiffer,
		}))

		// 诊断反馈闭环(Story 7.5;FR-26):对 ready 诊断 👍/👎(👎 可附正确根因)。认证 + CSRF(写方法)。
		// run 不存在 → 404;无诊断 → 422 no_diagnosis;同 run 重复提交 = upsert 覆盖最新。
		// correctRootCause 脱敏(过 mask)+ 长度上限。复用已注入 rs + o.feedback(WithDiagnosisFeedback)。
		// fb 为 nil → 503。GET diagnosis-stats 在 /settings 组内(只读 + 认证)。
		ar.Post("/runs/{id}/diagnosis/feedback", makeSubmitDiagnosisFeedbackHandler(rs, o.feedback, o.secretSource))

		// 手动触发创建运行(Story 3.2):认证 + CSRF;actor=admin;返 201 run-detail DTO。
		// o.parameters 非 nil 时,据项目参数定义校验 + 强转 + 填默认(typed params,P0);无定义则透传。
		ar.Post("/projects/{id}/runs", makeManualRunHandler(rs, o.parameters, aud))

		// 账户设置(Story 1.7):改口令 / 会话列表 / 撤销会话。ac 为 nil 时 handler 返回 503。
		// 改口令、撤销会话为写方法,过 auth + CSRF;列表为 GET,过 auth。
		// 改口令 / 撤销会话为敏感操作,业务成功后接 audit(detail 绝无明文口令)。
		ac := o.account
		ar.Post("/account/password", makeChangePasswordHandler(ac, aud))
		ar.Get("/account/sessions", makeListSessionsHandler(ac))
		ar.Delete("/account/sessions/{id}", makeRevokeSessionHandler(ac, aud))

		// 可配置 AI 提供商(Story 7.1)。aiSvc 为 nil 时 handler 返回 503。
		// GET 过 auth;PUT/test 为写方法,过 auth + CSRF。apiKey 加密入库、响应仅掩码。
		aiSvc := o.aiSettings
		ar.Get("/settings/ai", makeGetAISettingsHandler(aiSvc))
		ar.Put("/settings/ai", makeSaveAISettingsHandler(aiSvc))
		ar.Post("/settings/ai/test", makeTestAISettingsHandler(aiSvc))

		// 诊断反馈闭环统计(Story 7.5;FR-26):准确率/计数/最近趋势/最近 corrections(知识库种子可视化)。
		// 只读 + 认证;无反馈 → 全 0 / 空数组 / accuracy:null(不报错)。fb 为 nil → 503。
		ar.Get("/settings/diagnosis-stats", makeDiagnosisStatsHandler(o.feedback))

		// AI 生成流水线配置(Story 2.5):浅克隆分析 + LLM 生成提案 + 全/部分接受应用。
		// generate 复用 aiSvc + projects(p)+ vault(v);apply 复用 pipelines(pl)+ settings(ps)+ triggers(t)。
		// /pipeline/ai-generate、/pipeline/ai-apply 比 /pipeline 多一段,不会被吞;均为写方法,过 auth + CSRF。
		// 两路优雅降级(AI 未配 / clone 失败 / LLM 失败)均 HTTP 200,绝不 500、绝无明文密钥。
		aiGenDeps := aiGenerateDeps{
			analyzer:    o.aiAnalyzer,
			aiSvc:       aiSvc,
			projects:    p,
			pipes:       pl,
			settings:    ps,
			triggers:    t,
			vault:       v,
			customNodes: o.customNodes,
		}
		ar.Post("/projects/{id}/pipeline/ai-generate", makeAIGenerateHandler(aiGenDeps))
		ar.Post("/projects/{id}/pipeline/ai-apply", makeAIApplyHandler(aiGenDeps))

		// AI 脚本风险标注(护城河 · AI moat):对流水线脚本步骤(Epic 8 script step)做风险标注 —
		// 确定性正则预扫(rm -rf /、curl|sh、chmod 777、明文密钥、latest 镜像)+ 可选 LLM 语义增强。
		// 复用 aiSvc + projects(p)+ settings(ps)+ secretSource(出网前脱敏)。写方法,过 auth + CSRF。
		// AI 未配/失败均 200 + 确定性结果 + aiEnhanced=false,绝不 500、绝无明文密钥。
		aiRisk := aiRiskDeps{aiSvc: aiSvc, projects: p, settings: ps, secretSource: o.secretSource}
		ar.Post("/projects/{id}/pipeline/analyze-risks", makeAnalyzeRisksHandler(aiRisk))

		// AI 运维终端助手(运维终端 P1 · 护城河):中文 → 命令卡 / 命令解释。复用 aiSvc。
		// 写方法,过 auth + CSRF。未配 AI → 200 + available=false(前端提示去设置配);命令最终
		// risk 由领域层确定性复核(rm -rf/mkfs/dd/关机等无条件 danger),前端据此拦截直接执行。
		ar.Post("/ai/command", makeAICommandHandler(aiSvc))
		ar.Post("/ai/explain", makeAIExplainHandler(aiSvc))
		ar.Post("/ai/complete", makeAICompleteHandler(aiSvc)) // P2 智能补全(前缀→完整命令)
		ar.Post("/ai/compose", makeAIComposeHandler(aiSvc))   // 中文 → docker-compose.yml

		// 只读源码读取(Story 3.6;FR-4 预埋,7-4 消费):go-git 浅克隆读 tree/blob。
		// 复用已注入 projects(p)+ vault(v)取凭据 + 注入的 SourceReader。reader 为 nil → 503。
		// /source/tree、/source/blob 比 /projects/{id} 多两段,不会被吞;GET 过 auth。
		srcDeps := sourceDeps{projects: p, vault: v, reader: o.sourceReader}
		ar.Get("/projects/{id}/source/tree", makeSourceTreeHandler(srcDeps))
		ar.Get("/projects/{id}/source/blob", makeSourceBlobHandler(srcDeps))

		// 「流水线即代码」预览/校验(GitOps Slice 3):按 ref 拉取仓库 .pipewright.yml 并校验,
		// 让用户在依赖它驱动运行之前看清运行时会用到的配置摘要、显式捕获 YAML 错误(运行时静默回退)。
		// 复用 srcDeps(projects + vault + SourceReader);/pac/preview 比 /projects/{id} 多两段,不会被吞;GET 过 auth。
		ar.Get("/projects/{id}/pac/preview", makePacPreviewHandler(srcDeps))

		// 列仓库分支/tag(代码管理区 · Story 8-18):供前端触发时分支/commit 下拉。o.refsLister 为 nil → 503。
		ar.Get("/projects/{id}/refs", makeListRefsHandler(p, v, o.refsLister))
		ar.Get("/projects/{id}/commits", makeListCommitsHandler(p, v, o.refsLister))
		ar.Get("/projects/{id}/runner", makeGetRunnerHandler(o.runnerConfig))
		ar.Put("/projects/{id}/runner", makeSaveRunnerHandler(o.runnerConfig, aud))

		// 目标服务器登记 + 通用 SSH 执行层(Story 4.1;internal/target,FR-14)。
		// sv 为 nil 时 handler 返回 503。GET/List/Get 过 auth;create/update/delete/test 为写方法,
		// 过 auth + CSRF。/servers/{id}/test 比 /servers/{id} 多一段,不会被吞。
		// SSH 私钥/口令经 vault 即用即弃,响应/错误绝无明文(AC-SEC-01/02)。
		sv := o.servers
		ar.Get("/servers", makeListServersHandler(sv))
		ar.Post("/servers", makeCreateServerHandler(sv))
		ar.Get("/servers/{id}", makeGetServerHandler(sv))
		ar.Put("/servers/{id}", makeUpdateServerHandler(sv))
		ar.Delete("/servers/{id}", makeDeleteServerHandler(sv))
		ar.Post("/servers/{id}/test", makeTestServerHandler(sv))
		// 服务日志查看(Story 6.2;FR-16,经 SSH 取目标服务器日志)。复用 sv(4-1 装配,无新服务)。
		// 均为 GET 只读 → 过 auth、豁免 CSRF。source/target 严格白名单校验(AC-SEC-02);
		// SSH/命令失败人读不 500;实时 /logs/stream 为 SSE,客户端断开即关 SSH session。
		// 比 /servers/{id} 多一段,不会被吞。
		ar.Get("/servers/{id}/logs", makeServerLogsHandler(sv))
		ar.Get("/servers/{id}/logs/stream", makeServerLogsStreamHandler(sv))
		// 多机状态总览 —— 服务器层指标(Story 6.1;FR-15,经 SSH 跑固定白名单只读命令采集
		// CPU/内存/磁盘)。复用 sv(4-1 装配,无新服务)。均为 GET 只读 → 过 auth、豁免 CSRF。
		// 采集命令纯静态 array、不接受任何用户输入(AC-SEC-02);某台不可达/某指标缺失 →
		// reachable:false / 该指标 null,不 500。批量端点逐台并行有界、各自独立。
		// chi 字面段优先于 {id},/servers/metrics 不会被 /servers/{id} 吞。
		ar.Get("/servers/metrics", makeAllServerMetricsHandler(sv))
		ar.Get("/servers/{id}/metrics", makeServerMetricsHandler(sv))
		// 容器管理列表/聚合(Portainer 式总览)——经 SSH 跑 `docker ps -a --format {{json .}}`
		// 采集容器清单。复用 sv(4-1 装配,无新服务)。GET 只读 → 过 auth、豁免 CSRF。命令纯静态
		// array、不接受任何用户输入(AC-SEC-02);某台不可达/无运行时 → reachable:false / runtime:""
		// ,不 500。批量端点逐台并行有界、各自独立。容器**生命周期**写操作复用上面的 /service/action
		// (type=docker)。chi 字面段 /servers/containers 优先于 {id},不会被吞。
		ar.Get("/servers/containers", makeAllServerContainersHandler(sv))
		ar.Get("/servers/{id}/containers", makeServerContainersHandler(sv))
		// 容器实时资源 stats(docker stats --no-stream)。字面段 stats 不与 {id}/containers 冲突。
		ar.Get("/servers/{id}/containers/stats", makeServerContainerStatsHandler(sv))
		// 容器详情 inspect(docker inspect → 精选字段)。
		ar.Get("/servers/{id}/containers/{containerId}/inspect", makeContainerInspectHandler(sv))
		// 一键清理:磁盘占用(docker system df)+ prune(写,过 CSRF + 审计)。
		ar.Get("/servers/{id}/system/df", makeSystemDfHandler(sv))
		ar.Post("/servers/{id}/system/prune", makeSystemPruneHandler(sv, aud))
		// 新增容器(docker run) —— 写操作,过 auth + CSRF。参数严格白名单 + array 化,
		// 非法 400 invalid_create_spec;SSH/命令失败人读不 500;成功后写审计(detail 仅 image/name)。
		ar.Post("/servers/{id}/containers", makeCreateContainerHandler(sv, aud))
		// 镜像管理 —— 列表(只读)+ 拉取/删除(写,过 CSRF + 审计)。镜像引用严格白名单 + array 化。
		ar.Get("/servers/{id}/images", makeServerImagesHandler(sv))
		ar.Post("/servers/{id}/images/pull", makeImagePullHandler(sv, aud))
		ar.Post("/servers/{id}/images/remove", makeImageRemoveHandler(sv, aud))
		// Compose/Stacks —— 列表(只读)+ 部署(贴 yaml → up)+ start/stop/restart/down。
		// 项目名严格白名单 + array 化;compose 内容经 Upload 落文件(非 shell 注入面)。写过 CSRF + 审计。
		ar.Get("/servers/{id}/stacks", makeServerStacksHandler(sv))
		ar.Get("/servers/{id}/stacks/{name}", makeServerStackDetailHandler(sv))
		ar.Post("/servers/{id}/stacks/deploy", makeStackDeployHandler(sv, aud))
		ar.Post("/servers/{id}/stacks/action", makeStackActionHandler(sv, aud))
		ar.Post("/servers/{id}/stacks/save", makeStackSaveHandler(sv, aud))
		// 容器 AI 诊断 / 看日志(AI moat):取容器日志 → ai.Diagnose 出根因+修复。复用 sv +
		// o.aiSettings。出网前脱敏;AI 未配/失败 → 200 unavailable,绝不 500。比 {id} 多两段不被吞。
		ar.Post("/servers/{id}/containers/{containerId}/diagnose", makeContainerDiagnoseHandler(sv, o.aiSettings))
		// 数据卷 + 网络 —— 列表(只读)+ 创建/删除(写,过 CSRF + 审计)。名称白名单 + array 化。
		ar.Get("/servers/{id}/volumes", makeServerVolumesHandler(sv))
		ar.Post("/servers/{id}/volumes/create", makeVolumeCreateHandler(sv, aud))
		ar.Post("/servers/{id}/volumes/remove", makeVolumeRemoveHandler(sv, aud))
		ar.Get("/servers/{id}/networks", makeServerNetworksHandler(sv))
		ar.Post("/servers/{id}/networks/create", makeNetworkCreateHandler(sv, aud))
		ar.Post("/servers/{id}/networks/remove", makeNetworkRemoveHandler(sv, aud))
		// 网络详情:某网络上连着哪些容器 + 连接/断开。network 段白名单 + array 化。
		ar.Get("/servers/{id}/networks/{network}/containers", makeNetworkContainersHandler(sv))
		ar.Post("/servers/{id}/networks/{network}/connect", makeNetworkConnectHandler(sv, aud))
		ar.Post("/servers/{id}/networks/{network}/disconnect", makeNetworkDisconnectHandler(sv, aud))
		// 服务操作 —— 重启/停止/启动(Story 6.3;FR-17,经 SSH 跑 systemctl/docker)。
		// 复用 sv(4-1 装配)+ aud(1-4 装配),无需新服务。写操作 → 过 auth + CSRF。
		// type/target/action 严格白名单(AC-SEC-02:首字符非 `-` 防 flag 注入、无 shell 元字符
		// 防命令注入、action 枚举);命令 array 化经 target.Exec 不拼 shell。非法 → 400
		// invalid_service_target;SSH/命令失败人读不 500;成功投递后写审计(detail 脱敏)。
		// 比 /servers/{id} 多两段,不会被吞。
		ar.Post("/servers/{id}/service/action", makeServiceActionHandler(sv, aud))
		// 容器内交互终端(Story 6.4;FR-18,WS ↔ SSH → docker exec -it)。复用 sv(4-1 装配)
		// + aud(1-4 装配),无需新服务。**平台唯一 WS 升级点**(其余实时流走 SSE)。GET 升级
		// → 过 auth(未登录 401 不升级);CSRF 对 GET 豁免,改以同源(Origin)校验防跨站劫持。
		// containerId 严格白名单(首字符非 `-` 防 flag 注入、无 shell 元字符)+ shell 枚举白名单;
		// 命令 array 化经 target.ExecInteractive 不拼 shell。握手成功(PTY 建立)后写审计(detail 脱敏)。
		// 比 /servers/{id} 多两段,不会被吞。
		ar.Get("/servers/{id}/containers/{containerId}/terminal", makeContainerTerminalHandler(sv, aud))
		// 主机 shell 终端(「连服务器终端」的目标:SSH 直起交互 shell)。进容器留给用户在 shell 里
		// 自己 docker exec 自由探索(不绑死容器;很多服务器没 docker)。WS 升级,同源校验 + shell
		// 白名单;审计 server_terminal。比 /servers/{id} 多一段,不会被吞。
		ar.Get("/servers/{id}/terminal", makeServerTerminalHandler(sv, aud))
		// 通知渠道(Story 5.1;FR-19)。nf 为 nil 时 handler 返回 503。
		// GET(列表/详情)过 auth;POST/PUT/DELETE/test 为写方法,过 auth + CSRF。
		// 敏感字段(SMTP 密码)加密入库、响应仅 hasPassword。test 须在 {id} 路由内单独注册。
		nf := o.notifications
		// 通知全局配置(外发通知默认文案语言)。
		ar.Get("/notifications/config", makeGetNotifyConfigHandler(nf))
		ar.Put("/notifications/config", makeSetNotifyConfigHandler(nf))

		// 运行数据保留策略(全局)。GET 过 auth;PUT 写方法过 auth + CSRF。
		ar.Get("/retention/config", makeGetRetentionConfigHandler(o.retention))
		ar.Put("/retention/config", makeSetRetentionConfigHandler(o.retention))

		// 自动 HTTPS + 域名反向代理(R1):路由 CRUD;经已装配的 target.Service 在目标主机上编排
		// Caddy(渲染 Caddyfile + reload),Caddy 自动经 Let's Encrypt(HTTP-01)签发/续期证书。
		// px 为 nil → handler 返回 503。GET(列表)过 auth;POST/DELETE/enabled/refresh 为写方法,
		// 过 auth + CSRF;写操作记审计(detail 仅域名/容器/端口,无敏感信息)。enabled/refresh 比
		// /proxy/routes/{id} 多一段,不会被吞。
		px := o.proxy
		ar.Get("/proxy/routes", makeListProxyRoutesHandler(px))
		ar.Post("/proxy/routes", makeCreateProxyRouteHandler(px, aud))
		// 跨主机证书总览大盘(R2 E2.4):聚合全部路由 + 主机展示名 + 证书状态(只读,无审计)。
		// 字面段 /proxy/overview 与 /proxy/routes 不同首段,不会被吞。
		ar.Get("/proxy/overview", makeProxyOverviewHandler(px))
		// 更新高级配置(R2:多域名 / 访问控制 / 安全头 / 压缩 / 重定向)。写方法,过 auth + CSRF + 审计。
		ar.Put("/proxy/routes/{id}", makeUpdateProxyRouteHandler(px, aud))
		ar.Post("/proxy/routes/{id}/enabled", makeSetProxyRouteEnabledHandler(px, aud))
		ar.Post("/proxy/routes/{id}/refresh", makeRefreshProxyRouteHandler(px))
		ar.Delete("/proxy/routes/{id}", makeDeleteProxyRouteHandler(px, aud))

		// DNS 提供商集成层(R3 E3.1–E3.4):Cloudflare / DNSPod / 阿里云 DNS 接入(凭据走 vault)。
		// dp 为 nil → handler 返回 503。GET(列表)过 auth;POST/DELETE/verify 为写方法,过 auth + CSRF + 审计。
		// DNS token 经 vault 密文、apply 时即用即弃,响应/审计/日志绝无明文 token。
		// 字面段 /proxy/subdomains 与 /proxy/routes 不同尾段,不会被吞。
		dp := o.dnsProviders
		ar.Get("/dns/providers", makeListDNSProvidersHandler(dp))
		ar.Post("/dns/providers", makeCreateDNSProviderHandler(dp, aud))
		ar.Post("/dns/providers/{id}/verify", makeVerifyDNSProviderHandler(dp, aud))
		ar.Delete("/dns/providers/{id}", makeDeleteDNSProviderHandler(dp, aud))
		// 瞬时子域名分配(R3 E3.3 + E3.4):建 A 记录 + 建 DNS-01 反代路由 → 普通 Route DTO。
		ar.Post("/proxy/subdomains", makeAllocateSubdomainHandler(subdomainDeps{dns: dp, servers: sv, proxy: px}, aud))

		// Per-PR 预览环境(R4 E4.1 · 差异化王牌):列预览环境 / 手动回收 / 项目级预览配置。
		// pv 为 nil → handler 返回 503。GET 过 auth;reclaim + config-PUT 为写方法,过 auth + CSRF + 审计。
		// /preview-envs/{id}/reclaim 比 /preview-envs 多两段、/projects/{id}/preview-config 比 /projects/{id}
		// 多一段,均不会被吞。DNS token 全程走 R3 vault,DTO/审计/日志绝无密钥。
		pv := o.previewEnvs
		ar.Get("/preview-envs", makeListPreviewEnvsHandler(pv))
		ar.Post("/preview-envs/{id}/reclaim", makeReclaimPreviewEnvHandler(pv, aud))
		ar.Get("/projects/{id}/preview-config", makeGetPreviewConfigHandler(pv))
		ar.Put("/projects/{id}/preview-config", makeSetPreviewConfigHandler(pv, aud))

		ar.Get("/notifications/channels", makeListChannelsHandler(nf))
		ar.Post("/notifications/channels", makeCreateChannelHandler(nf))
		ar.Get("/notifications/channels/{id}", makeGetChannelHandler(nf))
		ar.Put("/notifications/channels/{id}", makeUpdateChannelHandler(nf))
		ar.Delete("/notifications/channels/{id}", makeDeleteChannelHandler(nf))
		ar.Post("/notifications/channels/{id}/test", makeTestChannelHandler(nf))

		// 通知事件路由(Story 5.2;FR-20)。复用同一 notify.Service。「事件 → 渠道」映射:
		// GET(列表)过 auth;POST/DELETE 为写方法,过 auth + CSRF。未配置路由的事件不发送。
		ar.Get("/notifications/routes", makeListRoutesHandler(nf))
		ar.Post("/notifications/routes", makeCreateRouteHandler(nf))
		ar.Delete("/notifications/routes/{id}", makeDeleteRouteHandler(nf))

		// 通知模板(Story 5.3;FR-21)。复用同一 notify.Service。「事件(可选按渠道)→ 标题/正文
		// 模板」:渲染纯文本替换占位(无 RCE);未配模板的事件 → 平台默认文案(5-2 行为不变)。
		// GET(列表)过 auth;POST/PUT/DELETE 为写方法,过 auth + CSRF。
		ar.Get("/notifications/templates", makeListTemplatesHandler(nf))
		ar.Post("/notifications/templates", makeCreateTemplateHandler(nf))
		ar.Put("/notifications/templates/{id}", makeUpdateTemplateHandler(nf))
		ar.Delete("/notifications/templates/{id}", makeDeleteTemplateHandler(nf))

		// 可配置异常检测与告警(Story 6.5;FR-23)。an 为 nil 时 handler 返回 503。
		// 规则 CRUD + 立即检测 + 告警列表;检测复用 6-1 指标采集(metricsCollector 适配
		// collectServerMetrics)逐服务器逐 enabled 规则求值,命中产告警入库(指标 null /
		// 不可达跳过,不误报)。GET(rules/alerts)过 auth;POST(rules/check)+ DELETE 为
		// 写方法,过 auth + CSRF。metric/operator 枚举校验;无规则 → 空检测不报错。
		// /anomaly/rules 字面段优先于 {id},/anomaly/check、/anomaly/alerts 不会被吞。
		an := o.anomaly
		ar.Get("/anomaly/config", makeAnomalyConfigHandler(o.anomalyInterval, o.anomalyCooldown))
		ar.Get("/anomaly/rules", makeListAnomalyRulesHandler(an))
		ar.Post("/anomaly/rules", makeCreateAnomalyRuleHandler(an))
		ar.Patch("/anomaly/rules/{id}", makeUpdateAnomalyRuleHandler(an))
		ar.Delete("/anomaly/rules/{id}", makeDeleteAnomalyRuleHandler(an))
		ar.Post("/anomaly/check", makeAnomalyCheckHandler(an))
		ar.Get("/anomaly/alerts", makeListAnomalyAlertsHandler(an))
		ar.Get("/metrics/history", makeMetricsHistoryHandler(o.metricsHistory))

		// 多 provider OAuth 凭据接入(连接 Gitee/GitHub/GitLab/自建)。oa 为 nil → handler 503。
		// apps 列表/upsert:GET 过 auth;PUT 写方法过 auth + CSRF(clientSecret 只写、加密入库、响应仅掩码)。
		// authorize/callback 均为 GET(过 auth、豁免 CSRF):authorize 生成绑会话 state → 302 跳授权页;
		// callback 验 state(CSRF 防护)→ Exchange → 经已注入 vault(v)把 access_token 存成 git_token
		// 凭据 → 302 跳回 /settings/vault。token/client_secret 经 vault 密文,响应/错误绝无明文。
		// 字面段 apps 优先于 {provider},/oauth/{provider}/authorize 不会被 /oauth/apps 吞。
		oa := o.oauth
		ar.Get("/oauth/apps", makeListOAuthAppsHandler(oa))
		ar.Put("/oauth/apps/{provider}", makeSaveOAuthAppHandler(oa))
		ar.Get("/oauth/{provider}/authorize", makeOAuthAuthorizeHandler(oa))
		ar.Get("/oauth/{provider}/callback", makeOAuthCallbackHandler(oa, v))

		// DORA 四指标只读聚合(FR-8-15):部署频率 / 变更前置时长 / 变更失败率 / MTTR。
		// 对既有运行数据做纯聚合(无新表、无副作用)。GET 只读 → 过 auth、豁免 CSRF。
		// projectId 可选过滤;window 形如 30d(默认)。o.doraMetrics 为 nil → handler 返回 503。
		ar.Get("/metrics/dora", makeDoraMetricsHandler(o.doraMetrics))

		// 流水线模板(FR-8-13 复用基座):命名、可复用的流水线定义,跨项目共享。
		// tpl 为 nil → handler 返回 503。GET(列表/详情)过 auth;POST/DELETE/apply 为写方法,
		// 过 auth + CSRF + 审计。模板 spec 不含明文 secret。apply-template 比 /pipeline 多一段,不会被吞。
		tpl := o.templates
		ar.Get("/templates", makeListPipelineTemplatesHandler(tpl))
		ar.Post("/templates", makeCreatePipelineTemplateHandler(tpl, aud))
		ar.Get("/templates/{id}", makeGetPipelineTemplateHandler(tpl))
		ar.Delete("/templates/{id}", makeDeletePipelineTemplateHandler(tpl, aud))
		ar.Post("/projects/{id}/pipeline/apply-template", makeApplyTemplateHandler(tpl, aud))

		// 变量组(FR-8-13 复用基座):命名、可复用的变量集合(key=value + secret 引用)。
		// vg 为 nil → handler 返回 503。GET 过 auth;POST/PUT/DELETE 为写方法,过 auth + CSRF + 审计。
		// secret 变量只存/回 credentialId + 掩码,绝无明文。
		vg := o.varGroups
		ar.Get("/variable-groups", makeListVarGroupsHandler(vg))
		ar.Post("/variable-groups", makeCreateVarGroupHandler(vg, aud))
		ar.Get("/variable-groups/{id}", makeGetVarGroupHandler(vg))
		ar.Put("/variable-groups/{id}", makeUpdateVarGroupHandler(vg, aud))
		ar.Delete("/variable-groups/{id}", makeDeleteVarGroupHandler(vg, aud))

		// 自定义节点(复用库 Tier 2):命名、可复用的「单节点」(nodeType + config 快照)。
		// cn 为 nil → handler 返回 503。GET 过 auth;POST/PUT/DELETE 为写方法,过 auth + CSRF + 审计。
		// config 为自由 KV 快照,不含明文 secret。
		cn := o.customNodes
		ar.Get("/custom-nodes", makeListCustomNodesHandler(cn))
		ar.Post("/custom-nodes", makeCreateCustomNodeHandler(cn, aud))
		ar.Get("/custom-nodes/{id}", makeGetCustomNodeHandler(cn))
		ar.Put("/custom-nodes/{id}", makeUpdateCustomNodeHandler(cn, aud))
		ar.Delete("/custom-nodes/{id}", makeDeleteCustomNodeHandler(cn, aud))

		// 后续 story 在此挂载其他 API 路由。
	})

	// 其余路径交给 SPA:存在的静态文件直接返回,否则回退 index.html。
	r.Handle("/*", spaHandler(webFS))

	return r
}

// ---- 认证中间件 --------------------------------------------------------

// requireAuth 中间件:校验会话 cookie → 注入 Session 到 context;未过 → 401 JSON。
func requireAuth(svc auth.Authenticator, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "请先登录")
			return
		}
		cookie, err := r.Cookie(cookieSession)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "请先登录")
			return
		}
		sess, err := svc.Verify(cookie.Value)
		if err != nil {
			if errors.Is(err, auth.ErrSessionNotFound) {
				writeError(w, http.StatusUnauthorized, "unauthorized", "会话已过期,请重新登录")
				return
			}
			// 其它错误(如 DB 故障)不应被当作「会话过期」掩盖。
			writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
			return
		}
		ctx := context.WithValue(r.Context(), contextKeySession, sess)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// sessionFromContext 从已认证的 context 取 Session。
func sessionFromContext(ctx context.Context) (*auth.Session, bool) {
	sess, ok := ctx.Value(contextKeySession).(*auth.Session)
	return sess, ok
}

// requireCSRF 中间件:将 X-CSRF-Token header 与服务端权威的 session.CSRFToken 比对。
// GET/HEAD/OPTIONS 豁免;写方法需 header == session.CSRFToken。
// 必须套在 requireAuth 之后(依赖 context 中的 Session)。
//
// 相较旧的「header == cookie」双提交方案,以会话存储的 csrf_token 为权威值,
// 子域/MITM 写入伪造 cookie 无法绕过(攻击者读不到 HttpOnly 会话对应的服务端 token)。
// 前端契约不变:它仍发送 header = pipewright_csrf cookie 值,而该 cookie 下发的正是
// session.CSRFToken,故 header 仍等于 session.CSRFToken。
func requireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		sess, ok := sessionFromContext(r.Context())
		if !ok || sess == nil {
			// 未经 requireAuth 注入 Session:无法校验 CSRF。
			writeError(w, http.StatusForbidden, "csrf_invalid", "CSRF token 缺失或不匹配")
			return
		}
		headerVal := r.Header.Get(headerCsrf)
		if headerVal == "" || subtle.ConstantTimeCompare([]byte(headerVal), []byte(sess.CSRFToken)) != 1 {
			writeError(w, http.StatusForbidden, "csrf_invalid", "CSRF token 缺失或不匹配")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---- 认证 Handler --------------------------------------------------------

// makeLoginHandler 返回 POST /api/auth/login handler。
// 登录端点豁免 CSRF(此时尚无会话);但受锁定限制。
func makeLoginHandler(svc auth.Authenticator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "no_auth", "认证服务未初始化")
			return
		}
		// 限制匿名请求体大小,防超大 body。
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}

		sess, err := svc.Login(req.Username, req.Password)
		if err != nil {
			var lockedErr *auth.ErrLockedOut
			if errors.As(err, &lockedErr) {
				mins := int(lockedErr.RetryAfter.Minutes()) + 1
				msg := fmt.Sprintf("登录失败次数过多,请 %d 分钟后重试", mins)
				writeError(w, http.StatusTooManyRequests, "locked_out", msg)
				return
			}
			if errors.Is(err, auth.ErrInvalidCredentials) {
				writeError(w, http.StatusUnauthorized, "invalid_credentials", "用户名或口令错误")
				return
			}
			writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
			return
		}

		// 设置 HttpOnly 会话 cookie(Secure 在 TLS 时自动加)。
		secure := r.TLS != nil
		http.SetCookie(w, &http.Cookie{
			Name:     cookieSession,
			Value:    sess.Token,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			Secure:   secure,
			Expires:  sess.ExpiresAt,
		})
		// 下发可读 CSRF cookie(非 HttpOnly,前端 JS 读取)。
		http.SetCookie(w, &http.Cookie{
			Name:     cookieCsrf,
			Value:    sess.CSRFToken,
			Path:     "/",
			HttpOnly: false,
			SameSite: http.SameSiteLaxMode,
			Secure:   secure,
			Expires:  sess.ExpiresAt,
		})
		// 回显真实存储的用户名(不再硬编码 "admin")。
		username := req.Username
		if u, err := svc.AdminUsername(); err == nil {
			username = u
		}
		writeJSON(w, http.StatusOK, map[string]string{"username": username})
	}
}

// makeSessionHandler 返回 GET /api/auth/session handler。
// 此端点本身不要求登录(用于前端探测会话状态)。
func makeSessionHandler(svc auth.Authenticator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "请先登录")
			return
		}
		cookie, err := r.Cookie(cookieSession)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "请先登录")
			return
		}
		sess, err := svc.Verify(cookie.Value)
		if err != nil {
			if errors.Is(err, auth.ErrSessionNotFound) {
				writeError(w, http.StatusUnauthorized, "unauthorized", "会话已过期,请重新登录")
				return
			}
			writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
			return
		}
		// 续期 CSRF cookie。
		secure := r.TLS != nil
		http.SetCookie(w, &http.Cookie{
			Name:     cookieCsrf,
			Value:    sess.CSRFToken,
			Path:     "/",
			HttpOnly: false,
			SameSite: http.SameSiteLaxMode,
			Secure:   secure,
			Expires:  sess.ExpiresAt,
		})
		// 回显真实存储的用户名。
		username := "admin"
		if u, err := svc.AdminUsername(); err == nil {
			username = u
		}
		writeJSON(w, http.StatusOK, map[string]string{"username": username})
	}
}

// makeLogoutHandler 返回 POST /api/auth/logout handler。
// 已由调用方套了 requireAuth + requireCSRF。
func makeLogoutHandler(svc auth.Authenticator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cookie, err := r.Cookie(cookieSession); err == nil && svc != nil {
			_ = svc.Logout(cookie.Value)
		}
		// 清除两个 cookie(Max-Age=-1)。
		http.SetCookie(w, &http.Cookie{
			Name:    cookieSession,
			Value:   "",
			Path:    "/",
			MaxAge:  -1,
			Expires: time.Unix(0, 0),
		})
		http.SetCookie(w, &http.Cookie{
			Name:    cookieCsrf,
			Value:   "",
			Path:    "/",
			MaxAge:  -1,
			Expires: time.Unix(0, 0),
		})
		w.WriteHeader(http.StatusNoContent)
	}
}

// ---- Static / SPA --------------------------------------------------------

func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleVersion 返回构建期注入的版本元数据(version/commit/date/goVersion/platform)。
func handleVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, version.Get())
}

// spaHandler 服务嵌入文件系统中的静态资源;无扩展名的路径(前端路由)回退 index.html,
// 但带扩展名却不存在的请求返回 404,避免把 SPA HTML 当成脚本/资源返回 200。
func spaHandler(webFS fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(webFS))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")

		if info, err := fs.Stat(webFS, name); err == nil && !info.IsDir() {
			if strings.HasPrefix(name, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			fileServer.ServeHTTP(w, r)
			return
		}

		if path.Ext(name) != "" {
			http.NotFound(w, r)
			return
		}

		serveIndex(w, webFS)
	})
}

func serveIndex(w http.ResponseWriter, webFS fs.FS) {
	index, err := fs.ReadFile(webFS, "index.html")
	if err != nil {
		http.Error(w, "index.html not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(index)
}

// ---- JSON helpers --------------------------------------------------------

// writeJSON 统一以 camelCase JSON 输出。
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

type errBody struct {
	Error errDetail `json:"error"`
}

type errDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// writeError 输出统一错误响应:{ "error": { "code": "...", "message": "..." } }
// msg 以 zh-CN 源串写在调用点;此处按请求 locale(localeMiddleware 解析)本地化。
func writeError(w http.ResponseWriter, code int, errCode, msg string) {
	msg = i18n.T(localeOf(w), msg)
	writeJSON(w, code, errBody{Error: errDetail{Code: errCode, Message: msg}})
}
