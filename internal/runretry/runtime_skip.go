package runretry

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/configstate"
)

// RecordUnstartedSkip seals a scheduler-proven skip without claiming an executor
// or cleanup owner. The transaction rejects inherited, begun or effectful nodes.
func RecordUnstartedSkip(ctx context.Context, n NodeRef, reason string) error {
	ex, ok := ExecutionFrom(ctx)
	if !ok {
		return ErrUnavailable
	}
	e, ok := ex.(*execution)
	if !ok || !e.valid(n) {
		return ErrInvalid
	}
	if reason != "condition_skipped" && reason != "dependency_skipped" && reason != "canceled_before_start" {
		return ErrInvalid
	}
	d := e.Decision(n)
	if d.Action != "execute" && d.Action != "control" {
		return ErrBlocked
	}
	var checkpointErr error
	err := configstate.WithWriteTxContext(ctx, e.svc.db, func(tx context.Context, q configstate.DBTX) error {
		if err := e.svc.lock(tx, q, e.l); err != nil {
			return err
		}
		// Reserve only a pending, unowned row. This is not executor admission: owner
		// stays empty and no resource may be allocated; Begin races lose the CAS.
		result, err := q.ExecContext(tx, "UPDATE run_retry_nodes SET status='running' WHERE attempt_id=? AND node_key=? AND status='pending' AND owner_epoch='' AND origin='executed'", e.runID, n.Key)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			return ErrConflict
		}
		if err = e.svc.noOwnedNodeEffects(tx, e.runID, n.Key); err != nil {
			return err
		}
		err = e.Finish(tx, n, Outcome{Status: "skipped", ExecutionMode: "real", ExternalOutcome: "none", ReasonCode: reason})
		if errors.Is(err, ErrCheckpoint) {
			checkpointErr = err
			return nil
		}
		return err
	})
	if err != nil {
		return err
	}
	return checkpointErr
}
