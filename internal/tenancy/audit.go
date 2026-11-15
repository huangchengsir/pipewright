package tenancy

import (
	"context"
	"database/sql"
	"errors"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"time"
)

// AppendAuditOwnership records attribution, not an authorization capability.
// It participates in the same transaction as the append-only audit row. It must
// still work after an accepted operation revokes its initiator. Unknown system
// activity remains instance-only instead of guessing a tenant from a target ID.
func AppendAuditOwnership(ctx context.Context, q configstate.DBTX, id, targetKind, targetID string) (string, error) {
	var enabled int
	err := q.QueryRowContext(ctx, `SELECT enabled FROM access_control WHERE id=1`).Scan(&enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if enabled == 0 {
		return "", nil
	}
	if enabled != 1 {
		return "", ErrConflict
	}
	user, team := "", ""
	if cap, ok := ctx.Value(automationKey{}).(*automationPrincipal); ok {
		user, team = cap.user, cap.team
	} else if cap, ok := ctx.Value(mcpContextKey{}).(mcpContext); ok {
		var u, t sql.NullString
		if e := q.QueryRowContext(ctx, `SELECT owner_user_id,owner_team_id FROM mcp_tokens WHERE id=?`, cap.tokenID).Scan(&u, &t); e != nil && !errors.Is(e, sql.ErrNoRows) {
			return "", e
		}
		user, team = u.String, t.String
	} else if cap, ok := ctx.Value(observationKey{}).(*observationPrincipal); ok {
		user, team = cap.user, cap.team
	} else if cap, ok := ctx.Value(executionContextKey{}).(executionContext); ok {
		if e := q.QueryRowContext(ctx, `SELECT user_id,tenant_id FROM access_run_authorizations WHERE run_id=?`, cap.runID).Scan(&user, &team); e != nil && !errors.Is(e, sql.ErrNoRows) {
			return "", e
		}
	} else if request, ok := ctx.Value(actorContextKey{}).(requestActor); ok {
		user, team = request.actor.UserID, request.teamID
	}
	if user == "" || team == "" {
		return "", nil
	}
	var exists int
	if err = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM access_users u CROSS JOIN access_teams t WHERE u.id=? AND t.id=?`, user, team).Scan(&exists); err != nil {
		return "", err
	}
	if exists != 1 {
		return "", nil
	}
	// Retain a resource's original project/environment only when trusted storage
	// confirms the target belongs to this team. Never consume caller Detail fields.
	var project, environment sql.NullString
	// Global account/settings activity stays instance-only even with a selected team.
	switch targetKind {
	case "trigger", "project", "run", "server", "credential", "template", "variable_group", "custom_node", "kubernetes", "environment":
	default:
		return "user:" + user, nil
	}
	if targetKind == "trigger" {
		targetKind = "project"
	}
	err = q.QueryRowContext(ctx, `SELECT project_id,environment_id FROM access_ownership WHERE resource_kind=? AND resource_id=? AND tenant_id=?`, targetKind, targetID, team).Scan(&project, &environment)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if errors.Is(err, sql.ErrNoRows) {
		var foreign int
		if e := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM access_ownership WHERE resource_kind=? AND resource_id=?`, targetKind, targetID).Scan(&foreign); e != nil {
			return "", e
		}
		// Never expose a foreign target through an audit list. A successful project
		// deletion may already have removed its target receipt; its server-supplied
		// ID remains the project identity for this completed-operation record.
		if foreign != 0 || targetKind != "project" {
			return "user:" + user, nil
		}
	}
	if targetKind == "project" {
		project = sql.NullString{String: targetID, Valid: true}
	}
	_, err = q.ExecContext(ctx, `INSERT INTO access_ownership(resource_kind,resource_id,tenant_id,owner_id,project_id,environment_id) VALUES('audit',?,?,?,?,?)`, id, team, user, project, environment)
	return "user:" + user, err
}

// AuditPredicate keeps both rows and cursor resolution within the same scope.
// Legacy/system rows without an ownership receipt are instance-admin-only.
func AuditPredicate(ctx context.Context, db *sql.DB) (authz.Predicate, error) {
	p, err := QueryPredicate(ctx, db, "audit.read", "al.id")
	if err != nil || p.SQL == "1=1" {
		return p, err
	}
	request, ok := ctx.Value(actorContextKey{}).(requestActor)
	if !ok {
		return p, nil
	}
	authority, _, err := authorityForRequest(ctx, db)
	if err != nil {
		return authz.Predicate{}, err
	}
	if !authority.Principal.InstanceAdmin {
		return p, nil
	}
	p.SQL = "(" + p.SQL + ") OR (NOT EXISTS (SELECT 1 FROM access_ownership au WHERE au.resource_kind='audit' AND au.resource_id=al.id) AND EXISTS (SELECT 1 FROM access_control ac WHERE ac.id=1 AND ac.enabled=1 AND ac.mode_epoch=? AND ac.authz_revision=? AND ac.catalog_version=?) AND EXISTS (SELECT 1 FROM access_sessions ss JOIN access_users uu ON uu.id=ss.user_id WHERE ss.token_hash=? AND ss.user_id=? AND ss.mode_epoch=? AND ss.security_generation=? AND ss.expires_at>? AND uu.enabled=1 AND uu.instance_admin=1 AND uu.must_change_password=0 AND uu.security_generation=ss.security_generation))"
	p.Args = append(p.Args, authority.Principal.ModeEpoch, authority.Principal.Revision, authz.CatalogVersion, request.actor.SessionHash, request.actor.UserID, request.actor.ModeEpoch, request.actor.SecurityGeneration, time.Now().Unix())
	return p, nil
}
