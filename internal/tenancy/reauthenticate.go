package tenancy

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"github.com/huangchengsir/pipewright/internal/auth"
	"time"
)

// Reauthenticate proves the current employee's own password. It never creates
// a password for an SSO-only account or changes the session's provider identity.
func (a *Accounts) Reauthenticate(ctx context.Context, token, password string) (time.Time, error) {
	session, err := a.Verify(ctx, token)
	if err != nil {
		return time.Time{}, err
	}
	if session.User.MustChangePassword {
		return time.Time{}, ErrForbidden
	}
	var stored sql.NullString
	if err = a.db.QueryRowContext(ctx, `SELECT password_hash FROM access_users WHERE id=?`, session.User.ID).Scan(&stored); err != nil {
		return time.Time{}, err
	}
	if !stored.Valid || stored.String == "" {
		return time.Time{}, auth.ErrInvalidCurrentPassword
	}
	manager, release, err := a.lockouts.acquire("user:" + session.User.ID)
	if err != nil {
		return time.Time{}, err
	}
	defer release()
	select {
	case a.loginSlots <- struct{}{}:
		defer func() { <-a.loginSlots }()
	case <-ctx.Done():
		return time.Time{}, ctx.Err()
	}
	ok, locked, remaining, err := manager.Guard(func() (bool, error) { return auth.VerifyPassword(password, stored.String) })
	if locked {
		return time.Time{}, &auth.ErrLockedOut{RetryAfter: remaining}
	}
	if err != nil {
		return time.Time{}, err
	}
	if !ok {
		return time.Time{}, auth.ErrInvalidCurrentPassword
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return time.Time{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE selfupdate_control SET task_id=task_id WHERE id=1`); err != nil {
		return time.Time{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE access_control SET mode_epoch=mode_epoch WHERE id=1`); err != nil {
		return time.Time{}, err
	}
	if _, err = verifyActor(ctx, tx, session.Actor, false); err != nil {
		return time.Time{}, err
	}
	var current string
	if err = tx.QueryRowContext(ctx, `SELECT password_hash FROM access_users WHERE id=?`, session.User.ID).Scan(&current); err != nil || subtle.ConstantTimeCompare([]byte(current), []byte(stored.String)) != 1 {
		return time.Time{}, auth.ErrInvalidCurrentPassword
	}
	now := a.now()
	if _, err = tx.ExecContext(ctx, `UPDATE access_sessions SET password_verified_at=? WHERE token_hash=?`, now.Unix(), session.Actor.SessionHash); err != nil {
		return time.Time{}, err
	}
	if err = tx.Commit(); err != nil {
		return time.Time{}, err
	}
	return now.Add(auth.RecentPasswordWindow), nil
}
func (a *Authenticator) Reauthenticate(token, password string) (time.Time, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := a.mode(ctx); err != nil {
		return time.Time{}, err
	}
	return a.accounts.Reauthenticate(ctx, token, password)
}
