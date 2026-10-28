package runretry

import (
	"context"
	"database/sql"
	"errors"
	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/store"
	"time"
)

func (s *Service) request(ctx context.Context, root, scope, id string) (*Request, string, error) {
	r := &Request{RequestID: id, LogicalRunID: root}
	var fp string
	var ms int64
	e := s.q(ctx).QueryRowContext(ctx, `SELECT input_digest,source_attempt_id,attempt_id,state,reason_code,created_at,initiator FROM run_retry_requests WHERE root_id=? AND caller_scope=? AND request_id=?`, root, scope, id).Scan(&fp, &r.SourceAttemptID, &r.AttemptRunID, &r.State, &r.ReasonCode, &ms, &r.Initiator)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	r.CreatedAt = time.UnixMilli(ms).UTC()
	return r, fp, e
}
func (s *Service) Request(ctx context.Context, runID, scope, id string) (*Request, error) {
	if !validIdentity(scope) || !validIdentity(id) {
		return nil, ErrInvalid
	}
	var root string
	e := s.q(ctx).QueryRowContext(ctx, "SELECT root_id FROM run_retry_attempts WHERE run_id=?", runID).Scan(&root)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if e != nil {
		return nil, e
	}
	r, _, e := s.request(ctx, root, scope, id)
	return r, e
}
func (s *Service) Submit(ctx context.Context, runID string, in SubmitInput) (*Request, error) {
	if !validIdentity(in.Scope) || !validIdentity(in.RequestID) || in.BaseAttemptID == "" || in.BaseRevision < 1 || len(in.PlanFingerprint) != 64 {
		return nil, ErrInvalid
	}
	targets, e := cleanTargets(in.TargetNodeIDs)
	if e != nil {
		return nil, e
	}
	in.TargetNodeIDs = targets
	if len(in.RebuildNodeIDs) > 0 {
		in.RebuildNodeIDs, e = cleanTargets(in.RebuildNodeIDs)
		if e != nil {
			return nil, e
		}
	}
	if len(in.Initiator) > 191 {
		return nil, ErrInvalid
	}
	fp := digest(struct {
		Input     SubmitInput
		Initiator string
	}{in, in.Initiator})
	var root string
	if e = s.q(ctx).QueryRowContext(ctx, "SELECT root_id FROM run_retry_attempts WHERE run_id=?", runID).Scan(&root); errors.Is(e, sql.ErrNoRows) {
		return nil, ErrUnavailable
	} else if e != nil {
		return nil, e
	}
	// Lookup precedes all stale-plan checks. A response-lost caller always receives
	// the original identity even after another attempt advanced the lineage head.
	if r, old, err := s.request(ctx, root, in.Scope, in.RequestID); err == nil {
		if old != fp {
			return nil, ErrConflict
		}
		return r, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	if s.child == nil || s.dispatch == nil {
		return nil, ErrUnavailable
	}
	// A concurrent identical caller may commit while read-only preflight runs.
	// Return that original request before classifying any stale/preflight result.
	existingOr := func(cause error) (*Request, error) {
		if r, old, err := s.request(ctx, root, in.Scope, in.RequestID); err == nil {
			if old != fp {
				return nil, ErrConflict
			}
			return r, nil
		} else if !errors.Is(err, ErrNotFound) {
			return nil, err
		}
		return nil, cause
	}
	currentIdentity, identityErr := s.load(ctx, runID)
	if identityErr != nil {
		return existingOr(identityErr)
	}
	if currentIdentity.head != runID || currentIdentity.head != in.BaseAttemptID || currentIdentity.revision != in.BaseRevision {
		return existingOr(ErrConflict)
	}
	ctx, e = s.planCheckContext(ctx, runID)
	if e != nil {
		return existingOr(e)
	}
	preflight, e := s.Plan(ctx, runID, targets, PlanOptions{RebuildNodeIDs: in.RebuildNodeIDs})
	if e != nil {
		return existingOr(e)
	}
	if preflight.BaseAttemptID != in.BaseAttemptID || preflight.BaseRevision != in.BaseRevision {
		return existingOr(ErrConflict)
	}
	if !preflight.CanSubmit {
		return existingOr(ErrBlocked)
	}
	if preflight.PlanFingerprint != in.PlanFingerprint {
		return existingOr(ErrConflict)
	}
	l, e := s.load(ctx, runID)
	if e != nil {
		return nil, e
	}
	var out *Request
	e = configstate.WithWriteTxContext(ctx, s.db, func(tx context.Context, q configstate.DBTX) error {
		if e := s.lock(tx, q, l); e != nil {
			return e
		}
		if r, old, err := s.request(tx, root, in.Scope, in.RequestID); err == nil {
			if old != fp {
				return ErrConflict
			}
			out = r
			return nil
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		current, err := s.load(tx, runID)
		if err != nil {
			return err
		}
		if current.head != in.BaseAttemptID || current.revision != in.BaseRevision || runID != current.head {
			return ErrConflict
		}
		p, err := s.makePlan(tx, current, runID, targets, PlanOptions{RebuildNodeIDs: in.RebuildNodeIDs})
		if err != nil {
			return err
		}
		if p.PlanFingerprint != in.PlanFingerprint {
			return ErrConflict
		}
		if !p.CanSubmit {
			return ErrBlocked
		}
		if err = configstate.CheckMaintenanceAdmission(tx, q); err != nil {
			return err
		}
		if s.admission != nil {
			if err = s.admission(tx, q, current.snapshot); err != nil {
				return err
			}
		}
		id := uuid.NewString()
		now := time.Now().UTC()
		revision := current.revision + 1
		if err = s.child(tx, q, ChildInput{ID: id, ProjectID: current.project, RootID: root, ParentID: current.head, Trigger: current.snapshot.Trigger, CreatedAt: now}); err != nil {
			return err
		}
		if _, err = q.ExecContext(tx, `INSERT INTO run_retry_attempts(run_id,root_id,parent_id,revision,execution_kind,status,created_at,initiator) VALUES(?,?,?,?,'recovery','queued',?,?)`, id, root, current.head, revision, now.UnixMilli(), in.Initiator); err != nil {
			return err
		}
		// Immutable within this locked transaction. Loading once avoids repeatedly
		// materializing every checkpoint for each successful inherited node.
		records, err := s.records(tx, current.head)
		if err != nil {
			return err
		}
		for _, pn := range p.Nodes {
			d := pn.Decision
			status, origin := d.Status, d.Action
			source, sourceKey := d.SourceAttemptID, d.SourceNodeKey
			if d.Action == "execute" || d.Action == "control" {
				status = "pending"
				origin = "executed"
				source = id
				sourceKey = pn.Node.Key
			}
			if status == "" {
				status = "pending"
			}
			if source == "" {
				source = current.head
				sourceKey = pn.Node.Key
			}
			if _, err = q.ExecContext(tx, `INSERT INTO run_retry_nodes(attempt_id,node_key,status,origin,source_attempt_id,source_node_key,updated_at) VALUES(?,?,?,?,?,?,?)`, id, pn.Node.Key, status, origin, source, sourceKey, now.UnixMilli()); err != nil {
				return err
			}
			if d.Action == "reuse" && pn.Node.JobID != "" {
				o, err := s.outcome(tx, current, pn.Node, records[pn.Node.Key])
				if err != nil {
					return err
				}
				keys := []string{"node"}
				for _, f := range o.Outputs.Files {
					keys = append(keys, f.Key)
				}
				for _, key := range keys {
					if _, err = q.ExecContext(tx, `INSERT INTO run_retry_refs(owner_attempt_id,producer_attempt_id,producer_node_key,object_key,purpose,created_at) VALUES(?,?,?,?,?,?) `+store.DoNothingSuffix(store.DialectOf(s.db), []string{"owner_attempt_id", "producer_attempt_id", "producer_node_key", "object_key"}), id, source, sourceKey, key, "recovery", now.UnixMilli()); err != nil {
						return err
					}
				}
			}
		}
		result, err := q.ExecContext(tx, `UPDATE run_retry_lineages SET head_attempt_id=?,revision=?,active_attempt_id=?,updated_at=? WHERE root_id=? AND revision=? AND head_attempt_id=?`, id, revision, id, now.UnixMilli(), root, current.revision, current.head)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil || n != 1 {
			return ErrConflict
		}
		if _, err = q.ExecContext(tx, `INSERT INTO run_retry_requests(root_id,caller_scope,request_id,input_digest,source_attempt_id,attempt_id,state,created_at,initiator) VALUES(?,?,?,?,?,?,'pending',?,?)`, root, in.Scope, in.RequestID, fp, current.head, id, now.UnixMilli(), in.Initiator); err != nil {
			return err
		}
		if _, err = q.ExecContext(tx, "INSERT INTO run_retry_dispatch(attempt_id,state,owner_epoch,updated_at) VALUES(?,'pending',?,?)", id, s.owner, now.UnixMilli()); err != nil {
			return err
		}
		out = &Request{RequestID: in.RequestID, LogicalRunID: root, SourceAttemptID: current.head, AttemptRunID: id, State: "pending", CreatedAt: now}
		if !configstate.AfterCommit(tx, func() { s.dispatchOnce(context.WithoutCancel(ctx), id) }) {
			return ErrUnavailable
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	r, _, e := s.request(ctx, root, in.Scope, in.RequestID)
	if e == nil {
		return r, nil
	}
	return out, nil
}
func (s *Service) dispatchOnce(ctx context.Context, id string) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	r, e := s.db.ExecContext(ctx, "UPDATE run_retry_dispatch SET state='dispatching',updated_at=? WHERE attempt_id=? AND state='pending' AND owner_epoch=?", time.Now().UnixMilli(), id, s.owner)
	if e != nil {
		return
	}
	n, e := r.RowsAffected()
	if e != nil || n != 1 {
		return
	}
	state, reason := "accepted", ""
	dispatchErr := s.dispatch(ctx, id)
	if dispatchErr != nil {
		state = "outcome_unknown"
		reason = "dispatch_outcome_unknown"
	}
	_ = configstate.WithWriteTxContext(ctx, s.db, func(tx context.Context, q configstate.DBTX) error {
		if errors.Is(dispatchErr, ErrDispatchRejected) && s.reject != nil {
			if err := s.reject(tx, id); err != nil {
				return err
			}
			state = "failed"
			reason = "dispatch_rejected"
			if _, err := q.ExecContext(tx, "UPDATE run_retry_attempts SET status='failed' WHERE run_id=?", id); err != nil {
				return err
			}
			var parent string
			if err := q.QueryRowContext(tx, "SELECT parent_id FROM run_retry_attempts WHERE run_id=?", id).Scan(&parent); err != nil {
				return err
			}
			previous, err := s.records(tx, parent)
			if err != nil {
				return err
			}
			// No business node started. The effective result is still its original
			// producer; preserve that provenance instead of inventing a fresh failure.
			for key, r := range previous {
				origin := "blocked"
				if r.status == "success" {
					origin = "reuse"
				}
				if r.status == "skipped" {
					origin = "skip"
				}
				if _, err = q.ExecContext(tx, "UPDATE run_retry_nodes SET status=?,origin=?,source_attempt_id=?,source_node_key=? WHERE attempt_id=? AND node_key=? AND status='pending'", r.status, origin, r.source, r.sourceKey, id, key); err != nil {
					return err
				}
			}
			if _, err := q.ExecContext(tx, "UPDATE run_retry_lineages SET active_attempt_id=NULL WHERE active_attempt_id=?", id); err != nil {
				return err
			}
			if _, err := q.ExecContext(tx, "UPDATE run_retry_refs SET released_at=? WHERE owner_attempt_id=? AND released_at IS NULL", time.Now().UnixMilli(), id); err != nil {
				return err
			}
		}
		if _, e := q.ExecContext(tx, "UPDATE run_retry_dispatch SET state=?,updated_at=? WHERE attempt_id=? AND state='dispatching' AND owner_epoch=?", state, time.Now().UnixMilli(), id, s.owner); e != nil {
			return e
		}
		// A fast worker may already have moved the request to running/completed.
		_, e := q.ExecContext(tx, "UPDATE run_retry_requests SET state=?,reason_code=? WHERE attempt_id=? AND state='pending'", state, reason, id)
		return e
	})
}

// Complete records business termination but deliberately keeps the active hold.
// Settle is separately called only after actual resource/readers drain.
func (s *Service) Complete(ctx context.Context, runID, status string) error {
	if !terminal(status) {
		return ErrInvalid
	}
	return configstate.WithWriteTxContext(ctx, s.db, func(tx context.Context, q configstate.DBTX) error {
		if _, e := q.ExecContext(tx, "UPDATE run_retry_attempts SET status=? WHERE run_id=?", status, runID); e != nil {
			return e
		}
		// Panic, cancellation or a lost Finish callback cannot prove that an
		// already-started external operation failed without effects.
		if _, e := q.ExecContext(tx, "UPDATE run_retry_nodes SET status='outcome_unknown',reason_code='node_outcome_unverified',updated_at=? WHERE attempt_id=? AND status='running'", time.Now().UnixMilli(), runID); e != nil {
			return e
		}
		state := "completed"
		if status != "success" {
			state = "failed"
		}
		var unknown int
		if _, e := q.ExecContext(tx, "UPDATE run_retry_external_targets SET state='outcome_unknown',updated_at=? WHERE attempt_id=? AND state='running'", time.Now().UnixMilli(), runID); e != nil {
			return e
		}
		if e := q.QueryRowContext(tx, "SELECT COUNT(*) FROM run_retry_nodes WHERE attempt_id=? AND status='outcome_unknown'", runID).Scan(&unknown); e != nil {
			return e
		}
		if unknown > 0 {
			state = "outcome_unknown"
		}
		var external int
		if e := q.QueryRowContext(tx, "SELECT COUNT(*) FROM run_retry_external_targets WHERE attempt_id=? AND state IN ('running','outcome_unknown')", runID).Scan(&external); e != nil {
			return e
		}
		if external > 0 {
			unknown += external
			state = "outcome_unknown"
		}
		if _, e := q.ExecContext(tx, "UPDATE run_retry_requests SET state=? WHERE attempt_id=? AND state<>'outcome_unknown'", state, runID); e != nil {
			return e
		}
		if unknown > 0 {
			if _, e := q.ExecContext(tx, "UPDATE run_retry_requests SET reason_code='node_outcome_unverified' WHERE attempt_id=?", runID); e != nil {
				return e
			}
		}
		_, e := q.ExecContext(tx, "UPDATE run_retry_dispatch SET state=?,updated_at=? WHERE attempt_id=? AND state<>'outcome_unknown'", state, time.Now().UnixMilli(), runID)
		return e
	})
}
func (s *Service) Settle(ctx context.Context, runID string) error {
	l, e := s.load(ctx, runID)
	if e != nil {
		return e
	}
	return configstate.WithWriteTxContext(ctx, s.db, func(tx context.Context, q configstate.DBTX) error {
		if e := s.lock(tx, q, l); e != nil {
			return e
		}
		// A deploy reader may finish before the enclosing stage cleanup. Its
		// exact local exit proof is retained; release it only once all persistent
		// resource/target facts now prove drained, never merely due to age.
		drained, err := s.ResourcesDrained(tx, runID)
		if err != nil {
			return err
		}
		if drained {
			rows, err := q.QueryContext(tx, `SELECT a.activity_id,a.owner_epoch FROM project_activity a JOIN run_retry_reader_exits e ON e.activity_id=a.activity_id AND e.attempt_id=a.run_id AND e.owner_epoch=a.owner_epoch WHERE a.run_id=? AND a.released_at IS NULL AND a.phase='cleanup_unknown' AND e.runtime_owner=?`, runID, s.owner)
			if err != nil {
				return err
			}
			type exit struct{ id, owner string }
			var exits []exit
			for rows.Next() {
				var item exit
				if err = rows.Scan(&item.id, &item.owner); err != nil {
					rows.Close()
					return err
				}
				exits = append(exits, item)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			for _, item := range exits {
				if err = configstate.ReleaseActivityTx(tx, q, item.id, item.owner); err != nil {
					return err
				}
			}
		}
		if e := s.quiescent(tx, q, runID); e != nil {
			return e
		}
		var unknown int
		if e := q.QueryRowContext(tx, "SELECT COUNT(*) FROM run_retry_nodes WHERE attempt_id=? AND status IN ('running','outcome_unknown')", runID).Scan(&unknown); e != nil {
			return e
		}
		if unknown > 0 {
			return ErrUnknown
		}
		if e := q.QueryRowContext(tx, "SELECT COUNT(*) FROM run_retry_dispatch WHERE attempt_id=? AND state='outcome_unknown'", runID).Scan(&unknown); e != nil {
			return e
		}
		if unknown > 0 {
			return ErrUnknown
		}
		if _, e := q.ExecContext(tx, "UPDATE run_retry_lineages SET active_attempt_id=NULL WHERE root_id=? AND active_attempt_id=?", l.root, runID); e != nil {
			return e
		}
		// Keep successful output references for descendants/history until the owner is
		// itself pruned by retention's reference-aware protocol.
		return nil
	})
}

// ReconcileStartup never re-enqueues a durable record from another process. No
// external exit evidence can be inferred from a timestamp or terminal run row.
func (s *Service) ReconcileStartup(ctx context.Context) error {
	return configstate.WithWriteTxContext(ctx, s.db, func(tx context.Context, q configstate.DBTX) error {
		if _, e := q.ExecContext(tx, `UPDATE run_retry_requests SET state='outcome_unknown',reason_code='process_restarted' WHERE attempt_id IN (SELECT attempt_id FROM run_retry_dispatch WHERE owner_epoch<>? AND state IN ('pending','dispatching','accepted','running'))`, s.owner); e != nil {
			return e
		}
		if _, e := q.ExecContext(tx, `UPDATE run_retry_nodes SET status='outcome_unknown',updated_at=? WHERE status='running' AND owner_epoch<>?`, time.Now().UnixMilli(), s.owner); e != nil {
			return e
		}
		if _, e := q.ExecContext(tx, `UPDATE run_retry_external_targets SET state='outcome_unknown',updated_at=? WHERE state='running' AND EXISTS(SELECT 1 FROM run_retry_nodes n WHERE n.attempt_id=run_retry_external_targets.attempt_id AND n.node_key=run_retry_external_targets.node_key AND n.owner_epoch<>?)`, time.Now().UnixMilli(), s.owner); e != nil {
			return e
		}
		_, e := q.ExecContext(tx, `UPDATE run_retry_dispatch SET state='outcome_unknown',updated_at=? WHERE owner_epoch<>? AND state IN ('pending','dispatching','accepted','running')`, time.Now().UnixMilli(), s.owner)
		return e
	})
}
