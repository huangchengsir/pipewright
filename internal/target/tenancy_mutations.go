package target

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"strings"
)

func (s *service) mutate(ctx context.Context, fn func(context.Context) error) error {
	return configstate.WithWriteTxContext(ctx, s.db, func(txctx context.Context, q configstate.DBTX) error {
		if err := tenancy.LockAuthorization(txctx, s.db); err != nil {
			return err
		}
		if err := configstate.LockEpochs(txctx, q, s.db, configstate.EpochSecrets, configstate.EpochExecutor); err != nil {
			return err
		}
		if err := fn(txctx); err != nil {
			return err
		}
		return configstate.BumpEpoch(txctx, q, configstate.EpochExecutor)
	})
}
func (s *service) Create(ctx context.Context, in CreateInput) (result *Server, err error) {
	err = s.mutate(ctx, func(txctx context.Context) error {
		var e error
		result, e = s.create(txctx, in)
		if e != nil {
			return e
		}
		return s.approveMetrics(txctx, result.ID)
	})
	return
}
func (s *service) Update(ctx context.Context, id string, in UpdateInput) (result *Server, err error) {
	err = s.mutate(ctx, func(txctx context.Context) error {
		var e error
		result, e = s.update(txctx, id, in)
		if e != nil {
			return e
		}
		return s.approveMetrics(txctx, result.ID)
	})
	return
}
func (s *service) Delete(ctx context.Context, id string) error {
	return s.mutate(ctx, func(txctx context.Context) error {
		if e := s.delete(txctx, id); e != nil {
			return e
		}
		return tenancy.RevokeAutomation(txctx, s.db, "server_metrics", id)
	})
}

// Team execution is confined to its explicitly assigned server and credential.
// A root account always remains an instance-administrator operation.
func (s *service) checkExecutionIdentity(ctx context.Context, server *Server) error {
	if strings.EqualFold(strings.TrimSpace(server.User), "root") {
		if err := tenancy.RequireInstanceAdministrator(ctx, s.db); err != nil {
			return err
		}
	}
	if _, err := s.executionHostKey(ctx, server); err != nil {
		return err
	}
	return tenancy.CheckSameTeam(ctx, s.db, authz.ResourceRef{Kind: "server", ID: server.ID}, authz.ResourceRef{Kind: "credential", ID: server.CredentialID})
}
