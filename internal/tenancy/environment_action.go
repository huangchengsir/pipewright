package tenancy

import (
	"context"
	"database/sql"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/configstate"
)

// CheckEnvironmentAction resolves environment identity and classification from
// server-owned rows. A request can name an environment, never its purpose or
// tenant. Executions must additionally stay inside their sealed plan ceiling.
func CheckEnvironmentAction(ctx context.Context, db *sql.DB, action, kind, id, project, name string) error {
	if _, ok := ctx.Value(observationKey{}).(*observationPrincipal); ok {
		return ErrForbidden
	}
	q := configstate.Queryer(ctx, db)
	on, err := modeEnabled(ctx, q)
	if err != nil || !on {
		return err
	}
	definition, known := authz.Lookup(action)
	if !known || definition.ResourceKind != kind || !definition.EnvironmentRequired || project == "" || name == "" {
		return ErrForbidden
	}
	authority, request, err := authorityForRequest(ctx, db)
	if err != nil {
		return err
	}
	var environment, purpose string
	if err = q.QueryRowContext(ctx, `SELECT id,purpose FROM access_environments WHERE tenant_id=? AND project_id=? AND name=?`, request.teamID, project, name).Scan(&environment, &purpose); err != nil {
		return ErrForbidden
	}
	if request.serviceRunID != "" {
		return CheckRunPermission(ctx, db, action, id, environment)
	}
	if err = automationAllows(ctx, db, action, id, "", environment); err != nil {
		return err
	}
	if cap, ok := ctx.Value(mcpContextKey{}).(mcpContext); ok {
		_, _, token, e := mcpAuthority(ctx, db, cap)
		if e != nil || !token.Allows(project, true) {
			return ErrForbidden
		}
		r, e := resourceFrom(ctx, q, request.teamID, "project", project)
		if e != nil {
			return e
		}
		allowed, e := authority.Service.Allowed(authority.Principal, "mcp.issue", r)
		if e != nil || !allowed {
			return ErrForbidden
		}
	}

	resource, err := resourceFrom(ctx, q, request.teamID, kind, id)
	if err != nil {
		return err
	}
	if resource.ProjectID != "" && resource.ProjectID != project {
		return ErrForbidden
	}
	resource.ProjectID = project
	resource.EnvironmentID = environment
	resource.EnvironmentPurpose = purpose
	yes, err := authority.Service.Allowed(authority.Principal, action, resource)
	if err != nil {
		return err
	}
	if !yes {
		return ErrForbidden
	}
	return nil
}

// CheckSameTeam verifies reference ownership even for instance administrators:
// global administration does not create cross-team secret/resource bindings.
func CheckSameTeam(ctx context.Context, db *sql.DB, refs ...authz.ResourceRef) error {
	q := configstate.Queryer(ctx, db)
	on, err := modeEnabled(ctx, q)
	if err != nil || !on {
		return err
	}
	_, request, err := authorityForRequest(ctx, db)
	if err != nil {
		return err
	}
	for _, ref := range refs {
		if _, err = resourceFrom(ctx, q, request.teamID, ref.Kind, ref.ID); err != nil {
			return err
		}
	}
	return nil
}

// EnvironmentPredicate binds legacy project/name references to the immutable
// environment registry before domain queries page or aggregate their rows.
// Both column arguments are server-authored qualified identifiers, never SQL
// supplied by clients. An empty name column selects projects with a visible
// environment, rather than revealing unrelated projects through existence tests.
func EnvironmentPredicate(ctx context.Context, db *sql.DB, action, projectColumn, nameColumn string) (authz.Predicate, error) {
	definition, known := authz.Lookup(action)
	if !known || definition.ResourceKind != "environment" || !authz.ValidRootColumn(projectColumn) || (nameColumn != "" && !authz.ValidRootColumn(nameColumn)) {
		return authz.Predicate{}, ErrForbidden
	}
	predicate, err := QueryPredicate(ctx, db, action, "azenvref.id")
	if err != nil || predicate.SQL == "1=1" || predicate.SQL == "1=0" {
		return predicate, err
	}
	clause := "azenvref.project_id=" + projectColumn
	if nameColumn != "" {
		clause += " AND azenvref.name=" + nameColumn
	}
	predicate.SQL = "EXISTS (SELECT 1 FROM access_environments azenvref WHERE " + clause + " AND (" + predicate.SQL + "))"
	return predicate, nil
}

// CheckEnvironmentName is for actions whose resource is the environment itself.
// Name/purpose/ownership are resolved from persisted registry rows, not a DTO.
func CheckEnvironmentName(ctx context.Context, db *sql.DB, action, project, name string) error {
	q := configstate.Queryer(ctx, db)
	on, err := modeEnabled(ctx, q)
	if err != nil || !on {
		return err
	}
	_, request, err := authorityForRequest(ctx, db)
	if err != nil {
		return err
	}
	var id string
	if err = q.QueryRowContext(ctx, `SELECT id FROM access_environments WHERE tenant_id=? AND project_id=? AND name=?`, request.teamID, project, name).Scan(&id); err != nil {
		return ErrForbidden
	}
	definition, known := authz.Lookup(action)
	if !known || definition.ResourceKind != "environment" {
		return ErrForbidden
	}
	if !definition.EnvironmentRequired {
		return Check(ctx, db, action, "environment", id)
	}
	return CheckEnvironmentAction(ctx, db, action, "environment", id, project, name)
}

type deploymentReadKey struct{}
type deploymentRead struct {
	db            *sql.DB
	runID, action string
}

// DeploymentReadContext permits internal metadata reads for one already-authorized
// source run. It never grants the run menu or artifact download. Each subsequent
// SQL read rechecks the environment action, current session and policy revision.
func DeploymentReadContext(ctx context.Context, db *sql.DB, runID, action string) (context.Context, error) {
	if action != "deployment.execute" && action != "release.rollback" {
		return ctx, ErrForbidden
	}
	on, err := modeEnabled(ctx, configstate.Queryer(ctx, db))
	if err != nil || !on {
		return ctx, err
	}
	var project, name string
	if err = configstate.Queryer(ctx, db).QueryRowContext(ctx, `SELECT project_id,resolved_environment FROM pipeline_runs WHERE id=?`, runID).Scan(&project, &name); err != nil {
		return ctx, ErrForbidden
	}
	if err = CheckEnvironmentName(ctx, db, action, project, name); err != nil {
		return ctx, err
	}
	return context.WithValue(ctx, deploymentReadKey{}, deploymentRead{db: db, runID: runID, action: action}), nil
}
func deploymentReadPredicate(ctx context.Context, db *sql.DB, read deploymentRead, column string) (authz.Predicate, error) {
	if read.db != db || !authz.ValidRootColumn(column) {
		return authz.Predicate{}, ErrForbidden
	}
	predicate, err := EnvironmentPredicate(ctx, db, read.action, "azdr.project_id", "azdr.resolved_environment")
	if err != nil {
		return authz.Predicate{}, err
	}
	return authz.Predicate{SQL: column + "=? AND EXISTS (SELECT 1 FROM pipeline_runs azdr WHERE azdr.id=" + column + " AND " + predicate.SQL + ")", Args: append([]any{read.runID}, predicate.Args...)}, nil
}

// DeploymentMetadataPredicate is deliberately separate from QueryPredicate:
// only the run summary and artifact metadata adapters consume it. Logs, test
// reports, downloads and arbitrary run queries retain their ordinary actions.
func DeploymentMetadataPredicate(ctx context.Context, db *sql.DB, column string) (authz.Predicate, bool, error) {
	read, ok := ctx.Value(deploymentReadKey{}).(deploymentRead)
	if !ok {
		return authz.Predicate{}, false, nil
	}
	on, err := modeEnabled(ctx, configstate.Queryer(ctx, db))
	if err != nil {
		return authz.Predicate{}, true, err
	}
	if !on {
		return authz.Predicate{}, true, ErrForbidden
	}
	predicate, err := deploymentReadPredicate(ctx, db, read, column)
	return predicate, true, err
}
