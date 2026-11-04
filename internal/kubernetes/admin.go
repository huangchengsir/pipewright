package kubernetes

import (
	"context"
	"database/sql"
	"errors"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/store"
	"strings"
	"time"
)

var ErrSession = errors.New("kubernetes_session_invalid")
var ErrRecentPassword = errors.New("kubernetes_recent_password_required")

// WithAdminMutation serializes session revocation, administrator security changes
// and configuration mutation. The callback MUST perform database work only, using
// its supplied context. Empty tokens never bypass the boundary.
func (s *Service) WithAdminMutation(ctx context.Context, token string, fn func(context.Context) error) error {
	if strings.TrimSpace(token) == "" || fn == nil {
		return ErrSession
	}
	return configstate.WithWriteTxContext(ctx, s.db, func(ctx context.Context, q configstate.DBTX) error {
		if _, e := q.ExecContext(ctx, `UPDATE admin_user SET security_generation=security_generation WHERE id=1`); e != nil {
			return e
		}
		var admin int
		if e := q.QueryRowContext(ctx, `SELECT id FROM admin_user WHERE id=1`).Scan(&admin); e != nil {
			return ErrSession
		}
		query := `SELECT expires_at,absolute_expires_at,password_verified_at,auth_source,provider_generation,identity_generation FROM sessions WHERE token=?`
		if store.DialectOf(s.db) == store.MySQL {
			query += " FOR UPDATE"
		}
		var expiry, source string
		var absolute, verified sql.NullString
		var pg, ig int64
		if e := q.QueryRowContext(ctx, query, token).Scan(&expiry, &absolute, &verified, &source, &pg, &ig); e != nil {
			if errors.Is(e, sql.ErrNoRows) {
				return ErrSession
			}
			return e
		}
		now := s.now().UTC()
		alive := func(value string) bool { v, e := time.Parse(time.RFC3339, value); return e == nil && now.Before(v) }
		if !alive(expiry) || (absolute.Valid && !alive(absolute.String)) {
			return ErrSession
		}
		if source != "password" {
			var enabled bool
			var currentPG, currentIG int64
			if e := q.QueryRowContext(ctx, `SELECT p.enabled,p.auth_generation,i.identity_generation FROM login_provider_settings p JOIN login_identities i ON i.provider=p.provider WHERE p.provider=?`, source).Scan(&enabled, &currentPG, &currentIG); e != nil || !enabled || pg != currentPG || ig != currentIG {
				return ErrSession
			}
		}
		v, e := time.Parse(time.RFC3339, verified.String)
		if !verified.Valid || e != nil || v.After(now) || now.Sub(v) > auth.RecentPasswordWindow {
			return ErrRecentPassword
		}
		if e = ctx.Err(); e != nil {
			return e
		}
		return fn(ctx)
	})
}
