package configdraft

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/tenancy"
)

// Admission belongs to the outermost configuration transaction, before policy
// epochs/project locks and any nested Kubernetes draft preparation. Per-resource
// authorization remains the responsibility of the domain invoked within it.
func (s *Service) withWriteTx(ctx context.Context, fn func(configstate.DBTX) error) error {
	return configstate.WithWriteTx(configstate.WithWriteAdmission(ctx, tenancy.LockAuthorization), s.db, fn)
}
