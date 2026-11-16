package previewenv

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/tenancy"
)

type ProviderChoice struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	BaseDomain string `json:"baseDomain"`
}

// Providers is a minimal use-selector, not the instance DNS configuration API.
// SQL intersects provider use, credential use and the actual project's team.
func (s *service) Providers(ctx context.Context, project string) ([]ProviderChoice, error) {
	if e := tenancy.Check(ctx, s.store.db, "pipeline.read", "project", project); e != nil {
		return nil, e
	}
	provider, e := tenancy.QueryPredicate(ctx, s.store.db, "dns.use", "p.id")
	if e != nil {
		return nil, e
	}
	credential, e := tenancy.QueryPredicate(ctx, s.store.db, "credential.use", "p.credential_id")
	if e != nil {
		return nil, e
	}
	on, e := tenancy.Enabled(ctx, s.store.db)
	if e != nil {
		return nil, e
	}
	query := `SELECT p.id,p.name,p.base_domain FROM dns_providers p WHERE (` + provider.SQL + `) AND (` + credential.SQL + `)`
	args := append(provider.Args, credential.Args...)
	if on {
		query += ` AND EXISTS (SELECT 1 FROM access_ownership a JOIN access_ownership b ON b.tenant_id=a.tenant_id WHERE a.resource_kind='dns_provider' AND a.resource_id=p.id AND b.resource_kind='project' AND b.resource_id=?)`
		args = append(args, project)
	}
	rows, e := configstate.Queryer(ctx, s.store.db).QueryContext(ctx, query+` ORDER BY p.name,p.id`, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []ProviderChoice{}
	for rows.Next() {
		var c ProviderChoice
		if e = rows.Scan(&c.ID, &c.Name, &c.BaseDomain); e != nil {
			return nil, e
		}
		out = append(out, c)
		if len(out) > 2048 {
			return nil, tenancy.ErrInvalidInput
		}
	}
	return out, rows.Err()
}
