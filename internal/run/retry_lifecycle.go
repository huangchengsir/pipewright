package run

import (
	"context"
	"log"
	"time"
)

// RetryRuntime is installed before the worker pool starts. It is optional for
// legacy installations and tests; it never changes an original business result
// merely because recovery evidence could not be captured.
type RetryRuntime interface {
	Prepare(context.Context, string) (context.Context, error)
	Complete(context.Context, string, string) error
	Settle(context.Context, string) error
	IsRetry(context.Context, string) (bool, error)
	HasManifest(context.Context, string) (bool, error)
	BindStep(context.Context, string, string, string, int) error
}

func WithRetryRuntime(runtime RetryRuntime) PoolOption {
	return func(p *WorkerPool) { p.svc.retryRuntime = runtime }
}
func (s *service) prepareRetryAttempt(ctx context.Context, id string) (context.Context, error) {
	rr := s.retryRuntime
	if rr == nil {
		return ctx, nil
	}
	retry, err := rr.IsRetry(ctx, id)
	if err != nil {
		return ctx, err
	}
	captured, err := rr.HasManifest(ctx, id)
	if err != nil {
		return ctx, err
	}
	prepared, err := rr.Prepare(ctx, id)
	if err == nil {
		return prepared, nil
	}
	if retry || captured {
		if restorer, ok := rr.(interface {
			RestorePreparationFailure(context.Context, string) error
		}); ok {
			if restorer.RestorePreparationFailure(context.WithoutCancel(ctx), id) != nil {
				log.Printf("[run] run %s: preparation evidence remains unavailable", id)
			}
		}
		return ctx, err
	}
	// Do not log a provider or vault error; it may contain configuration details.
	log.Printf("[run] run %s: recovery capture unavailable; original execution continues", id)
	return ctx, nil
}
func (s *service) completeRetryAttempt(id string) {
	rr := s.retryRuntime
	if rr == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	captured, err := rr.HasManifest(ctx, id)
	if err != nil || !captured {
		return
	}
	r, err := s.Get(ctx, id)
	if err != nil || !IsTerminal(r.Status) {
		return
	}
	if restorer, ok := rr.(interface {
		RestoreQueuedCancellation(context.Context, string) error
	}); ok && r.StartedAt == nil {
		_ = restorer.RestoreQueuedCancellation(ctx, id)
	}
	if rr.Complete(ctx, id, r.Status) != nil {
		log.Printf("[run] run %s: recovery final result evidence unavailable", id)
	}
}
func (s *service) settleRetryAttempt(id string) {
	rr := s.retryRuntime
	if rr == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	captured, err := rr.HasManifest(ctx, id)
	if err != nil || !captured {
		return
	}
	if rr.Settle(ctx, id) != nil {
		log.Printf("[run] run %s: recovery remains blocked pending evidence", id)
	}
}
