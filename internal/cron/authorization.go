package cron

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"strings"
)

func (s *service) Get(ctx context.Context, project string) (*Config, error) {
	project = strings.TrimSpace(project)
	if err := tenancy.Check(ctx, s.db, "pipeline.read", "project", project); err != nil {
		return nil, err
	}
	out, err := s.get(ctx, project)
	if err != nil {
		return nil, err
	}
	if on, e := tenancy.Enabled(ctx, s.db); e != nil {
		return nil, e
	} else if on && out.Enabled {
		out.AuthorizationState = "required"
		digest, e := s.automationDigest(ctx, project)
		if e == nil && tenancy.AutomationReady(ctx, s.db, "cron", project, digest, func(c context.Context, q configstate.DBTX) (string, error) {
			return s.automationDigest(configstate.WithTxContext(c, s.db, q), project)
		}) {
			out.AuthorizationState = "ready"
		}
	}
	return out, nil
}
func (s *service) Save(ctx context.Context, project string, in SaveInput) (*Config, error) {
	project = strings.TrimSpace(project)
	if err := tenancy.Check(ctx, s.db, "pipeline.edit", "project", project); err != nil {
		return nil, err
	}
	var out *Config
	ctx = configstate.WithWriteAdmission(ctx, tenancy.LockAuthorization)
	err := configstate.WithWriteTxContext(ctx, s.db, func(c context.Context, _ configstate.DBTX) error {
		if err := tenancy.Check(c, s.db, "pipeline.edit", "project", project); err != nil {
			return err
		}
		var err error
		out, err = s.save(c, project, in)
		if err != nil {
			return err
		}
		if !out.Enabled {
			return tenancy.RevokeAutomation(c, s.db, "cron", project)
		}
		digest, err := s.automationDigest(c, project)
		if err != nil {
			return err
		}
		return tenancy.ApproveProjectAutomation(c, s.db, "cron", project, project, digest)
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, project)
}
func (s *service) automationDigest(ctx context.Context, project string) (string, error) {
	cfg, err := s.get(ctx, project)
	if err != nil {
		return "", err
	}
	if !cfg.Enabled {
		return "", tenancy.ErrForbidden
	}
	body, err := json.Marshal(struct{ Project, Expression, Branch string }{project, cfg.Expression, cfg.Branch})
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(body)
	return hex.EncodeToString(hash[:]), nil
}

// ScheduledContext is called only by the controller after matching the exact
// saved schedule. HTTP cannot select an approval identity.
func (s *service) ScheduledContext(ctx context.Context, entry Entry) (context.Context, error) {
	on, err := tenancy.Enabled(ctx, s.db)
	if err != nil || !on {
		return ctx, err
	}
	cfg, err := s.get(ctx, entry.ProjectID)
	if err != nil {
		return nil, err
	}
	if !cfg.Enabled || cfg.Expression != entry.Expression || cfg.Branch != entry.Branch {
		return nil, tenancy.ErrForbidden
	}
	digest, err := s.automationDigest(ctx, entry.ProjectID)
	if err != nil {
		return nil, err
	}
	return tenancy.AutomationContext(ctx, s.db, "cron", entry.ProjectID, digest, func(c context.Context, q configstate.DBTX) (string, error) {
		return s.automationDigest(configstate.WithTxContext(c, s.db, q), entry.ProjectID)
	})
}
