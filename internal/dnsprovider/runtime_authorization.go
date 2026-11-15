package dnsprovider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"github.com/huangchengsir/pipewright/internal/vault"
)

func (s *service) authorizeUse(ctx context.Context, id string) error {
	on, err := tenancy.Enabled(ctx, s.store.db)
	if err != nil || !on {
		return err
	}
	if err := tenancy.VerifyExecution(ctx); err != nil {
		return err
	}
	if err := tenancy.Check(ctx, s.store.db, "dns.use", "dns_provider", id); err != nil {
		return err
	}
	p, err := s.store.get(ctx, id)
	if err != nil {
		return err
	}
	return tenancy.Check(ctx, s.store.db, "credential.use", "credential", p.CredentialID)
}
func (s *service) useToken(ctx context.Context, p *Provider) (string, error) {
	if err := s.authorizeUse(ctx, p.ID); err != nil {
		return "", err
	}
	if v, ok := s.vault.(vault.Vault); ok {
		return vault.Use(ctx, v, p.CredentialID)
	}
	on, err := tenancy.Enabled(ctx, s.store.db)
	if err != nil {
		return "", err
	}
	if on {
		return "", tenancy.ErrForbidden
	}
	if s.vault == nil {
		return "", ErrVaultUnconfigured
	}
	return s.vault.Reveal(p.CredentialID)
}
func (s *service) runtimeVersion(ctx context.Context, id string) (string, error) {
	on, err := tenancy.Enabled(ctx, s.store.db)
	if err != nil || !on {
		return "", err
	}
	if err = s.authorizeUse(ctx, id); err != nil {
		return "", err
	}
	p, err := s.store.get(ctx, id)
	if err != nil {
		return "", err
	}
	return s.providerVersion(ctx, p)
}
func (s *service) providerVersion(ctx context.Context, p *Provider) (string, error) {
	on, err := tenancy.Enabled(ctx, s.store.db)
	if err != nil || !on {
		return "", err
	}
	var cipher []byte
	if err = configstate.Queryer(ctx, s.store.db).QueryRowContext(ctx, `SELECT ciphertext FROM credentials WHERE id=?`, p.CredentialID).Scan(&cipher); err != nil {
		return "", err
	}
	raw, _ := json.Marshal([]any{p.ID, p.Type, p.BaseDomain, p.CredentialID, cipher})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
func (s *service) runtimeClient(ctx context.Context, p *Provider) (DNSClient, error) {
	version, err := s.providerVersion(ctx, p)
	if err != nil {
		return nil, err
	}
	token, err := s.useToken(ctx, p)
	if err != nil {
		return nil, err
	}
	client := s.dial(p.Type, token)
	token = ""
	verify := func(c context.Context) error {
		if e := s.authorizeUse(c, p.ID); e != nil {
			return e
		}
		current, e := s.runtimeVersion(c, p.ID)
		if e != nil {
			return e
		}
		if current != version {
			return tenancy.ErrConflict
		}
		return nil
	}
	if err = verify(ctx); err != nil {
		return nil, err
	}
	return &authorizedDNSClient{client: client, verify: verify}, nil
}

type authorizedDNSClient struct {
	client DNSClient
	verify func(context.Context) error
}

func (c *authorizedDNSClient) EnsureARecord(ctx context.Context, zone, name, ip string) error {
	if e := c.verify(ctx); e != nil {
		return e
	}
	if e := c.client.EnsureARecord(ctx, zone, name, ip); e != nil {
		return e
	}
	return c.verify(ctx)
}
func (c *authorizedDNSClient) DeleteARecord(ctx context.Context, zone, name string) error {
	if e := c.verify(ctx); e != nil {
		return e
	}
	if e := c.client.DeleteARecord(ctx, zone, name); e != nil {
		return e
	}
	return c.verify(ctx)
}
func (c *authorizedDNSClient) VerifyZone(ctx context.Context, zone string) error {
	if e := c.verify(ctx); e != nil {
		return e
	}
	if e := c.client.VerifyZone(ctx, zone); e != nil {
		return e
	}
	return c.verify(ctx)
}
func (s *service) AuthorizeAllocation(ctx context.Context, in AllocateInput) error {
	on, err := tenancy.Enabled(ctx, s.store.db)
	if err != nil || !on {
		return err
	}
	if err := s.authorizeUse(ctx, in.ProviderID); err != nil {
		return err
	}
	if err := tenancy.Check(ctx, s.store.db, "proxy.manage", "server", in.ServerID); err != nil {
		return err
	}
	return tenancy.Check(ctx, s.store.db, "server.use", "server", in.ServerID)
}

func (s *service) prepareAllocation(ctx context.Context, in AllocateInput) (RouteCreator, func(context.Context) error, error) {
	if err := s.AuthorizeAllocation(ctx, in); err != nil {
		return nil, nil, err
	}
	verify := func(c context.Context) error { return s.AuthorizeAllocation(c, in) }
	on, err := tenancy.Enabled(ctx, s.store.db)
	if err != nil {
		return nil, nil, err
	}
	if !on {
		return s.routes, verify, nil
	}
	preparer, ok := s.routes.(interface {
		PrepareDNS01Route(context.Context, string) (RouteCreator, func(context.Context) error, error)
	})
	if !ok {
		return nil, nil, tenancy.ErrForbidden
	}
	route, targetCheck, err := preparer.PrepareDNS01Route(ctx, in.ServerID)
	if err != nil {
		return nil, nil, err
	}
	return route, func(c context.Context) error {
		if e := verify(c); e != nil {
			return e
		}
		return targetCheck(c)
	}, nil
}
