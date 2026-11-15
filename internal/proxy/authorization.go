package proxy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/target"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"io"
)

func (s *service) authorize(ctx context.Context, action, server string) error {
	if err := tenancy.VerifyExecution(ctx); err != nil {
		return err
	}
	return tenancy.Check(ctx, s.store.db, action, "server", server)
}
func (s *service) authorizedRoute(ctx context.Context, id, action string) (*Route, error) {
	r, err := s.store.get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err = s.authorize(ctx, action, r.ServerID); err != nil {
		return nil, err
	}
	return r, nil
}

// Multi-step configuration keeps one approved SSH identity. Changing an endpoint
// or credential between upload and reload must not redirect later operations.
func (s *service) bindExecution(ctx context.Context, server, action string) (*service, context.Context, error) {
	if err := s.authorize(ctx, action, server); err != nil {
		return nil, ctx, err
	}
	boundary := tenancy.WithActionBoundary(ctx, func(c context.Context) error { return tenancy.Check(c, s.store.db, action, "server", server) })
	on, err := tenancy.Enabled(boundary, s.store.db)
	if err != nil || !on {
		return s, boundary, err
	}
	if existing, ok := s.tg.(*pinnedTarget); ok {
		if existing.approved.Server.ID != server {
			return nil, ctx, tenancy.ErrForbidden
		}
		return s, boundary, nil
	}
	pinned, ok := s.tg.(target.PinnedExecutor)
	if !ok {
		return nil, ctx, tenancy.ErrForbidden
	}
	approved, err := pinned.Capture(boundary, server)
	if err != nil {
		return nil, ctx, err
	}
	if err = tenancy.Check(boundary, s.store.db, "server.use", "server", server); err != nil {
		return nil, ctx, err
	}
	if err = tenancy.Check(boundary, s.store.db, "credential.use", "credential", approved.Server.CredentialID); err != nil {
		return nil, ctx, err
	}
	copy := *s
	copy.tg = &pinnedTarget{Service: s.tg, pinned: pinned, approved: approved}
	return &copy, boundary, nil
}

type pinnedTarget struct {
	target.Service
	pinned   target.PinnedExecutor
	approved target.ConnectionSnapshot
}

func (p *pinnedTarget) Exec(ctx context.Context, id string, argv []string) (*target.ExecResult, error) {
	if err := tenancy.VerifyExecution(ctx); err != nil {
		return nil, err
	}
	if id != p.approved.Server.ID {
		return nil, tenancy.ErrForbidden
	}
	result, err := p.pinned.ExecPinned(ctx, p.approved, argv, target.ExecutionLimits{})
	if err != nil {
		return nil, err
	}
	return &target.ExecResult{Stdout: result.Stdout, Stderr: result.Stderr, ExitCode: result.ExitCode}, nil
}
func (p *pinnedTarget) Upload(ctx context.Context, id string, content io.Reader, path string) error {
	if err := tenancy.VerifyExecution(ctx); err != nil {
		return err
	}
	if id != p.approved.Server.ID {
		return tenancy.ErrForbidden
	}
	return p.pinned.UploadPinned(ctx, p.approved, content, path)
}
func (s *service) write(ctx context.Context, server, action string, fn func(context.Context) error) error {
	return configstate.WithWriteTxContext(configstate.WithWriteAdmission(ctx, tenancy.LockAuthorization), s.store.db, func(c context.Context, _ configstate.DBTX) error {
		if err := s.authorize(c, action, server); err != nil {
			return err
		}
		return fn(c)
	})
}

func (s *service) recordRouteOwnership(ctx context.Context, r *Route) error {
	principal, on, err := tenancy.CurrentPrincipal(ctx, s.store.db)
	if err != nil || !on {
		return err
	}
	result, err := configstate.Queryer(ctx, s.store.db).ExecContext(ctx, `INSERT INTO access_ownership(resource_kind,resource_id,tenant_id,owner_id,project_id,environment_id) SELECT 'proxy_route',?,tenant_id,?,NULL,NULL FROM access_ownership WHERE resource_kind='server' AND resource_id=?`, r.ID, principal.ID, r.ServerID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return tenancy.ErrForbidden
	}
	return nil
}
func (s *service) authorizeDNS(ctx context.Context, id string) error {
	if err := tenancy.Check(ctx, s.store.db, "dns.use", "dns_provider", id); err != nil {
		return err
	}
	on, err := tenancy.Enabled(ctx, s.store.db)
	if err != nil || !on {
		return err
	}
	var credential string
	if err = configstate.Queryer(ctx, s.store.db).QueryRowContext(ctx, `SELECT credential_id FROM dns_providers WHERE id=?`, id).Scan(&credential); err != nil {
		return tenancy.ErrForbidden
	}
	return tenancy.Check(ctx, s.store.db, "credential.use", "credential", credential)
}

// PrepareDNSRoute fixes the execution target before a DNS record is written.
// The returned verifier remains live and must also run after each external step.
func (s *service) PrepareDNSRoute(ctx context.Context, server string) (Service, func(context.Context) error, error) {
	bound, _, err := s.bindExecution(ctx, server, "proxy.manage")
	if err != nil {
		return nil, nil, err
	}
	verify := func(c context.Context) error {
		if e := bound.authorize(c, "proxy.manage", server); e != nil {
			return e
		}
		on, e := tenancy.Enabled(c, s.store.db)
		if e != nil || !on {
			return e
		}
		pinned, ok := bound.tg.(*pinnedTarget)
		if !ok {
			return tenancy.ErrForbidden
		}
		check, ok := pinned.Service.(interface {
			AuthorizePinned(context.Context, target.ConnectionSnapshot) error
		})
		if !ok {
			return tenancy.ErrForbidden
		}
		return check.AuthorizePinned(c, pinned.approved)
	}
	if err = verify(ctx); err != nil {
		return nil, nil, err
	}
	return bound, verify, nil
}

// DNS token versions bind rendered configuration to the provider and sealed
// credential observed before resolution. No plaintext is included in a digest.
func (s *service) dnsVersion(ctx context.Context, id string) (string, error) {
	on, e := tenancy.Enabled(ctx, s.store.db)
	if e != nil || !on {
		return "", e
	}
	if e = s.authorizeDNS(ctx, id); e != nil {
		return "", e
	}
	var kind, domain, credential string
	var sealed []byte
	e = configstate.Queryer(ctx, s.store.db).QueryRowContext(ctx, `SELECT d.type,d.base_domain,d.credential_id,c.ciphertext FROM dns_providers d JOIN credentials c ON c.id=d.credential_id WHERE d.id=?`, id).Scan(&kind, &domain, &credential, &sealed)
	if e != nil {
		return "", e
	}
	raw, _ := json.Marshal([]any{id, kind, domain, credential, sealed})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
