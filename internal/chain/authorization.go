package chain

import (
	"context"
	"github.com/google/uuid"
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
	// Do not filter a replacement form: saving a partial view would delete hidden targets.
	for _, target := range cfg.Targets {
		if err = tenancy.CheckSameTeam(ctx, s.db, authz.ResourceRef{Kind: "project", ID: target.DownstreamProjectID}); err != nil {
			return nil, err
		}
		if err = tenancy.Check(ctx, s.db, "project.read", "project", target.DownstreamProjectID); err != nil {
			return nil, err
		}
	}
	if err = tenancy.Check(ctx, s.db, "pipeline.read", "project", projectID); err != nil {
		return nil, err
	}
	if on, e := tenancy.Enabled(ctx, s.db); e != nil {
		return nil, e
	} else if on {
		for i, target := range cfg.Targets {
			if !target.Enabled {
				continue
			}
			cfg.Targets[i].AuthorizationState = "required"
			var id string
			if e = configstate.Queryer(ctx, s.db).QueryRowContext(ctx, `SELECT id FROM project_chain_targets WHERE project_id=? AND downstream_project_id=? AND branch=? AND enabled=1`, projectID, target.DownstreamProjectID, target.Branch).Scan(&id); e != nil {
				return nil, e
			}
			digest, _, e := s.targetDigest(ctx, id)
			if e == nil && tenancy.AutomationReady(ctx, s.db, "chain", id, digest, func(c context.Context, q configstate.DBTX) (string, error) {
				d, _, e := s.targetDigest(configstate.WithTxContext(c, s.db, q), id)
				return d, e
			}) {
				cfg.Targets[i].AuthorizationState = "ready"
			}
		}
	}
	return cfg, nil
}
func (s *service) Save(ctx context.Context, projectID string, targets []Target) (*Config, error) {
	projectID = strings.TrimSpace(projectID)
	if err := tenancy.Check(ctx, s.db, "pipeline.edit", "project", projectID); err != nil {
		return nil, err
	}
	ctx = configstate.WithWriteAdmission(ctx, tenancy.LockAuthorization)
	err := configstate.WithWriteTxContext(ctx, s.db, func(c context.Context, _ configstate.DBTX) error {
		if err := tenancy.LockAuthorization(c, s.db); err != nil {
			return err
		}
		if err := tenancy.Check(c, s.db, "pipeline.edit", "project", projectID); err != nil {
			return err
		}
		if len(targets) > maxTargetsPerProject {
			return ErrTooManyTargets
		}
		for _, target := range targets {
			id := strings.TrimSpace(target.DownstreamProjectID)
			if err := tenancy.CheckSameTeam(c, s.db, authz.ResourceRef{Kind: "project", ID: id}); err != nil {
				return err
			}
			if err := tenancy.Check(c, s.db, "project.read", "project", id); err != nil {
				return err
			}
			if target.Enabled {
				if err := tenancy.CheckRunAdmission(c, s.db, id, uuid.NewString()); err != nil {
					return err
				}
			}
		}
		var err error
		_, err = s.save(c, projectID, targets)
		if err != nil {
			return err
		}
		return s.approveTargets(c, projectID)
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, projectID)
}
