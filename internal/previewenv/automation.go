package previewenv

import (
	"context"
	"crypto/sha256"

	"encoding/hex"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/tenancy"
)

func previewDigest(c *Config) string {
	b, _ := json.Marshal(struct {
		Project  string
		Enabled  bool
		Provider string
		Domain   string
	}{c.ProjectID, c.Enabled, c.DNSProviderID, c.BaseDomain})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (s *service) currentDigest(project string) func(context.Context, configstate.DBTX) (string, error) {
	return func(ctx context.Context, q configstate.DBTX) (string, error) {
		var c Config
		var enabled int
		c.ProjectID = project
		if e := q.QueryRowContext(ctx, `SELECT enabled,dns_provider_id,base_domain FROM preview_configs WHERE project_id=?`, project).Scan(&enabled, &c.DNSProviderID, &c.BaseDomain); e != nil {
			return "", e
		}
		if enabled != 1 {
			return "", tenancy.ErrForbidden
		}
		c.Enabled = true
		return previewDigest(&c), nil
	}
}
func (s *service) authorizeResources(ctx context.Context, project, provider, server string) error {
	on, e := tenancy.Enabled(ctx, s.store.db)
	if e != nil || !on {
		return e
	}
	if e = s.authorizeProvider(ctx, project, provider); e != nil {
		return e
	}
	var credential string
	if server == "" {
		return tenancy.RequireInstanceAdministrator(ctx, s.store.db)
	}
	if e = tenancy.CheckSameTeam(ctx, s.store.db, authz.ResourceRef{Kind: "server", ID: server}); e != nil {
		return e
	}
	if e = tenancy.Check(ctx, s.store.db, "server.use", "server", server); e != nil {
		return e
	}
	if e = tenancy.Check(ctx, s.store.db, "proxy.manage", "server", server); e != nil {
		return e
	}
	if e = configstate.Queryer(ctx, s.store.db).QueryRowContext(ctx, `SELECT credential_id FROM servers WHERE id=?`, server).Scan(&credential); e != nil {
		return e
	}
	return tenancy.Check(ctx, s.store.db, "credential.use", "credential", credential)
}
func (s *service) approveConfig(ctx context.Context, c Config) error {
	if !c.Enabled {
		return tenancy.RevokeAutomation(ctx, s.store.db, "preview", c.ProjectID)
	}
	return tenancy.ApproveProjectAutomation(ctx, s.store.db, "preview", c.ProjectID, c.ProjectID, previewDigest(&c))
}
func (s *service) automaticProjectContext(ctx context.Context, project string) (context.Context, error) {
	digest, e := s.currentDigest(project)(ctx, configstate.Queryer(ctx, s.store.db))
	if e != nil {
		return nil, e
	}
	return tenancy.AutomationContext(ctx, s.store.db, "preview", project, digest, s.currentDigest(project))
}

// Controller-only enumeration returns IDs, never user-visible rows. Each ID is
// then resolved through its own saved employee approval before PR/network work.
func (s *service) automaticProjects(ctx context.Context) ([]string, error) {
	if tenancy.HasAuthorizationContext(ctx) {
		return nil, tenancy.ErrForbidden
	}
	rows, e := s.store.db.QueryContext(ctx, `SELECT project_id FROM preview_configs WHERE enabled=1 ORDER BY project_id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			return nil, e
		}
		out = append(out, id)
		if len(out) > 4096 {
			return nil, tenancy.ErrInvalidInput
		}
	}
	return out, rows.Err()
}

func (s *service) authorizeProvider(ctx context.Context, project, provider string) error {
	on, e := tenancy.Enabled(ctx, s.store.db)
	if e != nil || !on {
		return e
	}
	if e = tenancy.CheckSameTeam(ctx, s.store.db, authz.ResourceRef{Kind: "project", ID: project}, authz.ResourceRef{Kind: "dns_provider", ID: provider}); e != nil {
		return e
	}
	if e = tenancy.Check(ctx, s.store.db, "dns.use", "dns_provider", provider); e != nil {
		return e
	}
	var credential string
	if e = configstate.Queryer(ctx, s.store.db).QueryRowContext(ctx, `SELECT credential_id FROM dns_providers WHERE id=?`, provider).Scan(&credential); e != nil {
		return e
	}
	if e = tenancy.Check(ctx, s.store.db, "credential.use", "credential", credential); e != nil {
		return e
	}
	return nil
}
