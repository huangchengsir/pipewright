package dbmigration

import (
	"context"
	"database/sql"
	"errors"
	"github.com/huangchengsir/pipewright/internal/store"
)

var ErrPermissions = errors.New("dbmigration: target catalog permissions are insufficient")

// Catalog checks are read-only hints, never a promise that all target DDL can
// execute. MySQL roles, DM effective grants and SQLite filesystem ACLs require
// actual initialization to establish write capability; preflight never writes.
func targetPermissions(ctx context.Context, db *store.Store) (string, error) {
	var query string
	switch db.Dialect {
	case store.PostgreSQL:
		query = "SELECT CASE WHEN has_database_privilege(current_database(),'CONNECT') AND has_schema_privilege(current_user,current_schema(),'USAGE') AND has_schema_privilege(current_user,current_schema(),'CREATE') THEN 1 ELSE 0 END"
	case store.SQLServer:
		query = "SELECT CASE WHEN HAS_PERMS_BY_NAME(DB_NAME(),'DATABASE','CREATE TABLE')=1 AND HAS_PERMS_BY_NAME(SCHEMA_NAME(),'SCHEMA','ALTER')=1 THEN 1 WHEN HAS_PERMS_BY_NAME(DB_NAME(),'DATABASE','CREATE TABLE')=0 OR HAS_PERMS_BY_NAME(SCHEMA_NAME(),'SCHEMA','ALTER')=0 THEN 0 ELSE NULL END"
	default:
		return "initialization_required", nil
	}
	var allowed sql.NullInt64
	if e := db.DB.QueryRowContext(ctx, query).Scan(&allowed); e != nil || !allowed.Valid {
		return "initialization_required", nil
	}
	if allowed.Int64 != 1 {
		return "", ErrPermissions
	}
	return "catalog_confirmed", nil
}
