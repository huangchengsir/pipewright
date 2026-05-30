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
	"github.com/huangjiawei/devopstool/internal/ai"
	"github.com/huangjiawei/devopstool/internal/audit"
	"github.com/huangjiawei/devopstool/internal/auth"
	"github.com/huangjiawei/devopstool/internal/pipeline"
	"github.com/huangjiawei/devopstool/internal/project"
	"github.com/huangjiawei/devopstool/internal/run"
	"github.com/huangjiawei/devopstool/internal/target"
	"github.com/huangjiawei/devopstool/internal/trigger"
	"github.com/huangjiawei/devopstool/internal/vault"
)

const (
	cookieSession = "devops_session"
	cookieCsrf    = "devops_csrf"
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
	pipelines        pipeline.Service
	pipelineSettings pipeline.SettingsService
	runs             run.Service
	runSub           runSubscriber
	receiver         *trigger.Receiver
	audit            audit.Recorder
	account          accountService
	aiSettings       ai.Service
	aiAnalyzer       ai.RepoAnalyzer
	sourceReader     SourceReader
	servers          target.Service
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

// WithSource 注入只读源码读取器(go-git 浅克隆),挂载 /api/projects/{id}/source/{tree,blob}
// 路由(Story 3.6;FR-4 预埋,7-4 前端消费)。复用已注入的 projects + vault 取仓库凭据。
// 不传则 source 端点返回 503(服务未初始化)。
func WithSource(reader SourceReader) Option {
	return func(o *options) { o.sourceReader = reader }
}

// WithServers 注入目标服务器服务(Story 4.1;internal/target 通用 SSH 层),挂载
// /api/servers* 路由(GET auth;写方法 + test auth + CSRF)。SSH 私钥/口令仅经 vault
// 密文,响应/错误绝无明文。不传则相关端点返回 503(服务未初始化)。
func WithServers(s target.Service) Option {
	return func(o *options) { o.servers = s }
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

	r := chi.NewRouter()
	r.Use(middleware.Recoverer)

	r.Get("/healthz", handleHealthz)

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

		// 审计查询(Story 1.4):只读 + 认证保护 + 分页 + 过滤。
		ar.Get("/audit", makeListAuditHandler(aud))

		// 凭据保险库(Story 1.3)。v 为 nil 时 handler 返回 vault_unconfigured。
		v := o.vault
		ar.Get("/credentials", makeListCredentialsHandler(v))
		ar.Post("/credentials", makeCreateCredentialHandler(v, aud))
		ar.Patch("/credentials/{id}", makeUpdateCredentialHandler(v, aud))
		ar.Delete("/credentials/{id}", makeDeleteCredentialHandler(v, aud))

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

		// 流水线编排配置(Story 2.2)。pl 为 nil 时 handler 返回 503。
		// 与 /projects/{id}/trigger 同在 {id} 路由组内并存(不会被 /projects/{id} 吞)。
		// GET 过 auth;PUT 为写方法,过 auth + CSRF。
		pl := o.pipelines
		ar.Get("/projects/{id}/pipeline", makeGetPipelineHandler(pl))
		ar.Put("/projects/{id}/pipeline", makeSavePipelineHandler(pl))

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
		ar.Post("/runs/{id}/cancel", makeCancelRunHandler(rs))

		// AI 失败诊断(Story 7.2):显式(重)诊断。认证 + CSRF(写方法)。
		// 取 run 失败日志 → 脱敏 → ai.Diagnose → 持久化 → 返回 diagnosis 子 DTO。
		// 非失败 → 422 run_not_failed;不存在 → 404;任何 LLM 失败 → 200 + status=unavailable
		// (绝不 500、绝无密钥)。复用已注入 rs + aiSettings(o.aiSettings),无新顶层依赖。
		ar.Post("/runs/{id}/diagnose", makeDiagnoseRunHandler(rs, o.aiSettings))

		// 手动触发创建运行(Story 3.2):认证 + CSRF;actor=admin;返 201 run-detail DTO。
		ar.Post("/projects/{id}/runs", makeManualRunHandler(rs, aud))

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

		// AI 生成流水线配置(Story 2.5):浅克隆分析 + LLM 生成提案 + 全/部分接受应用。
		// generate 复用 aiSvc + projects(p)+ vault(v);apply 复用 pipelines(pl)+ settings(ps)+ triggers(t)。
		// /pipeline/ai-generate、/pipeline/ai-apply 比 /pipeline 多一段,不会被吞;均为写方法,过 auth + CSRF。
		// 两路优雅降级(AI 未配 / clone 失败 / LLM 失败)均 HTTP 200,绝不 500、绝无明文密钥。
		aiGenDeps := aiGenerateDeps{
			analyzer: o.aiAnalyzer,
			aiSvc:    aiSvc,
			projects: p,
			pipes:    pl,
			settings: ps,
			triggers: t,
			vault:    v,
		}
		ar.Post("/projects/{id}/pipeline/ai-generate", makeAIGenerateHandler(aiGenDeps))
		ar.Post("/projects/{id}/pipeline/ai-apply", makeAIApplyHandler(aiGenDeps))

		// 只读源码读取(Story 3.6;FR-4 预埋,7-4 消费):go-git 浅克隆读 tree/blob。
		// 复用已注入 projects(p)+ vault(v)取凭据 + 注入的 SourceReader。reader 为 nil → 503。
		// /source/tree、/source/blob 比 /projects/{id} 多两段,不会被吞;GET 过 auth。
		srcDeps := sourceDeps{projects: p, vault: v, reader: o.sourceReader}
		ar.Get("/projects/{id}/source/tree", makeSourceTreeHandler(srcDeps))
		ar.Get("/projects/{id}/source/blob", makeSourceBlobHandler(srcDeps))

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
// 前端契约不变:它仍发送 header = devops_csrf cookie 值,而该 cookie 下发的正是
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
func writeError(w http.ResponseWriter, code int, errCode, msg string) {
	writeJSON(w, code, errBody{Error: errDetail{Code: errCode, Message: msg}})
}
