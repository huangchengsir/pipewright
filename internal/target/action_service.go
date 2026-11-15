package target

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"io"
)

// ForContainerAction binds a fixed product operation to its real parent server.
// Container identifiers are interpreted only by that server's validated command,
// never looked up globally or assigned synthetic cross-server ownership.
func ForContainerAction(s Service, action string) Service {
	if s == nil {
		return nil
	}
	return &actionService{Service: s, action: action}
}

type actionService struct {
	Service
	action string
}

func (s *actionService) authorize(ctx context.Context, id string) error {
	switch s.action {
	case "container.inspect", "container.operate", "container.terminal", "container.reveal", "container.diagnose":
	default:
		return tenancy.ErrForbidden
	}
	if real, ok := s.Service.(*service); ok {
		return tenancy.Check(ctx, real.db, s.action, "server", id)
	}
	if tenancy.HasAuthorizationContext(ctx) {
		return tenancy.ErrForbidden
	}
	return nil
}
func (s *actionService) bind(ctx context.Context, id string) context.Context {
	return tenancy.WithActionBoundary(ctx, func(current context.Context) error { return s.authorize(current, id) })
}
func (s *actionService) Get(ctx context.Context, id string) (*Server, error) {
	if err := s.authorize(ctx, id); err != nil {
		return nil, err
	}
	if real, ok := s.Service.(*service); ok {
		return real.getAction(ctx, s.action, id)
	}
	return s.Service.Get(ctx, id)
}
func (s *actionService) List(ctx context.Context) ([]*Server, error) {
	if real, ok := s.Service.(*service); ok {
		return real.listAction(ctx, s.action)
	}
	if tenancy.HasAuthorizationContext(ctx) {
		return nil, tenancy.ErrForbidden
	}
	return s.Service.List(ctx)
}
func (s *actionService) Exec(ctx context.Context, id string, cmd []string) (*ExecResult, error) {
	if err := s.authorize(ctx, id); err != nil {
		return nil, err
	}
	return s.Service.Exec(s.bind(ctx, id), id, cmd)
}
func (s *actionService) ExecStream(ctx context.Context, id string, cmd []string) (io.ReadCloser, error) {
	if err := s.authorize(ctx, id); err != nil {
		return nil, err
	}
	return s.Service.ExecStream(s.bind(ctx, id), id, cmd)
}
func (s *actionService) Upload(ctx context.Context, id string, content io.Reader, path string) error {
	if err := s.authorize(ctx, id); err != nil {
		return err
	}
	return s.Service.Upload(s.bind(ctx, id), id, content, path)
}
func (s *actionService) ExecInteractive(ctx context.Context, id string, cmd []string) (Session, error) {
	if s.action != "container.terminal" || !validContainerTerminal(cmd) {
		return nil, tenancy.ErrForbidden
	}
	if err := s.authorize(ctx, id); err != nil {
		return nil, err
	}
	return s.Service.ExecInteractive(context.WithValue(s.bind(ctx, id), containerTerminalKey{}, id), id, cmd)
}
func (s *actionService) AuthorizeTerminal(ctx context.Context, id string) error {
	if err := s.authorize(ctx, id); err != nil {
		return err
	}
	return AuthorizeTerminal(context.WithValue(s.bind(ctx, id), containerTerminalKey{}, id), s.Service, id)
}

// CanRevealContainerConfig never widens a caller's container inspection scope.
func CanRevealContainerConfig(ctx context.Context, svc Service, id string) bool {
	if wrapped, ok := svc.(*actionService); ok {
		return CanRevealContainerConfig(ctx, wrapped.Service, id)
	}
	if real, ok := svc.(*service); ok {
		return tenancy.Check(ctx, real.db, "container.reveal", "server", id) == nil
	}
	return !tenancy.HasAuthorizationContext(ctx)
}
