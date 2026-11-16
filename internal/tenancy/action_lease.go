package tenancy

import (
	"context"
	"database/sql"
	"time"
)

// ActionLease bounds an ongoing request by its original context and current
// concrete-resource authority. It is not a grant, cannot refresh a session or
// extend a deadline, and must be cancelled by the caller after the operation.
// Services still perform a final authorization check before returning data.
func ActionLease(parent context.Context, db *sql.DB, action, kind, id string) (context.Context, context.CancelFunc, error) {
	if err := Check(parent, db, action, kind, id); err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	enabled, err := Enabled(parent, db)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	if !enabled {
		return ctx, cancel, nil
	}
	go func() {
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if err := Check(ctx, db, action, kind, id); err != nil {
					cancel()
					return
				}
			}
		}
	}()
	return ctx, cancel, nil
}
