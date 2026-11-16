package library

import (
	"context"
	"database/sql"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/tenancy"
)

func mutate(ctx context.Context, db *sql.DB, fn func(context.Context) error) error {
	ctx = configstate.WithWriteAdmission(ctx, tenancy.LockAuthorization)
	return configstate.WithWriteTxContext(ctx, db, func(txctx context.Context, q configstate.DBTX) error {
		if err := configstate.LockEpochs(txctx, q, db, configstate.EpochPolicy, configstate.EpochSecrets, configstate.EpochExecutor); err != nil {
			return err
		}
		if err := fn(txctx); err != nil {
			return err
		}
		return configstate.BumpEpoch(txctx, q, configstate.EpochPolicy)
	})
}
func availableName(ctx context.Context, db *sql.DB, table, id, name string, taken error) (string, error) {
	scope, err := tenancy.ResourceNamespace(ctx, db)
	if err != nil {
		return "", err
	}
	// These identifiers are fixed internal call-site literals, never request input.
	if table != "pipeline_templates" && table != "variable_groups" && table != "custom_nodes" {
		return "", tenancy.ErrForbidden
	}
	if scope != "__single__" {
		var stored string
		err := configstate.Queryer(ctx, db).QueryRowContext(ctx, "SELECT name_scope FROM "+table+" WHERE id=?", id).Scan(&stored)
		if err == nil {
			scope = stored
		} else if err != sql.ErrNoRows {
			return "", err
		}
	}
	args := []any{name, id}
	where := "name=? AND id<>?"
	if scope != "__single__" {
		where += " AND name_scope=?"
		args = append(args, scope)
	}
	var n int
	err = configstate.Queryer(ctx, db).QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE "+where, args...).Scan(&n)
	if err != nil {
		return "", err
	}
	if n > 0 {
		return "", taken
	}
	return scope, nil
}
func removeOwnership(ctx context.Context, db *sql.DB, kind, id string) error {
	_, err := configstate.Queryer(ctx, db).ExecContext(ctx, `DELETE FROM access_ownership WHERE resource_kind=? AND resource_id=?`, kind, id)
	return err
}
