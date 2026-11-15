package run

import (
	"context"
	"database/sql"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/tenancy"
)

func editProjectConfiguration(ctx context.Context, db *sql.DB, id string, fn func(context.Context) error) error {
	if err := tenancy.Check(ctx, db, "pipeline.edit", "project", id); err != nil {
		return err
	}
	return configstate.WithWriteTxContext(ctx, db, func(c context.Context, _ configstate.DBTX) error {
		if err := tenancy.LockAuthorization(c, db); err != nil {
			return err
		}
		if err := tenancy.Check(c, db, "pipeline.edit", "project", id); err != nil {
			return err
		}
		return fn(c)
	})
}
