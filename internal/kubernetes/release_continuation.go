package kubernetes

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/configstate"
)

const releaseCheckpointPurpose = "pipewright.k8s.release-checkpoint.v2"

type ReleaseSuspension struct {
	Version int               `json:"version"`
	RunID   string            `json:"runId"`
	NodeKey string            `json:"nodeKey"`
	PlanID  string            `json:"planId"`
	Cursor  int               `json:"cursor"`
	Stages  map[string]string `json:"stages"`
	Nodes   map[string]string `json:"nodes"`
}
type ReleaseContinuation struct {
	OwnerEpoch       string            `json:"-"`
	ReaderOwnerEpoch string            `json:"-"`
	ID               string            `json:"id"`
	ReleaseID        string            `json:"releaseId"`
	RunID            string            `json:"runId"`
	Revision         int64             `json:"releaseRevision"`
	Action           string            `json:"action"`
	State            string            `json:"state"`
	Checkpoint       ReleaseSuspension `json:"-"`
}

// SuspendRelease commits the safe execution frontier only after every running
// branch has settled and the private tool was positively cleaned up. Waiting
// work retains resource gates but releases the worker slot and records no end.
func (s *Service) SuspendRelease(ctx context.Context, id, owner string, expected int64, cp ReleaseSuspension) (ReleaseRecord, error) {
	return s.mutateRelease(ctx, id, owner, expected, func(ctx context.Context, q dbtx, r *ReleaseRecord) error {
		if r.State != "running" || r.Cleanup != CleanupVerified || cp.Version != 2 || cp.PlanID != r.PlanID || cp.RunID != r.RunID || cp.NodeKey != r.NodeKey || cp.Cursor != r.Cursor+1 {
			return ErrConflict
		}
		for _, status := range cp.Stages {
			switch status {
			case "success", "failed", "skipped", "pending", "waiting_action":
			default:
				return ErrInvalid
			}
		}
		for _, status := range cp.Nodes {
			switch status {
			case "success", "failed", "skipped", "pending", "waiting_action":
			default:
				return ErrInvalid
			}
		}
		var count int
		if e := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM k8s_release_actions WHERE release_id=? AND state NOT IN ('verified','rejected')`, id).Scan(&count); e != nil {
			return e
		}
		if count != 0 {
			return ErrOutcomeUnknown
		}
		if _, e := q.ExecContext(ctx, `UPDATE k8s_release_continuations SET state='completed',updated_at=? WHERE release_id=? AND state='running'`, s.now().UnixMilli(), r.ID); e != nil {
			return e
		}
		r.State = "paused"
		r.OwnerFenced = true
		r.Cursor = cp.Cursor
		key := "kcont_" + uuid.NewString()
		b, _ := json.Marshal(cp)
		defer clear(b)
		cipher, e := s.seal(releaseCheckpointPurpose, key, 1, b)
		if e != nil {
			return e
		}
		now := s.now().UnixMilli()
		if _, e = q.ExecContext(ctx, `INSERT INTO k8s_release_continuations(id,release_id,run_id,release_revision,action,state,checkpoint_cipher,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, key, id, r.RunID, r.Revision+1, "continue", "waiting", cipher, now, now); e != nil {
			return e
		}
		if e = requireOne(q.ExecContext(ctx, `UPDATE pipeline_runs SET status='waiting_action' WHERE id=? AND status IN ('running','waiting_action') AND finished_at IS NULL`, r.RunID)); e != nil {
			return e
		}

		return nil
	})
}

// RequestReleaseContinue is an idempotent durable request. Enqueue is deliberately
// outside this transaction: the dispatcher retries delivery, while the run's
// queued->running claim prevents duplicate execution after a lost acknowledgement.
func (s *Service) RequestReleaseContinue(ctx context.Context, id string, expected int64, planID string) (ReleaseContinuation, error) {
	var out ReleaseContinuation
	e := s.write(ctx, func(ctx context.Context, q dbtx) error {
		r, e := s.release(ctx, q, id)
		if e != nil {
			return e
		}
		if r.Revision != expected || r.State != "paused" || r.PlanID != planID || !r.OwnerFenced || r.Cleanup != CleanupVerified {
			return ErrConflict
		}
		if e = s.validateReleaseAuthorization(ctx, q, r.Plan.Authorization); e != nil {
			return e
		}
		if e = q.QueryRowContext(ctx, `SELECT id,run_id FROM k8s_release_continuations WHERE release_id=? AND release_revision=? AND state='waiting'`, id, expected).Scan(&out.ID, &out.RunID); e != nil {
			return rowError(e)
		}
		var runState string
		var ended any
		if e = q.QueryRowContext(ctx, `SELECT status,finished_at FROM pipeline_runs WHERE id=?`, r.RunID).Scan(&runState, &ended); e != nil {
			return e
		}
		if runState != "waiting_action" || ended != nil {
			return ErrConflict
		}
		now := s.now().UnixMilli()
		if e = requireOne(q.ExecContext(ctx, `UPDATE k8s_release_continuations SET state='pending',updated_at=? WHERE id=? AND state='waiting'`, now, out.ID)); e != nil {
			return e
		}
		if e = requireOne(q.ExecContext(ctx, `UPDATE k8s_releases SET state='resume_requested',revision=revision+1,updated_at=? WHERE id=? AND revision=?`, now, id, expected)); e != nil {
			return e
		}
		out.ReleaseID = id
		out.Revision = expected + 1
		out.Action = "continue"
		out.State = "pending"
		return nil
	})
	return out, e
}
func (s *Service) PendingReleaseContinuations(ctx context.Context) ([]ReleaseContinuation, error) {
	rows, e := configstate.Queryer(ctx, s.db).QueryContext(ctx, `SELECT id,release_id,run_id,release_revision,action,state FROM k8s_release_continuations WHERE state IN ('pending','delivering') ORDER BY created_at,id LIMIT 500`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []ReleaseContinuation{}
	for rows.Next() {
		var c ReleaseContinuation
		if e = rows.Scan(&c.ID, &c.ReleaseID, &c.RunID, &c.Revision, &c.Action, &c.State); e != nil {
			return nil, e
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func (s *Service) ClaimReleaseContinuation(ctx context.Context, id, owner string) (ReleaseContinuation, error) {
	var out ReleaseContinuation
	if !validID(owner) {
		return out, ErrInvalid
	}
	e := s.write(ctx, func(ctx context.Context, q dbtx) error {
		var cipher []byte
		if e := q.QueryRowContext(ctx, `SELECT release_id,run_id,release_revision,action,state,checkpoint_cipher,owner_epoch,reader_owner_epoch FROM k8s_release_continuations WHERE id=?`, id).Scan(&out.ReleaseID, &out.RunID, &out.Revision, &out.Action, &out.State, &cipher, &out.OwnerEpoch, &out.ReaderOwnerEpoch); e != nil {
			return rowError(e)
		}
		out.ID = id
		if out.State == "delivering" && out.OwnerEpoch != owner {
			return ErrHeld
		}
		var activeReaders int
		if e := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM project_activity WHERE run_id=? AND released_at IS NULL`, out.RunID).Scan(&activeReaders); e != nil {
			return e
		}
		if activeReaders > 0 {
			if out.Action != "rollback" || activeReaders != 1 {
				return ErrHeld
			}
			var primary, phase, status string
			if e := q.QueryRowContext(ctx, `SELECT a.owner_epoch,a.phase,p.status FROM project_activity a JOIN pipeline_runs p ON p.id=a.run_id WHERE a.activity_id=? AND a.run_id=? AND a.released_at IS NULL`, out.RunID, out.RunID).Scan(&primary, &phase, &status); e != nil {
				return ErrHeld
			}
			if phase != "queued" || status != "queued" {
				return ErrHeld
			}
		}
		if out.State != "pending" && out.State != "delivering" {
			return ErrConflict
		}
		r, e := s.release(ctx, q, out.ReleaseID)
		if e != nil {
			return e
		}
		if out.Action == "rollback" {
			if r.Plan.RollbackOf == "" || !r.Plan.ForceApproval || r.Cleanup != CleanupVerified || (r.State != "prepared" && r.State != "awaiting_approval" && r.State != "running") || (!r.OwnerFenced && r.OwnerEpoch != owner) {
				return ErrConflict
			}
		} else if r.State != "resume_requested" || r.Cleanup != CleanupVerified || !r.OwnerFenced {
			return ErrConflict
		}
		if e = s.validateReleaseAuthorization(ctx, q, r.Plan.Authorization); e != nil {
			return e
		}
		body, e := s.open(releaseCheckpointPurpose, id, 1, cipher)
		if e != nil {
			return e
		}
		defer clear(body)
		if json.Unmarshal(body, &out.Checkpoint) != nil || out.Checkpoint.PlanID != r.PlanID {
			return ErrDecrypt
		}
		now := s.now().UnixMilli()
		if e = requireOne(q.ExecContext(ctx, `UPDATE k8s_release_continuations SET state='delivering',owner_epoch=?,updated_at=? WHERE id=? AND state IN ('pending','delivering')`, owner, now, id)); e != nil {
			return e
		}
		var status string
		if e = q.QueryRowContext(ctx, `SELECT status FROM pipeline_runs WHERE id=?`, r.RunID).Scan(&status); e != nil {
			return e
		}
		if status == "waiting_action" {
			if e = requireOne(q.ExecContext(ctx, `UPDATE pipeline_runs SET status='queued' WHERE id=? AND status='waiting_action' AND finished_at IS NULL`, r.RunID)); e != nil {
				return e
			}
		} else if status != "queued" {
			return ErrConflict
		}
		out.State = "delivering"
		return nil
	})
	return out, e
}

// ActivateReleaseContinuation must be called by the worker after winning the
// run claim, not by the HTTP handler or delivery loop. The old writer remains
// fenced until this ownership transaction completes.
func (s *Service) ActivateReleaseContinuation(ctx context.Context, id, owner string) error {
	if !validID(id) || !validID(owner) {
		return ErrInvalid
	}
	return s.write(ctx, func(ctx context.Context, q dbtx) error {
		var releaseID, state, claimOwner, readerOwner, action string
		if e := q.QueryRowContext(ctx, `SELECT release_id,state,owner_epoch,reader_owner_epoch,action FROM k8s_release_continuations WHERE id=?`, id).Scan(&releaseID, &state, &claimOwner, &readerOwner, &action); e != nil {
			return rowError(e)
		}
		var runID, runState string
		if e := q.QueryRowContext(ctx, `SELECT run_id FROM k8s_release_continuations WHERE id=?`, id).Scan(&runID); e != nil {
			return e
		}
		if e := q.QueryRowContext(ctx, `SELECT status FROM pipeline_runs WHERE id=?`, runID).Scan(&runState); e != nil {
			return e
		}
		if runState != "running" {
			return ErrOwner
		}
		if state != "delivering" {
			return ErrConflict
		}
		if claimOwner != owner {
			return ErrOwner
		}
		r, e := s.release(ctx, q, releaseID)
		if e != nil {
			return e
		}
		nextState := "running"
		if action == "rollback" {
			if r.Plan.RollbackOf == "" || !r.Plan.ForceApproval || r.Cleanup != CleanupVerified || (r.State != "prepared" && r.State != "awaiting_approval" && r.State != "running") || (!r.OwnerFenced && r.OwnerEpoch != owner) {
				return ErrConflict
			}
			nextState = r.State
		} else if r.State != "resume_requested" || !r.OwnerFenced || r.Cleanup != CleanupVerified {
			return ErrConflict
		}
		if e = s.validateReleaseAuthorization(ctx, q, r.Plan.Authorization); e != nil {
			return e
		}
		actualReader, e := releaseReaderOwner(ctx, q, r.RunID)
		if e != nil {
			return e
		}
		if actualReader != readerOwner {
			return ErrOwner
		}
		now := s.now().UnixMilli()
		if e = requireOne(q.ExecContext(ctx, `UPDATE k8s_releases SET state=?,owner_epoch=?,reader_owner_epoch=?,owner_fenced=0,revision=revision+1,updated_at=? WHERE id=? AND revision=?`, nextState, owner, readerOwner, now, r.ID, r.Revision)); e != nil {
			return e
		}
		if _, e = q.ExecContext(ctx, `UPDATE k8s_resource_gates SET owner_epoch=? WHERE owner_kind='v2' AND owner_id=? AND owner_epoch=?`, owner, r.ID, r.OwnerEpoch); e != nil {
			return e
		}
		return requireOne(q.ExecContext(ctx, `UPDATE k8s_release_continuations SET state='running',updated_at=? WHERE id=? AND state='delivering'`, now, id))
	})
}
func (s *Service) SetReleaseTerminal(ctx context.Context, id, owner string, expected int64, state, reason string) (ReleaseRecord, error) {
	return s.mutateRelease(ctx, id, owner, expected, func(ctx context.Context, q dbtx, r *ReleaseRecord) error {
		switch state {
		case "ready", "failed", "partial_failed", "outcome_unknown", "rolled_back":
		default:
			return ErrInvalid
		}
		var pending int
		if e := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM k8s_release_actions WHERE release_id=? AND state IN ('outcome_unknown','submitted')`, id).Scan(&pending); e != nil {
			return e
		}
		if pending > 0 && state != "outcome_unknown" {
			return ErrOutcomeUnknown
		}
		// A verified business result survives independent runtime cleanup failure.
		r.State = state
		r.ReasonCode = ReleaseSafeReason(reason)
		r.OwnerFenced = true
		if r.Plan.RollbackOf != "" && (state == "rolled_back" || state == "partial_failed") {
			if _, e := q.ExecContext(ctx, `UPDATE k8s_releases SET state=?,reason_code=?,revision=revision+1,updated_at=? WHERE id=? AND state='rollback_in_progress'`, state, ReleaseSafeReason(reason), s.now().UnixMilli(), r.Plan.RollbackOf); e != nil {
				return e
			}
		}
		continuationState := "failed"
		if state == "ready" || state == "rolled_back" {
			continuationState = "completed"
		}
		if state == "outcome_unknown" {
			continuationState = "blocked_unknown"
		}
		_, e := q.ExecContext(ctx, `UPDATE k8s_release_continuations SET state=?,updated_at=? WHERE release_id=? AND state='running'`, continuationState, time.Now().UnixMilli(), id)
		return e
	})
}

// RecoverReleaseContinuation handles only a departed writer with a positively
// verified cleanup. It preserves the user's already-durable continue request;
// unknown external requests remain blocked and are never replayed.
func (s *Service) RecoverReleaseContinuation(ctx context.Context, id, departedOwner string) error {
	var blocked error
	err := s.write(ctx, func(ctx context.Context, q dbtx) error {
		var releaseID, state, claimOwner, readerOwner, action string
		if e := q.QueryRowContext(ctx, `SELECT release_id,state,owner_epoch,reader_owner_epoch,action FROM k8s_release_continuations WHERE id=?`, id).Scan(&releaseID, &state, &claimOwner, &readerOwner, &action); e != nil {
			return rowError(e)
		}
		if state != "pending" && state != "delivering" && state != "running" {
			return ErrConflict
		}
		r, e := s.release(ctx, q, releaseID)
		if e != nil {
			return e
		}
		if (state == "delivering" && claimOwner != departedOwner) || (state != "delivering" && r.OwnerEpoch != departedOwner) || r.Cleanup != CleanupVerified {
			return ErrRuntimeUnverified
		}
		var unknown int
		if e = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_retry_resources WHERE attempt_id=? AND state<>'drained'`, r.RunID).Scan(&unknown); e != nil {
			return e
		}
		if unknown > 0 {
			return ErrRuntimeUnverified
		}
		if readerOwner == "" {
			readerOwner = r.ReaderOwnerEpoch
		}
		if e = s.releaseDepartedContinuationReaders(ctx, q, r, readerOwner); e != nil {
			return e
		}
		if e = q.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM k8s_release_actions WHERE release_id=? AND state IN ('outcome_unknown','submitted'))+(SELECT COUNT(*) FROM run_retry_nodes WHERE attempt_id=? AND status='outcome_unknown')+(SELECT COUNT(*) FROM run_retry_external_targets WHERE attempt_id=? AND state IN ('running','outcome_unknown'))`, releaseID, r.RunID, r.RunID).Scan(&unknown); e != nil {
			return e
		}
		if unknown > 0 {
			now := s.now()
			if e = requireOne(q.ExecContext(ctx, `UPDATE k8s_releases SET state='outcome_unknown',owner_fenced=1,reason_code='worker_interrupted',revision=revision+1,updated_at=? WHERE id=? AND revision=?`, now.UnixMilli(), r.ID, r.Revision)); e != nil {
				return e
			}
			if e = requireOne(q.ExecContext(ctx, `UPDATE k8s_release_continuations SET state='blocked_unknown',updated_at=? WHERE id=? AND state=?`, now.UnixMilli(), id, state)); e != nil {
				return e
			}
			if _, e = q.ExecContext(ctx, `UPDATE pipeline_runs SET status='failed',finished_at=COALESCE(finished_at,?) WHERE id=? AND status IN ('running','queued','waiting_action')`, now.UTC().Format(time.RFC3339), r.RunID); e != nil {
				return e
			}
			blocked = ErrOutcomeUnknown
			return nil
		}
		now := s.now().UnixMilli()
		if e = requireOne(q.ExecContext(ctx, `UPDATE k8s_releases SET state=?,owner_fenced=1,revision=revision+1,updated_at=? WHERE id=? AND revision=?`, func() string {
			if action == "rollback" {
				return r.State
			}
			return "resume_requested"
		}(), now, r.ID, r.Revision)); e != nil {
			return e
		}
		if e = requireOne(q.ExecContext(ctx, `UPDATE k8s_release_continuations SET state='pending',owner_epoch='',reader_owner_epoch='',updated_at=? WHERE id=? AND state=?`, now, id, state)); e != nil {
			return e
		}
		_, e = q.ExecContext(ctx, `UPDATE pipeline_runs SET status='waiting_action',finished_at=NULL WHERE id=? AND status IN ('running','queued','waiting_action')`, r.RunID)
		return e
	})
	if err != nil {
		return err
	}
	return blocked
}
