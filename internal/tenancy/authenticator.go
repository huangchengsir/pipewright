package tenancy

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
)

// Authenticator is installed only in enabled mode. It pins this process to the
// mode epoch it booted with; another controller changing modes cannot turn this
// instance into an implicit administrator-only bypass.
type Authenticator struct {
	accounts *Accounts
	epoch    uint64
}

func NewAuthenticator(accounts *Accounts, epoch uint64) *Authenticator {
	return &Authenticator{accounts: accounts, epoch: epoch}
}
func (a *Authenticator) mode(ctx context.Context) error {
	if a == nil || a.accounts == nil || a.epoch == 0 {
		return auth.ErrSessionNotFound
	}
	var enabled, catalog int
	var epoch uint64
	if err := a.accounts.db.QueryRowContext(ctx, `SELECT enabled,mode_epoch,catalog_version FROM access_control WHERE id=1`).Scan(&enabled, &epoch, &catalog); err != nil {
		return err
	}
	if catalog != authz.CatalogVersion || enabled != 1 || epoch != a.epoch {
		return auth.ErrSessionNotFound
	}
	return nil
}
func (s Session) AuthSession() *auth.Session { return authSession(s) }
func authSession(s Session) *auth.Session {
	return &auth.Session{Token: s.Token, CSRFToken: s.CSRFToken, ExpiresAt: time.Unix(s.ExpiresAt, 0), AuthSource: s.AuthSource, UserID: s.User.ID, Username: s.User.Username, DisplayName: s.User.DisplayName, MultiTenant: true, InstanceAdmin: s.User.InstanceAdmin, MustChangePassword: s.User.MustChangePassword, ModeEpoch: s.Actor.ModeEpoch, SecurityGeneration: s.Actor.SecurityGeneration}
}
func (a *Authenticator) Login(username, password string) (*auth.Session, error) {
	return a.LoginContext(context.Background(), username, password)
}
func (a *Authenticator) LoginContext(ctx context.Context, username, password string) (*auth.Session, error) {
	if err := a.mode(ctx); err != nil {
		return nil, err
	}
	s, err := a.accounts.Login(ctx, username, password)
	if err != nil {
		return nil, err
	}
	if s.Actor.ModeEpoch != a.epoch {
		return nil, auth.ErrSessionNotFound
	}
	return authSession(s), nil
}
func (a *Authenticator) Verify(token string) (*auth.Session, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.mode(ctx); err != nil {
		return nil, err
	}
	s, err := a.accounts.Verify(ctx, token)
	if errors.Is(err, ErrForbidden) || errors.Is(err, sql.ErrNoRows) {
		return nil, auth.ErrSessionNotFound
	}
	if err != nil {
		return nil, err
	}
	if s.Actor.ModeEpoch != a.epoch {
		return nil, auth.ErrSessionNotFound
	}
	return authSession(s), nil
}
func (a *Authenticator) Logout(token string) error {
	return a.accounts.Logout(context.Background(), token)
}
func (a *Authenticator) AdminUsername() (string, error) { return "", ErrForbidden }
func (a *Authenticator) Lease(parent context.Context, s *auth.Session) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	if s == nil || !s.MultiTenant {
		cancel()
		return ctx, cancel
	}
	go func() {
		tick := time.NewTicker(5 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				fresh, err := a.Verify(s.Token)
				if err != nil || fresh.SecurityGeneration != s.SecurityGeneration || fresh.ModeEpoch != s.ModeEpoch {
					cancel()
					return
				}
			}
		}
	}()
	return ctx, cancel
}
func ActorFromSession(s *auth.Session) (Actor, error) {
	if s == nil || !s.MultiTenant || s.UserID == "" || s.Token == "" || s.ModeEpoch == 0 || s.SecurityGeneration == 0 {
		return Actor{}, ErrForbidden
	}
	return Actor{UserID: s.UserID, SessionHash: tokenHash(s.Token), ModeEpoch: s.ModeEpoch, SecurityGeneration: s.SecurityGeneration}, nil
}
