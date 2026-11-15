package tenancy

import (
	"context"
	"database/sql"
	"encoding/hex"
	"github.com/huangchengsir/pipewright/internal/configstate"
)

// ValidateStoredSession authenticates a server-stored approval subject, not an
// HTTP-supplied user ID. Callers must separately authorize its exact operation.
func ValidateStoredSession(ctx context.Context, q configstate.DBTX, hash string) (Actor, error) {
	if len(hash) != 64 {
		return Actor{}, ErrForbidden
	}
	if _, err := hex.DecodeString(hash); err != nil {
		return Actor{}, ErrForbidden
	}
	var actor Actor
	actor.SessionHash = hash
	err := q.QueryRowContext(ctx, `SELECT user_id,mode_epoch,security_generation FROM access_sessions WHERE token_hash=?`, hash).Scan(&actor.UserID, &actor.ModeEpoch, &actor.SecurityGeneration)
	if err != nil {
		return Actor{}, ErrForbidden
	}
	_, err = verifyActor(ctx, q, actor, false)
	return actor, err
}

// ApprovalSubjectContext resolves the team's identity from the persisted project,
// and is used only after the browser subject or persisted approval was verified.
func ApprovalSubjectContext(ctx context.Context, db *sql.DB, hash, project string) (context.Context, error) {
	q := configstate.Queryer(ctx, db)
	actor, err := ValidateStoredSession(ctx, q, hash)
	if err != nil {
		return ctx, err
	}
	var team string
	if err = q.QueryRowContext(ctx, `SELECT tenant_id FROM access_ownership WHERE resource_kind='project' AND resource_id=?`, project).Scan(&team); err != nil {
		return ctx, ErrForbidden
	}
	// A bearer principal must not shadow the independently authenticated approver.
	ctx = context.WithValue(ctx, mcpContextKey{}, nil)
	return WithActor(ctx, actor, team), nil
}
