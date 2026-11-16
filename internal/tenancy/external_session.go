package tenancy

import (
	"context"
	"database/sql"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"time"
)

// VerifyAccountSession is shared by the platform-login service's fenced
// transaction. Token and actor claims always come from the actual session row.
func VerifyAccountSession(ctx context.Context, q configstate.DBTX, token string, administrator bool) (Session, error) {
	var s Session
	if token == "" {
		return s, auth.ErrSessionNotFound
	}
	s.Token = token
	s.Actor.SessionHash = tokenHash(token)
	err := q.QueryRowContext(ctx, `SELECT user_id,mode_epoch,security_generation,auth_source,csrf_token,expires_at FROM access_sessions WHERE token_hash=?`, s.Actor.SessionHash).Scan(&s.Actor.UserID, &s.Actor.ModeEpoch, &s.Actor.SecurityGeneration, &s.AuthSource, &s.CSRFToken, &s.ExpiresAt)
	if err != nil {
		return Session{}, auth.ErrSessionNotFound
	}
	s.User, err = verifyActor(ctx, q, s.Actor, administrator)
	if err != nil {
		return Session{}, ErrForbidden
	}
	return s, nil
}

// IssueExternalSession is called only after the adapter has verified an OAuth
// subject and the caller has locked access_control and consumed the exact flow.
// It never provisions users, matches email, or treats a label as identity.
func (a *Accounts) IssueExternalSession(ctx context.Context, tx *sql.Tx, provider, issuer, subject string, epoch uint64, providerGeneration int64) (Session, error) {
	var mode, catalog, enabled, admin, forced, providerEnabled int
	var currentEpoch uint64
	if err := tx.QueryRowContext(ctx, `SELECT enabled,mode_epoch,catalog_version FROM access_control WHERE id=1`).Scan(&mode, &currentEpoch, &catalog); err != nil || mode != 1 || currentEpoch != epoch || catalog != authz.CatalogVersion {
		return Session{}, ErrForbidden
	}
	var generation int64
	if err := tx.QueryRowContext(ctx, `SELECT enabled,auth_generation FROM login_provider_settings WHERE provider=?`, provider).Scan(&providerEnabled, &generation); err != nil || providerEnabled != 1 || generation != providerGeneration {
		return Session{}, ErrForbidden
	}
	var user User
	err := tx.QueryRowContext(ctx, `SELECT u.id,u.username,u.display_name,u.enabled,u.instance_admin,u.must_change_password,u.security_generation FROM access_identities i JOIN access_users u ON u.id=i.user_id WHERE i.identity_key=? AND i.provider=? AND i.issuer=? AND i.subject=?`, externalIdentityKey(provider, issuer, subject), provider, issuer, subject).Scan(&user.ID, &user.Username, &user.DisplayName, &enabled, &admin, &forced, &user.SecurityGeneration)
	if err != nil || enabled != 1 || forced != 0 {
		return Session{}, ErrForbidden
	}
	user.Enabled = true
	user.InstanceAdmin = admin == 1
	return a.issueSourceSession(ctx, tx, user, epoch, provider)
}

func RecentAccountPassword(ctx context.Context, q configstate.DBTX, s Session) bool {
	var at sql.NullInt64
	if q.QueryRowContext(ctx, `SELECT password_verified_at FROM access_sessions WHERE token_hash=?`, s.Actor.SessionHash).Scan(&at) != nil || !at.Valid {
		return false
	}
	now := time.Now()
	return at.Int64 <= now.Unix() && now.Before(time.Unix(at.Int64, 0).Add(auth.RecentPasswordWindow))
}
