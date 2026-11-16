package kubernetes

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/tenancy"
)

// AuthorizeRunEvidence is the user-facing boundary. Internal runtime reads stay
// separate so identity-proved cleanup does not depend on an interactive session.
func (s *Service) AuthorizeRunEvidence(ctx context.Context, runID string) error {
	return tenancy.Check(ctx, s.db, "run.read", "run", runID)
}

func (s *Service) ReadOperationForRun(ctx context.Context, runID, id string) (Operation, error) {
	if err := s.AuthorizeRunEvidence(ctx, runID); err != nil {
		return Operation{}, err
	}
	o, err := s.GetOperation(ctx, id)
	if err != nil {
		return Operation{}, err
	}
	if o.RunID != runID {
		return Operation{}, ErrNotFound
	}
	return o, nil
}

func (s *Service) ReadReleaseForRun(ctx context.Context, runID, id string) (ReleaseRecord, error) {
	if err := s.AuthorizeRunEvidence(ctx, runID); err != nil {
		return ReleaseRecord{}, err
	}
	r, err := s.GetRelease(ctx, id)
	if err != nil {
		return ReleaseRecord{}, err
	}
	if r.RunID != runID {
		return ReleaseRecord{}, ErrNotFound
	}
	return r, nil
}

// Recovery changes durable execution state and can access the original cluster.
// A readable run or a recent password alone never grants this authority.
func (s *Service) AuthorizeOperationRecovery(ctx context.Context, runID, id string) error {
	o, err := s.ReadOperationForRun(ctx, runID, id)
	if err != nil {
		return err
	}
	if err = tenancy.Check(ctx, s.db, "run.retry", "run", runID); err != nil {
		return err
	}
	t := o.Intent.Authorization.Target
	return s.authorizeTenantTarget(ctx, t.ProjectID, t.Environment, t.ConnectionID, t.CredentialID, t.Executor)
}

func (s *Service) AuthorizeReleaseRecovery(ctx context.Context, runID, id string) error {
	r, err := s.ReadReleaseForRun(ctx, runID, id)
	if err != nil {
		return err
	}
	if err = tenancy.Check(ctx, s.db, "run.retry", "run", runID); err != nil {
		return err
	}
	t := r.Plan.Authorization.Request
	return s.authorizeTenantTarget(ctx, t.ProjectID, t.Environment, t.ConnectionID, t.CredentialID, t.Executor)
}

// ListOperationsForRun exposes only the authorized run's bounded evidence list.
func (s *Service) ListOperationsForRun(ctx context.Context, runID string) ([]Operation, error) {
	if err := s.AuthorizeRunEvidence(ctx, runID); err != nil {
		return nil, err
	}
	return s.ListOperations(ctx, runID)
}

func (s *Service) ListReleasesForRun(ctx context.Context, runID string) ([]ReleaseRecord, error) {
	if err := s.AuthorizeRunEvidence(ctx, runID); err != nil {
		return nil, err
	}
	return s.ListReleases(ctx, runID)
}
