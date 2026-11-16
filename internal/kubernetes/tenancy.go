package kubernetes

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/tenancy"
)

func (s *Service) authorizeGrant(ctx context.Context, id, action string) error {
	predicate, err := tenancy.QueryPredicate(ctx, s.db, action, "g.connection_id")
	if err != nil {
		return err
	}
	projectPredicate, err := tenancy.QueryPredicate(ctx, s.db, "project.read", "g.project_id")
	if err != nil {
		return err
	}
	var count int
	args := append([]any{id}, predicate.Args...)
	args = append(args, projectPredicate.Args...)
	if err = configstate.Queryer(ctx, s.db).QueryRowContext(ctx, `SELECT COUNT(*) FROM k8s_deploy_grants g WHERE g.id=? AND `+predicate.SQL+" AND "+projectPredicate.SQL, args...).Scan(&count); err != nil {
		return err
	}
	if count != 1 {
		return ErrNotFound
	}
	return nil
}

// Runtime admission is distinct from metadata visibility. Cluster credentials,
// executor, project and environment are independently and explicitly granted.
func (s *Service) authorizeTenantTarget(ctx context.Context, project, environment, connection, credential string, executor Executor) error {
	if executor.Kind == "local" {
		if err := tenancy.RequireInstanceAdministrator(ctx, s.db); err != nil {
			return err
		}
	}
	if err := tenancy.CheckSameTeam(ctx, s.db, authz.ResourceRef{Kind: "project", ID: project}, authz.ResourceRef{Kind: "kubernetes", ID: connection}, authz.ResourceRef{Kind: "kubernetes_credential", ID: credential}); err != nil {
		return err
	}
	if err := tenancy.Check(ctx, s.db, "kubernetes.use", "kubernetes", connection); err != nil {
		return err
	}
	if err := tenancy.Check(ctx, s.db, "kubernetes_credential.use", "kubernetes_credential", credential); err != nil {
		return err
	}
	if executor.Kind == "ssh" {
		if err := tenancy.Check(ctx, s.db, "server.use", "server", executor.ServerID); err != nil {
			return err
		}
	}
	return tenancy.CheckEnvironmentAction(ctx, s.db, "kubernetes.deploy", "kubernetes", connection, project, environment)
}
