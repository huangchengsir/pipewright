package httpapi

// Retry HTTP views deliberately whitelist only public recovery metadata. In
// particular Snapshot, Inputs, ResourceRef and TargetProof never cross this API.
import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/runretry"
)

type runRetryService interface {
	Plan(context.Context, string, []string, ...runretry.PlanOptions) (*runretry.Plan, error)
	Submit(context.Context, string, runretry.SubmitInput) (*runretry.Request, error)
	Request(context.Context, string, string, string) (*runretry.Request, error)
	Attempts(context.Context, string, int, int) (*runretry.AttemptPage, error)
	Context(context.Context, string) (*runretry.Context, error)
	Reconcile(context.Context, string) (*runretry.ReconcileResult, error)
}

// The graph is bounded by the domain; two lists of its real 64-byte keys fit
// this bounded envelope. URI-based plans remain available for small selections.
const maxRunRetryBody = 2 << 20

func decodeRunRetryBody(w http.ResponseWriter, r *http.Request, value any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxRunRetryBody)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(value) != nil || d.Decode(new(any)) != io.EOF {
		writeError(w, 400, "bad_request", "请求体格式错误")
		return false
	}
	return true
}

// WithRunRetry is separate from WithRuns: missing recovery metadata must never
// make the ordinary detail/log/cancel endpoints unavailable.
func WithRunRetry(s *runretry.Service) Option {
	return func(o *options) {
		if s != nil {
			o.runRetry = s
		}
	}
}

func writeRunRetryError(w http.ResponseWriter, err error) {
	status, code := 503, "retry_unavailable"
	switch {
	case errors.Is(err, runretry.ErrNotFound), errors.Is(err, run.ErrNotFound):
		status, code = 404, "retry_not_found"
	case errors.Is(err, runretry.ErrInvalid):
		status, code = 422, "retry_invalid"
	case errors.Is(err, runretry.ErrConflict):
		status, code = 409, "retry_conflict"
	case errors.Is(err, runretry.ErrBlocked), errors.Is(err, runretry.ErrUnavailable), errors.Is(err, runretry.ErrCheckpoint):
		status, code = 422, "retry_evidence_unavailable"
	case errors.Is(err, runretry.ErrUnknown):
		status, code = 422, "retry_outcome_unknown"
	case errors.Is(err, configstate.ErrMaintenance):
		status, code = 503, "retry_maintenance"
	}
	// Never expose err.Error(): it may contain paths or execution configuration.
	writeError(w, status, code, "恢复操作暂不可用，请核对原操作与恢复证据")
}

func retryAuthenticated(w http.ResponseWriter, r *http.Request, s runRetryService) bool {
	if sess, ok := sessionFromContext(r.Context()); !ok || sess == nil {
		writeError(w, 401, "unauthorized", "请先登录")
		return false
	}
	if s == nil {
		writeRunRetryError(w, nil)
		return false
	}
	return true
}

func retryVisible(w http.ResponseWriter, r *http.Request, runs run.Service, s runRetryService) bool {
	if !retryAuthenticated(w, r, s) {
		return false
	}
	if runs == nil {
		writeRunRetryError(w, nil)
		return false
	}
	// The current product has one authenticated administrator with access to all
	// projects. Resolve the real run through that same service before any metadata.
	if _, err := runs.Get(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeRunError(w, err)
		return false
	}
	return true
}

func makeRunRetryPlanHandler(runs run.Service, s runRetryService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !retryVisible(w, r, runs, s) {
			return
		}
		q := r.URL.Query()
		targets, rebuild := q["nodeId"], q["rebuildNodeId"]
		if r.Method == http.MethodPost {
			// Transport-only alternative for a long selection. This performs the
			// identical read-only Plan and never creates a request or attempt.
			var input struct {
				TargetNodeIDs  []string `json:"targetNodeIds"`
				RebuildNodeIDs []string `json:"rebuildNodeIds,omitempty"`
			}
			if !decodeRunRetryBody(w, r, &input) {
				return
			}
			targets, rebuild = input.TargetNodeIDs, input.RebuildNodeIDs
		}
		p, err := s.Plan(r.Context(), chi.URLParam(r, "id"), targets, runretry.PlanOptions{RebuildNodeIDs: rebuild})
		if err != nil {
			writeRunRetryError(w, err)
			return
		}
		writeJSON(w, 200, publicRetryPlan(p))
	}
}

func makeRunRetryContextHandler(runs run.Service, s runRetryService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !retryVisible(w, r, runs, s) {
			return
		}
		v, err := s.Context(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeRunRetryError(w, err)
			return
		}
		writeJSON(w, 200, publicRetryContext(v))
	}
}

func makeRunRetrySubmitHandler(runs run.Service, s runRetryService, authn auth.Authenticator, aud audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !retryVisible(w, r, runs, s) {
			return
		}
		var in runretry.SubmitInput
		if !decodeRunRetryBody(w, r, &in) {
			return
		}
		if authn == nil {
			writeRunRetryError(w, nil)
			return
		}
		name, err := authn.AdminUsername()
		if err != nil || name == "" {
			writeRunRetryError(w, nil)
			return
		}
		// Stable principal scope survives a session renewal. Neither client input
		// nor a raw/hash session token supplies identity or enters persistence.
		in.Scope, in.Initiator = "admin", name
		v, err := s.Submit(r.Context(), chi.URLParam(r, "id"), in)
		if err != nil {
			writeRunRetryError(w, err)
			return
		}
		recordAudit(r.Context(), aud, audit.Entry{Actor: name, Action: "run.retry", TargetType: "run", TargetID: v.AttemptRunID, IP: clientIP(r), Detail: map[string]any{"sourceRunId": v.SourceAttemptID, "logicalRunId": v.LogicalRunID, "requestId": v.RequestID, "state": v.State, "targetCount": len(in.TargetNodeIDs), "rebuildCount": len(in.RebuildNodeIDs)}})
		writeJSON(w, 202, publicRetryRequest(v))
	}
}

func makeRunRetryRequestHandler(s runRetryService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Retention may remove a source pipeline_run while preserving its child
		// and request ledger. A visible accepted request must remain queryable.
		// The single authenticated administrator owns this stable caller scope;
		// the domain lookup still checks that exact scope and attempt lineage.
		if !retryAuthenticated(w, r, s) {
			return
		}
		v, err := s.Request(r.Context(), chi.URLParam(r, "id"), "admin", chi.URLParam(r, "requestId"))
		if err != nil {
			writeRunRetryError(w, err)
			return
		}
		writeJSON(w, 200, publicRetryRequest(v))
	}
}

func makeRunRetryAttemptsHandler(runs run.Service, s runRetryService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !retryVisible(w, r, runs, s) {
			return
		}
		page, size := 1, 20
		for key, dest := range map[string]*int{"page": &page, "pageSize": &size} {
			if raw := r.URL.Query().Get(key); raw != "" {
				v, err := strconv.Atoi(raw)
				if err != nil || v < 1 || (key == "page" && v > maxPage) || (key == "pageSize" && v > 100) {
					writeError(w, 400, "invalid_page", "分页参数非法")
					return
				}
				*dest = v
			}
		}
		v, err := s.Attempts(r.Context(), chi.URLParam(r, "id"), page, size)
		if err != nil {
			writeRunRetryError(w, err)
			return
		}
		items := make([]any, 0, len(v.Items))
		for _, a := range v.Items {
			items = append(items, map[string]any{"runId": a.RunID, "parentAttemptId": a.ParentAttemptID, "revision": a.Revision, "status": a.Status, "executionKind": a.ExecutionKind, "outcomeUnknown": a.OutcomeUnknown, "initiator": a.Initiator, "createdAt": a.CreatedAt})
		}
		writeJSON(w, 200, map[string]any{"items": items, "page": v.Page, "total": v.Total})
	}
}

func makeRunRetryReconcileHandler(runs run.Service, s runRetryService, authn auth.Authenticator, aud audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !retryVisible(w, r, runs, s) {
			return
		}
		// This POST observes existing external work and records local facts. It
		// accepts no ownership/exit proof or target identity from the browser.
		var in struct{}
		if !decodeUpdateBody(w, r, &in, true) {
			return
		}
		if authn == nil {
			writeRunRetryError(w, nil)
			return
		}
		name, err := authn.AdminUsername()
		if err != nil || name == "" {
			writeRunRetryError(w, nil)
			return
		}
		v, err := s.Reconcile(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeRunRetryError(w, err)
			return
		}
		recordAudit(r.Context(), aud, audit.Entry{Actor: name, Action: "run.retry_reconcile", TargetType: "run", TargetID: chi.URLParam(r, "id"), IP: clientIP(r), Detail: map[string]any{"checked": v.Checked, "drained": v.Drained, "reasonCount": len(v.Reasons)}})
		writeJSON(w, 200, map[string]any{"drained": v.Drained, "checked": v.Checked, "reasons": publicRetryReasons(v.Reasons)})
	}
}

func publicRetryReasons(rs []runretry.Reason) []any {
	out := make([]any, 0, len(rs))
	for _, r := range rs {
		out = append(out, map[string]any{"code": r.Code, "nodeKey": r.NodeKey, "related": r.Related})
	}
	return out
}
func publicRetryNode(n runretry.NodeRef) map[string]any {
	return map[string]any{"key": n.Key, "stageId": n.StageID, "jobId": n.JobID, "controlKey": n.ControlKey, "axes": n.Axes, "executionStageId": n.ExecutionStageID, "executionJobId": n.ExecutionJobID, "name": n.Name, "type": n.Type, "allowFailure": n.AllowFailure}
}
func publicRetryNodes(ns []runretry.PlannedNode) []any {
	out := make([]any, 0, len(ns))
	for _, n := range ns {
		out = append(out, map[string]any{"node": publicRetryNode(n.Node), "reasonCode": n.ReasonCode, "executionStarted": n.ExecutionStarted, "attemptStepId": n.StepID, "ordinal": n.Ordinal, "sourceStepId": n.SourceStepID, "sourceOrdinal": n.SourceOrdinal, "decision": map[string]any{"action": n.Decision.Action, "status": n.Decision.Status, "sourceAttemptId": n.Decision.SourceAttemptID, "sourceNodeKey": n.Decision.SourceNodeKey, "reasons": publicRetryReasons(n.Decision.Reasons)}})
	}
	return out
}
func publicRetryContext(c *runretry.Context) map[string]any {
	nodes := make([]any, 0, len(c.Graph.Nodes))
	edges := make([]any, 0, len(c.Graph.Edges))
	stages := make([]any, 0, len(c.Graph.Stages))
	for _, s := range c.Graph.Stages {
		stages = append(stages, map[string]any{"id": s.ID, "name": s.Name, "matrixParentId": s.MatrixParentID})
	}
	for _, n := range c.Graph.Nodes {
		nodes = append(nodes, publicRetryNode(n))
	}
	for _, e := range c.Graph.Edges {
		edges = append(edges, map[string]any{"from": e.From, "to": e.To, "kind": e.Kind, "order": e.Order})
	}
	return map[string]any{"logicalRunId": c.LogicalRunID, "attemptRunId": c.AttemptRunID, "headAttemptId": c.HeadAttemptID, "revision": c.Revision, "manifestFingerprint": c.ManifestFingerprint, "commitSha": c.CommitSHA, "graph": map[string]any{"version": c.Graph.Version, "nodes": nodes, "edges": edges, "stages": stages}, "nodes": publicRetryNodes(c.Nodes), "outcomeUnknown": c.OutcomeUnknown}
}
func publicRetryPlan(p *runretry.Plan) map[string]any {
	groups := make([]any, 0, len(p.RebuildGroups))
	for _, g := range p.RebuildGroups {
		groups = append(groups, map[string]any{"nodeIds": g.NodeIDs, "consumerIds": g.ConsumerIDs, "reasonCode": g.ReasonCode})
	}
	return map[string]any{"schemaVersion": p.SchemaVersion, "logicalRunId": p.LogicalRunID, "baseAttemptId": p.BaseAttemptID, "baseRevision": p.BaseRevision, "manifestFingerprint": p.ManifestFingerprint, "planFingerprint": p.PlanFingerprint, "targetNodeIds": p.TargetNodeIDs, "canSubmit": p.CanSubmit, "nodes": publicRetryNodes(p.Nodes), "blockingReasons": publicRetryReasons(p.BlockingReasons), "warnings": publicRetryReasons(p.Warnings), "rebuildNodeIds": p.RebuildNodeIDs, "rebuildGroups": groups}
}
func publicRetryRequest(v *runretry.Request) map[string]any {
	return map[string]any{"requestId": v.RequestID, "logicalRunId": v.LogicalRunID, "sourceAttemptId": v.SourceAttemptID, "attemptRunId": v.AttemptRunID, "state": v.State, "reasonCode": v.ReasonCode, "initiator": v.Initiator, "createdAt": v.CreatedAt}
}
