package configdraft

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/tenancy"
)

func (s *Service) currentToken(ctx context.Context, q configstate.DBTX, t *auth.MCPToken) error {
	ctx = configstate.WithTxContext(ctx, s.db, q)
	if err := tenancy.AuthorizeMCPReference(ctx, s.db, t); err != nil {
		return err
	}
	return tenancy.MCPAuthorization(s.db).Verify(ctx, q, t)
}
func (s *Service) configurationRead(ctx context.Context, q configstate.DBTX, project, section string) error {
	ctx = configstate.WithTxContext(ctx, s.db, q)
	action := "pipeline.read"
	if section == "project" {
		action = "project.read"
	}
	return tenancy.Check(ctx, s.db, action, "project", project)
}
func (s *Service) configurationWrite(ctx context.Context, q configstate.DBTX, project string, ops []Operation) error {
	ctx = configstate.WithTxContext(ctx, s.db, q)
	for _, op := range ops {
		action := "pipeline.edit"
		if sectionFor(op.Type) == "project" {
			action = "project.edit"
		}
		if err := tenancy.Check(ctx, s.db, action, "project", project); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) configurationApproval(ctx context.Context, q configstate.DBTX, subject, project string, ops []Operation) error {
	ctx = configstate.WithTxContext(ctx, s.db, q)
	on, err := tenancy.Enabled(ctx, s.db)
	if err != nil || !on {
		return err
	}
	approved, err := tenancy.ApprovalSubjectContext(ctx, s.db, subject, project)
	if err != nil {
		return err
	}
	if err = tenancy.Check(approved, s.db, "mcp.approve", "project", project); err != nil {
		return err
	}
	return s.configurationWrite(approved, q, project, ops)
}
