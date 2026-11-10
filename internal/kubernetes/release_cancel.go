package kubernetes

import "context"

// CancelReleaseRun participates in run.Cancel's project-locked transaction.
// It only fences execution and its outbox; it never performs a rollback or any
// remote cleanup. Unknown effects and unverified cleanup retain their gates.
func (s *Service) CancelReleaseRun(ctx context.Context, runID string) (safe bool, resultErr error) {
	safe = true
	resultErr = s.write(ctx, func(ctx context.Context, q dbtx) error {
		var status string
		if e := q.QueryRowContext(ctx, "SELECT status FROM pipeline_runs WHERE id=?", runID).Scan(&status); e != nil {
			return e
		}
		if status != "failed" {
			return ErrConflict
		}
		rows, e := q.QueryContext(ctx, "SELECT id FROM k8s_releases WHERE run_id=? ORDER BY created_at,id", runID)
		if e != nil {
			return e
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				break
			}
			ids = append(ids, id)
		}
		if e == nil {
			e = rows.Err()
		}
		rows.Close()
		if e != nil {
			return e
		}
		for _, id := range ids {
			r, e := s.release(ctx, q, id)
			if e != nil {
				return e
			}
			if r.State == "ready" || r.State == "rolled_back" {
				continue
			}
			facts, e := s.ListReleaseActions(ctx, id)
			if e != nil {
				return e
			}
			state := releaseFailureState(facts)
			if r.State == "outcome_unknown" {
				state = "outcome_unknown"
			}
			if e = requireOne(q.ExecContext(ctx, "UPDATE k8s_releases SET state=?,reason_code='run_canceled',owner_fenced=1,revision=revision+1,updated_at=? WHERE id=? AND revision=?", state, s.now().UnixMilli(), id, r.Revision)); e != nil {
				return e
			}
			if _, e = q.ExecContext(ctx, "UPDATE k8s_release_continuations SET state='failed',updated_at=? WHERE release_id=? AND state IN ('waiting','pending','delivering','running')", s.now().UnixMilli(), id); e != nil {
				return e
			}
			if state == "outcome_unknown" || r.Cleanup != CleanupVerified {
				safe = false
				continue
			}
			if _, e = q.ExecContext(ctx, "DELETE FROM k8s_resource_gates WHERE owner_kind='v2' AND owner_id=? AND owner_epoch=?", id, r.OwnerEpoch); e != nil {
				return e
			}
		}
		return nil
	})
	return safe, resultErr
}
