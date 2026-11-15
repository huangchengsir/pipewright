package tenancy

import (
	"context"
	"database/sql"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/configstate"
)

type executionVerifierKey struct{}
type actionBoundaryKey struct{}

// WithExecutionVerifier is installed by the controller after loading a sealed
// plan, never from browser input. Missing verification in an authorized job is
// an integration error and must not enable a legacy execution path.
func WithExecutionVerifier(ctx context.Context, verify func(context.Context) error) context.Context {
	return context.WithValue(ctx, executionVerifierKey{}, verify)
}

// WithActionBoundary adds a current-authority check for an independent operation.
// It cannot replace the sealed-run verifier. Callbacks only check server-derived
// identities and must not perform network IO or mutate data.
func WithActionBoundary(ctx context.Context, check func(context.Context) error) context.Context {
	previous, _ := ctx.Value(actionBoundaryKey{}).(func(context.Context) error)
	return context.WithValue(ctx, actionBoundaryKey{}, func(current context.Context) error {
		if previous != nil {
			if err := previous(current); err != nil {
				return err
			}
		}
		if check == nil {
			return ErrForbidden
		}
		return check(current)
	})
}
func HasAuthorizationContext(ctx context.Context) bool {
	_, actor := ctx.Value(actorContextKey{}).(requestActor)
	_, job := ctx.Value(executionContextKey{}).(executionContext)
	_, observation := ctx.Value(observationKey{}).(*observationPrincipal)
	_, token := ctx.Value(mcpContextKey{}).(mcpContext)
	_, automation := ctx.Value(automationKey{}).(*automationPrincipal)
	return actor || job || observation || token || automation
}
func VerifyExecution(ctx context.Context) error {
	if cap, ok := ctx.Value(automationKey{}).(*automationPrincipal); ok {
		if _, _, err := automationAuthority(ctx, cap.db, cap); err != nil {
			return err
		}
	}
	if cap, ok := ctx.Value(mcpContextKey{}).(mcpContext); ok {
		if _, _, _, err := mcpAuthority(ctx, cap.db, cap); err != nil {
			return err
		}
	}
	if cap, ok := ctx.Value(observationKey{}).(*observationPrincipal); ok {
		if _, err := observationAuthority(ctx, cap.db, cap); err != nil {
			return err
		}
	}
	if check, ok := ctx.Value(actionBoundaryKey{}).(func(context.Context) error); ok {
		if err := check(ctx); err != nil {
			return err
		}
	}

	if _, ok := ctx.Value(executionContextKey{}).(executionContext); !ok {
		return nil
	}
	verify, ok := ctx.Value(executionVerifierKey{}).(func(context.Context) error)
	if !ok || verify == nil {
		return ErrForbidden
	}
	return verify(ctx)
}

// CheckRunBoundary rechecks the complete approved resource ceiling against
// current grants. Logout is irrelevant; disablement, changes to referenced
// configuration, and revoked roles stop the next side effect.
func CheckRunBoundary(ctx context.Context, db *sql.DB, runID string) error {
	job, ok := ctx.Value(executionContextKey{}).(executionContext)
	if !ok || job.db != db || job.runID != runID {
		return ErrForbidden
	}
	return configstate.WithReadTxContext(ctx, db, func(txctx context.Context, q configstate.DBTX) error {
		authority, cap, err := loadRunAuthority(txctx, q, runID)
		if err != nil {
			return err
		}
		if job.planDigest == "" || job.planDigest != cap.digest {
			return ErrForbidden
		}
		for _, permission := range cap.permissions {
			definition, ok := authz.Lookup(permission.Action)
			if !ok {
				return ErrForbidden
			}
			resource, err := executionResource(txctx, q, cap.team, cap.project, definition, permission)
			if err != nil {
				return err
			}
			allowed, err := authority.Service.Allowed(authority.Principal, permission.Action, resource)
			if err != nil {
				return err
			}
			if !allowed {
				return ErrForbidden
			}
		}
		return nil
	})
}

// CheckExecutionInputOverride protects caller-authored commands, paths and health
// probes. A sealed worker uses its compiler-verified snapshot instead; a browser
// or MCP request must have configuration edit authority for this exact project.
func CheckExecutionInputOverride(ctx context.Context, db *sql.DB, project string) error {
	on, err := modeEnabled(ctx, configstate.Queryer(ctx, db))
	if err != nil || !on {
		return err
	}
	if job, ok := ctx.Value(executionContextKey{}).(executionContext); ok {
		if job.db != db {
			return ErrForbidden
		}
		if err = VerifyExecution(ctx); err != nil {
			return err
		}
		_, cap, err := loadRunAuthority(ctx, configstate.Queryer(ctx, db), job.runID)
		if err != nil {
			return err
		}
		if cap.project != project {
			return ErrForbidden
		}
		return nil
	}
	return Check(ctx, db, "pipeline.edit", "project", project)
}
