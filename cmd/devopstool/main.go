// Command devopstool 是平台入口:加载配置 → 打开存储(应用迁移)→ 装配 HTTP → 启动。
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

	root "github.com/huangjiawei/devopstool"
	"github.com/huangjiawei/devopstool/internal/auth"
	"github.com/huangjiawei/devopstool/internal/config"
	"github.com/huangjiawei/devopstool/internal/httpapi"
	"github.com/huangjiawei/devopstool/internal/pipeline"
	"github.com/huangjiawei/devopstool/internal/project"
	"github.com/huangjiawei/devopstool/internal/run"
	"github.com/huangjiawei/devopstool/internal/store"
	"github.com/huangjiawei/devopstool/internal/trigger"
	"github.com/huangjiawei/devopstool/internal/vault"
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
		log.Printf("[vault] 警告:未配置 master key(DEVOPSTOOL_MASTER_KEY 或 _FILE),凭据保险库不可用。")
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

	// 装配运行服务 + 进程内 worker pool(Story 3.1):本期桩 runner 跑占位步骤打通状态机;
	// 真实构建/部署执行=3-3/4-x 换 Runner 实现。pool 调度多 run 并行、结果独立。
	runSvc := run.New(st.DB)
	pool := run.NewWorkerPool(runSvc)
	pool.Start()
	log.Printf("[run] worker pool started")

	// 装配 webhook 接收器(Story 3.2):按 token 定位项目触发配置、解密密钥验签、
	// 解析 Gitee 投递、按 events+分支映射匹配、去重后经 RunService 创建运行(入 pool 调度)。
	webhookReceiver := trigger.NewReceiver(st.DB, credVault, httpapi.NewRunCreator(runSvc))

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           httpapi.New(webFS, authSvc, httpapi.WithVault(credVault), httpapi.WithProjects(projectSvc), httpapi.WithTriggers(triggerSvc), httpapi.WithPipelines(pipelineSvc), httpapi.WithRuns(runSvc, pool), httpapi.WithWebhooks(webhookReceiver)),
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
		log.Printf("devopstool listening on %s", cfg.Addr)
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
