package tenancy

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/configstate"
)

const dummyPasswordHash = "$argon2id$v=19$m=65536,t=1,p=4$WyWRpzFnab6vlgFLECjL9g$ZJhWU5lKWEo1T7NhxBxQf70Gk+We+sUQ8SO52b8nnxQ"

type Session struct {
	AuthSource string `json:"-"`
	Actor      Actor  `json:"-"`
	User       User   `json:"user"`
	Token      string `json:"-"`
	CSRFToken  string `json:"csrfToken"`
	ExpiresAt  int64  `json:"expiresAt"`
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
func (a *Accounts) Login(ctx context.Context, username, password string) (Session, error) {
	select {
	case a.loginSlots <- struct{}{}:
		defer func() { <-a.loginSlots }()
	case <-ctx.Done():
		return Session{}, ctx.Err()
	}
	unknownKey := sha256.Sum256([]byte(strings.ToLower(username)))
	guardKey := "unknown:" + hex.EncodeToString(unknownKey[:])
	var user User
	var stored sql.NullString
	var expires, consumed sql.NullInt64
	var enabled, mustChange, admin int
	err := a.db.QueryRowContext(ctx, `SELECT id,username,display_name,password_hash,enabled,instance_admin,must_change_password,password_expires_at,password_consumed_at,security_generation FROM access_users WHERE username_key=?`, strings.ToLower(username)).Scan(&user.ID, &user.Username, &user.DisplayName, &stored, &enabled, &admin, &mustChange, &expires, &consumed, &user.SecurityGeneration)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Session{}, err
	}
	found := err == nil
	if found {
		guardKey = "user:" + user.ID
	}
	hash := dummyPasswordHash
	if found && stored.Valid {
		hash = stored.String
	}
	guards := &a.lockouts
	if !found {
		guards = &a.unknownLockouts
	}
	manager, release, err := guards.acquire(guardKey)
	if err != nil {
		return Session{}, err
	}
	defer release()
	ok, locked, remaining, err := manager.Guard(func() (bool, error) {
		match, e := auth.VerifyPassword(password, hash)
		if e != nil {
			return false, e
		}
		valid := found && stored.Valid && enabled == 1
		if mustChange == 1 {
			valid = valid && expires.Valid && expires.Int64 > a.now().Unix() && !consumed.Valid
		}
		return match && valid, nil
	})
	if locked {
		return Session{}, &auth.ErrLockedOut{RetryAfter: remaining}
	}
	if err != nil {
		return Session{}, err
	}
	if !ok {
		return Session{}, auth.ErrInvalidCredentials
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, err
	}
	defer tx.Rollback()
	// Lock without advancing policy revision on ordinary sign-in.
	if _, err = tx.ExecContext(ctx, `UPDATE selfupdate_control SET task_id=task_id WHERE id=1`); err != nil {
		return Session{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE access_control SET mode_epoch=mode_epoch WHERE id=1`); err != nil {
		return Session{}, err
	}
	var mode, catalog int
	var epoch uint64
	if err = tx.QueryRowContext(ctx, `SELECT enabled,mode_epoch,catalog_version FROM access_control WHERE id=1`).Scan(&mode, &epoch, &catalog); err != nil || catalog != authz.CatalogVersion || mode != 1 {
		return Session{}, auth.ErrInvalidCredentials
	}
	var currentHash sql.NullString
	var generation uint64
	if err = tx.QueryRowContext(ctx, `SELECT password_hash,enabled,must_change_password,password_expires_at,password_consumed_at,security_generation FROM access_users WHERE id=?`, user.ID).Scan(&currentHash, &enabled, &mustChange, &expires, &consumed, &generation); err != nil || enabled != 1 || !currentHash.Valid || generation != user.SecurityGeneration || subtle.ConstantTimeCompare([]byte(currentHash.String), []byte(hash)) != 1 {
		return Session{}, auth.ErrInvalidCredentials
	}
	if mustChange == 1 {
		if err = configstate.CheckMaintenanceAdmission(ctx, tx); err != nil {
			return Session{}, err
		}
		if !expires.Valid || expires.Int64 <= a.now().Unix() || consumed.Valid {
			return Session{}, auth.ErrInvalidCredentials
		}
		if _, err = tx.ExecContext(ctx, `UPDATE access_users SET password_consumed_at=? WHERE id=?`, a.now().Unix(), user.ID); err != nil {
			return Session{}, err
		}
	}
	user.Enabled = true
	user.InstanceAdmin = admin == 1
	user.MustChangePassword = mustChange == 1
	session, err := a.issueSession(ctx, tx, user, epoch)
	if err != nil {
		return Session{}, err
	}
	if err = tx.Commit(); err != nil {
		return Session{}, err
	}
	return session, nil
}
func (a *Accounts) issueSession(ctx context.Context, tx *sql.Tx, user User, epoch uint64) (Session, error) {
	return a.issueSourceSession(ctx, tx, user, epoch, "password")
}
func (a *Accounts) issueSourceSession(ctx context.Context, tx *sql.Tx, user User, epoch uint64, source string) (Session, error) {
	token, err := temporaryPassword()
	if err != nil {
		return Session{}, err
	}
	csrf, err := temporaryPassword()
	if err != nil {
		return Session{}, err
	}
	now := a.now()
	expires := now.Add(24 * time.Hour).Unix()
	if user.MustChangePassword {
		expires = now.Add(5 * time.Minute).Unix()
	}
	hash := tokenHash(token)
	var verified any
	if source == "password" {
		verified = now.Unix()
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO access_sessions(token_hash,user_id,csrf_token,mode_epoch,security_generation,auth_source,created_at,expires_at,last_seen_at,password_verified_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, hash, user.ID, csrf, epoch, user.SecurityGeneration, source, now.Unix(), expires, now.Unix(), verified)
	if err != nil {
		return Session{}, err
	}
	return Session{AuthSource: source, Actor: Actor{UserID: user.ID, SessionHash: hash, ModeEpoch: epoch, SecurityGeneration: user.SecurityGeneration}, User: user, Token: token, CSRFToken: csrf, ExpiresAt: expires}, nil
}
func (a *Accounts) Verify(ctx context.Context, token string) (Session, error) {
	if token == "" {
		return Session{}, auth.ErrSessionNotFound
	}
	hash := tokenHash(token)
	var session Session
	var enabled, admin, mustChange, mode, catalog int
	var storedEpoch, actualEpoch, sessionGeneration uint64
	err := a.db.QueryRowContext(ctx, `SELECT u.id,u.username,u.display_name,u.enabled,u.instance_admin,u.must_change_password,u.security_generation,s.auth_source,s.csrf_token,s.expires_at,s.mode_epoch,s.security_generation,c.enabled,c.mode_epoch,c.catalog_version FROM access_sessions s JOIN access_users u ON u.id=s.user_id JOIN access_control c ON c.id=1 WHERE s.token_hash=?`, hash).Scan(&session.User.ID, &session.User.Username, &session.User.DisplayName, &enabled, &admin, &mustChange, &session.User.SecurityGeneration, &session.AuthSource, &session.CSRFToken, &session.ExpiresAt, &storedEpoch, &sessionGeneration, &mode, &actualEpoch, &catalog)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Session{}, auth.ErrSessionNotFound
		}
		return Session{}, err
	}
	if catalog != authz.CatalogVersion || mode != 1 || enabled != 1 || sessionGeneration != session.User.SecurityGeneration || storedEpoch != actualEpoch || session.ExpiresAt <= a.now().Unix() {
		return Session{}, auth.ErrSessionNotFound
	}
	session.User.Enabled = true
	session.User.InstanceAdmin = admin == 1
	session.User.MustChangePassword = mustChange == 1
	session.Token = token
	session.Actor = Actor{UserID: session.User.ID, SessionHash: hash, ModeEpoch: actualEpoch, SecurityGeneration: sessionGeneration}
	return session, nil
}

// RestrictedAllowed is an explicit allow-list. The caller must apply it before
// every authenticated route, tool, team selection and OAuth/MCP capability flow.
func RestrictedAllowed(s Session, operation string) bool {
	if s.User.ID == "" || !s.User.Enabled || s.Token == "" {
		return false
	}
	if !s.User.MustChangePassword {
		return true
	}
	return operation == "self.password" || operation == "self.identity" || operation == "self.logout"
}
func (a *Accounts) Logout(ctx context.Context, token string) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE selfupdate_control SET task_id=task_id WHERE id=1`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE access_control SET authz_revision=authz_revision+1 WHERE id=1`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM access_sessions WHERE token_hash=?`, tokenHash(token)); err != nil {
		return err
	}
	return tx.Commit()
}
func (a *Accounts) ChangePassword(ctx context.Context, token, current, next string) (Session, error) {
	if len(next) < 8 || len(next) > 1024 || strings.TrimSpace(next) == "" || next == current {
		return Session{}, auth.ErrWeakPassword
	}
	session, err := a.Verify(ctx, token)
	if err != nil {
		return Session{}, err
	}
	select {
	case a.loginSlots <- struct{}{}:
		defer func() { <-a.loginSlots }()
	case <-ctx.Done():
		return Session{}, ctx.Err()
	}
	var stored sql.NullString
	if err = a.db.QueryRowContext(ctx, `SELECT password_hash FROM access_users WHERE id=?`, session.User.ID).Scan(&stored); err != nil || !stored.Valid {
		return Session{}, auth.ErrInvalidCurrentPassword
	}
	manager, release, err := a.lockouts.acquire("user:" + session.User.ID)
	if err != nil {
		return Session{}, err
	}
	defer release()
	ok, locked, remaining, err := manager.Guard(func() (bool, error) { return auth.VerifyPassword(current, stored.String) })
	if locked {
		return Session{}, &auth.ErrLockedOut{RetryAfter: remaining}
	}
	if err != nil {
		return Session{}, err
	}
	if !ok {
		return Session{}, auth.ErrInvalidCurrentPassword
	}
	hash, err := auth.HashPassword(next)
	if err != nil {
		return Session{}, err
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, err
	}
	defer tx.Rollback()
	if err = lockControl(ctx, tx); err != nil {
		return Session{}, err
	}
	var currentHash sql.NullString
	var generation, epoch uint64
	var enabled, mode int
	var expires int64
	if err = tx.QueryRowContext(ctx, `SELECT u.password_hash,u.security_generation,u.enabled,c.mode_epoch,c.enabled,s.expires_at FROM access_users u JOIN access_sessions s ON s.user_id=u.id JOIN access_control c ON c.id=1 WHERE u.id=? AND s.token_hash=? AND s.security_generation=u.security_generation AND s.mode_epoch=c.mode_epoch`, session.User.ID, session.Actor.SessionHash).Scan(&currentHash, &generation, &enabled, &epoch, &mode, &expires); err != nil || !currentHash.Valid || subtle.ConstantTimeCompare([]byte(currentHash.String), []byte(stored.String)) != 1 || generation != session.Actor.SecurityGeneration || epoch != session.Actor.ModeEpoch || mode != 1 || enabled != 1 || expires <= a.now().Unix() {
		return Session{}, auth.ErrSessionNotFound
	}
	if _, err = tx.ExecContext(ctx, `UPDATE access_users SET password_hash=?,must_change_password=0,password_expires_at=NULL,password_consumed_at=NULL,security_generation=security_generation+1,updated_at=? WHERE id=?`, hash, a.now().Unix(), session.User.ID); err != nil {
		return Session{}, err
	}
	var bootstrap sql.NullString
	if err = tx.QueryRowContext(ctx, `SELECT bootstrap_user_id FROM access_control WHERE id=1`).Scan(&bootstrap); err != nil {
		return Session{}, err
	}
	if bootstrap.Valid && bootstrap.String == session.User.ID {
		result, e := tx.ExecContext(ctx, `UPDATE admin_user SET password_hash=?,security_generation=security_generation+1,updated_at=? WHERE id=1`, hash, a.now().UTC().Format(time.RFC3339))
		if e != nil {
			return Session{}, e
		}
		n, e := result.RowsAffected()
		if e != nil || n != 1 {
			return Session{}, ErrConflict
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM sessions`); err != nil {
			return Session{}, err
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM access_sessions WHERE user_id=?`, session.User.ID); err != nil {
		return Session{}, err
	}
	session.User.SecurityGeneration++
	session.User.MustChangePassword = false
	rotated, err := a.issueSession(ctx, tx, session.User, epoch)
	if err != nil {
		return Session{}, err
	}
	if err = tx.Commit(); err != nil {
		return Session{}, err
	}
	return rotated, nil
}
