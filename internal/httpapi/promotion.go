package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/approval"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/promotion"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/vault"
)

// promotion.go 装配环境晋级流(Epic 8 · Story 8-7 / FR-8-7)的 HTTP 端点与适配器。
//
// 复用:晋级审批门复用 FR-8-4 的 approval.Coordinator + run_approvals(promotionGate);
// 源运行状态/归属经 run.Service 适配(runLookup);secret 变量经 vault.Vault 解密(secretReveal)。

// promotionGateTimeout 是晋级审批门兜底超时(超时自动拒绝)。审批节奏可较慢,取较长值。
const promotionGateTimeout = 24 * time.Hour

// runLookup 把 run.Service 适配为 promotion.RunLookup(只取状态/归属,不引入循环依赖)。
type runLookup struct{ runs run.Service }

func (l runLookup) LookupRun(ctx context.Context, runID string) (promotion.RunInfo, error) {
	r, err := l.runs.Get(ctx, runID)
	if err != nil {
		if errors.Is(err, run.ErrNotFound) {
			return promotion.RunInfo{}, promotion.ErrRunNotFound
		}
		return promotion.RunInfo{}, err
	}
	return promotion.RunInfo{ID: r.ID, ProjectID: r.ProjectID, Status: r.Status}, nil
}

// secretReveal 把 vault.Vault 适配为 promotion.SecretResolver(Reveal 不刷新 last_used_at)。
type secretReveal struct{ v vault.Vault }

func (s secretReveal) Reveal(credentialID string) (string, error) {
	if s.v == nil {
		return "", errors.New("vault unconfigured")
	}
	return s.v.Reveal(credentialID)
}

// promotionGate 复用审批门内核(approval.Coordinator + Store)实现 promotion.Gate:
// 对 gated 目标环境,登记待批 + 阻塞协调器等待端点投递批准/拒绝(或超时)。
//
// 复用而非复制:Wait/Resolve/Cancel 全走既有 approval.Coordinator;待批/决定落 run_approvals
// (stageId = "promote:<env>"),晋级端点(approve/reject)直接复用既有 /runs/{id}/approve|reject。
type promotionGate struct {
	coord *approval.Coordinator
	store *approval.Store
	runID string // 审批门内核以 (runID, stageId) 组合键;晋级门挂在源运行上
}

func (g promotionGate) Await(ctx context.Context, stageID string, info promotion.GateInfo) (bool, string, error) {
	key := approval.Key(g.runID, stageID)
	stageName := "晋级到「" + info.TargetEnvironment + "」"
	_ = g.store.CreatePending(ctx, g.runID, stageID, stageName)
	ch := g.coord.Wait(key)
	defer g.coord.Cancel(key)

	select {
	case d := <-ch:
		status := approval.StatusRejected
		if d.Approved {
			status = approval.StatusApproved
		}
		_ = g.store.Decide(context.Background(), g.runID, stageID, status, d.Actor)
		return d.Approved, d.Actor, nil
	case <-ctx.Done():
		_ = g.store.Decide(context.Background(), g.runID, stageID, approval.StatusRejected, "canceled")
		return false, "canceled", ctx.Err()
	case <-time.After(promotionGateTimeout):
		_ = g.store.Decide(context.Background(), g.runID, stageID, approval.StatusRejected, "timeout")
		return false, "timeout", context.DeadlineExceeded
	}
}

// makeGetEnvironmentsHandler 返回 GET /api/projects/{id}/environments(环境链 + 各环境变量掩码视图)。
func makeGetEnvironmentsHandler(store *promotion.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "晋级服务未初始化")
			return
		}
		projectID := chi.URLParam(r, "id")
		chain, err := store.GetChain(r.Context(), projectID)
		if err != nil && !errors.Is(err, promotion.ErrChainNotConfigured) {
			writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
			return
		}
		envs := chain.Environments
		if envs == nil {
			envs = []promotion.EnvStage{}
		}
		// 每环境附其变量掩码视图(secret 仅 credentialId,绝无明文)。
		varsByEnv := map[string][]promotion.Variable{}
		for _, e := range envs {
			vs, verr := store.ListVariables(r.Context(), projectID, e.Name)
			if verr != nil {
				writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
				return
			}
			varsByEnv[e.Name] = vs
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"environments": envs,
			"variables":    varsByEnv,
		})
	}
}

// makeSaveEnvironmentsHandler 返回 PUT /api/projects/{id}/environments(配置环境链 + 可选逐环境变量)。
func makeSaveEnvironmentsHandler(store *promotion.Store, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "晋级服务未初始化")
			return
		}
		projectID := chi.URLParam(r, "id")
		r.Body = http.MaxBytesReader(w, r.Body, 1<<18)
		var req struct {
			Environments []promotion.EnvStage            `json:"environments"`
			Variables    map[string][]promotion.Variable `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}
		chain, err := store.SaveChain(r.Context(), projectID, promotion.Chain{Environments: req.Environments})
		if err != nil {
			writePromotionError(w, err)
			return
		}
		// 可选:逐环境覆盖变量(仅接受链上环境;未知环境忽略以防悬挂作用域)。
		for env, vars := range req.Variables {
			if chain.IndexOf(env) < 0 {
				continue
			}
			if verr := store.SetVariables(r.Context(), projectID, env, vars); verr != nil {
				writePromotionError(w, verr)
				return
			}
		}
		recordAudit(r.Context(), rec, audit.Entry{
			Actor:      auditActor,
			Action:     "project.environments.save",
			TargetType: audit.TargetProject,
			TargetID:   projectID,
			Detail:     map[string]any{"chain": chain.Names()},
			IP:         clientIP(r),
		})
		writeJSON(w, http.StatusOK, map[string]any{"environments": chain.Environments})
	}
}

// makePromoteRunHandler 返回 POST /api/runs/{id}/promote(把源运行晋级到下一环境)。
// body {targetEnvironment?}:留空取链上下一级;给定则校验恰为下一级(防跳级)。
// gated 目标会阻塞等待审批(经既有 /runs/{id}/approve|reject 决定)。
func makePromoteRunHandler(store *promotion.Store, runs run.Service, coord *approval.Coordinator,
	astore *approval.Store, vlt vault.Vault, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if store == nil || runs == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "晋级服务未初始化")
			return
		}
		runID := chi.URLParam(r, "id")
		r.Body = http.MaxBytesReader(w, r.Body, 1<<13)
		var req struct {
			TargetEnvironment string `json:"targetEnvironment"`
		}
		// 空 body 合法(取下一级)。
		_ = json.NewDecoder(r.Body).Decode(&req)
		target := strings.TrimSpace(req.TargetEnvironment)

		var gate promotion.Gate
		if coord != nil && astore != nil {
			gate = promotionGate{coord: coord, store: astore, runID: runID}
		}
		coordinator := promotion.NewCoordinator(store, runLookup{runs: runs}, gate, secretReveal{v: vlt})

		res, err := coordinator.Promote(r.Context(), runID, target, auditActor)
		if err != nil {
			// 审批被拒/超时/fail-closed:仍返回记录(409),非 500。
			if res != nil && promotion.IsTerminalErr(err) {
				recordAudit(r.Context(), rec, audit.Entry{
					Actor:      auditActor,
					Action:     "run.promote.reject",
					TargetType: audit.TargetRun,
					TargetID:   runID,
					Detail:     map[string]any{"targetEnvironment": res.Record.TargetEnvironment},
					IP:         clientIP(r),
				})
				writeJSON(w, http.StatusConflict, toPromotionDTO(res.Record))
				return
			}
			writePromotionError(w, err)
			return
		}
		recordAudit(r.Context(), rec, audit.Entry{
			Actor:      auditActor,
			Action:     "run.promote",
			TargetType: audit.TargetRun,
			TargetID:   runID,
			Detail: map[string]any{
				"targetEnvironment": res.Record.TargetEnvironment,
				"fromEnvironment":   res.Record.FromEnvironment,
			},
			IP: clientIP(r),
		})
		writeJSON(w, http.StatusOK, toPromotionDTO(res.Record))
	}
}

// makeListRunPromotionsHandler 返回 GET /api/runs/{id}/promotions(某运行的晋级历史)。
func makeListRunPromotionsHandler(store *promotion.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "晋级服务未初始化")
			return
		}
		recs, err := store.ListRecordsForRun(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": toPromotionDTOs(recs)})
	}
}

// makeListProjectPromotionsHandler 返回 GET /api/projects/{id}/promotions(项目晋级历史,最近在前)。
func makeListProjectPromotionsHandler(store *promotion.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "晋级服务未初始化")
			return
		}
		recs, err := store.ListRecordsForProject(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": toPromotionDTOs(recs)})
	}
}

// promotionDTO 是晋级记录的对外 JSON 形状(camelCase)。
type promotionDTO struct {
	ID                string `json:"id"`
	ProjectID         string `json:"projectId"`
	SourceRunID       string `json:"sourceRunId"`
	FromEnvironment   string `json:"fromEnvironment"`
	TargetEnvironment string `json:"targetEnvironment"`
	Status            string `json:"status"`
	PromotedBy        string `json:"promotedBy"`
	CreatedAt         string `json:"createdAt"`
	DecidedAt         string `json:"decidedAt"`
}

func toPromotionDTO(r promotion.Record) promotionDTO {
	return promotionDTO{
		ID:                r.ID,
		ProjectID:         r.ProjectID,
		SourceRunID:       r.SourceRunID,
		FromEnvironment:   r.FromEnvironment,
		TargetEnvironment: r.TargetEnvironment,
		Status:            r.Status,
		PromotedBy:        r.PromotedBy,
		CreatedAt:         r.CreatedAt,
		DecidedAt:         r.DecidedAt,
	}
}

func toPromotionDTOs(recs []promotion.Record) []promotionDTO {
	out := make([]promotionDTO, 0, len(recs))
	for _, r := range recs {
		out = append(out, toPromotionDTO(r))
	}
	return out
}

// writePromotionError 把晋级领域错误映射为 HTTP 状态码(错误体绝无敏感数据)。
func writePromotionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, promotion.ErrRunNotFound):
		writeError(w, http.StatusNotFound, "not_found", "运行不存在")
	case errors.Is(err, promotion.ErrProjectNotFound):
		writeError(w, http.StatusNotFound, "not_found", "项目不存在")
	case errors.Is(err, promotion.ErrChainNotConfigured):
		writeError(w, http.StatusUnprocessableEntity, "chain_not_configured", "尚未配置环境链")
	case errors.Is(err, promotion.ErrEmptyChain):
		writeError(w, http.StatusUnprocessableEntity, "empty_chain", "环境链不能为空")
	case errors.Is(err, promotion.ErrDuplicateEnv):
		writeError(w, http.StatusUnprocessableEntity, "duplicate_env", "环境链含重复环境名")
	case errors.Is(err, promotion.ErrInvalidEnvName):
		writeError(w, http.StatusUnprocessableEntity, "invalid_env", "环境名非法")
	case errors.Is(err, promotion.ErrUnknownEnv):
		writeError(w, http.StatusUnprocessableEntity, "unknown_env", "目标环境不在链上")
	case errors.Is(err, promotion.ErrRunNotSuccessful):
		writeError(w, http.StatusConflict, "run_not_successful", "源运行未成功,不可晋级")
	case errors.Is(err, promotion.ErrSkipEnv):
		writeError(w, http.StatusConflict, "skip_env", "不可跳级:只能晋级到下一环境")
	case errors.Is(err, promotion.ErrAlreadyAtTop):
		writeError(w, http.StatusConflict, "already_at_top", "已晋级到链尾环境")
	case errors.Is(err, promotion.ErrAlreadyPromoted):
		writeError(w, http.StatusConflict, "already_promoted", "该运行已晋级到该环境")
	case errors.Is(err, promotion.ErrGateRejected):
		writeError(w, http.StatusConflict, "gate_rejected", "晋级审批被拒绝")
	case errors.Is(err, promotion.ErrVarKeyEmpty):
		writeError(w, http.StatusUnprocessableEntity, "var_key_empty", "变量 key 不能为空")
	case errors.Is(err, promotion.ErrVarKeyDuplicate):
		writeError(w, http.StatusUnprocessableEntity, "var_key_duplicate", "环境内变量 key 重复")
	default:
		writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
	}
}
