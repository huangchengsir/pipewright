package configdraft

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/maintenance"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/store"
	"gopkg.in/yaml.v3"
)

type operationRow struct {
	id, token, project, request, input, draft, approval, owner, state, reason, result string
	before                                                                            int64
	after                                                                             sql.NullInt64
}

func (s *Service) readOperation(ctx context.Context, q configstate.DBTX, where string, lock bool, args ...any) (*operationRow, error) {
	x := &operationRow{}
	sqlText := `SELECT id,token_id,project_id,request_id,input_digest,draft_id,approval_id,owner_epoch,state,reason,before_revision,after_revision,result_json FROM mcp_config_operations WHERE ` + where
	if lock && store.DialectOf(s.db) == store.MySQL {
		sqlText += " FOR UPDATE"
	}
	e := q.QueryRowContext(ctx, sqlText, args...).Scan(&x.id, &x.token, &x.project, &x.request, &x.input, &x.draft, &x.approval, &x.owner, &x.state, &x.reason, &x.before, &x.after, &x.result)
	return x, e
}
func operationView(x *operationRow) *OperationResult {
	v := &OperationResult{OperationID: x.id, ProjectID: x.project, State: x.state, Reason: x.reason, BaseRevision: revision(x.before), ChangedOperations: []string{}}
	if x.after.Valid {
		v.NewRevision = revision(x.after.Int64)
	}
	if len(x.result) <= 4096 {
		var stored OperationResult
		if json.Unmarshal([]byte(x.result), &stored) == nil {
			for _, n := range stored.ChangedOperations {
				if contains(OperationTypes, n) {
					v.ChangedOperations = append(v.ChangedOperations, n)
				}
			}
		}
	}
	return v
}
func (s *Service) Apply(ctx context.Context, tokenID string, in ApplyRequest) (*OperationResult, error) {
	release, admissionErr := maintenance.Enter(ctx, s.db, "config_apply")
	if admissionErr != nil {
		return nil, admissionErr
	}
	defer release()
	if !s.opts.ApplyEnabled {
		return nil, ErrDisabled
	}
	if !validRequestID(in.RequestID) || in.DraftID == "" || in.ApprovalID == "" {
		return nil, ErrInvalid
	}
	g, e := s.prepare(ctx)
	if e != nil {
		return nil, e
	}
	hash := digest(in)
	var intent *operationRow
	execute := false
	e = configstate.WithWriteTx(ctx, s.db, func(q configstate.DBTX) error {
		t, e := s.locks(ctx, q, tokenID, g)
		if e != nil {
			return e
		}
		old, e := s.readOperation(ctx, q, `token_id=? AND request_id=?`, false, tokenID, in.RequestID)
		if e == nil {
			if !sameDigest(old.input, hash) {
				return ErrIdempotency
			}
			if !allowed(t, old.project, "apply", "", "") {
				return ErrForbidden
			}
			for _, op := range operationView(old).ChangedOperations {
				if !allowed(t, old.project, "apply", sectionFor(op), op) {
					return ErrForbidden
				}
			}
			intent = old
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		r, e := readDraft(ctx, q, `id=? AND owner_token_id=?`, in.DraftID, tokenID)
		if e != nil {
			return ErrForbidden
		}
		if e = authorize(t, r.project, "apply", r.ops); e != nil {
			return e
		}
		if len(r.ops) == 0 {
			return ErrApproval
		}
		if _, e = configstate.LockProject(ctx, q, s.db, r.project); e != nil {
			return e
		}
		now := s.opts.Now().UnixMilli()
		intent = &operationRow{id: uuid.NewString(), token: tokenID, project: r.project, request: in.RequestID, input: hash, draft: r.id, approval: in.ApprovalID, owner: uuid.NewString(), state: "pending", before: r.base, result: "{}"}
		_, e = q.ExecContext(ctx, `INSERT INTO mcp_config_operations (id,token_id,project_id,request_id,input_digest,draft_id,approval_id,owner_epoch,state,reason,before_revision,result_json,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, intent.id, intent.token, intent.project, intent.request, intent.input, intent.draft, intent.approval, intent.owner, "pending", "", intent.before, "{}", now, now)
		if e != nil {
			return e
		}
		execute = true
		return nil
	})
	if e != nil {
		return nil, e
	}
	if !execute {
		if e = s.fresh(ctx, tokenID, g); e != nil {
			return nil, e
		}
		return operationView(intent), nil
	}
	// Learn the immutable browser subject before acquiring project locks, then
	// verify that exact subject with the approval record locked below.
	var approver string
	if e = s.db.QueryRowContext(ctx, `SELECT approver FROM mcp_config_approvals WHERE id=? AND draft_id=? AND token_id=?`, in.ApprovalID, in.DraftID, tokenID).Scan(&approver); e != nil {
		approver = ""
	}
	work, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	commitAttempted := false
	e = configstate.WithWriteTx(work, s.db, func(q configstate.DBTX) error {
		t, e := s.locks(work, q, tokenID, g)
		if e != nil {
			return e
		}
		if s.opts.RevalidateApprover == nil || approver == "" || s.opts.RevalidateApprover(work, q, approver) != nil {
			return ErrApproval
		}
		if _, e = configstate.LockProject(work, q, s.db, intent.project); e != nil {
			return e
		}
		x, e := s.readOperation(work, q, `id=?`, true, intent.id)
		if e != nil {
			return e
		}
		if x.state != "pending" || x.owner != intent.owner {
			return ErrIdempotency
		}
		where := `id=? AND owner_token_id=?`
		if store.DialectOf(s.db) == store.MySQL {
			where += " FOR UPDATE"
		}
		r, e := readDraft(work, q, where, in.DraftID, tokenID)
		if e != nil {
			return ErrForbidden
		}
		if e = authorize(t, r.project, "apply", r.ops); e != nil {
			return e
		}
		if r.applied != "" {
			return ErrIdempotency
		}
		if s.opts.Now().UnixMilli() >= r.expiry {
			return ErrExpired
		}
		if t.AuthzRevision != r.authz {
			return ErrForbidden
		}
		d, e := s.load(work, q, r.project)
		if e != nil {
			return e
		}
		if d.rev != r.base {
			return ErrConflict
		}
		if d.policy != r.policy || r.validator != ValidatorVersion {
			return ErrPolicy
		}
		if e = configstate.CheckIdle(work, q, r.project); e != nil {
			if errors.Is(e, configstate.ErrBusy) {
				return ErrBusy
			}
			return e
		}
		if _, e = s.normalize(r.ops, g); e != nil {
			return ErrSecret
		}
		candidate, diff, e := s.patch(d, r.ops, g)
		if e != nil {
			return e
		}
		if len(diff) == 0 {
			return ErrApproval
		}
		validation := s.validateDocument(work, q, d, r.ops, g)
		if !validation.ApplyAllowed {
			return Error(firstIssue(validation))
		}
		var aDraft, aToken, aProject, aPolicy, aValidator, aPatch, aReview, aApprover string
		var aBase, aAuthz, aExpiry int64
		var revoked sql.NullInt64
		var consumed sql.NullString
		stmt := `SELECT draft_id,token_id,project_id,base_revision,policy_version,validator_version,authz_revision,patch_digest,review_digest,approver,expires_at,revoked_at,consumed_by_operation_id FROM mcp_config_approvals WHERE id=?`
		if store.DialectOf(s.db) == store.MySQL {
			stmt += " FOR UPDATE"
		}
		e = q.QueryRowContext(work, stmt, in.ApprovalID).Scan(&aDraft, &aToken, &aProject, &aBase, &aPolicy, &aValidator, &aAuthz, &aPatch, &aReview, &aApprover, &aExpiry, &revoked, &consumed)
		if e != nil || aDraft != r.id || aToken != tokenID || aProject != r.project || aBase != r.base || aPolicy != r.policy || aValidator != ValidatorVersion || aAuthz != t.AuthzRevision || !sameDigest(aPatch, r.patchDigest) || !sameDigest(aReview, s.reviewDigest(r, diff)) || aApprover != approver || revoked.Valid || consumed.Valid {
			return ErrApproval
		}
		if s.opts.Now().UnixMilli() >= aExpiry {
			return ErrApprovalExpired
		}
		stamp := s.opts.Now().UTC().Format(time.RFC3339)
		if d.name != candidate.name || d.branch != candidate.branch {
			if _, e = q.ExecContext(work, `UPDATE projects SET name=?,default_branch=?,updated_at=? WHERE id=?`, candidate.name, candidate.branch, stamp, r.project); e != nil {
				return e
			}
		}
		if !reflect.DeepEqual(d.raw, candidate.raw) {
			raw, e := json.Marshal(candidate.raw)
			if e != nil {
				return e
			}
			yaml, e := renderRawYAML(raw)
			if e != nil {
				return ErrUnsupported
			}
			if _, e = q.ExecContext(work, `UPDATE pipeline_configs SET spec_json=?,spec_yaml=?,updated_at=?,saved_at=? WHERE project_id=?`, string(raw), yaml, stamp, stamp, r.project); e != nil {
				return e
			}
		}
		newRev, e := configstate.BumpRevision(work, q, r.project, r.base)
		if e != nil {
			return e
		}
		res, e := q.ExecContext(work, `UPDATE mcp_config_approvals SET consumed_by_operation_id=? WHERE id=? AND consumed_by_operation_id IS NULL AND revoked_at IS NULL`, intent.id, in.ApprovalID)
		if e != nil {
			return e
		}
		n, e := res.RowsAffected()
		if e != nil || n != 1 {
			return ErrApproval
		}
		res, e = q.ExecContext(work, `UPDATE mcp_config_drafts SET applied_operation_id=? WHERE id=? AND applied_operation_id IS NULL`, intent.id, r.id)
		if e != nil {
			return e
		}
		n, e = res.RowsAffected()
		if e != nil || n != 1 {
			return ErrIdempotency
		}
		out := OperationResult{OperationID: intent.id, ProjectID: r.project, State: "applied", BaseRevision: revision(r.base), NewRevision: revision(newRev), ChangedOperations: operationNames(r.ops)}
		b, _ := json.Marshal(out)
		res, e = q.ExecContext(work, `UPDATE mcp_config_operations SET state='applied',reason='',after_revision=?,result_json=?,updated_at=? WHERE id=? AND state='pending' AND owner_epoch=?`, newRev, string(b), s.opts.Now().UnixMilli(), intent.id, intent.owner)
		if e != nil {
			return e
		}
		n, e = res.RowsAffected()
		if e != nil || n != 1 {
			return ErrIdempotency
		}
		if e = s.audit(work, q, tokenID, "mcp_config_apply", r.project, r.id, operationNames(r.ops)); e != nil {
			return e
		}
		if _, e = auth.LoadMCPToken(work, q, s.db, tokenID, false); e != nil {
			return ErrForbidden
		}
		if s.opts.Now().UnixMilli() >= aExpiry {
			return ErrApprovalExpired
		}
		if s.opts.RevalidateApprover == nil || s.opts.RevalidateApprover(work, q, approver) != nil {
			return ErrApproval
		}
		commitAttempted = true
		return nil
	})
	if e != nil && !commitAttempted {
		s.finishFailed(context.WithoutCancel(ctx), intent, e)
	}
	// Read the authoritative primary, including after an ambiguous COMMIT error.
	result, readErr := s.Operation(ctx, tokenID, OperationRequest{OperationID: intent.id})
	if readErr != nil {
		return nil, readErr
	}
	if e != nil && commitAttempted && result.State == "pending" {
		result.State = "unknown"
		result.Reason = "commit_unknown"
	}
	return result, nil
}

// AdminOperation exposes only the fixed operation result for manual recovery.
// It remains accessible after a client token is revoked, through browser admin
// authentication, without returning the draft, credentials or raw configuration.
func (s *Service) AdminOperation(ctx context.Context, adminID, operationID string) (*OperationResult, error) {
	if operationID == "" || len(operationID) > 36 {
		return nil, ErrInvalid
	}
	var out *OperationResult
	err := configstate.WithWriteTx(ctx, s.db, func(q configstate.DBTX) error {
		if err := configstate.LockEpochs(ctx, q, s.db, configstate.EpochPolicy, configstate.EpochSecrets, configstate.EpochExecutor); err != nil {
			return err
		}
		if s.opts.AuthorizeAdmin == nil || s.opts.AuthorizeAdmin(ctx, q, adminID) != nil {
			return ErrForbidden
		}
		x, err := s.readOperation(ctx, q, `id=?`, false, operationID)
		if err != nil {
			return ErrForbidden
		}
		out = operationView(x)
		return s.audit(ctx, q, "admin", "mcp_config_operation_inspect", x.project, x.draft, nil)
	})
	return out, err
}

// JSON is a YAML subset. Parsing it as nodes preserves numeric lexemes and
// every unknown field; the legacy typed exporter drops DAG control fields.
func renderRawYAML(raw []byte) (string, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return "", err
	}
	var blockStyle func(*yaml.Node)
	blockStyle = func(n *yaml.Node) {
		n.Style = 0
		for _, child := range n.Content {
			blockStyle(child)
		}
	}
	blockStyle(&root)
	b, err := yaml.Marshal(&root)
	return string(b), err
}
func (s *Service) finishFailed(ctx context.Context, x *operationRow, cause error) {
	cleanup, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_ = configstate.WithWriteTx(cleanup, s.db, func(q configstate.DBTX) error {
		state := "failed"
		var controlled Error
		if errors.As(cause, &controlled) {
			state = "rejected"
		}
		reason := Code(cause)
		_, e := q.ExecContext(cleanup, `UPDATE mcp_config_operations SET state=?,reason=?,updated_at=? WHERE id=? AND state='pending' AND owner_epoch=?`, state, reason, s.opts.Now().UnixMilli(), x.id, x.owner)
		return e
	})
}
func (s *Service) Operation(ctx context.Context, tokenID string, in OperationRequest) (*OperationResult, error) {
	if (in.OperationID != "" && (in.ProjectID != "" || in.RequestID != "")) || (in.OperationID == "" && (in.ProjectID == "" || !validRequestID(in.RequestID))) {
		return nil, ErrInvalid
	}
	g, e := s.prepare(ctx)
	if e != nil {
		return nil, e
	}
	var out *OperationResult
	e = configstate.WithWriteTx(ctx, s.db, func(q configstate.DBTX) error {
		t, e := s.locks(ctx, q, tokenID, g)
		if e != nil {
			return e
		}
		where := `id=? AND token_id=?`
		args := []any{in.OperationID, tokenID}
		if in.OperationID == "" {
			where = `project_id=? AND request_id=? AND token_id=?`
			args = []any{in.ProjectID, in.RequestID, tokenID}
		}
		x, e := s.readOperation(ctx, q, where, false, args...)
		if e != nil {
			return ErrForbidden
		}
		if !allowed(t, x.project, "read", "", "") {
			return ErrForbidden
		}
		out = operationView(x)
		for _, op := range out.ChangedOperations {
			if !allowed(t, x.project, "read", sectionFor(op), "") {
				return ErrForbidden
			}
		}
		return s.audit(ctx, q, tokenID, "mcp_config_operation_read", x.project, x.draft, nil)
	})
	if e != nil {
		return nil, e
	}
	if e = s.fresh(ctx, tokenID, g); e != nil {
		return nil, e
	}
	return out, nil
}

// FencePending is an explicit browser-admin recovery action, never an automatic
// retry. A delayed original executor must fail its state/owner check afterwards.
func (s *Service) FencePending(ctx context.Context, adminID, operationID string) error {
	return configstate.WithWriteTx(ctx, s.db, func(q configstate.DBTX) error {
		if e := configstate.LockEpochs(ctx, q, s.db, configstate.EpochPolicy, configstate.EpochSecrets, configstate.EpochExecutor); e != nil {
			return e
		}
		if s.opts.AuthorizeAdmin == nil || s.opts.AuthorizeAdmin(ctx, q, adminID) != nil {
			return ErrForbidden
		}
		x, e := s.readOperation(ctx, q, `id=?`, false, operationID)
		if e != nil {
			return ErrForbidden
		}
		if _, e = configstate.LockProject(ctx, q, s.db, x.project); e != nil {
			return e
		}
		x, e = s.readOperation(ctx, q, `id=?`, true, operationID)
		if e != nil {
			return e
		}
		if x.state != "pending" {
			return nil
		}
		res, e := q.ExecContext(ctx, `UPDATE mcp_config_operations SET owner_epoch=?,state='failed',reason='interrupted_not_applied',updated_at=? WHERE id=? AND state='pending' AND owner_epoch=?`, uuid.NewString(), s.opts.Now().UnixMilli(), x.id, x.owner)
		if e != nil {
			return e
		}
		n, e := res.RowsAffected()
		if e != nil || n != 1 {
			return ErrIdempotency
		}
		return s.audit(ctx, q, "admin", "mcp_config_operation_fenced", x.project, x.draft, nil)
	})
}
