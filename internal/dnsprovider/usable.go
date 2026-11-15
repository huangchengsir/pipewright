package dnsprovider

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/tenancy"
)

// UsableProvider is a bounded selection view. It never includes credential
// identity, ciphertext, application configuration or provider-wide settings.
type UsableProvider struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Name       string `json:"name"`
	BaseDomain string `json:"baseDomain"`
}

func (s *service) ListUsable(ctx context.Context) ([]UsableProvider, error) {
	provider, e := tenancy.QueryPredicate(ctx, s.store.db, "dns.use", "d.id")
	if e != nil {
		return nil, e
	}
	credential, e := tenancy.QueryPredicate(ctx, s.store.db, "credential.use", "d.credential_id")
	if e != nil {
		return nil, e
	}
	args := append(append([]any{}, provider.Args...), credential.Args...)
	rows, e := configstate.Queryer(ctx, s.store.db).QueryContext(ctx, `SELECT d.id,d.type,d.name,d.base_domain FROM dns_providers d WHERE (`+provider.SQL+`) AND (`+credential.SQL+`) ORDER BY d.name,d.id`, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []UsableProvider{}
	for rows.Next() {
		var item UsableProvider
		if e = rows.Scan(&item.ID, &item.Type, &item.Name, &item.BaseDomain); e != nil {
			return nil, e
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
