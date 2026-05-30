package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/huangjiawei/devopstool/internal/audit"
	"github.com/huangjiawei/devopstool/internal/run"
	"github.com/huangjiawei/devopstool/internal/trigger"
)

// webhookMaxBody 限制 webhook 投递体大小(防超大 body 拖垮内存)。
const webhookMaxBody = 1 << 20 // 1 MiB

// NewRunCreator 把 run.Service 适配为 trigger.RunCreator(供 main 装配 webhook 接收器)。
func NewRunCreator(svc run.Service) trigger.RunCreator {
	return runCreatorAdapter{svc: svc}
}

// runCreatorAdapter 把 run.Service 适配为 trigger.RunCreator:
// 注入解析出的环境/目标服务器为运行内部元数据(供 Epic 4),并返回新运行 id。
type runCreatorAdapter struct {
	svc run.Service
}

// CreateWebhookRun 实现 trigger.RunCreator:以 webhook 触发类型创建运行。
func (a runCreatorAdapter) CreateWebhookRun(ctx context.Context, projectID string, in trigger.RunRequest) (string, error) {
	r, err := a.svc.Create(ctx, projectID, run.Trigger{
		Type:                    run.TriggerWebhook,
		Branch:                  in.Branch,
		Commit:                  in.Commit,
		Actor:                   in.Actor,
		ResolvedEnvironment:     in.ResolvedEnvironment,
		ResolvedTargetServerIDs: in.ResolvedTargetServerIDs,
	})
	if err != nil {
		return "", err
	}
	return r.ID, nil
}

// webhookAcceptedDTO 是命中创建运行的响应体(冻结契约)。
type webhookAcceptedDTO struct {
	Accepted bool   `json:"accepted"`
	RunID    string `json:"runId"`
}

// webhookIgnoredDTO 是未命中/被忽略的响应体(冻结契约;ignored 为原因枚举)。
type webhookIgnoredDTO struct {
	Accepted bool   `json:"accepted"`
	Ignored  string `json:"ignored"`
}

// makeWebhookHandler 返回 POST /api/webhooks/{token} handler(公开;靠签名校验;豁免 auth/CSRF）。
// 验签失败 → 401(不创建);命中 → 200 {accepted:true,runId};否则 200 {accepted:false,ignored}.
// 绝不回显密钥明文/密文;错误体永不含 secret。
func makeWebhookHandler(rc *trigger.Receiver) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if rc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "webhook 接收服务未初始化")
			return
		}
		token := chi.URLParam(r, "token")

		// 限体积后读取原始 body(验签 + 解析所需)。
		r.Body = http.MaxBytesReader(w, r.Body, webhookMaxBody)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体过大或读取失败")
			return
		}

		res, err := rc.Handle(r.Context(), trigger.Delivery{
			Token:      token,
			Event:      r.Header.Get(trigger.HeaderGiteeEvent),
			TokenHdr:   r.Header.Get(trigger.HeaderGiteeToken),
			Timestamp:  r.Header.Get(trigger.HeaderGiteeTimestamp),
			DeliveryID: r.Header.Get(trigger.HeaderGiteeDelivery),
			RawBody:    body,
		})
		if err != nil {
			switch {
			case errors.Is(err, trigger.ErrUnauthorized):
				// 验签失败:401,不创建。错误体不含任何密钥信息。
				writeError(w, http.StatusUnauthorized, "unauthorized", "webhook 签名校验失败")
			case errors.Is(err, trigger.ErrTokenNotFound):
				writeError(w, http.StatusNotFound, "webhook_token_not_found", "webhook 端点不存在")
			default:
				writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
			}
			return
		}

		if res.Accepted {
			writeJSON(w, http.StatusOK, webhookAcceptedDTO{Accepted: true, RunID: res.RunID})
			return
		}
		writeJSON(w, http.StatusOK, webhookIgnoredDTO{Accepted: false, Ignored: res.Ignored})
	}
}

// makeManualRunHandler 返回 POST /api/projects/{id}/runs handler(认证 + CSRF;手动触发）。
// body {branch, commit?};actor=admin;成功 → 201 冻结 run-detail DTO。
// 成功后追加 run_trigger_manual 审计(detail 记分支/commit/运行 id)。
func makeManualRunHandler(svc run.Service, aud audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "运行服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		var req struct {
			Branch string `json:"branch"`
			Commit string `json:"commit"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}
		branch := strings.TrimSpace(req.Branch)
		if branch == "" {
			writeError(w, http.StatusBadRequest, "branch_required", "必须指定分支")
			return
		}

		commit := strings.TrimSpace(req.Commit)
		rn, err := svc.Create(r.Context(), id, run.Trigger{
			Type:   run.TriggerManual,
			Branch: branch,
			Commit: commit,
			Actor:  "admin",
		})
		if err != nil {
			writeRunError(w, err)
			return
		}
		recordAudit(r.Context(), aud, audit.Entry{
			Actor:      auditActor,
			Action:     audit.ActionRunTriggerManual,
			TargetType: audit.TargetRun,
			TargetID:   rn.ID,
			Detail:     map[string]any{"projectId": id, "branch": branch, "commit": commit},
			IP:         clientIP(r),
		})
		// 刚创建(queued)的运行尚无产物/无部署;nil → []/null。产物在成功路径由 worker emit、部署经 deploy 端点。
		writeJSON(w, http.StatusCreated, toRunDetailDTO(rn, nil, nil))
	}
}
