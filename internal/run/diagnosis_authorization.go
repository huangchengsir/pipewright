package run

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/tenancy"
)

// AuthorizeDiagnosis is the common boundary for HTTP, worker and other adapters.
// Legacy mocks remain supported only without any tenancy principal.
func AuthorizeDiagnosis(ctx context.Context, service Service, id string) error {
	if checker, ok := service.(interface {
		AuthorizeDiagnosis(context.Context, string) error
	}); ok {
		return checker.AuthorizeDiagnosis(ctx, id)
	}
	if tenancy.HasAuthorizationContext(ctx) {
		return tenancy.ErrForbidden
	}
	return nil
}
func (s *service) AuthorizeDiagnosis(ctx context.Context, id string) error {
	return tenancy.Check(ctx, s.db, "run.diagnose", "run", id)
}
