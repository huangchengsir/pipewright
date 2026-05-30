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
	"syscall"
	"time"

	root "github.com/huangchengsir/pipewright"
	"github.com/huangchengsir/pipewright/internal/ai"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/deploy"
	"github.com/huangchengsir/pipewright/internal/httpapi"
	"github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/notify"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/project"
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

	// 装配运行服务(Story 3.1):本期桩 runner 跑占位步骤打通状态机;真实构建/部署=3-3/4-x。
	// worker pool 在 aiSvc 装配后创建(以便注入 best-effort 自动诊断钩子,Story 7.2)。
	runSvc := run.New(st.DB)

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
	// 日志脱敏器(Story 3.6 / AC-SEC-04):dbStepSink.Log 落 run_logs / 发 SSE 前一律 Scrub。
	// 本期同 7-2 的 registerRunSecrets 债:只登记桩假 secret;3-3/3-6 真实日志落地后改为从
	// vault 取该 run 实际凭据明文登记(届时凭 run→项目→凭据解析)。
	logMasker := mask.NewMasker()
	logMasker.RegisterSecret(run.StubFailureSecret)

	// 装配通知渠道服务(Story 5.1;FR-19):复用 vault secretbox 加密敏感字段(如 SMTP 密码,密文入库)。
	// webhook 投递用带超时的注入 HTTP 客户端(~8s);webhook URL 经 SSRF 收口(拒云元数据/链路本地)。
	// master key 未配置时,涉及敏感字段的保存/读取降级为明确错误(不 panic)。未启用渠道发送时优雅跳过。
	// 提前装配(先于 pool)以便把通知钩子注入 pool(Story 5.2;FR-20)。
	notifySvc := notify.New(st.DB, credVault, &http.Client{Timeout: 8 * time.Second})

	pool := run.NewWorkerPool(runSvc,
		run.WithDiagnoseHook(httpapi.NewDiagnoseHook(runSvc, aiSvc)),
		run.WithLogMasker(logMasker),
		// best-effort 通知钩子(Story 5.2;FR-20):run 终态 → event → 构 Payload → Router.RouteEvent。
		// 未配路由的事件不发送;单渠道失败续发;绝不阻断 run 终态。run 包不 import notify(钩子解耦)。
		run.WithNotifyHook(httpapi.NewNotifyHook(runSvc, notifySvc)),
	)
	pool.Start()
	log.Printf("[run] worker pool started")

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
	deploySvc := deploy.New(targetSvc, runSvc)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           httpapi.New(webFS, authSvc, httpapi.WithVault(credVault), httpapi.WithProjects(projectSvc), httpapi.WithTriggers(triggerSvc), httpapi.WithPipelines(pipelineSvc), httpapi.WithPipelineSettings(pipelineSettingsSvc), httpapi.WithRuns(runSvc, pool), httpapi.WithWebhooks(webhookReceiver), httpapi.WithAudit(auditRec), httpapi.WithAccount(authSvc), httpapi.WithAISettings(aiSvc), httpapi.WithAIGenerate(repoAnalyzer), httpapi.WithRunDiff(runDiffer), httpapi.WithSource(sourceReader), httpapi.WithServers(targetSvc), httpapi.WithDeploy(deploySvc), httpapi.WithNotifications(notifySvc), httpapi.WithDiagnosisFeedback(feedbackSvc)),
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
	pool.Stop(shutdownCtx)
	log.Printf("[run] worker pool stopped")
}
