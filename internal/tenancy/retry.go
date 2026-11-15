package tenancy

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/configstate"
)

// RetryCaller derives stable request ownership from authenticated server context.
// An HTTP or MCP body cannot choose another employee's idempotency namespace.
func RetryCaller(ctx context.Context, db *sql.DB, legacyScope, legacyName string) (string, string, error) {
	on, err := modeEnabled(ctx, configstate.Queryer(ctx, db))
	if err != nil || !on {
		return legacyScope, legacyName, err
	}
	_, request, err := authorityForRequest(ctx, db)
	if err != nil || request.serviceRunID != "" {
		return "", "", ErrForbidden
	}
	if _, ok := ctx.Value(observationKey{}).(*observationPrincipal); ok {
		return "", "", ErrForbidden
	}
	identity := request.teamID + ":" + request.actor.UserID
	if cap, ok := ctx.Value(mcpContextKey{}).(mcpContext); ok {
		identity += ":" + cap.tokenID
	}
	sum := sha256.Sum256([]byte(identity))
	return "principal:" + hex.EncodeToString(sum[:]), request.actor.UserID, nil
}

// ApproveRetryRun re-authorizes a sealed original plan as the current caller.
// Original user privileges and browser cookies are never inherited by a child.
// The original plan ceiling is conservative until node-specific resource
// projections are available; every retained permission is checked afresh.
func ApproveRetryRun(ctx context.Context, db *sql.DB, source, child, project, digest string) error {
	q := configstate.Queryer(ctx, db)
	on, err := modeEnabled(ctx, q)
	if err != nil || !on {
		return err
	}
	if q == db {
		return ErrConflict
	}
	if err = Check(ctx, db, "run.retry", "run", source); err != nil {
		return err
	}
	var storedProject, storedDigest, body string
	var revision int64
	var epoch uint64
	var catalog int
	if err = q.QueryRowContext(ctx, `SELECT project_id,plan_digest,permissions_json,config_revision,mode_epoch,catalog_version FROM access_run_authorizations WHERE run_id=? AND revoked_at IS NULL`, source).Scan(&storedProject, &storedDigest, &body, &revision, &epoch, &catalog); err != nil {
		return ErrForbidden
	}
	_, request, err := authorityForRequest(ctx, db)
	if err != nil {
		return err
	}
	actualRevision, err := configstate.ReadRevision(ctx, q, project)
	if err != nil || storedProject != project || storedDigest != digest || revision != actualRevision || epoch != request.actor.ModeEpoch || catalog != authz.CatalogVersion {
		return ErrConflict
	}
	if cap, ok := ctx.Value(mcpContextKey{}).(mcpContext); ok {
		_, _, token, e := mcpAuthority(ctx, db, cap)
		if e != nil || !token.Allows(project, true) {
			return ErrForbidden
		}
		if e = Check(ctx, db, "mcp.issue", "project", project); e != nil {
			return e
		}
	}
	envelope, decodeErr := decodeRunPermissions(body)
	if decodeErr != nil {
		return ErrForbidden
	}
	permissions := []ExecutionPermission{{Action: "run.retry", ResourceID: child}}
	for _, permission := range envelope.Permissions {
		action, known := authz.Lookup(permission.Action)
		if !known {
			return ErrForbidden
		}
		if action.ResourceKind != "run" {
			permissions = append(permissions, permission)
		}
	}
	if err = RecordOwnership(ctx, db, "run", child, project); err != nil {
		return err
	}
	return ApproveRun(ctx, db, child, project, digest, permissions)
}

func RunAdmissionAction(ctx context.Context, db *sql.DB, runID string) (string, error) {
	_, cap, err := loadRunAuthority(ctx, configstate.Queryer(ctx, db), runID)
	if err != nil {
		return "", err
	}
	for _, permission := range cap.permissions {
		if permission.ResourceID == runID && permission.EnvironmentID == "" && (permission.Action == "run.start" || permission.Action == "run.retry") {
			return permission.Action, nil
		}
	}
	return "", ErrForbidden
}
