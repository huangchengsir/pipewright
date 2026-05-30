// Package deploy 是「SSH 部署执行」领域层(FR-10 / Story 4.2)。
//
// 它把一次成功运行的某个产物经 SSH 部署到一台或多台目标服务器,记录每机结果,并据结果
// 更新 run 终态。部署命令一律 **array 化([]string)**:经注入的 target.Service.Exec
// 执行(各参数由 target 层 shell 转义后再交 SSH session),绝不拼接原始 shell 字符串
// (AC-SEC-02,杜绝命令注入)。SSH 密钥经 vault 由 target 层即用即弃,本层绝不接触明文;
// 输出 / message 绝无明文密钥。
//
// 边界(本期不做):零停机切换 / 回滚 = 4-4;多机并行扇出细节 = 4-5(本期顺序执行,失败
// 不连累其它机)。本层 import run(取产物 + 写部署结果 + 更新终态)与 target(SSH 执行);
// run 包**不** import deploy(避免环)。
package deploy

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/target"
)

// 领域错误。错误体永不含明文 / 私钥 / 口令 / 内部栈。
var (
	// ErrRunNotFound 表示运行不存在。
	ErrRunNotFound = errors.New("deploy: run not found")
	// ErrRunNotSuccessful 表示运行非成功态,不可部署。
	ErrRunNotSuccessful = errors.New("deploy: run is not in a successful state")
	// ErrArtifactNotFound 表示该 run 下无指定产物。
	ErrArtifactNotFound = errors.New("deploy: artifact not found for run")
	// ErrServerNotFound 表示指定的目标服务器不存在。
	ErrServerNotFound = errors.New("deploy: target server not found")
	// ErrNoServers 表示未指定任何目标服务器。
	ErrNoServers = errors.New("deploy: no target servers specified")
)

// truncateLen 是写入 message 的命令输出最大长度(防超大输出撑爆响应 / 内存)。
const truncateLen = 800

// execTimeout 是单台部署的执行超时(防一台挂死拖垮整次部署;失败不连累其它机)。
const execTimeout = 60 * time.Second

// DeployInput 是一次部署请求(对齐 POST /api/runs/{id}/deploy 请求体)。
type DeployInput struct {
	RunID      string
	ArtifactID string
	ServerIDs  []string
	// Config 是可选部署参数(如目标路径);本期最小消费(dist 用 Path 决定部署目录)。
	Config map[string]string
	// HealthCheck 是可选的部署后健康门控(Story 4.3 / FR-12)。
	// nil 或 type=none → 跳过(向后兼容 4-2:部署命令成功即 success)。
	HealthCheck *HealthCheck
}

// TargetResult 是一台目标机的部署结果(与 run.DeployTarget 同形,供 HTTP 层映射回 DTO)。
type TargetResult struct {
	ServerID   string
	ServerName string
	Status     string // run.Target* 枚举:success | failed | …
	Message    string // 人读摘要(绝无明文密钥)
	StartedAt  time.Time
	FinishedAt *time.Time
}

// Service 定义部署执行对外接口(冻结点由 httpapi 层 DTO 承载;本接口可演进)。
type Service interface {
	// Deploy 取 run 的指定产物 → 校验成功态 / 产物 / 服务器 → 逐机经 SSH 执行该产物类型的
	// 部署命令 → 持久化每机结果 → 据结果更新 run 终态 → 返回每机结果。
	//
	// 定位类错误(run 非成功 / 无产物 / 服务器不存在 / 未指定服务器)上抛,供 HTTP 层 422/404。
	// **执行失败不上抛**:该机 status=failed + 人读 message,整体仍返回结果(整体 200)。
	Deploy(ctx context.Context, in DeployInput) ([]TargetResult, error)
}

// service 是 run + target 支撑的 Service 实现。
type service struct {
	targets target.Service
	runs    run.Service
}

// New 构造部署 Service。
//   - targetSvc:通用 SSH 执行层(Story 4.1),部署命令经其 Exec 执行(array 不拼 shell)。
//   - runSvc   :运行领域层,取产物 / 写部署结果 / 更新终态。
//
// 不做任何重活(无 init() 副作用,避免抬高空载内存)。
func New(targetSvc target.Service, runSvc run.Service) Service {
	return &service{targets: targetSvc, runs: runSvc}
}

func (s *service) Deploy(ctx context.Context, in DeployInput) ([]TargetResult, error) {
	if len(in.ServerIDs) == 0 {
		return nil, ErrNoServers
	}

	// 1) 校验 run 存在 + 成功态。
	rn, err := s.runs.Get(ctx, in.RunID)
	if err != nil {
		if errors.Is(err, run.ErrNotFound) {
			return nil, ErrRunNotFound
		}
		return nil, err
	}
	if rn.Status != run.StatusSuccess && rn.Status != run.StatusPartialFailed {
		// 仅成功 / 部分成功的运行有可部署产物;进行中 / 失败 / 排队 → 不可部署。
		return nil, ErrRunNotSuccessful
	}

	// 2) 定位产物(必须属于该 run)。
	arts, err := s.runs.ListArtifacts(ctx, in.RunID)
	if err != nil {
		return nil, err
	}
	var artifact *run.Artifact
	for i := range arts {
		if arts[i].ID == in.ArtifactID {
			a := arts[i]
			artifact = &a
			break
		}
	}
	if artifact == nil {
		return nil, ErrArtifactNotFound
	}

	// 3) 预解析所有目标服务器(任一不存在 → 422,整次拒绝,不留半截)。
	servers := make([]*target.Server, 0, len(in.ServerIDs))
	for _, sid := range in.ServerIDs {
		srv, gerr := s.targets.Get(ctx, sid)
		if gerr != nil {
			if errors.Is(gerr, target.ErrNotFound) {
				return nil, ErrServerNotFound
			}
			return nil, gerr
		}
		servers = append(servers, srv)
	}

	// 4) 逐机执行部署命令(顺序;失败不连累其它机。多机并行细节留 4-5)。
	results := make([]TargetResult, 0, len(servers))
	for _, srv := range servers {
		results = append(results, s.deployOne(ctx, srv, *artifact, in.Config, in.HealthCheck))
	}

	// 5) 持久化每机结果(填 run-detail targets slot)。
	dts := make([]run.DeployTarget, 0, len(results))
	for _, r := range results {
		dts = append(dts, run.DeployTarget{
			RunID:      in.RunID,
			ServerID:   r.ServerID,
			ServerName: r.ServerName,
			Status:     r.Status,
			Message:    r.Message,
			StartedAt:  r.StartedAt,
			FinishedAt: r.FinishedAt,
		})
	}
	if err := s.runs.SaveDeployTargets(ctx, in.RunID, dts); err != nil {
		return nil, err
	}

	// 6) 据结果置 run 终态:全成功 → success;有失败 → partial_failed;全失败 → failed。
	if err := s.runs.SetDeployTerminal(ctx, in.RunID, overallStatus(results)); err != nil {
		return nil, err
	}

	return results, nil
}

// deployOne 在一台目标机上构造并执行该产物类型的部署命令,返回该机结果。
// 部署命令全部成功后,若配置了健康检查(Story 4.3),再经同一 Exec 链路做健康门控:
// 探测通过 → success(message 含"健康检查通过");重试耗尽仍失败 → failed + 人读 message。
// 执行错误**不上抛**:映射为 status=failed + 人读 message(绝无明文密钥)。
func (s *service) deployOne(ctx context.Context, srv *target.Server, a run.Artifact, cfg map[string]string, hc *HealthCheck) TargetResult {
	// dist / jar 走「发布目录 + current 软链原子切换 + 健康门控 + 失败回滚」零停机模式(Story 4.4)。
	// image 本期仍 docker run、archive 维持文件部署,走下方既有命令链路。
	if releaseModeArtifact(a) {
		return s.deployReleaseOne(ctx, srv, a, cfg, hc)
	}

	started := time.Now().UTC()
	res := TargetResult{
		ServerID:   srv.ID,
		ServerName: srv.Name,
		StartedAt:  started,
	}

	cmds, summary, berr := buildCommands(a, cfg)
	if berr != nil {
		finish := time.Now().UTC()
		res.Status = run.TargetFailed
		res.Message = berr.Error()
		res.FinishedAt = &finish
		return res
	}

	execCtx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()

	for _, cmd := range cmds {
		out, eerr := s.targets.Exec(execCtx, srv.ID, cmd)
		if eerr != nil {
			finish := time.Now().UTC()
			res.Status = run.TargetFailed
			res.Message = humanExecError(eerr)
			res.FinishedAt = &finish
			return res
		}
		if out != nil && out.ExitCode != 0 {
			finish := time.Now().UTC()
			res.Status = run.TargetFailed
			// 命令本身非零退出:回显 stderr 摘要(target 层执行的是平台构造的命令,
			// 不含凭据明文;仍截断防超大输出)。
			res.Message = fmt.Sprintf("部署命令退出码 %d:%s", out.ExitCode, truncate(strings.TrimSpace(out.Stderr)))
			res.FinishedAt = &finish
			return res
		}
	}

	// 部署命令全部成功 → 若配置了健康检查,做部署后健康门控(Story 4.3 / FR-12)。
	// 探测在部署命令成功之后跑;每机独立;经同一 target.Exec 链路(array 不拼 shell)。
	if hc.enabled() {
		if herr := s.runHealthCheck(execCtx, srv.ID, hc); herr != nil {
			finish := time.Now().UTC()
			res.Status = run.TargetFailed
			res.Message = herr.Error()
			res.FinishedAt = &finish
			return res
		}
		finish := time.Now().UTC()
		res.Status = run.TargetSuccess
		res.Message = summary + "(健康检查通过)"
		res.FinishedAt = &finish
		return res
	}

	finish := time.Now().UTC()
	res.Status = run.TargetSuccess
	res.Message = summary
	res.FinishedAt = &finish
	return res
}

// overallStatus 据每机结果聚合 run 终态:
//
//	全成功 → success;有成功有失败 → partial_failed;全失败 → failed。
func overallStatus(results []TargetResult) string {
	anySuccess, anyFailed := false, false
	for _, r := range results {
		switch r.Status {
		case run.TargetSuccess:
			anySuccess = true
		default:
			anyFailed = true
		}
	}
	switch {
	case anyFailed && anySuccess:
		return run.StatusPartialFailed
	case anyFailed:
		return run.StatusFailed
	default:
		return run.StatusSuccess
	}
}

// truncate 截断字符串到 truncateLen(防超大输出)。
func truncate(s string) string {
	if len(s) <= truncateLen {
		return s
	}
	return s[:truncateLen] + "…(已截断)"
}

// humanExecError 把 target 层执行错误映射为人读文案(绝不含凭据明文 / 内部栈)。
// target.Exec 已把连接 / 认证类错误映射为领域错误,本层进一步人读化。
func humanExecError(err error) string {
	switch {
	case errors.Is(err, target.ErrAuth):
		return "SSH 认证失败:密钥或口令无效,或无登录权限"
	case errors.Is(err, target.ErrUnreachable):
		return "无法连接服务器:端口未开放、主机不可达或超时"
	case errors.Is(err, target.ErrVaultUnconfigured):
		return "保险库未配置 master key,无法取 SSH 凭据"
	case errors.Is(err, target.ErrCredentialNotFound):
		return "引用的 SSH 凭据不存在"
	case errors.Is(err, context.DeadlineExceeded):
		return "部署执行超时"
	default:
		// 兜底:不泄漏内部细节(target 层错误体已无凭据明文)。
		return "部署执行失败"
	}
}
