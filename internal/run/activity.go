package run

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/huangchengsir/pipewright/internal/configstate"
)

// These holds protect readers of current project configuration. They do not
// assert that remote effects have stopped, and are never expired by elapsed time.
func (s *service) lockRunProject(ctx context.Context, q configstate.DBTX, id string) (string, error) {
	var projectID string
	if err := q.QueryRowContext(ctx, `SELECT project_id FROM pipeline_runs WHERE id = ?`, id).Scan(&projectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	_, err := configstate.LockProject(ctx, q, s.db, projectID)
	return projectID, err
}

// A queued cancellation/rejection may release only the hold which no worker
// has claimed. Worker claim takes the same project lock before any config read.
func (s *service) releaseQueuedActivityTx(ctx context.Context, q configstate.DBTX, runID string) error {
	var phase, owner string
	err := q.QueryRowContext(ctx, `SELECT phase,owner_epoch FROM project_activity WHERE activity_id = ? AND released_at IS NULL`, runID).Scan(&phase, &owner)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if owner != s.activityOwner || phase != "queued" {
		return nil
	}
	if err := configstate.ReleaseActivityTx(ctx, q, runID, owner); err != nil {
		return err
	}
	configstate.AfterCommit(ctx, func() { configstate.ForgetActivityReaders(runID, owner) })
	return nil
}

// Preserve the legacy non-idempotent dispatch contract: a definite rejection
// discards the queued row and releases its hold atomically. Unknown dispatch
// errors retain both; no automatic replay is safe in that case.
func (s *service) discardRejectedRun(ctx context.Context, r *Run) error {
	return configstate.WithWriteTxContext(ctx, s.db, func(txctx context.Context, q configstate.DBTX) error {
		if _, err := s.lockRunProject(txctx, q, r.ID); err != nil {
			return err
		}
		res, err := q.ExecContext(txctx, `DELETE FROM pipeline_runs WHERE id = ? AND status = ?`, r.ID, StatusQueued)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil || n == 0 {
			return err
		}
		return s.releaseQueuedActivityTx(txctx, q, r.ID)
	})
}

func (s *service) claimRunActivity(ctx context.Context, runID string) error {
	if err := configstate.StartActivityReaders(runID, s.activityOwner); err != nil {
		return err
	}
	return configstate.WithWriteTxContext(ctx, s.db, func(txctx context.Context, q configstate.DBTX) error {
		projectID, err := s.lockRunProject(txctx, q, runID)
		if err != nil {
			return err
		}
		var status string
		if err := q.QueryRowContext(txctx, `SELECT status FROM pipeline_runs WHERE id = ?`, runID).Scan(&status); err != nil {
			return err
		}
		if status != StatusQueued {
			return ErrInvalidTransition
		}
		var owner string
		err = q.QueryRowContext(txctx, `SELECT owner_epoch FROM project_activity WHERE activity_id = ? AND released_at IS NULL`, runID).Scan(&owner)
		if errors.Is(err, sql.ErrNoRows) {
			// Old callers may have inserted queued work directly. The queued row
			// itself blocks config writes until this locked admission is complete.
			_, err = configstate.AcquireActivityTx(txctx, q, s.db, configstate.Activity{ID: runID, ProjectID: projectID, RunID: runID, OwnerEpoch: s.activityOwner, Kind: "run", Phase: "queued"})
			owner = s.activityOwner
		}
		if err != nil {
			return err
		}
		if owner != s.activityOwner {
			return configstate.ErrOwner
		}
		if err := configstate.SetActivityPhaseTx(txctx, q, runID, owner, "executing"); err != nil {
			return err
		}
		return s.transition(txctx, runID, StatusQueued, StatusRunning, true)
	})
}

func (s *service) updateRunActivity(runID, phase string, release bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := configstate.WithWriteTxContext(ctx, s.db, func(txctx context.Context, q configstate.DBTX) error {
		// Retention may delete a terminal run while its finalizing hook is still
		// reading configuration. The durable activity is the release authority;
		// its identity deliberately survives run deletion.
		var projectID string
		if err := q.QueryRowContext(txctx, `SELECT project_id FROM project_activity WHERE activity_id=? AND owner_epoch=?`, runID, s.activityOwner).Scan(&projectID); err != nil {
			return err
		}
		if _, err := configstate.LockProject(txctx, q, s.db, projectID); err != nil {
			return err
		}
		if release {
			if rr, ok := s.retryRuntime.(interface {
				MarkReaderExit(context.Context, string, string, string) error
			}); ok {
				if err := rr.MarkReaderExit(txctx, runID, runID, s.activityOwner); err != nil {
					return err
				}
			}
			if rr, ok := s.retryRuntime.(interface {
				ResourcesDrained(context.Context, string) (bool, error)
			}); ok {
				drained, err := rr.ResourcesDrained(txctx, runID)
				if err != nil {
					return err
				}
				if !drained {
					return configstate.SetActivityPhaseTx(txctx, q, runID, s.activityOwner, "cleanup_unknown")
				}
			}
			return configstate.ReleaseActivityTx(txctx, q, runID, s.activityOwner)
		}
		return configstate.SetActivityPhaseTx(txctx, q, runID, s.activityOwner, phase)
	})
	if err != nil {
		log.Printf("[run] activity for run %s retained after %s: %v", runID, phase, err)
	} else if release {
		var active int
		if checkErr := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM project_activity WHERE activity_id=? AND released_at IS NULL", runID).Scan(&active); checkErr != nil || active != 0 {
			return
		}
		configstate.ForgetActivityReaders(runID, s.activityOwner)
		s.settleRetryAttempt(runID)
	}
}

func (s *service) releaseUnclaimedActivity(runID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = configstate.WithWriteTxContext(ctx, s.db, func(txctx context.Context, q configstate.DBTX) error {
		if _, err := s.lockRunProject(txctx, q, runID); err != nil {
			return err
		}
		var status string
		if err := q.QueryRowContext(txctx, `SELECT status FROM pipeline_runs WHERE id=?`, runID).Scan(&status); err != nil {
			return err
		}
		if !IsTerminal(status) {
			return nil
		}
		return s.releaseQueuedActivityTx(txctx, q, runID)
	})
}

// One durable hold spans the worker and every asynchronously scheduled hook.
// References are created before spawning, so there is no unprotected gap after
// publishing a terminal run status. A process crash deliberately leaves the hold.
type activityReaders struct {
	mu      sync.Mutex
	readers int
	svc     *service
	runID   string
	armed   bool
}

type readerCohortKey struct {
	svc   *service
	runID string
}

// Duplicate dispatch attempts must participate in the same local reader count.
// An unsuccessful claimant cannot prove exit while a successful worker or its
// hooks still use the durable hold.
var readerCohorts = struct {
	sync.Mutex
	items map[readerCohortKey]*activityReaders
}{items: make(map[readerCohortKey]*activityReaders)}

func (s *service) newActivityReaders(runID string) *activityReaders {
	readerCohorts.Lock()
	defer readerCohorts.Unlock()
	key := readerCohortKey{s, runID}
	if a := readerCohorts.items[key]; a != nil {
		a.mu.Lock()
		a.readers++
		a.mu.Unlock()
		return a
	}
	a := &activityReaders{readers: 1, svc: s, runID: runID}
	readerCohorts.items[key] = a
	return a
}

func (a *activityReaders) arm() { a.mu.Lock(); a.armed = true; a.mu.Unlock() }

func (a *activityReaders) add() func() {
	a.mu.Lock()
	a.readers++
	a.mu.Unlock()
	return a.done
}

func (a *activityReaders) done() {
	readerCohorts.Lock()
	a.mu.Lock()
	a.readers--
	last := a.readers == 0
	armed := a.armed
	a.mu.Unlock()
	if last {
		delete(readerCohorts.items, readerCohortKey{a.svc, a.runID})
		// Even an unarmed worker has really exited. COMMIT may have succeeded
		// despite a driver error, so preserve that fact for maintenance without
		// asserting that an executing hold was never persisted. Keep cohort
		// admission serialized until the exit proof is recorded.
		configstate.RecordActivityReadersExited(a.runID, a.svc.activityOwner)
	}
	readerCohorts.Unlock()
	if last && armed {
		a.svc.updateRunActivity(a.runID, "reader_exit", true)
	} else if last {
		a.svc.releaseUnclaimedActivity(a.runID)
	}
}

func completed(callbacks []func()) {
	for _, fn := range callbacks {
		if fn != nil {
			fn()
		}
	}
}
