package tenancy

import (
	"context"
	"database/sql"
	"errors"
	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/configstate"
)

// ApproveProjectAutomation freezes the saving employee's explicit execution
// resource ceiling. The run compiler later selects only the resolved plan's
// dependencies. New grants/resources never expand an existing approval.
// Call after the source mutation, inside the same write transaction.
func ApproveProjectAutomation(ctx context.Context, db *sql.DB, kind, id, project, digest string, sourceProject ...string) error {
	q := configstate.Queryer(ctx, db)
	on, err := modeEnabled(ctx, q)
	if err != nil || !on {
		return err
	}
	if q == db {
		return ErrConflict
	}
	if kind != "preview" {
		if err = CheckRunAdmission(ctx, db, project, uuid.NewString()); err != nil {
			return err
		}
	}
	authority, request, err := authorityForRequest(ctx, db)
	if err != nil {
		return err
	}
	permissions := []AutomationPermission{{Action: "run.start", ProjectID: project}}
	if kind == "preview" {
		if err = Check(ctx, db, "preview.provision", "project", project); err != nil {
			return err
		}
		permissions = []AutomationPermission{{Action: "preview.provision", ResourceID: project}, {Action: "preview.read", ProjectID: project}, {Action: "preview.reclaim", ProjectID: project}}
	}

	// Configuration reads are limited to this project; ordinary pipeline edits
	// do not grant any execution credential or execution node implicitly.
	for _, action := range []string{"project.read", "pipeline.read", "pipeline.edit", "preview.provision"} {
		if kind == "preview" && action == "pipeline.edit" {
			continue
		}
		if err = Check(ctx, db, action, "project", project); err == nil {
			permissions = append(permissions, AutomationPermission{Action: action, ResourceID: project})
		}
	}
	rows, err := q.QueryContext(ctx, `SELECT resource_kind,resource_id FROM access_ownership WHERE tenant_id=? AND (project_id IS NULL OR project_id=?) ORDER BY resource_kind,resource_id`, request.teamID, project)
	if err != nil {
		return err
	}
	refs := []authz.ResourceRef{}
	for rows.Next() {
		var ref authz.ResourceRef
		if err = rows.Scan(&ref.Kind, &ref.ID); err != nil {
			rows.Close()
			return err
		}
		refs = append(refs, ref)
		if len(refs) > 2048 {
			rows.Close()
			return ErrInvalidInput
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	type environment struct{ id, purpose string }
	envs := []environment{}
	erows, err := q.QueryContext(ctx, `SELECT id,purpose FROM access_environments WHERE tenant_id=? AND project_id=?`, request.teamID, project)
	if err != nil {
		return err
	}
	for erows.Next() {
		var e environment
		if err = erows.Scan(&e.id, &e.purpose); err != nil {
			erows.Close()
			return err
		}
		envs = append(envs, e)
		if len(envs) > 128 {
			erows.Close()
			return ErrInvalidInput
		}
	}
	err = erows.Err()
	erows.Close()
	if err != nil {
		return err
	}
	for _, action := range []string{"credential.use", "server.use", "kubernetes.use", "kubernetes_credential.use", "kubernetes.deploy", "deployment.execute", "notification.send", "dns.use", "proxy.manage", "proxy.install", "preview.read", "preview.reclaim"} {
		definition, known := authz.Lookup(action)
		if !known {
			return ErrForbidden
		}
		for _, ref := range refs {
			if ref.Kind != definition.ResourceKind {
				continue
			}
			// Ownership metadata alone is not a usable execution resource. Deleted
			// or dangling records cannot enter a newly approved resource ceiling.
			if _, e := automationResourceVersion(ctx, q, ref.Kind, ref.ID); errors.Is(e, ErrForbidden) {
				continue
			} else if e != nil {
				return e
			}
			candidates := []string{""}
			if definition.EnvironmentRequired {
				candidates = nil
				for _, e := range envs {
					candidates = append(candidates, e.id)
				}
			}
			for _, env := range candidates {
				permission := AutomationPermission{Action: action, ResourceID: ref.ID, EnvironmentID: env}
				resource, e := automationResource(ctx, q, request.teamID, project, request.actor.UserID, permission)
				if e != nil {
					continue
				}
				allowed, e := authority.Service.Allowed(authority.Principal, action, resource)
				if e != nil {
					return e
				}
				if !allowed {
					continue
				}
				permissions = append(permissions, permission)
				if len(permissions) > 256 {
					return ErrInvalidInput
				}
			}
		}
	}
	if len(sourceProject) > 1 {
		return ErrInvalidInput
	}
	if len(sourceProject) == 1 {
		if err = Check(ctx, db, "pipeline.read", "project", sourceProject[0]); err != nil {
			return err
		}
		ctx = context.WithValue(ctx, automationSourceProjectKey{}, sourceProject[0])
	}
	return ApproveAutomation(ctx, db, kind, id, project, digest, permissions)
}
