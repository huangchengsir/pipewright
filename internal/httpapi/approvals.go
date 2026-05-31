package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/approval"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/dagrun"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
)

// approvals.go 实现人工审批门(Epic 8 · Story 8-4)的 gate hook 与审批端点。
//
// gate hook(NewApprovalGate)注入 dagrun:进入 Gate 阶段前登记待批 + 置 run 状态 waiting_approval +
// 阻塞协调器,直到端点投递批准/拒绝(或取消/超时)。端点(approve/reject)经协调器解析决定 + 审计。

// defaultGateTimeout 是审批门兜底超时:超时自动拒绝以释放被该运行占用的 worker。
// 审批人节奏可较慢,故取较长值;运行亦可经取消端点立即释放。
const defaultGateTimeout = 24 * time.Hour

// NewApprovalGate 构造注入 dagrun 的审批门 hook(供 main 装配)。
func NewApprovalGate(runs run.Service, coord *approval.Coordinator, store *approval.Store) dagrun.GateFunc {
	return func(ctx context.Context, r *run.Run, stage pipeline.Stage) (bool, error) {
		key := approval.Key(r.ID, stage.ID)
		_ = store.CreatePending(ctx, r.ID, stage.ID, stage.Name)
		ch := coord.Wait(key)
		defer coord.Cancel(key)

		// 先注册等待者再置 waiting,确保端点在状态切换后总能解析到该门。
		if err := runs.MarkWaitingApproval(ctx, r.ID); err != nil {
			// 已终态/被取消:无法进入等待,退化失败让 worker 收尾。
			return false, err
		}

		var (
			approved bool
			status   = approval.StatusRejected
			by       string
		)
		select {
		case d := <-ch:
			approved = d.Approved
			by = d.Actor
			if approved {
				status = approval.StatusApproved
			}
		case <-ctx.Done():
			by = "canceled"
			_ = store.Decide(context.Background(), r.ID, stage.ID, status, by)
			_ = runs.ResumeFromApproval(context.Background(), r.ID)
			return false, ctx.Err()
		case <-time.After(defaultGateTimeout):
			by = "timeout"
			_ = store.Decide(context.Background(), r.ID, stage.ID, status, by)
			_ = runs.ResumeFromApproval(context.Background(), r.ID)
			return false, fmt.Errorf("approval gate timed out after %s", defaultGateTimeout)
		}
		_ = store.Decide(context.Background(), r.ID, stage.ID, status, by)
		// 决定后置回 running,让 worker 在收尾时按 running→终态 落定。
		_ = runs.ResumeFromApproval(context.Background(), r.ID)
		return approved, nil
	}
}

// makeApprovalDecisionHandler 返回 approve(approve=true)/reject(false)端点 handler。
// body {stageId};经协调器投递决定。该阶段当前不在等待 → 409。
func makeApprovalDecisionHandler(coord *approval.Coordinator, store *approval.Store, rec audit.Recorder, approve bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if coord == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "审批门服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		r.Body = http.MaxBytesReader(w, r.Body, 1<<13)
		var req struct {
			StageID string `json:"stageId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}
		stageID := strings.TrimSpace(req.StageID)
		if stageID == "" {
			writeError(w, http.StatusUnprocessableEntity, "missing_stage", "缺少 stageId")
			return
		}
		key := approval.Key(id, stageID)
		if !coord.Resolve(key, approval.Decision{Approved: approve, Actor: auditActor}) {
			writeError(w, http.StatusConflict, "not_waiting", "该运行阶段当前不在等待审批")
			return
		}
		action := "run.approve"
		if !approve {
			action = "run.reject"
		}
		recordAudit(r.Context(), rec, audit.Entry{
			Actor:      auditActor,
			Action:     action,
			TargetType: "run",
			TargetID:   id,
			Detail:     map[string]any{"stageId": stageID},
			IP:         clientIP(r),
		})
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "approved": approve})
	}
}

// makeListApprovalsHandler 返回 GET /api/runs/{id}/approvals(某运行的审批门记录列表)。
func makeListApprovalsHandler(store *approval.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "审批门服务未初始化")
			return
		}
		recs, err := store.ListForRun(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
			return
		}
		if recs == nil {
			recs = []approval.Record{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": recs})
	}
}
