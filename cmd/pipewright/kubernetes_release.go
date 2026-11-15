package main

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/kubernetes"
	"github.com/huangchengsir/pipewright/internal/runretry"
	"github.com/huangchengsir/pipewright/internal/tenancy"
)

// continuationContext resolves the exact persisted worker identity. The
// dispatcher has no interactive session and cannot authorize another run.
func (c *runAccessCompiler) continuationContext(ctx context.Context, runID string) (context.Context, error) {
	enabled, err := tenancy.Enabled(ctx, c.db)
	if err != nil || !enabled {
		return ctx, err
	}
	digest, err := c.Digest(ctx, runID)
	if err != nil {
		return ctx, err
	}
	execution := tenancy.RunContext(ctx, c.db, runID, digest)
	admission, err := tenancy.RunAdmissionAction(execution, c.db, runID)
	if err != nil {
		return ctx, err
	}
	if err = tenancy.CheckRunPermission(execution, c.db, admission, runID, ""); err != nil {
		return ctx, err
	}
	return execution, nil
}

type releaseRetryRuntime struct {
	*runretry.Service
	releases *kubernetes.Service
	owner    string
}

func (r releaseRetryRuntime) Prepare(ctx context.Context, id string) (context.Context, error) {
	if e := r.releases.ActivateReleaseRun(ctx, id, r.owner); e != nil {
		return ctx, e
	}
	return r.Service.Prepare(ctx, id)
}
func (r releaseRetryRuntime) Complete(ctx context.Context, id, status string) error {
	// Persist external facts even when a panic/cancellation skipped a node callback.
	// Completion still records the run termination when cleanup requires intervention.
	e := r.releases.FinalizeReleaseRun(ctx, id)
	return errors.Join(e, r.Service.Complete(ctx, id, status))
}

func (r releaseRetryRuntime) BindRunReader(ctx context.Context, id, reader string) error {
	return r.releases.BindReleaseContinuationReader(ctx, id, r.owner, reader)
}
