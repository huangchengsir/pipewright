package tenancy

import (
	"context"
	"database/sql"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/configstate"
)

// RequireInstanceAdministrator protects controller-local/root execution and
// cluster-global maintenance. Ordinary grants never confer this boundary.
func RequireInstanceAdministrator(ctx context.Context, db *sql.DB) error {
	on, err := modeEnabled(ctx, configstate.Queryer(ctx, db))
	if err != nil || !on {
		return err
	}
	authority, _, err := authorityForRequest(ctx, db)
	if err != nil {
		return err
	}
	if err = automationAllows(ctx, db, "system.manage", "instance", "", ""); err != nil {
		return err
	}
	if !authority.Principal.InstanceAdmin {
		return ErrForbidden
	}
	return nil
}

// CurrentPrincipal resolves the live server-authenticated actor for fixed
// execution boundaries. It never accepts a caller-supplied administrator flag.
func CurrentPrincipal(ctx context.Context, db *sql.DB) (authz.Principal, bool, error) {
	on, err := modeEnabled(ctx, configstate.Queryer(ctx, db))
	if err != nil || !on {
		return authz.Principal{}, on, err
	}
	authority, _, err := authorityForRequest(ctx, db)
	return authority.Principal, true, err
}
