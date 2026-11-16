package tenancy

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/configstate"
)

type actorContextKey struct{}
type requestActor struct {
	serviceRunID string
	actor        Actor
	teamID       string
}

// WithActor is for authenticated server adapters only. Team IDs are always
// rechecked against membership; callers must never deserialize Actor from JSON.
func WithActor(ctx context.Context, actor Actor, teamID string) context.Context {
	return configstate.WithWriteAdmission(context.WithValue(ctx, actorContextKey{}, requestActor{actor: actor, teamID: teamID}), LockAuthorization)
}
func modeEnabled(ctx context.Context, q configstate.DBTX) (bool, error) {
	var enabled, ever, catalog int
	var epoch, revision uint64
	err := q.QueryRowContext(ctx, `SELECT enabled,ever_enabled,mode_epoch,authz_revision,catalog_version FROM access_control WHERE id=1`).Scan(&enabled, &ever, &epoch, &revision, &catalog)
	if errors.Is(err, sql.ErrNoRows) {
		if _, ok := ctx.Value(automationKey{}).(*automationPrincipal); ok {
			return false, ErrConflict
		}
		if _, ok := ctx.Value(mcpContextKey{}).(mcpContext); ok {
			return false, ErrConflict
		}
		if _, ok := ctx.Value(observationKey{}).(*observationPrincipal); ok {
			return false, ErrConflict
		}
		if _, ok := ctx.Value(executionContextKey{}).(executionContext); ok {
			return false, ErrConflict
		}
		if _, ok := ctx.Value(actorContextKey{}).(requestActor); ok {
			return false, ErrConflict
		}
		if err = checkUninitialized(ctx, q); err != nil {
			return false, err
		}
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if catalog != authz.CatalogVersion || (enabled != 0 && enabled != 1) || (ever != 0 && ever != 1) || enabled > ever || epoch == 0 || revision == 0 {
		return false, ErrConflict
	}
	if enabled != 1 {
		if _, ok := ctx.Value(automationKey{}).(*automationPrincipal); ok {
			return false, ErrForbidden
		}
		if _, ok := ctx.Value(mcpContextKey{}).(mcpContext); ok {
			return false, ErrForbidden
		}
		if _, ok := ctx.Value(observationKey{}).(*observationPrincipal); ok {
			return false, ErrForbidden
		}
		if _, ok := ctx.Value(executionContextKey{}).(executionContext); ok {
			return false, ErrForbidden
		}
		if _, ok := ctx.Value(actorContextKey{}).(requestActor); ok {
			return false, ErrForbidden
		}
	}
	return enabled == 1, nil
}

// LockAuthorization serializes a business mutation against membership, roles,
// session/password revocation and mode changes. It must run inside the same
// configstate write transaction that performs the mutation, before authorization.
func LockAuthorization(ctx context.Context, db *sql.DB) error {
	return lockAuthorization(ctx, db, true)
}

// LockCompletionAuthorization is only for fixed existing-run cancellation and
// gate decisions. It preserves the revocation fence while allowing active work
// to drain during maintenance. It must never admit configuration or new work.
func LockCompletionAuthorization(ctx context.Context, db *sql.DB) error {
	return lockAuthorization(ctx, db, false)
}
func lockAuthorization(ctx context.Context, db *sql.DB, admit bool) error {
	q := configstate.Queryer(ctx, db)
	enabled, err := modeEnabled(ctx, q)
	if err != nil || !enabled {
		return err
	}
	if q == db {
		return ErrConflict
	}
	if admit {
		if err = configstate.CheckMaintenanceAdmission(ctx, q); err != nil {
			return err
		}
	}
	_, err = q.ExecContext(ctx, `UPDATE access_control SET authz_revision=authz_revision WHERE id=1`)
	return err
}
func authorityForRequest(ctx context.Context, db *sql.DB) (Authority, requestActor, error) {
	if cap, ok := ctx.Value(automationKey{}).(*automationPrincipal); ok {
		a, r, err := automationAuthority(ctx, db, cap)
		return a, requestActor{actor: Actor{UserID: r.user, ModeEpoch: r.epoch, SecurityGeneration: r.generation}, teamID: r.team}, err
	}
	if cap, ok := ctx.Value(mcpContextKey{}).(mcpContext); ok {
		a, r, _, err := mcpAuthority(ctx, db, cap)
		return a, r, err
	}
	if cap, ok := ctx.Value(observationKey{}).(*observationPrincipal); ok {
		authority, err := observationAuthority(ctx, db, cap)
		return authority, requestActor{actor: Actor{UserID: cap.user, ModeEpoch: cap.epoch, SecurityGeneration: cap.generation}, teamID: cap.team}, err
	}
	if job, ok := ctx.Value(executionContextKey{}).(executionContext); ok {
		if job.db != db {
			return Authority{}, requestActor{}, ErrForbidden
		}
		authority, cap, err := loadRunAuthority(ctx, configstate.Queryer(ctx, db), job.runID)
		if err == nil && (job.planDigest == "" || job.planDigest != cap.digest) {
			return Authority{}, requestActor{}, ErrForbidden
		}
		return authority, requestActor{actor: Actor{UserID: cap.user, ModeEpoch: cap.epoch, SecurityGeneration: cap.generation}, teamID: cap.team, serviceRunID: job.runID}, err
	}

	request, ok := ctx.Value(actorContextKey{}).(requestActor)
	if !ok || request.teamID == "" {
		return Authority{}, request, ErrForbidden
	}
	authority, err := NewRepository(db).Authority(ctx, request.actor, request.teamID)
	return authority, request, err
}

// QueryPredicate is used in domain queries before count, ordering and paging.
// It also checks live session expiry/logout at query time, not just when the
// request was admitted. No authenticated request context means deny when on.
func baseQueryPredicate(ctx context.Context, db *sql.DB, action, column string) (authz.Predicate, error) {
	if cap, ok := ctx.Value(automationKey{}).(*automationPrincipal); ok {
		return automationPredicate(ctx, db, cap, action, column)
	}
	if cap, ok := ctx.Value(observationKey{}).(*observationPrincipal); ok {
		return observationPredicate(ctx, db, cap, action, column)
	}
	q := configstate.Queryer(ctx, db)
	enabled, err := modeEnabled(ctx, q)
	if err != nil {
		return authz.Predicate{}, err
	}
	if !enabled {
		return authz.Predicate{SQL: "1=1"}, nil
	}
	authority, request, err := authorityForRequest(ctx, db)
	if err != nil {
		return authz.Predicate{}, err
	}
	// An approved execution may consume its exact fixed project inputs without
	// granting its initiator a configuration-inspection menu/API permission.
	if request.serviceRunID != "" && (action == "pipeline.read" || action == "project.read") {
		return executionInputPredicate(ctx, db, authority, request.serviceRunID, column)
	}
	predicate, err := authority.Service.RootPredicate(authority.Principal, action, column)
	if err != nil {
		return authz.Predicate{}, err
	}
	if preparation, ok := ctx.Value(preparationContextKey{}).(preparationContext); ok && (action == "pipeline.read" || action == "project.read") {
		if preparation.db != db || !authz.ValidRootColumn(column) {
			return authz.Predicate{SQL: "1=0"}, nil
		}
		if err = CheckRunAdmission(ctx, db, preparation.project, "prepared-run"); err != nil {
			return authz.Predicate{}, err
		}
		predicate = authz.Predicate{SQL: column + `=? AND EXISTS (SELECT 1 FROM access_control azpc WHERE azpc.id=1 AND azpc.enabled=1 AND azpc.mode_epoch=? AND azpc.authz_revision=?)`, Args: []any{preparation.project, authority.Principal.ModeEpoch, authority.Principal.Revision}}
	}

	if predicate.SQL == "1=0" {
		return predicate, nil
	}
	if _, ok := ctx.Value(mcpContextKey{}).(mcpContext); ok {
		return mcpPredicate(ctx, db, authority, action, column, predicate)
	}
	if request.serviceRunID != "" {
		_, cap, err := loadRunAuthority(ctx, q, request.serviceRunID)
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
		// column has already passed the catalog's strict qualified-identifier guard.
		predicate.SQL = "(" + predicate.SQL + ") AND " + column + " IN (" + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + ") AND EXISTS (SELECT 1 FROM access_run_authorizations azj JOIN access_users azu ON azu.id=azj.user_id JOIN project_config_versions azv ON azv.project_id=azj.project_id WHERE azj.run_id=? AND azj.revoked_at IS NULL AND azj.mode_epoch=? AND azj.user_generation=azu.security_generation AND azu.enabled=1 AND azu.must_change_password=0 AND azj.config_revision=azv.revision AND azv.deleted_at IS NULL)"
		for _, id := range ids {
			predicate.Args = append(predicate.Args, id)
		}
		predicate.Args = append(predicate.Args, cap.runID, cap.epoch)
		predicate.SQL += ` AND NOT EXISTS (SELECT 1 FROM access_run_authorizations azmc WHERE azmc.run_id=? AND azmc.mcp_token_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM mcp_tokens azmt WHERE azmt.id=azmc.mcp_token_id AND azmt.revoked_at IS NULL AND azmt.expires_at>? AND azmt.owner_mode_epoch=azmc.mode_epoch AND azmt.owner_generation=azmc.user_generation))`
		predicate.Args = append(predicate.Args, cap.runID, time.Now().UTC().Format(time.RFC3339))
		return predicate, nil
	}
	predicate.SQL = "(" + predicate.SQL + `) AND EXISTS (SELECT 1 FROM access_sessions azs JOIN access_users azu ON azu.id=azs.user_id WHERE azs.token_hash=? AND azs.user_id=? AND azs.mode_epoch=? AND azs.security_generation=? AND azs.expires_at>? AND azu.enabled=1 AND azu.must_change_password=0 AND azu.security_generation=azs.security_generation)`
	predicate.Args = append(predicate.Args, request.actor.SessionHash, request.actor.UserID, request.actor.ModeEpoch, request.actor.SecurityGeneration, time.Now().Unix())
	return predicate, nil
}
func Check(ctx context.Context, db *sql.DB, action, kind, id string) error {
	definition, known := authz.Lookup(action)
	if !known || definition.ResourceKind != kind || id == "" {
		return ErrForbidden
	}
	q := configstate.Queryer(ctx, db)
	predicate, err := QueryPredicate(ctx, db, action, "azo.resource_id")
	if err != nil {
		return err
	}
	if predicate.SQL == "1=1" {
		return nil
	}
	var count int
	args := []any{kind, id}
	args = append(args, predicate.Args...)
	if err = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM access_ownership azo WHERE azo.resource_kind=? AND azo.resource_id=? AND `+predicate.SQL, args...).Scan(&count); err != nil {
		return err
	}
	if count != 1 {
		return ErrForbidden
	}
	return nil
}

// CheckNew authorizes creation against the selected team's scope before a
// business row exists. RecordOwnership must be in that same fenced transaction.
func CheckNew(ctx context.Context, db *sql.DB, action, kind, id string) error {
	if _, ok := ctx.Value(automationKey{}).(*automationPrincipal); ok {
		return ErrForbidden
	}
	if _, ok := ctx.Value(observationKey{}).(*observationPrincipal); ok {
		return ErrForbidden
	}
	enabled, err := modeEnabled(ctx, configstate.Queryer(ctx, db))
	if err != nil || !enabled {
		return err
	}
	authority, request, err := authorityForRequest(ctx, db)
	if err != nil {
		return err
	}
	allowed, err := authority.Service.Allowed(authority.Principal, action, authz.Resource{TenantID: request.teamID, Kind: kind, ID: id, OwnerID: request.actor.UserID})
	if err != nil {
		return err
	}
	if !allowed {
		return ErrForbidden
	}
	return nil
}
func RecordOwnership(ctx context.Context, db *sql.DB, kind, id, projectID string) error {
	if _, ok := ctx.Value(observationKey{}).(*observationPrincipal); ok {
		return ErrForbidden
	}
	q := configstate.Queryer(ctx, db)
	enabled, err := modeEnabled(ctx, q)
	if err != nil || !enabled {
		return err
	}
	if q == db {
		return ErrConflict
	}
	_, request, err := authorityForRequest(ctx, db)
	if err != nil {
		return err
	}
	var project any
	if projectID != "" {
		project = projectID
	}
	_, err = q.ExecContext(ctx, `INSERT INTO access_ownership(resource_kind,resource_id,tenant_id,owner_id,project_id,environment_id) VALUES(?,?,?,?,?,NULL)`, kind, id, request.teamID, request.actor.UserID, project)
	return err
}

// Enabled reports the strictly validated persistent mode for domain adapters.
func Enabled(ctx context.Context, db *sql.DB) (bool, error) {
	return modeEnabled(ctx, configstate.Queryer(ctx, db))
}

// ResourceNamespace returns a server-resolved namespace for unique human names.
// It does not authorize an action; callers must also use Check/CheckNew.
func ResourceNamespace(ctx context.Context, db *sql.DB) (string, error) {
	enabled, err := modeEnabled(ctx, configstate.Queryer(ctx, db))
	if err != nil {
		return "", err
	}
	if !enabled {
		return "__single__", nil
	}
	_, request, err := authorityForRequest(ctx, db)
	if err != nil {
		return "", err
	}
	return request.teamID, nil
}
