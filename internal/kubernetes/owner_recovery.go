package kubernetes

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/configstate"
)

// SettledOwners confirms a departed owner has no outstanding operation, hold or
// runtime lease. Only these names may be pruned from the local OS-lock journal.
// A database failure returns no confirmation; age is never considered.
func (s *Service) SettledOwners(ctx context.Context, owners []string) ([]string, error) {
	out := []string{}
	e := configstate.WithReadTxContext(ctx, s.db, func(ctx context.Context, q dbtx) error {
		for _, owner := range owners {
			if !validID(owner) {
				return ErrInvalid
			}
			var count int
			for _, query := range []string{
				`SELECT COUNT(*) FROM k8s_operations WHERE owner_epoch=? AND (owner_fenced=0 OR cleanup_state<>'verified' OR (retired=0 AND business_state NOT IN ('ready','unchanged','failed','superseded')))`,
				`SELECT COUNT(*) FROM k8s_workload_holds WHERE owner_epoch=?`,
				`SELECT COUNT(*) FROM k8s_runtime_leases WHERE owner_epoch=? AND cleanup_state<>'verified'`,
			} {
				var n int
				if e := q.QueryRowContext(ctx, query, owner).Scan(&n); e != nil {
					return e
				}
				count += n
			}
			if count == 0 {
				out = append(out, owner)
			}
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	return out, nil
}

// settleNeverStarted uses durable admission ordering: every private runtime is
// registered before any container creation. No lease + fenced owner + no dispatch
// or hold proves this particular failed operation never created runtime resources.
func (s *Service) settleNeverStarted(ctx context.Context, id, owner string) (bool, error) {
	settled := false
	e := s.write(ctx, func(ctx context.Context, q dbtx) error {
		o, e := s.operation(ctx, q, id)
		if e != nil {
			return e
		}
		if o.OwnerEpoch != owner || !o.OwnerFenced || o.DispatchFenced || o.Business != Failed {
			return nil
		}
		var n int
		if e = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM k8s_runtime_leases WHERE operation_id=?`, id).Scan(&n); e != nil {
			return e
		}
		if n != 0 {
			return nil
		}
		if e = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM k8s_workload_holds WHERE operation_id=?`, id).Scan(&n); e != nil {
			return e
		}
		if n != 0 {
			return nil
		}
		e = requireOne(q.ExecContext(ctx, `UPDATE k8s_operations SET cleanup_state='verified',revision=revision+1,updated_at=? WHERE id=? AND revision=?`, s.now().UnixMilli(), id, o.Revision))
		settled = e == nil
		return e
	})
	return settled, e
}
