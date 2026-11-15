package tenancy

import (
	"context"
	"database/sql"
	"github.com/huangchengsir/pipewright/internal/auth"
	"time"
)

// ProcessMode pins both enabled and legacy processes. A stale off-mode process
// cannot authenticate as a legacy administrator after another mode transition.
type ProcessMode struct {
	db    *sql.DB
	state ModeState
}

func PinProcessMode(db *sql.DB, state ModeState) *ProcessMode {
	return &ProcessMode{db: db, state: state}
}
func (p *ProcessMode) Check(ctx context.Context) error {
	if p == nil || p.db == nil || p.state.Epoch == 0 {
		return ErrForbidden
	}
	state, e := NewRepository(p.db).Mode(ctx)
	if e != nil {
		return e
	}
	if state.Enabled != p.state.Enabled || state.Epoch != p.state.Epoch {
		return ErrForbidden
	}
	return nil
}

type LegacyAuthenticator struct {
	legacy *auth.Service
	mode   *ProcessMode
}

func NewLegacyAuthenticator(legacy *auth.Service, mode *ProcessMode) *LegacyAuthenticator {
	return &LegacyAuthenticator{legacy, mode}
}
func (a *LegacyAuthenticator) Login(username, password string) (*auth.Session, error) {
	return a.LoginContext(context.Background(), username, password)
}
func (a *LegacyAuthenticator) LoginContext(ctx context.Context, username, password string) (*auth.Session, error) {
	if e := a.mode.Check(ctx); e != nil {
		return nil, e
	}
	s, e := a.legacy.Login(username, password)
	if e != nil {
		return nil, e
	}
	if e = a.mode.Check(ctx); e != nil {
		_ = a.legacy.Logout(s.Token)
		return nil, e
	}
	return s, nil
}
func (a *LegacyAuthenticator) Verify(token string) (*auth.Session, error) {
	if e := a.mode.Check(context.Background()); e != nil {
		return nil, auth.ErrSessionNotFound
	}
	s, e := a.legacy.Verify(token)
	if e != nil {
		return nil, e
	}
	if e = a.mode.Check(context.Background()); e != nil {
		return nil, auth.ErrSessionNotFound
	}
	return s, nil
}
func (a *LegacyAuthenticator) Logout(token string) error { return a.legacy.Logout(token) }
func (a *LegacyAuthenticator) AdminUsername() (string, error) {
	if e := a.mode.Check(context.Background()); e != nil {
		return "", e
	}
	return a.legacy.AdminUsername()
}
func (a *LegacyAuthenticator) Lease(parent context.Context, s *auth.Session) (context.Context, context.CancelFunc) {
	ctx, oldCancel := a.legacy.Lease(parent, s)
	ctx, cancel := context.WithCancel(ctx)
	stop := func() { cancel(); oldCancel() }
	if e := a.mode.Check(ctx); e != nil {
		stop()
		return ctx, stop
	}
	go func() {
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		defer stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if e := a.mode.Check(ctx); e != nil {
					return
				}
			}
		}
	}()
	return ctx, stop
}
