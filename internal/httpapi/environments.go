package httpapi

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/deploy"
	"github.com/huangchengsir/pipewright/internal/environments"
	"github.com/huangchengsir/pipewright/internal/run"
)

// environments.go 暴露「环境一等公民」只读聚合 + 一键回滚端点(对标 GitLab environments)。
//
//	GET  /api/projects/{id}/environments/deployments        → 按环境聚合的部署时间线(每环境最近 N 次 + 活跃版本)
//	GET  /api/projects/{id}/environments/{env}/history       → 单环境时间线
//	POST /api/projects/{id}/environments/{env}/rollback      → 一键回滚到上一次成功部署(需 CSRF)
//
// 聚合是纯查询既有表(零迁移);回滚定位由 environments.Service 完成,执行复用既有 deploy.Service.Deploy
// 链路(重发上一次成功部署的同一产物到同一组目标机)。env 路径段经 URL 解码(chi 已解)。

// ---- 只读聚合 DTO --------------------------------------------------------------

type envTargetDTO struct {
	ServerID   string `json:"serverId"`
	ServerName string `json:"serverName"`
	Status     string `json:"status"`
}

type envArtifactDTO struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Name      string `json:"name"`
	Reference string `json:"reference"`
}

type envDeploymentDTO struct {
	RunID       string           `json:"runId"`
	Status      string           `json:"status"`
	Commit      string           `json:"commit"`
	Branch      string           `json:"branch"`
	TriggeredBy string           `json:"triggeredBy"`
	DeployedAt  string           `json:"deployedAt"`
	Active      bool             `json:"active"`
	Targets     []envTargetDTO   `json:"targets"`
	Artifacts   []envArtifactDTO `json:"artifacts"`
}

type envTimelineDTO struct {
	Environment string             `json:"environment"`
	Active      *envDeploymentDTO  `json:"active"`
	Deployments []envDeploymentDTO `json:"deployments"`
}

func toEnvDeploymentDTO(d environments.Deployment) envDeploymentDTO {
	targets := make([]envTargetDTO, 0, len(d.Targets))
	for _, t := range d.Targets {
		targets = append(targets, envTargetDTO{ServerID: t.ServerID, ServerName: t.ServerName, Status: t.Status})
	}
	arts := make([]envArtifactDTO, 0, len(d.Artifacts))
	for _, a := range d.Artifacts {
		arts = append(arts, envArtifactDTO{ID: a.ID, Type: a.Type, Name: a.Name, Reference: a.Reference})
	}
	return envDeploymentDTO{
		RunID:       d.RunID,
		Status:      d.Status,
		Commit:      d.Commit,
		Branch:      d.Branch,
		TriggeredBy: d.TriggeredBy,
		DeployedAt:  d.DeployedAt,
		Active:      d.Active,
		Targets:     targets,
		Artifacts:   arts,
	}
}

func toEnvTimelineDTO(tl environments.EnvironmentTimeline) envTimelineDTO {
	deps := make([]envDeploymentDTO, 0, len(tl.Deployments))
	for i := range tl.Deployments {
		deps = append(deps, toEnvDeploymentDTO(tl.Deployments[i]))
	}
	out := envTimelineDTO{Environment: tl.Environment, Deployments: deps}
	if tl.Active != nil {
		a := toEnvDeploymentDTO(*tl.Active)
		out.Active = &a
	}
	return out
}

func writeEnvError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, environments.ErrProjectNotFound):
		writeError(w, http.StatusNotFound, "project_not_found", "项目不存在")
	case errors.Is(err, environments.ErrEnvNotFound):
		writeError(w, http.StatusNotFound, "environment_not_found", "该环境暂无部署历史")
	case errors.Is(err, environments.ErrNoRollbackTarget):
		writeError(w, http.StatusUnprocessableEntity, "no_rollback_target", "该环境没有可回滚的上一次成功部署")
	default:
		writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
	}
}

// makeListEnvironmentDeploymentsHandler 返回 GET /api/projects/{id}/environments/deployments。
func makeListEnvironmentDeploymentsHandler(svc *environments.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "环境服务未初始化")
			return
		}
		tls, err := svc.ListEnvironments(r.Context(), chi.URLParam(r, "id"), 0)
		if err != nil {
			writeEnvError(w, err)
			return
		}
		items := make([]envTimelineDTO, 0, len(tls))
		for i := range tls {
			items = append(items, toEnvTimelineDTO(tls[i]))
		}
		writeJSON(w, http.StatusOK, map[string]any{"environments": items})
	}
}

// makeEnvironmentHistoryHandler 返回 GET /api/projects/{id}/environments/{env}/history。
func makeEnvironmentHistoryHandler(svc *environments.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "环境服务未初始化")
			return
		}
		tl, err := svc.EnvironmentHistory(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "env"), 0)
		if err != nil {
			writeEnvError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toEnvTimelineDTO(tl))
	}
}

// rollbackResponse 是回滚响应:复用 deploy targets 形状 + 回滚溯源(从哪个 run 回滚到哪个 run)。
type rollbackResponse struct {
	Environment string      `json:"environment"`
	FromRunID   string      `json:"fromRunId"`  // 回滚前的活跃版本 run
	ToRunID     string      `json:"toRunId"`    // 回滚目标(上一次成功部署)run
	ArtifactID  string      `json:"artifactId"` // 重发的产物
	Targets     []targetDTO `json:"targets"`    // 重发后的每机结果(复用部署 targets 形状)
}

// makeRollbackEnvironmentHandler 返回 POST /api/projects/{id}/environments/{env}/rollback(认证 + CSRF)。
//
// 定位该环境「上一次成功部署」(当前活跃版本之前最近一次全成功)→ 经既有 deploy.Service.Deploy
// 把那次的同一产物重发到原目标机(复用健康门控 / 多机扇出 / 失败诊断全链路)→ 返回重发后的 targets。
//
// 项目不存在 / 环境无历史 → 404;无可回滚目标 / 产物缺失 / 服务器不存在 → 422;
// 重发执行失败由 deploy.Deploy 内化为每机 failed(整体 200,不 500)。
func makeRollbackEnvironmentHandler(envSvc *environments.Service, deploySvc deploy.Service, runSvc run.Service, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if envSvc == nil || deploySvc == nil || runSvc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "回滚服务未初始化")
			return
		}
		projectID := chi.URLParam(r, "id")
		env := chi.URLParam(r, "env")

		rt, err := envSvc.ResolveRollback(r.Context(), projectID, env)
		if err != nil {
			writeEnvError(w, err)
			return
		}
		if rt.ArtifactID == "" {
			writeError(w, http.StatusUnprocessableEntity, "artifact_not_found", "上一次成功部署无可重发产物")
			return
		}

		// 复用既有部署链路重发上一次成功部署的产物到原目标机(默认 rolling 策略)。
		results, derr := deploySvc.Deploy(r.Context(), deploy.DeployInput{
			RunID:      rt.RunID,
			ArtifactID: rt.ArtifactID,
			ServerIDs:  rt.ServerIDs,
		})
		if derr != nil {
			writeDeployError(w, derr)
			return
		}

		recordAudit(r.Context(), rec, audit.Entry{
			Actor:      auditActor,
			Action:     "environment.rollback",
			TargetType: audit.TargetProject,
			TargetID:   projectID,
			Detail: map[string]any{
				"environment": env,
				"fromRunId":   rt.CurrentRunID,
				"toRunId":     rt.RunID,
				"artifactId":  rt.ArtifactID,
			},
			IP: clientIP(r),
		})

		// 回读权威持久化结果(与 run-detail targets 同源)。
		out := make([]targetDTO, 0, len(results))
		if targets, lerr := runSvc.ListDeployTargets(r.Context(), rt.RunID); lerr == nil {
			out = toTargetDTOs(targets)
		} else {
			for i := range results {
				out = append(out, targetResultToDTO(results[i]))
			}
		}
		writeJSON(w, http.StatusOK, rollbackResponse{
			Environment: env,
			FromRunID:   rt.CurrentRunID,
			ToRunID:     rt.RunID,
			ArtifactID:  rt.ArtifactID,
			Targets:     out,
		})
	}
}
