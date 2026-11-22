package tenancy

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/configstate"
)

// DecisionQuery is a presentation query, never an authorization token. Mutation
// services still resolve the current principal and ownership independently.
type DecisionQuery struct {
	Action        string `json:"action"`
	ResourceID    string `json:"resourceId"`
	EnvironmentID string `json:"environmentId,omitempty"`
	Create        bool   `json:"create,omitempty"`
}
type DecisionBatch struct {
	UserID         string `json:"userId"`
	TeamID         string `json:"teamId"`
	ModeEpoch      uint64 `json:"modeEpoch"`
	Revision       uint64 `json:"revision"`
	CatalogVersion int    `json:"catalogVersion"`
	Allowed        []bool `json:"allowed"`
}

func (r *Repository) Decisions(ctx context.Context, actor Actor, team string, queries []DecisionQuery) (DecisionBatch, error) {
	out := DecisionBatch{UserID: actor.UserID, TeamID: team, CatalogVersion: authz.CatalogVersion, Allowed: make([]bool, len(queries))}
	if len(queries) == 0 || len(queries) > 64 {
		return DecisionBatch{}, ErrInvalidInput
	}
	err := configstate.WithReadTxContext(ctx, r.db, func(ctx context.Context, q configstate.DBTX) error {
		authority, err := loadAuthority(ctx, q, actor, team)
		if err != nil {
			return err
		}
		out.ModeEpoch = authority.Principal.ModeEpoch
		out.Revision = authority.Principal.Revision
		for i, input := range queries {
			if len(input.Action) > 80 || len(input.ResourceID) > 128 || len(input.EnvironmentID) > 128 {
				return ErrInvalidInput
			}
			action, known := authz.Lookup(input.Action)
			if !known {
				continue
			}
			var resource authz.Resource
			if input.Create && input.Action == "run.start" {
				// Issuing a trigger-capable token needs a real team-owned project and the
				// caller's synthetic new-run identity. This is not a deployment-plan approval.
				if input.ResourceID == "" || input.EnvironmentID != "" {
					continue
				}
				project, e := resourceFrom(ctx, q, team, "project", input.ResourceID)
				if e != nil {
					continue
				}
				resource = authz.Resource{TenantID: team, Kind: "run", ID: "new-run", ProjectID: project.ID, OwnerID: actor.UserID}
			} else if input.Create {
				// Only actual creation contracts can use synthetic resources. Editing an
				// existing object cannot bypass its ownership lookup by toggling create.
				switch input.Action {
				case "template.create", "variable_group.create", "custom_node.create", "project.create", "business_project.create", "product.create", "server.create", "credential.create", "kubernetes.create", "kubernetes_credential.create", "role.manage", "member.manage":
				default:
					continue
				}
				if input.ResourceID != "" || input.EnvironmentID != "" {
					continue
				}
				resource = authz.Resource{TenantID: team, Kind: action.ResourceKind, ID: "new-resource", OwnerID: actor.UserID}
			} else if action.InstanceOnly && action.ResourceKind == "instance" {
				if input.ResourceID != "instance" || input.EnvironmentID != "" {
					continue
				}
				resource = authz.Resource{TenantID: team, Kind: "instance", ID: "instance"}
			} else {
				resource, err = resourceFrom(ctx, q, team, action.ResourceKind, input.ResourceID)
				if err != nil {
					continue
				}
				// Approval presentation is bound to an actual pending gate, not an
				// arbitrary environment supplied by the browser.
				if input.Action == "release.approve" || input.Action == "run.approve" {
					present, e := PendingApprovalEnvironment(ctx, q, input.ResourceID, input.Action, input.EnvironmentID)
					if e != nil {
						return e
					}
					if !present {
						continue
					}
					if input.Action == "release.approve" {
						resource.EnvironmentID = input.EnvironmentID
					}
				}
				// Promotion selects a new target for the same source run. Its
				// environment is intentionally distinct from the source's old
				// deployment environment; the persisted registry still fixes
				// its project/team and the service enforces the next stage.
				if input.Action == "release.promote" && input.EnvironmentID != "" {
					resource.EnvironmentID = ""
				}
				// Never use arbitrary environment IDs to relabel a project-bound resource.
				if input.EnvironmentID != "" {
					if !action.EnvironmentRequired || (resource.EnvironmentID != "" && resource.EnvironmentID != input.EnvironmentID) {
						continue
					}
					// Only a Kubernetes connection chooses an environment at dispatch.
					// Runs and environment resources already have a stored identity.
					if resource.EnvironmentID == "" && action.ID != "kubernetes.deploy" && action.ID != "release.promote" {
						continue
					}
					var project, purpose string
					if err = q.QueryRowContext(ctx, `SELECT project_id,purpose FROM access_environments WHERE id=? AND tenant_id=?`, input.EnvironmentID, team).Scan(&project, &purpose); err != nil {
						continue
					}
					if resource.ProjectID != "" && resource.ProjectID != project {
						continue
					}
					resource.ProjectID = project
					resource.EnvironmentID = input.EnvironmentID
					resource.EnvironmentPurpose = purpose
				}
			}
			yes, e := authority.Service.Allowed(authority.Principal, input.Action, resource)
			if e != nil {
				return e
			}
			out.Allowed[i] = yes
		}
		return nil
	})
	return out, err
}
