package releaseguard

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/tenancy"
)

// Mutation authorization and persistence share the policy fence; worker ledger
// finalization uses save directly so revoked work can still record its stop.
func (s *Service) authorizeGuard(ctx context.Context, project, id, action string) (*document, error) {
	return s.authorizedDocument(ctx, project, id, action)
}
func (s *Service) guardWrite(ctx context.Context, runID, action string, fn func(context.Context, configstate.DBTX) error) error {
	ctx = configstate.WithWriteAdmission(ctx, tenancy.LockAuthorization)
	return configstate.WithWriteTxContext(ctx, s.db, func(txctx context.Context, q configstate.DBTX) error {
		if err := tenancy.Check(txctx, s.db, action, "run", runID); err != nil {
			return err
		}
		return fn(txctx, q)
	})
}
func (s *Service) saveAuthorized(ctx context.Context, d *document, action, kind string, e *Evidence) error {
	return s.guardWrite(ctx, d.Report.RunID, action, func(txctx context.Context, q configstate.DBTX) error { return s.save(txctx, d, kind, e) })
}
