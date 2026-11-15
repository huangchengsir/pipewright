package tenancy

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"time"
)

var ErrRecentPassword = errors.New("tenancy: recent password verification required")

// CheckRecentPassword is a step-up check, not an authorization grant. Domain
// callbacks must still check their exact action/resource inside the same fence.
func CheckRecentPassword(ctx context.Context, db *sql.DB, token string, now time.Time) error {
	q := configstate.Queryer(ctx, db)
	request, ok := ctx.Value(actorContextKey{}).(requestActor)
	if !ok || token == "" || subtle.ConstantTimeCompare([]byte(request.actor.SessionHash), []byte(tokenHash(token))) != 1 {
		return ErrForbidden
	}
	if _, err := verifyActor(ctx, q, request.actor, false); err != nil {
		return err
	}
	var verified sql.NullInt64
	if err := q.QueryRowContext(ctx, `SELECT password_verified_at FROM access_sessions WHERE token_hash=?`, request.actor.SessionHash).Scan(&verified); err != nil {
		return ErrForbidden
	}
	if !verified.Valid || verified.Int64 > now.Unix() || now.Sub(time.Unix(verified.Int64, 0)) > auth.RecentPasswordWindow {
		return ErrRecentPassword
	}
	return nil
}
