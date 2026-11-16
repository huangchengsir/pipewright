package tenancy

import (
	"context"
	"database/sql"
	"errors"
	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/configstate"
)

// RegisterEnvironment records a configured name using a stable authorization ID.
// Existing purpose is immutable here; ordinary pipeline edits can never turn a
// production environment into test. Removed names remain for historical runs.
func RegisterEnvironment(ctx context.Context, db *sql.DB, project, name string) error {
	q := configstate.Queryer(ctx, db)
	on, err := modeEnabled(ctx, q)
	if err != nil || !on {
		return err
	}
	if q == db || !validName(name) {
		return ErrInvalidInput
	}
	if err = Check(ctx, db, "pipeline.edit", "project", project); err != nil {
		return err
	}
	request, ok := ctx.Value(actorContextKey{}).(requestActor)
	if !ok {
		return ErrForbidden
	}
	var id string
	err = q.QueryRowContext(ctx, `SELECT id FROM access_environments WHERE tenant_id=? AND project_id=? AND name=?`, request.teamID, project, name).Scan(&id)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	id = uuid.NewString()
	if _, err = q.ExecContext(ctx, `INSERT INTO access_environments(id,tenant_id,project_id,name,purpose,revision) VALUES(?,?,?,?,'other',1)`, id, request.teamID, project, name); err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `INSERT INTO access_ownership(resource_kind,resource_id,tenant_id,owner_id,project_id,environment_id) VALUES('environment',?,?,?,?,?)`, id, request.teamID, request.actor.UserID, project, id)
	return err
}
