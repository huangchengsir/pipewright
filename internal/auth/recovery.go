package auth

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
)

// VerifyRecoveryPassword authenticates the existing administrator without
// creating, refreshing or revoking database sessions. Dedicated maintenance
// processes must use this entry point instead of Login/Verify. The existing
// per-instance lockout protects the expensive password check.
func (s *Service) VerifyRecoveryPassword(ctx context.Context, username, password string) error {
	var actual, hash string
	err := s.db.QueryRowContext(ctx, "SELECT username,password_hash FROM admin_user WHERE id=1").Scan(&actual, &hash)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ErrInvalidCredentials
	}
	matches := err == nil && subtle.ConstantTimeCompare([]byte(username), []byte(actual)) == 1
	if !matches {
		hash = dummyHash
	}
	ok, locked, remaining, err := s.lockout.Guard(func() (bool, error) {
		valid, e := VerifyPassword(password, hash)
		return valid && matches, e
	})
	if locked {
		return &ErrLockedOut{RetryAfter: remaining}
	}
	if err != nil || !ok {
		return ErrInvalidCredentials
	}
	return nil
}
