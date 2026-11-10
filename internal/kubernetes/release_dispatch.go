package kubernetes

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/configstate"
)

func (s *Service) ActivateReleaseRun(ctx context.Context, run, owner string) error {
	rows, e := configstate.Queryer(ctx, s.db).QueryContext(ctx, `SELECT id FROM k8s_release_continuations WHERE run_id=? AND state='delivering' ORDER BY id`, run)
	if e != nil {
		return e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, id := range ids {
		if e = s.ActivateReleaseContinuation(ctx, id, owner); e != nil {
			return e
		}
	}
	return nil
}
func (s *Service) PreserveReleaseSuspension(ctx context.Context, run string) error {
	return s.write(ctx, func(ctx context.Context, q dbtx) error {
		var count int
		if e := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM k8s_releases WHERE run_id=? AND state='paused' AND owner_fenced=1 AND cleanup_state='verified'`, run).Scan(&count); e != nil {
			return e
		}
		if count == 0 {
			return ErrConflict
		}
		return requireOne(q.ExecContext(ctx, `UPDATE pipeline_runs SET status='waiting_action' WHERE id=? AND status IN ('running','waiting_action') AND finished_at IS NULL`, run))
	})
}
func (s *Service) DeliverReleaseContinuations(ctx context.Context, owner string, enqueue func(string) error) error {
	pending, e := s.PendingReleaseContinuations(ctx)
	if e != nil {
		return e
	}
	var result error
	for _, entry := range pending {
		claim, e := s.ClaimReleaseContinuation(ctx, entry.ID, owner)
		if errors.Is(e, ErrHeld) || errors.Is(e, ErrConflict) {
			continue
		}
		if e != nil {
			result = errors.Join(result, e)
			continue
		}
		result = errors.Join(result, enqueue(claim.RunID))
	}
	return result
}
func (s *Service) releaseRuntimeBlocked(ctx context.Context, id string) error {
	return s.write(ctx, func(ctx context.Context, q dbtx) error {
		lease, e := s.releaseRuntime(ctx, q, id)
		if e != nil {
			return e
		}
		if lease.Runtime.Cleanup == CleanupVerified {
			return nil
		}
		lease.Runtime.Cleanup = CleanupBlocked
		return s.saveReleaseRuntime(ctx, q, &lease)
	})
}
func (r *Runtime) CleanupReleaseRuntime(ctx context.Context, id string) (ReleaseRuntimeLease, error) {
	lease, e := r.store.GetReleaseRuntime(ctx, id)
	if e != nil {
		return lease, e
	}
	if lease.Runtime.Cleanup == CleanupVerified {
		return lease, nil
	}
	if lease.ReleaseID != "" {
		parent, e := r.store.GetRelease(ctx, lease.ReleaseID)
		if e != nil {
			return lease, e
		}
		if !parent.OwnerFenced {
			return lease, ErrOwner
		}
	}
	transport, e := r.factory(ctx, lease.Authorization.Request.Executor, lease.Runtime.SSHSnapshot)
	if e != nil {
		return lease, e
	}
	session := &ReleaseSession{runtime: r, transport: transport, lease: lease}
	return session.Close(ctx)
}

// Recovery only accepts owners proved departed by the local OS-lock journal.
// It cleans their exact private tools, never redispatches an unknown API action.
func (c *ReleaseController) RecoverDeparted(ctx context.Context, owners []string) error {
	allowed := map[string]bool{}
	for _, owner := range owners {
		if validID(owner) {
			allowed[owner] = true
		}
	}
	if len(allowed) == 0 {
		return nil
	}
	rows, e := c.store.db.QueryContext(ctx, `SELECT r.id,r.owner_epoch,r.owner_fenced,COALESCE(c.owner_epoch,'') FROM k8s_releases r LEFT JOIN k8s_release_continuations c ON c.release_id=r.id AND c.state IN ('pending','delivering','running') WHERE r.owner_fenced=0 OR r.cleanup_state<>'verified' OR r.state IN ('prepared','awaiting_approval','running','paused','resume_requested','outcome_unknown')`)
	if e != nil {
		return e
	}
	ids := []string{}
	seen := map[string]bool{}
	for rows.Next() {
		var id, owner, claimOwner string
		var fenced bool
		if e = rows.Scan(&id, &owner, &fenced, &claimOwner); e != nil {
			rows.Close()
			return e
		}
		if !seen[id] && (allowed[owner] || fenced && allowed[claimOwner]) {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	var result error
	for _, id := range ids {
		e = c.store.write(ctx, func(ctx context.Context, q dbtx) error {
			r, e := c.store.release(ctx, q, id)
			if e != nil {
				return e
			}
			if !allowed[r.OwnerEpoch] {
				if r.OwnerFenced {
					return nil
				}
				return ErrOwner
			}
			_, e = q.ExecContext(ctx, `UPDATE k8s_releases SET owner_fenced=1,revision=revision+1 WHERE id=? AND owner_fenced=0`, id)
			return e
		})
		result = errors.Join(result, e)
	}
	rows, e = c.store.db.QueryContext(ctx, `SELECT id,owner_epoch FROM k8s_release_runtime WHERE cleanup_state<>'verified'`)
	if e != nil {
		return errors.Join(result, e)
	}
	leases := []string{}
	for rows.Next() {
		var id, owner string
		if e = rows.Scan(&id, &owner); e != nil {
			rows.Close()
			return errors.Join(result, e)
		}
		if allowed[owner] {
			leases = append(leases, id)
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return errors.Join(result, e)
	}
	for _, id := range leases {
		_, e = c.runtime.CleanupReleaseRuntime(ctx, id)
		result = errors.Join(result, e)
	}
	for _, id := range ids {
		r, e := c.store.GetRelease(ctx, id)
		if e != nil {
			result = errors.Join(result, e)
			continue
		}
		if r.Cleanup != CleanupVerified {
			continue
		}
		rows, e := c.store.db.QueryContext(ctx, `SELECT id,state,owner_epoch FROM k8s_release_continuations WHERE release_id=? AND state IN ('pending','delivering','running')`, id)
		if e != nil {
			result = errors.Join(result, e)
			continue
		}
		type requestOwner struct{ id, owner string }
		requests := []requestOwner{}
		activeRequests := false
		for rows.Next() {
			var key, state, claimOwner string
			if e = rows.Scan(&key, &state, &claimOwner); e != nil {
				break
			}
			activeRequests = true
			proofOwner := r.OwnerEpoch
			if state == "delivering" {
				proofOwner = claimOwner
			}
			if allowed[proofOwner] {
				requests = append(requests, requestOwner{key, proofOwner})
			}
		}
		rows.Close()
		if e != nil {
			result = errors.Join(result, e)
			continue
		}
		for _, request := range requests {
			result = errors.Join(result, c.store.RecoverReleaseContinuation(ctx, request.id, request.owner))
		}
		if activeRequests || r.State == "paused" || r.State == "resume_requested" {
			continue
		}
		result = errors.Join(result, c.store.settleDepartedRelease(ctx, id, allowed))
	}
	return result
}

// Only explicit OS-journal departed-owner recovery may settle an already fenced
// release. Ordinary mutation/terminal methods keep rejecting fenced owners.
func (s *Service) settleDepartedRelease(ctx context.Context, id string, departed map[string]bool) error {
	return s.write(ctx, func(ctx context.Context, q dbtx) error {
		r, e := s.release(ctx, q, id)
		if e != nil {
			return e
		}
		if !departed[r.OwnerEpoch] || !r.OwnerFenced || r.Cleanup != CleanupVerified {
			return ErrOwner
		}
		if r.State == "paused" || r.State == "resume_requested" || r.State == "rollback_in_progress" {
			return nil
		}
		var active int
		if e = q.QueryRowContext(ctx, "SELECT COUNT(*) FROM k8s_release_continuations WHERE release_id=? AND state IN ('pending','delivering','running')", id).Scan(&active); e != nil {
			return e
		}
		if active > 0 {
			return nil
		}
		facts, e := s.ListReleaseActions(ctx, id)
		if e != nil {
			return e
		}
		state := releaseFailureState(facts)
		if state != "outcome_unknown" && (r.State == "ready" || r.State == "rolled_back") {
			state = r.State
		}
		if e = requireOne(q.ExecContext(ctx, "UPDATE k8s_releases SET state=?,reason_code='worker_interrupted',revision=revision+1,updated_at=? WHERE id=? AND revision=? AND owner_epoch=? AND owner_fenced=1 AND cleanup_state='verified'", state, s.now().UnixMilli(), id, r.Revision, r.OwnerEpoch)); e != nil {
			return e
		}
		if r.Plan.RollbackOf != "" && (state == "partial_failed" || state == "rolled_back") {
			if _, e = q.ExecContext(ctx, "UPDATE k8s_releases SET state=?,reason_code='worker_interrupted',revision=revision+1,updated_at=? WHERE id=? AND state='rollback_in_progress'", state, s.now().UnixMilli(), r.Plan.RollbackOf); e != nil {
				return e
			}
		}
		if state != "outcome_unknown" {
			_, e = q.ExecContext(ctx, "DELETE FROM k8s_resource_gates WHERE owner_kind='v2' AND owner_id=? AND owner_epoch=?", id, r.OwnerEpoch)
		}
		return e
	})
}
