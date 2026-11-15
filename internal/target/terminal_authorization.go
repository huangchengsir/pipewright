package target

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"github.com/huangchengsir/pipewright/internal/vault"
	"io"
	"regexp"
	"sync"
	"time"
)

type containerTerminalKey struct{}

func terminalAction(ctx context.Context, id string) string {
	if server, _ := ctx.Value(containerTerminalKey{}).(string); server == id {
		return "container.terminal"
	}
	return "server.terminal"
}

var containerTerminalID = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,255}$`)

func validContainerTerminal(cmd []string) bool {
	if len(cmd) != 5 || cmd[0] != "docker" || cmd[1] != "exec" || cmd[2] != "-it" || !containerTerminalID.MatchString(cmd[3]) {
		return false
	}
	switch cmd[4] {
	case "sh", "bash", "/bin/sh", "/bin/bash", "/bin/ash", "/bin/zsh", "/usr/bin/sh", "/usr/bin/bash", "/usr/bin/zsh":
		return true
	}
	return false
}

// AuthorizeTerminal is shared by every interactive transport and manager access.
// Legacy test adapters have no database; production adapters must implement the
// concrete service boundary whenever an authenticated tenancy context exists.
func AuthorizeTerminal(ctx context.Context, svc Service, id string) error {
	if secured, ok := svc.(interface {
		AuthorizeTerminal(context.Context, string) error
	}); ok {
		return secured.AuthorizeTerminal(ctx, id)
	}
	if tenancy.HasAuthorizationContext(ctx) {
		return tenancy.ErrForbidden
	}
	return nil
}
func (s *service) AuthorizeTerminal(ctx context.Context, id string) error {
	if err := tenancy.VerifyExecution(ctx); err != nil {
		return err
	}
	for _, action := range []string{"server.use", terminalAction(ctx, id)} {
		if err := tenancy.Check(ctx, s.db, action, "server", id); err != nil {
			return err
		}
	}
	server, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if err = s.checkExecutionIdentity(ctx, server); err != nil {
		return err
	}
	return vault.AuthorizeUse(ctx, s.vault, server.CredentialID)
}

type authorizedTerminal struct {
	Session
	ctx      context.Context
	cancel   context.CancelFunc
	check    func(context.Context) error
	once     sync.Once
	closeErr error
}

func (s *service) guardTerminal(ctx context.Context, approved ConnectionSnapshot, session Session) (Session, error) {
	server := &approved.Server
	enabled, err := tenancy.Enabled(ctx, s.db)
	if err != nil {
		_ = session.Close()
		return nil, err
	}
	if !enabled {
		return session, nil
	}
	lifetime, cancel := context.WithCancel(ctx)
	guarded := &authorizedTerminal{Session: session, ctx: lifetime, cancel: cancel}
	guarded.check = func(current context.Context) error {
		if err := s.AuthorizeTerminal(current, server.ID); err != nil {
			return err
		}
		latest, err := s.Capture(current, server.ID)
		if err != nil {
			return err
		}
		if latest.Server.Host != server.Host || latest.Server.Port != server.Port || latest.Server.User != server.User || latest.Server.CredentialID != server.CredentialID || latest.CredentialVersion != approved.CredentialVersion {
			return tenancy.ErrConflict
		}
		return nil
	}
	if err = guarded.validate(); err != nil {
		return nil, err
	}
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-lifetime.Done():
				_ = guarded.Close()
				return
			case <-ticker.C:
				if guarded.validate() != nil {
					return
				}
			}
		}
	}()
	return guarded, nil
}
func (s *authorizedTerminal) validate() error {
	if err := s.ctx.Err(); err != nil {
		_ = s.Close()
		return err
	}
	if err := s.check(s.ctx); err != nil {
		_ = s.Close()
		return err
	}
	return nil
}
func (s *authorizedTerminal) Read(p []byte) (int, error) {
	if err := s.validate(); err != nil {
		return 0, err
	}
	n, err := s.Session.Read(p)
	if denied := s.validate(); denied != nil {
		clear(p[:n])
		return 0, denied
	}
	return n, err
}
func (s *authorizedTerminal) Write(p []byte) (int, error) {
	if err := s.validate(); err != nil {
		return 0, err
	}
	return s.Session.Write(p)
}
func (s *authorizedTerminal) Resize(cols, rows int) error {
	if err := s.validate(); err != nil {
		return err
	}
	return s.Session.Resize(cols, rows)
}

// Closing an already-owned session is fixed cleanup and must remain possible
// after revocation; it does not start a command or switch to a current target.
func (s *authorizedTerminal) Close() error {
	s.once.Do(func() { s.cancel(); s.closeErr = s.Session.Close() })
	return s.closeErr
}

// authorizedStream applies the same current resource checks to read-only SSE
// output. Already buffered remote bytes are discarded after a revocation.
type authorizedStream struct {
	io.ReadCloser
	ctx      context.Context
	cancel   context.CancelFunc
	check    func(context.Context) error
	once     sync.Once
	closeErr error
}

func (s *authorizedStream) validate() error {
	if err := s.ctx.Err(); err != nil {
		_ = s.Close()
		return err
	}
	if err := s.check(s.ctx); err != nil {
		_ = s.Close()
		return err
	}
	return nil
}
func (s *authorizedStream) Read(p []byte) (int, error) {
	if err := s.validate(); err != nil {
		return 0, err
	}
	n, e := s.ReadCloser.Read(p)
	if err := s.validate(); err != nil {
		clear(p[:n])
		return 0, err
	}
	return n, e
}
func (s *authorizedStream) Close() error {
	s.once.Do(func() { s.cancel(); s.closeErr = s.ReadCloser.Close() })
	return s.closeErr
}
func (s *service) guardStream(ctx context.Context, approved ConnectionSnapshot, stream io.ReadCloser) (io.ReadCloser, error) {
	enabled, err := tenancy.Enabled(ctx, s.db)
	if err != nil {
		_ = stream.Close()
		return nil, err
	}
	if !enabled {
		return stream, nil
	}
	lifetime, cancel := context.WithCancel(ctx)
	guarded := &authorizedStream{ReadCloser: stream, ctx: lifetime, cancel: cancel}
	guarded.check = func(current context.Context) error {
		if err := tenancy.VerifyExecution(current); err != nil {
			return err
		}
		if err := tenancy.Check(current, s.db, "server.use", "server", approved.Server.ID); err != nil {
			return err
		}
		if err := vault.AuthorizeUse(current, s.vault, approved.Server.CredentialID); err != nil {
			return err
		}
		latest, err := s.Capture(current, approved.Server.ID)
		if err != nil {
			return err
		}
		if latest.Server.Host != approved.Server.Host || latest.Server.Port != approved.Server.Port || latest.Server.User != approved.Server.User || latest.Server.CredentialID != approved.Server.CredentialID || latest.CredentialVersion != approved.CredentialVersion {
			return tenancy.ErrConflict
		}
		return s.checkExecutionIdentity(current, &latest.Server)
	}
	if err = guarded.validate(); err != nil {
		return nil, err
	}
	go func() {
		tick := time.NewTicker(5 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-lifetime.Done():
				_ = guarded.Close()
				return
			case <-tick.C:
				if guarded.validate() != nil {
					return
				}
			}
		}
	}()
	return guarded, nil
}
