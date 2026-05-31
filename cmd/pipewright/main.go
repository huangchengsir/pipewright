// Command pipewright 是平台入口:加载配置 → 打开存储(应用迁移)→ 装配 HTTP → 启动。
// 整个平台为单静态二进制(前端经 go:embed 内嵌),无前后端分离部署。
package main

import (
	"context"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	root "github.com/huangchengsir/pipewright"
	"github.com/huangchengsir/pipewright/internal/ai"
	"github.com/huangchengsir/pipewright/internal/anomaly"
	"github.com/huangchengsir/pipewright/internal/approval"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/build"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/cron"
	"github.com/huangchengsir/pipewright/internal/dagrun"
	"github.com/huangchengsir/pipewright/internal/deploy"
	"github.com/huangchengsir/pipewright/internal/httpapi"
	"github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/notify"
	"github.com/huangchengsir/pipewright/internal/oauth"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/promotion"
	"github.com/huangchengsir/pipewright/internal/prstatus"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/target"
	"github.com/huangchengsir/pipewright/internal/trigger"
	"github.com/huangchengsir/pipewright/internal/vault"
)

func main() {
	cfg := config.Load()

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer func() { _ = st.Close() }()

	// 启动期 fail-fast:内嵌前端必须含 index.html,否则运行期白屏(空/错误 web/dist 也能编过)。
	webFS := root.WebFS()
	if _, err := fs.Stat(webFS, "index.html"); err != nil {
		log.Fatalf("embedded web/dist 缺少 index.html(前端未正确构建?): %v", err)
	}

	if abs, err := filepath.Abs(cfg.DBPath); err == nil {
		log.Printf("data db: %s", abs)
	}

	// 装配认证服务(nil clock → RealClock)并执行首次启动管理员引导。
	authSvc := auth.NewService(st.DB, nil)
	if err := authSvc.Bootstrap(cfg.AdminUsername, cfg.AdminPassword); err != nil {
		log.Fatalf("auth bootstrap: %v", err)
	}
	// 启动时清理 DB 中遗留的过期会话(惰性删除可能遗漏)。
	if n, err := authSvc.PurgeExpiredSessions(); err != nil {
		log.Printf("[auth] 警告:启动清理过期会话失败:%v", err)
	} else if n > 0 {
		log.Printf("[auth] 启动清理过期会话:%d 条", n)
	}

	// 装配凭据保险库(Story 1.3)。master key 缺失 → 未配置态(不 panic,平台仍启动);
	// 解码/长度错误 → 视为配置错误,提示但仍以未配置态继续(/healthz 保持可用)。
	var masterKey *[config.MasterKeyLen]byte
	switch key, err := config.LoadMasterKey(); {
	case err == nil:
		masterKey = key
		log.Printf("[vault] master key 已加载,凭据保险库可用")
	case errors.Is(err, config.ErrNoMasterKey):
		log.Printf("[vault] 警告:未配置 master key(PIPEWRIGHT_MASTER_KEY 或 _FILE),凭据保险库不可用。")
	default:
		// 不打印 err 之外的任何内容;err 本身不含 key 值。
		log.Printf("[vault] 警告:master key 配置无效(%v),凭据保险库不可用。", err)
	}
	credVault := vault.New(st.DB, masterKey)

	// 装配项目服务(Story 2.1):经 store 触库、经 vault 取凭据做 ls-remote 校验(进程内,用完即弃)。
	// prober 传 nil → 使用默认 go-git ListRemote 实现(纯 Go,无宿主 git 依赖,不驻留)。
	projectSvc := project.New(st.DB, credVault, nil)

	// 装配触发配置服务(Story 2.3):复用 vault secretbox(master key)加密 webhook 签名密钥。
	// master key 未配置时,涉及密钥的读取/重置降级为明确错误(不 panic)。
	triggerSvc := trigger.New(st.DB, credVault)

	// 装配流水线配置服务(Story 2.2):经 store 触库,惰性默认 + spec 校验 + YAML 渲染。
	pipelineSvc := pipeline.New(st.DB)

	// 装配构建/部署配置服务(Story 2.4):独立于 2-2 spec;经 vault 校验 secret 引用存在性 +
	// 回算掩码。master key 未配置时,涉及 secret 引用的保存降级为明确错误(不 panic)。
	pipelineSettingsSvc := pipeline.NewSettingsService(st.DB, credVault)

	// 装配定时(cron)触发配置服务(Story 8-6):每项目一份 cron 表达式 + 分支 + 启用开关。
	// 调度器(下方,pool 后)按启用配置分钟粒度扫描、到点经 run.Service 创建「定时」触发的运行。
	cronSvc := cron.NewService(st.DB)

	// 装配项目级并发上限配置服务(FR-8-10 并发/队列控制):每项目一份 max_concurrent。
	// 注入 worker pool 做准入(下方);经 /api/projects/{id}/concurrency 读写。
	concurrencySvc := run.NewConcurrencyService(st.DB)

	// 装配运行服务(Story 3.1):本期桩 runner 跑占位步骤打通状态机;真实构建/部署=3-3/4-x。
	// worker pool 在 aiSvc 装配后创建(以便注入 best-effort 自动诊断钩子,Story 7.2)。
	runSvc := run.New(st.DB)

	// 装配人工审批门协调器 + 持久层(Story 8-4):DAG 调度到 Gate 阶段时阻塞等待批准/拒绝。
	// 协调器为进程内(零持久状态);记录落 run_approvals 供 UI/审计/重启清理。
	approvalCoord := approval.New()
	approvalStore := approval.NewStore(st.DB)

	// 装配环境晋级流持久层(Story 8-7 / FR-8-7):环境链 + 逐环境变量/密钥 + 晋级记录。
	// 晋级审批门复用上面的 approvalCoord/approvalStore(不另起协调器)。
	promotionStore := promotion.NewStore(st.DB)
	// 重启清理:进程重启会丢失内存等待者,把残留 waiting_approval 运行清为 failed(孤儿不可恢复)。
	if n, err := runSvc.FailOrphanedRuns(context.Background()); err != nil {
		log.Printf("[run] 警告:清理孤儿运行失败:%v", err)
	} else if n > 0 {
		log.Printf("[run] 启动清理孤儿运行(waiting_approval/running):%d 条 → failed", n)
	}

	// 装配诊断反馈服务(Story 7.5;FR-26):诊断 👍/👎(👎 可附正确根因)持久化 + 准确率统计(知识库种子)。
	// 复用同一 *sql.DB(参数化 SQL);correctRootCause 脱敏在 httpapi 层(过 mask)+ 领域层长度上限兜底。
	// upsert by run_id(同 run 改判覆盖)。无 init 副作用、不抬高空载内存。
	feedbackSvc := run.NewFeedbackService(st.DB)

	// 装配 webhook 接收器(Story 3.2):按 token 定位项目触发配置、解密密钥验签、
	// 解析 Gitee 投递、按 events+分支映射匹配、去重后经 RunService 创建运行(入 pool 调度)。
	webhookReceiver := trigger.NewReceiver(st.DB, credVault, httpapi.NewRunCreator(runSvc))

	// 装配审计地基(Story 1.4):敏感操作成功后写 append-only 审计表(SQLite trigger 硬拦
	// UPDATE/DELETE)。Masker 在 detail 落库前脱敏(绝不写明文 secret);可选远端 sink
	// (PIPEWRIGHT_AUDIT_SINK)使本地被删后远端仍完整。sink 失败降级不阻断主操作。
	auditMasker := mask.NewMasker()
	var auditSink audit.Sink
	if sink, err := audit.SinkFromEnv(); err != nil {
		log.Printf("[audit] 警告:远端 sink 配置无效(%v),仅启用本地审计。", err)
	} else if sink != nil {
		auditSink = sink
		log.Printf("[audit] 远端审计 sink 已启用(PIPEWRIGHT_AUDIT_SINK)")
	}
	auditRec := audit.New(st.DB, auditMasker, auditSink)

	// 装配可配置 AI 提供商服务(Story 7.1):复用 vault secretbox 加密 apiKey(密文入库)。
	// Test 探测用带超时的 HTTP 客户端(~8s);master key 未配置时涉及密钥的操作降级为明确错误。
	// 未配置/未启用 → 下游优雅禁用,核心 CI/CD 不依赖此服务(NFR-10)。
	aiSvc := ai.New(st.DB, credVault, &http.Client{Timeout: 8 * time.Second})

	// 装配仓库分析器(Story 2.5):go-git 浅克隆(内存 storer + memfs,Depth:1)读 manifest
	// 检测技术栈;严格 SSRF 收口;克隆失败优雅降级(不致命)。无 init 副作用/不驻留。
	repoAnalyzer := ai.NewRepoAnalyzer()

	// 装配成功/失败差异计算器(Story 7.3;FR-25):go-git 克隆 + 两 commit tree diff;严格 SSRF 收口;
	// 克隆失败/commit 不可达优雅降级(不致命)。无 init 副作用/不驻留。
	runDiffer := ai.NewRunDiffer()

	// 装配进程内 worker pool(Story 3.1)+ best-effort 自动诊断钩子(Story 7.2)。
	// 钩子在 run→failed 后「取失败日志→脱敏→ai.Diagnose→保存」,经 Option 注入使 run 包不 import ai。
	// AI 未配/失败仅降级(status=unavailable),绝不阻断 run 终态(NFR-10)。pool 调度多 run 并行、结果独立。
	// 日志/诊断/反馈/通知出网脱敏器(Story 3.6/7-2/7-5/5-3;AC-SEC-04)——**红线修**:
	// 不再只登记桩假 secret,而是 per-run 解析该 run 真实用到的全部凭据(项目主凭据 + 流水线
	// settings 的 secret 构建/环境变量 + 镜像仓库凭据),从 vault Reveal 明文登记进 Masker。
	// 这样 3-3 真实构建落地后,真实日志/诊断/反馈/通知里的真凭据也一律 [MASKED],绝不明文外发。
	secretSrc := httpapi.NewRunSecretSource(runSvc, projectSvc, pipelineSettingsSvc, credVault)

	// 装配通知渠道服务(Story 5.1;FR-19):复用 vault secretbox 加密敏感字段(如 SMTP 密码,密文入库)。
	// webhook 投递用带超时的注入 HTTP 客户端(~8s);webhook URL 经 SSRF 收口(拒云元数据/链路本地)。
	// master key 未配置时,涉及敏感字段的保存/读取降级为明确错误(不 panic)。未启用渠道发送时优雅跳过。
	// 提前装配(先于 pool)以便把通知钩子注入 pool(Story 5.2;FR-20)。
	notifySvc := notify.New(st.DB, credVault, &http.Client{Timeout: 8 * time.Second})

	// 选择运行执行器(Story 3-3/3-5):按 PIPEWRIGHT_BUILDER 开关装配真实隔离构建器或桩。
	//   - real :强制真实 Builder(探测不到容器 CLI 即启动失败,诚实不假装)。
	//   - stub :强制桩 runner(合成日志,不碰容器;dev/CI 无 Docker 时用)。
	//   - 缺省 auto:探测到 docker/nerdctl/podman 用 real,否则回退桩(优雅降级,NFR-10)。
	// 真实 Builder 经构造函数注入 projectSvc/pipelineSettingsSvc/credVault;真实日志/产物/失败日志
	// 经同一 run.StepSink 喂出,复用既有持久化/SSE/脱敏(下方 WithLogMaskerFunc 对真实日志同样生效)。
	// PIPEWRIGHT_RUNNER=dag 时启用 DAG 调度执行器(Epic 8 · 8-1↔8-3):按阶段 needs 构 DAG 调度
	// (无依赖阶段并行、失败阻断下游),阶段编排经既有 StepSink 持久化。**默认关闭**(不破存量):
	// 缺省/其它值仍走 PIPEWRIGHT_BUILDER 选出的真实 Builder / 桩 runner。
	// ⚠ 当前阶段执行体为占位 stub(仅打日志即成功);真实「阶段内并行 job → job 内有序 step
	// 在隔离容器构建/部署」= Story 8-2 尚未接入,故 dag 模式现仅用于验证编排,不做真实构建。
	var runnerOpts []run.PoolOption
	if strings.EqualFold(strings.TrimSpace(os.Getenv("PIPEWRIGHT_RUNNER")), "dag") {
		// 阶段执行体(Story 8-2):探测到容器 CLI → 注入真实阶段执行器(script 类型 job 在隔离
		// 容器内真实跑命令,复用 Builder 的 cloner+driver);否则回退 stub(优雅降级,NFR-10)。
		var dagOpts []dagrun.Option
		// 审批门 hook(Story 8-4):Gate 阶段阻塞等待人工批准/拒绝。
		dagOpts = append(dagOpts, dagrun.WithGate(httpapi.NewApprovalGate(runSvc, approvalCoord, approvalStore)))
		if b, berr := build.NewBuilder(projectSvc, pipelineSettingsSvc, credVault); berr == nil {
			dagOpts = append(dagOpts, dagrun.WithStageExecutor(build.NewStageExecutor(b)))
			log.Printf("[run] PIPEWRIGHT_RUNNER=dag:DAG 调度执行器已启用(阶段按 needs 编排;script 类型 job 在隔离容器真实执行,CLI=%s;build_image/push_image/deploy_ssh 真实化=后续)", b.DriverBinary())
		} else {
			log.Printf("[run] PIPEWRIGHT_RUNNER=dag:DAG 调度执行器已启用(阶段按 needs 编排;⚠ 未探测到容器 CLI,阶段执行体回退 stub:%v)", berr)
		}
		runnerOpts = []run.PoolOption{run.WithRunner(dagrun.New(pipelineSvc, dagOpts...))}
	} else {
		runnerOpts = buildRunnerOption(projectSvc, pipelineSettingsSvc, credVault)
	}

	// 终态钩子:通知(Story 5.2);PIPEWRIGHT_PR_STATUS=1 时再叠加「PR 状态回写」(Story 8-9 / FR-8-9):
	// run 终态 → 据项目仓库识别 GitHub/Gitee → 经项目凭据回写该 commit 的提交状态(PR 检查)。
	// **默认关闭**(避免意外往用户真实仓库回写);开启时 best-effort,失败仅记日志不阻断 run 终态。
	terminalHook := httpapi.NewNotifyHook(runSvc, notifySvc, secretSrc)
	if strings.EqualFold(strings.TrimSpace(os.Getenv("PIPEWRIGHT_PR_STATUS")), "1") {
		// API base 可经 env 覆盖(GitHub/Gitee 企业版自托管;空则用公有云默认)。
		reporter := prstatus.NewReporter(&http.Client{Timeout: 10 * time.Second}).WithBaseURLs(
			strings.TrimSpace(os.Getenv("PIPEWRIGHT_PR_STATUS_GITHUB_BASE")),
			strings.TrimSpace(os.Getenv("PIPEWRIGHT_PR_STATUS_GITEE_BASE")))
		prHook := httpapi.NewPRStatusHook(runSvc, projectSvc, credVault, reporter,
			strings.TrimSpace(os.Getenv("PIPEWRIGHT_PUBLIC_URL")))
		notify := terminalHook
		terminalHook = func(ctx context.Context, runID, finalStatus string) {
			notify(ctx, runID, finalStatus)
			prHook(ctx, runID, finalStatus)
		}
		log.Printf("[prstatus] PR 状态回写已启用(PIPEWRIGHT_PR_STATUS=1;GitHub/Gitee,经项目凭据,best-effort)")
	}

	// 并发/队列控制(FR-8-10):全局上限经 PIPEWRIGHT_MAX_CONCURRENT 设置(默认 = worker 数);
	// 项目级上限经 concurrencySvc(/api/projects/{id}/concurrency 配置)。突发超限的运行保持 queued
	// 排队,待槽位释放再调度(FIFO)。非法/未设 env → 0,pool 回落到默认行为(仅受全局上限)。
	maxConcurrent := 0
	if v := strings.TrimSpace(os.Getenv("PIPEWRIGHT_MAX_CONCURRENT")); v != "" {
		if n, perr := strconv.Atoi(v); perr == nil && n > 0 {
			maxConcurrent = n
		} else {
			log.Printf("[run] 警告:PIPEWRIGHT_MAX_CONCURRENT=%q 非法(须为正整数),忽略", v)
		}
	}

	pool := run.NewWorkerPool(runSvc,
		append(runnerOpts,
			run.WithDiagnoseHook(httpapi.NewDiagnoseHook(runSvc, aiSvc, secretSrc)),
			// per-run 日志脱敏:每个 run 构建登记了该 run 真实凭据的 Masker(红线修)。
			run.WithLogMaskerFunc(secretSrc.MaskerForRun),
			// best-effort 终态钩子(通知 + 可选 PR 状态回写)。未配路由的事件不发送;绝不阻断 run 终态。
			run.WithNotifyHook(terminalHook),
			// 并发/队列控制(FR-8-10):全局上限 + 项目级上限准入。
			run.WithMaxConcurrent(maxConcurrent),
			run.WithConcurrencyLimits(concurrencySvc),
		)...,
	)
	pool.Start()
	log.Printf("[run] worker pool started")

	// 装配并启动定时(cron)调度器(Story 8-6):分钟粒度扫描启用的项目 cron,到点经 run.Service
	// 创建「定时」触发的运行(入 pool 调度)。同一项目同一分钟去重;表达式非法的项目跳过。
	// 无启用配置时空转无副作用。优雅停机随 worker pool 停。
	cronScheduler := cron.NewScheduler(cronSvc, httpapi.NewScheduledRunCreator(runSvc))
	cronScheduler.Start(context.Background())
	log.Printf("[cron] 定时调度器已启动")

	// 装配只读源码读取器(Story 3.6;FR-4 预埋):go-git 浅克隆读 tree/blob;严格 SSRF 收口;
	// 克隆失败优雅降级(不致命)。无 init 副作用/不驻留。
	sourceReader := httpapi.NewSourceReader()

	// 装配目标服务器服务(Story 4.1;internal/target,FR-14):通用 SSH exec/session 基座,
	// 供 Epic 4 部署 + Epic 6 运维共享。SSH 私钥/口令经 vault 即用即弃,绝不入库/日志/响应/错误。
	// dialer 传 nil → 使用默认 x/crypto/ssh 实现(无宿主依赖,不驻留)。master key 未配置时
	// Exec/Test 降级为明确错误(不 panic)。
	targetSvc := target.New(st.DB, credVault, nil)

	// 装配部署执行服务(Story 4.2;internal/deploy,FR-10):把成功运行的产物经 SSH 部署到目标机。
	// 部署命令 array 化经 targetSvc.Exec 执行(AC-SEC-02,不拼 shell);每机结果落 deploy_targets,
	// 据结果更新 run 终态(success/partial_failed/failed)。复用已装配的 targetSvc + runSvc,无新顶层依赖。
	// (notifySvc 已在 pool 装配前声明〔Story 5.2 注入 NotifyHook〕,此处不重复。)
	// Story 4.6(FR-22 种子):注入诊断钩子 → 部署失败(failed/partial_failed)合成失败日志 +
	// best-effort 触发 7-2 AI 诊断,让诊断飞轮覆盖部署失败(而非只覆盖构建失败)。复用 7-2 NewDiagnoseHook。
	deploySvc := deploy.New(targetSvc, runSvc, deploy.WithDiagnoseHook(httpapi.NewDiagnoseHook(runSvc, aiSvc, secretSrc)))

	// 装配可配置异常检测服务(Story 6.5;FR-23):按阈值规则对服务器指标做检测,命中产告警入库。
	// 检测复用 6-1 指标采集(NewAnomalyCollector 适配 collectServerMetrics);不可达/指标 null 的
	// 服务器跳过(不误报)。复用已装配的 targetSvc(采集),无新顶层依赖;无 init 副作用/不起轮询。
	anomalySvc := anomaly.New(st.DB, httpapi.NewAnomalyCollector(targetSvc))

	// 装配多 provider OAuth 凭据接入服务(连接 Gitee/GitHub/GitLab/自建):复用 vault secretbox 加密
	// OAuth 应用 client_secret(密文入库);OAuth 拿到的 access_token 经 vault.Create 存成 git_token 凭据,
	// 之后克隆/构建直接复用(无需改克隆逻辑)。Exchange 兑换用带超时的注入 HTTP 客户端(~10s);
	// state CSRF 短期内存绑会话。master key 未配置时涉及密钥的操作降级为明确错误(不 panic)。
	oauthSvc := oauth.New(st.DB, credVault, &http.Client{Timeout: 10 * time.Second})

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           httpapi.New(webFS, authSvc, httpapi.WithVault(credVault), httpapi.WithProjects(projectSvc), httpapi.WithTriggers(triggerSvc), httpapi.WithPipelines(pipelineSvc), httpapi.WithPipelineSettings(pipelineSettingsSvc), httpapi.WithRuns(runSvc, pool), httpapi.WithWebhooks(webhookReceiver), httpapi.WithAudit(auditRec), httpapi.WithAccount(authSvc), httpapi.WithAISettings(aiSvc), httpapi.WithAIGenerate(repoAnalyzer), httpapi.WithRunDiff(runDiffer), httpapi.WithSource(sourceReader), httpapi.WithServers(targetSvc), httpapi.WithDeploy(deploySvc), httpapi.WithNotifications(notifySvc), httpapi.WithDiagnosisFeedback(feedbackSvc), httpapi.WithAnomaly(anomalySvc), httpapi.WithSecretSource(secretSrc), httpapi.WithOAuth(oauthSvc), httpapi.WithCron(cronSvc), httpapi.WithApprovals(approvalCoord, approvalStore), httpapi.WithConcurrency(concurrencySvc), httpapi.WithPromotion(promotionStore)),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		// WriteTimeout 置 0:SSE 长连接(/api/runs/{id}/events)不可被写超时切断;
		// 各非流式 handler 自身在合理时间内完成,慢响应风险由 ReadTimeout/IdleTimeout 兜底。
		WriteTimeout: 0,
		IdleTimeout:  120 * time.Second,
	}

	// 优雅停机:SIGINT/SIGTERM 后 Shutdown,放行在途请求并让 defer st.Close() 生效。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("pipewright listening on %s", cfg.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server: %v", err)
		}
	}()

	<-ctx.Done()
	stop()
	log.Printf("shutting down…")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}
	// HTTP 停机后停 worker pool:取消在途运行执行,等 worker 退出(随 shutdownCtx 超时)。
	cronScheduler.Stop()
	pool.Stop(shutdownCtx)
	log.Printf("[run] worker pool stopped")
}

// buildRunnerOption 按 PIPEWRIGHT_BUILDER 开关返回注入 pool 的运行执行器选项(Story 3-3/3-5)。
//
//   - real :强制真实 Builder;构造失败(无容器 CLI)直接 fail-fast(用户显式要真实却没环境,应明确报错)。
//   - stub :不注入 WithRunner(pool 默认 StubRunner),返回空选项。
//   - 其它/缺省 auto:尝试构造真实 Builder,探测不到容器 CLI 时回退桩(优雅降级,NFR-10)。
//
// 返回 []run.PoolOption(可能为空):空 ⇒ 用 pool 默认 StubRunner。
func buildRunnerOption(projectSvc project.Service, settingsSvc pipeline.SettingsService, v vault.Vault) []run.PoolOption {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("PIPEWRIGHT_BUILDER")))
	switch mode {
	case "stub":
		log.Printf("[build] PIPEWRIGHT_BUILDER=stub:使用桩 runner(合成日志,不碰容器)")
		return nil
	case "real":
		b, err := build.NewBuilder(projectSvc, settingsSvc, v)
		if err != nil {
			log.Fatalf("[build] PIPEWRIGHT_BUILDER=real 但构建器不可用:%v", err)
		}
		log.Printf("[build] 真实隔离构建器已启用(容器 CLI=%s)", b.DriverBinary())
		return []run.PoolOption{run.WithRunner(b)}
	default:
		b, err := build.NewBuilder(projectSvc, settingsSvc, v)
		if err != nil {
			log.Printf("[build] 未探测到容器 CLI(docker/nerdctl/podman),回退桩 runner(PIPEWRIGHT_BUILDER=real 可强制要求真实):%v", err)
			return nil
		}
		log.Printf("[build] auto:真实隔离构建器已启用(容器 CLI=%s)", b.DriverBinary())
		return []run.PoolOption{run.WithRunner(b)}
	}
}
