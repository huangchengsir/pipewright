package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/deploy"
	"github.com/huangchengsir/pipewright/internal/run"
)

// ---- 冻结 run-detail targets 子 DTO(Story 4.2;填 3-1 留的 null slot) -------
//
// 形状由本 story 定义并冻结:每台目标机一张结果。部署过 → 数组;否则 null。
// status 枚举:pending | deploying | success | failed | rolled_back(冻结)。
// 4-4 回滚置 rolled_back;4-5 多机扇出新增多条;均不改此形状。message 人读(绝无明文密钥)。

// targetDTO 是单台目标机的部署结果(run-detail targets slot 元素)。
type targetDTO struct {
	ServerID   string  `json:"serverId"`
	ServerName string  `json:"serverName"`
	Status     string  `json:"status"`               // pending|deploying|success|failed|rolled_back(冻结枚举)
	Message    string  `json:"message"`              // 人读摘要(绝无明文密钥)
	StartedAt  string  `json:"startedAt"`            // RFC3339
	FinishedAt *string `json:"finishedAt,omitempty"` // RFC3339;未结束为 null
}

func toTargetDTO(t run.DeployTarget) targetDTO {
	return targetDTO{
		ServerID:   t.ServerID,
		ServerName: t.ServerName,
		Status:     t.Status,
		Message:    t.Message,
		StartedAt:  t.StartedAt.UTC().Format(time.RFC3339),
		FinishedAt: rfc3339Ptr(t.FinishedAt),
	}
}

// toTargetDTOs 把领域部署目标切片映射为 DTO 切片(始终非 nil;空 → [])。
func toTargetDTOs(ts []run.DeployTarget) []targetDTO {
	out := make([]targetDTO, 0, len(ts))
	for i := range ts {
		out = append(out, toTargetDTO(ts[i]))
	}
	return out
}

// healthCheckDTO 是部署后健康门控配置(Story 4.3 / FR-12;冻结子结构)。
// type=none / 缺省 → 不做健康检查(向后兼容 4-2)。retries/interval/timeout 的
// 默认值与上限由领域层 deploy.HealthCheck 归一(retries 默认 3、≤20;interval 默认 3s;
// timeout 默认 5s、≤60s)。命令 array 化经 target.Exec 执行(AC-SEC-02,不拼 shell)。
type healthCheckDTO struct {
	Type            string   `json:"type"`            // none | http | command
	URL             string   `json:"url"`             // type=http
	Command         []string `json:"command"`         // type=command(array)
	Retries         int      `json:"retries"`         // 默认 3,上限 20
	IntervalSeconds int      `json:"intervalSeconds"` // 默认 3
	TimeoutSeconds  int      `json:"timeoutSeconds"`  // 默认 5,上限 60
}

// deployRequest 是 POST /api/runs/{id}/deploy 请求体。
// 4-3 扩展:可选 healthCheck(部署后健康门控);targets 子 DTO 形状不变(4-2 冻结)。
// 4-4 扩展:零停机切换 / 回滚的可选参数经既有 deployConfig map 透传(不加新字段 / 路由):
//   - deployConfig["releaseBase"] : 发布根目录 <base>(缺省从 path 推导);dist/jar 落 <base>/releases/<runId>,current 软链落 <base>/current。
//   - deployConfig["keepReleases"]: 额外保留旧发布份数(缺省 1,上限 50)。
type deployRequest struct {
	ArtifactID   string            `json:"artifactId"`
	ServerIDs    []string          `json:"serverIds"`
	DeployConfig map[string]string `json:"deployConfig"`
	HealthCheck  *healthCheckDTO   `json:"healthCheck"`
	// Strategy 是部署策略(Story 8-8 / FR-8-8):rolling(默认)| canary | blue_green。
	// 空 / 未知 → rolling。金丝雀批量经 deployConfig["canaryCount"|"canaryPercent"] 透传。
	Strategy string `json:"strategy"`
}

// toHealthCheck 把请求 DTO 映射为领域 HealthCheck(nil / type=none → nil,跳过健康检查)。
func toHealthCheck(h *healthCheckDTO) *deploy.HealthCheck {
	if h == nil || h.Type == "" || h.Type == deploy.HealthCheckNone {
		return nil
	}
	return &deploy.HealthCheck{
		Type:            h.Type,
		URL:             h.URL,
		Command:         h.Command,
		Retries:         h.Retries,
		IntervalSeconds: h.IntervalSeconds,
		TimeoutSeconds:  h.TimeoutSeconds,
	}
}

// deployResponse 是 POST /api/runs/{id}/deploy 响应体:本期同步执行,返回填好的 targets 数组。
type deployResponse struct {
	Targets []targetDTO `json:"targets"`
}

// makeDeployRunHandler 返回 POST /api/runs/{id}/deploy handler(认证 + CSRF)。
//
// 取 run + 产物 + 服务器 → deploy.Deploy(逐机经 SSH 执行部署命令)→ 据结果更新 run 终态
// → 返回每机 targets。本期同步执行返回最终 targets(简单可验)。
//
// run 不存在 → 404;run 非成功 / 无该产物 / 服务器不存在 / 未指定服务器 → 422(人读)。
// **部署执行失败不 500**:每机 status=failed 记录,整体 200(由 deploy.Deploy 保证不上抛执行错误)。
func makeDeployRunHandler(svc deploy.Service, runSvc run.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil || runSvc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "部署服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")

		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		var req deployRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}

		results, err := svc.Deploy(r.Context(), deploy.DeployInput{
			RunID:       id,
			ArtifactID:  req.ArtifactID,
			ServerIDs:   req.ServerIDs,
			Config:      req.DeployConfig,
			HealthCheck: toHealthCheck(req.HealthCheck),
			Strategy:    req.Strategy,
		})
		if err != nil {
			writeDeployError(w, err)
			return
		}

		// 部署后回读权威持久化结果(填 run-detail targets slot 同源);保证响应与详情一致。
		targets, lerr := runSvc.ListDeployTargets(r.Context(), id)
		if lerr != nil {
			// 回读失败:降级用执行结果直接映射(不阻断已成功的部署)。
			out := make([]targetDTO, 0, len(results))
			for i := range results {
				out = append(out, targetResultToDTO(results[i]))
			}
			writeJSON(w, http.StatusOK, deployResponse{Targets: out})
			return
		}
		writeJSON(w, http.StatusOK, deployResponse{Targets: toTargetDTOs(targets)})
	}
}

// targetResultToDTO 把 deploy.TargetResult 映射为 DTO(回读降级路径用)。
func targetResultToDTO(r deploy.TargetResult) targetDTO {
	var finished *string
	if r.FinishedAt != nil {
		s := r.FinishedAt.UTC().Format(time.RFC3339)
		finished = &s
	}
	return targetDTO{
		ServerID:   r.ServerID,
		ServerName: r.ServerName,
		Status:     r.Status,
		Message:    r.Message,
		StartedAt:  r.StartedAt.UTC().Format(time.RFC3339),
		FinishedAt: finished,
	}
}

// retryDeployRequest 是 POST /api/runs/{id}/deploy/retry 请求体(Story 4.5「仅重试失败目标」)。
//
// 无迁移、复用 deploy_targets:retry 不持久化原始部署产物/配置,故由前端(已持有上次部署表单)
// 随请求带回 artifactId + deployConfig + healthCheck,复用既有 deployOne 链路。serverIds 可选:
// 省略 → 重试该 run 当前所有 failed/rolled_back 目标;给定 → 只重试其中已有失败的目标。
type retryDeployRequest struct {
	ArtifactID   string            `json:"artifactId"`
	ServerIDs    []string          `json:"serverIds"`
	DeployConfig map[string]string `json:"deployConfig"`
	HealthCheck  *healthCheckDTO   `json:"healthCheck"`
}

// makeRetryDeployHandler 返回 POST /api/runs/{id}/deploy/retry handler(认证 + CSRF)。
//
// 取 run 的失败/回滚目标 → 复用产物 + 配置并行重跑 deployOne → 逐目标 upsert(成功目标不动)
// → 重算 run 终态 → 返回该 run 全量最新 targets。
//
// run 不存在 → 404;run 非失败 / 无失败目标 / 未部署过 / 无该产物 / 服务器不存在 → 422(人读)。
// **重试执行失败不 500**:被重试目标 status=failed 记录,整体 200(由 deploy.RetryFailed 保证不上抛执行错误)。
func makeRetryDeployHandler(svc deploy.Service, runSvc run.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil || runSvc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "部署服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")

		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		var req retryDeployRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}

		results, err := svc.RetryFailed(r.Context(), deploy.RetryInput{
			RunID:       id,
			ArtifactID:  req.ArtifactID,
			ServerIDs:   req.ServerIDs,
			Config:      req.DeployConfig,
			HealthCheck: toHealthCheck(req.HealthCheck),
		})
		if err != nil {
			writeDeployError(w, err)
			return
		}

		// 重试后回读权威持久化结果(与 run-detail targets slot 同源);保证响应与详情一致。
		targets, lerr := runSvc.ListDeployTargets(r.Context(), id)
		if lerr != nil {
			out := make([]targetDTO, 0, len(results))
			for i := range results {
				out = append(out, targetResultToDTO(results[i]))
			}
			writeJSON(w, http.StatusOK, deployResponse{Targets: out})
			return
		}
		writeJSON(w, http.StatusOK, deployResponse{Targets: toTargetDTOs(targets)})
	}
}

// writeDeployError 把部署定位类错误映射为契约错误码 / 状态码(绝不回显明文 / 私钥 / 口令 / 栈)。
// 执行类失败由 deploy.Deploy 内化为每机 failed,不走此路径(整体 200)。
func writeDeployError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, deploy.ErrRunNotFound), errors.Is(err, run.ErrNotFound):
		writeError(w, http.StatusNotFound, "run_not_found", "运行不存在")
	case errors.Is(err, deploy.ErrRunNotSuccessful):
		writeError(w, http.StatusUnprocessableEntity, "run_not_successful", "运行非成功态,无可部署产物")
	case errors.Is(err, deploy.ErrArtifactNotFound):
		writeError(w, http.StatusUnprocessableEntity, "artifact_not_found", "该运行下不存在指定产物")
	case errors.Is(err, deploy.ErrServerNotFound):
		writeError(w, http.StatusUnprocessableEntity, "server_not_found", "目标服务器不存在")
	case errors.Is(err, deploy.ErrNoServers):
		writeError(w, http.StatusUnprocessableEntity, "no_servers", "请至少选择一台目标服务器")
	case errors.Is(err, deploy.ErrNoFailedTargets):
		writeError(w, http.StatusUnprocessableEntity, "no_failed_targets", "该运行没有可重试的失败目标")
	case errors.Is(err, deploy.ErrRunNotDeployed):
		writeError(w, http.StatusUnprocessableEntity, "run_not_deployed", "该运行尚未部署过,无可重试目标")
	default:
		writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
	}
}
