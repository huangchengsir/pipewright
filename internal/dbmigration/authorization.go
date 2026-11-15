package dbmigration

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/tenancy"
)

// AuthorizeConfiguration protects normal interactive database settings. Recovery
// has a separate password/CSRF session and must never enter this API surface.
func (r *Runtime) AuthorizeConfiguration(ctx context.Context) error {
	if r == nil || r.Recovery || r.Source == nil {
		return ErrInvalidState
	}
	return tenancy.RequireInstanceAdministrator(ctx, r.Source.DB)
}

// No remote network calls run while the administrator revocation fence is held.
func (r *Runtime) authorizedConfigurationWrite(ctx context.Context, fn func(context.Context) error) error {
	if err := r.AuthorizeConfiguration(ctx); err != nil {
		return err
	}
	if configstate.Queryer(ctx, r.Source.DB) != r.Source.DB {
		return ErrInvalidState
	}
	ctx = configstate.WithWriteAdmission(ctx, tenancy.LockCompletionAuthorization)
	return configstate.WithWriteTxContext(ctx, r.Source.DB, func(txctx context.Context, _ configstate.DBTX) error {
		if err := tenancy.LockCompletionAuthorization(txctx, r.Source.DB); err != nil {
			return err
		}
		if err := r.AuthorizeConfiguration(txctx); err != nil {
			return err
		}
		return fn(txctx)
	})
}
