package kubernetes

import (
	"context"
	"database/sql"
	"errors"
	"github.com/huangchengsir/pipewright/internal/configstate"
)

func releaseReaderOwner(ctx context.Context, q dbtx, run string) (string, error) {
	var owner string
	e := q.QueryRowContext(ctx, `SELECT owner_epoch FROM project_activity WHERE activity_id=? AND run_id=? AND kind='run' AND released_at IS NULL`, run, run).Scan(&owner)
	if errors.Is(e, sql.ErrNoRows) {
		return "", nil
	}
	return owner, e
}

// BindReleaseRunReader is used by transactional run creation, before the new
// queued request can be delivered. It binds process reader identity, not a new
// permission or a changed release plan.
func (s *Service) BindReleaseRunReader(ctx context.Context, id, owner string) error {
	q := configstate.Queryer(ctx, s.db)
	if q == s.db {
		return ErrOwner
	}
	r, e := s.release(ctx, q, id)
	if e != nil {
		return e
	}
	if r.OwnerEpoch != owner || r.OwnerFenced {
		return ErrOwner
	}
	reader, e := releaseReaderOwner(ctx, q, r.RunID)
	if e != nil {
		return e
	}
	if reader == "" {
		return ErrOwner
	}
	if r.ReaderOwnerEpoch != "" && r.ReaderOwnerEpoch != reader {
		return ErrOwner
	}
	_, e = q.ExecContext(ctx, `UPDATE k8s_releases SET reader_owner_epoch=? WHERE id=? AND owner_epoch=?`, reader, id, owner)
	return e
}

// BindReleaseContinuationReader runs inside the winning queued->running claim.
// The dispatcher and worker must belong to the same proved process owner. This
// closes the window before ActivateReleaseContinuation has been called.
func (s *Service) BindReleaseContinuationReader(ctx context.Context, run, owner, reader string) error {
	q := configstate.Queryer(ctx, s.db)
	if q == s.db {
		return ErrOwner
	}
	rows, e := q.QueryContext(ctx, `SELECT id,owner_epoch,reader_owner_epoch FROM k8s_release_continuations WHERE run_id=? AND state='delivering'`, run)
	if e != nil {
		return e
	}
	type entry struct{ id, owner, reader string }
	entries := []entry{}
	for rows.Next() {
		var v entry
		if e = rows.Scan(&v.id, &v.owner, &v.reader); e != nil {
			rows.Close()
			return e
		}
		entries = append(entries, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, v := range entries {
		if v.owner != owner || v.reader != "" && v.reader != reader {
			return ErrOwner
		}
		if e = requireOne(q.ExecContext(ctx, `UPDATE k8s_release_continuations SET reader_owner_epoch=? WHERE id=? AND state='delivering' AND owner_epoch=?`, reader, v.id, owner)); e != nil {
			return e
		}
	}
	return nil
}

// Called only after the OS-lock owner journal proved this process departed and
// every owned runtime/resource/target was positively drained. Never use age or
// a request-supplied owner value as evidence. Other unproved readers stay held.
func (s *Service) releaseDepartedContinuationReaders(ctx context.Context, q dbtx, r ReleaseRecord, reader string) error {
	if _, e := configstate.LockProject(ctx, q, s.db, r.Plan.Authorization.Request.ProjectID); e != nil {
		return e
	}
	var actual, phase, status string
	e := q.QueryRowContext(ctx, `SELECT a.owner_epoch,a.phase,p.status FROM project_activity a JOIN pipeline_runs p ON p.id=a.run_id WHERE a.activity_id=? AND a.run_id=? AND a.released_at IS NULL`, r.RunID, r.RunID).Scan(&actual, &phase, &status)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	if e == nil {
		if phase != "queued" || status != "queued" {
			if reader == "" || actual != reader {
				return ErrRuntimeUnverified
			}
		}
		if e = configstate.ReleaseActivityTx(ctx, q, r.RunID, actual); e != nil {
			return e
		}
	}
	rows, e := q.QueryContext(ctx, `SELECT a.activity_id,a.owner_epoch FROM project_activity a JOIN run_retry_reader_exits e ON e.activity_id=a.activity_id AND e.attempt_id=a.run_id AND e.owner_epoch=a.owner_epoch WHERE a.run_id=? AND a.released_at IS NULL`, r.RunID)
	if e != nil {
		return e
	}
	type lease struct{ id, owner string }
	leases := []lease{}
	for rows.Next() {
		var v lease
		if e = rows.Scan(&v.id, &v.owner); e != nil {
			rows.Close()
			return e
		}
		leases = append(leases, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, v := range leases {
		if e = configstate.ReleaseActivityTx(ctx, q, v.id, v.owner); e != nil {
			return e
		}
	}
	return nil
}
