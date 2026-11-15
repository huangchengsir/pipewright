package vault

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/tenancy"
)

// WithContext binds an immutable request/execution scope without changing the
// legacy Vault interface. Production decorators must preserve this method.
func WithContext(ctx context.Context, v Vault) Vault {
	if scoped, ok := v.(interface{ WithContext(context.Context) Vault }); ok {
		return scoped.WithContext(ctx)
	}
	return v
}
func (s *service) WithContext(ctx context.Context) Vault { copy := *s; copy.ctx = ctx; return &copy }
func (s *service) context() context.Context {
	if s.ctx != nil {
		return s.ctx
	}
	return context.Background()
}
func (s *service) AuthorizeUse(ctx context.Context, id string) error {
	if id == "" {
		return nil
	}
	return tenancy.Check(ctx, s.db, "credential.use", "credential", id)
}
func AuthorizeUse(ctx context.Context, v Vault, id string) error {
	if scoped, ok := v.(interface {
		AuthorizeUse(context.Context, string) error
	}); ok {
		return scoped.AuthorizeUse(ctx, id)
	}
	return nil // older in-memory test adapters have no persistent tenant state
}

// Use returns plaintext only for internal consumption. User-facing reveal uses
// its separate action; neither grants the other action implicitly.
func Use(ctx context.Context, v Vault, id string) (string, error) {
	if err := AuthorizeUse(ctx, v, id); err != nil {
		return "", err
	}
	return WithContext(ctx, v).Get(id)
}

// Columns are fixed implementation literals. The policy and live actor guard
// are part of the same query that reads the selected row, before decryption.
func (s *service) readCredential(action, id, columns string, dest ...any) error {
	predicate, err := tenancy.QueryPredicate(s.context(), s.db, action, "credentials.id")
	if err != nil {
		return err
	}
	args := append([]any{id}, predicate.Args...)
	return configstate.Queryer(s.context(), s.db).QueryRowContext(s.context(), `SELECT `+columns+` FROM credentials WHERE id=? AND `+predicate.SQL, args...).Scan(dest...)
}
