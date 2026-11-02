package auth

import (
	"context"
	"database/sql"
	"time"
)

const RecentPasswordWindow = 5 * time.Minute

type sessionLease struct {
	token, source string
	cancel        context.CancelFunc
}

// Lease ties an authenticated channel to revocation, with a 20 second DB fallback.
func (s *Service) Lease(parent context.Context, sess *Session) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	s.leaseMu.Lock()
	s.leaseSeq++
	id := s.leaseSeq
	s.leases[id] = sessionLease{sess.Token, sess.AuthSource, cancel}
	s.leaseMu.Unlock()
	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, e := s.Verify(sess.Token); e != nil {
					cancel()
					return
				}
			}
		}
	}()
	release := func() { cancel(); s.leaseMu.Lock(); delete(s.leases, id); s.leaseMu.Unlock() }
	return ctx, release
}
func (s *Service) NotifyRevokedSource(source string) {
	s.leaseMu.Lock()
	defer s.leaseMu.Unlock()
	for _, l := range s.leases {
		if l.source == source {
			l.cancel()
		}
	}
}
func (s *Service) NotifyRevokedToken(token string) {
	s.leaseMu.Lock()
	defer s.leaseMu.Unlock()
	for _, l := range s.leases {
		if l.token == token {
			l.cancel()
		}
	}
}
func (s *Service) notifyID(id string) {
	s.leaseMu.Lock()
	defer s.leaseMu.Unlock()
	for _, l := range s.leases {
		if SessionID(l.token) == id {
			l.cancel()
		}
	}
}
func (s *Service) notifyOthers(keep string) {
	s.leaseMu.Lock()
	defer s.leaseMu.Unlock()
	for _, l := range s.leases {
		if l.token != keep {
			l.cancel()
		}
	}
}
func (s *Service) verifyPasswordLimited(password, hash string) (bool, error) {
	ok, locked, remaining, err := s.lockout.Guard(func() (bool, error) { return VerifyPassword(password, hash) })
	if locked {
		return false, &ErrLockedOut{RetryAfter: remaining}
	}
	return ok, err
}

// Reauthenticate updates only the current session; it never creates another one.
func (s *Service) Reauthenticate(token, password string) (time.Time, error) {
	var hash string
	var generation int64
	if err := s.db.QueryRow(`SELECT password_hash,security_generation FROM admin_user WHERE id=1`).Scan(&hash, &generation); err != nil {
		return time.Time{}, ErrInvalidCurrentPassword
	}
	ok, err := s.verifyPasswordLimited(password, hash)
	if err != nil {
		return time.Time{}, err
	}
	if !ok {
		return time.Time{}, ErrInvalidCurrentPassword
	}
	now := s.clock.Now().UTC()
	result, err := s.db.Exec(`UPDATE sessions SET password_verified_at=? WHERE token=? AND EXISTS(SELECT 1 FROM admin_user WHERE id=1 AND security_generation=?)`, now.Format(time.RFC3339), token, generation)
	if err != nil {
		return time.Time{}, err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return time.Time{}, ErrSessionNotFound
	}
	return now.Add(RecentPasswordWindow), nil
}
func RecentPassword(db *sql.DB, token string, now time.Time) bool {
	var raw sql.NullString
	if db.QueryRow(`SELECT password_verified_at FROM sessions WHERE token=? AND expires_at>?`, token, now.UTC().Format(time.RFC3339)).Scan(&raw) != nil || !raw.Valid {
		return false
	}
	t, err := time.Parse(time.RFC3339, raw.String)
	return err == nil && !t.After(now) && now.Before(t.Add(RecentPasswordWindow))
}
