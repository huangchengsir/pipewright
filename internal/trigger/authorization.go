package trigger

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"strings"
)

// A tenant read must not create credentials or advance configuration revisions.
// Legacy single-administrator mode retains its lazy default behavior.
func (s *service) Get(ctx context.Context, projectID string) (*Config, error) {
	projectID = strings.TrimSpace(projectID)
	if err := tenancy.Check(ctx, s.db, "pipeline.read", "project", projectID); err != nil {
		return nil, err
	}
	enabled, err := tenancy.Enabled(ctx, s.db)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return s.get(ctx, projectID)
	}
	cfg, masked, err := s.load(ctx, projectID)
	if errors.Is(err, ErrNotFound) {
		if err = s.ensureProjectExists(ctx, projectID); err != nil {
			return nil, err
		}
		cfg = &Config{ProjectID: projectID, BranchMappings: []BranchMapping{}, PathFilters: []string{}, UnmatchedPolicy: PolicyRecord}
	} else if err != nil {
		return nil, err
	} else {
		cfg.WebhookSecretMasked = masked
	}
	if err = tenancy.Check(ctx, s.db, "pipeline.read", "project", projectID); err != nil {
		return nil, err
	}
	if cfg.Events.Push || cfg.Events.Tag || cfg.Events.PullRequest || cfg.Events.Release {
		cfg.AuthorizationState = "required"
		digest, e := triggerDigest(ctx, configstate.Queryer(ctx, s.db), projectID)
		if e == nil && tenancy.AutomationReady(ctx, s.db, "trigger", projectID, digest, func(c context.Context, q configstate.DBTX) (string, error) { return triggerDigest(c, q, projectID) }) {
			cfg.AuthorizationState = "ready"
		}
	}
	return cfg, nil
}
func (s *service) mutateAuthorized(ctx context.Context, projectID string, fn func(context.Context) error) error {
	if err := tenancy.Check(ctx, s.db, "pipeline.edit", "project", projectID); err != nil {
		return err
	}
	ctx = configstate.WithWriteAdmission(ctx, tenancy.LockAuthorization)
	return configstate.WithWriteTxContext(ctx, s.db, func(c context.Context, _ configstate.DBTX) error {
		if err := tenancy.LockAuthorization(c, s.db); err != nil {
			return err
		}
		if err := tenancy.Check(c, s.db, "pipeline.edit", "project", projectID); err != nil {
			return err
		}
		return fn(c)
	})
}
func (s *service) Save(ctx context.Context, projectID string, in SaveInput) (*Config, error) {
	projectID = strings.TrimSpace(projectID)
	var result *Config
	err := s.mutateAuthorized(ctx, projectID, func(c context.Context) error {
		mappings, err := normalizeMappings(in.BranchMappings)
		if err != nil {
			return err
		}
		for _, m := range mappings {
			for _, id := range m.TargetServerIDs {
				if err := tenancy.CheckSameTeam(c, s.db, authz.ResourceRef{Kind: "server", ID: id}); err != nil {
					return err
				}
				if err := tenancy.Check(c, s.db, "server.use", "server", id); err != nil {
					return err
				}
			}
		}
		in.BranchMappings = mappings
		result, err = s.save(c, projectID, in)
		if err != nil {
			return err
		}
		for _, m := range mappings {
			if m.Environment != "" {
				if err = tenancy.RegisterEnvironment(c, s.db, projectID, m.Environment); err != nil {
					return err
				}
			}
		}
		return s.approveTrigger(c, projectID, result)
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, projectID)
}
func (s *service) ResetSecret(ctx context.Context, projectID string) (*ResetResult, error) {
	projectID = strings.TrimSpace(projectID)
	var result *ResetResult
	err := s.mutateAuthorized(ctx, projectID, func(c context.Context) error {
		var err error
		result, err = s.resetSecret(c, projectID)
		if err != nil {
			return err
		}
		cfg, _, loadErr := s.load(c, projectID)
		if loadErr != nil {
			return loadErr
		}
		if err = s.approveTrigger(c, projectID, cfg); err != nil {
			return err
		}
		current, err := s.Get(c, projectID)
		if err != nil {
			return err
		}
		result.AuthorizationState = current.AuthorizationState
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
