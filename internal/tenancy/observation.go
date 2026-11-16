package tenancy

import (
	"context"
	"database/sql"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"strings"
	"time"
)

type observationKey struct{}
type observationPrincipal struct {
	db                       *sql.DB
	user, team, project, run string
	epoch, generation        uint64
	revision                 int64
	expires                  time.Time
	permissions              []ExecutionPermission
}

// ObservationContext is a controller-only, bounded, in-memory authority. It
// intentionally cannot be serialized or restored after restart: existing guard
// recovery interrupts observations. Browser logout does not cancel an accepted
// observation; disablement, mode/config changes and revoked grants do.
func ObservationContext(ctx context.Context, db *sql.DB, project, runID string, servers []string) (context.Context, error) {
	if _, ok := ctx.Value(observationKey{}).(*observationPrincipal); ok {
		return nil, ErrForbidden
	}
	q := configstate.Queryer(ctx, db)
	on, err := modeEnabled(ctx, q)
	if err != nil {
		return nil, err
	}
	if !on {
		return context.Background(), nil
	}
	if len(servers) == 0 || len(servers) > 5 {
		return nil, ErrInvalidInput
	}
	authority, request, err := authorityForRequest(ctx, db)
	if err != nil {
		return nil, err
	}
	if request.serviceRunID != "" {
		return nil, ErrForbidden
	}
	cap := &observationPrincipal{db: db, user: request.actor.UserID, team: request.teamID, project: project, run: runID, epoch: request.actor.ModeEpoch, generation: request.actor.SecurityGeneration, expires: time.Now().Add(35 * time.Minute)}
	cap.revision, err = configstate.ReadRevision(ctx, q, project)
	if err != nil {
		return nil, err
	}
	cap.permissions = []ExecutionPermission{{Action: "guard.observe", ResourceID: runID}, {Action: "guard.read", ResourceID: runID}}
	for _, id := range servers {
		var credential string
		if err = q.QueryRowContext(ctx, `SELECT credential_id FROM servers WHERE id=?`, id).Scan(&credential); err != nil {
			return nil, ErrForbidden
		}
		cap.permissions = append(cap.permissions, ExecutionPermission{Action: "server.use", ResourceID: id}, ExecutionPermission{Action: "server.inspect", ResourceID: id}, ExecutionPermission{Action: "credential.use", ResourceID: credential}, ExecutionPermission{Action: "credential.metadata", ResourceID: credential})
	}
	for _, permission := range cap.permissions {
		definition, known := authz.Lookup(permission.Action)
		if !known {
			return nil, ErrForbidden
		}
		resource, e := executionResource(ctx, q, cap.team, project, definition, permission)
		if e != nil {
			return nil, e
		}
		yes, e := authority.Service.Allowed(authority.Principal, permission.Action, resource)
		if e != nil {
			return nil, e
		}
		if !yes {
			return nil, ErrForbidden
		}
	}
	out := context.WithValue(context.Background(), observationKey{}, cap)
	return configstate.WithWriteAdmission(out, LockAuthorization), nil
}

func observationAuthority(ctx context.Context, db *sql.DB, cap *observationPrincipal) (Authority, error) {
	if cap == nil || cap.db != db || !time.Now().Before(cap.expires) {
		return Authority{}, ErrForbidden
	}
	q := configstate.Queryer(ctx, db)
	on, err := modeEnabled(ctx, q)
	if err != nil {
		return Authority{}, err
	}
	if !on {
		return Authority{}, ErrForbidden
	}
	var epoch uint64
	if err = q.QueryRowContext(ctx, `SELECT mode_epoch FROM access_control WHERE id=1`).Scan(&epoch); err != nil || epoch != cap.epoch {
		return Authority{}, ErrForbidden
	}
	revision, err := configstate.ReadRevision(ctx, q, cap.project)
	if err != nil || revision != cap.revision {
		return Authority{}, ErrForbidden
	}
	var u User
	var enabled, admin, restricted int
	if err = q.QueryRowContext(ctx, `SELECT id,enabled,instance_admin,must_change_password,security_generation FROM access_users WHERE id=?`, cap.user).Scan(&u.ID, &enabled, &admin, &restricted, &u.SecurityGeneration); err != nil || enabled != 1 || restricted != 0 || u.SecurityGeneration != cap.generation {
		return Authority{}, ErrForbidden
	}
	u.Enabled = true
	u.InstanceAdmin = admin == 1
	authority, err := loadSubjectAuthority(ctx, q, u, cap.team, nil)
	if err != nil {
		return Authority{}, err
	}
	for _, permission := range cap.permissions {
		definition, known := authz.Lookup(permission.Action)
		if !known {
			return Authority{}, ErrForbidden
		}
		resource, e := executionResource(ctx, q, cap.team, cap.project, definition, permission)
		if e != nil {
			return Authority{}, e
		}
		yes, e := authority.Service.Allowed(authority.Principal, permission.Action, resource)
		if e != nil {
			return Authority{}, e
		}
		if !yes {
			return Authority{}, ErrForbidden
		}
	}
	return authority, nil
}
func observationPredicate(ctx context.Context, db *sql.DB, cap *observationPrincipal, action, column string) (authz.Predicate, error) {
	authority, err := observationAuthority(ctx, db, cap)
	if err != nil {
		return authz.Predicate{}, err
	}
	predicate, err := authority.Service.RootPredicate(authority.Principal, action, column)
	if err != nil {
		return authz.Predicate{}, err
	}
	ids := []string{}
	seen := map[string]bool{}
	for _, permission := range cap.permissions {
		if permission.Action == action && !seen[permission.ResourceID] {
			ids = append(ids, permission.ResourceID)
			seen[permission.ResourceID] = true
		}
	}
	if len(ids) == 0 {
		return authz.Predicate{SQL: "1=0"}, nil
	}
	predicate.SQL = "(" + predicate.SQL + ") AND " + column + " IN (" + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + ") AND EXISTS (SELECT 1 FROM access_users azou JOIN project_config_versions azov ON azov.project_id=? WHERE azou.id=? AND azou.enabled=1 AND azou.must_change_password=0 AND azou.security_generation=? AND azov.revision=? AND azov.deleted_at IS NULL)"
	for _, id := range ids {
		predicate.Args = append(predicate.Args, id)
	}
	predicate.Args = append(predicate.Args, cap.project, cap.user, cap.generation, cap.revision)
	return predicate, nil
}
