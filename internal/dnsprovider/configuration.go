package dnsprovider

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"github.com/huangchengsir/pipewright/internal/vault"
	"strings"
)

func (s *service) AuthorizeConfiguration(ctx context.Context) error {
	return tenancy.RequireInstanceAdministrator(ctx, s.store.db)
}
func configurationWrite[T any](ctx context.Context, s *service, fn func(context.Context) (T, error)) (T, error) {
	var result T
	err := configstate.WithWriteTxContext(ctx, s.store.db, func(c context.Context, _ configstate.DBTX) error {
		if e := tenancy.LockAuthorization(c, s.store.db); e != nil {
			return e
		}
		if e := s.AuthorizeConfiguration(c); e != nil {
			return e
		}
		var e error
		result, e = fn(c)
		return e
	})
	return result, err
}
func (s *service) Create(ctx context.Context, in CreateInput) (*Provider, error) {
	return configurationWrite(ctx, s, func(c context.Context) (*Provider, error) { return s.create(c, in) })
}

// CreateWithToken commits the write-only credential and provider together. A
// rejected or invalid provider never leaves a credential behind.
func (s *service) CreateWithToken(ctx context.Context, in CreateInput, token string) (*Provider, error) {
	return configurationWrite(ctx, s, func(c context.Context) (*Provider, error) {
		v, ok := s.vault.(vault.Vault)
		if !ok || v == nil {
			return nil, ErrVaultUnconfigured
		}
		cred, err := vault.WithContext(c, v).Create(vault.CreateInput{Name: "DNS · " + strings.TrimSpace(in.Name), Type: vault.TypeDNSToken, Secret: token})
		if err != nil {
			return nil, err
		}
		in.CredentialID = cred.ID
		return s.create(c, in)
	})
}
