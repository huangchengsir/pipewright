package runner

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"strings"
)

func (s *service) Get(ctx context.Context, projectID string) (*Config, error) {
	projectID = strings.TrimSpace(projectID)
	if err := tenancy.Check(ctx, s.db, "pipeline.read", "project", projectID); err != nil {
		return nil, err
	}
	cfg, err := s.get(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if err = tenancy.Check(ctx, s.db, "pipeline.read", "project", projectID); err != nil {
		return nil, err
	}
	return cfg, nil
}
func (s *service) Save(ctx context.Context, projectID, serverID string) (*Config, error) {
	projectID = strings.TrimSpace(projectID)
	serverID = strings.TrimSpace(serverID)
	if err := tenancy.Check(ctx, s.db, "pipeline.edit", "project", projectID); err != nil {
		return nil, err
	}
	var cfg *Config
	err := configstate.WithWriteTxContext(ctx, s.db, func(c context.Context, _ configstate.DBTX) error {
		if err := tenancy.LockAuthorization(c, s.db); err != nil {
			return err
		}
		if err := tenancy.Check(c, s.db, "pipeline.edit", "project", projectID); err != nil {
			return err
		}
		if serverID != "" {
			if err := tenancy.CheckSameTeam(c, s.db, authz.ResourceRef{Kind: "server", ID: serverID}); err != nil {
				return err
			}
			if err := tenancy.Check(c, s.db, "server.use", "server", serverID); err != nil {
				return err
			}
		}
		var err error
		cfg, err = s.save(c, projectID, serverID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return cfg, nil
}
